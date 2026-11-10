# The router-side contract stays compatible with mtvpn

Router sync writes the same objects as `mtvpn.py`. Each domain gets a DNS static FWD entry and an address-list hostname entry, tagged by service, in `to_vpn_list` and forwarded to `vpn-doh`. Proxier leaves `mtvpn:` infra pins and `telegram-cidr` unchanged.

A router set up by `fresh-router.rsc` and managed by mtvpn needs no migration. Its first sync leaves matching tags unchanged. Renaming the prefix to `proxier:` would require changes to the script and every router without changing behavior.

## Consequences

- Proxier inherits mtvpn's quirks deliberately: one `:set` per line, `/import` output scanning, long push timeouts, hostname entries because RouterOS refuses static entries that duplicate dynamic ones. They are documented in the RouterOS integration doc so they don't get "simplified" away.
- The names are per-router settings with mtvpn's defaults, so a router with other names can still be managed.
