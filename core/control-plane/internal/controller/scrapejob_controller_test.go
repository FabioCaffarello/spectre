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

	"github.com/google/uuid"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"

	spectrev1alpha2 "github.com/FabioCaffarello/spectre/core/control-plane/api/v1alpha2"
	"github.com/FabioCaffarello/spectre/core/control-plane/internal/runner"
)

const testDefaultEndpoint = "127.0.0.1:9090"

// reconcilerFor builds a reconciler bound to the envtest client, the
// supplied JobRunner, and the test default engine endpoint. The
// runner is delivered through a fixed factory regardless of the
// resolved endpoint — envtest does not dial real services.
func reconcilerFor(r runner.JobRunner) *ScrapeJobReconciler {
	return &ScrapeJobReconciler{
		Client:                k8sClient,
		Scheme:                k8sClient.Scheme(),
		DefaultEngineEndpoint: testDefaultEndpoint,
		RunnerFactory: func(string) runner.JobRunner {
			return r
		},
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

func (e *errorRunner) Run(
	_ context.Context,
	_ uuid.UUID,
	_ string,
	_ string,
	_ io.Writer,
) (int64, error) {
	return 0, e.err
}

// createScrapeJob persists a ScrapeJob in the default namespace
// under a name unique to the test. t.Cleanup deletes it at the end.
func createScrapeJob(t *testing.T, spec spectrev1alpha2.ScrapeJobSpec) *spectrev1alpha2.ScrapeJob {
	t.Helper()
	name := strings.ToLower(strings.ReplaceAll(t.Name(), "_", "-"))
	job := &spectrev1alpha2.ScrapeJob{
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
func reconcileOnce(t *testing.T, r *ScrapeJobReconciler, job *spectrev1alpha2.ScrapeJob) *spectrev1alpha2.ScrapeJob {
	t.Helper()
	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: job.Name, Namespace: job.Namespace}}
	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	var got spectrev1alpha2.ScrapeJob
	if err := k8sClient.Get(context.Background(), req.NamespacedName, &got); err != nil {
		t.Fatalf("Get after Reconcile: %v", err)
	}
	return &got
}

// validSpec returns a minimum spec that the reconciler accepts:
// non-empty DSL, OutputSink.Stdout selected, EngineRef nil so the
// reconciler falls back to DefaultEngineEndpoint.
func validSpec() spectrev1alpha2.ScrapeJobSpec {
	return spectrev1alpha2.ScrapeJobSpec{
		JobDSL:     "spectre: v1alpha1\n",
		OutputSink: spectrev1alpha2.OutputSink{Stdout: &spectrev1alpha2.StdoutSink{}},
	}
}

func TestPendingTransition(t *testing.T) {
	job := createScrapeJob(t, validSpec())
	got := reconcileOnce(t, reconcilerFor(stubRunnerForTests()), job)

	if got.Status.Phase != spectrev1alpha2.ScrapeJobPhasePending {
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

	if got.Status.Phase != spectrev1alpha2.ScrapeJobPhaseRunning {
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

	if got.Status.Phase != spectrev1alpha2.ScrapeJobPhaseCompleted {
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

func TestFailedOnRunnerError(t *testing.T) {
	job := createScrapeJob(t, validSpec())
	r := reconcilerFor(&errorRunner{err: errors.New("simulated engine failure")})

	_ = reconcileOnce(t, r, job)    // → Pending
	_ = reconcileOnce(t, r, job)    // → Running
	got := reconcileOnce(t, r, job) // Running → Failed

	if got.Status.Phase != spectrev1alpha2.ScrapeJobPhaseFailed {
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
	if completed.Status.Phase != spectrev1alpha2.ScrapeJobPhaseCompleted {
		t.Fatalf("setup: Phase = %q, want Completed", completed.Status.Phase)
	}

	completedAt := completed.Status.CompletedAt
	again := reconcileOnce(t, r, job)
	if again.Status.Phase != spectrev1alpha2.ScrapeJobPhaseCompleted {
		t.Fatalf("Phase = %q after second reconcile, want Completed", again.Status.Phase)
	}
	if !again.Status.CompletedAt.Equal(completedAt) {
		t.Fatalf("CompletedAt changed from %v to %v on idempotent reconcile",
			completedAt, again.Status.CompletedAt)
	}
}

// Output sink enforcement — Kafka/S3/Webhook variants are
// schema-only in v1alpha2 and the reconciler rejects them at
// Pending → Running. Stdout is the accepted variant.

func TestValidateOutputSink_StdoutAccepted(t *testing.T) {
	if err := validateOutputSink(spectrev1alpha2.OutputSink{Stdout: &spectrev1alpha2.StdoutSink{}}); err != nil {
		t.Fatalf("validateOutputSink(Stdout) = %v, want nil", err)
	}
}

func TestValidateOutputSink_KafkaRejected(t *testing.T) {
	err := validateOutputSink(spectrev1alpha2.OutputSink{
		Kafka: &spectrev1alpha2.KafkaSink{Brokers: []string{"localhost:9092"}, Topic: "rows"},
	})
	if err == nil || !strings.Contains(err.Error(), "kafka") {
		t.Fatalf("validateOutputSink(Kafka) = %v, want kafka-rejection error", err)
	}
	if !strings.Contains(err.Error(), "R4.4") {
		t.Fatalf("validateOutputSink(Kafka) error %q should reference R4.4", err)
	}
}

func TestValidateOutputSink_S3Rejected(t *testing.T) {
	err := validateOutputSink(spectrev1alpha2.OutputSink{
		S3: &spectrev1alpha2.S3Sink{Bucket: "b", Key: "k"},
	})
	if err == nil || !strings.Contains(err.Error(), "s3") {
		t.Fatalf("validateOutputSink(S3) = %v, want s3-rejection error", err)
	}
	if !strings.Contains(err.Error(), "R5.1") {
		t.Fatalf("validateOutputSink(S3) error %q should reference R5.1", err)
	}
}

func TestValidateOutputSink_WebhookRejected(t *testing.T) {
	err := validateOutputSink(spectrev1alpha2.OutputSink{
		Webhook: &spectrev1alpha2.WebhookSink{URL: "https://example.com/sink"},
	})
	if err == nil || !strings.Contains(err.Error(), "webhook") {
		t.Fatalf("validateOutputSink(Webhook) = %v, want webhook-rejection error", err)
	}
	if !strings.Contains(err.Error(), "R5.1") {
		t.Fatalf("validateOutputSink(Webhook) error %q should reference R5.1", err)
	}
}

func TestValidateOutputSink_NoneSet(t *testing.T) {
	err := validateOutputSink(spectrev1alpha2.OutputSink{})
	if err == nil {
		t.Fatalf("validateOutputSink(empty) = nil, want error (CEL-rule defence-in-depth)")
	}
}

// EngineRef resolution — the three valid forms (Service, Endpoint,
// nil-fallback) and the malformed-EngineRef defence-in-depth.

func TestResolveEngineEndpoint_ServiceForm(t *testing.T) {
	port := int32(9090)
	got, err := resolveEngineEndpoint(
		&spectrev1alpha2.EngineRef{Service: &spectrev1alpha2.EngineServiceRef{
			Name: "spectre-engine", Namespace: "spectre-system", Port: &port,
		}},
		"default",
		testDefaultEndpoint,
	)
	if err != nil {
		t.Fatalf("resolveEngineEndpoint(Service) error = %v", err)
	}
	const want = "spectre-engine.spectre-system.svc.cluster.local:9090"
	if got != want {
		t.Fatalf("resolveEngineEndpoint(Service) = %q, want %q", got, want)
	}
}

func TestResolveEngineEndpoint_ServiceForm_DefaultsNamespaceAndPort(t *testing.T) {
	got, err := resolveEngineEndpoint(
		&spectrev1alpha2.EngineRef{Service: &spectrev1alpha2.EngineServiceRef{Name: "engine"}},
		"my-ns",
		testDefaultEndpoint,
	)
	if err != nil {
		t.Fatalf("resolveEngineEndpoint(Service, defaults) error = %v", err)
	}
	const want = "engine.my-ns.svc.cluster.local:9090"
	if got != want {
		t.Fatalf("resolveEngineEndpoint(Service, defaults) = %q, want %q", got, want)
	}
}

func TestResolveEngineEndpoint_EndpointForm(t *testing.T) {
	got, err := resolveEngineEndpoint(
		&spectrev1alpha2.EngineRef{Endpoint: "10.0.0.50:9090"},
		"default",
		testDefaultEndpoint,
	)
	if err != nil {
		t.Fatalf("resolveEngineEndpoint(Endpoint) error = %v", err)
	}
	if got != "10.0.0.50:9090" {
		t.Fatalf("resolveEngineEndpoint(Endpoint) = %q, want \"10.0.0.50:9090\"", got)
	}
}

func TestResolveEngineEndpoint_NilFallsBackToDefault(t *testing.T) {
	got, err := resolveEngineEndpoint(nil, "default", testDefaultEndpoint)
	if err != nil {
		t.Fatalf("resolveEngineEndpoint(nil) error = %v", err)
	}
	if got != testDefaultEndpoint {
		t.Fatalf("resolveEngineEndpoint(nil) = %q, want default %q", got, testDefaultEndpoint)
	}
}

func TestResolveEngineEndpoint_NilWithoutDefault(t *testing.T) {
	_, err := resolveEngineEndpoint(nil, "default", "")
	if err == nil {
		t.Fatalf("resolveEngineEndpoint(nil, no default) = nil error, want failure")
	}
}

// TestStatusResolvedEngineEndpointPopulated drives a ScrapeJob
// through Pending → Running and asserts that
// Status.ResolvedEngineEndpoint reflects the EngineRef the
// reconciler actually resolved. The test uses the Endpoint form so
// no Service-FQDN dependency is involved.
func TestStatusResolvedEngineEndpointPopulated(t *testing.T) {
	spec := validSpec()
	spec.EngineRef = &spectrev1alpha2.EngineRef{Endpoint: "192.0.2.10:9090"}
	job := createScrapeJob(t, spec)
	r := reconcilerFor(stubRunnerForTests())

	_ = reconcileOnce(t, r, job)    // Pending
	got := reconcileOnce(t, r, job) // Running

	if got.Status.Phase != spectrev1alpha2.ScrapeJobPhaseRunning {
		t.Fatalf("Phase = %q, want Running", got.Status.Phase)
	}
	if got.Status.ResolvedEngineEndpoint != "192.0.2.10:9090" {
		t.Fatalf("ResolvedEngineEndpoint = %q, want 192.0.2.10:9090",
			got.Status.ResolvedEngineEndpoint)
	}
}

// TestStatusResolvedEngineEndpoint_DefaultFallback asserts that a
// ScrapeJob without EngineRef set surfaces the operator's default
// endpoint in the status — making fallback visible to operators
// inspecting `kubectl get scrapejob -o yaml`.
func TestStatusResolvedEngineEndpoint_DefaultFallback(t *testing.T) {
	job := createScrapeJob(t, validSpec()) // EngineRef nil
	r := reconcilerFor(stubRunnerForTests())

	_ = reconcileOnce(t, r, job)    // Pending
	got := reconcileOnce(t, r, job) // Running

	if got.Status.ResolvedEngineEndpoint != testDefaultEndpoint {
		t.Fatalf("ResolvedEngineEndpoint = %q, want default %q",
			got.Status.ResolvedEngineEndpoint, testDefaultEndpoint)
	}
}

// TestFailedOnUnsupportedSink covers the full reconciler path for a
// schema-only sink (Kafka here): the spec passes apiserver
// admission (CEL allows exactly-one-of), but the reconciler rejects
// it at Pending → Running with a Failed terminal phase. End-to-end
// coverage of validateOutputSink integrated with the reconciler.
func TestFailedOnUnsupportedSink(t *testing.T) {
	spec := validSpec()
	spec.OutputSink = spectrev1alpha2.OutputSink{
		Kafka: &spectrev1alpha2.KafkaSink{Brokers: []string{"kafka:9092"}, Topic: "rows"},
	}
	job := createScrapeJob(t, spec)
	r := reconcilerFor(stubRunnerForTests())

	_ = reconcileOnce(t, r, job)    // empty → Pending
	got := reconcileOnce(t, r, job) // Pending → Failed (sink rejected)

	if got.Status.Phase != spectrev1alpha2.ScrapeJobPhaseFailed {
		t.Fatalf("Phase = %q, want Failed", got.Status.Phase)
	}
	if !strings.Contains(got.Status.Error, "kafka") || !strings.Contains(got.Status.Error, "R4.4") {
		t.Fatalf("Error = %q, want kafka/R4.4 substring", got.Status.Error)
	}
	if got.Status.CompletedAt == nil {
		t.Fatalf("CompletedAt = nil, want non-nil for terminal phase")
	}
}
