# hookrelay

[![CI](https://github.com/WickTech/hookrelay/actions/workflows/ci.yml/badge.svg)](https://github.com/WickTech/hookrelay/actions/workflows/ci.yml)
[![Coverage](https://img.shields.io/endpoint?url=https://raw.githubusercontent.com/WickTech/hookrelay/badges/coverage.json)](https://github.com/WickTech/hookrelay/actions/workflows/coverage.yml)
[![Go](https://img.shields.io/badge/go-1.26-blue)](https://go.dev)

Durable webhook relay. Accepts events, returns `202` immediately, then retries delivery with exponential backoff. Zero external dependencies — uses embedded [bbolt](https://github.com/etcd-io/bbolt) for durability.

## Features

- **202 Accepted** — producer never waits for downstream latency
- **HMAC-SHA256 signature verification** per target
- **Exponential backoff + jitter** (1s → cap 5 min), configurable max attempts
- **Dead Letter Queue** with replay and discard APIs
- **Prometheus metrics** on `/metrics`
- **Admin API** — list events, view delivery attempts (bearer-token protected)
- **Zero infrastructure** — single binary, embedded DB

## Quick start

```bash
cp targets.example.yaml targets.yaml
# edit targets.yaml with your target URLs and secrets
export HOOKRELAY_ADMIN_TOKEN=$(openssl rand -hex 32)
go run ./cmd/hookrelay targets.yaml
```

Send a webhook:
```bash
curl -X POST http://localhost:8080/hooks/my-service \
  -H "Content-Type: application/json" \
  -H "X-Hub-Signature-256: sha256=$(echo -n '{"event":"test"}' | openssl dgst -sha256 -hmac 'change-me-to-a-strong-secret' | awk '{print $2}')" \
  -d '{"event":"test"}'
# → 202 Accepted, X-Relay-ID: <uuid>
```

## Configuration

```yaml
# targets.yaml
host: ""            # "" = all interfaces; 127.0.0.1 = local only
port: 8080
db_path: hookrelay.db
admin:
  token: ""         # bearer token for /events, /dlq, /metrics (prefer the env var)

targets:
  - id: my-service
    url: https://my-service.example.com/webhooks
    secret: strong-shared-secret
    max_attempts: 10
    timeout_ms: 5000
```

Environment overrides: `HOOKRELAY_ADMIN_TOKEN` (admin.token), `HOOKRELAY_HOST` (host),
`HOOKRELAY_DB_PATH` (db_path).

### Admin authentication

`/events*`, `/dlq*` and `/metrics` require `Authorization: Bearer <admin.token>`.
Missing or wrong tokens get `401` with `WWW-Authenticate: Bearer`. `/hooks/{target-id}`
stays public (each request is verified by its HMAC signature) and `/health` stays open.

hookrelay **refuses to start** when `admin.token` is empty unless `host` is a loopback
address (`127.0.0.1`, `::1`, `localhost`).

```bash
curl -H "Authorization: Bearer $HOOKRELAY_ADMIN_TOKEN" http://localhost:8080/dlq
```

Prometheus scrape config:

```yaml
scrape_configs:
  - job_name: hookrelay
    authorization:
      type: Bearer
      credentials_file: /etc/prometheus/hookrelay_admin_token
    static_configs:
      - targets: ["hookrelay:8080"]
```

## API

| Method | Path | Auth | Description |
|---|---|---|---|
| `POST` | `/hooks/{target-id}` | HMAC signature | Ingest webhook event |
| `GET` | `/health` | none | Health check |
| `GET` | `/events` | bearer | List events (`?target=&status=&limit=&cursor=`) |
| `GET` | `/events/{id}` | bearer | Event detail + delivery attempts |
| `GET` | `/dlq` | bearer | List Dead Letter Queue (`?limit=&cursor=`) |
| `POST` | `/dlq/{id}/replay` | bearer | Re-enqueue a DLQ event |
| `DELETE` | `/dlq/{id}` | bearer | Discard a DLQ event |
| `GET` | `/metrics` | bearer | Prometheus metrics |

## Architecture

```
Producer  →  POST /hooks/{target}  →  202 (persisted to bbolt)
                                            ↓
                                     Delivery worker
                                     (retry + backoff)
                                            ↓
                                       Target URL  ←→  DLQ on max failure
```

## Docker

```bash
docker pull ghcr.io/wicktech/hookrelay:latest
docker run -p 8080:8080 \
  -e HOOKRELAY_ADMIN_TOKEN="$(openssl rand -hex 32)" \
  -v $(pwd)/targets.yaml:/targets.yaml:ro \
  -v hookrelay-data:/data \
  ghcr.io/wicktech/hookrelay:latest
```

The image runs as UID/GID `65532` (distroless `nonroot`) and stores the bbolt database at
`/data/hookrelay.db`. A named volume inherits the right ownership; for a bind mount, make
the host directory writable by `65532` (`sudo chown 65532:65532 ./data`).

## Phases

- [x] P1 — Scaffold, ingest, bbolt store, HMAC verify
- [x] P2 — Delivery worker, retry/backoff, DLQ
- [x] P3 — Admin API, Prometheus metrics, health
- [x] P4 — e2e tests (in-process), GoReleaser, GHCR image
- [x] P5 — Fuzz tests, benchmarks, coverage badge
