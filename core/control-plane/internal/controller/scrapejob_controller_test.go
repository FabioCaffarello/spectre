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
	"io"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"

	spectrev1alpha1 "github.com/FabioCaffarello/spectre/core/control-plane/api/v1alpha1"
	"github.com/FabioCaffarello/spectre/core/control-plane/internal/runner"
)

// reconcilerFor builds a reconciler bound to the envtest client and
// the supplied JobRunner.
func reconcilerFor(r runner.JobRunner) *ScrapeJobReconciler {
	return &ScrapeJobReconciler{
		Client: k8sClient,
		Scheme: k8sClient.Scheme(),
		Runner: r,
	}
}

// stubRunnerForTests is a fast StubRunner used by every test that
// needs a successful Run.
func stubRunnerForTests() *runner.StubRunner {
	return &runner.StubRunner{SleepDuration: 5 * time.Millisecond}
}

// errorRunner returns a fixed error from Run; used to drive the
// Running → Failed transition without depending on context timeout.
type errorRunner struct{ err error }

func (e *errorRunner) Run(_ context.Context, _ string, _ io.Writer) (int64, error) {
	return 0, e.err
}

// createScrapeJob persists a ScrapeJob in the default namespace under
// a name unique to the test. t.Cleanup deletes it at the end.
func createScrapeJob(t *testing.T, spec spectrev1alpha1.ScrapeJobSpec) *spectrev1alpha1.ScrapeJob {
	t.Helper()
	name := strings.ToLower(strings.ReplaceAll(t.Name(), "_", "-"))
	job := &spectrev1alpha1.ScrapeJob{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: corev1.NamespaceDefault,
		},
		Spec: spec,
	}
	if err := k8sClient.Create(context.Background(), job); err != nil {
		t.Fatalf("create ScrapeJob: %v", err)
	}
	t.Cleanup(func() {
		_ = k8sClient.Delete(context.Background(), job)
	})
	return job
}

// reconcileOnce invokes Reconcile for the given object and returns
// the freshly fetched ScrapeJob. Fails the test on any error.
func reconcileOnce(t *testing.T, r *ScrapeJobReconciler, job *spectrev1alpha1.ScrapeJob) *spectrev1alpha1.ScrapeJob {
	t.Helper()
	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: job.Name, Namespace: job.Namespace}}
	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	var got spectrev1alpha1.ScrapeJob
	if err := k8sClient.Get(context.Background(), req.NamespacedName, &got); err != nil {
		t.Fatalf("Get after Reconcile: %v", err)
	}
	return &got
}

func validSpec() spectrev1alpha1.ScrapeJobSpec {
	return spectrev1alpha1.ScrapeJobSpec{
		JobDSL:     "spectre: v1alpha1\n",
		OutputSink: "stdout",
	}
}

func TestPendingTransition(t *testing.T) {
	job := createScrapeJob(t, validSpec())
	got := reconcileOnce(t, reconcilerFor(stubRunnerForTests()), job)

	if got.Status.Phase != spectrev1alpha1.ScrapeJobPhasePending {
		t.Fatalf("Phase = %q, want Pending", got.Status.Phase)
	}
	if got.Status.StartedAt != nil {
		t.Fatalf("StartedAt = %v, want nil", got.Status.StartedAt)
	}
}

func TestRunningTransition(t *testing.T) {
	job := createScrapeJob(t, validSpec())
	r := reconcilerFor(stubRunnerForTests())

	// First Reconcile: Pending.
	_ = reconcileOnce(t, r, job)
	// Second Reconcile: Running.
	got := reconcileOnce(t, r, job)

	if got.Status.Phase != spectrev1alpha1.ScrapeJobPhaseRunning {
		t.Fatalf("Phase = %q, want Running", got.Status.Phase)
	}
	if got.Status.StartedAt == nil {
		t.Fatalf("StartedAt = nil, want non-nil")
	}
	if got.Status.CompletedAt != nil {
		t.Fatalf("CompletedAt = %v, want nil", got.Status.CompletedAt)
	}
}

