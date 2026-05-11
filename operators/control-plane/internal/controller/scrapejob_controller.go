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

package controller

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	spectrev1alpha2 "github.com/FabioCaffarello/spectre/operators/control-plane/api/v1alpha2"
	"github.com/FabioCaffarello/spectre/operators/control-plane/internal/db"
	"github.com/FabioCaffarello/spectre/operators/control-plane/internal/runner"
	"github.com/FabioCaffarello/spectre/operators/control-plane/internal/telemetry"
	enginev1alpha1 "github.com/FabioCaffarello/spectre/proto/gen/go/spectre/engine/v1alpha1"
)

// defaultJobTimeout is the timeout applied to ScrapeJob runs whose
// Spec.TimeoutSeconds is unset. Matches the v1alpha2 CRD default
// declared via the kubebuilder marker.
const defaultJobTimeout = 10 * time.Minute

// defaultEnginePort is the canonical Service port for an engine
// reference whose EngineServiceRef.Port is unset. Matches ADR-0021's
// engine port and the operator's `--engine-endpoint` default.
const defaultEnginePort = int32(8090)

// runnerFactory constructs a JobRunner for a single Reconcile call.
// Production wires this to a closure that returns an
// EngineClientRunner bound to the resolved endpoint; envtest cases
// inject a fixed StubRunner / errorRunner regardless of endpoint.
type runnerFactory func(endpoint string) runner.JobRunner

// ScrapeJobReconciler reconciles a ScrapeJob object.
//
// The reconciler is a state machine over Status.Phase per ADR-0019 §4:
// Pending → Running → Completed | Failed, with terminal phases
// short-circuiting subsequent reconciliations. The actual job
// execution is delegated to a JobRunner constructed per-Reconcile
// (R3.2) — the resolved engine endpoint depends on Spec.EngineRef,
// so the runner cannot be a long-lived field on the reconciler. The
// `RunnerFactory` seam lets envtest substitute StubRunner without
// dialling a real gRPC service. ADR-0019 §5's JobRunner interface
// preserved in spirit; the R4.2 addendum records the
// jobID + outputSinkKind evolution.
//
// R4.2: the reconciler also reads Postgres before invoking the
// runner. ADR-0023 §2 makes Postgres the durable store underneath
// `Status.Phase`; if the operator restarts mid-job and observes a
// ScrapeJob still at Running, the reconciler queries `jobs` by
// `ScrapeJob.UID` to discover whether the engine already finished.
// If the persisted row reports completed/failed, Status.Phase syncs
// without re-running. If the row is absent (engine has not yet
// inserted) or the read errors, the reconciler proceeds to invoke
// the runner.
type ScrapeJobReconciler struct {
	client.Client
	Scheme *runtime.Scheme

	// DefaultEngineEndpoint is the host:port the operator dials when
	// a ScrapeJob's Spec.EngineRef is nil. Set from
	// `--engine-endpoint` / `SPECTRE_ENGINE_ENDPOINT` at startup.
	DefaultEngineEndpoint string

	// RunnerFactory builds the JobRunner used for a single Reconcile.
	// Production wires it to a closure returning EngineClientRunner;
	// envtest injects a StubRunner-returning factory. Required.
	RunnerFactory runnerFactory

	// DB is the Postgres handle used for restart recovery in the
	// Running phase. Production sets this from
	// `SPECTRE_POSTGRES_URL` at operator startup (ADR-0023 §6 + §12).
	// Envtest cases that don't exercise the recovery path leave it
	// nil; the reconciler treats `nil` as "skip recovery and invoke
	// the runner". ADR-0023 §6 still mandates Postgres in production
	// — the operator binary refuses to start without it.
	DB db.Pool

	// Metrics is the operator-side telemetry handle for ADR-0031
	// §5.2 recordings. Production wires this from the
	// `telemetry.Register` return value at startup; envtest leaves
	// it nil and the reconciler skips emission. The reconciler
	// only emits `EngineDialFailuresTotal` here — the
	// `spectre_operator_scrapejobs_total{phase}` gauge is
	// populated by a separate prometheus.Collector that lists
	// from the cache on every scrape.
	Metrics *telemetry.Metrics
}

