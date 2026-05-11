// SPDX-License-Identifier: Apache-2.0

package logging

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"go.opentelemetry.io/otel/trace"
)

func TestNew_EmitsElevenMandatoryFieldsAtInfoLevel(t *testing.T) {
	var buf bytes.Buffer
	logger := New(&buf, "spectre-curl-impersonate", "test-version")

	logger.Info("hello", "job_id", "job-1", "request_id", "req-1")

	var obj map[string]any
	if err := json.Unmarshal(buf.Bytes(), &obj); err != nil {
		t.Fatalf("expected JSON output, got %q: %v", buf.String(), err)
	}

	// ADR-0031 §3.4 mandates 11 fields. tenant_id is always null
	// in v1alpha1; trace_id / span_id are null when no OTel
	// context is active (this case).
	for _, k := range []string{
		"timestamp",
		"level",
		"service",
		"service_version",
		"caller",
		"message",
		"trace_id",
		"span_id",
		"request_id",
		"job_id",
		"tenant_id",
	} {
		if _, ok := obj[k]; !ok {
			t.Errorf("missing field %q in %v", k, obj)
		}
	}
	if obj["level"] != "INFO" {
		t.Errorf("level = %v; want INFO", obj["level"])
	}
	if obj["service"] != "spectre-curl-impersonate" {
		t.Errorf("service = %v; want spectre-curl-impersonate", obj["service"])
	}
	if obj["service_version"] != "test-version" {
		t.Errorf("service_version = %v; want test-version", obj["service_version"])
	}
	if obj["message"] != "hello" {
		t.Errorf("message = %v; want hello", obj["message"])
	}
	if obj["job_id"] != "job-1" {
		t.Errorf("job_id = %v; want job-1", obj["job_id"])
	}
	if obj["request_id"] != "req-1" {
		t.Errorf("request_id = %v; want req-1", obj["request_id"])
	}
	if obj["tenant_id"] != nil {
		t.Errorf("tenant_id = %v; want null", obj["tenant_id"])
	}
	if obj["trace_id"] != nil {
		t.Errorf("trace_id = %v; want null (no active span)", obj["trace_id"])
	}
	if obj["span_id"] != nil {
		t.Errorf("span_id = %v; want null (no active span)", obj["span_id"])
	}
}

func TestNew_PopulatesTraceIdFromActiveSpanContext(t *testing.T) {
	var buf bytes.Buffer
	logger := New(&buf, "spectre-curl-impersonate", "test-version")

	traceID, _ := trace.TraceIDFromHex("0123456789abcdef0123456789abcdef")
	spanID, _ := trace.SpanIDFromHex("0123456789abcdef")
	sc := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    traceID,
		SpanID:     spanID,
		TraceFlags: trace.FlagsSampled,
		Remote:     true,
	})
	ctx := trace.ContextWithSpanContext(context.Background(), sc)

	logger.InfoContext(ctx, "inside span")

	var obj map[string]any
	if err := json.Unmarshal(buf.Bytes(), &obj); err != nil {
		t.Fatalf("expected JSON: %v", err)
	}
	if got := obj["trace_id"]; got != "0123456789abcdef0123456789abcdef" {
		t.Errorf("trace_id = %v; want 0123456789abcdef0123456789abcdef", got)
	}
	if got := obj["span_id"]; got != "0123456789abcdef" {
		t.Errorf("span_id = %v; want 0123456789abcdef", got)
	}
}

func TestFormatSource_StripsDirectoryPrefix(t *testing.T) {
	// Black-box: invoke through the logger so the slog.Record
	// gets a real Source, then assert the rendered caller is the
	// basename:line form ADR-0031 §3.4 prescribes.
	var buf bytes.Buffer
	logger := New(&buf, "svc", "v")
	logger.Info("checkpoint") // recorded on this file:<line>

	var obj map[string]any
	if err := json.Unmarshal(buf.Bytes(), &obj); err != nil {
		t.Fatalf("expected JSON: %v", err)
	}
	caller, _ := obj["caller"].(string)
	if caller == "" {
		t.Fatalf("caller missing")
	}
	// Verify no directory separator in the rendered string.
	for _, c := range caller {
		if c == '/' || c == '\\' {
			t.Errorf("caller %q contains a path separator; expected basename only", caller)
		}
	}
}
