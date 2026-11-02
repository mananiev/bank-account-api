# Bank account API

A small HTTP API for bank accounts: create accounts, deposit money, and transfer money between accounts atomically. Written in Go with PostgreSQL.

> **Status:** all requirements in [docs/REQUIREMENTS.md](docs/REQUIREMENTS.md) are implemented, including the optional transaction list (FR3).

- Requirements: [docs/REQUIREMENTS.md](docs/REQUIREMENTS.md)
- Data model, API design and trade-offs: [docs/DESIGN.md](docs/DESIGN.md)
- How AI tools were used: [docs/AI_USAGE.md](docs/AI_USAGE.md)

## Quick start

Requires Docker (with Compose v2).

```sh
docker compose up --build
```

This starts PostgreSQL 16 and the API. The API waits until the database is healthy, applies migrations automatically, and listens on `http://localhost:8080`.

```sh
curl -i http://localhost:8080/healthz
# HTTP/1.1 200 OK
# {"status":"ok"}
```

**See it work:** with the stack running, `make demo` (or `./scripts/demo.sh`; needs only bash and curl) walks through every feature: accounts, deposits, idempotent replays, transfers, rejected requests, the transaction list and final balances. It prints each request and response and stops with an error if any status code or balance is wrong.

If ports 8080 or 5432 are already in use on your machine:

```sh
API_PORT=18080 DB_PORT=15432 docker compose up --build
API_URL=http://localhost:18080 make demo
```

Stop and remove everything, including the database volume:

```sh
docker compose down -v
```

## Running locally without Docker for the API

Requires Go 1.27.2 (Go 1.21 or newer downloads the right toolchain automatically) and `make`.

```sh
docker compose up -d db                       # just the database
export DATABASE_URL="postgres://bank:bank@localhost:5432/bank?sslmode=disable"
make run
```

| Target | What it does |
| --- | --- |
| `make build` | Builds the binary to `bin/api` |
| `make run` | Runs the API (needs `DATABASE_URL`) |
| `make test` | Runs all tests, including integration tests against a real PostgreSQL (Docker required). `make test RACE=1` enables the race detector (requires cgo / a C compiler) |
| `make test-unit` | Runs only the unit tests (`go test -short`); no Docker needed |
| `make lint` | Runs golangci-lint at a pinned version via `go run`; nothing to install |
| `make demo` | Runs the end-to-end walkthrough (`scripts/demo.sh`) against a running API; `API_URL` overrides the target |

## Configuration

Configuration is read from environment variables only.

| Variable | Required | Default | Description |
| --- | --- | --- | --- |
| `DATABASE_URL` | yes | — | PostgreSQL connection string. Pool settings can be tuned in the URL, e.g. `?pool_max_conns=10` |
| `HTTP_ADDR` | no | `:8080` | Address the HTTP server listens on |
| `LOG_LEVEL` | no | `info` | `debug`, `info`, `warn` or `error` |

The service exits at startup with a clear error if `DATABASE_URL` is missing or the database is unreachable.

## Endpoints

Amounts are integers in minor units (cents). Request bodies are validated strictly: unknown fields, duplicate fields (e.g. two `amount`s), non-integer amounts, trailing data and bodies over 1 MB are rejected with `400`.

| Method and path | Description |
| --- | --- |
| `POST /v1/accounts` | Create an account. Body `{"initial_balance": 5000}` (optional, default 0). `201` + account and `Location` header; `400 invalid_request`; `422 invalid_amount` if negative |
| `GET /v1/accounts/{id}` | Fetch an account. `200` + account; `400 invalid_account_id`; `404 account_not_found` |
| `POST /v1/accounts/{id}/deposits` | Deposit money. Body `{"amount": 2500}`, header `Idempotency-Key` (required, 1–255 chars). `201` + transaction; `400 invalid_request` / `invalid_idempotency_key` / `invalid_account_id`; `404 account_not_found`; `409 idempotency_key_reused`; `422 invalid_amount` (≤ 0 or balance overflow) |
| `GET /v1/accounts/{id}/transactions` | List the account's transactions, newest first: `{"transactions":[…]}`, including the opening balance. `200`; `400 invalid_account_id`; `404 account_not_found` |
| `POST /v1/transfers` | Transfer money. Body `{"from_account_id":"…","to_account_id":"…","amount":2500}`, header `Idempotency-Key`. `201` + transaction; `400 invalid_request` (incl. missing/malformed account ID) / `invalid_idempotency_key`; `409 idempotency_key_reused`; `422 insufficient_funds` / `same_account` / `account_not_found` / `invalid_amount` |
| `GET /healthz` | `200` if the service can reach the database, `503` otherwise |

