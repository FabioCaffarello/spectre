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

// Package runner is the integration boundary between the ScrapeJob
// reconciler and the engine. The reconciler depends on JobRunner; PR15
// drops in a SubprocessRunner that shells out to the spectre engine
// binary. PR14 ships StubRunner, which sleeps for a configurable
// duration and returns no rows. See ADR-0019 §5 for the seam rationale.
package runner

import (
	"context"
	"io"
	"time"
)

// JobRunner executes a DSL job document and writes JSONL rows to the
// supplied writer. Implementations report total rows on success or
// surface an error on validation, launch, runtime, or timeout failure.
// The reconciler does not discriminate between failure modes for
// v1alpha1; any non-nil error transitions the ScrapeJob to Failed.
type JobRunner interface {
	Run(ctx context.Context, jobDSL string, writer io.Writer) (int64, error)
}

// StubRunner is the JobRunner used by PR14's reconciler and by every
// envtest case in the suite. It sleeps for SleepDuration, honours
// context cancellation, and returns (0, nil) on completion. It writes
// nothing to the supplied writer because PR14 does not produce real
// JSONL output.
type StubRunner struct {
	// SleepDuration is the simulated work time before Run returns.
	// Tests use a short duration (~10ms); the deployed manager uses
	// a longer one (~5s) so phase transitions are visible by hand
	// when watching `kubectl get scrapejob -w`.
	SleepDuration time.Duration
}

// Run implements JobRunner.
func (r *StubRunner) Run(ctx context.Context, _ string, _ io.Writer) (int64, error) {
	select {
	case <-time.After(r.SleepDuration):
		return 0, nil
	case <-ctx.Done():
		return 0, ctx.Err()
	}
}
