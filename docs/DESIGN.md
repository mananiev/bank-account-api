## Data model and API design

### Tables

| Table | Columns | Purpose |
| --- | --- | --- |
| `accounts` | id (UUID), balance (BIGINT, `CHECK (balance >= 0)`), created\_at | Current state, fast to read |
| `transactions` | id (UUID), type (OPENING / DEPOSIT / TRANSFER), amount (BIGINT, `CHECK (amount > 0)`), idempotency\_key (UNIQUE, NULL for OPENING), request\_hash, created\_at | One row per money movement |
| `ledger_entries` | id, transaction\_id (FK), account\_id (FK), amount (signed BIGINT, non-zero), created\_at | The audit trail: + for credit, − for debit. Indexed on `(account_id, created_at DESC)` for the transaction list |

**Balance column and ledger entries**

- The **ledger** is the source of truth and the audit trail. Every balance change is an immutable row, so any balance can be explained and recalculated as the sum of its entries.
- The **balance column** keeps reads fast and gives the database a row to lock and a constraint to enforce (`balance >= 0`). Without it, every request would sum all entries, and overdrafts could not be prevented with a simple CHECK constraint.
- **Trade-off:** the balance is duplicated data that must stay in sync with the ledger. Both are always written in the same database transaction, and a test verifies `balance = SUM(entries)` for every account.
- **Immutability is enforced by the database:** a trigger rejects any `UPDATE` or `DELETE` on `ledger_entries`.
- **Initial balance:** a non-zero initial balance is recorded as an `OPENING` transaction with one ledger entry, in the same database transaction that creates the account. Every balance change, including the first, is therefore in the ledger.
- **Double-entry:** a transfer writes two entries (−100 on the source, +100 on the target) that sum to zero, following double-entry bookkeeping.
- **Simplification:** deposits and opening balances write a single entry, so the books do not fully balance. Strict double-entry would debit a system "external funds" account. This is a deliberate simplification to revisit.

**BIGINT minor units:** floating point cannot represent 0.10 exactly (`0.1 + 0.2 ≠ 0.3`), so money would drift. An int64 amount in cents is exact, fast, and has ample range. A decimal type (`NUMERIC` or a decimal library) would be the alternative for currencies with three decimals or sub-cent precision, at the cost of speed and complexity.

**UUIDs:** IDs cannot be guessed or enumerated (`/accounts/1`, `/accounts/2`, …) and can be generated anywhere. Trade-off: larger indexes than integer IDs.

### Concurrency

A transfer runs in a single database transaction:

1. Lock both account rows with `SELECT … FOR UPDATE`, always in ascending ID order. The IDs are sorted in Go and locked with two separate statements, lower ID first, so the lock order is explicit in the code rather than relying on how Postgres executes `ORDER BY … FOR UPDATE` in a single statement (one extra round trip).
2. Check that both accounts exist and that the source has sufficient funds.
3. Update both balances, insert the transaction and its two ledger entries, and commit.

The fixed lock order means two opposite transfers (A→B and B→A) cannot deadlock. Transfers touching the same account are serialized, which is the intended behaviour for money; the trade-off is reduced throughput on a very busy account. The `CHECK (balance >= 0)` constraint is the last line of defence if application logic is ever wrong.

Alternatives considered:

- **Conditional update** (`UPDATE … SET balance = balance - $1 WHERE id = $2 AND balance >= $1`): fewer round trips, but still needs ordered locking to avoid deadlocks, and zero affected rows does not distinguish "not found" from "insufficient funds".
- **SERIALIZABLE isolation with retries:** correctness is guaranteed by the database, but requires retry logic and causes many aborts under contention.
- **Optimistic locking (version column):** suited to rarely contended data; a busy account would cause constant retries.
- **In-process mutex:** only protects a single instance; incorrect as soon as more than one API instance runs.

A test runs many concurrent transfers in both directions between the same accounts and asserts that the total amount of money is unchanged and no balance is negative.

### Idempotency

