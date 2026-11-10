# Cloudflare (DNS provider)

The DNS driver. Proxier creates the A records of servers it provisions, waits for them to resolve, and deletes them at retirement. It never touches records it didn't create.

## Setup

- Settings → Integrations → Cloudflare: an **API token** with `Zone → DNS → Edit` (and `Zone → Zone → Read`) for the zones Proxier may use, e.g. `example.com`. Saving the token first verifies it and lists the zones with it; if Cloudflare refuses, nothing is saved ("Cloudflare refused this token"). **Test** lists the zones the token can see again. A member with `settings: manage` ticks the ones Proxier may use (`cloudflare.zones`, comma-separated names; a name the token does not see is dropped).
- The hostname pattern (Settings → Servers) must end in one of those zones. Proxier picks the zone by the longest matching suffix. Settings → Servers warns when the pattern is not set, or when its zone is not ticked.
- A hostname typed in the new-server form that ends in none of those zones, or any typed hostname while no token is saved, gets a record made by hand at the member's own DNS provider. Proxier only checks that record ([provisioning](../processes/servers/server-provisioning.md)).

## Records

| Field | Value |
|---|---|
| Type | `A` (IPv4 only) |
| Name | the management hostname (and the proxy hostname, once different) |
| Content | the server's IP |
| Proxied | **false, always.** Clients connect straight to nginx with the server's own certificate, and Let's Encrypt must reach port 80. A proxied record would break both. |
| TTL | 60 s |
| Comment | `proxier:<server name>`. This marks the record as Proxier's. |

## Behaviour

- **Create**: if a record already exists at the name, it is updated only if it is the only one and its comment is Proxier's for the same server (a retry; a record someone proxied or gave another TTL is put back to DNS-only, 60 s). Otherwise provisioning stops with a conflict that shows the existing value (several records: all their values) and its comment, and **Overwrite** is a member's explicit choice ([provisioning](../processes/servers/server-provisioning.md)): the first record is updated in place and given Proxier's comment, and any others at the name are deleted so the name leads to one IP.
- **Wait**: after writing, Proxier first asks the zone's own nameservers until they serve the record, then the public resolvers.
  - Proxier finds the nameservers by asking the public resolvers over DoH for the zone's NS records and their addresses. It asks them about the zone's apex and the nameservers' names only, never about the name it waits for. It then asks every IPv4 address of every nameserver over port 53 (UDP, then TCP when UDP gets no answer), without recursion, every 2 s, until all answer with the IP and the authoritative (AA) flag. The zone's SOA, asked the same way, gives the negative TTL: its MINIMUM, capped by the SOA record's TTL (RFC 2308); 30 min, Cloudflare's, when it can't be read.
  - Cloudflare's API returns before its nameservers serve a new record. A resolver asked in that gap gets NXDOMAIN and may keep it for the negative TTL (1800 s on a Cloudflare zone), longer than the wait. Asked from home 0.36 s after the record was made, 1.1.1.1 answered nothing for the next 10 min.
  - Port 53 to Cloudflare's nameservers works from home, over UDP and TCP, with the AA flag. If something drops it, or answers in the nameservers' place (no AA flag), the log says so and the wait pauses 10 s before the first public question instead.
  - Proxier then queries Cloudflare at `1.1.1.1` and Google at `8.8.8.8` every 10 seconds until both return the IP or 10 minutes pass. Requests use DNS-over-HTTPS at each IP's `/dns-query` endpoint, avoiding local DNS resolution and routers that intercept port 53. If DoH fails at the network or HTTP level, Proxier tries UDP port 53 and logs the answering transport.

    NXDOMAIN is a negative answer, not a transport failure. If one resolver still returns no address after 10 minutes while every other resolver has the IP, Proxier waits until the authoritative negative TTL plus 1 minute has elapsed since the nameservers first served the record. The log explains this extension.

    Other wrong answers fail after 10 minutes. On failure, the error lists each resolver's answer and identifies an empty answer as a probable cached negative response. Cancellation ends the wait immediately.
- **Delete** at retirement: only a record with Proxier's comment for that server that still points to its IP; otherwise it is left and the reason shows ("points to 198.51.100.7", "comment changed"). A record that is already gone counts as removed.
- The Cloudflare record ID is stored with the server's DNS record, so later changes don't search by name.

## Errors

| Cloudflare says | Proxier does |
|---|---|
| `401` / `403` | The step fails with "Cloudflare token rejected or lacks DNS edit on <zone>". Settings shows the token as failing. |
| Zone not found | The form refuses the server before anything starts (the hostname isn't in an allowed zone). |
| `429` | Waits what `Retry-After` asks (at most 60 s; absent or larger means 10 s) and retries within the call, up to 5 times. |
| `5xx` or a network error | Retries 3 times within the call, after 1 s, 3 s and 9 s, then fails the step. **Retry** resumes here. |

Every call has a 15 s timeout, and the token never appears in an error or a log. The settings are `cloudflare.api_token` (a secret, sealed with the vault) and `cloudflare.zones`; the driver reads both on every call, so a change applies at once.
