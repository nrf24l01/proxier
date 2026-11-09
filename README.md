# Proxier

A self-hosted control panel for one person's proxy setup: VPS proxy servers built from templates and checked from Russia, subscription links for people and devices, domain routing kept in sync on MikroTik routers and in Shadowrocket, and the router setup script. One Go binary, one SQLite file, a templ + htmx UI in English and Russian.

![Proxier dashboard](docs/assets/screenshot-dashboard.png)

## Getting started

Save this as `compose.yaml`:

```yaml
services:
  proxier:
    image: code.tikhonnnnn.com/tikhon/proxier:latest
    restart: unless-stopped
    ports:
      - "8080:8080"
    environment:
      PROXIER_MASTER_KEY: ${PROXIER_MASTER_KEY}
      PROXIER_BASE_URL: http://localhost:8080
    volumes:
      - proxier-data:/data

volumes:
  proxier-data:
```

Then:

```sh
echo "PROXIER_MASTER_KEY=$(openssl rand -base64 32)" > .env
docker compose up -d
docker compose exec proxier /bin/proxier manage create-admin
```

Open http://localhost:8080 and sign in. Keep a copy of `.env`. Without the master key, Proxier can't decrypt its secrets.

For real use, run it on a machine in Russia, put an HTTPS reverse proxy in front, and set `PROXIER_BASE_URL` to its public URL, since every link URL starts with it. [docs/deployment.md](docs/deployment.md) has the hardened compose file, the reverse proxy settings and the first steps in Settings.

## Development

Docs start at [docs/README.md](docs/README.md).

```sh
make test     # tests
make check    # templ generate, gofmt, vet, generated-file diff, tests
make dev      # hot reload on :8080 (needs .env, see .env.example)
```

AGPL-3.0, see [LICENSE.md](LICENSE.md).
