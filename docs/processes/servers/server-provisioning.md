# Server provisioning

**Actors**: a member with `servers: manage`; the provisioning job (queue `provisioning`); the new VPS; Cloudflare, or the member's own DNS provider for a hostname outside the Cloudflare zones; Let's Encrypt (through the template's steps); the proxy test client.

Provisioning turns a freshly issued VPS (an IP and a root password) or an existing SSH account that already trusts Proxier's key into an **active** server built from a template version. The member fills in one form and watches the job. There is nothing to type over SSH. There is nothing to do in DNS either, unless the member types a hostname that none of the Cloudflare zones covers. A server only becomes active once a real proxy connection from home works through it.

## Steps: the form

1. Servers → **New server**. Fields:
   - **IP address** (IPv4) and **SSH port** (default 22, under "advanced").
   - **How Proxier signs in**, either:
     - **Root password**, used once to create the deploy user and install Proxier's key, then erased; never shown again; or
     - **SSH key**: the member has already put Proxier's public key (Settings → SSH) into the **SSH account**'s `authorized_keys`. The account defaults to `root`; any other account needs passwordless sudo. Nothing is created on the machine for access.
     Both are picked per server; Settings → Servers chooses what the form starts with.
   - **Server setup**, either **Full** (hardens SSH, resets the firewall, turns on TCP BBR, installs the prerequisites and Docker) or **Deploy only** (installs the prerequisites and Docker if missing, and leaves SSH, the firewall and the kernel alone). Picked per server; Settings → Servers chooses the default.
   - **Country**, a searchable picker: typing filters the list of every country by name (in the member's language) or code, and the field takes "🇳🇱 Netherlands · NL"; a code (`nl`) or a name in English or Russian typed in full counts too. It is preselected from the IP's country. The country's lower-case code is the first half of the server name ([ADR 0015](../../adr/0015-servers-have-a-country-not-a-location.md)).
   - **Hostname**, optional: the management and proxy hostname, e.g. `vpn.example.com`. Left empty, the server gets the name the hostname pattern gives (`nl-2.hosts.example.com`). Proxier trims it, writes it in lower case and drops a trailing dot. When none of the Cloudflare zones covers it, the member creates its A record by hand at their DNS provider (a **record made by hand**).
   - **Template** and **version** (default version preselected), then the version's **parameters** as form fields with labels, help and defaults.
   - **Notes**.
2. While the member types, the form shows what will be created: the server name (`nl-2`), the management and proxy hostnames, the DNS record, and the endpoints with their display names. A picker the member hasn't chosen in (empty with nothing suggested yet, or showing what the IP suggested last) follows the IP's country as the summary updates; anything else in it (a half-typed name, an emptied field, a country they picked) is theirs, and the summary never replaces it. Until a country is picked the summary shows no name, and text that names no country is refused only on **Create**. For a record made by hand, the summary shows the record to create (`A vpn.example.org → 203.0.113.10`, not proxied, short TTL) and a **Check the record** button. The summary asks no DNS server about the name until the member presses it.
3. **Create**. Proxier checks:
   - no non-retired server has this IP, as its IP or as a previous IP it keeps ([IP change](./ip-change.md));
   - the country is a country (an ISO code in use today), and the template version exists and the template isn't archived;
   - parameters pass validation;
   - the hostname pattern in Settings → Servers is set, unless the member typed a hostname;
   - a typed hostname is a full DNS name: two labels or more, letters, digits and hyphens, no wildcard;
   - no non-retired server has the hostname, as its management or proxy hostname. Create checks it again in its transaction, so two servers created at once never share one;
   - for the pattern's name: Cloudflare is configured and a configured zone covers it;
   - for a record made by hand (a typed hostname that no zone covers, or any typed hostname while Cloudflare is not configured): the name points to the IP and to nothing else. Proxier finds the zone from the name's parents (`example.org` for `vpn.example.org`) and asks its own nameservers, which cache nothing, so a check made before the record exists does not delay it later. When the nameservers can't be asked, it asks 1.1.1.1 and 8.8.8.8 instead. **Check the record** runs the same check;
   - no routing list covers the hostname (when the routing module is enabled): a list holding `example.com`, or the hostname itself, would send Proxier's checks of the server into the router's tunnel. Any name listed in any list counts, even one the list doesn't install. The live summary says it too: "nl-3.hosts.example.com is covered by example.com (my-sites) in Main: Proxier's checks would go into the tunnel. Take it out of the list first."

   With key access, the SSH account must be a plain account name (lower-case letters, digits, `_` and `-`) and the root password field is ignored. In password access it must be left alone: the server gets the `proxier` account provisioning creates. Full setup turns root login off, so a key-access server whose account is `root` is refused: choose Deploy only, or another account.

   Failures are shown on the form and nothing is created.
4. The server is created in **provisioning** with its name reserved and the access and setup choices stored on it. In password access the root password goes encrypted into the job payload; in key access the payload holds no secret. The job is queued. The browser goes to the server page, which shows the steps and the live log. → `server.created{ip, country, hostname, manual_dns, template_version, access, setup}`

## Steps: the job

Each step is resumable. A retried job starts at the step that didn't finish.

1. **Preflight.**
   - Connect to `IP:port`: as root with the password, or as the SSH account with Proxier's key. The host key is pinned now ([ADR 0007](../../adr/0007-agentless-ssh-with-pinned-host-keys.md)).
   - With key access, prove root commands work (`sudo -n true`, or nothing for root) before changing anything: a key that logs in without sudo fails here.
   - Read `/etc/os-release` and the architecture, and compare them with the template's `requires`.
   - Check that the manifest's TCP ports (e.g. 80, 443) are free (`ss -ltn`).
2. **Install access.** With key access this step changes nothing: the account already trusts Proxier's key, and every later connection uses it.
   With password access, Proxier never works as root after this step. It uses a deploy user called **`proxier`**.
   - Install `sudo` if the image lacks it, then create the user `proxier` if it doesn't exist: a home directory, a login shell and a locked password.
   - Give it passwordless sudo through `/etc/sudoers.d/proxier` (`proxier ALL=(ALL) NOPASSWD:ALL`). Check the file with `visudo -cf` before moving it into place.
   - Append Proxier's public key and your personal keys to `~proxier/.ssh/authorized_keys`, skipping keys already there. The directory gets mode `700` and the file `600`, owned by `proxier`.
   - Open a **new** connection as `proxier` with Proxier's key, and run `sudo -n true`.
   - Once both work, erase the password from the job payload. From here on, nothing can use or show it.
3. **Base bootstrap** (the template's `base-bootstrap` step). Commands that need root run through `sudo -n`, or directly when the login account is root.
   - **Full setup**: SSH hardening, `.hushlogin`, the apt prerequisites, TCP BBR, Docker and the firewall, as below.
   - **Deploy only**: the apt prerequisites and Docker if missing; nothing else.
   - SSH hardening: `PasswordAuthentication no`, `KbdInteractiveAuthentication no`, `PermitRootLogin no`, via a drop-in file. sshd is reloaded, then key login as the account and `sudo -n` are verified again. If verification fails, the change is reverted and the step fails.
   - `.hushlogin` and the apt prerequisites.
   - TCP BBR: `net.ipv4.tcp_congestion_control = bbr` (and the `fq` qdisc, best effort), kept across reboots in `/etc/sysctl.d/90-proxier-bbr.conf`. The server sends what clients download through it, over long lossy paths, where BBR keeps the throughput CUBIC halves on every loss. It comes before Docker, because a container's network namespace starts with the host's default. A kernel without BBR (OpenVZ, LXC) gets a warning, "TCP stays on cubic", no file, and provisioning goes on.
   - Docker via get.docker.com unless Docker is already installed.
   - Firewall: ufw is reset, then denies every incoming connection but the SSH port's and the manifest's ports, and is turned on. Rules the image came with go, so the rule set is exactly what Proxier means; no template rule is lost, because the template's own steps come later.
4. **DNS.** Create the A record(s) for the hostname(s): DNS-only, TTL 60, comment `proxier:nl-2`. Then wait for the name to be visible ([Cloudflare](../../integrations/cloudflare.md#behaviour)):
   - First ask the zone's own nameservers (`gabe.ns.cloudflare.com` and the rest, port 53, without recursion) every 2 s until every one of them answers with the IP. A resolver asked before then gets "no such name" and may keep that answer for the zone's negative TTL (the SOA's minimum: 30 min at Cloudflare). If the nameservers can't be asked (port 53 dropped, an answer without the authoritative flag, no nameservers found), the log says why and the wait pauses 10 s instead.
   - Then wait until 1.1.1.1 and 8.8.8.8 both answer with the IP (timeout 10 min). A resolver that still answers nothing at 10 min, while the other shows the IP, probably holds a negative answer from before the record existed; the log says so, and it gets until the zone's negative TTL plus a minute has passed since the nameservers had the record.
   - A record made by hand: Proxier writes nothing. The log says the record is made by hand, and the step waits for it the same way, with the zone found from the name's parents. When it doesn't show, the step fails and names the record to create.
5. **Generated values.** Create every generated value the version declares (encrypted).
6. **Render and upload.** Render the version for this server and upload the files to the manifest's `dir` (`upload-files`). Record the deployment and its rendered files.
7. **Template install steps**, in order (in the seed template: issue the certificate, `compose up --pull`, wait for the decoy site to answer locally).
8. **Endpoints.** Render and store the endpoints and their display names.
9. **Self-check.** Run the template's checks once. All must pass.
10. **Smoke test.** Run the proxy test from home through every endpoint (up to 3 attempts, 20 s apart). Every endpoint must pass.
11. **Activate.** The server becomes **active**, its health state becomes `unknown`, and the scheduler starts its checks. Subscriptions with "add new servers automatically" append it. → `server.activated{proxy_test}` (notifies "nl-2 is ready")

The job makes one attempt and never retries by itself: a retry may need input, so it is a member's. Any step failing stops the job. The server becomes **failed**, and the page shows the step, the error and the log. → `server.provisioning_failed{step, error}` (notifies). **Cancelling** the job ends the same way with the error `cancelled` and `cancelled: true` in the event, which does not notify; DNS records already made stay until retry or retirement.

## Steps: after a failure

1. **Retry** resumes at the failed step.
   - If the server uses password access and the failed step was preflight or install access (before Proxier's key works), it **always** asks for the root password again; the new one replaces any kept one. Later failures ask for nothing, and a key-access server never asks.
   - If the template's default version moved on in the meantime, it still uses the version the server was created with.
2. **Activate anyway** is offered only when every step up to the self-check passed and only the smoke test failed. A typical case is a VPS whose hosting network is already blocked in Russia, which can still be useful to link holders abroad. The dialog shows the proxy test's error and asks for a confirmation.
   - The server becomes **active** and the scheduler starts its checks. Its health state comes from the first check round, usually `blocked` or `down`, never `healthy` by default.
   - Subscriptions with "add new servers automatically" append it. Those that hide unhealthy servers keep it hidden until it is healthy. → `server.activated{proxy_test, forced: true}` (notifies "nl-2 is active without a passing proxy test")
3. **Retire** cleans up whatever the job had created: the DNS records it made, and the stack if it was uploaded and the server is reachable ([retirement](./server-retirement.md)).
4. Upgrading or changing parameters isn't possible on a failed server. Retire it and create a new one.

## Rules

- The name is reserved at **Create** and never reused, even if provisioning fails and the server is retired.
- The root password exists only encrypted in the job payload, and only until Proxier's key login works (step 2). It never appears in logs, events or the UI. A provisioning job that fails before step 2 keeps it, encrypted, until it is retried, cancelled or the server is retired. A key-access server has no root password at all: its job payload holds no secret.
- Access and setup are the server's and stay so: a redeploy, a health check, an IP change and retirement connect the same way, and never re-run the base setup. With key access they use the account stored at creation; with password access they use `proxier` and its key.
- Every remote change is verified before the next step depends on it: key login after installing keys, key login after the sshd change, DNS answers before certificate issuance.
- Proxier never writes, changes or deletes a record made by hand. [IP change](./ip-change.md) checks that the member pointed it at the new IP, and [retirement](./server-retirement.md) leaves it and says to delete it at the DNS provider.
- Provisioning never overwrites a DNS record that Proxier didn't create. A conflicting record fails step 4 with the record's current value. **Retry** then offers **Overwrite** as an explicit choice.
- At most two provisioning jobs run at once. Each server's jobs run one at a time.
- Health checks don't run until the server is active.
- The smoke test is the definition of done: a server whose self-check passes but whose proxy test fails from home is **failed**, with the proxy test's error in the log. Only an explicit **Activate anyway** makes it active, and the event records `forced`.
- Root is used once, with the password, to create the `proxier` user. After that every connection is `proxier` with Proxier's key, root login over SSH is off, and root commands go through `sudo -n`. Your personal keys are installed for `proxier` too.
- With key access, Proxier creates nothing for access: the account is the member's, already trusting Proxier's key and (unless it is root) already having passwordless sudo. Full setup still hardens sshd and resets the firewall, so the account and root must survive that; deploy only leaves them as they are.

## Edge cases (each is a test)

- IP already used by active `nl-1` → refused on the form, nothing created.
- An IP `nl-1` keeps as a previous IP → refused, "nl-1 keeps this IP as a previous IP. Forget it there first."
- An IP in the Netherlands, picker empty → "🇳🇱 Netherlands · NL" preselected with "Suggested from the IP (NL)", summary `nl-1`. Another IP in Germany while the suggestion is untouched → Germany. Any country is suggested, not only those with servers; a lookup answer that is no country (or the lookup turned off) suggests nothing.
- Typing "Neth" in the picker, emptying it, or picking Finland while the IP suggests another country → the field is left as it is; the summary shows no name for "Neth" and no complaint while it is typed.
- "Atlantis", a withdrawn code (`UK`) or nothing in the picker → refused on **Create** ("Pick a country from the list." / "Pick a country."), the text kept, nothing created. `nl` or "Нидерланды" typed in full → the Netherlands.
- "Main" holds a custom service with suffix `example.com`; the new server would be `nl-3.hosts.example.com` → refused on the form, naming the list and the service, nothing created.
- Hostname `VPN.example.com.` typed → the summary shows `nl-2` with `vpn.example.com`. The job makes the A record for `vpn.example.com`, the certificate and the connection URIs name it, and `server.created` records it. The next server without a typed hostname gets the pattern's name again.
- Hostname `vpn`, `my vpn.example.com`, `-vpn.example.com`, `*.example.com` or `https://vpn.example.com/` → refused, "Enter a full DNS name such as vpn.example.com.", nothing created.
- Hostname `vpn.example.org` where no configured zone covers `example.org` → the summary shows `A vpn.example.org → 127.0.0.1`, "Create this record at your DNS provider…" and **Check the record**, with no error.
- That record missing, pointing to `198.51.100.1`, or holding a second address → **Check the record** and **Create** say "vpn.example.org does not point to 127.0.0.1 yet (ns1 answered nothing). Create the record A vpn.example.org → 127.0.0.1 at your DNS provider and check again." Nothing is created.
- That record in place → **Check the record** says "The record is in place: ns1 answered 127.0.0.1." **Create** creates the server. The job writes no record of its own, waits for the resolvers to show it, and the server goes active. `server.created` has `manual_dns: true`.
- The record made by hand is deleted between **Create** and the DNS step → the DNS step fails: "… Make sure the record A vpn.example.org → 127.0.0.1 exists at your DNS provider, then retry".
- The zone's nameservers can't be asked (port 53 dropped) → the check asks 1.1.1.1 and 8.8.8.8 instead. No nameservers found from the name's parents → the same.
- Cloudflare not configured → every typed hostname is a record made by hand, in any zone. A server without a typed hostname is refused: "Cloudflare is not configured…".
- The pattern's name in no configured zone → refused, "nl-2.hosts.example.org is in none of the zones Proxier may use. Allow its zone under Integrations → Cloudflare."
- Hostname `nl-1.hosts.example.com` while `nl-1` is active → refused, "nl-1 already has nl-1.hosts.example.com." Once `nl-1` is retired, the name is free.
- `de-1` was given the hostname `nl-3.hosts.example.com`; the next server in the Netherlands would get that name from the pattern → refused, naming `de-1`. Typing another hostname gets it through.
- Two servers created at once with the same typed hostname → one is created, the other refused.
- A typed hostname covered by a routing list → refused, naming the list and the service, as for a pattern name.
- A new install has no hostname pattern → the form's summary says so before a country is picked, and **Create** refuses it unless the member typed a hostname. Nothing is created. Settings → Servers and the dashboard's first-run list ask for the pattern.
- An install that built servers while the pattern had a default, and never saved one → a migration saves the pattern rebuilt from the newest server's management hostname: `nl-2.hosts.example.com` gives `{country}-{number}.hosts.example.com`. A pattern saved in Settings stays.
- Wrong root password → fails at preflight with "authentication failed". The password stays encrypted for retry. **Retry** asks for it again.
- IP unreachable (timeout) → fails at preflight. The DNS is untouched.
- Port 443 already in use on the VPS → fails at preflight, naming the process from `ss`.
- Ubuntu 20.04 against `requires: ubuntu-22.04+` → fails at preflight with the OS found.
- The new key login fails after keys were installed (e.g. `authorized_keys` permissions) → install access fails. The password is kept for retry.
- The sshd reload breaks key login → the drop-in is removed, sshd reloaded, the step fails, and Proxier can still log in.
- Docker already installed → that sub-step is skipped and logged as such.
- The kernel has no TCP BBR → "The kernel has no TCP BBR: TCP stays on cubic" in the log, and provisioning goes on.
- A `proxier` user already exists (a VPS image or an earlier attempt) → it is reused: keys appended, sudoers file rewritten, nothing else changed.
- The sudoers file fails `visudo -cf` → install access fails. The original sudoers configuration is untouched, and the password is kept for retry.
- `sudo -n true` fails as `proxier` (for example, `requiretty` set by the image) → install access fails with sudo's message.
- Self-check passes but every smoke-test attempt stalls after 16 KB → **failed**, with **Retry**, **Activate anyway** and **Retire** offered.
- **Activate anyway** → active. The first round gives `blocked`. A subscription that hides `blocked` servers hides it like any other, once it has been blocked for that subscription's grace period.
- A failure at step 7 → **Activate anyway** isn't offered.
- A conflicting A record at `nl-2.hosts.example.com` (not created by Proxier) → DNS step fails, naming its value. **Retry → Overwrite** replaces it.
- DNS still not visible on 8.8.8.8 after 10 min → DNS step fails. **Retry** resumes at DNS and doesn't recreate the record.
- The zone's nameservers don't serve the record yet → no resolver is asked until they all do; they never do within 10 min → DNS step fails, naming what each nameserver answered.
- Port 53 to the zone's nameservers dropped, or answered by something else (no authoritative flag) → the log says so, the wait pauses 10 s, then asks the resolvers.
- 1.1.1.1 still answers nothing at 10 min while 8.8.8.8 shows the IP (a cached negative answer) → the wait goes on up to the zone's negative TTL plus a minute, and ends when 1.1.1.1 shows the IP. Still nothing then → DNS step fails: "Cloudflare answered nothing (probably a cached negative answer …)".
- Let's Encrypt rate limit → install step fails with certbot's message.
- The container restarts during "Install Docker" → the job resumes at base bootstrap, and running it again is harmless.
- Self-check passes but the proxy test fails 3 times (e.g. the hosting network is already blocked in Russia) → server **failed** at smoke test, with the error.
- **Cancel** during DNS → job cancelled, server **failed**, DNS record kept until retirement or retry.
- Two servers created in the same country at the same moment → `nl-2` and `nl-3`, never two `nl-2`.
- A server fails provisioning as `nl-2` and is retired; the next new server in the Netherlands → `nl-3`.
- A server created when servers had locations, `ams-1` in NL → keeps its name; the next new server in the Netherlands is numbered after the highest `nl-` number, not after `ams-1`.
- Success → server active, health `unknown`, first check round within 5 minutes. Subscriptions with "add new servers automatically" include it, and the `server.activated` notification arrives.
- Key access with Proxier's key missing from the account → preflight fails with "the server refused Proxier's key for <user>" and nothing is changed; Retry asks for no password.
- Key access to a non-root account without passwordless sudo → preflight fails before any change.
- Key access, deploy only → the `proxier` user is not created, sshd is not hardened, ufw stays off, BBR stays off, and the stack still runs.
- Key access, full setup → sshd is hardened and the firewall reset as for password access; the account's key login is verified before the step ends.
- The SSH account field says `not a user`, `Root`, or `a b` → refused on the form, nothing created. `root` and `_deploy` pass.
- Access or setup not one of the stored values (a hand-made POST) → refused on the form.
- Key access as `root` with full setup → refused on the form: hardening would turn root login off and lock Proxier out.

- A template with no endpoint fails at preflight before connecting. Rendered endpoints are validated before uploading files, so an invalid endpoint cannot leave a newly started stack behind.