func TestCompletedTransition(t *testing.T) {
	job := createScrapeJob(t, validSpec())
	r := reconcilerFor(stubRunnerForTests())

	_ = reconcileOnce(t, r, job)    // Pending
	_ = reconcileOnce(t, r, job)    // Running
	got := reconcileOnce(t, r, job) // Running → Completed

	if got.Status.Phase != spectrev1alpha1.ScrapeJobPhaseCompleted {
		t.Fatalf("Phase = %q, want Completed", got.Status.Phase)
	}
	if got.Status.CompletedAt == nil {
		t.Fatalf("CompletedAt = nil, want non-nil")
	}
	if got.Status.RowsExtracted != 0 {
		t.Fatalf("RowsExtracted = %d, want 0 (StubRunner)", got.Status.RowsExtracted)
	}
	if got.Status.Error != "" {
		t.Fatalf("Error = %q, want empty", got.Status.Error)
	}
}

// The CRD enforces +kubebuilder:validation:MinLength=1 on jobDSL, so
// the apiserver rejects empty-DSL CRs before the reconciler can see
// them. The reconciler's empty-jobDSL guard is defense-in-depth and
// not testable through envtest; we exercise the analogous Failed
// transition by submitting an unsupported outputSink instead. The
// outputSink grammar is enforced by the reconciler at Pending →
// Running per ADR-0019 §6.
func TestFailedOnUnsupportedOutputSink(t *testing.T) {
	spec := validSpec()
	spec.OutputSink = "s3://bucket/path"
	job := createScrapeJob(t, spec)
	r := reconcilerFor(stubRunnerForTests())

	_ = reconcileOnce(t, r, job)    // empty → Pending
	got := reconcileOnce(t, r, job) // Pending → Failed (sink rejected)

	if got.Status.Phase != spectrev1alpha1.ScrapeJobPhaseFailed {
		t.Fatalf("Phase = %q, want Failed", got.Status.Phase)
	}
	if !strings.Contains(got.Status.Error, "outputSink") {
		t.Fatalf("Error = %q, want substring \"outputSink\"", got.Status.Error)
	}
	if got.Status.CompletedAt == nil {
		t.Fatalf("CompletedAt = nil, want non-nil for terminal phase")
	}
}

func TestFailedOnRunnerError(t *testing.T) {
	job := createScrapeJob(t, validSpec())
	r := reconcilerFor(&errorRunner{err: errors.New("simulated engine failure")})

	_ = reconcileOnce(t, r, job)    // → Pending
	_ = reconcileOnce(t, r, job)    // → Running
	got := reconcileOnce(t, r, job) // Running → Failed

	if got.Status.Phase != spectrev1alpha1.ScrapeJobPhaseFailed {
		t.Fatalf("Phase = %q, want Failed", got.Status.Phase)
	}
	if !strings.Contains(got.Status.Error, "simulated engine failure") {
		t.Fatalf("Error = %q, want substring \"simulated engine failure\"", got.Status.Error)
	}
}

func TestIdempotencyOnCompleted(t *testing.T) {
	job := createScrapeJob(t, validSpec())
	r := reconcilerFor(stubRunnerForTests())

	_ = reconcileOnce(t, r, job)
	_ = reconcileOnce(t, r, job)
	completed := reconcileOnce(t, r, job)
	if completed.Status.Phase != spectrev1alpha1.ScrapeJobPhaseCompleted {
		t.Fatalf("setup: Phase = %q, want Completed", completed.Status.Phase)
	}

	completedAt := completed.Status.CompletedAt
	again := reconcileOnce(t, r, job)
	if again.Status.Phase != spectrev1alpha1.ScrapeJobPhaseCompleted {
		t.Fatalf("Phase = %q after second reconcile, want Completed", again.Status.Phase)
	}
	if !again.Status.CompletedAt.Equal(completedAt) {
		t.Fatalf("CompletedAt changed from %v to %v on idempotent reconcile",
			completedAt, again.Status.CompletedAt)
	}
}