// +kubebuilder:rbac:groups=spectre.io,resources=scrapejobs,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=spectre.io,resources=scrapejobs/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=spectre.io,resources=scrapejobs/finalizers,verbs=update

// Reconcile drives a ScrapeJob through its lifecycle phases. See
// ADR-0019 §4 (state machine), §5 (JobRunner seam), and the R3.2
// addendum (EngineRef resolution + OutputSink enforcement).
func (r *ScrapeJobReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	// ADR-0031 §4.2 root span. The engine's `engine.run_job` span
	// extracts this as parent via the W3C `traceparent` propagator
	// otelgrpc injects on the EngineClientRunner dial. SpanKind
	// `Internal` matches the OTel semantic convention for
	// reconciler-style background work (no inbound RPC, no
	// outbound RPC at this level — the runner span is the dial).
	ctx, span := otel.Tracer(telemetry.TracerName).Start(
		ctx,
		"operator.reconcile_scrapejob",
		trace.WithSpanKind(trace.SpanKindInternal),
		trace.WithAttributes(
			attribute.String("scrapejob.namespace", req.Namespace),
			attribute.String("scrapejob.name", req.Name),
		),
	)
	defer span.End()

	var job spectrev1alpha2.ScrapeJob
	if err := r.Get(ctx, req.NamespacedName, &job); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	// `job_id` lands on the span once the resource is fetched. ADR-
	// 0019 §4: `ScrapeJob.UID` is a Kubernetes-issued RFC 4122 UUID
	// that doubles as the engine-side `jobs.id` (ADR-0023 §2).
	span.SetAttributes(attribute.String("job_id", string(job.UID)))

	switch job.Status.Phase {
	case "":
		job.Status.Phase = spectrev1alpha2.ScrapeJobPhasePending
		if err := r.Status().Update(ctx, &job); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{Requeue: true}, nil

	case spectrev1alpha2.ScrapeJobPhasePending:
		if err := validateOutputSink(job.Spec.OutputSink); err != nil {
			return ctrl.Result{}, r.transitionToFailed(ctx, &job, err.Error())
		}
		endpoint, err := resolveEngineEndpoint(job.Spec.EngineRef, job.Namespace, r.DefaultEngineEndpoint)
		if err != nil {
			return ctrl.Result{}, r.transitionToFailed(ctx, &job, err.Error())
		}

		now := metav1.Now()
		job.Status.Phase = spectrev1alpha2.ScrapeJobPhaseRunning
		job.Status.StartedAt = &now
		job.Status.ResolvedEngineEndpoint = endpoint
		if err := r.Status().Update(ctx, &job); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{Requeue: true}, nil

	case spectrev1alpha2.ScrapeJobPhaseRunning:
		runCtx, cancel := context.WithTimeout(ctx, jobTimeout(&job))
		defer cancel()

		// R4.2: ADR-0023 §2's `jobs.id` is `UUID PRIMARY KEY`. We
		// reuse `ScrapeJob.UID` (a Kubernetes-issued RFC 4122 UUID)
		// rather than minting a separate identifier so a Postgres
		// reader can correlate rows back to the CR by name. Parse
		// failure is treated as an internal invariant violation —
		// Kubernetes guarantees UID format — and surfaces as Failed.
		jobUUID, err := uuid.Parse(string(job.UID))
		if err != nil {
			return ctrl.Result{}, r.transitionToFailed(
				ctx, &job,
				fmt.Sprintf("internal: ScrapeJob UID is not a UUID: %v", err),
			)
		}
		sinkKind := outputSinkKind(job.Spec.OutputSink)

		// Restart recovery (R4.2 / ADR-0023 §2). If a previous
		// operator instance had already invoked the engine and the
		// engine wrote a terminal row, sync Status.Phase from
		// Postgres and skip the runner. Other Postgres errors fail
		// the job with the underlying error message; ErrJobNotFound
		// (engine has not yet inserted) falls through to the runner.
		if r.DB != nil {
			if synced, syncErr := r.syncFromPostgres(ctx, &job, jobUUID); syncErr != nil {
				return ctrl.Result{}, r.transitionToFailed(
					ctx, &job,
					fmt.Sprintf("postgres: restart recovery failed: %v", syncErr),
				)
			} else if synced {
				return ctrl.Result{}, nil
			}
		}

		jr := r.RunnerFactory(job.Status.ResolvedEngineEndpoint)
		// JSONL rows flow to the operator's stdout so they surface in
		// `kubectl logs <operator-pod>` per ADR-0019 §6.
		// EngineClientRunner forwards every Row event's json_line;
		// StubRunner ignores the writer.
		// R4.4: kafkaTopic is sourced from `Spec.OutputSink.Kafka.Topic`
		// when the sink is Kafka; empty for every other variant.
		// The engine consumes it only on `output_sink_kind = "kafka"`.
		// R5.1: s3Config / webhookConfig are sourced from
		// `Spec.OutputSink.S3` / `.Webhook` when those variants are
		// set; nil for every other variant. The engine consumes
		// each only when `output_sink_kind` matches (ADR-0024 §3 / §4).
		kafkaTopic := outputSinkKafkaTopic(job.Spec.OutputSink)
		s3Config := outputSinkS3Config(job.Spec.OutputSink)
		webhookConfig := outputSinkWebhookConfig(job.Spec.OutputSink)
		rows, runErr := jr.Run(
			runCtx, jobUUID, job.Spec.JobDSL, sinkKind, kafkaTopic,
			s3Config, webhookConfig, os.Stdout,
		)

		now := metav1.Now()
		if runErr != nil {
			// ADR-0031 §5.2: count dial-level failures separately from
			// engine-reported failures so the operator dashboard can
			// distinguish "engine unreachable" (dial counter ticks)
			// from "engine ran the job and reported a failure" (dial
			// counter steady, Status.Phase=Failed).
			var dialErr *runner.DialError
			if r.Metrics != nil && errors.As(runErr, &dialErr) {
				r.Metrics.EngineDialFailuresTotal.Inc()
			}
			job.Status.Phase = spectrev1alpha2.ScrapeJobPhaseFailed
			job.Status.Error = runErr.Error()
		} else {
			job.Status.Phase = spectrev1alpha2.ScrapeJobPhaseCompleted
			job.Status.RowsExtracted = rows
		}
		job.Status.CompletedAt = &now
		return ctrl.Result{}, r.Status().Update(ctx, &job)

	case spectrev1alpha2.ScrapeJobPhaseCompleted, spectrev1alpha2.ScrapeJobPhaseFailed:
		return ctrl.Result{}, nil
	}

	return ctrl.Result{}, nil
}

