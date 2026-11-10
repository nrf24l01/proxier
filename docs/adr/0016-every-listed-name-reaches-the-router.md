# Every name a service lists reaches the router, even under another's suffix

Supersedes the coverage part of [ADR 0013](./0013-one-owner-service-per-domain.md): Proxier no longer drops a name because another service in the list has a suffix above it, and a custom service no longer absorbs its own names under its suffixes. A router installs every name its services list, for every service, upstream or custom. Only the same name is still given to one owner: in the same form, the first service in list order; as both a suffix and an exact name, the suffix.

A MikroTik suffix entry does not resolve every subdomain. The router resolves each address-list hostname it receives, and each FWD entry adds the addresses it resolves to the address list. A listed subdomain therefore needs its own entries, including for clients that do not use the router's DNS.

mtvpn kept such subdomains on routers and removed them only from Shadowrocket exports. Proxier initially applied the export rule to routers too. That prevented a listed `console.anthropic.com` from reaching the router whenever another service listed `anthropic.com`.

The same name stays with one owner because a router holds one entry per name: the service block removes every other entry with its names, so two tags claiming one name would take it from each other on every sync ([RouterOS integration](../integrations/routeros.md#service-block)).

## Consequences

- A list installs more entries: each name under another service's suffix is its own pair of entries. They are harmless (the same forwarder, the same address list) and they make the router resolve those names.
- Reordering services, or removing the service with the broader suffix, no longer moves those names: only names shared in the same form (and an exact name a suffix of the same name replaces) change owner.
- The Shadowrocket config is unchanged: it still drops every rule another rule of the config covers, since Shadowrocket's `DOMAIN-SUFFIX` already matches the names under it.
- The service page lists a name as not installed only when another service installs the same name, the guard leaves it out, or it is a router's infra pin. The custom editor says which rows another service installs instead, and which are installed though another service's suffix covers them too.

## Rejected

- **Only custom services' names**: the hand-written lists kept as URL sources by the mtvpn import, and upstream lists, have such names too, and mtvpn installed them all.
- **A per-name "force" flag**: a schema change and a choice to make on every name, for what is the right default.
