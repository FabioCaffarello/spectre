// SPDX-License-Identifier: Apache-2.0
//
// OpenTelemetry observability surface for the Playwright adapter.
//
// ADR-0031 §3.5 (TypeScript SDK choice) + §5.3 (adapter metric
// taxonomy) landed here as W3.2 Cluster A. Mirror of the
// curl-impersonate `internal/telemetry/` package and the
// SeleniumBase `telemetry.py` module: tracer + metrics + shutdown
// helpers.
//
// The trace provider always registers a W3C propagator unconditionally
// so cross-service `traceparent` propagates even when the OTLP
// exporter is unconfigured (the optional-exporter pattern shared
// across engine + operator + the three adapters).
//
// `HttpInstrumentation` auto-instruments Node's HTTP/2 server (the
// `@connectrpc/connect-node` transport) so every incoming Connect
// RPC opens a server-kind span as a child of the engine's client-
// kind span (W3.1 Cluster B). Per-RPC handlers in `server.ts`
// inherit the active span; the `metrics` handle they record into
// is supplied separately.

import {
  type Counter,
  type Histogram,
  metrics as metricsApi,
  type UpDownCounter,
} from "@opentelemetry/api";
import { OTLPTraceExporter } from "@opentelemetry/exporter-trace-otlp-grpc";
import { PrometheusExporter } from "@opentelemetry/exporter-prometheus";
import { HttpInstrumentation } from "@opentelemetry/instrumentation-http";
import { resourceFromAttributes } from "@opentelemetry/resources";
import { NodeSDK } from "@opentelemetry/sdk-node";

/** Canonical `service.name` resource attribute (ADR-0031 §3.4 + §3.5). */
export const SERVICE_NAME = "spectre-playwright";

/** Global meter slot the adapter's instruments register against. */
export const METER_NAME = "spectre-playwright";

/**
 * Canonical `{kind}` label value the adapter stamps on every
 * `spectre_adapter_*` metric per ADR-0031 §3.4 (`lower_snake_case`).
 */
export const KIND = "playwright";

/** Default histogram buckets aligned with the curl-impersonate adapter. */
const DEFAULT_DURATION_BUCKETS = [
  0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10,
];

/** ADR-0031 §3.3 uniform port. Honoured when SPECTRE_METRICS_PORT is unset. */
export const DEFAULT_METRICS_PORT = 9090;

/** Holds the §5.3 instrument handles the DriverServiceImpl records into. */
export interface AdapterMetrics {
  readonly sessionsActive: UpDownCounter;
  readonly initializeDuration: Histogram;
  readonly navigateDuration: Histogram;
  readonly extractDuration: Histogram;
  readonly capabilityViolationsTotal: Counter;
}

/** Live OTel handles owned by the binary; the `shutdown` drains them. */
export interface TelemetryHandle {
  readonly metrics: AdapterMetrics;
  readonly sdk: NodeSDK;
  shutdown(): Promise<void>;
}

export interface InitOptions {
  readonly serviceVersion: string;
  readonly metricsPort?: number;
}

/**
 * Initialise the OpenTelemetry SDK + Prometheus `/metrics` exporter
 * + the five §5.3 instruments.
 *
 * The OTLP/gRPC trace exporter attaches only when
 * `OTEL_EXPORTER_OTLP_ENDPOINT` is non-empty — mirror of the
 * operator's optional-exporter pattern (ADR-0023 §6 alignment).
 * Without an endpoint, spans still generate valid IDs and propagate
 * downstream via `traceparent`; they are dropped at end-of-span
 * rather than exported.
 *
 * The `PrometheusExporter` self-hosts an HTTP server on
 * `metricsPort` (default 9090 per ADR-0031 §3.3) serving the
 * `/metrics` endpoint. The chart + Compose populate
 * `SPECTRE_METRICS_PORT` from `observability.metricsPort`.
 */
export async function initTelemetry(
  options: InitOptions,
): Promise<TelemetryHandle> {
  const metricsPort = options.metricsPort ?? DEFAULT_METRICS_PORT;

  const resource = resourceFromAttributes({
    "service.name": SERVICE_NAME,
    "service.version": options.serviceVersion,
    "service.namespace": "spectre",
  });

  // The PrometheusExporter self-hosts an HTTP listener; `endpoint`
  // defaults to `/metrics` so the chart + Compose can scrape it
  // without per-adapter routing.
  const prometheusExporter = new PrometheusExporter({
    port: metricsPort,
    host: "0.0.0.0",
  });

  const otlpEndpoint = process.env["OTEL_EXPORTER_OTLP_ENDPOINT"];
  const traceExporter =
    otlpEndpoint && otlpEndpoint.length > 0
      ? new OTLPTraceExporter({ url: otlpEndpoint })
      : undefined;

  const sdk = new NodeSDK({
    resource,
    traceExporter,
    metricReader: prometheusExporter,
    instrumentations: [
      // Auto-instruments Node's `http` / `http2` server so every
      // incoming Connect RPC opens a server-kind span with the
      // extracted `traceparent` as parent.
      new HttpInstrumentation(),
    ],
  });

  sdk.start();

  const meter = metricsApi.getMeter(METER_NAME);
  const metrics: AdapterMetrics = {
    sessionsActive: meter.createUpDownCounter(
      "spectre_adapter_sessions_active",
      {
        description: "Active driver sessions held by the adapter.",
      },
    ),
    initializeDuration: meter.createHistogram(
      "spectre_adapter_initialize_duration_seconds",
      {
        description: "Driver.Initialize RPC duration in seconds.",
        unit: "s",
        advice: { explicitBucketBoundaries: DEFAULT_DURATION_BUCKETS },
      },
    ),
    navigateDuration: meter.createHistogram(
      "spectre_adapter_navigate_duration_seconds",
      {
        description: "Driver.Navigate RPC duration in seconds.",
        unit: "s",
        advice: { explicitBucketBoundaries: DEFAULT_DURATION_BUCKETS },
      },
    ),
    extractDuration: meter.createHistogram(
      "spectre_adapter_extract_duration_seconds",
      {
        description: "Driver.Extract RPC duration in seconds.",
        unit: "s",
        advice: { explicitBucketBoundaries: DEFAULT_DURATION_BUCKETS },
      },
    ),
    capabilityViolationsTotal: meter.createCounter(
      "spectre_adapter_capability_violations_total",
      {
        description:
          "Initialize requests for capabilities not in the adapter manifest.",
      },
    ),
  };

  return {
    metrics,
    sdk,
    async shutdown(): Promise<void> {
      // 5s ceiling so a stuck collector cannot block the adapter's
      // own shutdown — same shape as engine + operator.
      await Promise.race([
        sdk.shutdown(),
        new Promise<void>((resolve) => setTimeout(resolve, 5_000)),
      ]);
    },
  };
}

/** Resolve the metrics port from `SPECTRE_METRICS_PORT`. */
export function resolveMetricsPort(env: NodeJS.ProcessEnv): number {
  const raw = env["SPECTRE_METRICS_PORT"];
  if (raw === undefined || raw === "") {
    return DEFAULT_METRICS_PORT;
  }
  const port = Number.parseInt(raw, 10);
  if (!Number.isInteger(port) || String(port) !== raw.trim()) {
    throw new Error(
      `SPECTRE_METRICS_PORT must be a port number, got ${JSON.stringify(raw)}`,
    );
  }
  if (port < 0 || port > 65535) {
    throw new Error(
      `SPECTRE_METRICS_PORT must be between 0 and 65535, got ${port}`,
    );
  }
  return port;
}
