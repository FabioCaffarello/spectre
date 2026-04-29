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
	"io"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/pashagolub/pgxmock/v3"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"

	spectrev1alpha2 "github.com/FabioCaffarello/spectre/core/control-plane/api/v1alpha2"
	"github.com/FabioCaffarello/spectre/core/control-plane/internal/db"
	"github.com/FabioCaffarello/spectre/core/control-plane/internal/runner"
	enginev1alpha1 "github.com/FabioCaffarello/spectre/proto/gen/go/spectre/engine/v1alpha1"
)

const testDefaultEndpoint = "127.0.0.1:8090"

// reconcilerFor builds a reconciler bound to the envtest client, the
// supplied JobRunner, and the test default engine endpoint. The
// runner is delivered through a fixed factory regardless of the
// resolved endpoint — envtest does not dial real services.
//
// `DB` is left nil so the reconciler skips the R4.2 restart-recovery
// query; the recovery-path tests construct their reconciler via
// reconcilerForWithDB instead.
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

// reconcilerForWithDB is the recovery-path counterpart, binding a
// pgxmock pool so the syncFromPostgres branch of the reconciler can
// be exercised without a real Postgres.
func reconcilerForWithDB(r runner.JobRunner, pool db.Pool) *ScrapeJobReconciler {
	rec := reconcilerFor(r)
	rec.DB = pool
	return rec
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
	_ string,
	_ *enginev1alpha1.S3SinkConfig,
	_ *enginev1alpha1.WebhookSinkConfig,
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

// Output sink enforcement — every v1alpha2 OutputSink variant is
// behaviourally implemented post-R5.1: Stdout (R3.2 / R4.2), Kafka
// (R4.4), S3 + Webhook (R5.1). validateOutputSink is now an
// emptiness check + defence-in-depth on the per-variant fields the
// CRD's CEL rules already enforce at admission.

func TestValidateOutputSink_StdoutAccepted(t *testing.T) {
	if err := validateOutputSink(spectrev1alpha2.OutputSink{Stdout: &spectrev1alpha2.StdoutSink{}}); err != nil {
		t.Fatalf("validateOutputSink(Stdout) = %v, want nil", err)
	}
}

// TestValidateOutputSink_KafkaAccepted (R4.4): the engine ships
// an rdkafka producer and the reconciler forwards the topic via
// the engine's RunJobRequest.kafka_topic field. R3.2's Kafka
// rejection is gone; admission gating is engine-side per
// ADR-0023 §3 R4.4 addendum.
func TestValidateOutputSink_KafkaAccepted(t *testing.T) {
	err := validateOutputSink(spectrev1alpha2.OutputSink{
		Kafka: &spectrev1alpha2.KafkaSink{Brokers: []string{"localhost:9092"}, Topic: "spectre.rows.default"},
	})
	if err != nil {
		t.Fatalf("validateOutputSink(Kafka) = %v, want nil (R4.4 unblocks)", err)
	}
}

func TestValidateOutputSink_KafkaRejectsEmptyTopic(t *testing.T) {
	err := validateOutputSink(spectrev1alpha2.OutputSink{
		Kafka: &spectrev1alpha2.KafkaSink{Brokers: []string{"localhost:9092"}, Topic: ""},
	})
	if err == nil || !strings.Contains(err.Error(), "topic") {
		t.Fatalf("validateOutputSink(Kafka, empty topic) = %v, want topic-required error", err)
	}
}

// TestValidateOutputSink_S3Accepted (R5.1): the engine ships an
// `aws-sdk-s3` uploader and the reconciler forwards the per-job
// S3 config via the engine's RunJobRequest.s3 field. R3.2's S3
// rejection is gone; admission gating is engine-side per
// ADR-0024 §5.
func TestValidateOutputSink_S3Accepted(t *testing.T) {
	err := validateOutputSink(spectrev1alpha2.OutputSink{
		S3: &spectrev1alpha2.S3Sink{Bucket: "spectre-rows", Key: "scrapes/{{.JobID}}/rows.jsonl"},
	})
	if err != nil {
		t.Fatalf("validateOutputSink(S3) = %v, want nil (R5.1 unblocks)", err)
	}
}

func TestValidateOutputSink_S3RejectsEmptyBucket(t *testing.T) {
	err := validateOutputSink(spectrev1alpha2.OutputSink{
		S3: &spectrev1alpha2.S3Sink{Bucket: "", Key: "k"},
	})
	if err == nil || !strings.Contains(err.Error(), "bucket") {
		t.Fatalf("validateOutputSink(S3, empty bucket) = %v, want bucket-required error", err)
	}
}

func TestValidateOutputSink_S3RejectsEmptyKey(t *testing.T) {
	err := validateOutputSink(spectrev1alpha2.OutputSink{
		S3: &spectrev1alpha2.S3Sink{Bucket: "b", Key: ""},
	})
	if err == nil || !strings.Contains(err.Error(), "key") {
		t.Fatalf("validateOutputSink(S3, empty key) = %v, want key-required error", err)
	}
}

// TestValidateOutputSink_WebhookAccepted (R5.1): the engine ships
// a `reqwest`-based webhook client and the reconciler forwards the
// per-job webhook config via the engine's RunJobRequest.webhook
// field. R3.2's webhook rejection is gone; admission gating is
// per-job at the executor (ADR-0024 §5).
func TestValidateOutputSink_WebhookAccepted(t *testing.T) {
	err := validateOutputSink(spectrev1alpha2.OutputSink{
		Webhook: &spectrev1alpha2.WebhookSink{URL: "https://example.com/sink", Method: "POST"},
	})
	if err != nil {
		t.Fatalf("validateOutputSink(Webhook) = %v, want nil (R5.1 unblocks)", err)
	}
}

func TestValidateOutputSink_WebhookRejectsEmptyURL(t *testing.T) {
	err := validateOutputSink(spectrev1alpha2.OutputSink{
		Webhook: &spectrev1alpha2.WebhookSink{URL: "", Method: "POST"},
	})
	if err == nil || !strings.Contains(err.Error(), "url") {
		t.Fatalf("validateOutputSink(Webhook, empty url) = %v, want url-required error", err)
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
	port := int32(8090)
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
	const want = "spectre-engine.spectre-system.svc.cluster.local:8090"
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
	const want = "engine.my-ns.svc.cluster.local:8090"
	if got != want {
		t.Fatalf("resolveEngineEndpoint(Service, defaults) = %q, want %q", got, want)
	}
}

func TestResolveEngineEndpoint_EndpointForm(t *testing.T) {
	got, err := resolveEngineEndpoint(
		&spectrev1alpha2.EngineRef{Endpoint: "10.0.0.50:8090"},
		"default",
		testDefaultEndpoint,
	)
	if err != nil {
		t.Fatalf("resolveEngineEndpoint(Endpoint) error = %v", err)
	}
	if got != "10.0.0.50:8090" {
		t.Fatalf("resolveEngineEndpoint(Endpoint) = %q, want \"10.0.0.50:8090\"", got)
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
	spec.EngineRef = &spectrev1alpha2.EngineRef{Endpoint: "192.0.2.10:8090"}
	job := createScrapeJob(t, spec)
	r := reconcilerFor(stubRunnerForTests())

	_ = reconcileOnce(t, r, job)    // Pending
	got := reconcileOnce(t, r, job) // Running

	if got.Status.Phase != spectrev1alpha2.ScrapeJobPhaseRunning {
		t.Fatalf("Phase = %q, want Running", got.Status.Phase)
	}
	if got.Status.ResolvedEngineEndpoint != "192.0.2.10:8090" {
		t.Fatalf("ResolvedEngineEndpoint = %q, want 192.0.2.10:8090",
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

// Note: TestFailedOnUnsupportedSink was deleted in R5.1. After
// R5.1 wires S3 + Webhook end-to-end, every v1alpha2 OutputSink
// variant is behaviourally implemented; the test's input set
// (a schema variant the reconciler rejects) has gone to zero.
// Preserving it would require fabricating an invalid sink (a
// fifth variant), which is itself a schema violation. CHANGELOG
// + ADR-0024 §1 record the deletion. The defence-in-depth
// emptiness checks (S3/Webhook RejectsEmpty*) above continue to
// cover the remaining negative-path surface.

// TestRunningTransition_KafkaSinkAccepted (R4.4): a ScrapeJob with
// the Kafka sink reaches Running phase (was Failed pre-R4.4 with
// "kafka output sink not yet implemented"). The runner is stubbed
// because envtest does not dial a real engine; the test exercises
// admission acceptance, not engine kafka publishing — that lives
// in `core/engine/tests/kafka_integration.rs` and the conformance
// `test_kafka_sink.py`.
func TestRunningTransition_KafkaSinkAccepted(t *testing.T) {
	spec := validSpec()
	spec.OutputSink = spectrev1alpha2.OutputSink{
		Kafka: &spectrev1alpha2.KafkaSink{
			Brokers: []string{"kafka:9092"},
			Topic:   "spectre.rows.default",
		},
	}
	job := createScrapeJob(t, spec)
	r := reconcilerFor(stubRunnerForTests())

	_ = reconcileOnce(t, r, job)    // → Pending
	got := reconcileOnce(t, r, job) // Pending → Running (R4.4 unblocks)

	if got.Status.Phase != spectrev1alpha2.ScrapeJobPhaseRunning {
		t.Fatalf("Phase = %q, want Running (R4.4 unblocks Kafka admission)", got.Status.Phase)
	}
}

// TestRunningTransition_S3SinkAccepted (R5.1): a ScrapeJob with
// the S3 sink reaches Running phase (was Failed pre-R5.1 with
// "s3 output sink not yet implemented"). The runner is stubbed
// because envtest does not dial a real engine; the test exercises
// admission acceptance, not engine S3 uploading — that lives in
// `core/engine/tests/s3_integration.rs` and the conformance
// `test_s3_sink.py`.
func TestRunningTransition_S3SinkAccepted(t *testing.T) {
	spec := validSpec()
	spec.OutputSink = spectrev1alpha2.OutputSink{
		S3: &spectrev1alpha2.S3Sink{
			Bucket:   "spectre-rows",
			Key:      "scrapes/{{.JobID}}/rows.jsonl",
			Endpoint: "http://minio:9000",
			Region:   "us-east-1",
		},
	}
	job := createScrapeJob(t, spec)
	r := reconcilerFor(stubRunnerForTests())

	_ = reconcileOnce(t, r, job)    // → Pending
	got := reconcileOnce(t, r, job) // Pending → Running (R5.1 unblocks)

	if got.Status.Phase != spectrev1alpha2.ScrapeJobPhaseRunning {
		t.Fatalf("Phase = %q, want Running (R5.1 unblocks S3 admission)", got.Status.Phase)
	}
}

// TestRunningTransition_WebhookSinkAccepted (R5.1): a ScrapeJob
// with the Webhook sink reaches Running phase (was Failed pre-R5.1
// with "webhook output sink not yet implemented"). Same stub-runner
// pattern as the Kafka and S3 cases. Engine-side webhook delivery
// is exercised in `core/engine/tests/webhook_integration.rs` and
// the conformance `test_webhook_sink.py`.
func TestRunningTransition_WebhookSinkAccepted(t *testing.T) {
	spec := validSpec()
	spec.OutputSink = spectrev1alpha2.OutputSink{
		Webhook: &spectrev1alpha2.WebhookSink{
			URL:       "https://example.com/spectre",
			Method:    "POST",
			BatchSize: 0,
		},
	}
	job := createScrapeJob(t, spec)
	r := reconcilerFor(stubRunnerForTests())

	_ = reconcileOnce(t, r, job)    // → Pending
	got := reconcileOnce(t, r, job) // Pending → Running (R5.1 unblocks)

	if got.Status.Phase != spectrev1alpha2.ScrapeJobPhaseRunning {
		t.Fatalf("Phase = %q, want Running (R5.1 unblocks Webhook admission)", got.Status.Phase)
	}
}

// R4.2 restart-recovery — the reconciler queries Postgres before
// invoking the runner. A persisted terminal row syncs Status without
// re-running; ErrJobNotFound falls through to the runner; other
// errors mark the ScrapeJob Failed.

// expectGetJob installs a pgxmock expectation matching syncFromPostgres's
// SELECT against `jobs` for the given UID, returning either a
// hydrated row of the supplied status or pgx.ErrNoRows / generic err.
type recoveryRow struct {
	status string
	rows   *int64
	errMsg *string
}

func expectGetJob(mock pgxmock.PgxPoolIface, jobUUID uuid.UUID, row *recoveryRow, returnErr error) {
	q := mock.ExpectQuery(`SELECT id, status, driver, output_sink_kind`).
		WithArgs(jobUUID)
	if returnErr != nil {
		q.WillReturnError(returnErr)
		return
	}
	now := time.Date(2026, 4, 28, 12, 0, 0, 0, time.UTC)
	q.WillReturnRows(pgxmock.NewRows([]string{
		"id", "status", "driver", "output_sink_kind",
		"created_at", "started_at", "completed_at",
		"rows_extracted", "error", "resolved_engine_endpoint",
	}).AddRow(
		jobUUID, row.status, "playwright", "stdout",
		now, &now, &now,
		row.rows, row.errMsg, (*string)(nil),
	))
}

// driveToRunning runs Reconcile twice (empty→Pending, Pending→Running)
// and returns the freshly-fetched ScrapeJob ready for the recovery
// path test.
func driveToRunning(t *testing.T, r *ScrapeJobReconciler, job *spectrev1alpha2.ScrapeJob) *spectrev1alpha2.ScrapeJob {
	t.Helper()
	_ = reconcileOnce(t, r, job)    // empty → Pending
	got := reconcileOnce(t, r, job) // Pending → Running
	if got.Status.Phase != spectrev1alpha2.ScrapeJobPhaseRunning {
		t.Fatalf("setup: Phase = %q, want Running", got.Status.Phase)
	}
	return got
}

func TestReconciler_RestartRecovery_CompletedSyncsStatus(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatalf("pgxmock.NewPool: %v", err)
	}
	defer mock.Close()

	job := createScrapeJob(t, validSpec())
	r := reconcilerForWithDB(stubRunnerForTests(), mock)
	got := driveToRunning(t, r, job)

	jobUUID := uuid.MustParse(string(got.UID))
	rows := int64(123)
	expectGetJob(mock, jobUUID, &recoveryRow{status: "completed", rows: &rows}, nil)

	got = reconcileOnce(t, r, job) // Running → Completed (synced from Postgres)

	if got.Status.Phase != spectrev1alpha2.ScrapeJobPhaseCompleted {
		t.Fatalf("Phase = %q, want Completed (synced from Postgres)", got.Status.Phase)
	}
	if got.Status.RowsExtracted != 123 {
		t.Fatalf("RowsExtracted = %d, want 123", got.Status.RowsExtracted)
	}
	if got.Status.CompletedAt == nil {
		t.Fatal("CompletedAt = nil, want non-nil")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestReconciler_RestartRecovery_FailedSyncsStatus(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatalf("pgxmock.NewPool: %v", err)
	}
	defer mock.Close()

	job := createScrapeJob(t, validSpec())
	r := reconcilerForWithDB(stubRunnerForTests(), mock)
	got := driveToRunning(t, r, job)

	jobUUID := uuid.MustParse(string(got.UID))
	errMsg := "engine failed: adapter dial: connection refused"
	expectGetJob(mock, jobUUID, &recoveryRow{status: "failed", errMsg: &errMsg}, nil)

	got = reconcileOnce(t, r, job) // Running → Failed (synced from Postgres)

	if got.Status.Phase != spectrev1alpha2.ScrapeJobPhaseFailed {
		t.Fatalf("Phase = %q, want Failed (synced from Postgres)", got.Status.Phase)
	}
	if !strings.Contains(got.Status.Error, "adapter dial") {
		t.Fatalf("Error = %q, want adapter-dial substring", got.Status.Error)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestReconciler_RestartRecovery_NotFoundProceedsToRunner(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatalf("pgxmock.NewPool: %v", err)
	}
	defer mock.Close()

	job := createScrapeJob(t, validSpec())
	r := reconcilerForWithDB(stubRunnerForTests(), mock)
	got := driveToRunning(t, r, job)

	jobUUID := uuid.MustParse(string(got.UID))
	expectGetJob(mock, jobUUID, nil, pgx.ErrNoRows)

	got = reconcileOnce(t, r, job) // Running → Completed (via StubRunner)

	if got.Status.Phase != spectrev1alpha2.ScrapeJobPhaseCompleted {
		t.Fatalf("Phase = %q, want Completed (StubRunner ran)", got.Status.Phase)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestReconciler_PostgresUnavailable_MarksFailed(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatalf("pgxmock.NewPool: %v", err)
	}
	defer mock.Close()

	job := createScrapeJob(t, validSpec())
	r := reconcilerForWithDB(stubRunnerForTests(), mock)
	got := driveToRunning(t, r, job)

	jobUUID := uuid.MustParse(string(got.UID))
	expectGetJob(mock, jobUUID, nil, fmt.Errorf("postgres connection lost"))

	got = reconcileOnce(t, r, job) // Running → Failed (Postgres error)

	if got.Status.Phase != spectrev1alpha2.ScrapeJobPhaseFailed {
		t.Fatalf("Phase = %q, want Failed", got.Status.Phase)
	}
	if !strings.Contains(got.Status.Error, "restart recovery failed") {
		t.Fatalf("Error = %q, want restart-recovery substring", got.Status.Error)
	}
	if !strings.Contains(got.Status.Error, "postgres connection lost") {
		t.Fatalf("Error = %q, want underlying error substring", got.Status.Error)
	}
}
