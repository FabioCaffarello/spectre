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

package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ScrapeJobPhase is the lifecycle phase of a ScrapeJob. Transitions are
// strictly monotonic: Pending → Running → Completed | Failed. See
// ADR-0019 §4.
// +kubebuilder:validation:Enum=Pending;Running;Completed;Failed
type ScrapeJobPhase string

const (
	// ScrapeJobPhasePending is the initial phase assigned by the
	// reconciler on first observation. The DSL has not yet been
	// validated.
	ScrapeJobPhasePending ScrapeJobPhase = "Pending"

	// ScrapeJobPhaseRunning indicates the DSL has been validated and
	// the JobRunner has been invoked. PR14's StubRunner sleeps in
	// this phase; PR15's SubprocessRunner shells out to the engine.
	ScrapeJobPhaseRunning ScrapeJobPhase = "Running"

	// ScrapeJobPhaseCompleted is a terminal phase. RowsExtracted and
	// CompletedAt are set; no further reconciliation occurs.
	ScrapeJobPhaseCompleted ScrapeJobPhase = "Completed"

	// ScrapeJobPhaseFailed is a terminal phase. Error and CompletedAt
	// are set; no further reconciliation occurs.
	ScrapeJobPhaseFailed ScrapeJobPhase = "Failed"
)

// ScrapeJobSpec defines the desired state of ScrapeJob.
type ScrapeJobSpec struct {
	// jobDSL is the inline YAML body of the DSL job document the
	// engine will execute. Must be non-empty; the reconciler rejects
	// empty values with a Failed phase. The DSL grammar itself is
	// documented in ADR-0012.
	// +kubebuilder:validation:MinLength=1
	JobDSL string `json:"jobDSL"`

	// adapterImage is an optional override for the adapter container
	// image. Reserved for future per-job-Pod execution (ADR-0019 §3
	// "v1alpha2 escape hatch"); v1alpha1 ignores this field. When the
	// field is empty the operator uses whatever adapter binary it was
	// deployed with.
	// +optional
	AdapterImage string `json:"adapterImage,omitempty"`

	// outputSink declares where the engine's JSONL output is sent.
	// v1alpha1 accepts only "stdout" (the operator's stdout, visible
	// in `kubectl logs`); empty is treated as "stdout". Other syntaxes
	// (s3://, pvc://, webhook://, kafka://) are reserved for v1alpha2
	// — the schema accepts the string but the reconciler rejects them
	// at validation time. See ADR-0019 §6.
	// +optional
	OutputSink string `json:"outputSink,omitempty"`

	// resources is the resource requests/limits hint forwarded to the
	// execution context. v1alpha1's single-Pod execution model
	// (ADR-0019 §3) cannot enforce per-job limits at the OS level;
	// the field is recorded for future per-Pod-per-job execution.
	// +optional
	Resources corev1.ResourceRequirements `json:"resources,omitempty"`

	// timeoutSeconds bounds the entire job runtime. Defaults to 600
	// (10 minutes) when omitted. The reconciler propagates this as a
	// context deadline to the JobRunner.
	// +optional
	// +kubebuilder:validation:Minimum=1
	TimeoutSeconds *int32 `json:"timeoutSeconds,omitempty"`
}

// ScrapeJobStatus defines the observed state of ScrapeJob.
type ScrapeJobStatus struct {
	// phase is the current lifecycle phase. See ADR-0019 §4 for the
	// state-machine semantics: phase is the source of truth, not a
	// derived view of conditions.
	// +optional
	Phase ScrapeJobPhase `json:"phase,omitempty"`

	// startedAt is the timestamp at which the job transitioned to
	// Running.
	// +optional
	StartedAt *metav1.Time `json:"startedAt,omitempty"`

	// completedAt is the timestamp at which the job transitioned to a
	// terminal phase (Completed or Failed).
	// +optional
	CompletedAt *metav1.Time `json:"completedAt,omitempty"`

	// rowsExtracted is the total number of JSONL rows the engine
	// produced. PR14's StubRunner always reports 0; PR15+ reports the
	// real count.
	// +optional
	RowsExtracted int64 `json:"rowsExtracted,omitempty"`

	// error captures the failure reason when phase is Failed. Empty
	// otherwise.
	// +optional
	Error string `json:"error,omitempty"`

	// conditions carries standard Kubernetes condition entries for
	// diagnostic purposes. Per ADR-0019 §4 they complement (do not
	// replace) the phase as the authoritative lifecycle state.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Rows",type=integer,JSONPath=`.status.rowsExtracted`
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