// syncFromPostgres queries the engine's `jobs` table for `jobUUID`
// and, if it carries a terminal status, mirrors that state into
// the ScrapeJob's Status subresource (without invoking the runner).
//
// Returns (true, nil) when Status was synced from a terminal row
// — the caller exits the Running phase without re-running. Returns
// (false, nil) when the row is absent (engine has not yet inserted)
// or carries a non-terminal status (running) — the caller proceeds
// to the runner. Returns (false, err) for unexpected Postgres
// errors; the caller marks the ScrapeJob Failed with the error.
func (r *ScrapeJobReconciler) syncFromPostgres(
	ctx context.Context,
	job *spectrev1alpha2.ScrapeJob,
	jobUUID uuid.UUID,
) (bool, error) {
	persisted, err := db.GetJob(ctx, r.DB, jobUUID)
	if err != nil {
		if errors.Is(err, db.ErrJobNotFound) {
			return false, nil
		}
		return false, err
	}

	switch persisted.Status {
	case db.JobStatusCompleted:
		now := metav1.Now()
		job.Status.Phase = spectrev1alpha2.ScrapeJobPhaseCompleted
		job.Status.CompletedAt = &now
		if persisted.RowsExtracted != nil {
			job.Status.RowsExtracted = *persisted.RowsExtracted
		}
		return true, r.Status().Update(ctx, job)
	case db.JobStatusFailed:
		now := metav1.Now()
		job.Status.Phase = spectrev1alpha2.ScrapeJobPhaseFailed
		job.Status.CompletedAt = &now
		if persisted.Error != nil {
			job.Status.Error = *persisted.Error
		}
		return true, r.Status().Update(ctx, job)
	default:
		// `running` (or unexpected `pending`): the engine still has
		// the job in flight. The reconciler proceeds to invoke the
		// runner; the engine treats the duplicate insert_job as a
		// PK violation that surfaces as Internal — at which point
		// the reconciler marks the ScrapeJob Failed. Acceptable
		// for v1alpha1; v1alpha2 may add a "rejoin running stream"
		// affordance.
		return false, nil
	}
}

