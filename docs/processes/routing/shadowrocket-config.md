# Shadowrocket config

**Actors**: a member with `routing: own` or more; Shadowrocket on the phone (through `GET /r/{token}/{name}.conf`); routing module.

The phone, away from home, should tunnel the same domains as the home router. Proxier hosts a Shadowrocket config for that: the member's hand-written base config, with the routing list's rules spliced into `[Rule]`. Shadowrocket subscribes to its URL once, and every routing change reaches the phone on its next refresh. It replaced `mtvpn.py shadowrocket` and the copyparty upload. Format rules: [Shadowrocket integration](../../integrations/shadowrocket.md).

## Steps: creating

1. Routing → Shadowrocket → **New config**: name (also the file name, e.g. `iphone`: lower case, and a name that isn't a file name yet is made one as a custom service's tag is, so "iPhone Mom" is `iphone-mom`; the form shows "File name: iphone-mom.conf" as it is typed, and refuses only a name with no Latin letter or digit), routing list, rule policy (default `PROXY`), and the base config (paste, upload, or **Import from URL**, which reads it once, e.g. from copyparty).
2. The base config is validated: it must have a `[Rule]` section. Otherwise saving is refused, with "no [Rule] section to add the services to".
3. Saving creates base config version 1 and the token, and shows the URL `https://proxier.example.com/r/{token}/iphone.conf` with **Copy** and a **QR code**. → `routing.shadowrocket_created`
4. In Shadowrocket: Config → add the URL, then use it.

## Steps: serving

1. Rate limit per IP as for links. An unknown token → `404`.
2. Render:
   1. Take the base config verbatim.
   2. Find `[Rule]`. Rules go in before its `FINAL,` line if it has one (Shadowrocket stops at the first match), otherwise at the end of the section, followed by `FINAL,DIRECT`.
   3. Write a header comment, `# Services from routing list "Main", by Proxier (2026-10-05 04:12 UTC)`, then one `# <tag>` block per service in list order, with `DOMAIN-SUFFIX,<d>,<policy>` for suffix domains and `DOMAIN,<d>,<policy>` for exact ones. Apply the list's ownership rules ([routing lists](./routing-lists.md#rules)). A service left with no rules gets no block.
3. Answer `200` with `Content-Type: text/plain; charset=utf-8` and `Cache-Control: no-store`.
4. Record the fetch: time, IP, user agent.

## Steps: editing

1. **Edit base config** opens an editor filling the page. **Save** sits in the title; upload, import and the version note sit below the code. Saving validates the config, creates a version and records `routing.shadowrocket_updated`.

   If an agent or another tab saved after the editor opened, Proxier rejects the stale save. The warning says "The base config changed since you opened it (an agent or another tab saved it)." **Save** becomes disabled and **Reload** appears. The editor keeps the typed text.

   Older versions can be viewed, compared and restored. A restore creates a new version.
2. **Preview** ("What the phone gets", on the config page) shows the rendered output exactly as the phone would get it, numbered, with Proxier's block marked; **Download** is in its title band.
3. **Change routing list**, **change rule policy**: from the next fetch on.
4. **Regenerate token**: the old URL returns `404`, and the new URL is shown.
5. **Disable**: the URL returns `404` until enabled.
6. **Delete**: the URL returns `404`; its versions and fetch log are deleted with it. → `routing.shadowrocket_deleted`

## Steps: handing the base config to an agent

A coding agent on the member's machine (Claude Code, Codex or similar) can change a base config remotely, as it can a [template draft](../servers/template-authoring.md#steps-handing-the-draft-to-an-agent) or a [router script draft](../router-scripts/script-versions.md#steps-handing-the-draft-to-an-agent). Proxier never runs the agent. It gives the agent a prompt and a short-lived key that only reaches this config's base. A base config has no draft. Each save by the agent is a new base version, as an editor save is, and the phone gets it on its next refresh.

1. On the config page, a member who may change the config chooses **Hand off to an agent…** and fills in:
   - what the problem is and what the agent should do, as free text (for example the error Shadowrocket showed);
   - the agent (Claude Code, Codex, other), which changes only the wording;
   - how long access lasts: 30 min, 2 h (the default) or 8 h.

   The form names the values the agent will never see (below) and says that every save goes to the phone.
