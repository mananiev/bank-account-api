# AI usage

I used AI tools throughout this exercise: Claude (chat) to draft the initial data model and API design, and Claude Code (an AI coding agent working in the repository) to write code. This document describes how I split the work, what I asked for and why, which decisions I made, and where I changed or rejected the AI's output.

## Approach

- **I wrote the requirements first** ([REQUIREMENTS.md](REQUIREMENTS.md)) and the design ([DESIGN.md](DESIGN.md)) before asking for code, so the AI implemented decisions rather than making them.
- **One small step per prompt**, each reviewed before the next: skeleton → Docker and schema → features. Each step ended with a working, verified state that I could review and commit.
- **I fixed the stack in the prompt** (standard library `net/http`, `pgx/v5`, `log/slog`, environment-only config) so the AI could not pull in frameworks I would have to defend.
- **Decisions a reviewer might ask about were brought to me first**, with options and a recommendation, rather than decided silently by the AI.

## Step 1: Project skeleton

**Prompt (summary):** set up a Go 1.27.2 module with `cmd/api`, `internal/account`, `internal/storage`, `internal/httpapi`; `net/http` routing, `pgx/v5`, `log/slog`; config from environment variables only; a Makefile with build, run, test, and lint.

**Decisions I made** (from options the AI presented):

| Topic | Decision | Alternatives considered |
| --- | --- | --- |
| DB config | Single `DATABASE_URL`, required, fail fast at startup | `PG*` variables; both |
| Health check | One `GET /healthz` that pings the database | Separate liveness/readiness; none |
| Linting | golangci-lint at a pinned version via `go run`, no install step | Expect it installed (an implicit dependency); run via Docker |
| Layering | `internal/account` declares the storage interface it consumes; `internal/storage` implements it | No interface; decide later |

**Accepted from the AI:** a separate `internal/config` package so parsing is unit-testable without touching the real environment; JSON logs; HTTP server timeouts; graceful shutdown; race detector opt-in (`make test RACE=1`) because it needs a C compiler on Windows; `.gitattributes` enforcing LF line endings so Linux containers are not broken by Windows checkouts.

**Not done yet, deliberately:** the storage interface has no methods until the first feature needs one.

## Step 2: Data model review

The first draft of `DESIGN.md` came from Claude (chat). I asked Claude Code to review it against the requirements. Findings and what I did:

- **Currency contradicted my own requirements** (out of scope, but the design had a currency column and currency errors). I removed currency entirely.
- **Initial balances bypassed the ledger**, breaking the "every balance change is a ledger entry" requirement. I added an `OPENING` transaction type.
- **No concurrency strategy was written down**, although it is the core of the exercise. I compared four options (pessimistic row locks in a fixed order, conditional `UPDATE`, `SERIALIZABLE` with retries, optimistic version column) and chose **row locks with `SELECT … FOR UPDATE` in ascending ID order**: explicit, easy to reason about, and deadlock-free.
- **Idempotency details:** I decided the key is required, the request hash includes the operation type, and a replay returns the original status and body.
- **Missing account in a transfer:** I chose `422` over `404`, because the `/v1/transfers` resource exists and the unknown ID is invalid input.
- **Immutable ledger:** I chose to enforce it with a database trigger instead of relying on application code.
- Response shapes made consistent (deposit and transfer both return the transaction).

## Step 3: Docker and migrations

**Prompt (summary):** multi-stage Dockerfile (golang image to build, distroless or alpine to run), docker-compose with Postgres 16 (health check) and the API (depends on healthy DB), SQL migrations from `DESIGN.md`, run automatically on startup with embedded migration files.

**AI choices I accepted:** goose (runs embedded migrations from Go code with little setup); distroless static non-root runtime image; host ports configurable via `API_PORT` / `DB_PORT` so the setup works on machines where 8080 or 5432 are taken; database `CHECK` constraints tying idempotency keys to transaction types.

**Corrected during the step:** the AI first proposed storing the full HTTP response for idempotent replays, then dropped it because the response can be rebuilt from the stored transaction. As a consequence, only successful operations are recorded, so a request rejected for insufficient funds can be retried with the same key later. I kept that behavior and documented it.

**Verification:** started the stack with `docker compose up --build` and checked that migrations apply, `/healthz` returns 200 (and 503 with the database stopped), negative balances and keyless deposits are rejected by the database, `UPDATE` / `DELETE` / `TRUNCATE` on the ledger fail, and a restart does not re-run migrations.