```sh
curl -i -X POST http://localhost:8080/v1/accounts -d '{"initial_balance":5000}'
# HTTP/1.1 201 Created
# Location: /v1/accounts/cf4dd370-31e7-4e31-a174-19788f557269
# {"id":"cf4dd370-31e7-4e31-a174-19788f557269","balance":5000,"created_at":"2026-11-02T10:26:39.64831Z"}

curl -s http://localhost:8080/v1/accounts/00000000-0000-0000-0000-000000000000
# {"error":{"code":"account_not_found","message":"account not found"}}

curl -s -X POST http://localhost:8080/v1/accounts/<id>/deposits \
  -H "Idempotency-Key: 3f0c7a8e-6c1b-4b7e-9a39-1f4d1f2a9b10" -d '{"amount":2500}'
# {"id":"7c7cdd9b-…","type":"deposit","account_id":"<id>","amount":2500,"created_at":"…"}
```

```sh
curl -s -X POST http://localhost:8080/v1/transfers \
  -H "Idempotency-Key: 9b2e4c1a-0f3d-4a8e-b7c6-2d5e8f1a3c47" \
  -d '{"from_account_id":"<id>","to_account_id":"<other-id>","amount":400}'
# {"id":"5f1f2774-…","type":"transfer","from_account_id":"<id>","to_account_id":"<other-id>","amount":400,"created_at":"…"}
```

