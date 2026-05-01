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

package db

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// JobStatus mirrors the CHECK-constrained values of the engine's
// `jobs.status` column (ADR-0023 §2). The constants here parallel
// the v1alpha2 ScrapeJobPhase values; the reconciler maps between
// them in scrapejob_controller.go.
type JobStatus string

const (
	// JobStatusPending is unused by the engine in v1alpha1 — the
	// engine inserts directly at running. Defined for completeness
	// against the schema's CHECK and forward compatibility.
	JobStatusPending JobStatus = "pending"
	// JobStatusRunning is the engine's first persisted state
	// (insert-time).
	JobStatusRunning JobStatus = "running"
	// JobStatusCompleted is the terminal success state.
	JobStatusCompleted JobStatus = "completed"
	// JobStatusFailed is the terminal failure state.
	JobStatusFailed JobStatus = "failed"
)

// Job is the in-memory projection of a `jobs` row the reconciler
// reads on restart recovery. Nullable columns are pointers so the
// caller can distinguish "unset" from "zero".
type Job struct {
	ID                     uuid.UUID
	Status                 JobStatus
	Driver                 string
	OutputSinkKind         string
	CreatedAt              time.Time
	StartedAt              *time.Time
	CompletedAt            *time.Time
	RowsExtracted          *int64
	Error                  *string
	ResolvedEngineEndpoint *string
}

// ErrJobNotFound is returned by GetJob when no row matches the id.
// The reconciler treats it as "the engine has not yet inserted this
// job's row" and proceeds with the normal Pending → Running path.
var ErrJobNotFound = errors.New("job not found")

// GetJob fetches one `jobs` row by id. Returns ErrJobNotFound if no
// row matches. Used by the reconciler on Reconcile-after-restart to
// recover Status.Phase from the engine's persisted view.
func GetJob(ctx context.Context, p Pool, id uuid.UUID) (*Job, error) {
	const sql = `
		SELECT id, status, driver, output_sink_kind,
		       created_at, started_at, completed_at,
		       rows_extracted, error, resolved_engine_endpoint
		FROM jobs
		WHERE id = $1
	`
	var j Job
	var status string
	err := p.QueryRow(ctx, sql, id).Scan(
		&j.ID,
		&status,
		&j.Driver,
		&j.OutputSinkKind,
		&j.CreatedAt,
		&j.StartedAt,
		&j.CompletedAt,
		&j.RowsExtracted,
		&j.Error,
		&j.ResolvedEngineEndpoint,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrJobNotFound
		}
		return nil, fmt.Errorf("postgres: select job %s: %w", id, err)
	}
	j.Status = JobStatus(status)
	return &j, nil
}

// CountJobRows returns the number of `job_rows` rows persisted for a
// given job. Lightweight surface used by R4.2 verification scripts
// and the conformance suite's smoke checks; the reconciler does not
// call it in v1alpha1.
func CountJobRows(ctx context.Context, p Pool, jobID uuid.UUID) (int64, error) {
	const sql = `SELECT COUNT(*) FROM job_rows WHERE job_id = $1`
	var n int64
	if err := p.QueryRow(ctx, sql, jobID).Scan(&n); err != nil {
		return 0, fmt.Errorf("postgres: count job_rows for %s: %w", jobID, err)
	}
	return n, nil
}