**Trade-offs accepted:** migrations on startup (fine for one instance); local-only credentials in compose.

## Step 4: Create and get account, test setup

Before this step I planned the remaining work myself: accounts → deposits with idempotency → transfers with the concurrency test → transaction list (optional, dropped first if time runs out) → final pass. Accounts came first because they set the patterns everything else reuses: domain errors, the repository interface, JSON handling, error format, and the test setup.

**Prompt (summary):** implement `POST /v1/accounts` and `GET /v1/accounts/{id}` as specified in `DESIGN.md`, test-first, with integration tests against a real database.

**Decisions I made:**

| Topic | Decision | Alternatives considered |
| --- | --- | --- |
| Test database | testcontainers-go: each test run starts its own PostgreSQL, so `go test ./...` works anywhere Docker runs | Reuse the compose database via an env var (a hidden prerequisite; tests silently skipped without it); in-memory fakes only (cannot test locks or constraints) |
| Test layers | Mostly HTTP-level tests through the real service and database; unit tests only for pure validation | Classic layering with fake repositories and fake services (fakes would have to imitate database locking and constraints) |
| Docker missing | Integration tests fail with a clear message; `-short` / `make test-unit` runs unit tests deliberately | Skip silently (a green run could hide that the important tests never ran) |
| JSON input | Strict: unknown fields, trailing data and bodies over 1 MB return `400` | Lenient (a typo like `inital_balance` would silently create a zero-balance account) |
| Amounts in JSON | Integers in cents | Strings (safer for JavaScript above 2^53, but unusual and clunkier) |

**Accepted from the AI:** named domain errors (`ErrNotFound`, `ErrInvalidAmount`) mapped to HTTP status and error code in one place in `httpapi`, so business code knows nothing about HTTP; `github.com/google/uuid`; one container per test package; tests that never clean up but create their own accounts (the append-only ledger makes cleanup impossible anyway); a reusable `AssertLedgerConsistent` helper for the `balance = SUM(entries)` invariant.

**How it was built:** test-first. All tests were written and run against stubs and failed for the expected reasons before any implementation existed. Because the ledger assertions had never been seen failing, I also broke the code deliberately (skipped the opening ledger entry; mapped "not found" to `200`) and confirmed the tests caught both.

**Corrected during the step:** the AI's first version returned raw Go decoder errors to clients (`json: cannot unmarshal array into Go value of type httpapi.createAccountRequest`), leaking internal type names. Replaced with client-facing messages; the final review found field-level messages still said `int64`, fixed in Step 8. Lint (`noctx`) also caught test requests created without a context.

**Verification:** `make lint` clean; full test suite green against PostgreSQL in testcontainers; `-short` runs only unit tests; manual curl checks through `docker compose up`.

## Step 5: Idempotent deposits

**Prompt (summary):** implement `POST /v1/accounts/{id}/deposits` with the idempotency rules from `DESIGN.md`, test-first, including a concurrency test for duplicate requests.

**Decisions I made:**

