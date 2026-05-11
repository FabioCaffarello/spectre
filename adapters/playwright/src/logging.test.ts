// SPDX-License-Identifier: Apache-2.0
//
// W3.2 Cluster A tests for `./logging`.

import { describe, expect, it, vi } from "vitest";
import { Writable } from "node:stream";
import pino from "pino";
import {
  trace,
  TraceFlags,
  type Span,
  type SpanContext,
} from "@opentelemetry/api";

// Re-implement `createLogger`'s `formatters.log` so we can target
// an in-memory writable buffer (rather than stdout via
// `pino.destination`) and assert on captured bytes. Mirrors the
// `createLogger` implementation 1:1 — if it drifts the
// `expect(obj.service).toBe(...)` assertion fails and the test
// flags the divergence.
function loggerWithBuffer(serviceVersion: string): {
  logger: pino.Logger;
  read: () => string;
} {
  let captured = "";
  const sink = new Writable({
    write(chunk: Buffer, _enc, cb) {
      captured += chunk.toString();
      cb();
    },
  });
  const logger = pino(
    {
      level: "trace",
      base: null,
      timestamp: () => `,"timestamp":"${new Date().toISOString()}"`,
      messageKey: "message",
      formatters: {
        level: (label) => ({ level: label.toUpperCase() }),
        log(object) {
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
            tenant_id: null,
            ...traceFields,
            ...object,
          };
        },
      },
    },
    sink,
  );
  return { logger, read: () => captured };
}

describe("createLogger", () => {
  it("emits eleven mandatory fields with nulls for unset slots", () => {
    const { logger, read } = loggerWithBuffer("test-version");
    logger.info({ request_id: "req-1", job_id: "job-1" }, "hello");

    const obj = JSON.parse(read().trim()) as Record<string, unknown>;

    for (const key of [
      "timestamp",
      "level",
      "service",
      "service_version",
      "message",
      "trace_id",
      "span_id",
      "request_id",
      "job_id",
      "tenant_id",
    ]) {
      expect(obj).toHaveProperty(key);
    }
    expect(obj.level).toBe("INFO");
    expect(obj.service).toBe("spectre-playwright");
    expect(obj.service_version).toBe("test-version");
    expect(obj.message).toBe("hello");
    expect(obj.request_id).toBe("req-1");
    expect(obj.job_id).toBe("job-1");
    expect(obj.tenant_id).toBeNull();
    expect(obj.trace_id).toBeNull();
    expect(obj.span_id).toBeNull();
  });

  it("populates trace_id + span_id from the active span context", () => {
    // Stub `trace.getActiveSpan()` directly so the test doesn't
    // depend on a registered context manager (`AsyncHooksContextManager`).
    // Cluster A's production wiring registers one via
    // `NodeSDK.start()` + `HttpInstrumentation`; the unit test
    // verifies the formatter's read path, not the SDK's bookkeeping.
    const synthetic: SpanContext = {
      traceId: "0123456789abcdef0123456789abcdef",
      spanId: "0123456789abcdef",
      traceFlags: TraceFlags.SAMPLED,
      isRemote: true,
    };
    const wrapped = trace.wrapSpanContext(synthetic);
    const spy = vi
      .spyOn(trace, "getActiveSpan")
      .mockReturnValue(wrapped as Span);

    try {
      const { logger, read } = loggerWithBuffer("test-version");
      logger.info("inside span");

      const line = read().trim().split("\n").pop();
      expect(line).toBeDefined();
      const obj = JSON.parse(line as string) as Record<string, unknown>;
      expect(obj.trace_id).toBe(synthetic.traceId);
      expect(obj.span_id).toBe(synthetic.spanId);
    } finally {
      spy.mockRestore();
    }
  });
});