2. Proxier creates an **agent session** and returns a token (`pxa_…`, stored only as a hash) inside a Markdown prompt to **Copy**; the page shows the prompt with the token masked, once. → `routing.shadowrocket_agent_session_opened{expires_at, agent, session}`
3. The agent works through the agent API. Its saves are base versions recorded with `agent:<session>` as the actor, with the note it sends or "saved by an agent". → `routing.shadowrocket_updated{changes: base, version}`
4. While a session is open, the config page and the base editor show a band: the agent, the first line of the problem, how many saves, when it expires, **What it changed** (the activity of this session) and, for a member who may change the config, **Revoke access**. The editor asks every 10 seconds whether the base is still the version it opened; when the agent (or another tab) saved, the "base config changed" band turns **Save** off and offers **Reload**.
5. The session ends on expiry, on **Revoke access**, when a new hand-off replaces it (`replaced`), or after the 8-hour maximum. Deleting the config deletes its sessions. A request after that gets `401` "session ended", the same answer as an unknown token. → `routing.shadowrocket_agent_session_closed{reason, saves, session}`
6. The member checks **What the phone gets** and the versions, and restores an older one if the agent's change is wrong.

The **agent API** lives under `/agent/v1/shadowrocket` and accepts only an agent token, never a session cookie.

| Method and path | Does |
|---|---|
| `GET /context` | The problem, the config's name, file, routing list, rule policy, rules inserted now and base version, the check of the base, links. |
| `GET /base` | The whole base config as `text/plain`, with `ETag: "<version>"`. |
| `PUT /base` | Replace the base config, as a new version. Requires `If-Match` with the ETag: a missing header gets `428`, a stale one `412` "the base config changed". `?note=` (at most 200 characters) is the version's note. An LF body saved over a CRLF base gets CRLF. The answer has the new version, ETag, whether anything changed and the check. |
| `POST /base/check` | What a save would refuse (no `[Rule]`, over 256 KiB, not UTF-8, empty), the line of `[Rule]` and of its `FINAL`, where the rules go in, and where the hidden values are: of the saved base (empty body) or of the body, which is not saved. |
| `GET /output` | What the phone gets now, with the hidden values masked. |

Rules:

- **One open session per config.** Opening a new one ends the old one.
- **Narrow scope.** A token reaches one config's base and nothing else. It can't read the config's URL, change its routing list, policy or token, disable, delete or restore it, or read another config.
- **Hidden values never leave Proxier.** The value of every `[Proxy]` line (a server's address and password) and of `[MITM]`'s `ca-p12` and `ca-passphrase` (the MITM certificate authority and its key) reads as `MASKED-BY-PROXIER` in the base, the check and the output. A save puts the stored value back wherever a key of the same section still holds the mask, in order when a key is there more than once; a real value written there replaces it. A mask under a key that held no hidden value, or anywhere else in the base, refuses the save with `422`, so the mask never becomes a value.
- **ETags** are `"<base version>"`; every changing save makes the next version.
- **Limits.** The base's own limits (`422` with the editor's message); a body over 512 KiB is `413`. 600 requests per session and 60 per minute, as for templates.
- **The prompt is a secret.** The token is shown once; reloading the answer to the form shows "Already copied" and opens nothing. A new hand-off makes a new session.
- Opening a session needs what changing the config needs: its owner at `routing: own`, anyone at `manage_all`. A reader sees the band without **Revoke access**.

## Rules

