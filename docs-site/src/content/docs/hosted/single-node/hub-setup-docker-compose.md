---
title: Hub Setup via Docker Compose
description: Deploy a Scion Hub on any Linux host using docker-compose with Caddy for automatic TLS.
---

## Overview

`scripts/starter-hub-docker/` ships a self-contained compose stack
that pairs a single-replica Hub with [Caddy](https://caddyserver.com/)
for automatic Let's Encrypt TLS. This is the recommended starting
point when you want to self-host a Hub on a generic Linux box — your
laptop, a VPS, an internal server, anything that isn't tied to a
specific cloud provider.

Compared to the [GCE starter](/scion/hosted/single-node/hub-setup-gce/):

| | GCE starter | Docker Compose starter |
|---|---|---|
| Target | Google Compute Engine VM | any Linux + Docker |
| TLS | Caddy (provisioned by `gce-certs.sh`) | Caddy (in compose) |
| Process supervision | systemd | Docker daemon |
| Storage backend | GCS via `SCION_HUB_STORAGE_BUCKET` | local filesystem volume |
| Cloud bindings | gcloud, IAM, Cloud Logging | none |
| Backup unit | systemd job + GCS | single `hub-data` Docker volume |

Use the Compose starter when "any Linux box with Docker" is your
deployment target and you don't need cloud-native storage or identity.

## Prerequisites

- Linux server with **Docker** and **docker compose v2** (`docker compose version`).
- A DNS A record for your chosen domain (e.g. `hub.example.com`)
  pointing at the server's public IP.
- Ports **80** and **443** reachable from the public internet for
  Let's Encrypt's HTTP-01 challenge and serving traffic. Caddy
  redirects 80 → 443 automatically.
- The `scion-hub:latest` container image, built locally from this
  repo (no public registry image is available yet).

## 1. Build the images

The Hub container extends `scion-base`, which in turn extends
`core-base`. Build the full chain once:

```sh
git clone https://github.com/GoogleCloudPlatform/scion.git
cd scion
image-build/scripts/build-images.sh --target all
```

This produces `scion-hub:latest` plus the harness images (used by
brokers). On a fresh machine expect 15–30 min and 4–6 GB of disk for
the full chain.

If the Hub host will never run a broker, you can skip the harnesses
and build only the Hub chain:

```sh
image-build/scripts/build-images.sh --target core-base
image-build/scripts/build-images.sh --target scion-base
image-build/scripts/build-images.sh --target hub
```

To push images to a registry instead of relying on the local store,
add `--registry <your-registry> --push` and edit the `image:` line in
`docker-compose.yml` to match the registry path.

## 2. Build the Web UI bundle

The Hub binary in `scion-hub:latest` is compiled with the
`no_embed_web` build tag, so the React UI is **not** baked into the
image. The compose stack bind-mounts the bundle from your repo
checkout:

```sh
make web        # populates web/dist/client/
```

You can skip this step if you only plan to use the CLI and the Hub
API — the dashboard URL will show a placeholder page until the
bundle is mounted, but everything under `/api/v1/...` works without
it. Re-run after any update to the web sources.

## 3. Configure the stack

```sh
cd scripts/starter-hub-docker
cp .env.example .env
$EDITOR .env
```

Three required values:

| Variable | Purpose |
|---|---|
| `HUB_DOMAIN` | Public hostname clients will use. Must already resolve to this server. |
| `CADDY_EMAIL` | Address Let's Encrypt uses for cert expiry notices. |
| `SCION_SESSION_SECRET` | HMAC key for web session cookies. Generate with `openssl rand -hex 32`. |

`SCION_SESSION_SECRET` is critical for stability: it seeds the Hub's
JWT signing key persistence. If left unset, the Hub generates a fresh
random key on every restart, which invalidates all existing tokens
(broker registrations, agent identities, web sessions). Set it once
and keep it.

See the [LAN testing](#lan-testing) section below for the optional
`LAN_HOSTS` value.

## 4. Start the stack

```sh
docker compose up -d
docker compose logs -f hub
```

Caddy obtains a Let's Encrypt certificate on first boot (typically
15–60 s). The Hub logs print the dev token:

```
Dev token: export SCION_DEV_TOKEN=scion_dev_<long-random-token>
```

Verify health from the Hub host:

```sh
curl https://hub.example.com/healthz
# {"status":"healthy",...,"checks":{"database":"healthy"}}
```

## 5. Authenticate the CLI

Grab the dev token from the container:

```sh
docker compose exec hub cat /root/.scion/dev-token
```

On the machine you'll run `scion` from, edit `~/.scion/settings.yaml`:

```yaml
hub:
  enabled: true
  endpoint: https://hub.example.com
```

Then export the token and verify:

```sh
export SCION_DEV_TOKEN=<paste-the-token>
scion hub status
```

The Web Dashboard at `https://hub.example.com` accepts the same dev
token automatically when accessed from the host where it was
generated, or via the URL query string `?dev_token=...`.

## 6. Register your first runtime broker

The Hub itself does not run agents — it dispatches work to brokers.
Bring at least one broker online on any host with the `scion` binary
(your laptop, this same server, a Kubernetes node):

```sh
# On the broker host, with SCION_DEV_TOKEN and hub.endpoint set
scion broker start
scion broker register
scion broker provide --project <project-slug>
```

See the [Runtime Broker guide](/scion/hub-user/runtime-broker/) for the
broker lifecycle in more detail.

## State and persistence

A single named Docker volume — `hub-data` — holds everything stateful:

```
/root/.scion/
├── hub.db                 # SQLite: projects, agents, users, secrets, signing keys
├── storage/local/         # template + harness-config file blobs
└── dev-token              # the dev-auth shared admin token
```

(The Hub image runs as root by default, so `$HOME=/root`. If you
rebuild the image to drop privileges, the volume mount target needs
updating to match.)

To back up the Hub:

```sh
docker run --rm -v scion_hub-data:/data -v "$PWD":/backup alpine \
  tar -czf /backup/hub-backup-$(date +%F).tar.gz -C /data .
```

To restore on a new host:

```sh
# After completing steps 1–3 on the new host:
docker volume create scion_hub-data
docker run --rm -v scion_hub-data:/data -v "$PWD":/backup alpine \
  tar -xzf /backup/hub-backup-YYYY-MM-DD.tar.gz -C /data
docker compose up -d
```

Repoint DNS for `HUB_DOMAIN` at the new server. Active brokers
reconnect via outbound WebSocket; agent JWTs stay valid because the
signing keys live inside `hub.db` and migrated with it. Keep
`SCION_SESSION_SECRET` the same across machines if you want existing
web sessions to survive — otherwise users simply re-login.

## Day-2 operations

**Stop / restart:**

```sh
docker compose stop hub          # Hub only
docker compose restart           # everything
docker compose down              # stop and remove containers (volumes survive)
```

**Update the Hub:**

```sh
image-build/scripts/build-images.sh --target hub
make web                         # only if web sources changed
docker compose up -d hub
```

The container picks up the new image; SQLite and storage stay intact
in `hub-data`.

## LAN testing

For end-to-end testing from another machine on the same network
before you have public DNS, two patterns are bundled.

### Plain HTTP (no TLS gymnastics)

`docker-compose.lan.yml` is an override that publishes the Hub
directly on host port `8080` alongside the normal Caddy stack:

```sh
docker compose -f docker-compose.yml -f docker-compose.lan.yml up -d
# From a peer:  http://<this-host-LAN-IP>:8080
```

Suitable only for trusted networks — there's no encryption and
dev-auth is shared-secret.

### TLS via Caddy on additional hostnames

Set `LAN_HOSTS` in `.env` to a comma-prefixed list of extra
hostnames or IPs that should also resolve to this host:

```
LAN_HOSTS=, 192.168.50.10, hub.lan
```

The comma prefix is required — `LAN_HOSTS` is concatenated after
`HUB_DOMAIN` to form a multi-host Caddy site address. After
`docker compose up -d` peers can reach `https://192.168.50.10/`
or `https://hub.lan/`. Browsers warn because the certificate is
signed by Caddy's internal CA; click through or install the root:

```sh
docker exec scion-caddy cat /data/caddy/pki/authorities/local/root.crt \
  > caddy-root.crt
# Then add caddy-root.crt to the peer's system trust store.
```

The Caddyfile sets `default_sni $HUB_DOMAIN` so peers connecting by
IP address still complete the TLS handshake — RFC 6066 forbids
sending an IP literal as the SNI field, and without a fallback Caddy
would have no way to pick a matching site.

## Moving from dev-auth to OAuth

When dev-token becomes inadequate (multi-user, audit trail, browser
SSO), switch to OAuth:

1. Register an OAuth client at
   [Google Cloud Console](https://console.cloud.google.com/apis/credentials)
   or [GitHub Developer Settings](https://github.com/settings/developers).
2. Set the redirect URL to
   `https://hub.example.com/auth/google/callback` or
   `/auth/github/callback`.
3. Add credentials to `.env`:
   ```
   SCION_OAUTH_GOOGLE_CLIENT_ID=...
   SCION_OAUTH_GOOGLE_CLIENT_SECRET=...
   ```
4. Pass them through in `docker-compose.yml` under `hub.environment:`.
5. Drop `--dev-auth` from the Hub `command:` and add the OAuth
   provider flags. See the full [Authentication guide](/scion/hosted/single-node/auth/)
   for the production setup.
6. `docker compose up -d hub`.

Personal Access Tokens (created via the Web UI under your profile)
keep working across the switch — no broker re-registration required.

## Limitations

- **Single replica only.** The Hub uses SQLite for state. There's no
  PostgreSQL backend in the current release, so this stack is not
  HA-ready. `replicas > 1` will not work.
- **No embedded UI.** As noted above, the bundled image excludes the
  React bundle to keep build times short; you must run `make web`
  on the host and the bundle is bind-mounted in.
- **No runtime broker in the compose.** This is intentional — brokers
  are independent processes that connect outbound, so they can run
  on this same host (outside the compose), on a laptop, or on a
  Kubernetes node without changes to the compose stack.
- **GCP identity emulation is off.** The `SCION_METADATA_MODE`
  feature (per-agent GCP service accounts via emulated GCE metadata)
  requires extra setup; see the [GCE setup guide](/scion/hosted/single-node/hub-setup-gce/)
  if you need it.
