// Package storage implements persistence for the account domain on PostgreSQL.
package storage

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Postgres is the PostgreSQL-backed store. It implements the repository
// interface declared by package account.
type Postgres struct {
	pool *pgxpool.Pool
}

// Open creates a connection pool for databaseURL and verifies connectivity.
// Pool settings can be tuned through the URL (e.g. ?pool_max_conns=10).
func Open(ctx context.Context, databaseURL string) (*Postgres, error) {
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		return nil, fmt.Errorf("create pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}
	return &Postgres{pool: pool}, nil
}

// Ping reports whether the database is reachable.
func (p *Postgres) Ping(ctx context.Context) error {
	return p.pool.Ping(ctx)
}

// Close releases all pool connections.
func (p *Postgres) Close() {
	p.pool.Close()
}
