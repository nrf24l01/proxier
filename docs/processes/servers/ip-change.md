# IP change

**Actors**: a member with `servers: manage`; the Change IP job (queue `provisioning`); the server over SSH; the proxy test client at home; Cloudflare; the previous IP tests (queue `checks`); the notifier.

An IP change moves an active server to another IP of the same VPS, usually because Russia blocked the one it had. The server keeps its name, hostnames, credentials and stack. Connection URIs name the hostname, so links keep working. Once the record points at the new IP, apps reach it on their next connection, with nothing to refresh. The IP the server leaves can stay as a **previous IP**. Proxier keeps proxy-testing it from home and says when it works again, so the server can switch back.

Moving to another VPS, with a new IP and a fresh machine, is not an IP change. It is still on the [backlog](../../overview.md#backlog).

## Steps: at the hosting provider

Proxier has no provider APIs. Before **Change IP** the member:

1. Adds the IP to the VPS at the provider.
2. Makes sure the VPS answers on it, while keeping the current IP. Some providers configure an added IP by themselves. Others need it added to the network interface (a second address in netplan or `/etc/network/interfaces`). The stack listens on every address, so nothing in it changes.

The dialog lists both steps.

## Steps: the dialog

1. Server page → Actions → **Change IP** (active servers only). The dialog shows the two provider steps, what the job will do, the DNS records it will repoint (`nl-1.hosts.tikhonnnnn.com → 201.58.181.185`), and a band when the template's stack uses `.Server.IP` (a redeploy is needed after the change).
2. Fields:
   - **New IP**, an IPv4 address. The field suggests the server's previous IPs.
   - **Keep the old IP as a previous IP and test it from home**, checked. Unchecked when the provider takes the IP away.
   - **Switch even if the new IP fails the proxy test from home**, unchecked. Use it for a server meant for people abroad.
3. **Change IP** checks the form and queues the job. Nothing is written yet. Refused, with nothing queued:
   - not an IPv4 address;
   - the server's own IP ("nl-1 has this IP already.");
   - another non-retired server's IP, or one it keeps as a previous IP.
4. The browser goes to the server page, which shows the job's steps and log.

## Steps: the job

The job holds `server:<id>`, so no other job changes the server meanwhile. Until step 3 the server stays at its IP, in Proxier and in DNS.

1. **Reach the new IP.** Connect over SSH as `proxier` with Proxier's key, at the new IP and the server's SSH port. When that address has no pinned host key yet, its key must be the one pinned for the server's current address, which proves it is the same machine. Only then does Proxier pin the key for the new address ([ADR 0007](../../adr/0007-agentless-ssh-with-pinned-host-keys.md)). Another key fails the job ("it is another machine") and pins nothing. No answer fails it too, asking whether the IP is set up on the VPS.
2. **Proxy test.** The proxy test from home through every endpoint, aimed at the new IP: the hostname stays in TLS and HTTP, as a client's will once DNS leads there. Up to 3 attempts, 20 s apart, like the smoke test. A failure stops the job ("failed from home too … nothing was changed"), unless the member chose to switch anyway; then it logs a warning and goes on.
3. **Repoint DNS.** Bring each record Proxier made for the server (the management hostname's, and the proxy hostname's once they differ) to the new IP, keeping its comment, DNS-only and TTL 60. A record at the name that Proxier did not make stops the job, with nothing changed. Then, in one transaction:
   - store the record's new content and the server's new IP, and the time of the change;
   - when the new IP was a previous IP, drop it from the list, since the server switched back;
   - when **Keep** was checked, keep the IP it left as a previous IP, untested so far;
   - set the health state to `unknown` with the reason "The IP changed: waiting for its first checks" (a paused server stays paused) → `server.health_changed`;
   - → `server.ip_changed{from, to, kept, back, forced}`.

   When **Keep** was off, the host key pinned at the old address is forgotten, so another server built there later is a first contact, not a changed key. The log says when the stack's files use the IP: **Redeploy** brings them to the new one.
4. **Wait for resolvers.** Wait until 1.1.1.1 and 8.8.8.8 show the new IP (as in provisioning, up to 10 min). The change is already made, so a record that is slow to show is a warning, not a failure.
5. The job ends. In the transaction that marks it succeeded, it queues the checks of the new IP. The self-check runs at once. The proxy test, a test of the previous IPs and an on-demand external check (within its budget) run 90 s later, beyond the records' 60 s TTL, so home's resolver no longer answers with the old IP. The first verdict after `unknown` applies at once ([health](./server-health.md)).

A failed or cancelled job → `server.ip_change_failed{to, step, error, cancelled}`, which notifies unless cancelled. The job makes one attempt; the member runs **Change IP** again. A run after a failure at step 4 or later finds the server at the new IP already.

## Steps: previous IPs

1. Every 30 minutes (Settings → Servers, **Previous IPs tested every**), a round queues a test of each active, unpaused, not retiring server that keeps a previous IP. An IP change queues one too (above).
2. The test runs the proxy test from home through every endpoint of the server, aimed at each previous IP in turn. A previous IP **works** when every endpoint passes, and **fails** otherwise, with the first failing endpoint's class and error. Like the proxy test, it stores nothing while another job is changing the server.
3. While home is not `online`, results are stored marked inconclusive and change nothing.
4. A previous IP starts as **untested**, and its first conclusive result applies at once. After that its state changes only when as many results in a row agree as a server's health needs (**Flap confirmations**, 2 by default). Each change → `server.previous_ip_changed{ip, from, to, error}`. Only `fails` → `works` notifies: "🟢 nl-1: previous IP 201.58.181.185 works again".
5. The Health tab lists the previous IPs: the IP and until when it was the server's, its state and since when, the latest result, and a 24 h strip. Per IP:
   - **Switch back** opens Change IP with that IP filled in. The same job runs, and the IP the server leaves becomes a previous IP when **Keep** stays checked.
   - **Forget** (confirmed) stops testing it, deletes its results and forgets its pinned host key. The IP is then free for another server. → `server.previous_ip_forgotten{ip}`

## Rules

- A server has one IP. SSH, the self-check, the external checks and DNS all use it. Previous IPs get the proxy test from home, and nothing else (no SSH, no check-host budget).
- No two non-retired servers share an IP, as their IP or a previous one, because one server's checks would reach the other. The new-server form refuses such an IP too ("nl-1 keeps this IP as a previous IP. Forget it there first.").
- Nothing changes until the new IP shows the server's host key and, unless forced, passes the proxy test from home.
- Checks from before an IP change are about the old IP. The verdict ignores results older than the change, so stale external answers or a blocked proxy test can't decide the new IP's state.
- An IP change never touches subscriptions, links, credentials or the stack. Templates whose stack uses `.Server.IP` need a redeploy afterwards; the dialog and the log say so.
- Retirement drops a server's previous IPs with their results. Their host keys go with the server's other pins.
- The SSH port, the country and the name still can't change. The name is the identity.

## Edge cases (each is a test)

- Change a blocked server to a new IP of the same VPS, keeping the old one → the server and its Cloudflare record are at the new IP, the old IP is a previous IP that the first test finds failing (quietly), health goes `blocked` → `unknown` → `healthy`, both addresses are pinned, the endpoints (and so the connection URIs) are unchanged (`TestChangeMovesTheServerAndKeepsTheOldIP`).
- Another machine answers SSH at the new IP → fails at **Reach the new IP**, "another machine"; nothing changed, its key not pinned, `server.ip_change_failed` (`TestChangeToAnotherMachineChangesNothing`).
- No SSH at the new IP → fails at **Reach the new IP**, asking whether the IP is set up on the VPS; nothing changed (`TestChangeToAnIPWithoutSSHChangesNothing`).
- The new IP fails the proxy test from home → fails at **Proxy test**, nothing changed; with **Switch anyway** → the server moves, the log says so, and the event records `forced` (`TestChangeToABlockedIPChangesNothingUnlessForced`).
- **Keep** unchecked → no previous IP, and the old address's host key is forgotten (`TestChangeWithoutKeepingForgetsTheOldIP`).
- Switch back to a previous IP → the server is at it again, it leaves the list, the IP left becomes a previous IP, `back: true` (`TestSwitchBackToAPreviousIP`).
- The form refuses an empty IP, an IPv6 address, a name, the server's own IP, another server's IP and another server's previous IP, and queues nothing (`TestChangeFormRefusals`).
- The record at the hostname no longer carries Proxier's comment → fails at **Repoint DNS**, nothing changed (`TestConflictingDNSRecordChangesNothing`).
- **Forget** → the previous IP, its results and its host key are gone, a second Forget is not found, and a new server can be built at that IP (`TestForgetAPreviousIP`).
- A template whose stack uses `.Server.IP` → the dialog's band and the job's log ask for a redeploy; the seed template doesn't (`TestStackThatNamesTheIPAsksForARedeploy`, `TestChangeIPPages`).
- A previous IP that was blocked when it was left → `fails` at once, with the error, and no notification (`TestBlockedPreviousIPFailsAtOnceQuietly`).
- A failing previous IP passes once → still `fails` with one confirmation; passes again → `works` and one notification; one failure after that is flap (`TestPreviousIPThatWorksAgainNotifiesAfterConfirmation`).
- Home offline during a previous IP test → the result is inconclusive and the state stays (`TestPreviousIPResultIsInconclusiveWhileHomeIsOffline`).
- A paused server → its previous IPs aren't tested, and the round leaves it out (`TestPausedServerKeepsItsPreviousIPsUntested`).
- A healthy server's IP changes → `unknown`, and an evaluation with only the results from before the change stays `unknown`; the new IP's first passing round → `healthy` (`TestResultsFromBeforeAnIPChangeDoNotCount`).
- An IP a server keeps as a previous IP on the new-server form → refused, naming the server (`TestIPKeptAsPreviousIsRefused`).
- Retire a server with a previous IP → the previous IP is gone and free for a new server (`TestRetireDropsPreviousIPs`).
- The dialog, a refusal, the change, the previous IP on the Overview and Health tab with **Switch back** and **Forget** (`TestChangeIPPages`).
