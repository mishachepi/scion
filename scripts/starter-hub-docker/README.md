# Scion Hub via Docker Compose (dev-auth)

Minimal compose stack to run a Scion Hub behind Caddy with automatic
Let's Encrypt TLS. Starts in `--dev-auth` mode (single shared admin
token, no OAuth config required) — good for solo and small-team
operation, swap for OAuth later.

The narrative version with deployment context lives in the user-facing
docs: [Hub Setup (Docker Compose)](../../docs-site/src/content/docs/hosted/single-node/hub-setup-docker-compose.md).
This README is the quick reference next to the files themselves.

State:
- `hub-data` volume → `/root/.scion` inside the Hub container (the
  image runs as root by default, so `$HOME=/root`). Contains
  `hub.db` (SQLite), `storage/local/` (templates and harness
  configs), `dev-token`, signing keys. **This is the only thing you
  need to back up.**
- `caddy-data` volume → Let's Encrypt account + certs.
- `caddy-config` volume → Caddy runtime config.

## Prerequisites

- Linux server with Docker + docker compose v2.
- A DNS A record for `HUB_DOMAIN` (e.g. `hub.example.com`) pointing at
  the server's public IP.
- Ports **80** and **443** open to the public internet (Let's Encrypt
  HTTP-01 + serving traffic). The Hub itself is not exposed directly.
- A locally-built `scion-hub:latest` image (see step 1).

## 1. Build the Hub image on the server

Clone the repo, then build the full image chain:

```sh
git clone https://github.com/GoogleCloudPlatform/scion.git
cd scion
image-build/scripts/build-images.sh --target all
# Rebuilds core-base → scion-base → harnesses + hub. Tagged
# scion-hub:latest in the local docker engine. Takes 15–30 min on
# a fresh machine; harness images add ~2–4 GB of disk.
```

`--target hub` alone is faster but assumes `scion-base:latest` already
exists locally — use it for incremental rebuilds, not first-time setup.

If you only ever run Hub here (brokers live on other hosts), you can
skip the harness images by building sequentially:

```sh
image-build/scripts/build-images.sh --target core-base
image-build/scripts/build-images.sh --target scion-base
image-build/scripts/build-images.sh --target hub
```

If you want to build elsewhere and push: pass `--registry <your-registry> --push`
and change the `image:` line in `docker-compose.yml` to match.

## 2. Build the Web UI bundle

The Hub image is built with `-tags no_embed_web` (see
`image-build/scion-base/Dockerfile`), so the binary ships without the
React app. The compose mount its assets in from the repo:

```sh
make web        # produces web/dist/client/, bind-mounted into the Hub
```

The Hub API and CLI work fine without this — but the Web Dashboard
will return a placeholder page until you do this once. Re-run after
updating any web source.

## 3. Configure

```sh
cd scripts/starter-hub-docker
cp .env.example .env
$EDITOR .env
# Set HUB_DOMAIN, CADDY_EMAIL.
# Generate SCION_SESSION_SECRET: openssl rand -hex 32
```

## 4. Start

```sh
docker compose up -d
docker compose logs -f hub
```

Caddy will obtain a certificate on first boot (15–60s). The Hub logs
will print the generated dev token:

```
Dev token: export SCION_DEV_TOKEN=<long-random-token>
```

## 5. Grab the dev token and authenticate your local CLI

On the server:

```sh
docker compose exec hub cat /root/.scion/dev-token
```

On your laptop, edit `~/.scion/settings.yaml`:

```yaml
hub:
  enabled: true
  endpoint: https://hub.example.com
```

Then in your shell (or your shell rc):

```sh
export SCION_DEV_TOKEN=<paste-the-token>
# verify
scion hub status
```

You should see a successful connection. Open `https://hub.example.com`
in a browser — the dev token is auto-applied for the Web Dashboard
when it's set in the URL (`?dev_token=...`) or via the same env on the
machine running the CLI.

## 6. Register your first runtime broker

The Hub alone runs no agents — it just routes commands. Bring at least
one runtime broker online (your laptop, this same server, a k8s
machine — any host with the `scion` binary):

