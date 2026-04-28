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
// reconciler and the engine. The reconciler depends on JobRunner;
// R3.1 wires EngineClientRunner, which dials the engine's gRPC
// service and streams Row events back. PR14 ships StubRunner, which
// sleeps for a configurable duration and returns no rows; the
// envtest reconciler suite continues to use it. See ADR-0019 §5 for
// the seam rationale (with the R4.2 addendum recording the
// `jobID` + `outputSinkKind` evolution) and ADR-0020 §5 for the
// refactor that retired SubprocessRunner (PR15) in favour of the
// gRPC client.
package runner

import (
	"context"
	"io"
	"time"

	"github.com/google/uuid"
)

// JobRunner executes a DSL job document and writes JSONL rows to the
// supplied writer. Implementations report total rows on success or
// surface an error on validation, launch, runtime, or timeout failure.
// The reconciler does not discriminate between failure modes for
// v1alpha1; any non-nil error transitions the ScrapeJob to Failed.
//
// R4.2 evolved the signature with `jobID` and `outputSinkKind`.
// R4.4 evolves it again with `kafkaTopic`:
//
//   - jobID — the Kubernetes UID of the ScrapeJob, used as the
//     `jobs.id` UUID in the engine's Postgres write path
//     (ADR-0023 §2). The reconciler parses `ScrapeJob.UID`; runner
//     implementations forward verbatim.
//   - outputSinkKind — one of "stdout" / "kafka" / "s3" / "webhook"
//     derived from `Spec.OutputSink`. The engine writes this to
//     `jobs.output_sink_kind` and gates `job_rows` appends on it.
//   - kafkaTopic — the topic name from `Spec.OutputSink.Kafka.Topic`
//     when the sink is Kafka; empty for every other variant. The
//     engine consumes this only when `outputSinkKind = "kafka"`
//     (ADR-0023 §3 R4.4 addendum); runner implementations forward
//     verbatim. An empty topic with `outputSinkKind = "kafka"`
//     fails the job at the engine with `KAFKA_TOPIC_REQUIRED`.
//
// The R3.1 vindication of the abstraction holds in spirit — "run a
// job, write output, return rows/error" is preserved. ADR-0019 §5's
// R4.2 / R4.4 addenda document the evolutions.
type JobRunner interface {
	Run(
		ctx context.Context,
		jobID uuid.UUID,
		jobDSL string,
		outputSinkKind string,
		kafkaTopic string,
		writer io.Writer,
	) (int64, error)
}

// StubRunner is the JobRunner used by PR14's reconciler and by every
// envtest case in the suite. It sleeps for SleepDuration, honours
// context cancellation, and returns (0, nil) on completion. It writes
// nothing to the supplied writer because PR14 does not produce real
// JSONL output. R4.2's interface evolution does not change StubRunner's
// behaviour — the new parameters are accepted and ignored, since
// the test surface verifies state-machine transitions, not engine
// interaction.
type StubRunner struct {
	// SleepDuration is the simulated work time before Run returns.
	// Tests use a short duration (~10ms); the deployed manager uses
	// a longer one (~5s) so phase transitions are visible by hand
	// when watching `kubectl get scrapejob -w`.
	SleepDuration time.Duration
}

// Run implements JobRunner.
func (r *StubRunner) Run(
	ctx context.Context,
	_ uuid.UUID,
	_ string,
	_ string,
	_ string,
	_ io.Writer,
) (int64, error) {
	select {
	case <-time.After(r.SleepDuration):
		return 0, nil
	case <-ctx.Done():
		return 0, ctx.Err()
	}
}
