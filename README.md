# hookrelay

[![CI](https://github.com/WickTech/hookrelay/actions/workflows/ci.yml/badge.svg)](https://github.com/WickTech/hookrelay/actions/workflows/ci.yml)
[![Go](https://img.shields.io/badge/go-1.25-blue)](https://go.dev)

Durable webhook relay. Accepts events, returns `202` immediately, then retries delivery with exponential backoff. Zero external dependencies — uses embedded [bbolt](https://github.com/etcd-io/bbolt) for durability.

## Features

- **202 Accepted** — producer never waits for downstream latency
- **HMAC-SHA256 signature verification** per target
- **Exponential backoff + jitter** (1s → cap 5 min), configurable max attempts
- **Dead Letter Queue** with replay and discard APIs
- **Prometheus metrics** on `/metrics`
- **Admin API** — list events, view delivery attempts
- **Zero infrastructure** — single binary, embedded DB

## Quick start

```bash
cp targets.example.yaml targets.yaml
# edit targets.yaml with your target URLs and secrets
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
port: 8080
db_path: hookrelay.db

targets:
  - id: my-service
    url: https://my-service.example.com/webhooks
    secret: strong-shared-secret
    max_attempts: 10
    timeout_ms: 5000
```

## API

| Method | Path | Description |
|---|---|---|
| `POST` | `/hooks/{target-id}` | Ingest webhook event |
| `GET` | `/health` | Health check |
| `GET` | `/events` | List events (`?target=&status=&limit=&cursor=`) |
| `GET` | `/events/{id}` | Event detail + delivery attempts |
| `GET` | `/dlq` | List Dead Letter Queue (`?limit=&cursor=`) |
| `POST` | `/dlq/{id}/replay` | Re-enqueue a DLQ event |
| `DELETE` | `/dlq/{id}` | Discard a DLQ event |
| `GET` | `/metrics` | Prometheus metrics |

## Architecture

```
Producer  →  POST /hooks/{target}  →  202 (persisted to bbolt)
                                            ↓
                                     Delivery worker
                                     (retry + backoff)
                                            ↓
                                       Target URL  ←→  DLQ on max failure
```

## Phases

- [x] P1 — Scaffold, ingest, bbolt store, HMAC verify
- [ ] P2 — Delivery worker, retry/backoff, DLQ
- [ ] P3 — Admin API, Prometheus metrics, health
- [ ] P4 — e2e with walletd, GoReleaser, GHCR
- [ ] P5 — Fuzz, race, benchmarks, coverage badge