// transitionToFailed records a Failed terminal phase with the given
// error message and a CompletedAt timestamp. Used at Pending →
// Failed for spec-level rejections (unsupported sink, malformed
// EngineRef). Returns the apiserver Update error; callers wrap with
// `ctrl.Result{}` so the reconciler does not requeue.
func (r *ScrapeJobReconciler) transitionToFailed(
	ctx context.Context,
	job *spectrev1alpha2.ScrapeJob,
	message string,
) error {
	job.Status.Phase = spectrev1alpha2.ScrapeJobPhaseFailed
	job.Status.Error = message
	now := metav1.Now()
	job.Status.CompletedAt = &now
	return r.Status().Update(ctx, job)
}

// jobTimeout resolves Spec.TimeoutSeconds into a duration, falling
// back to defaultJobTimeout when unset.
func jobTimeout(job *spectrev1alpha2.ScrapeJob) time.Duration {
	if job.Spec.TimeoutSeconds == nil {
		return defaultJobTimeout
	}
	return time.Duration(*job.Spec.TimeoutSeconds) * time.Second
}

// resolveEngineEndpoint resolves a ScrapeJob's EngineRef to a
// host:port string. The CEL rule on EngineRef enforces "exactly one
// of Service or Endpoint" at admission, so the reconciler treats
// neither-set as an internal error rather than a user-recoverable
// validation failure.
//
//   - Endpoint set: returned verbatim.
//   - Service set: rendered as
//     `<name>.<namespace-or-job-namespace>.svc.cluster.local:<port-or-8090>`.
//   - EngineRef nil: fallback returned.
func resolveEngineEndpoint(
	ref *spectrev1alpha2.EngineRef,
	jobNamespace string,
	fallback string,
) (string, error) {
	if ref == nil {
		if fallback == "" {
			return "", fmt.Errorf("EngineRef is unset and no default engine endpoint is configured")
		}
		return fallback, nil
	}
	if ref.Endpoint != "" {
		return ref.Endpoint, nil
	}
	if ref.Service != nil {
		ns := ref.Service.Namespace
		if ns == "" {
			ns = jobNamespace
		}
		port := defaultEnginePort
		if ref.Service.Port != nil {
			port = *ref.Service.Port
		}
		return fmt.Sprintf("%s.%s.svc.cluster.local:%d", ref.Service.Name, ns, port), nil
	}
	return "", fmt.Errorf("EngineRef has neither Service nor Endpoint set")
}

// outputSinkKind maps the v1alpha2 OutputSink discriminated union
// to the canonical string the engine writes to `jobs.output_sink_kind`
// (ADR-0023 §2). Returns the empty string when no variant is set;
// the CEL rule prevents that at admission, so callers should
// already have run validateOutputSink.
func outputSinkKind(sink spectrev1alpha2.OutputSink) string {
	switch {
	case sink.Stdout != nil:
		return "stdout"
	case sink.Kafka != nil:
		return "kafka"
	case sink.S3 != nil:
		return "s3"
	case sink.Webhook != nil:
		return "webhook"
	default:
		return ""
	}
}

