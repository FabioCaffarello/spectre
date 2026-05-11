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

package runner

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/google/uuid"
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	enginev1alpha1 "github.com/FabioCaffarello/spectre/proto/gen/go/spectre/engine/v1alpha1"
)

// EngineClientRunner runs a ScrapeJob by streaming through the
// spectre.engine.v1alpha1.Engine.RunJob RPC. It dials the engine
// endpoint per Run call, consumes the streaming response, writes each
// Row.json_line event to the supplied writer, and returns the
// Completed.rows_extracted count from the terminal event.
//
// EngineClientRunner is the third implementation against the
// JobRunner seam introduced by ADR-0019 §5; StubRunner and
// SubprocessRunner (retired in R3.1) preceded it. The seam
// signature did not change. See ADR-0020 §5 for the refactor that
// motivated the swap and ADR-0022 for the TCP transport.
type EngineClientRunner struct {
	// EngineEndpoint is the host:port (or grpc://host:port) of the
	// engine service the operator dials. Empty values are rejected
	// at Run time. Production deployments set this from the
	// SPECTRE_ENGINE_ENDPOINT env var; local development defaults
	// to 127.0.0.1:8090 (the engine's canonical port — ADR-0021 §4
	// enacted by ADR-0025 §7).
	EngineEndpoint string

	// dialFunc is the gRPC dial seam. Production code leaves this
	// nil and uses defaultDial; tests inject a bufconn-backed
	// dialer so the runner exercises a real gRPC server in the
	// same process without a TCP listener.
	dialFunc func(ctx context.Context, endpoint string) (*grpc.ClientConn, error)
}

// Compile-time assertion that the runner satisfies JobRunner.
var _ JobRunner = (*EngineClientRunner)(nil)

// DialError is the typed error returned when the gRPC channel to
// the engine cannot be established. The reconciler uses
// `errors.As(err, &runner.DialError{})` to distinguish dial-level
// failures (network unreachable, deadline, engine pod not yet
// serving) from stream-level / engine-reported failures so the
// `spectre_operator_engine_dial_failures_total` counter (ADR-0031
// §5.2) is scoped to true transport failures.
type DialError struct {
	Endpoint string
	Cause    error
}

// Error implements `error`.
func (e *DialError) Error() string {
	return fmt.Sprintf("engine client runner: dial %s: %v", e.Endpoint, e.Cause)
}

// Unwrap exposes the underlying gRPC dial error.
func (e *DialError) Unwrap() error { return e.Cause }

