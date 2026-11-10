# Modules are compiled-in packages that talk through ports and events, with no cross-module foreign keys

Each module is a Go package registered in `main`. It owns its tables, migrations, routes, jobs and translations. Modules call each other through small exported interfaces, such as `EndpointCatalog` and `LinkIssuer`, and react to events.

A module never reads another module's tables. The database has no foreign keys between modules. A row may store another module's ID, but an event triggers cleanup. For example, `server.retired` makes subscriptions drop that server. These boundaries allow a module to change or be omitted without rewriting the others.

## Considered options

- **Go runtime plugins or separate services**: Go plugins are fragile across builds, and services would bring network APIs, deployment and versioning for a tool a few members use.
- **One shared schema with foreign keys everywhere**: simpler queries, but every module then depends on every other module's tables, and "swappable" stops being true.

## Consequences

- Cross-module cleanups are eventually consistent (after the event is dispatched), and event handlers must be idempotent.
- A module consuming a port must work, with features hidden, when the providing module is absent.
