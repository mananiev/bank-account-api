package storage_test

import (
	"strings"
	"testing"

	"github.com/mananiev/bank-account-api/internal/testdb"
)

func TestMain(m *testing.M) { testdb.Main(m) }

// The ledger is append-only, enforced by a database trigger rather than by
// application code: even direct SQL cannot rewrite history.
func TestLedgerEntriesAreAppendOnly(t *testing.T) {
	db := testdb.New(t)
	acc, err := db.Store.CreateAccount(t.Context(), 100) // writes one OPENING ledger entry
	if err != nil {
		t.Fatal(err)
	}

	statements := map[string]string{
		"UPDATE":   `UPDATE ledger_entries SET amount = 1 WHERE account_id = '` + acc.ID.String() + `'`,
		"DELETE":   `DELETE FROM ledger_entries WHERE account_id = '` + acc.ID.String() + `'`,
		"TRUNCATE": `TRUNCATE ledger_entries`,
	}
	for op, sql := range statements {
		t.Run(op, func(t *testing.T) {
			_, err := db.Pool.Exec(t.Context(), sql)
			if err == nil || !strings.Contains(err.Error(), "append-only") {
				t.Fatalf("%s: got error %v, want the append-only trigger to reject it", op, err)
			}
		})
	}
	db.AssertLedgerConsistent(t, acc.ID)
}
