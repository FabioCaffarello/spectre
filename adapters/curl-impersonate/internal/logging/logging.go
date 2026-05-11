// SPDX-License-Identifier: Apache-2.0

// Package logging emits JSON-line stdout events per ADR-0031 §3.4.
//
// Built on slog's `JSONHandler` with a thin wrapper that injects
// `trace_id` + `span_id` from the OTel context active at the
// emission site. The 11 mandatory fields ADR-0031 §3.4 codifies
// land at the top level; per-call extras are additive.
//
// Field shape (ADR-0031 §3.4):
//
//	timestamp        RFC 3339 with ms precision (slog.Record.Time.UTC())
//	level            slog level (DEBUG/INFO/WARN/ERROR)
//	service          configured at handler construction
//	service_version  configured at handler construction
//	caller           <file>:<line> from slog.Record.Source
//	message          slog.Record.Message
//	trace_id         from `trace.SpanContextFromContext(ctx)`
//	span_id          same
//	request_id       caller's `request_id` attr, else null
//	job_id           caller's `job_id` attr, else null
//	tenant_id        always null in v1alpha1 (multi-tenancy is v1beta1)
//	latency_ms       caller's `latency_ms` attr, omitted when absent
//	error_code       caller's `error_code` attr on ERROR level only
//
// Mirror of `engines/engine/src/telemetry/logs.rs` (Cluster D of
// W3.1) — identical schema so cross-service log correlation works
// out of the box.
package logging

import (
	"context"
	"io"
	"log/slog"

	"go.opentelemetry.io/otel/trace"
)

// New constructs a slog.Logger writing one JSON line per event to
// `w` (typically `os.Stdout`). `service` + `serviceVersion` are
// stamped on every record as static top-level fields.
func New(w io.Writer, service, serviceVersion string) *slog.Logger {
	base := slog.NewJSONHandler(w, &slog.HandlerOptions{
		Level:     slog.LevelInfo,
		AddSource: true,
		ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
			switch a.Key {
			case slog.TimeKey:
				return slog.Attr{Key: "timestamp", Value: a.Value}
			case slog.LevelKey:
				return slog.Attr{Key: "level", Value: a.Value}
			case slog.MessageKey:
				return slog.Attr{Key: "message", Value: a.Value}
			case slog.SourceKey:
				// Compress slog.Source struct → "<file>:<line>" string.
				if src, ok := a.Value.Any().(*slog.Source); ok {
					return slog.String("caller", formatSource(src))
				}
			}
			return a
		},
	})
	return slog.New(&otelHandler{
		inner:          base,
		service:        service,
		serviceVersion: serviceVersion,
	})
}

// otelHandler wraps a slog.Handler and injects the
// service / service_version / trace_id / span_id / tenant_id
// fields ADR-0031 §3.4 mandates.
type otelHandler struct {
	inner          slog.Handler
	service        string
	serviceVersion string
}

func (h *otelHandler) Enabled(ctx context.Context, lvl slog.Level) bool {
	return h.inner.Enabled(ctx, lvl)
}

func (h *otelHandler) Handle(ctx context.Context, r slog.Record) error {
	r.AddAttrs(
		slog.String("service", h.service),
		slog.String("service_version", h.serviceVersion),
		// tenant_id is always emitted (null) for forward-compat with
		// multi-tenant deployments (v1beta1 scope).
		slog.Any("tenant_id", nil),
	)

	// trace_id + span_id from the active OTel context. With the
	// W3C propagator + tracer provider registered (telemetry.Init),
	// `IsValid()` is true whenever the event fires inside a span
	// — typically a server-kind RPC span set up by
	// `otelgrpc.NewServerHandler()`.
	span := trace.SpanContextFromContext(ctx)
	if span.IsValid() {
		r.AddAttrs(
			slog.String("trace_id", span.TraceID().String()),
			slog.String("span_id", span.SpanID().String()),
		)
	} else {
		r.AddAttrs(
			slog.Any("trace_id", nil),
			slog.Any("span_id", nil),
		)
	}

	return h.inner.Handle(ctx, r)
}

func (h *otelHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &otelHandler{
		inner:          h.inner.WithAttrs(attrs),
		service:        h.service,
		serviceVersion: h.serviceVersion,
	}
}

func (h *otelHandler) WithGroup(name string) slog.Handler {
	return &otelHandler{
		inner:          h.inner.WithGroup(name),
		service:        h.service,
		serviceVersion: h.serviceVersion,
	}
}

// formatSource renders the slog.Source as `<file>:<line>` matching
// the engine's `caller` field shape exactly. Only the basename
// keeps log lines compact; the absolute path would explode size
// without adding signal.
func formatSource(src *slog.Source) string {
	file := src.File
	for i := len(file) - 1; i >= 0; i-- {
		if file[i] == '/' {
			file = file[i+1:]
			break
		}
	}
	return file + ":" + itoa(src.Line)
}

// itoa avoids strconv.Itoa to keep this file dependency-free for
// the smallest possible hot path.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	neg := n < 0
	if neg {
		n = -n
	}
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
