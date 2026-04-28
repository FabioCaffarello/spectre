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

package v1alpha2

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ScrapeJobPhase is the lifecycle phase of a ScrapeJob. Transitions
// are strictly monotonic: Pending → Running → Completed | Failed.
// The state-machine semantics are unchanged from v1alpha1; see
// ADR-0019 §4.
// +kubebuilder:validation:Enum=Pending;Running;Completed;Failed
type ScrapeJobPhase string

const (
	// ScrapeJobPhasePending is the initial phase assigned by the
	// reconciler on first observation. Spec validation has not run.
	ScrapeJobPhasePending ScrapeJobPhase = "Pending"

	// ScrapeJobPhaseRunning indicates the spec has been validated and
	// the engine has been dialed.
	ScrapeJobPhaseRunning ScrapeJobPhase = "Running"

	// ScrapeJobPhaseCompleted is a terminal phase. RowsExtracted and
	// CompletedAt are set.
	ScrapeJobPhaseCompleted ScrapeJobPhase = "Completed"

	// ScrapeJobPhaseFailed is a terminal phase. Error and CompletedAt
	// are set.
	ScrapeJobPhaseFailed ScrapeJobPhase = "Failed"
)

// EngineRef identifies an engine service via a Kubernetes Service
// reference or a direct host:port endpoint. Exactly one of Service
// or Endpoint must be set; the apiserver enforces this via CEL.
//
// +kubebuilder:validation:XValidation:rule="(has(self.service) ? 1 : 0) + (has(self.endpoint) ? 1 : 0) == 1",message="exactly one of service, endpoint must be set"
type EngineRef struct {
	// Service references a Kubernetes Service in the specified
	// Namespace. The operator resolves to
	// `<service>.<namespace>.svc.cluster.local:<port>`. Mutually
	// exclusive with Endpoint.
	// +optional
	Service *EngineServiceRef `json:"service,omitempty"`

	// Endpoint is a direct host:port string. Useful for testing or
	// for engines outside the cluster. Mutually exclusive with
	// Service.
	// +optional
	Endpoint string `json:"endpoint,omitempty"`
}

// EngineServiceRef is a Kubernetes Service reference for an engine
// endpoint.
type EngineServiceRef struct {
	// Name of the Kubernetes Service.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`

	// Namespace of the Kubernetes Service. Defaults to the
	// ScrapeJob's own namespace if empty.
	// +optional
	Namespace string `json:"namespace,omitempty"`

	// Port number of the Service. Defaults to 9090 (the engine's
	// canonical port from ADR-0021).
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=65535
	// +kubebuilder:default=9090
	// +optional
	Port *int32 `json:"port,omitempty"`
}

// OutputSink describes where the engine's JSONL row events land.
// Exactly one variant must be set; the apiserver enforces this via
// CEL. R3.2 implements only Stdout — the Kafka, S3, and Webhook
// variants exist as schema fields for v1alpha2 forward-compatibility
// (R4.4 wires Kafka; R5.1 wires S3 and Webhook), but the reconciler
// rejects them at admission with explicit "not yet implemented"
// errors until those phases land.
//
// +kubebuilder:validation:XValidation:rule="(has(self.stdout) ? 1 : 0) + (has(self.kafka) ? 1 : 0) + (has(self.s3) ? 1 : 0) + (has(self.webhook) ? 1 : 0) == 1",message="exactly one of stdout, kafka, s3, webhook must be set"
type OutputSink struct {
	// Stdout writes JSONL rows to the operator's stdout, surfaced
	// via `kubectl logs <operator-pod>`.
	// +optional
	Stdout *StdoutSink `json:"stdout,omitempty"`

	// Kafka publishes JSONL rows to a Kafka topic. R4.4 wires the
	// Kafka producer; R3.2 admission rejects with "kafka output
	// sink not yet implemented".
	// +optional
	Kafka *KafkaSink `json:"kafka,omitempty"`

	// S3 uploads JSONL output to an S3-compatible bucket. R5.1
	// wires the S3 client; R3.2 admission rejects with "s3 output
	// sink not yet implemented".
	// +optional
	S3 *S3Sink `json:"s3,omitempty"`

	// Webhook POSTs JSONL rows to an HTTP endpoint. R5.1 wires the
	// webhook client; R3.2 admission rejects with "webhook output
	// sink not yet implemented".
	// +optional
	Webhook *WebhookSink `json:"webhook,omitempty"`
}

// StdoutSink is the marker variant — its presence selects stdout
// output. v1alpha2 carries no fields; future revisions may add
// formatting hints.
type StdoutSink struct{}

// KafkaSink describes a Kafka topic destination. Schema-only in
// v1alpha2; R4.4 implements the producer.
type KafkaSink struct {
	// Brokers is the list of Kafka broker host:port addresses.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinItems=1
	Brokers []string `json:"brokers"`

	// Topic is the Kafka topic the engine publishes to.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Topic string `json:"topic"`
}

