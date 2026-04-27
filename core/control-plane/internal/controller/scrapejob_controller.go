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
	"io"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	spectrev1alpha1 "github.com/FabioCaffarello/spectre/core/control-plane/api/v1alpha1"
	"github.com/FabioCaffarello/spectre/core/control-plane/internal/runner"
)

// defaultJobTimeout is the timeout applied to ScrapeJob runs whose
// Spec.TimeoutSeconds is unset. Matches the documented default in the
// CRD field comment.
const defaultJobTimeout = 10 * time.Minute

// ScrapeJobReconciler reconciles a ScrapeJob object.
//
// The reconciler is a state machine over Status.Phase per ADR-0019 §4:
// Pending → Running → Completed | Failed, with terminal phases
// short-circuiting subsequent reconciliations. The actual job
// execution is delegated to a JobRunner injected by main.go (ADR-0019
// §5). PR14 wires StubRunner; PR15 will swap in SubprocessRunner.
type ScrapeJobReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	Runner runner.JobRunner
}

// +kubebuilder:rbac:groups=spectre.io,resources=scrapejobs,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=spectre.io,resources=scrapejobs/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=spectre.io,resources=scrapejobs/finalizers,verbs=update

// Reconcile drives a ScrapeJob through its lifecycle phases. See
// ADR-0019 §4 (state machine) and §5 (JobRunner seam) for the design.
func (r *ScrapeJobReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var job spectrev1alpha1.ScrapeJob
	if err := r.Get(ctx, req.NamespacedName, &job); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	switch job.Status.Phase {
	case "":
		job.Status.Phase = spectrev1alpha1.ScrapeJobPhasePending
		if err := r.Status().Update(ctx, &job); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{Requeue: true}, nil

	case spectrev1alpha1.ScrapeJobPhasePending:
		if job.Spec.JobDSL == "" {
			job.Status.Phase = spectrev1alpha1.ScrapeJobPhaseFailed
			job.Status.Error = "jobDSL must not be empty"
			now := metav1.Now()
			job.Status.CompletedAt = &now
			return ctrl.Result{}, r.Status().Update(ctx, &job)
		}
		if !isOutputSinkSupported(job.Spec.OutputSink) {
			job.Status.Phase = spectrev1alpha1.ScrapeJobPhaseFailed
			job.Status.Error = "outputSink " + job.Spec.OutputSink + " is not supported in v1alpha1; use \"stdout\" (see ADR-0019 §6)"
			now := metav1.Now()
			job.Status.CompletedAt = &now
			return ctrl.Result{}, r.Status().Update(ctx, &job)
		}
		now := metav1.Now()
		job.Status.Phase = spectrev1alpha1.ScrapeJobPhaseRunning
		job.Status.StartedAt = &now
		if err := r.Status().Update(ctx, &job); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{Requeue: true}, nil

	case spectrev1alpha1.ScrapeJobPhaseRunning:
		// TODO(PR15): replace runner.StubRunner (wired in main.go) with
		// runner.SubprocessRunner. The SubprocessRunner shells out to
		// the spectre engine binary, captures JSONL output, and tracks
		// row counts. See ADR-0019 §5 for the seam.
		runCtx, cancel := context.WithTimeout(ctx, jobTimeout(&job))
		defer cancel()

		rows, runErr := r.Runner.Run(runCtx, job.Spec.JobDSL, io.Discard)

		now := metav1.Now()
		if runErr != nil {
			job.Status.Phase = spectrev1alpha1.ScrapeJobPhaseFailed
			job.Status.Error = runErr.Error()
		} else {
			job.Status.Phase = spectrev1alpha1.ScrapeJobPhaseCompleted
			job.Status.RowsExtracted = rows
		}
		job.Status.CompletedAt = &now
		return ctrl.Result{}, r.Status().Update(ctx, &job)

	case spectrev1alpha1.ScrapeJobPhaseCompleted, spectrev1alpha1.ScrapeJobPhaseFailed:
		return ctrl.Result{}, nil
	}

	return ctrl.Result{}, nil
}

// jobTimeout resolves Spec.TimeoutSeconds into a duration, falling
// back to defaultJobTimeout when unset.
func jobTimeout(job *spectrev1alpha1.ScrapeJob) time.Duration {
	if job.Spec.TimeoutSeconds == nil {
		return defaultJobTimeout
	}
	return time.Duration(*job.Spec.TimeoutSeconds) * time.Second
}

// isOutputSinkSupported encodes ADR-0019 §6's runtime grammar: empty
// and "stdout" are accepted; everything else is rejected by the
// reconciler with an explicit error in Status.Error.
func isOutputSinkSupported(sink string) bool {
	return sink == "" || sink == "stdout"
}

// SetupWithManager sets up the controller with the Manager.
func (r *ScrapeJobReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&spectrev1alpha1.ScrapeJob{}).
		Named("scrapejob").
		Complete(r)
}
