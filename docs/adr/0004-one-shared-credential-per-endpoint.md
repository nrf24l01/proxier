# One shared credential per endpoint; links control updates, rotation controls access

Every link that includes a server returns the same VLESS UUID for each endpoint. Proxier does not add or remove clients on servers for individual links.

Disabling, expiring or deleting a link stops its updates. The link returns one stub entry with HTTP `200`, so apps can replace their cached server list. An error response would let apps keep the old list. Copied connection URIs remain usable until the server's credentials rotate.

**Cut-off** disables a link and can rotate the credentials of every server in its subscription. Rotation is off by default because it affects every link using those servers. Shared credentials avoid per-link sync jobs and let servers keep working when Proxier is unavailable.

## Considered options

- **Per-link credentials** (a UUID per link per server, kept in sync by Proxier): real revocation per link, per-link traffic stats and quotas. It costs a sync job per change and per server, and a server unreachable during a change means drift.
- **Per-subscription credentials**: a middle ground, with the same costs at a smaller scale.

## Consequences

- Copied connection URIs keep working until rotation. The UI says this wherever a link is disabled or deleted.
- Shared-link alerts can only use fetches (IPs, apps), never actual connections.
- Rotation changes the URIs for everyone on the server. Apps that refresh carry on without noticing.
- Templates are written against a client list (`.Clients`), so moving to per-link credentials later is mostly a sync job plus data.