- A rule another rule of the config covers is dropped, within one service or across services (mtvpn's `shadowrocket_rules`): a suffix name under any suffix name of the config, an exact name equal to or under one. Routers keep those names ([ADR 0016](../../adr/0016-every-listed-name-reaches-the-router.md)); Shadowrocket's `DOMAIN-SUFFIX` already matches them.
- The header's time is the render time, in UTC, to the minute.
- The base's line endings are kept: a base whose first line ends in CRLF gets CRLF inserted lines.
- **Preview** can be downloaded as `<name>.conf` (the same bytes; rendering it records nothing). The fetch log keeps 90 days.
- The output is computed on every fetch from the current base version and the accepted snapshots. A sync never needs to run.
- Everything outside the inserted block is copied byte for byte from the base config, comments and blank lines included.
- The inserted block always comes before `FINAL`. Rules after `FINAL` would never match.
- The base config may hold `[Proxy]` or `[Proxy Group]` sections. Proxier copies them, but proxies normally come from the phone's link subscription, not from here.
- The server hostname guard applies, because the list is the same as for routers.
- A config is its owner's and follows a list its owner may change. Its public URL serves whoever owns it, and after its owner is removed ([routing: owners](../../modules/routing.md#owners)).

## Edge cases (each is a test)

- A base with `[General]`, `[Rule]` containing `RULE-SET,…` lines and `FINAL,DIRECT` → services inserted after the `RULE-SET` lines and before `FINAL,DIRECT`, all other lines unchanged.
- A base whose `[Rule]` has no `FINAL` and is followed by `[Host]` → services at the end of `[Rule]`, then `FINAL,DIRECT`, then a blank line before `[Host]`.
- A base without `[Rule]` → refused on save.
- Name " iPad " → `ipad`; "iPhone Mom" → `iphone-mom` (then "Iphone Mom" is taken); "мамин" or " - " → refused, the name needs Latin letters or digits.
- "Main" with `anthropic` (suffix `anthropic.com`) and `mine` (exact `console.anthropic.com`) → only `DOMAIN-SUFFIX,anthropic.com,PROXY`, and `mine` gets no block if it has nothing else, though routers install `console.anthropic.com` under `mine`.
- Rule policy `Proxy-Group-A` → every inserted rule ends with `,Proxy-Group-A`.
- A snapshot accepted at 04:05 → the 04:06 fetch includes the change.
- Regenerate the token → the old URL `404`s, and the new one serves.
- A disabled config → `404`.
- Restore base version 2 while version 4 is current → version 5 is created with version 2's content.
- A member at `routing: own` creates a config on member 1's list → refused on the list field; on their own list → theirs, and another member doesn't find it.
- The editor opened v2, the agent saved v3, then **Save** → refused with the "base config changed" band, the typed text kept, v3 still current. Saved from a reloaded editor → v4.
- Hand-off with an empty problem, one over 4,000 characters or an unknown agent → refused, nothing recorded.
- Access asked for longer than 8 hours → 8 hours.
- A second hand-off on the same config → the first ends (`replaced`) and its token gets `401`. If the first had expired unnoticed, it ends as `expired`.
- An ended, an expired, an unknown token, no token and a session cookie → the same `401` "session ended".
- `GET /base`, `/context` and `/output` of a base with a `[Proxy]` line and `[MITM]`'s `ca-p12` and `ca-passphrase` → all three read `MASKED-BY-PROXIER`; none holds the values.
- `PUT /base` without `If-Match` → `428`; with a stale ETag → `412`; neither makes a version.
- The agent adds a rule and leaves the masks → a new version with the rule and the real values, its note; `shadowrocket_updated` has actor `agent:<id>`; the save is counted. The same text again → no version, no event, not counted.
- The agent renames the masked proxy, or puts the mask on a rule → `422`, no version.
- A body without `[Rule]`, not UTF-8, empty or over 256 KiB → `422`; over 512 KiB → `413`; a note over 200 characters → `422`.
- An LF body saved over a CRLF base → the version keeps CRLF.
- A member saves while the agent edits → the agent's write with the old ETag gets `412`.
- `POST /base/check` of a base whose `[Rule]` has no `FINAL` and is followed by `[Host]` → no errors, the rules go in before the blank line, "followed by FINAL,DIRECT"; nothing saved.
- Revoke access → the session ends (`revoked`) and the token gets `401`. Revoking a session through another config's page → `404`.
- Deleting the config → its token gets `401`.
- 60 requests in a minute → the 61st gets `429` with `Retry-After`; the next minute works again; the 601st request of a session → `429`.
- The posted hand-off form reloaded → "Already copied", no second session.
- The band shows the first line of the problem only; it and the editor's poll go when the session ends.
- A member at `routing: own` opens the hand-off of another member's config → `404`; a Viewer → `403` and no **Hand off** button; on their own config → the form.
