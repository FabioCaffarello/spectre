/*
Copyright 2026 Fabio Caffarello.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

// Package db is the control plane's read-side view of the engine's
// `jobs` and `job_rows` tables. ADR-0023 §2 commits the engine to
// owning the schema (writes) and the control plane to consuming it
// for `Status.HistoryQueryable` and operator-restart recovery (R4.2
// Step 10). Per ADR-0023 §8 the driver is `pgx/v5`; the Go module's
// historical `lib/pq` is in maintenance mode and explicitly off the
// table.
//
// The package is intentionally narrow: a typed pool wrapper, two
// query functions matching the read paths v1alpha1 needs, and
// pgxmock-backed unit tests. Heavier query needs land when
// v1alpha2's HistoryQueryable surface materialises.
package db

import (
	"context"
	"fmt"
	"os"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// URLEnv is the environment variable carrying the operator's
// Postgres URL. Matches ADR-0021 §5 / ADR-0023 §12 — the same
// `SPECTRE_POSTGRES_URL` the engine binary reads.
const URLEnv = "SPECTRE_POSTGRES_URL"

// Pool is the minimal interface the typed query functions in jobs.go
// consume. `*pgxpool.Pool` satisfies it natively; `pgxmock.PgxPoolIface`
// satisfies it for unit tests in db_test.go. The interface lets the
// reconciler swap a real pool for a mock without runtime DI plumbing.
type Pool interface {
	// QueryRow executes a query expected to return at most one row.
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	// Query executes a query expected to return zero or more rows.
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	// Exec executes a non-row-returning statement.
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// Database wraps a pgxpool.Pool used by the operator for read-side
// access to the engine's `jobs` / `job_rows` tables. The pool is
// safe for concurrent use; the reconciler hands the same pool to
// every Reconcile call rather than acquiring per-call connections.
type Database struct {
	// Pool is the active pgxpool. Consumers may call its methods
	// directly (e.g. `db.Pool.Acquire(ctx)`); the typed query
	// functions in jobs.go are the preferred surface for the
	// engine's schema.
	Pool *pgxpool.Pool
}

// FromEnv constructs a Database from `SPECTRE_POSTGRES_URL`. The
// pool is dialled eagerly via Ping so a misconfigured deployment
// fails at operator startup rather than at first reconcile, mirroring
// the engine's startup-time discipline (ADR-0023 §6).
func FromEnv(ctx context.Context) (*Database, error) {
	url := os.Getenv(URLEnv)
	if url == "" {
		return nil, fmt.Errorf("postgres: %s must be set", URLEnv)
	}

	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, fmt.Errorf("postgres: parse url: %w", err)
	}

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("postgres: new pool: %w", err)
	}

	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("postgres: ping: %w", err)
	}

	return &Database{Pool: pool}, nil
}

// Close releases the underlying pool. The reconciler is the sole
// owner of the Database; the operator binary calls Close on
// shutdown signal as part of its normal teardown.
func (d *Database) Close() {
	if d.Pool != nil {
		d.Pool.Close()
	}
}