```sh
# on the broker host, with SCION_DEV_TOKEN + hub.endpoint set as above
scion broker start
scion broker register
scion broker provide --project <project-slug>
```

## Day-2

**Stop / restart:**

```sh
docker compose stop hub        # Hub only
docker compose restart         # everything
docker compose down            # stop and remove containers (volumes survive)
```

**Update Hub binary** (after rebuilding the image):

```sh
image-build/scripts/build-images.sh --target hub
docker compose up -d hub
```

The container restart picks up the new image; SQLite and storage stay
intact in the named volume.

**Backup:**

```sh
docker run --rm -v scion_hub-data:/data -v "$PWD":/backup alpine \
  tar -czf /backup/hub-backup-$(date +%F).tar.gz -C /data .
```

**Restore on a different server:**

1. Provision a fresh server, complete steps 1–2 above (same
   `SCION_SESSION_SECRET` if you want existing web sessions to keep
   working — otherwise users just relogin).
2. `docker volume create scion_hub-data`
3. `docker run --rm -v scion_hub-data:/data -v "$PWD":/backup alpine tar -xzf /backup/hub-backup-YYYY-MM-DD.tar.gz -C /data`
4. `docker compose up -d`

Update DNS to point `HUB_DOMAIN` at the new server. Brokers
auto-reconnect via outbound WebSocket; agent JWTs stay valid because
the signing keys live inside `hub.db` and migrated with it.

## Moving from dev-auth to OAuth

When you outgrow dev-token, add Google or GitHub OAuth:

1. Create an OAuth client at https://console.cloud.google.com/apis/credentials
   (or https://github.com/settings/developers).
2. Set the redirect URL to `https://hub.example.com/auth/google/callback`
   (or `/auth/github/callback`).
3. Add to `.env`:
   ```
   SCION_OAUTH_GOOGLE_CLIENT_ID=...
   SCION_OAUTH_GOOGLE_CLIENT_SECRET=...
   ```
4. Pass them through in `docker-compose.yml` `hub.environment:`.
5. Drop `--dev-auth` from `command:` and add `--enable-oauth` (see
   `docs-site/src/content/docs/hosted/single-node/auth.md` for full setup).
6. `docker compose up -d hub`.

Existing PATs (created via the Web UI under your profile) keep working
across the switch.

## LAN testing

Two opt-in patterns for reaching the Hub from another machine on the
same network before you have a real domain.

**Plain HTTP** (fastest, no TLS gymnastics) — use the bundled override:

```sh
docker compose -f docker-compose.yml -f docker-compose.lan.yml up -d
# From a peer:  http://<this-host-LAN-IP>:8080
```

The override publishes the Hub directly on host port 8080 alongside
the normal Caddy stack.

**TLS via Caddy** — set `LAN_HOSTS` in `.env`:

```
LAN_HOSTS=, 192.168.50.10
```

The comma prefix is mandatory — `LAN_HOSTS` is concatenated after
`HUB_DOMAIN` in the Caddyfile to form a multi-host site definition.
After `docker compose up -d`, peers can reach
`https://192.168.50.10/`. Browsers will warn about the certificate
(it's signed by Caddy's local CA) — click through, or trust the CA
from `docker exec scion-caddy cat /data/caddy/pki/authorities/local/root.crt`.

A `default_sni HUB_DOMAIN` directive in the Caddyfile handles the
fact that browsers and curl don't send SNI when the host is an IP
literal (RFC 6066) — without it Caddy would abort the handshake.

## Notes

- This stack runs **Hub + Web only**. It does NOT run a runtime broker
  inside compose. Brokers are intentionally separate processes — they
  can live on this same server (run `scion broker start` outside the
  container), on your laptop, or on a k8s node.
- Caddy uses Let's Encrypt by default. For staging certs (avoid rate
  limits while you experiment), add `acme_ca https://acme-staging-v02.api.letsencrypt.org/directory`
  to the global block in `Caddyfile`.
- For an internal-only Hub (no public DNS), replace Caddy with your
  own reverse proxy and self-signed cert, or bind Hub directly to
  `127.0.0.1:8080` and front it with SSH tunneling.
