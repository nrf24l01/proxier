# The UI and the public URLs share one public host, with password sign-in

The UI and the token URLs `/s/`, `/r/` and `/f/` share one public host, set by `PROXIER_BASE_URL`, behind a reverse proxy. Members can sign in from any network with a password. Authenticator codes and passkeys are optional. See [ADR 0017](./0017-passkeys-sign-in-alone.md).

Per-IP lockout, new-IP sign-in notifications, short sessions and CSRF protection limit the risks of a public sign-in page. The route prefixes let a reverse proxy move the UI or the token URLs to another host later. Changing the public base URL still changes every link URL.

When this was decided, one person signed in. Several members now do, each with their own password and a level in each area ([ADR 0018](./0018-several-members-with-per-area-access.md)); the decision holds for all of them.

## Considered options

- **The UI on the tailnet only**: fewer publicly reachable routes, but every member's device must be on the tailnet.
- **A separate public subdomain for link URLs**: lets a blocked or leaked link host be swapped without touching the UI. It remains possible later, though moving the public base URL changes every link URL.

## Consequences

- A stolen password gives what its member's grant reaches, unless their authenticator app is on. With `servers: manage` that is every server. Passkeys don't remove the risk: the password still signs in.
- Changing `PROXIER_BASE_URL` changes every link URL: the old name must keep serving until apps have refreshed from the new URLs.
