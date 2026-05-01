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
// service and streams Row events back. StubRunner sleeps for a
// configurable duration and returns no rows; the envtest reconciler
// suite uses it. See ADR-0019 §5 for the seam rationale (with the
// R4.2 addendum recording the `jobID` + `outputSinkKind` evolution)
// and ADR-0020 §5 for the refactor that retired SubprocessRunner in
// favour of the gRPC client.
package runner

import (
	"context"
	"io"
	"time"

	"github.com/google/uuid"

	enginev1alpha1 "github.com/FabioCaffarello/spectre/proto/gen/go/spectre/engine/v1alpha1"
)

// JobRunner executes a DSL job document and writes JSONL rows to the
// supplied writer. Implementations report total rows on success or
// surface an error on validation, launch, runtime, or timeout failure.
// The reconciler does not discriminate between failure modes for
// v1alpha1; any non-nil error transitions the ScrapeJob to Failed.
//
// Parameter accumulation across phases:
//
//   - R4.2 added jobID + outputSinkKind.
//
//   - R4.4 added kafkaTopic.
//
//   - R5.1 adds s3Config + webhookConfig.
//
//   - jobID — the Kubernetes UID of the ScrapeJob, used as the
//     `jobs.id` UUID in the engine's Postgres write path
//     (ADR-0023 §2). The reconciler parses `ScrapeJob.UID`; runner
//     implementations forward verbatim.
//
//   - outputSinkKind — one of "stdout" / "kafka" / "s3" / "webhook"
//     derived from `Spec.OutputSink`. The engine writes this to
//     `jobs.output_sink_kind` and gates `job_rows` appends on it.
//
//   - kafkaTopic — the topic name from `Spec.OutputSink.Kafka.Topic`
//     when the sink is Kafka; empty for every other variant. The
//     engine consumes this only when `outputSinkKind = "kafka"`
//     (ADR-0023 §3 R4.4 addendum); runner implementations forward
//     verbatim. An empty topic with `outputSinkKind = "kafka"`
//     fails the job at the engine with `KAFKA_TOPIC_REQUIRED`.
//
//   - s3Config — the per-job S3 sink config (bucket / key /
//     endpoint / region) from `Spec.OutputSink.S3` when the sink is
//     S3; nil otherwise. ADR-0024 §3.
//
//   - webhookConfig — the per-job webhook sink config (url /
//     method / batchSize) from `Spec.OutputSink.Webhook` when the
//     sink is Webhook; nil otherwise. ADR-0024 §4.
//
// The R3.1 vindication of the abstraction holds in spirit — "run a
// job, write output, return rows/error" is preserved. The parameter
// list now stands at 7; ADR-0019 §5 R5.1 addendum records the
// trade-off and the deferred v1alpha2 RunRequest struct refactor.
// ADR-0019 §5 R4.2 / R4.4 / R5.1 addenda track the evolutions.
type JobRunner interface {
	Run(
		ctx context.Context,
		jobID uuid.UUID,
		jobDSL string,
		outputSinkKind string,
		kafkaTopic string,
		s3Config *enginev1alpha1.S3SinkConfig,
		webhookConfig *enginev1alpha1.WebhookSinkConfig,
		writer io.Writer,
	) (int64, error)
}

// StubRunner is the JobRunner used by every envtest case in the
// reconciler suite. It sleeps for SleepDuration, honours context
// cancellation, and returns (0, nil) on completion. It writes
// nothing to the supplied writer because the test surface verifies
// state-machine transitions, not engine interaction. The R4.2 /
// R4.4 / R5.1 interface evolutions accept and ignore new
// parameters without changing StubRunner's behaviour.
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
	_ *enginev1alpha1.S3SinkConfig,
	_ *enginev1alpha1.WebhookSinkConfig,
	_ io.Writer,
) (int64, error) {
	select {
	case <-time.After(r.SleepDuration):
		return 0, nil
	case <-ctx.Done():
		return 0, ctx.Err()
	}
}
