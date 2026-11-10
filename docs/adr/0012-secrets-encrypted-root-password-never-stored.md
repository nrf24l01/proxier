# Secrets are encrypted under one master key; a new VPS's root password is never kept

Proxier encrypts database secrets with AES-256-GCM under a master key from the environment. These include generated values, link and config tokens, deployed files containing credentials, integration tokens and Proxier's SSH private key. Token lookup uses an HMAC rather than the token's plain value.

A new VPS's root password stays encrypted in its provisioning job until login with Proxier's SSH key works. Proxier then erases it. Later database snapshots and logs cannot recover that password.

## Consequences

- Losing the master key loses every secret. Keep a copy outside Proxier's machine, in a password manager. Backups are useless without it, which is also what makes them safe to copy around.
- A provisioning job that fails before key login keeps the password, encrypted, until it is retried, cancelled or the server is retired. A retry after that point asks for it again.
