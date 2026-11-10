# VLESS over XHTTP (the seed template and endpoint type)

The endpoint type `vless-xhttp-tls` uses the seed template "VLESS XHTTP behind nginx". Proxier renders the stack files and runs the installation steps.

## The stack

```
client --TLS:443--> nginx --/<xhttp-path>/...--> xray (VLESS over XHTTP, h2c via grpc_pass)
                          --anything else-------> decoy one-page site
```

| Service | Role |
|---|---|
| `nginx` (`nginx:stable-alpine`) | Terminates TLS with the Let's Encrypt certificate. Rejects handshakes without the right SNI (`ssl_reject_handshake` on the default server), so the domain can't be learned from the IP. Forwards only `<xhttp-path>/` to xray over h2c (`grpc_pass`), serves the decoy site for everything else, and reloads every 6 h to pick up renewed certificates. |
| `xray` (`ghcr.io/xtls/xray-core`) | VLESS inbound on `10001` with `network: xhttp`, `security: none`, the secret path, `mode: auto`, and padding `100-1000` bytes. No published ports. Sniffing on (`http`, `tls`, `quic`), so xray dials the domain a connection names rather than the address the client resolved, and a `freedom` outbound that resolves it IPv4 first (`sockopt.domainStrategy: UseIPv4v6`): a server whose containers have no IPv6 still reaches a site the client chose to reach over IPv6 (Hiddify 4.1.1 on Android sends IPv6 destinations even with its IPv6 off). |
| `certbot` (`certbot/certbot`) | Renews the certificate every 12 h via webroot. The first certificate is issued by the install step in standalone mode, because nginx can't start without one. |

## Template contents

| File | Purpose |
|---|---|
| `compose.yaml` | Runs nginx and xray. |
| `nginx/default.conf.template` | Routes the secret XHTTP path to xray. `${SERVER_DOMAIN}` and `${VLESS_XHTTP_PATH}` come from `.env` through nginx's envsubst. |
| `site/index.html` | The decoy site. |
| `xray-config.json` | Renders `clients` from `.Clients` and `path` from `.Gen.xhttp_path`. See [the client list](../modules/servers.md#the-client-list). |
| `.env` | Renders `CONTAINER_POSTFIX`, `SERVER_DOMAIN` and `VLESS_XHTTP_PATH` from server and generated values. |
| `issue-cert.sh` | Issues the TLS certificate. It exits early when a certificate exists and adds an email only when the parameter is set. |

Proxier installs Docker, SSH keys and the firewall through base bootstrap. It creates DNS records for the server hostname, waits for resolution, renders the stack and runs the template's steps. The template takes an optional `letsencrypt_email` parameter and generates `client_uuid`, `xhttp_path` and `container_postfix`. Connection details appear on the server page and in subscription outputs.

## Connection URI

The endpoint produces this URI:

```
vless://{uuid}@{host}:443?encryption=none&security=tls&sni={host}&fp=chrome&host={host}&alpn=h2&type=xhttp&path=%2F{path-without-leading-slash}&mode=stream-up#{display-name}
```

| Part | Value |
|---|---|
| `uuid` | the endpoint credential (`.Gen.client_uuid`) |
| `host` | the proxy hostname |
| `path` | the secret path. It is hex, so only the leading `/` needs encoding. |
| `mode=stream-up` | One streaming POST through nginx's `grpc_pass`. The server accepts any mode, so a client may switch to `packet-up` (e.g. behind a CDN). |
| `fp=chrome`, `alpn=h2` | as in the original |
| `#display-name` | percent-encoded UTF-8, e.g. `%F0%9F%87%B3%F0%9F%87%B1%20Netherlands%201` |

## Checks for this template

- **Self-check**: `compose-running` (nginx, xray, certbot running), `http-local` (the decoy site returns `200` when asked for the proxy hostname on the server itself), `cert-expiry` on `certbot/conf/live/<host>/fullchain.pem`, `disk-free`.
- **Proxy test**: three steps ([health](../processes/servers/server-health.md#checks)). xray's own errors are opaque, so two direct probes make the class of a network failure certain before xray is involved:
  1. a plain TCP connection to `host:443` (5 s): no answer in time → `tcp-timeout`, a refusal → `tcp-refused`;
  2. a TLS handshake with the endpoint's SNI, ALPN and **fingerprint** (the same ClientHello a real client sends, so a network that treats a Go handshake differently from Chrome's does not mislead the probe), checking the certificate (5 s) → `tls-failed`;
  3. the test object downloaded through the embedded xray client, which builds an outbound from the endpoint (XHTTP in the endpoint's mode, TLS with `fp` and `alpn`), with a 15 s overall timeout and a 5 s stall timer that restarts whenever data arrives: the stall timer fires → `stalled` (with the bytes received), the overall one → `timeout`, a non-2xx answer or any other failure of the request (a refused credential shows as a connection closed with no answer) → `http-error` (the error text says which).

  A failing probe ends the test: step 3 would only repeat it less clearly. Success records the connect and TLS times of the probes, the time to the first byte and the throughput of step 3, and the bytes received; a body under 64 KiB carries a "small object" warning, because throttling after ~20 KB cannot be seen on it.

## Ports and firewall

`ports: [80/tcp, 443/tcp]`. Port 80 serves ACME challenges and redirects to HTTPS. 443 is the endpoint. SSH stays on the server's SSH port.
