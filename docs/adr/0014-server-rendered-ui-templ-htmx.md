# The UI is server-rendered with templ and htmx

The Go binary renders the UI with templ components. htmx handles form posts, partial page updates and server-sent events for live logs and step progress. Plain JavaScript handles the remaining interactions:

- the keymap and the bottom key line;
- the search and actions pop-up (`/`, `:`) and the keys list (`?`);
- drag-to-reorder;
- the code editor, with optional vim keys;
- charts, drawn as inline SVG rendered on the server.

The design in [ui/design](../ui/design/README.md) is built for this. Its pages are mostly lists, forms and panels. Live data arrives as events. `tokens.css` defines the shared design tokens.

A JSON API with a single-page app would add client state, a separate build pipeline and an API to version. Most screens are lists, forms and panels. The few interactions that need client state can use separate JavaScript modules.

## Consequences

- One binary and one language. Pages work without JavaScript except the interactive pieces listed above, so a slow phone still gets a usable page.
- Every action is a normal form post or an htmx request with the CSRF token ([architecture](../architecture.md#http-routes)). The search pop-up's action list comes from the same action registry that renders the buttons, so a button and its action always share the same words.
- Live views (job logs, provisioning steps, syncs) use SSE endpoints per job. A reconnect resumes from the last line ID, so nothing is lost after a phone sleeps.
- The keyboard layer is a progressive enhancement. Every key maps to a link or button that is already on the page.
- Translations live in the Go i18n catalogs used by the templates. Nothing is translated on the client.
- A screen that needs more client state can load its own JavaScript module.
