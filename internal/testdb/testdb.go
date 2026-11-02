// Package testdb provides a real PostgreSQL database for integration tests.
//
// One container is started lazily per test binary (i.e. per package) and
// migrated with the production migrations. Tests never clean up: each test
// creates its own accounts, so tests stay independent and can run in parallel.
// (Cleaning up would not be possible anyway: the ledger is append-only.)
package testdb

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/mananiev/bank-account-api/internal/storage"
)

// DB gives tests the store under test and a raw pool for assertions.
type DB struct {
	Store *storage.Postgres
	Pool  *pgxpool.Pool
}

var (
	once      sync.Once
	shared    *DB
	container *postgres.PostgresContainer
	startErr  error
)

// New returns the package-wide test database, starting it on first use.
// Integration tests are skipped with -short and fail if Docker is unavailable.
func New(t testing.TB) *DB {
	t.Helper()
	if testing.Short() {
		t.Skip("integration test: skipped in -short mode")
	}
	once.Do(start)
	if startErr != nil {
		t.Fatalf("start test database (Docker is required; run `go test -short ./...` for unit tests only): %v", startErr)
	}
	return shared
}

// Main runs the tests and then stops the container. Call it from TestMain.
func Main(m *testing.M) {
	code := m.Run()
	if shared != nil {
		shared.Pool.Close()
		shared.Store.Close()
	}
	if container != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		if err := container.Terminate(ctx); err != nil {
			fmt.Fprintf(os.Stderr, "terminate test database: %v\n", err)
		}
		cancel()
	}
	os.Exit(code)
}

func start() {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	c, err := postgres.Run(ctx, "postgres:16-alpine",
		postgres.WithDatabase("bank"),
		postgres.WithUsername("bank"),
		postgres.WithPassword("bank"),
		postgres.BasicWaitStrategies(),
	)
	if err != nil {
		startErr = fmt.Errorf("run container: %w", err)
		return
	}
	container = c

	dsn, err := c.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		startErr = fmt.Errorf("connection string: %w", err)
		return
	}

	store, err := storage.Open(ctx, dsn)
	if err != nil {
		startErr = err
		return
	}
	if err := store.Migrate(ctx, slog.New(slog.DiscardHandler)); err != nil {
		store.Close()
		startErr = err
		return
	}

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		store.Close()
		startErr = fmt.Errorf("open assertion pool: %w", err)
		return
	}

	shared = &DB{Store: store, Pool: pool}
}

// AssertLedgerConsistent checks that the account's balance equals the sum of
// its ledger entries (NFR2).
func (db *DB) AssertLedgerConsistent(t testing.TB, accountID uuid.UUID) {
	t.Helper()
	var balance, ledgerSum int64
	err := db.Pool.QueryRow(context.Background(), `
		SELECT a.balance, COALESCE(SUM(e.amount), 0)
		FROM accounts a
		LEFT JOIN ledger_entries e ON e.account_id = a.id
		WHERE a.id = $1
		GROUP BY a.balance`, accountID).Scan(&balance, &ledgerSum)
	if err != nil {
		t.Fatalf("query ledger for account %s: %v", accountID, err)
	}
	if balance != ledgerSum {
		t.Errorf("account %s: balance %d != sum of ledger entries %d", accountID, balance, ledgerSum)
	}
}
