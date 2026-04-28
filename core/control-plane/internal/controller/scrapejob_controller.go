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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	spectrev1alpha2 "github.com/FabioCaffarello/spectre/core/control-plane/api/v1alpha2"
	"github.com/FabioCaffarello/spectre/core/control-plane/internal/db"
	"github.com/FabioCaffarello/spectre/core/control-plane/internal/runner"
)

// defaultJobTimeout is the timeout applied to ScrapeJob runs whose
// Spec.TimeoutSeconds is unset. Matches the v1alpha2 CRD default
// declared via the kubebuilder marker.
const defaultJobTimeout = 10 * time.Minute

// defaultEnginePort is the canonical Service port for an engine
// reference whose EngineServiceRef.Port is unset. Matches ADR-0021's
// engine port and the operator's `--engine-endpoint` default.
const defaultEnginePort = int32(9090)

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
}

// +kubebuilder:rbac:groups=spectre.io,resources=scrapejobs,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=spectre.io,resources=scrapejobs/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=spectre.io,resources=scrapejobs/finalizers,verbs=update

// Reconcile drives a ScrapeJob through its lifecycle phases. See
// ADR-0019 §4 (state machine), §5 (JobRunner seam), and the R3.2
// addendum (EngineRef resolution + OutputSink enforcement).
func (r *ScrapeJobReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var job spectrev1alpha2.ScrapeJob
	if err := r.Get(ctx, req.NamespacedName, &job); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

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
		rows, runErr := jr.Run(runCtx, jobUUID, job.Spec.JobDSL, sinkKind, os.Stdout)

		now := metav1.Now()
		if runErr != nil {
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
//     `<name>.<namespace-or-job-namespace>.svc.cluster.local:<port-or-9090>`.
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
// already have run validateOutputSink. Kafka / S3 / Webhook return
// their canonical value here even though validateOutputSink rejects
// them today — the engine.proto field is forward-compatible
// (R4.2 Step 6) and the kind plumbing lands now so R4.4 / R5.1's
// reconciler diff is just removing rejection lines.
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

// validateOutputSink enforces R3.2's runtime sink grammar: only
// Stdout is accepted; Kafka, S3, and Webhook are schema-only and
// reject with an explicit pointer to the implementing phase. The CEL
// rule on OutputSink enforces "exactly one variant set" at admission,
// so the unset-everything case is treated as an internal error.
func validateOutputSink(sink spectrev1alpha2.OutputSink) error {
	switch {
	case sink.Stdout != nil:
		return nil
	case sink.Kafka != nil:
		return fmt.Errorf("kafka output sink not yet implemented (R4.4)")
	case sink.S3 != nil:
		return fmt.Errorf("s3 output sink not yet implemented (R5.1)")
	case sink.Webhook != nil:
		return fmt.Errorf("webhook output sink not yet implemented (R5.1)")
	default:
		return fmt.Errorf("OutputSink has no variant set")
	}
}

// SetupWithManager sets up the controller with the Manager.
func (r *ScrapeJobReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&spectrev1alpha2.ScrapeJob{}).
		Named("scrapejob").
		Complete(r)
}
