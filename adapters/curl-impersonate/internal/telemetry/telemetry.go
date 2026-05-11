// SPDX-License-Identifier: Apache-2.0

// Package telemetry initialises the curl-impersonate adapter's
// OpenTelemetry surface per ADR-0031 §3.5 (Go SDK choice) + §5.3
// (adapter metric taxonomy). Mirror of `operators/control-plane/
// internal/telemetry/` from W3.1 Cluster E with three deltas:
//
//   - `ServiceName` is `spectre-curl-impersonate`; `Kind` is the
//     normalised metric label value `curl_impersonate`.
//   - The metric set is ADR-0031 §5.3 (sessions_active,
//     initialize_duration_seconds, navigate_duration_seconds,
//     extract_duration_seconds, capability_violations_total) rather
//     than §5.2.
//   - The adapter registers `otelgrpc.NewServerHandler()` as a
//     gRPC server stats handler in cmd/adapter/main.go — the
//     server-side counterpart to the operator's client-side
//     `otelgrpc.NewClientHandler()`.
package telemetry

import (
	"context"
	"fmt"
	"os"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// ServiceName is the `service.name` OTel resource attribute shared
// across resource emissions and the JSON log `service` field
// (ADR-0031 §3.4 + §3.5).
const ServiceName = "spectre-curl-impersonate"

// TracerName is the global tracer slot the adapter's spans
// register against.
const TracerName = "spectre-curl-impersonate"

// Kind is the canonical `{kind}` label value the adapter stamps on
// every `spectre_adapter_*` metric per ADR-0031 §3.4
// (`lower_snake_case`).
const Kind = "curl_impersonate"

// Shutdown drains the OTel pipeline on process exit.
type Shutdown func(context.Context) error

// Resource attribute keys pinned directly to avoid the semconv
// pinning chase the operator's `internal/telemetry/telemetry.go`
// flagged in W3.1 Cluster E. ADR-0031 §3.5: the keys are stable
// across schema versions per the OTel governance.
const (
	attrServiceName      = "service.name"
	attrServiceVersion   = "service.version"
	attrServiceNamespace = "service.namespace"
)

var semconv = struct {
	ServiceName      func(string) attribute.KeyValue
	ServiceVersion   func(string) attribute.KeyValue
	ServiceNamespace func(string) attribute.KeyValue
}{
	ServiceName:      func(v string) attribute.KeyValue { return attribute.String(attrServiceName, v) },
	ServiceVersion:   func(v string) attribute.KeyValue { return attribute.String(attrServiceVersion, v) },
	ServiceNamespace: func(v string) attribute.KeyValue { return attribute.String(attrServiceNamespace, v) },
}

// Init registers the global W3C TraceContext propagator and a
// tracer provider. The OTLP exporter attaches only when
// `OTEL_EXPORTER_OTLP_ENDPOINT` is non-empty — mirror of the
// operator's optional-exporter pattern (ADR-0023 §6 alignment).
// Without an endpoint, spans still generate valid IDs and
// propagate downstream via `traceparent` injected on outgoing
// metadata; they are dropped at end-of-span instead of exported.
func Init(ctx context.Context, serviceVersion string) (Shutdown, error) {
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	res := resource.NewSchemaless(
		semconv.ServiceName(ServiceName),
		semconv.ServiceVersion(serviceVersion),
		semconv.ServiceNamespace("spectre"),
	)

	builder := sdktrace.NewTracerProvider(sdktrace.WithResource(res))
	endpoint := os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")
	if endpoint != "" {
		exporter, expErr := otlptracegrpc.New(
			ctx,
			otlptracegrpc.WithEndpoint(stripScheme(endpoint)),
			otlptracegrpc.WithInsecure(),
		)
		if expErr != nil {
			return nil, fmt.Errorf("telemetry: otlp exporter init: %w", expErr)
		}
		builder = sdktrace.NewTracerProvider(
			sdktrace.WithResource(res),
			sdktrace.WithBatcher(exporter),
		)
	}
	otel.SetTracerProvider(builder)

	return func(shutdownCtx context.Context) error {
		flushCtx, cancel := context.WithTimeout(shutdownCtx, 5*time.Second)
		defer cancel()
		return builder.Shutdown(flushCtx)
	}, nil
}

// stripScheme removes a leading `http://` or `https://` from `s`.
// `otlptracegrpc.WithEndpoint` expects `host:port`, not a URL —
// passing a URL is the most common configuration mistake.
func stripScheme(s string) string {
	for _, prefix := range []string{"http://", "https://"} {
		if len(s) > len(prefix) && s[:len(prefix)] == prefix {
			return s[len(prefix):]
		}
	}
	return s
}
