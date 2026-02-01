[![Discord](https://img.shields.io/discord/1459154799407665397?label=Join%20Discord)](https://hybrowse.gg/discord)
[![Docker Pulls](https://img.shields.io/docker/pulls/hybrowse/hytale-session-token-broker)](https://hub.docker.com/r/hybrowse/hytale-session-token-broker)

# Hytale Session Token Broker

Small Go service + CLI that authenticates against Hytale via OAuth Device Flow, persists refresh tokens, and mints short-lived game session tokens for your servers.

## Why this exists

The official Hytale server requires authentication to allow player connections. For a single manually operated server, doing this interactively can be fine.

For **providers / fleets / networks**, interactive auth becomes a bottleneck:

- you want **non-interactive startup** (no console attach)
- you want **one place to manage credentials**
- you want to **spread load** across multiple Hytale accounts and profiles

This broker solves that by:

- keeping long-lived OAuth credentials in one place (persisted refresh tokens)
- minting short-lived `session_token` + `identity_token` on demand via HTTP

## Hybrowse Server Stack

This broker is part of the **Hybrowse Server Stack** — production-grade building blocks for running Hytale at scale:

- [Hybrowse/hytale-server-docker](https://github.com/Hybrowse/hytale-server-docker) — can fetch `session_token` / `identity_token` from this broker at container startup to skip interactive `/auth`
- [Hybrowse/hyrouter](https://github.com/Hybrowse/hyrouter) — stateless QUIC entrypoint and referral router for routing players to backends

## Image

- **Image (Docker Hub)**: [`hybrowse/hytale-session-token-broker`](https://hub.docker.com/r/hybrowse/hytale-session-token-broker)
- **Mirror (GHCR)**: [`ghcr.io/hybrowse/hytale-session-token-broker`](https://ghcr.io/hybrowse/hytale-session-token-broker)

## Community

Join the **Hybrowse Discord Server** to get help and stay up to date: https://hybrowse.gg/discord

## How it works

- **Broker owns long-lived credentials** (refresh token) and stores them in a local JSON state file.
- **Game servers request short-lived tokens** via HTTP `POST /v1/game-session`.
- **Access tokens are refreshed automatically** when close to expiry.

## Token lifetimes and broker downtime

- **Access tokens** are short-lived (typically ~1 hour). The persisted `access_token_expires_at` timestamp is stored as RFC3339 and usually in **UTC** (suffix `Z`).
- **Refresh tokens** are the long-lived credential. The broker persists them in `state.json` and will refresh access tokens automatically on demand.

The refresh token lifetime is not exposed by the API (no fixed TTL is documented), so the practical signal is whether refresh starts failing (for example `invalid_grant`).

The broker can be stopped for longer than an hour and still work after restart, as long as the refresh token is still valid and `state.json` is preserved.

You typically only need manual intervention when:

- the broker lost its state file (missing/unmounted `/app/data/state.json`), or
- the refresh token becomes invalid/revoked (then run `auth-login-device` again).

## Configuration

The broker reads YAML config (default path in container: `/app/config.yaml`). See `config.yaml` in this repo.

Important fields:

- **`http.addr`**: listen address (default `:8080`)
- **`http.bearer_token`**: optional auth for the HTTP API (can be overridden via env var)
- **`store.path`**: path to JSON state file (default `/app/data/state.json`)

Bearer token via env (recommended):

Set `HYTALE_SESSION_TOKEN_BROKER_BEARER_TOKEN` and keep `http.bearer_token` empty in `config.yaml`.

## Operational model (mental model)

- **Accounts** are named slots (e.g. `default`, `account1`, `account2`) that each hold OAuth refresh/access tokens.
- Each account can have **multiple profiles** (Hytale-side). The broker can mint sessions from any of them.
- A mint request targets either:
  - a specific account (`{"account":"account1"}`), or
  - **any authenticated account** (`{"account":"any"}` or omit the field)

This gives you:

- **Capacity / HA** by using multiple profiles per account (fallback) and multiple accounts (fallback)
- **Load-spreading** via round-robin

## Accounts (what they are)

An **account** is just a **named slot** (e.g. `default`, `account1`, `account2`) that the broker uses to:

- store OAuth tokens in `state.json` under `accounts.<name>`
- store per-account defaults (e.g. default profile pool + round-robin cursor)

You can have multiple accounts authenticated at the same time.

When minting a session you can either:

- pin a specific account by setting `account` in the request, or
- omit `account` (or set it to `"any"`) to let the broker pick **any authenticated account**.

The broker uses **round-robin across authenticated accounts** and persists the cursor in `state.json` (`next_account_index`).

## Profiles

Profiles are always configured as a **pool** via `profile_uuids` (even if the pool has only 1 entry).

This pool is used for:

- **Fallback**: if minting fails for a profile, the broker tries the next.
- **Round-robin**: across requests the broker rotates which profile is tried first (persisted per account in `state.json` via `next_profile_index`).

### Resolution / priority order

When minting a session, the broker picks profile candidates in this order:

1. **Explicit `profile_uuids` argument** (pool, no rotation)
2. **Persisted state pool** set via CLI `set-profiles` (pool + round-robin)
3. **Config `accounts.<name>.profile_uuids`** (pool + round-robin)
4. **Config `defaults.profile_uuids`** (pool)
5. Otherwise: broker fetches profiles from the Hytale API.
   - If multiple profiles exist, they are all used as candidates (round-robin + fallback).

### Example config (multi-profile)

```yaml
http:
  addr: ":8080"
  bearer_token: ""  # prefer setting HYTALE_SESSION_TOKEN_BROKER_BEARER_TOKEN instead
store:
  path: "/app/data/state.json"
oauth:
  client_id: "hytale-server"
  scope: "openid offline auth:server"
  device_auth_url: "https://oauth.accounts.hytale.com/oauth2/device/auth"
  token_url: "https://oauth.accounts.hytale.com/oauth2/token"
hytale:
  account_data_base_url: "https://account-data.hytale.com"
  sessions_base_url: "https://sessions.hytale.com"

accounts:
  default:
    profile_uuids:
      - "11111111-1111-1111-1111-111111111111"
      - "22222222-2222-2222-2222-222222222222"

defaults:
  account: "default"
```

### Example config (multiple accounts)

```yaml
accounts:
  account1:
    profile_uuids:
      - "11111111-1111-1111-1111-111111111111"
  account2:
    profile_uuids:
      - "22222222-2222-2222-2222-222222222222"

defaults:
  account: "account1"
```

## CLI usage (local)

### 1) Start device login

This will print a URL + code. Complete the login in your browser.

```sh
go run ./cmd/hytale-session-token-broker -config config.yaml auth-login-device
```

Login a second account:

```sh
go run ./cmd/hytale-session-token-broker -config config.yaml auth-login-device account2
```

### 2) Check auth status

```sh
go run ./cmd/hytale-session-token-broker -config config.yaml auth-status
```

### 3) List profiles on the account

```sh
go run ./cmd/hytale-session-token-broker -config config.yaml profiles
```

### 4) Persist default profile(s) (optional)

```sh
go run ./cmd/hytale-session-token-broker -config config.yaml set-profiles <uuid1,uuid2,uuid3>
```

### 5) Run the HTTP server

```sh
go run ./cmd/hytale-session-token-broker -config config.yaml serve
```

## HTTP API

### Health

```sh
curl -sS http://localhost:8080/healthz
```

### Mint a game session

Minimal (any authenticated account + any profile):

```sh
curl -sS \
  -X POST http://localhost:8080/v1/game-session \
  -H 'Content-Type: application/json' \
  -d '{}'
```

Choose a specific account (when multiple are logged in):

```sh
curl -sS \
  -X POST http://localhost:8080/v1/game-session \
  -H 'Content-Type: application/json' \
  -d '{"account":"account2"}'
```

Use a pool for fallback (tries first profile, then second if minting fails):

```sh
curl -sS \
  -X POST http://localhost:8080/v1/game-session \
  -H 'Content-Type: application/json' \
  -d '{"account":"default","profile_uuids":["11111111-1111-1111-1111-111111111111","22222222-2222-2222-2222-222222222222"]}'
```

If `http.bearer_token` is configured:

```sh
curl -sS \
  -X POST http://localhost:8080/v1/game-session \
  -H 'Authorization: Bearer YOUR_TOKEN' \
  -H 'Content-Type: application/json' \
  -d '{}'
```

Response:

```json
{
  "session_token": "...",
  "identity_token": "...",
  "expires_at": "..."
}
```

### Error responses

All error responses are JSON:

```json
{"error":"..."}
```

Common cases:

- **401 Unauthorized (HTTP bearer token)**

```json
{"error":"unauthorized"}
```

- **401 Unauthorized (account not authenticated)**

```json
{"error":"account \"default\" is not authenticated"}
```

- **401 Unauthorized (re-auth required)**

```json
{"error":"account \"default\" needs re-authentication (invalid_grant)"}
```

- **400 Bad Request (invalid JSON body)**

```json
{"error":"invalid json body"}
```

- **405 Method Not Allowed**

```json
{"error":"method not allowed"}
```

## Integration with the Hytale Server Docker Image

- Repository: https://github.com/Hybrowse/hytale-server-docker
 
 The server image supports fetching broker tokens at container startup.

### Minimal Docker Compose

```yaml
services:
  broker:
    image: hybrowse/hytale-session-token-broker:latest
    environment:
      HYTALE_SESSION_TOKEN_BROKER_BEARER_TOKEN: "" # optional; see secrets example below
    volumes:
      - ./broker-data:/app/data
      - ./config.yaml:/app/config.yaml:ro
    ports:
      - "8080:8080"
    restart: unless-stopped

  hytale:
    image: hybrowse/hytale-server:latest
    environment:
      HYTALE_SESSION_TOKEN_BROKER_ENABLED: "true"
      HYTALE_SESSION_TOKEN_BROKER_URL: "http://broker:8080"
      HYTALE_SESSION_TOKEN_BROKER_TIMEOUT_SECONDS: "10"
    volumes:
      - ./hytale-data:/data
    ports:
      - "5520:5520/udp"
    tty: true
    stdin_open: true
    restart: unless-stopped
```

### With HTTP bearer token (recommended via file-based secret)

```yaml
services:
  broker:
    image: hybrowse/hytale-session-token-broker:latest
    secrets:
      - broker_bearer
    environment:
      HYTALE_SESSION_TOKEN_BROKER_BEARER_TOKEN_SRC: "/run/secrets/broker_bearer"
    volumes:
      - ./broker-data:/app/data
      - ./config.yaml:/app/config.yaml:ro

  hytale:
    image: hybrowse/hytale-server:latest
    secrets:
      - broker_bearer
    environment:
      HYTALE_SESSION_TOKEN_BROKER_ENABLED: "true"
      HYTALE_SESSION_TOKEN_BROKER_URL: "http://broker:8080"
      HYTALE_SESSION_TOKEN_BROKER_BEARER_TOKEN_SRC: "/run/secrets/broker_bearer"

secrets:
  broker_bearer:
    file: ./secrets/broker_bearer_token
```

If you set `http.bearer_token` in the broker config (or via env), the broker expects:

- `Authorization: Bearer <token>`

## End-to-end (broker + server)

This is the typical workflow for a non-interactive server startup.

### 1) Start the broker

Create a directory structure:

- `./broker-data/` (persistent state)
- `./config.yaml` (broker config)

Start the broker (Compose example from above) and wait until it listens on `:8080`.

### 2) Authenticate the broker account(s)

Run device login inside the broker container:

```sh
docker exec -it <broker-container> \
  hytale-session-token-broker -config /app/config.yaml auth-login-device default
```

Follow the printed URL/code in your browser.

Verify status:

```sh
docker exec -it <broker-container> \
  hytale-session-token-broker -config /app/config.yaml auth-status default
```

Optional (inspect profiles and/or pin a deterministic pool):

```sh
docker exec -it <broker-container> \
  hytale-session-token-broker -config /app/config.yaml profiles default

docker exec -it <broker-container> \
  hytale-session-token-broker -config /app/config.yaml set-profiles <uuid1,uuid2,uuid3> default
```

### 3) Start the server with broker enabled

Start `hybrowse/hytale-server` with:

- `HYTALE_SESSION_TOKEN_BROKER_ENABLED=true`
- `HYTALE_SESSION_TOKEN_BROKER_URL=http://broker:8080`

On startup, the server container will request `{ "session_token", "identity_token" }` from the broker.

If you configured a broker bearer token, also provide it to the server via `HYTALE_SESSION_TOKEN_BROKER_BEARER_TOKEN_SRC`.

### 4) Quick sanity check (manual broker request)

From anywhere that can reach the broker:

```sh
curl -sS -X POST http://localhost:8080/v1/game-session \
  -H 'Content-Type: application/json' \
  -d '{}'
```

## Notes for providers / fleets

- If you have multiple accounts authenticated in the broker, use `account:"any"` (or omit) to load-spread.
- If you have multiple profiles per account, the broker will automatically fall back to another profile on mint failures.
- For deterministic behavior, pin a profile pool via `set-profiles`.

## Docker

Build:

```sh
docker build -t hytale-session-token-broker:local .
```

Run:

```sh
docker run --rm -p 8080:8080 hytale-session-token-broker:local
```

Persist state on the host (recommended):

```sh
docker run --rm \
  -p 8080:8080 \
  -v "$(pwd)/local:/app/data" \
  hytale-session-token-broker:local
```

## Development

Useful tasks:

```sh
task fmt
task lint
task test
task cover
task ci
```

## Contributing & Security
 
- [`CONTRIBUTING.md`](CONTRIBUTING.md)
- [`LICENSING.md`](LICENSING.md)
- [`SECURITY.md`](SECURITY.md)

## Legal and policy notes

This is an **unofficial** community project and is not affiliated with or endorsed by Hypixel Studios Canada Inc.

This repository and image do not redistribute proprietary Hytale game/server files.
Server operators are responsible for complying with the Hytale EULA, Terms of Service, and Server Operator Policies (including monetization and branding rules): https://hytale.com/server-policies

## License
 
Current repository license: [`LICENSE`](LICENSE)

Non-binding summary (see [`LICENSE`](LICENSE) for the full, binding terms):
 
- Free production use is permitted for self-operated communities that are not a managed service, within the license’s free-use thresholds (currently: Peak CCU <= 250 and Peak Game Server Count <= 10).
- Managed-service/hosting usage requires a commercial agreement.

See also: [`NOTICE`](NOTICE).

For an overview (including commercial agreements and trademarks), see:

- [`LICENSING.md`](LICENSING.md)
- [`COMMERCIAL_LICENSE.md`](COMMERCIAL_LICENSE.md)
- [`TRADEMARKS.md`](TRADEMARKS.md)
