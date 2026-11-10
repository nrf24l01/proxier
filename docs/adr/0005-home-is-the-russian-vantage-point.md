# Proxier runs at home, which makes it its own Russian vantage point

Proxier runs at home in Russia behind an HTTPS reverse proxy. Its proxy tests measure whether a server works from that network, including throttling that starts after the first 16 to 20 KB. An SSH self-check and check-host.net nodes abroad help distinguish a blocked server from a failed one.

A reference check tests home's internet connection. Proxier freezes server verdicts when home is offline or cannot reach foreign sites. A failure of home's connection must not change the health state of every server.

## Considered options

- **A VPS abroad**: always reachable, independent of home internet, but blind to Russian blocking without a separate in-Russia probe.
- **A VPS in Russia**: sees blocks and stays up when home is down, but costs extra and is another host holding every secret.

## Consequences

- Traffic from Proxier to its servers must never enter the home router's VPN. Routing lists refuse to cover a server's hostname.
- When home is offline, the UI and every link URL are unreachable. Apps keep their last list, so connections are unaffected.
- Blocks specific to mobile networks or other ISPs stay invisible until probe agents exist.
