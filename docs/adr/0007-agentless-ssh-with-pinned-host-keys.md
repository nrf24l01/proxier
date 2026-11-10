# Servers are managed agentlessly over SSH, with host keys pinned on first contact

Proxier installs no agent on servers. Provisioning, redeploys, self-checks and stats use SSH with Proxier's key. A server runs Debian or Ubuntu with Docker and keeps working without Proxier.

Proxier pins each server, jump-host and router host key on first contact. A changed key stops jobs for that host and triggers a notification. A member with `settings: manage` must accept the replacement before jobs continue. Automatic acceptance would let an attacker impersonate the host and read or change server secrets.

## Considered options

- **An agent on each server** (pushing metrics, applying configs): real-time data and no inbound SSH, but another daemon to build, update and secure on every VPS, and another fingerprint for censors to detect.

## Consequences

- Stats are 5-minute samples read over SSH, not real-time.
- Rebuilding a VPS at the same address (a provider reinstall) shows a host-key change that a member with `settings: manage` must accept.
