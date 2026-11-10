# Within a routing list, every domain belongs to exactly one service

When services in a routing list share the same name in the same form, the first service in list order owns it. If the name appears as both an exact name and a suffix, the suffix owns it. Each tag installs only its owned names.

A subdomain under another service's broader suffix still reaches the router. [ADR 0016](./0016-every-listed-name-reaches-the-router.md) replaced the earlier rule that dropped those subdomains. Shadowrocket exports still remove rules that another rule covers.

mtvpn installed services in sequence, so the last service took shared names from earlier ones. Removing it could leave a name missing until another service refreshed. Proxier recomputes ownership after a list edit and syncs every tag whose names changed.

## Consequences

- One change to a list can touch several tags on a router (the old and the new owner). The sync pushes tags that gain names first, so a name moving between tags is never missing.
- The service page has to show "owned elsewhere" for names a service lists but doesn't install, or the counts look wrong.