- `Idempotency-Key` is **required** on deposits and transfers: any string of 1–255 characters (clients are advised to use UUIDs). A missing or invalid key returns `400`.
- Keys are unique across all operations. The stored `request_hash` is a SHA-256 of the **parsed** request (operation type, account IDs, amount), not the raw body, so a retry with different whitespace or field order is still recognised as the same request. Reusing a key for a different request (including a deposit key reused for a transfer) returns `409`.
- Keys are **kept forever**: they live on the transaction row, which is never deleted. Production systems often expire keys (e.g. after 24 hours) so they can be reused and the index stays bounded; that would need a separate keys table.
- **Idempotency is implemented in the storage transaction, not as HTTP middleware.** Claiming the key and moving the money must commit or roll back together. A generic middleware caching responses would need its own table and handling for requests still in progress, and could still record a key whose money movement failed.
- A replay with the same key and the same request returns the **original status and body** (`201` + the stored transaction).
- Only successful operations are recorded. A request rejected by a business rule (e.g. insufficient funds) stores nothing, so the client can retry it with the same key once the condition changes.
- Concurrent requests with the same key are safe: the second insert waits on the unique index until the first commits, gets a conflict, and returns the stored result.
- Account creation is not idempotent; a retried create may produce a second empty account. This is accepted for this exercise.

### Endpoints

| Method and path | Body | Success | Errors |
| --- | --- | --- | --- |
| `POST /v1/accounts` | `{"initial_balance":0}` | `201` + account, `Location` header | `400` bad JSON, `422` negative balance |
| `GET /v1/accounts/{id}` | — | `200` + account | `400` invalid UUID, `404` not found |
| `POST /v1/accounts/{id}/deposits` | `{"amount":5000}` + `Idempotency-Key` header | `201` + transaction | `400` bad JSON / missing key, `404` account not found, `422` amount ≤ 0 or balance overflow, `409` key reused with a different request |
| `POST /v1/transfers` | `{"from_account_id":"…","to_account_id":"…","amount":2500}` + `Idempotency-Key` | `201` + transaction | `400` bad JSON / missing or malformed account ID / missing key, `422` `account_not_found` / `insufficient_funds` / `same_account` / `invalid_amount`, `409` key reused with a different request |
| `GET /v1/accounts/{id}/transactions` | — | `200` + `{"transactions":[…]}`, newest first | `400` invalid UUID, `404` not found |

**Transaction list (FR3)**

- The list is **wrapped in an object** (`{"transactions":[…]}`) so pagination fields such as a cursor can be added later without breaking clients. A bare array cannot grow.
- **Items use the same shapes** the deposit and transfer endpoints return (`account_id` for opening and deposit; `from_account_id` / `to_account_id` for transfers; amounts always positive). Clients tell direction by comparing `from_account_id` with the account. An account-centric view with signed amounts would suit a statement UI better, but would introduce a second representation of the same transaction.
- **The opening balance is included** (type `opening`), so the listed movements add up to the current balance.
- **No pagination** (out of scope): the full history is returned. Cursor pagination on `(created_at, id)` would be the first addition; the `(account_id, created_at DESC, id DESC)` index already matches that order. Capping the list without pagination was rejected because it would silently hide history.

**API design choices**

- **Deposits are nested under the account** because a deposit belongs to exactly one account. **Transfers are top-level** because they involve two accounts equally and are a resource in their own right.
- **Movements are nouns created with POST**, not actions like `/transfer-money`. Each deposit or transfer is an addressable resource.
- **400 vs 422:** `400` means the request is malformed (invalid JSON, wrong types, missing required header). `422` means the request is well-formed but a business rule rejects it. Insufficient funds is a `422`; `409` is a common alternative.
- **Missing accounts in a transfer return 422, not 404:** the `/v1/transfers` resource exists; an unknown account ID in the body is invalid input to it. `404` is reserved for a resource in the URL path that does not exist (as in the deposit route). The error code is the same `account_not_found` used elsewhere; the message names the side (source or destination).
- **A missing or malformed account ID in a transfer body is a `400`**, following the 400-vs-422 rule: the request is malformed. An ID that is well-formed but unknown is a `422`.
- **Errors** have one consistent shape: `{"error":{"code":"insufficient_funds","message":"…"}}`. Clients branch on `code`, never on the message text.
- **`/v1/` prefix** allows breaking changes to be introduced under a new version without affecting existing clients.
