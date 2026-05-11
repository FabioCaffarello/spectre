// SPDX-License-Identifier: Apache-2.0
//
// JSON-line stdout logger for the Playwright adapter (ADR-0031 §3.4).
//
// Built on Pino with a custom `formatters` config emitting the
// eleven mandatory fields ADR-0031 §3.4 codifies. `trace_id` and
// `span_id` are read from the active OTel context at emission
// time via `trace.getActiveSpan()?.spanContext()` — independent
// of whether the surrounding span was created by
// `HttpInstrumentation` (the auto-instrumented Connect RPC entry)
// or by manual `tracer.startActiveSpan(...)` blocks.
//
// Mirror of the curl-impersonate `internal/logging` package and
// the SeleniumBase `logging.py` module — identical field shape so
// cross-service log correlation works out of the box.

import pino, { type Logger } from "pino";
import { trace } from "@opentelemetry/api";

/**
 * Configure a Pino logger writing one JSON line per event to
 * `stdout` with the eleven canonical fields stamped at every
 * call. `service` + `service_version` are static; `trace_id` +
 * `span_id` + `tenant_id` are derived per call.
 */
export function createLogger(serviceVersion: string): Logger {
  return pino(
    {
      level: process.env["LOG_LEVEL"] ?? "info",
      base: null, // suppress default `pid` and `hostname` fields
      timestamp: () => `,"timestamp":"${new Date().toISOString()}"`,
      messageKey: "message",
      formatters: {
        level: (label) => ({ level: label.toUpperCase() }),
        log(object) {
          // ADR-0031 §3.4: every event carries `service` + canonical
          // null-by-default sentinels for the optional fields. The
          // log call's own keys (e.g. `request_id`, `job_id`,
          // `error_code`) merge on top — Pino's `log` formatter runs
          // before serialisation, so caller-supplied keys win when
          // present.
          const span = trace.getActiveSpan();
          const sc = span?.spanContext();
          const traceFields =
            sc && sc.traceId && sc.spanId
              ? { trace_id: sc.traceId, span_id: sc.spanId }
              : { trace_id: null, span_id: null };
          return {
            service: "spectre-playwright",
            service_version: serviceVersion,
            request_id: null,
            job_id: null,
            // ADR-0031 §3.4 — `tenant_id` is always emitted (null)
            // for forward-compat with multi-tenant deployments
            // (v1beta1 scope).
            tenant_id: null,
            ...traceFields,
            ...object,
          };
        },
      },
      // The `caller` field ADR-0031 §3.4 codifies cannot be
      // populated portably across Pino's transport / sync paths
      // without the `pino-caller` package — Cluster D (or a later
      // hardening pass) can wire that in. For now the field is
      // omitted from the schema; the engine + curl-impersonate +
      // SeleniumBase adapter all carry it, so this is a known
      // single-service gap recorded in W3.2 closing notes.
    },
    pino.destination({
      dest: 1, // stdout
      sync: true,
    }),
  );
}