**Idempotency:** repeating a deposit or transfer with the same `Idempotency-Key` and the same request returns the original `201` response without depositing again, even when the requests arrive concurrently. The same key with a different request returns `409 idempotency_key_reused`. Failed requests store nothing, so they can be retried with the same key. See [docs/DESIGN.md](docs/DESIGN.md#idempotency).

All errors share one shape, `{"error":{"code":"…","message":"…"}}`, including unknown routes (`404 route_not_found`) and wrong methods (`405 method_not_allowed`, with an `Allow` header). Clients should branch on `code`; unexpected failures return `500 internal_error` without internal details (they are logged).

## Testing

```sh
make test        # everything; needs Docker
make test-unit   # unit tests only
```

- **Integration tests use a real PostgreSQL 16** started with [testcontainers](https://golang.testcontainers.org/) (one container per test package), migrated with the same embedded migrations as production. No manual setup; the only requirement is Docker. If Docker is missing, these tests fail with a clear message instead of being silently skipped.
- **Most tests go through HTTP** (`httptest`) into the real service and database, so they cover status codes, error codes, JSON handling and database constraints together, without mocks.
- **Ledger consistency is asserted** in the tests that move money: an account's balance must equal the sum of its ledger entries. A separate test checks that the database rejects `UPDATE`, `DELETE` and `TRUNCATE` on the ledger.
- **Concurrency is tested directly**, with requests sent in parallel against the real database:
  - 90 transfers in both directions between three accounts: every response is `201` or `422 insufficient_funds` (never a `500` from a deadlock), total money is unchanged, no balance is negative, and every ledger matches its balance;
  - 10 transfers of 60 from an account holding 100: exactly one succeeds;
  - 20 identical deposits or transfers with the same key: the money moves exactly once.
- **The concurrency tests were checked against broken code:** removing `FOR UPDATE`, removing the lock ordering, or removing the funds check each makes them fail.
- **Tests don't clean up.** Each test creates its own accounts, so tests are independent and run in parallel. (The ledger is append-only, so it could not be truncated anyway.)

## Project layout

```
cmd/api/              entry point: config, logging, DB pool, migrations, HTTP server, graceful shutdown
internal/config/      environment variable parsing
internal/account/     domain model and service (business rules)
internal/storage/     PostgreSQL implementation (pgx/v5) and embedded SQL migrations (goose)
internal/httpapi/     HTTP routing (net/http), handlers, JSON decoding and error mapping
internal/testdb/      test helper: PostgreSQL via testcontainers, ledger assertions
scripts/demo.sh       end-to-end walkthrough and smoke test (bash + curl)
```

`internal/account` declares the storage interface it needs; `internal/storage` implements it. Database transactions stay inside the storage layer.

## Key decisions

The full reasoning is in [docs/DESIGN.md](docs/DESIGN.md). In short:

- **Money is `BIGINT` in minor units (cents).** No floating point anywhere.
- **Balance column + append-only ledger.** The balance makes reads fast and lets the database enforce `balance >= 0`; the ledger records every change. The database rejects any `UPDATE`, `DELETE` or `TRUNCATE` on ledger entries.
- **Concurrent transfers** lock both account rows with `SELECT … FOR UPDATE` in a fixed ID order, so they cannot lose or create money and cannot deadlock.
- **Idempotency** is required on deposits and transfers via the `Idempotency-Key` header, backed by a unique constraint in the database.
- **Standard library first:** `net/http` routing, `log/slog` JSON logging. Runtime dependencies are pgx, goose and google/uuid; testcontainers is used only in tests.
- **Production-minded basics:** HTTP server timeouts, graceful shutdown on SIGINT/SIGTERM, a distroless non-root runtime image.

## Trade-offs and what I would do with more time

- **No pagination on the transaction list** (out of scope). The full history is returned, which would not scale for busy accounts. Cursor pagination on `(created_at, id)` is the first thing I would add; the response is already wrapped in an object and the index already matches that order, so it can be added without breaking clients.
- **Migrations run on application startup.** Simple for one instance; with several replicas they should run as a separate job before deployment.
- **Credentials in `docker-compose.yml` are for local use only.** In production they would come from a secret store.
- **Deposits are single-entry.** Transfers follow double-entry bookkeeping (two entries summing to zero), but deposits and opening balances have no counterpart account. Strict double-entry would add a system "external funds" account.
- **One health endpoint.** A deployment on Kubernetes would split liveness and readiness.
- **Transfers on the same account are serialized** by the row locks. Correct for money, but a very busy account (e.g. a merchant receiving many payments) becomes a bottleneck; at scale this would call for techniques like splitting hot accounts or batching.
- **Idempotency keys never expire.** Simple and safe, but keys cannot be reused and the unique index grows forever; production systems often expire keys after ~24 hours.
- **Account creation is not idempotent.** A retried create can produce a second account.
- **Amounts are JSON numbers.** JavaScript clients lose precision above 2^53 cents (about 90 trillion euros); sending amounts as strings would avoid that at the cost of a less natural API.
- **No bound on lock waits.** Requests have no deadline and Postgres `lock_timeout` is unset, so if a transaction holding an account lock stalls, other requests for that account wait indefinitely while holding pool connections. Production would add a per-request deadline (or `lock_timeout`) and map timeouts to `503`.
- **The ledger is only partly tamper-proof.** A trigger blocks changes to `ledger_entries`, but the `transactions` table can be updated, and the app connects as a database superuser that could disable the trigger. Production would use separate migration and runtime roles and revoke `UPDATE`/`DELETE` from the runtime role.
- **"Newest first" uses each transaction's start time** (`now()` in Postgres), not its commit time; concurrent transactions can commit in a different order. Stable cursor pagination would need a commit-ordered sequence.
- **JSON field names match case-insensitively** (`{"AMOUNT":5}` is accepted), a property of Go's `encoding/json`. Duplicates in any casing are rejected; `encoding/json/v2` would allow strict matching.
- **No request logging.** Only failures are logged. Production would add access logs (method, path, status, duration) and a request ID, plus metrics and tracing.
- **No CI pipeline.** The tests need only Docker, so a GitHub Actions workflow running `make lint` and `make test` on every pull request would be a small addition.
- **Integration tests are slower than mocks** (a container start of a few seconds per package) in exchange for testing real constraints and locking.
