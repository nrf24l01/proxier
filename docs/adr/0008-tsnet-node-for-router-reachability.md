# Proxier joins the tailnet as its own node to reach routers

Proxier reaches remote router LANs through jump hosts on a tailnet, as mtvpn did with `-r JUMPHOST:HOST`. It embeds `tsnet` and joins Tailscale or headscale as its own node, `proxier`. The node makes outgoing SSH connections to jump hosts and routers.

The container does not need host networking or the host's tailnet state. Proxier has its own identity in the tailnet ACLs, and its configuration declares the connection settings.

## Considered options

- **Host networking** (`network_mode: host`) to use the host's own Tailscale: no extra node, but the container sees the whole host network and depends on the host's tailnet state.
- **A Tailscale sidecar container**: works, but it is another container with its own state to run beside the binary.

## Consequences

- The node needs a pre-auth key on first start, and its key expiry must be handled (a non-expiring node, or re-authentication from Settings).
- Routers that are reachable directly (on Proxier's own LAN) don't need the tailnet at all.
