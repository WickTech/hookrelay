# hookrelay — Spec

**Status:** planned  
**Language:** Go (latest stable)  
**DB:** bbolt (embedded, zero-infra)  
**Estimated effort:** 4–5 sessions  
**GitHub:** WickTech/hookrelay (public)

---

## Problem

Webhook producers (e.g. walletd) fire-and-forget. If the consumer is down, the event is lost.  
hookrelay accepts the event, returns 202 immediately, then durably retries delivery until success or max attempts.

---

## Core Features

### 1. Inbound endpoint
- `POST /hooks/{target-id}` — accepts any JSON body
- Returns `202 Accepted` + `X-Relay-ID: <uuid>` immediately
- Validates HMAC-SHA256 signature from `X-Hub-Signature-256` header (shared secret per target)
- Rejects with `401` if signature invalid

### 2. Durable delivery
- Persists event to bbolt before returning 202
- Worker goroutine picks up pending events, POSTs to registered target URL
- On success (2xx): marks delivered, stores response status + timestamp
- On failure: exponential backoff with jitter (1s → 2s → 4s … cap 5 min)
- Max attempts: 10 (configurable)
- After max attempts: moves to Dead Letter Queue (DLQ)

### 3. Dead Letter Queue
- `GET /dlq` — list DLQ events (paginated)
- `POST /dlq/{relay-id}/replay` — re-enqueue a DLQ event
- `DELETE /dlq/{relay-id}` — discard

### 4. Target registration
- Config file (`targets.yaml`) or env vars — no dynamic API in v1
- Each target: `id`, `url`, `secret`, `max_attempts`, `timeout_ms`

### 5. Observability
- Prometheus metrics on `/metrics`:
  - `hookrelay_received_total` (counter)
  - `hookrelay_delivered_total` (counter, label: target_id)
  - `hookrelay_failed_total` (counter, label: target_id)
  - `hookrelay_dlq_size` (gauge, label: target_id)
  - `hookrelay_delivery_duration_seconds` (histogram)
- Structured JSON logs (log/slog)
- `GET /health` → `{"status":"ok","version":"x.y.z"}`

### 6. Admin API (v1)
- `GET /events?target=&status=&limit=&cursor=` — list events with pagination
- `GET /events/{relay-id}` — event detail + delivery attempts log

---

## Demo integration
walletd fires `POST /hooks/walletd-credits` on credit deduction → hookrelay delivers to a mock consumer → 10 deliveries deduct 1 credit total. Covered by e2e test using walletd Docker image.

---

## Non-Goals (v1)
- No UI dashboard
- No dynamic target registration via API
- No mTLS between relay and target
- No multi-tenant auth
- No horizontal scaling (single binary, bbolt is not distributed)

---

## Data Model (bbolt buckets)

```
events/         {relay_id} → Event{id, target_id, body, received_at, status, attempts}
dlq/            {relay_id} → same Event (status=dlq)
attempts/       {relay_id}/{seq} → Attempt{seq, tried_at, status_code, error, duration_ms}
targets/        {target_id} → Target{id, url, secret_hash, max_attempts, timeout_ms}
```

---

## Tech Stack

| Concern | Choice | Reason |
|---|---|---|
| Language | Go latest | fast binary, good stdlib HTTP |
| DB | bbolt (etcd/bbolt) | embedded, no infra, durable |
| HTTP router | net/http stdlib | zero deps |
| Metrics | prometheus/client_golang | standard |
| Config | spf13/viper + yaml | simple |
| Release | GoReleaser + GHCR | binary + Docker image |
| CI | GitHub Actions | free |
| Tests | go test + race + fuzz | built-in |

---

## Project Layout

```
hookrelay/
├── cmd/hookrelay/main.go
├── internal/
│   ├── api/          # HTTP handlers
│   ├── relay/        # delivery worker, retry logic
│   ├── store/        # bbolt wrapper
│   ├── hmac/         # signature verification
│   └── config/       # target config loading
├── e2e/              # docker-compose + walletd integration test
├── targets.example.yaml
├── Dockerfile
├── .goreleaser.yaml
└── .github/workflows/ci.yml
```

---

## CI Pipeline

1. `go vet` + `staticcheck`
2. `go test -race ./...`
3. fuzz targets (30s smoke run)
4. benchmarks (`go test -bench`)
5. build binary
6. e2e with walletd Docker image
7. GoReleaser on tag → GHCR image + GitHub release binaries (linux/amd64, darwin/arm64, windows/amd64)

---

## Phases

| Phase | Deliverable | PR |
|---|---|---|
| P1 | Scaffold + ingest endpoint + bbolt store + HMAC verify | PR#1 |
| P2 | Delivery worker + retry/backoff + DLQ | PR#2 |
| P3 | Admin API + Prometheus metrics + health | PR#3 |
| P4 | e2e with walletd + GoReleaser + GHCR + README | PR#4 |
| P5 | Fuzz tests + race tests + benchmarks + coverage badge | PR#5 |

---

## Success Criteria

- 202 returned in < 5ms regardless of target latency
- Zero event loss: if hookrelay process crashes after 202, event survives restart
- DLQ replay works end-to-end
- All CI jobs green including e2e with walletd
- README badge: Go version, CI, coverage
