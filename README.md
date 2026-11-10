# Proxier

Proxier is a self-hosted control panel for proxy servers and domain routing. It builds VPS proxy servers from templates and checks whether they work from Russia. It provides subscription links for people and devices, syncs routing to MikroTik routers and Shadowrocket, and hosts router setup scripts.

It runs as one Go binary with a SQLite database and a templ + htmx UI in English and Russian.

![Proxier dashboard](docs/assets/screenshot-dashboard.png)

## Getting started

Save this as `compose.yaml`:

```yaml
services:
  proxier:
    image: ghcr.io/tikhonp/proxier:latest
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

The image comes from GitHub Container Registry. You can also [build it yourself](docs/deployment.md#images-and-ci).

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
