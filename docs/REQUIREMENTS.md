Accounts
* FR1: A client can create an account with an optional initial balance (default 0). The initial balance must be ≥ 0.
* FR2: A client can fetch an account by ID and see its current balance.
* FR3 (Optional): A client can list an account's transactions, newest first.

Deposits
* FR4: A client can deposit a positive amount into an existing account.
* FR5: Deposits are idempotent: repeating a request with the same Idempotency-Key does not deposit twice and returns the original result.

Transfers
* FR6: A client can transfer a positive amount between two different existing accounts.
* FR7: A transfer is atomic: either both balances change, or neither does.
* FR8: A transfer fails if the source account has insufficient funds. Balances can never go negative (no overdraft).
* FR9: Transfers are idempotent, like deposits.
* FR10: Concurrent transfers involving the same accounts must never lose or create money.

Non-functional
* NFR1: Amounts are integers in minor units (cents). No floating point anywhere.
* NFR2: Every balance change is recorded as an immutable ledger entry, so balances can be audited and recalculated.
* NFR3: Starts with one command (docker compose up), on any machine with Docker.
* NFR4: Clear, consistent JSON errors with machine-readable codes.

Out of scope
* Authentication and authorization (in production: every account belongs to an owner, and you could only move your own money).
* Withdrawals, currencies, currency conversion, fees, interest, overdrafts.
* Account closing, pagination, rate limiting.