// S3Sink describes an S3-compatible bucket destination. Schema-only
// in v1alpha2; R5.1 implements the client.
type S3Sink struct {
	// Bucket is the S3 bucket name.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Bucket string `json:"bucket"`

	// Key is the object key within the bucket. Supports
	// `{{.JobID}}` template substitution at upload time.
	// +kubebuilder:validation:Required
	Key string `json:"key"`

	// Endpoint overrides the default S3 endpoint (for MinIO, R2,
	// etc).
	// +optional
	Endpoint string `json:"endpoint,omitempty"`

	// Region is the AWS region. Defaults to us-east-1.
	// +kubebuilder:default="us-east-1"
	// +optional
	Region string `json:"region,omitempty"`
}

// WebhookSink describes an HTTP webhook destination. Schema-only in
// v1alpha2; R5.1 implements the client.
type WebhookSink struct {
	// URL is the HTTP endpoint that receives JSONL rows.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Pattern=`^https?://.+$`
	URL string `json:"url"`

	// Method is the HTTP method.
	// +kubebuilder:validation:Enum=POST;PUT
	// +kubebuilder:default=POST
	// +optional
	Method string `json:"method,omitempty"`

	// BatchSize is the number of rows per HTTP request. Zero means
	// one row per request.
	// +kubebuilder:default=0
	// +kubebuilder:validation:Minimum=0
	// +optional
	BatchSize int32 `json:"batchSize,omitempty"`
}

// ScrapeJobSpec defines the desired state of ScrapeJob.
type ScrapeJobSpec struct {
	// jobDSL is the inline YAML document describing the scrape job.
	// The engine parses, plans, resolves the requested driver, and
	// runs the plan. The DSL grammar is documented in ADR-0012.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	JobDSL string `json:"jobDSL"`

	// engineRef identifies the engine service the operator dials
	// for this job. Exactly one of Service or Endpoint must be set
	// (CEL-validated). When unset, the operator falls back to its
	// startup-time `--engine-endpoint` / `SPECTRE_ENGINE_ENDPOINT`
	// configuration.
	// +optional
	EngineRef *EngineRef `json:"engineRef,omitempty"`

	// outputSink describes where the engine's JSONL row events
	// land. Exactly one of Stdout, Kafka, S3, or Webhook must be
	// set (CEL-validated). R3.2 implements only Stdout; the others
	// fail at admission with "not yet implemented" until R4.4 /
	// R5.1 wire them.
	// +kubebuilder:validation:Required
	OutputSink OutputSink `json:"outputSink"`

	// timeoutSeconds bounds the entire job runtime. Defaults to
	// 600 (10 minutes). The reconciler propagates this as a
	// context deadline to the engine client.
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:default=600
	// +optional
	TimeoutSeconds *int32 `json:"timeoutSeconds,omitempty"`

	// resources is a hint forwarded to the engine. The operator
	// does not enforce limits in v1alpha2 (the engine runs in its
	// own Pod with its own resources); the field exists for
	// forward-compatibility with per-job Pods (a future runtime
	// model evolution).
	// +optional
	Resources corev1.ResourceRequirements `json:"resources,omitempty"`
}

// ScrapeJobStatus defines the observed state of ScrapeJob.
type ScrapeJobStatus struct {
	// phase is the current lifecycle phase. The state machine
	// (Pending → Running → Completed | Failed) is unchanged from
	// v1alpha1; see ADR-0019 §4.
	// +optional
	Phase ScrapeJobPhase `json:"phase,omitempty"`

	// startedAt is the timestamp at which the job transitioned to
	// Running.
	// +optional
	StartedAt *metav1.Time `json:"startedAt,omitempty"`

	// completedAt is the timestamp at which the job transitioned
	// to a terminal phase (Completed or Failed).
	// +optional
	CompletedAt *metav1.Time `json:"completedAt,omitempty"`

	// rowsExtracted is the total number of JSONL rows the engine
	// produced.
	// +optional
	RowsExtracted int64 `json:"rowsExtracted,omitempty"`

	// error captures the failure reason when phase is Failed.
	// Structured failure information lives in Conditions.
	// +optional
	Error string `json:"error,omitempty"`

	// resolvedEngineEndpoint records the host:port the operator
	// actually dialed for this job. Useful for debugging EngineRef
	// resolution issues — if the spec used a Service form but the
	// status shows the default endpoint, resolution fell back.
	// +optional
	ResolvedEngineEndpoint string `json:"resolvedEngineEndpoint,omitempty"`

	// conditions carries standard Kubernetes condition entries for
	// diagnostic purposes. Per ADR-0019 §4 they complement (do not
	// replace) the phase as the authoritative lifecycle state.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:storageversion
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Rows",type=integer,JSONPath=`.status.rowsExtracted`
// +kubebuilder:printcolumn:name="Engine",type=string,JSONPath=`.status.resolvedEngineEndpoint`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// ScrapeJob is the Schema for the scrapejobs API.
type ScrapeJob struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of ScrapeJob
	// +required
	Spec ScrapeJobSpec `json:"spec"`

	// status defines the observed state of ScrapeJob
	// +optional
	Status ScrapeJobStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// ScrapeJobList contains a list of ScrapeJob.
type ScrapeJobList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []ScrapeJob `json:"items"`
}

func init() {
	SchemeBuilder.Register(&ScrapeJob{}, &ScrapeJobList{})
}