// validateOutputSink enforces R5.1's runtime sink grammar: every
// v1alpha2 OutputSink variant (Stdout, Kafka, S3, Webhook) is
// behaviourally implemented. The CEL rule on OutputSink enforces
// "exactly one variant set" at admission, so the unset-everything
// case is treated as an internal error.
//
// Engine-side admission gating per ADR-0024 §5:
//
//   - Kafka: engine startup probes the broker; jobs against an
//     unreachable broker fail fast with `KAFKA_UNAVAILABLE`.
//   - S3: engine startup parses SPECTRE_S3_* env (or relies on the
//     AWS default credential chain); jobs against `None` fail fast
//     with `S3_UNAVAILABLE`.
//   - Webhook: per-job, no engine-level state. Failures surface
//     mid-job as `WEBHOOK_POST_FAILED`.
//
// Each branch defence-in-depths the CEL-enforced field constraints
// so a regenerated CRD without the rules still surfaces a clear
// error at admission.
func validateOutputSink(sink spectrev1alpha2.OutputSink) error {
	switch {
	case sink.Stdout != nil:
		return nil
	case sink.Kafka != nil:
		// R4.4: Kafka is wired end-to-end. Defence-in-depth on
		// topic emptiness — CEL's `MinLength=1` already enforces
		// this at admission, but we surface a clear error if the
		// CRD is ever regenerated without the rule.
		if sink.Kafka.Topic == "" {
			return fmt.Errorf("kafka output sink: topic must be non-empty")
		}
		return nil
	case sink.S3 != nil:
		// R5.1: S3 is wired end-to-end via the engine's
		// `aws-sdk-s3`. Defence-in-depth on bucket / key
		// emptiness — CEL enforces `MinLength=1` for both at
		// admission. ADR-0024 §3.
		if sink.S3.Bucket == "" {
			return fmt.Errorf("s3 output sink: bucket must be non-empty")
		}
		if sink.S3.Key == "" {
			return fmt.Errorf("s3 output sink: key must be non-empty")
		}
		return nil
	case sink.Webhook != nil:
		// R5.1: Webhook is wired end-to-end via the engine's
		// `reqwest` client. Defence-in-depth on URL emptiness —
		// CEL enforces `Pattern=^https?://.+$` and `MinLength=1`
		// at admission. ADR-0024 §4.
		if sink.Webhook.URL == "" {
			return fmt.Errorf("webhook output sink: url must be non-empty")
		}
		return nil
	default:
		return fmt.Errorf("OutputSink has no variant set")
	}
}

// outputSinkKafkaTopic extracts the topic name when the sink is
// Kafka, returning the empty string for every other variant. R4.4
// (ADR-0023 §3) ships the topic on `RunJobRequest.kafka_topic`
// (engine.proto field 4); the engine ignores the field for sinks
// other than Kafka.
func outputSinkKafkaTopic(sink spectrev1alpha2.OutputSink) string {
	if sink.Kafka != nil {
		return sink.Kafka.Topic
	}
	return ""
}

// outputSinkS3Config extracts the S3 sink config when the sink is
// S3, returning nil for every other variant. R5.1 (ADR-0024 §3)
// ships the config on `RunJobRequest.s3` (engine.proto field 5);
// the engine ignores the field for sinks other than S3.
func outputSinkS3Config(sink spectrev1alpha2.OutputSink) *enginev1alpha1.S3SinkConfig {
	if sink.S3 == nil {
		return nil
	}
	return &enginev1alpha1.S3SinkConfig{
		Bucket:   sink.S3.Bucket,
		Key:      sink.S3.Key,
		Endpoint: sink.S3.Endpoint,
		Region:   sink.S3.Region,
	}
}

// outputSinkWebhookConfig extracts the webhook sink config when
// the sink is Webhook, returning nil for every other variant. R5.1
// (ADR-0024 §4) ships the config on `RunJobRequest.webhook`
// (engine.proto field 6); the engine ignores the field for sinks
// other than Webhook.
func outputSinkWebhookConfig(sink spectrev1alpha2.OutputSink) *enginev1alpha1.WebhookSinkConfig {
	if sink.Webhook == nil {
		return nil
	}
	return &enginev1alpha1.WebhookSinkConfig{
		Url:       sink.Webhook.URL,
		Method:    sink.Webhook.Method,
		BatchSize: sink.Webhook.BatchSize,
	}
}

// SetupWithManager sets up the controller with the Manager.
func (r *ScrapeJobReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&spectrev1alpha2.ScrapeJob{}).
		Named("scrapejob").
		Complete(r)
}
