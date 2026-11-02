#!/usr/bin/env bash
# End-to-end walkthrough of the API. Prints every request and response and
# exits non-zero if any status code or balance is not what it should be, so it
# doubles as a smoke test.
#
# Requires: bash, curl, and a running API (docker compose up).
# Usage:    ./scripts/demo.sh          (or: make demo)
#           API_URL=http://localhost:18080 ./scripts/demo.sh
set -euo pipefail

API_URL="${API_URL:-http://localhost:8080}"
RUN="demo-$(date +%s)-$RANDOM" # unique idempotency keys per run

bold=$'\033[1m' green=$'\033[32m' red=$'\033[31m' reset=$'\033[0m'

step() { printf '\n%s== %s%s\n' "$bold" "$*" "$reset"; }

fail() {
	printf '%sFAILED: %s%s\n' "$red" "$*" "$reset" >&2
	exit 1
}

# call METHOD PATH EXPECTED_STATUS [BODY] [IDEMPOTENCY_KEY]
# Prints the request and response and sets STATUS and RESPONSE.
call() {
	local method=$1 path=$2 expected=$3 body=${4:-} key=${5:-}
	local args=(-sS -X "$method" -w $'\n%{http_code}' -H 'Content-Type: application/json')
	[[ -n $key ]] && args+=(-H "Idempotency-Key: $key")
	[[ -n $body ]] && args+=(-d "$body")

	printf '> %s %s' "$method" "$path"
	[[ -n $key ]] && printf '   [Idempotency-Key: %s]' "$key"
	printf '\n'
	[[ -n $body ]] && printf '> %s\n' "$body"

	local out
	out=$(curl "${args[@]}" "$API_URL$path") || fail "request failed: $method $path"
	STATUS=${out##*$'\n'}
	RESPONSE=${out%$'\n'*}
	RESPONSE=${RESPONSE%$'\n'} # drop the JSON encoder's trailing newline
	printf '< %s %s\n' "$STATUS" "$RESPONSE"

	[[ $STATUS == "$expected" ]] || fail "expected HTTP $expected, got $STATUS"
}

# field NAME: extracts a top-level string or number field from RESPONSE.
field() { sed -n "s/.*\"$1\":\"\{0,1\}\([^\",}]*\).*/\1/p" <<<"$RESPONSE"; }

expect() { [[ $1 == "$2" ]] || fail "$3: got '$1', want '$2'"; }

if ! curl -sf -o /dev/null "$API_URL/healthz"; then
	fail "API not reachable at $API_URL. Start it with: docker compose up --build"
fi

step "Create two accounts: Alice with 100.00, Bob with no body (balance defaults to 0)"
call POST /v1/accounts 201 '{"initial_balance":10000}'
ALICE=$(field id)
call POST /v1/accounts 201
BOB=$(field id)

step "Fetch Alice's account"
call GET "/v1/accounts/$ALICE" 200

step "Deposit 25.00 into Alice's account"
call POST "/v1/accounts/$ALICE/deposits" 201 '{"amount":2500}' "$RUN-deposit"
DEPOSIT_ID=$(field id)

step "Retry the same deposit with the same Idempotency-Key: same response, no second deposit"
call POST "/v1/accounts/$ALICE/deposits" 201 '{"amount":2500}' "$RUN-deposit"
expect "$(field id)" "$DEPOSIT_ID" "replayed transaction id"
call GET "/v1/accounts/$ALICE" 200
expect "$(field balance)" 12500 "Alice's balance after replay"

step "Reuse that key for a different amount: rejected"
call POST "/v1/accounts/$ALICE/deposits" 409 '{"amount":999}' "$RUN-deposit"

step "Transfer 40.00 from Alice to Bob"
call POST /v1/transfers 201 "{\"from_account_id\":\"$ALICE\",\"to_account_id\":\"$BOB\",\"amount\":4000}" "$RUN-transfer"

step "Retry the transfer with the same key: same response, money moves once"
call POST /v1/transfers 201 "{\"from_account_id\":\"$ALICE\",\"to_account_id\":\"$BOB\",\"amount\":4000}" "$RUN-transfer"

step "Rejected transfers: insufficient funds, same account, unknown account"
call POST /v1/transfers 422 "{\"from_account_id\":\"$BOB\",\"to_account_id\":\"$ALICE\",\"amount\":999999}" "$RUN-insufficient"
call POST /v1/transfers 422 "{\"from_account_id\":\"$ALICE\",\"to_account_id\":\"$ALICE\",\"amount\":100}" "$RUN-same"
call POST /v1/transfers 422 "{\"from_account_id\":\"$ALICE\",\"to_account_id\":\"00000000-0000-0000-0000-000000000001\",\"amount\":100}" "$RUN-unknown"

step "Malformed requests: missing Idempotency-Key, invalid JSON, negative initial balance, unknown route"
call POST "/v1/accounts/$ALICE/deposits" 400 '{"amount":100}'
call POST "/v1/accounts/$ALICE/deposits" 400 '{"amount":' "$RUN-badjson"
call POST /v1/accounts 422 '{"initial_balance":-1}'
call GET /v1/nope 404

step "Alice's transactions, newest first (opening balance included)"
call GET "/v1/accounts/$ALICE/transactions" 200

step "Final balances: Alice 100.00 + 25.00 - 40.00 = 85.00, Bob 40.00"
call GET "/v1/accounts/$ALICE" 200
expect "$(field balance)" 8500 "Alice's final balance"
call GET "/v1/accounts/$BOB" 200
expect "$(field balance)" 4000 "Bob's final balance"

printf '\n%sAll checks passed.%s\n' "$green" "$reset"