| Topic | Decision | Alternatives considered |
| --- | --- | --- |
| Request hash | SHA-256 of the parsed values (operation type, account ID, amount) | Hash of raw body bytes (a retry with different whitespace or field order would get a false `409`) |
| Key format | Any string of 1–255 characters | UUID only (forces a format the server doesn't need) |
| Key retention | Forever, on the transaction row | Expire after 24h (needs a separate table or cleanup job; a replay after expiry would deposit twice) |
| Response shape | Type-specific fields (`account_id` for deposits; `from_account_id` / `to_account_id` for transfers) | One generic shape with a list of ledger entries (clients would have to dig to see who paid whom) |

**Accepted from the AI:**
- Idempotency inside the storage transaction rather than HTTP middleware, so claiming the key and moving the money commit or roll back together.
- The key is claimed with `INSERT … ON CONFLICT DO NOTHING`. A concurrent duplicate waits on the unique index and then replays the stored result.
- Balance overflow (Postgres "out of range") mapped to `422 invalid_amount` instead of a `500`.
- Error messages reworded to read naturally: "invalid amount: amount must be positive".

**How it was built:** test-first. All tests failed for the expected reasons first. Then I broke the implementation three ways (removed `ON CONFLICT`, skipped the hash comparison, disabled the overflow mapping) and confirmed each was caught by a test. One of those checks first appeared to pass; the cause was a mutation that didn't compile, not a gap in the tests, and a corrected mutation was caught.

**Corrected during the step:** the AI's first concurrency test called `t.Fatal` from goroutines (not allowed in Go), and used a fixed 255-character key that would collide when tests are repeated with `-count`. Both fixed before the first run. Lint (staticcheck) flagged a test comparing an expression with itself.

**Verification:** full suite green (also repeated three times to check for flakiness); `make lint` clean; manual deposit, replay, key reuse and validation checks with curl through `docker compose up`.

## Step 6: Atomic, idempotent transfers

**Prompt (summary):** implement `POST /v1/transfers` with the concurrency strategy and idempotency rules from `DESIGN.md`, test-first, with concurrency tests that would fail if the locking were wrong.

**Decisions I made:**

| Topic | Decision | Alternatives considered |
| --- | --- | --- |
| Lock statements | Sort the two IDs in Go, then lock with two `SELECT … FOR UPDATE` statements, lower ID first | One `SELECT … WHERE id IN (…) ORDER BY id FOR UPDATE` (one round trip, but relies on Postgres locking rows in `ORDER BY` order, which is harder to prove) |
| Unknown account | `422` with the existing `account_not_found` code; the message names the side | Separate `source_account_not_found` / `destination_account_not_found` codes |
| Missing or malformed account ID in the body | `400 invalid_request`, following the 400-vs-422 rule | `422` for any problem with the account IDs |

The strategy itself (pessimistic row locks in a fixed order) was decided earlier in the design review; see Step 2.

**Accepted from the AI:**
- Claim the idempotency key before locking accounts, so a replay returns the original result even if balances changed since.
- Shared helpers for claiming a key and replaying a transaction, used by both deposits and transfers. Replay rebuilds a transfer from its two ledger entries (the negative one is the source).
- A separate domain error for an unknown account in a transfer body, so it maps to `422` while an unknown account in a deposit URL stays `404`.
- Relative balance updates (`balance = balance - $1`) rather than writing a computed value, so even a locking bug could not silently lose an update.

**How it was built:** test-first; all tests failed for the expected reasons before implementation. Then I broke the implementation three ways and confirmed the concurrency tests catch each:

| Broken code | Result |
| --- | --- |
| `FOR UPDATE` removed | Overspend and conservation tests fail with `500`s |
| Lock ordering removed (source always locked first) | Conservation test fails with `500`s, and the run took about 33s instead of about 2s. That points to Postgres detecting deadlocks between opposite transfers; this is an inference, the logs were not inspected. |
| Balance check removed | Rejected by the database's `CHECK (balance >= 0)` as a `500` instead of a clean `422`: the constraint works as the last line of defence, and no money was lost |

**Corrected during the step:** a test checked the error message after another helper had already read the response body, so it compared against an empty string. Fixed by capturing the body first.

**Known rough edge:** a malformed UUID in the body returns `invalid JSON: invalid UUID length: 4`, which does not name the field.

**Verification:** full suite green, repeated five times; `make lint` clean; transfer, replay, insufficient funds, same account, unknown account, and malformed ID checked with curl through `docker compose up`.

## Step 7: Transaction list (FR3)

**Prompt (summary):** implement `GET /v1/accounts/{id}/transactions`, newest first, test-first.

**Decisions I made:**

| Topic | Decision | Alternatives considered |
| --- | --- | --- |
| Response envelope | `{"transactions":[…]}` | Bare array (adding pagination later would break clients) |
| Item shape | Same transaction shapes as the deposit and transfer endpoints | Account-centric view with signed amounts and a counterparty (a second representation of the same transaction) |
| Long histories | Return everything; document cursor pagination as the first improvement | Cap at the newest 100 (silently hides history); implement cursor pagination (beyond the stated scope) |

**Accepted from the AI:**
- One query over the account's ledger entries, joined with the transaction and, for transfers, the other side's entry to find the counterparty.
- The code that turns ledger entries into a deposit, opening, or transfer was extracted from the replay logic, so the list and the idempotent replay share it.
- Opening balances are included, so the list adds up to the balance; a test asserts exactly that.
- The account existence check only runs when the list is empty, to tell `404` from an empty account without an extra query in the common case.

**How it was built:** test-first; tests failed for the expected reasons before implementation. Mutation checks: reversing the sort order and removing the existence check each made a test fail.

**Checked, without overclaiming:** `EXPLAIN` shows the account filter uses `ledger_entries_account_created_idx`. On a tiny table Postgres picks a bitmap scan and still sorts. I did not claim that the index avoids sorting.

**Verification:** full suite green, repeated three times; `make lint` clean; list, empty list, and unknown account checked with curl through `docker compose up`.

## Step 8: Final pass

**Prompt (summary):** close the gaps a reviewer would notice, add a way for reviewers to see the API working, then review the whole branch.

**Decisions I made:**

| Topic | Decision | Alternatives considered |
| --- | --- | --- |
| Reviewer demo | `scripts/demo.sh` (bash + curl, `make demo`) that prints each request and response and fails on any unexpected status or balance | `.http` file for an IDE HTTP client (not runnable from a terminal); both |
| Plain-text 404/405 from the router | Fixed: JSON `route_not_found` / `method_not_allowed`, because it contradicted my own NFR4 | Leave as is |
| Request logging | Not added; listed under "with more time" | Logging middleware |
| CI | Not added; listed under "with more time" | GitHub Actions running lint and tests |
| Branch review | Independent reviewer agent with no access to this conversation, reviewing like an interviewer; every finding verified before acting | Self-review by the same session that wrote the code |

**Fixed:** transfer bodies now name the field for malformed account IDs (`from_account_id must be a UUID` instead of `invalid UUID length: 4`).

**Verified:** the demo passes against `docker compose up`, and fails with a clear message when the API is down or a balance is wrong. The full suite and lint are clean; `go mod verify` passes.

**Independent review.** A fresh AI reviewer, with no access to this conversation, reviewed the whole branch against the requirements, the design, and the brief. It ran the tests, the demo, and its own curl and psql probes. Verdict: no critical issues; atomicity, lock ordering, overdraft prevention, and idempotency under concurrency all hold. I checked its findings against the code before acting, then decided:

| Finding | Decision |
| --- | --- |
| A non-UTF-8 `Idempotency-Key` caused a `500` (Postgres rejects the bytes) | Fixed: `400 invalid_idempotency_key`, with a test |
| The amount or same-account check ran before the key check, so a missing key could return `422` instead of `400` | Fixed: the key is validated first |
| `encoding/json` silently keeps the last of duplicate fields (`{"amount":1,"amount":7}` deposited 7) | Fixed: duplicate fields (in any casing) are rejected with `400`. Case-insensitive field matching remains and is documented |
| Field errors still showed Go types (`must be of type int64`) | Fixed: "must be an integer" / "must be a string" |
| Untested paths: the append-only trigger, transfer destination overflow, concurrent same-key requests where the first fails, `/healthz` 503 | Tests added. They passed straight away because the behaviour already worked, so I broke the trigger migration to confirm the trigger test catches it |
| `demo.sh` would be committed without the executable bit (Windows checkout) | Fixed at commit time |
| No bound on how long a request waits for a lock | Documented as a trade-off rather than fixed, given the timebox |
| The `transactions` table is mutable and the app runs as a DB superuser; "newest first" uses start time, not commit time | Documented as trade-offs |
| Docs overclaimed in places (deposits as "addressable resources", ledger assertions on "every" operation, index columns, TRUNCATE, Go toolchain note) | Corrected |

## Overall

**How I split the work.** *I used Claude chat to lay out the requirements and build the design first, then made a plan to split the development into logical phases. One vertical slice per step (skeleton → Docker and schema → accounts → deposits → transfers → list → final pass). Each step was reviewed and committed before the next. Accounts came first because they set the patterns (errors, JSON handling, tests) the rest reuses; transfers got the most time because of concurrency.*

**What worked well.**
-  *Using Jesse Vincents Superpowers and especially Brainstormin, fixing the stack and decisions in the prompt, and having options brought to me before code was written, kept the AI from making decisions I would have to defend without having made them.*
-  *Test-first with deliberate mutations: breaking the locking and checking that the tests fail turned "the concurrency tests pass" into evidence that they actually test the locking.*

**Where the AI needed correcting.**  *Examples from the steps above:*
- *Its design draft contradicted my requirements (currency) and left initial balances out of the ledger.*
- *It proposed storing whole responses for idempotency, and then dropped that idea itself.*
- *It leaked Go type names in error messages.*
- *It wrote test bugs: `t.Fatal` called from goroutines, a test key that collided on repeat runs, and a body that was read twice.*
- *A mutation check that didn't compile looked like a passing test until it was investigated.*

**What I'd do differently.**  *I would spend a little bit more time on the initial design and perfect the plan, steps, and possible issues beforehand*

**Time spent.**  *Total time spent is about 5h that also includes three breaks of 20 minutes*
