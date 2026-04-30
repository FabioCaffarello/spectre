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

package db_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/pashagolub/pgxmock/v3"

	"github.com/FabioCaffarello/spectre/operators/control-plane/internal/db"
)

func TestGetJobReturnsHydratedRow(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatalf("pgxmock.NewPool: %v", err)
	}
	defer mock.Close()

	id := uuid.New()
	createdAt := time.Date(2026, 4, 28, 12, 0, 0, 0, time.UTC)
	startedAt := createdAt.Add(time.Second)
	completedAt := startedAt.Add(5 * time.Second)
	rowsExtracted := int64(42)
	endpoint := "engine.spectre-system.svc.cluster.local:8090"

	mock.ExpectQuery("SELECT id, status, driver, output_sink_kind").
		WithArgs(id).
		WillReturnRows(pgxmock.NewRows([]string{
			"id", "status", "driver", "output_sink_kind",
			"created_at", "started_at", "completed_at",
			"rows_extracted", "error", "resolved_engine_endpoint",
		}).AddRow(
			id, "completed", "playwright", "stdout",
			createdAt, &startedAt, &completedAt,
			&rowsExtracted, (*string)(nil), &endpoint,
		))

	got, err := db.GetJob(context.Background(), mock, id)
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	if got.Status != db.JobStatusCompleted {
		t.Fatalf("Status = %q, want completed", got.Status)
	}
	if got.Driver != "playwright" {
		t.Fatalf("Driver = %q, want playwright", got.Driver)
	}
	if got.OutputSinkKind != "stdout" {
		t.Fatalf("OutputSinkKind = %q, want stdout", got.OutputSinkKind)
	}
	if got.RowsExtracted == nil || *got.RowsExtracted != 42 {
		t.Fatalf("RowsExtracted = %v, want 42", got.RowsExtracted)
	}
	if got.ResolvedEngineEndpoint == nil || *got.ResolvedEngineEndpoint != endpoint {
		t.Fatalf("ResolvedEngineEndpoint = %v, want %q", got.ResolvedEngineEndpoint, endpoint)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestGetJobMissingRowMapsToErrJobNotFound(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatalf("pgxmock.NewPool: %v", err)
	}
	defer mock.Close()

	id := uuid.New()
	mock.ExpectQuery("SELECT id, status, driver, output_sink_kind").
		WithArgs(id).
		WillReturnError(pgx.ErrNoRows)

	_, err = db.GetJob(context.Background(), mock, id)
	if !errors.Is(err, db.ErrJobNotFound) {
		t.Fatalf("err = %v, want ErrJobNotFound", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestCountJobRowsReturnsAggregate(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatalf("pgxmock.NewPool: %v", err)
	}
	defer mock.Close()

	id := uuid.New()
	mock.ExpectQuery(`SELECT COUNT\(\*\) FROM job_rows`).
		WithArgs(id).
		WillReturnRows(pgxmock.NewRows([]string{"count"}).AddRow(int64(7)))

	n, err := db.CountJobRows(context.Background(), mock, id)
	if err != nil {
		t.Fatalf("CountJobRows: %v", err)
	}
	if n != 7 {
		t.Fatalf("n = %d, want 7", n)
	}
}
