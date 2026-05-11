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

// Package telemetry initialises the operator's OpenTelemetry tracer
// provider + W3C TraceContext propagator per ADR-0031.
//
// The operator's metrics surface (ADR-0031 §5.2) reuses
// controller-runtime's existing Prometheus server — see the
// `metrics` sibling file for the per-instrument registrations.
// Traces emit via OTLP/gRPC when `OTEL_EXPORTER_OTLP_ENDPOINT` is
// set, mirroring the engine's optional-exporter pattern (ADR-0031
// §2.2 + Cluster A).
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

// Resource attribute keys (OTel semantic conventions). We pin the
// strings directly instead of importing `go.opentelemetry.io/otel/
// semconv/v1.27.0` so a schema-URL bump in the SDK does not require
// a manual semconv version chase. The keys themselves are stable
// across schema versions per the OTel governance.
const (
	attrServiceName      = "service.name"
	attrServiceVersion   = "service.version"
	attrServiceNamespace = "service.namespace"
)

// semconv exposes the keys used in `Init` via `attribute.KeyValue`
// for resource construction.
var semconv = struct {
	ServiceName      func(string) attribute.KeyValue
	ServiceVersion   func(string) attribute.KeyValue
	ServiceNamespace func(string) attribute.KeyValue
}{
	ServiceName:      func(v string) attribute.KeyValue { return attribute.String(attrServiceName, v) },
	ServiceVersion:   func(v string) attribute.KeyValue { return attribute.String(attrServiceVersion, v) },
	ServiceNamespace: func(v string) attribute.KeyValue { return attribute.String(attrServiceNamespace, v) },
}

// ServiceName is the canonical `service.name` resource attribute
// shared across all operator emissions (OTel resource + JSON log
// `service` field) per ADR-0031 §3.4.
const ServiceName = "spectre-control-plane"

// TracerName is the global tracer slot the operator's spans
// register against.
const TracerName = "spectre-control-plane"

// Shutdown drains the OTel pipeline on process exit. Idempotent:
// calling it twice is a no-op.
type Shutdown func(context.Context) error

// Init registers the global W3C TraceContext propagator and a tracer
// provider. The OTLP exporter is attached only when
// `OTEL_EXPORTER_OTLP_ENDPOINT` is non-empty, mirroring the engine's
// optional-exporter pattern (ADR-0023 §6 — Kafka / S3 likewise opt
// in via env). Without an endpoint, spans still generate valid IDs
// and propagate downstream via `traceparent`; they are simply
// dropped at end-of-span instead of being exported.
func Init(ctx context.Context, serviceVersion string) (Shutdown, error) {
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	// `resource.NewSchemaless` (no schema URL) avoids the merge
	// conflict that would arise from blending a pinned `semconv`
	// version against the SDK's own default schema. The mandatory
	// attributes per ADR-0031 §3.4 + §3.5 land directly; host /
	// process attributes the SDK default adds are not consumed by
	// the operator's spans today (ADR-0031 §5.5 deferral).
	res := resource.NewSchemaless(
		semconv.ServiceName(ServiceName),
		semconv.ServiceVersion(serviceVersion),
		semconv.ServiceNamespace("spectre"),
	)

	builder := sdktrace.NewTracerProvider(sdktrace.WithResource(res))
	endpoint := os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")
	if endpoint != "" {
		exporter, expErr := otlptracegrpc.New(ctx, otlptracegrpc.WithEndpoint(stripScheme(endpoint)), otlptracegrpc.WithInsecure())
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
		// 5s ceiling so a stuck collector cannot block the operator's
		// own shutdown.
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
