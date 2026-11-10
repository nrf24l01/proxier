# Passkeys sign in alone; the authenticator app guards the password; both are opt-in

Members can add passkeys and enable an authenticator app on Settings → Security. A passkey signs in without a username, password or code and requires user verification on the device. An authenticator app adds a 6-digit code to password sign-in. With neither enabled, a password is enough.

Password sign-in remains available. To restore access after losing a phone or passkeys, run `proxier manage reset-2fa <username>` in the container. This requires the same shell access as `reset-password`.

This was decided when one person signed in. Each member now has their own passkeys and authenticator app ([ADR 0018](./0018-several-members-with-per-area-access.md)); nothing else changed.

## Considered options

- **A passkey as a second step after the password**: no gain over a passkey alone (the passkey already proves two factors) and slower every time.
- **A "passkeys only" switch** that turns password sign-in off: the strongest setup, but one more state to lock oneself out with, for members who can already keep the password long and the app on. Not now.
- **Recovery codes** for the authenticator app: another secret to store and keep safe, when the container shell already restores access and is needed for `reset-password` anyway.

## Consequences

- A stolen password alone still signs in unless the app is on. Passkeys make phishing a member harder, not the password stronger.
- Changing authentication methods requires the current password and sends a notification. A stolen session alone cannot add a new method.
- The relying party is `PROXIER_BASE_URL`. Moving Proxier to another host name makes every passkey useless there: sign in with the password and add them again.
- Failed codes and passkeys count toward the same per-IP lockout as failed passwords; a right password before its code doesn't reset the count.
