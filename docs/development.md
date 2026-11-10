# Development

This page describes the code layout, implementation rules and checks. The [module docs](./modules/) and [process docs](./processes/README.md) describe behavior. The [ADRs](./adr/) explain the decisions behind it.

## Making a change

1. Read the process doc of the flow before touching it. Its **Edge cases** are a test list: each one has a test, and a new edge case gets a test with it.
2. Change the docs in the same commit when behaviour changes (process doc, module doc, [events.md](./events.md), [data-model.md](./data-model.md), [ui/README.md](./ui/README.md)). A decision that is hard to reverse, surprising and a real trade-off gets an ADR.
3. Schema changes go only into **new** migrations (`NNNNN_name.sql` in the module's `migrations/`, goose `-- +goose Up` / `Down`, `STRICT` tables). Every module's schema is deployed; never edit an existing migration. Each module's `migrations_test.go` tests the constraints.
4. Run `make check`, `make lint` and `docker build .`. The Docker commands need Docker Desktop running. On macOS, start it with `open -a Docker`.
5. State in the commit which checks still need real hardware, accounts or phones. Track them in [open-questions.md](./open-questions.md). Do not report a simulated check as a real-device result.

## Layout

```
cmd/proxier/                 serve | manage <command> | version | healthcheck; modules() lists the enabled modules in order
internal/platform/           the platform, itself module "platform"
  config obs db (+dbtest) vault events settings migrations httpx
  access                     areas, levels, grants, presets, scopes: what a member may do (a leaf)
  auth (+authtest) web ui pages i18n sitetest
  jobs (+jobstest) notify (+telegram) bot sshx (+sshxtest) tailnet backup geoip
  agentapi                   what every agent API shares: the token, request limits, the one-time hand-off form
  module                     Module and the optional interfaces below
internal/modules/servers/        templates, provisioning, deploys, health, stats, retirement, the agent API
internal/modules/subscriptions/  subscriptions, links, the public /s/ fetch, alerts, cut-off
internal/modules/routing/        services, lists, refresh, catalog, Shadowrocket (+ its agent API), mtvpn import, router sync, discovery
internal/modules/routerscripts/  script versions, generations, fetch URLs, the router script agent API
web/editor/                  source of the CodeMirror bundle (make editor → internal/platform/ui/static/js/editor.bundle.js, committed)
```

Modules are enabled in this order: `servers`, `subscriptions`, `routing`, `routerscripts`; each later one may consume the ports of earlier ones. Inside a module the shape repeats:

| Package | What |
|---|---|
| root | the `module.Module`, its optional interfaces, `New(Ports)`, the ports it provides; wiring only |
| `conf` | the settings section and its keys (a leaf, so services and pages read the same names) |
| `migrations` | the module's schema |
| `store` | every SQL statement of the module, `FieldErrors` (i18n keys), retention constants |
| one package per area | services and their job types (`servers/provision`, `subscriptions/links`, `routing/routers`, `routerscripts/generations`, …) |
| pure packages | no database, no clock of their own: `servers/render`, `subscriptions/output`, `subscriptions/linkpage`, `routing/domain`, `selector`, `snapshot`, `own`, `routeros`, `routerscripts/params` |
| `pages` | handlers and templ files |
| `<module>test` | the test harness (below) |

Packages worth knowing by name:

- **servers**: `remote` (every shell command sent to a server, and `remote.Parse`, its inverse), `sealed` (every AAD), `endpoint` (`vless-xhttp-tls`: checks, URI, masked URI), `proxy` (the only importer of xray: proxy test, `Dialer`, config validation), `dns` + `dns/cloudflare`, `checkhost`, `validate` (pure-Go validators), `agent` (`/agent/v1`).
- **routing**: `change` (shared sync triggers), `sources` (the one outbound HTTP client), `routeros` (every RouterOS command and script, `Parse`/`ParseScript`), `routers` (plan, sync, drift, unmanaged tags, awaiting setup), `discovery` (+ `cdp`, `socks`), `shadowrocket/agent` (`/agent/v1/shadowrocket`; `mask.go` keeps `[Proxy]` lines and the MITM key from the agent).
- The router scripts module uses `params` for PARAMETERS blocks, literals, `Fill` and `Eval`. Its `params/paramstest` package embeds a sanitized `fresh-router.rsc` fixture without an end marker. Tests build variants in code. The `agent` package serves `/agent/v1/router-script`, and `mask.go` hides secret defaults from the agent.

## Module interfaces

`internal/platform/module` defines these interfaces. The platform discovers them through type assertions and implements them like any other module. `platform.Open` calls `Init` before requesting job types, subscribers, renderers and subject namers, so those methods can use services created in `Init`.

| Interface | Method |
|---|---|
| `Module` | `Name() string`, `Migrations() fs.FS` |
| `EventDeclarer` | `EventTypes() []events.Type` |
| `SettingsDeclarer` | `SettingsSections() []settings.Section` |
| `Initializer` | `Init(d Deps) error` (`Deps`: Cfg, Log, DB, Vault, Events, Settings, I18n, Auth, Jobs, Dispatcher, Notify, SSH, Tailnet) |
| `Migrated` | `AfterMigrate(ctx) error`, after every migration, on every start |
| `MessagesDeclarer` | `Messages() i18n.Messages` |
| `RouteDeclarer` | `Routes(r web.Routes)`: `r.Area(access.X)` gives `.View`, `.Manage` and `.At(level)` (session + CSRF + the grant's level in X), `r.Public` (no cookies), `r.Shell`, `r.SettingsPages`. `r.Member` (any grant) is the platform's, for every member's own pages |
| `NavDeclarer`, `SettingsPageDeclarer`, `IntegrationDeclarer` | sidebar entries and Settings pages, each with its `Area` (`access.EveryMember` for every member's; a nav entry or dashboard area may add the `Level` its page needs), rows on Settings → Integrations (a Settings page, so `settings: manage`) |
| `Searcher` | `Search(ctx, who, q, limit)`: hits for the `/` and `:` pop-up of what `who.Scope(area)` reads, each with its `Area`; a hit naming none is left out |
| `JobDeclarer` | `JobTypes()`, `Schedules()` |
| `SubscriberDeclarer` | `Subscribers() []events.Subscriber` |
| `SubjectNamer` | names and links for `type:id` subjects in Activity and Jobs |
| `SubjectOwner` | `OwnedSubjects(ctx, member)`: the subjects of the things a member owns, for Activity and Jobs at `activity: own`; `Owner(ctx, subject)`: the member who owns one subject (not ok for a type the module doesn't own, a shared thing or one gone), for a notification's recipients |
| `NotificationRenderer` | the module's own notification texts |
| `BotCommandDeclarer` | `BotCommands() []bot.Command`: commands a member gives the Telegram bot in their own chat, each with its area and level and a first step; a step's `bot.Reply` may ask for a text or offer buttons, each naming the next step ([telegram bot](./processes/platform/telegram-bot.md)) |
| `DashboardDeclarer` | `Dashboard(ctx, who)`: dashboard areas of what `who.Scope(area)` reads, sorted by `Order`, each with its `Area` (and `Level`) |

## Rules

**Data and transactions**

- `db.DB.Write(ctx, fn)` holds the process's only write connection (`BEGIN IMMEDIATE`). Never touch `d.W` inside its callback (deadlock); reads inside a write use the tx. Nothing slow or remote inside: read, act remotely, then write. `R` is a 4-connection `query_only` pool (`VACUUM INTO` needs its own `mode=ro` connection: `db.DB.VacuumInto`).
- Times are stored as `db.Time` (`2006-01-02T15:04:05.000Z`, SQL default `strftime('%Y-%m-%dT%H:%M:%fZ','now')`) and shown in the display zone.
- A module never reads another module's tables; no foreign keys across modules. Another module's thing is kept by id plus the name copied at the time.

**Ports** (ADR 0002)

- A module's non-test files import from another module only its root package (the ports), plus `servers/endpoint` for subscriptions. `TestImportsOnlyServersPorts` / `TestImportsOnlyPorts` enforce it.
- A nil port hides the feature: no `Rotator` hides Cut off, no `Hostnames` guards nothing, no `Links` / `Routers` turns `@fill` parameters into ordinary fields.
- Ports that write (`LinkIssuer.Issue`, `RouterRegistrar.Register`) run inside the caller's transaction (`links.CreateTx`, `routers.RegisterTx`).

**Events and actors**

- Every state change records its event in the same transaction; a change that changes nothing records nothing. Not events: job lifecycle (the job tables are its history; only `job.failed` and modules' own failure events), notification delivery (only `notification.failed`), bookkeeping (last seen, cursors, schedule runs).
- Actors: `member:<id>` (`events.MemberActor`; a handler takes the signed-in member's from `web.Actor(ctx)`), `system` (reactions, public fetches), `cli`, `job:<id>`, `agent:<session>`. A job's `created_by` adds `schedule:<name>` and `event:<id>`. Rows from before members say `admin`, which is member 1 (`events.ActorMember` reads both); the UI names a member by username with `web.ActorName`.
- Payloads carry names, not ids, for other things (`list: "Main"`), lists as one comma-joined string, times as `db.Time` strings (`""` for none), and never a secret.
- Every event type needs `event.<type>` messages; a notifying one `notify.<type>` too; every job type `job.<type>` (tests enforce all three).
- An event type's `Audience` says who its notification may go to ([recipients](./processes/platform/notifications.md#recipients)): by default the subject's owner and the members who manage the area; `AudienceSubject` for a member's own sign-ins and ways in; `AudienceSettings` for changes to members. A new owned thing's subject type gets an answer from its module's `SubjectOwner.Owner`, or its owner hears nothing of it.

**Access** ([members](./processes/platform/members.md))

- Every signed-in route is on its area's group: what reads on `View`, what changes on `Manage`, what needs more on `At(level)`. `r.Member` holds only every member's own pages; `TestEverySignedInRouteChecksAnArea` (cmd/proxier) walks every route of the app as a member with no access and fails on any other that answers without 403.
- A level is parsed at the boundary (`access.ParseArea`, `access.ParseLevel`): forms and rows become an `access.Area` or `access.Level` there and nowhere else.
- A handler that checks more than its route (Cancel and Retry check the job's area) returns `web.Forbidden(area)`, an error, so it stops there; pages that show more to a higher level ask `web.Can(ctx, area, level)` or `Shell.Grant`.
- Owned resources have an `owner_id` with no cross-module foreign key. Every query takes an `access.Scope`. Handlers use `web.Scope(ctx, area)`; jobs, reactions and public fetches use `access.All`. SQL applies it with `? IN (0, owner_id)`. A zero scope reads nothing. See [ownership](./processes/platform/members.md#ownership).
- At `own`, the scope reads the member's resources. From `view_all`, it reads everyone's. Only `manage_all` writes everyone's resources. A service's `toChange` helper returns not found outside the read scope, or `access.Forbidden(area)` when `scope.Writes(owner)` is false. Pickers use `scope.Writable()`. Pages hide unavailable controls and use `web.OwnerNote` to identify another member's ownership.
- Creating takes the owner from `access.Who` (`web.Who(ctx)`): a port that creates for another module (`LinkIssuer.Issue`, `RouterRegistrar.Register`) takes the `Who` and decides the scope of its own area.
- Each owning module reacts to `member.removed` (`auth.HandoverOf`) by giving the removed member's things to the heir in the reaction's transaction, recording one `*.handed_over` event only when it moved something.

**Secrets**

- Only through the vault, sealed with an AAD naming where they live, found by `vault.Lookup` (HMAC). AADs: `setting:<key>`, `job:<id>:payload`, `member:<id>:totp` (member 1's is `admin:totp`), `setup_link:<id>:token`, `server:<id>:gen:<key>`, `server:<id>:params`, `server:<id>:endpoint:<key>`, `deployment:<id>:…`, `link:<id>:token`, `shadowrocket:<id>:token`, `generation:<id>:secrets`, `fetch_url:<id>:token` (servers' are all in `servers/sealed`).
- Tokens are `vault.NewToken()` (256 bits, 43 characters); agent tokens are `pxa_` + one (`agentapi.NewToken`), for every agent API; each module looks its own up. A token appears only on its own page (masked, Reveal, Copy, QR), and a link's in the bot's answer to `/link` ([ADR 0019](./adr/0019-members-give-the-bot-commands-in-their-own-chat.md)); never in an event, notification, log line, job payload, list, search or the dashboard. `httpx.MaskPath` masks `/s/`, `/r/`, `/f/` in logs. Pages that show one are `no-store`.
- A job that renders registers every generated value and secret parameter with `Logger.Redact` before its first remote call.

**Jobs**

- Resource keys: `server:<id>` for every job that changes a server or reads it over SSH (the self-check with `SkipIfBusy`; the proxy test takes none and asks `jobs.System.Busy` instead, since `SkipIfBusy` counts queued jobs too). `router:<id>` for every job that connects to a router. `service:<id>` for one service's refresh.
- Every waiting step selects on `r.Cancelling()`. A job type that writes a deployment marks it failed in `OnCancelled`, and its failure event carries `cancelled: true` (which doesn't notify).
- Work a job queues for after itself on its own resource key goes in `OnSucceeded`, the transaction that marks it succeeded. Queued from a step, a delayed job that asks `Busy` when it starts would find the job still running and skip itself. The IP change queues the new IP's checks this way.
- A server's current files are those of its latest succeeded deployment with `uploaded = 1`, never just the latest deployment.

**Remote text in one place**

- Every shell command sent to a server is built in `servers/remote`; every RouterOS command and script in `routing/routeros`. The fakes answer exactly what `remote.Parse` / `routeros.Parse` recognise, so a command changed in one place changes in the fake too.
- Outbound HTTP of routing goes through `sources.Fetcher` (one client, `proxier/<version>` user agent, a body cap, base URLs in `sources.Endpoints`). Interactive calls: 45 s overall, 15 s per request; jobs: 30 s per request.

**Clocks**: every service that compares times takes `Now func() time.Time`. Each module has `Module.Now`, read through a function set in `Init`, so a test sets it once.

**i18n**: EN and RU catalogs per module (`Messages()`); a key belongs to the module that declares it, under the module's prefixes. `TestEveryUsedKeyExists` scans literals.

## Pages

- `r.Shell` + `ui.Layout`, rendered with `web.Render`. A public page for people who never sign in (a link's page) is `ui.Standalone`: the logo bar and the page, without htmx and `proxier.js`, with its own script. Every button is a `ui.Action` (it shows up in the `:` pop-up; one drawn elsewhere, like Settings in the sidebar, stays there as a `Hidden` action in the header); confirmations through `ui.Confirm`; an action that needs fields is a **page styled as a dialog**; secondary actions in an **Actions** area (no "More ▾" menu).
- A strict CSP: no inline `style` or `script` (`TestPagesHaveNoInlineScriptOrStyle`); shared classes go into the platform's `app.css`. Never name a row class `app` (the shell's `.app` is 100 vh).
- htmx 2 doesn't swap 4xx with the platform's config: htmx answers that must re-render are 200 with a notice; plain posts get 409. A refusal for the member's grant is the exception: 403 with a `.forbidden` fragment, which `proxier.js` says in the key line.
- A table is a `.colhead` and its `.row`s inside a `<div class="tbl">` (`TestEveryTableIsInATableBox`). Every column in its `grid-template-columns` has a real minimum (`minmax(120px, 1fr)`, never `minmax(0, …)`, whose columns collide when zoomed in). Cells are one line ending in "…", so give a long value a `title`; a message cell that must wrap gets `.wrap` (`.wrap.clamp` stops it after three lines, its `title` holding the rest). Two-column page layouts stack in the `max-width: 1100px` block at the end of `app.css`; one whose table needs more room beside its panel gets its own earlier block after its rules (`.tgrid` at 1270 px). A layout that changes again on a phone puts its narrower rules before its 760 px block, since the 1100 px block at the end would override them (`TestTemplatePagesHoldUpWhenZoomed`).
- A row's `›` caret is placed absolutely in the gutter and takes no grid cell, so a row's `grid-template-columns` has one column per cell, never one for the caret (`.row.srv` alone places its caret in a column, `position: static`). A column for the caret shifts every cell into the wrong column (`TestRowGridsHaveACellPerColumn` checks every row with a caret).
- The page column (`.main`) has no padding: tables, areas, forms and the title block bring their own. Text placed straight in it carries a class that pads it like the rows: `.foot` for a note under a table or area, `.pager`, `.area-body`, or it goes inside a `.form` (`TestPageTextSitsInsideThePadding` checks every direct child of `ui.Layout` and `ui.SettingsLayout`, and of a component of the same package called there). A list in an `.area-body` carries a class that keeps its markers inside the padding too (`padding-left: 36px`, or `list-style: none`; `TestListMarkersSitInsideThePadding`). A block of output under padded controls sits inside their padding and wraps (`pre-wrap`) rather than scrolling sideways when text follows it, since a sideways scrollbar covers the note under it (`TestPreviewSitsInsideThePadding`).
- An area body whose parts sit under each other (a URL field and its note, a QR toggle, lines of text and a button row) is `area-body stacked`: a column whose 12 px gap is the only spacing between its parts (their margins are zeroed). A secret field's buttons go in one `.url-btns` inside `.urlfield`, so they wrap together; a QR slot is `.qrslot`, its toggle first and the code under it (`TestLinkPartsAreSpaced`, `TestAreaPartsAreSpaced`). The URL itself is `ui.URLText(u)`: a `<wbr>` after each slash, with `overflow-wrap: anywhere` for a part wider than the line, never `word-break: break-all`, which splits words (`TestURLTextBreaksOnlyAfterSlashes`).
- An **Actions** area wraps its `.row.act` rows in `<div class="act-rows">`: a grid whose button column is as wide as the longest button, which the rows share as a subgrid, so the descriptions line up. A bare `.row.act`'s column is `minmax(190px, max-content)`, so a long label never runs over its description.
- A long rendered output beside other areas (the Shadowrocket preview) grows with the page; a `max-height` box of its own would put a second scrollbar beside the page's (`TestShadowrocketPageHoldsUp`, `TestConfigPageHoldsTogether`). The key line is `position: sticky` in the flow after the page column, never `fixed`, so the end of a page is never under it (`TestKeyLineNeverCoversThePageEnd`). The `g` which-key strip is the key line's first child, `position: absolute` above its top border, so it neither covers the line nor lengthens a short page (`TestWhichKeyStripSitsAboveTheKeyLine`).
- A block with nothing to show is left out (an `if` around it), not drawn empty: its padding and lines would stand as a blank band. A group heading in a table always has a row under it, if only one saying there is nothing (`TestHealthTabHasNoEmptyBlocks`).
- A side panel beside a long form or list is an `aside` that the grid stretches to the row's height (no `align-items: start`), with the divider as its border, and its content in a `<div class="stick">` (sticky at `top: 0`, since the header scrolls away; capped at the window above the key line, scrolling on its own). Stacked under 1100 px, `.stick` is static (`TestSidePanelsStickToTheTop`).
- A job's log is `ui.JobLog` (the `#log` box, one per page) in a `section.jobmain` whose head holds `ui.LogTools` (Follow while live, Expand); the box scrolls on its own (`max-height: max(240px, 60vh)`) and Expand makes the column the window's height (`.jobmain.log-full`). The script follows by setting the box's `scrollTop`, never `scrollIntoView`, which scrolls the page too (`TestLogScrollsOnItsOwn`).
- A choice among hundreds (New server's country) is an `input` with a `datalist`: the browser searches it as the member types, with no script, and the handler reads the text back (`country.Parse`). Keep the `datalist` out of any out-of-band swap, so a live answer stays small.
- `proxier.js` handles sortable lists, cursor selection, copy, escape, reveal and the keymap. A `[data-reveal]` response scrolls into view if needed and focuses a target with `tabindex="-1"`. Scrolling honors reduced-motion preferences.
- Every arrow-driven list also uses `ctrlStep(e)` for `⌃N` and `⌃P`. It reads `e.code` for non-Latin layouts, so Russian `⌃т` works. Modified-key handling must not discard these events before row navigation. Fields and dialogs keep their own behavior. Code editors stop their key events at the editor host. See `TestCtrlNPMoveLikeTheArrows`.
- The country `datalist` belongs to the browser and cannot use page key handling. Use `hx-trigger="every Ns"` polling only while something is waiting.
- A code editor's height is the page's: `editor.js` sets none, and `app.css` makes CodeMirror fill its box. A page whose job is its editor marks the part under the title `.code-fill` (the Shadowrocket form, the template editor's `.ed`): it fills the window to the key line and the code takes what is left (`.code-frame`, at least 360 px), so the page doesn't scroll; its actions go in the title block (`form="…"`, `data-code-save` for `⌘S`), and the sidebar is `contain: size` there so a long nav doesn't stretch it. Every editor's band holds `ui.CodeTools()` (Wrap lines, kept in `localStorage` by the bundle; Full screen, `#app.code-full` in `proxier.js`, which `esc` leaves after the editor's own `esc` has left the code, never from a dialog) and its page adds `ui.CodeKeys` to the keys sheet (`TestCodeEditorsFillTheWindow`).
- CodeMirror lives on the page itself, not in a shadow root, where Safari's Vimlike takes typed keys as commands. CodeMirror writes `<style>` tags the CSP blocks, so `web/editor/build.sh` patches style-mod to use adopted stylesheets. Code areas are `textarea[data-code="<mode>"]` (`initCodeAreas()`), so forms work without JS; a page loads the bundle with `Shell.Scripts`.
- After editing `*.templ`, `make templ`; generated files are committed and `make check` compares them against the index (stage them).

## Tests

- Test names are CamelCase sentences (`TestRolledBackChangeLeavesNoEvent`). No test touches the network or sleeps longer than 100 ms; clocks are fake. SQLite in temp files (`dbtest.Open`); a set of migrations runs once per test binary and every test gets a copy of the migrated file (`dbtest.Seed`, which `sitetest.New` uses too), since migrating takes most of a second under `-race`.
The test helpers provide the following components:

| Helper | Use |
|---|---|
| `sitetest` | Runs the application over HTTP with a fake auth clock. `Join` creates a member through a setup link and signs them in. |
| `authtest.Authenticator` | Implements a software passkey. `Create` and `Get` answer server options; fields let tests simulate invalid behavior. |
| `jobstest` | Provides a fake clock and `Drain`. |
| `sshxtest.Server` | Simulates SSH hosts. `Freeze` stops all responses, `Session.Freeze` stops one connection, and `OnUpload` observes uploads. `BreakOnGlobalRequest` simulates RouterOS rejecting a keepalive. |
| `sshxtest.Links` | Replaces `SSH.Dial`. `Stall` stops traffic on existing connections to an address, leaving writes blocked until close. |
| `telegram.Fake` | Queues updates with `Message`, `GroupMessage` and `Tap`. Tests run the bot through `App.Bot.Poll`. |
| `serverstest` | Simulates a VPS answering `remote` commands. `NewHarness(t, serverstest.StubProxy())` uses one VPS per test on loopback. Fake nameservers serve records held by fake Cloudflare. `HideDNS` hides records from resolvers; `NameserversDown` drops DNS requests. External checks use `CH`, and reference checks use a local site. |
| `proxytest` | Runs an in-process xray XHTTP server. |
| `cloudflaretest`, `checkhosttest`, `sourcestest` | Simulate upstream APIs and sources. |
| `substest` | Tests subscriptions against a fake catalog. Options include `WithServers` and `NoRotator`. |
| `routingtest` | Tests routing with `New`, `WithServers` and `WithBrowser`. Helpers include `h.Router`, `h.JumpFake`, `h.Settle` and `h.Advance`. |
| `routerostest` | Simulates RouterOS and jump hosts. Imports continue after a session ends until the user's script jobs stop. Helpers include `Hold`, `ForgetImport` and `SlowDNSRead`. |
| `discoverytest` | Simulates browser visits for discovery. |
| `rscriptstest` | Uses fake ports with `New` or real modules with `WithModules`. |

- Test files that use a module harness are external test packages (`package links_test`): the harness imports the module root, which imports every service.
- xray's XHTTP client has a data race (`splithttp.WaitReadCloser.Set`), so packages that push XHTTP traffic (`servers/proxy`, `provision`, `deploy`) run without `-race` (`XRAY_TESTS` in the Makefile). Other tests use `StubProxy()` and run under `-race`. SQLite is instrumented too (`RACEFLAGS` in the Makefile), so the race detector sees its synchronization. The two runs of `make test` go side by side. This xray has no `allowInsecure`: tests pin the self-signed certificate.
- Never assert the absence of a bare word in a page (CSRF tokens are random): use `>Name<`.
- Set real-time wait limits through `testwait.Deadline(d)`, including `Drain`, `Settle`, `WaitFor` and polling loops. Do not use `time.Now().Add(d)`. Choose `d` for a laptop. CI scales it with `PROXIER_TEST_PATIENCE=10` and sets `GOTESTFLAGS=-timeout=60m` because concurrent race tests run more slowly there.
- Move a harness clock only with `Advance`. The display zone in tests is Moscow.
- `TestRealChromium` runs only with `PROXIER_TEST_CHROMIUM_URL` (and `PROXIER_TEST_PAGE_HOST=host.docker.internal` when Chromium is a container).
- `TestKeymapInRealChromium` (routing pages) presses `proxier.js`'s keys on routing's pages and runs only with `PROXIER_TEST_CHROMIUM_URL` (and `PROXIER_TEST_PAGE_HOST`). It types as a keyboard does, one `keyDown` carrying the character: `chromedp.KeyEvent` sends the character as a separate event, which a handler's `preventDefault` can't drop, so it lands in the next page's autofocused field. The page's CSP refuses a `style` attribute: a test that shapes the page sets `el.style` from script.
- `TestPasskeyInRealChromium` (pages) runs only with `PROXIER_TEST_CHROMIUM_URL` too. WebAuthn needs a secure context, so it serves the app on `localhost:18123` (`PROXIER_TEST_PAGE_PORT`); with Chromium in a container, forward that port inside it: `docker exec -d <container> socat TCP4-LISTEN:18123,fork,reuseaddr TCP4:host.docker.internal:18123`. Chromium's `--unsafely-treat-insecure-origin-as-secure` has no effect in `chromedp/headless-shell`.

## Looking in a real browser

For container-based browser checks, run `chromedp/headless-shell` with `-p 127.0.0.1:19222:9222`. Start a temporary application instance on the host with a test or `go run ./cmd/proxier serve`. Connect over CDP and use `host.docker.internal` to reach the application.

Plain HTTP at that address is not a secure context. Clipboard tests may need a stub for `navigator.clipboard`; WebAuthn tests need the localhost forwarding described above. Choose an unused application port and stop the temporary instance after the check.
