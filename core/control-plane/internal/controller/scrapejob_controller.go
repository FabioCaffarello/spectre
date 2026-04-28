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
	"fmt"
	"os"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	spectrev1alpha2 "github.com/FabioCaffarello/spectre/core/control-plane/api/v1alpha2"
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
// dialling a real gRPC service. ADR-0019 §5's JobRunner interface is
// preserved (vindicated R3.1).
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
			return r.transitionToFailed(ctx, &job, err.Error())
		}
		endpoint, err := resolveEngineEndpoint(job.Spec.EngineRef, job.Namespace, r.DefaultEngineEndpoint)
		if err != nil {
			return r.transitionToFailed(ctx, &job, err.Error())
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

		jr := r.RunnerFactory(job.Status.ResolvedEngineEndpoint)
		// JSONL rows flow to the operator's stdout so they surface in
		// `kubectl logs <operator-pod>` per ADR-0019 §6.
		// EngineClientRunner forwards every Row event's json_line;
		// StubRunner ignores the writer.
		rows, runErr := jr.Run(runCtx, job.Spec.JobDSL, os.Stdout)

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

// transitionToFailed records a Failed terminal phase with the given
// error message and a CompletedAt timestamp. Used at Pending →
// Failed for spec-level rejections (unsupported sink, malformed
// EngineRef).
func (r *ScrapeJobReconciler) transitionToFailed(
	ctx context.Context,
	job *spectrev1alpha2.ScrapeJob,
	message string,
) (ctrl.Result, error) {
	job.Status.Phase = spectrev1alpha2.ScrapeJobPhaseFailed
	job.Status.Error = message
	now := metav1.Now()
	job.Status.CompletedAt = &now
	return ctrl.Result{}, r.Status().Update(ctx, job)
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