// Run implements JobRunner. It dials the engine, opens a RunJob
// stream, copies every Row event into writer, and returns either the
// Completed event's row count on success or a non-nil error on any
// failure (dial error, stream-level error, Failed event, or context
// cancellation). Connection lifecycle is per-call: the gRPC channel
// opens at Run start and closes at Run return. v1alpha1's reconciler
// processes ScrapeJobs sequentially (MaxConcurrentReconciles=1) so
// per-call dial keeps connection state simple at the cost of a
// sub-millisecond TCP+HTTP/2 setup per job.
//
// Parameter forwarding contract:
//   - jobID + outputSinkKind (R4.2) → RunJobRequest job_id + output_sink_kind.
//   - kafkaTopic (R4.4) → RunJobRequest kafka_topic.
//   - s3Config (R5.1) → RunJobRequest s3 nested message; nil for
//     non-S3 sinks.
//   - webhookConfig (R5.1) → RunJobRequest webhook nested message;
//     nil for non-webhook sinks.
//
// The empty-uuid case sends an empty job_id string; the engine then
// generates a fresh UUID — kept so hand-written gRPC clients
// without UID provenance still work.
func (r *EngineClientRunner) Run(
	ctx context.Context,
	jobID uuid.UUID,
	jobDSL string,
	outputSinkKind string,
	kafkaTopic string,
	s3Config *enginev1alpha1.S3SinkConfig,
	webhookConfig *enginev1alpha1.WebhookSinkConfig,
	writer io.Writer,
) (int64, error) {
	if r.EngineEndpoint == "" {
		return 0, fmt.Errorf("engine client runner: engine endpoint is empty")
	}

	dial := r.dialFunc
	if dial == nil {
		dial = defaultDial
	}

	conn, err := dial(ctx, r.EngineEndpoint)
	if err != nil {
		return 0, &DialError{Endpoint: r.EngineEndpoint, Cause: err}
	}
	defer func() { _ = conn.Close() }()

	jobIDStr := ""
	if jobID != uuid.Nil {
		jobIDStr = jobID.String()
	}

	client := enginev1alpha1.NewEngineClient(conn)
	stream, err := client.RunJob(ctx, &enginev1alpha1.RunJobRequest{
		JobDsl:         jobDSL,
		JobId:          jobIDStr,
		OutputSinkKind: outputSinkKind,
		KafkaTopic:     kafkaTopic,
		S3:             s3Config,
		Webhook:        webhookConfig,
	})
	if err != nil {
		return 0, fmt.Errorf("engine client runner: open RunJob: %w", err)
	}

	var rows int64
	for {
		resp, recvErr := stream.Recv()
		if errors.Is(recvErr, io.EOF) {
			return rows, fmt.Errorf("engine client runner: stream closed without terminal event")
		}
		if recvErr != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return rows, ctxErr
			}
			// W3.1 Cluster G follow-up: with `otelgrpc.NewClientHandler`
			// in the dial options the gRPC stream surfaces context
			// cancellation as a status-coded recv error before
			// `ctx.Err()` propagates locally (the stats handler
			// observes the RST_STREAM ahead of the outer context's
			// own Done channel). Map the canonical status codes back
			// to their `context` sentinels so the reconciler's
			// `errors.Is(err, context.DeadlineExceeded)` /
			// `errors.Is(err, context.Canceled)` checks remain stable.
			if s, ok := status.FromError(recvErr); ok {
				switch s.Code() {
				case codes.DeadlineExceeded:
					return rows, context.DeadlineExceeded
				case codes.Canceled:
					return rows, context.Canceled
				}
			}
			return rows, fmt.Errorf("engine client runner: recv: %w", recvErr)
		}

		switch event := resp.Event.(type) {
		case *enginev1alpha1.RunJobResponse_Row:
			if _, werr := fmt.Fprintln(writer, event.Row.GetJsonLine()); werr != nil {
				return rows, fmt.Errorf("engine client runner: write row: %w", werr)
			}
			rows++
		case *enginev1alpha1.RunJobResponse_Completed:
			return event.Completed.GetRowsExtracted(), nil
		case *enginev1alpha1.RunJobResponse_Failed:
			return rows, fmt.Errorf("engine client runner: engine reported failure: %s: %s",
				event.Failed.GetErrorCode(), event.Failed.GetErrorMessage())
		case nil:
			return rows, fmt.Errorf("engine client runner: empty event in RunJob response")
		default:
			return rows, fmt.Errorf("engine client runner: unknown event type %T", event)
		}
	}
}

// defaultDial is the production gRPC dial. Plain-text transport per
// ADR-0022 §6 — TLS / mTLS is deferred to v1alpha2; in v1alpha1 the
// operator-engine traffic runs on a private network namespace
// (Compose / Kubernetes Pod network) where plain-text is acceptable.
//
// W3.1 Cluster E adds the OpenTelemetry gRPC stats handler so the
// outgoing RunJob RPC carries the active span's `traceparent`
// metadata per ADR-0031 §4.1. The handler also emits client-side
// spans wrapping each RPC — the engine receives both a propagated
// trace context AND a parent span name set by `otelgrpc`.
func defaultDial(ctx context.Context, endpoint string) (*grpc.ClientConn, error) {
	return grpc.NewClient(endpoint,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithStatsHandler(otelgrpc.NewClientHandler()),
	)
}
