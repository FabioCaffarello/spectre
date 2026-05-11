// SPDX-License-Identifier: Apache-2.0
//
// Spectre Playwright driver — gRPC service implementation.
//
// Implements the v1alpha1 unary surface: `Initialize`, `Navigate`,
// `Close`, `Query`, `Extract`, and `Screenshot`. See ADR-0008
// (handshake), ADR-0009
// (Navigate / session lifecycle / error mapping), ADR-0010 (element
// lifecycle and capability gating), and ADR-0011 (Screenshot scope
// mapping, JPEG quality default, payload-size boundary, read-only
// contract). R2.2 swaps the Unix-domain-socket transport for TCP
// (ADR-0021 + ADR-0022); the wire-level service definitions in
// proto/spectre/driver/v1alpha1 are unchanged.

import { create } from "@bufbuild/protobuf";
import { DurationSchema } from "@bufbuild/protobuf/wkt";
import { Code, ConnectError, type ConnectRouter } from "@connectrpc/connect";
import { connectNodeAdapter } from "@connectrpc/connect-node";
import { randomUUID } from "node:crypto";
import * as http2 from "node:http2";
import { chromium, type Locator, type Page } from "playwright";

import {
  Health,
  HealthCheckResponseSchema,
  HealthCheckResponse_ServingStatus,
} from "./proto/grpc/health/v1/health_pb.js";

import {
  CAPABILITY_NAMES,
  DRIVER_VERSION,
  missingCapabilityForMode,
} from "./capabilities.js";
import { playwrightErrorToDriverError } from "./errors.js";
import { CapabilitiesSchema } from "./proto/spectre/driver/v1alpha1/capabilities_pb.js";
import {
  Driver,
  type CloseRequest,
  type CloseResponse,
  CloseResponseSchema,
  type InitializeRequest,
  type InitializeResponse,
  InitializeResponseSchema,
  type NavigateRequest,
  type NavigateResponse,
  NavigateResponseSchema,
  type ScreenshotRequest,
  type ScreenshotResponse,
  ScreenshotResponseSchema,
  ScreenshotFormat,
  ScreenshotScope,
  WaitCondition,
} from "./proto/spectre/driver/v1alpha1/driver_pb.js";
import { DriverError_Code } from "./proto/spectre/driver/v1alpha1/errors_pb.js";
import {
  type ExtractRequest,
  type ExtractResponse,
  ExtractResponseSchema,
  ExtractedValuesSchema,
  ExtractedValues_EntrySchema,
  Field_Mode,
  type QueryRequest,
  type QueryResponse,
  QueryResponseSchema,
  SelectorKind,
} from "./proto/spectre/driver/v1alpha1/extraction_pb.js";
import { type RedisClientLike } from "./redis.js";
import {
  type BrowserFactory,
  SessionManager,
  UnknownSessionError,
} from "./sessions.js";
import { type AdapterMetrics, KIND as METRIC_KIND } from "./telemetry.js";
import { getLogger } from "./logging.js";
import type { Histogram } from "@opentelemetry/api";

/**
 * Record the wrapped handler's duration with the canonical
 * `result` label derived from the in-band `DriverError`. Mirror
 * of the SeleniumBase `_navigate_impl` / `_extract_impl` wrapper
 * pattern: the handler body stays unchanged with `return`
 * statements; the timing wrapper handles label derivation.
 */
async function timed<T extends { error?: unknown }>(
  histogram: Histogram | undefined,
  fn: () => Promise<T>,
): Promise<T> {
  const start = process.hrtime.bigint();
  let resp: T | undefined;
  try {
    resp = await fn();
    return resp;
  } finally {
    if (histogram) {
      const seconds = Number(process.hrtime.bigint() - start) / 1_000_000_000;
      const result = resp?.error ? "failure" : "success";
      histogram.record(seconds, { kind: METRIC_KIND, result });
    }
  }
}

export interface DriverServiceImpl {
  initialize(req: InitializeRequest): Promise<InitializeResponse>;
  navigate(req: NavigateRequest): Promise<NavigateResponse>;
  query(req: QueryRequest): Promise<QueryResponse>;
  extract(req: ExtractRequest): Promise<ExtractResponse>;
  screenshot(req: ScreenshotRequest): Promise<ScreenshotResponse>;
  close(req: CloseRequest): Promise<CloseResponse>;
}

const DEFAULT_NAVIGATE_TIMEOUT_MS = 30_000;

// ADR-0011, decision 2: JPEG quality is fixed at 80 in v1alpha1.
// The choice is the conventional "high" preset balancing fidelity
// against payload size; clients who need a different quality
// should request PNG (lossless) until v1alpha2 adds a quality
// field on `ScreenshotRequest`.
const JPEG_QUALITY_DEFAULT = 80;

// ADR-0011, decision 3: soft-warn at 3MB so an operator sees the
// boundary before a full-page screenshot of a long page crosses
// the ~4MB Connect transport limit. The adapter does not fail the
// RPC at the warning threshold — the bytes are returned unchanged
// and the transport surfaces a `RESOURCE_EXHAUSTED`-style error if
// the message actually exceeds the limit.
const SCREENSHOT_PAYLOAD_WARN_BYTES = 3 * 1024 * 1024;

export const defaultBrowserFactory: BrowserFactory = () => chromium.launch();

const waitConditionToPlaywright = (
  wait: WaitCondition,
): "load" | "domcontentloaded" | "networkidle" => {
  switch (wait) {
    case WaitCondition.DOM_CONTENT_LOADED:
      return "domcontentloaded";
    case WaitCondition.NETWORK_IDLE:
      return "networkidle";
    case WaitCondition.LOAD:
    case WaitCondition.UNSPECIFIED:
    default:
      return "load";
  }
};

const durationToMs = (
  d: { seconds?: bigint; nanos?: number } | undefined,
): number | null => {
  if (!d) return null;
  const seconds = d.seconds ?? 0n;
  const nanos = d.nanos ?? 0;
  if (seconds === 0n && nanos === 0) return null;
  return Number(seconds) * 1000 + Math.floor(nanos / 1_000_000);
};

const msToDuration = (ms: number): { seconds: bigint; nanos: number } => {
  const whole = Math.floor(ms / 1000);
  const remainder = ms - whole * 1000;
  return create(DurationSchema, {
    seconds: BigInt(whole),
    nanos: remainder * 1_000_000,
  });
};

const errorResponse = (
  code: DriverError_Code,
  message: string,
): NavigateResponse =>
  create(NavigateResponseSchema, {
    error: { code, message },
  });

const queryError = (code: DriverError_Code, message: string): QueryResponse =>
  create(QueryResponseSchema, {
    error: { code, message },
  });

const extractError = (
  code: DriverError_Code,
  message: string,
): ExtractResponse =>
  create(ExtractResponseSchema, {
    error: { code, message },
  });

const closeError = (code: DriverError_Code, message: string): CloseResponse =>
  create(CloseResponseSchema, {
    error: { code, message },
  });

const screenshotError = (
  code: DriverError_Code,
  message: string,
): ScreenshotResponse =>
  create(ScreenshotResponseSchema, {
    error: { code, message },
  });

const STALE_NAVIGATE_MESSAGE =
  "element reference is stale; query was performed before a navigation";
const UNKNOWN_REF_MESSAGE =
  "element reference not found in this session; ensure Query was called against the same session_id";

class ProtocolError extends Error {
  readonly code: DriverError_Code;
  constructor(code: DriverError_Code, message: string) {
    super(message);
    this.code = code;
    this.name = "ProtocolError";
  }
}

const selectorKindToLocator = (
  page: Page,
  kind: SelectorKind,
  selector: string,
): Locator | null => {
  switch (kind) {
    case SelectorKind.CSS:
      return page.locator(selector);
    case SelectorKind.XPATH:
      return page.locator(`xpath=${selector}`);
    case SelectorKind.TEXT:
      return page.getByText(selector, { exact: false });
    case SelectorKind.ATTRIBUTE:
      return page.locator(`[${selector}]`);
    case SelectorKind.UNSPECIFIED:
    default:
      return null;
  }
};

const readField = async (
  locator: Locator,
  fieldName: string,
  mode: Field_Mode,
  arg: string,
): Promise<unknown> => {
  switch (mode) {
    case Field_Mode.TEXT_CONTENT:
      return (await locator.textContent()) ?? "";
    case Field_Mode.INNER_TEXT:
      return await locator.innerText();
    case Field_Mode.INNER_HTML:
      return await locator.innerHTML();
    case Field_Mode.OUTER_HTML:
      return await locator.evaluate(
        (el) => (el as unknown as { outerHTML: string }).outerHTML,
      );
    case Field_Mode.ATTR:
      return (await locator.getAttribute(arg)) ?? null;
    case Field_Mode.EVAL:
      // Function is serialised by Playwright and executed in the
      // page context, with `el` bound to the matched element and
      // `arg` carrying the JS expression string. The `new Function`
      // call therefore runs in the browser, not in Node.
      return await locator.evaluate(
        // eslint-disable-next-line no-new-func
        (el, expr) => new Function("el", `return (${expr});`)(el),
        arg,
      );
    case Field_Mode.UNSPECIFIED:
    default:
      throw new ProtocolError(
        DriverError_Code.INVALID_ARGUMENT,
        `field ${JSON.stringify(fieldName)} has an unspecified mode`,
      );
  }
};

const isValidNavigationUrl = (url: string): boolean => {
  try {
    const parsed = new URL(url);
    return parsed.protocol === "http:" || parsed.protocol === "https:";
  } catch {
    return false;
  }
};

// R4.3 / ADR-0023 §5: every non-Initialize RPC validates the session
// against Redis before doing any work. The result is one of:
//
//   - "ok"      → Redis has the session and `adapter_instance_id`
//                 matches; caller proceeds with the RPC.
//   - "unknown" → Redis has no entry for this id (never created or
//                 TTL-expired). Caller maps to the existing
//                 in-band `INVALID_ARGUMENT` envelope.
//
// Restart-invalidation ("different-instance") and Redis-unreachable
// surface as transport-level gRPC `UNAVAILABLE` (Code.Unavailable),
// thrown synchronously here. The conformance test in
// `tools/conformance/tests/test_session_restart_invalidation.py`
// asserts on `grpc.StatusCode.UNAVAILABLE` precisely against this
// throw path.
type SessionGate = { kind: "ok" } | { kind: "unknown" };

const gateSession = async (
  sessions: SessionManager,
  sessionId: string,
): Promise<SessionGate> => {
  let result;
  try {
    result = await sessions.validate(sessionId);
  } catch (err) {
    throw new ConnectError(
      `redis unreachable: ${err instanceof Error ? err.message : String(err)}`,
      Code.Unavailable,
    );
  }
  if (result.kind === "different-instance") {
    throw new ConnectError(
      "session belongs to a different adapter instance; client must re-Initialize",
      Code.Unavailable,
    );
  }
  return result;
};

const unknownSessionResponse = (sessionId: string): string =>
  `unknown session_id ${JSON.stringify(sessionId)}; call Initialize first`;

export const createDriverService = (
  sessions: SessionManager,
  metrics?: AdapterMetrics,
): DriverServiceImpl => ({
  async initialize(req: InitializeRequest): Promise<InitializeResponse> {
    const start = process.hrtime.bigint();
    // W3.2 Cluster A: every requested capability not in the adapter
    // manifest increments `capability_violations_total` with the
    // offending name. The Initialize still succeeds with the
    // adapter's actual manifest so the caller can negotiate
    // gracefully; the counter is the auditable signal.
    if (metrics) {
      const manifest = new Set<string>(CAPABILITY_NAMES);
      for (const requested of req.requestedCapabilities ?? []) {
        if (!manifest.has(requested)) {
          metrics.capabilityViolationsTotal.add(1, {
            kind: METRIC_KIND,
            capability: requested,
          });
        }
      }
    }

    const sessionId = randomUUID();
    // ADR-0023 §6 makes Redis required: if the metadata write fails
    // we surface the failure at the transport layer so the caller
    // sees the same gRPC `UNAVAILABLE` it would see on adapter
    // startup. The local `registered` set is only updated after a
    // successful Redis write (see `SessionManager.register`), so
    // there is nothing to roll back here.
    try {
      await sessions.register(sessionId);
    } catch (err) {
      throw new ConnectError(
        `redis unreachable; cannot persist session metadata: ${err instanceof Error ? err.message : String(err)}`,
        Code.Unavailable,
      );
    }
    if (metrics) {
      metrics.sessionsActive.add(1, { kind: METRIC_KIND });
      metrics.initializeDuration.record(
        Number(process.hrtime.bigint() - start) / 1_000_000_000,
        { kind: METRIC_KIND },
      );
    }
    const capabilities = create(CapabilitiesSchema, {
      names: [...CAPABILITY_NAMES],
      driverVersion: DRIVER_VERSION,
      runtimeVersion: `node@${process.version}`,
    });
    return create(InitializeResponseSchema, {
      sessionId,
      capabilities,
    });
  },
  async navigate(req: NavigateRequest): Promise<NavigateResponse> {
    return timed(metrics?.navigateDuration, async () => {
      // W3.2 Cluster E follow-up: emit one info log per Navigate
      // RPC inside the server-kind span the HttpInstrumentation
      // auto-opens. Pino's `formatters.log` reads
      // `trace.getActiveSpan()?.spanContext()` at call time, so
      // this line surfaces the trace_id propagated from the engine
      // — the proof point the production-smoke trace-topology
      // assertion grep's on.
      getLogger().info({ session_id: req.sessionId, url: req.url }, "navigate");
      if (!req.sessionId) {
        return errorResponse(
          DriverError_Code.INVALID_ARGUMENT,
          "session_id is required",
        );
      }
      const gate = await gateSession(sessions, req.sessionId);
      if (gate.kind === "unknown") {
        return errorResponse(
          DriverError_Code.INVALID_ARGUMENT,
          unknownSessionResponse(req.sessionId),
        );
      }
      if (!req.url) {
        return errorResponse(
          DriverError_Code.INVALID_ARGUMENT,
          "url is required",
        );
      }
      if (!isValidNavigationUrl(req.url)) {
        return errorResponse(
          DriverError_Code.INVALID_ARGUMENT,
          `url must be an absolute http(s) URL, got ${JSON.stringify(req.url)}`,
        );
      }

      const timeoutMs =
        durationToMs(req.timeout) ?? DEFAULT_NAVIGATE_TIMEOUT_MS;
      const waitUntil = waitConditionToPlaywright(req.wait);

      let page;
      try {
        page = await sessions.getOrCreatePage(req.sessionId);
      } catch (err) {
        if (err instanceof UnknownSessionError) {
          return errorResponse(DriverError_Code.INVALID_ARGUMENT, err.message);
        }
        const mapped = playwrightErrorToDriverError(err);
        return errorResponse(mapped.code, mapped.message);
      }

      const start = process.hrtime.bigint();
      try {
        const response = await page.goto(req.url, {
          waitUntil,
          timeout: timeoutMs,
        });
        // Strict ElementRef invalidation: any successful navigation
        // bumps the session's generation counter. Refs allocated in
        // a prior generation become stale and Extract will reject
        // them with CODE_INVALID_ARGUMENT. See ADR-0010.
        sessions.bumpGeneration(req.sessionId);
        const elapsedMs = Number(process.hrtime.bigint() - start) / 1_000_000;
        return create(NavigateResponseSchema, {
          finalUrl: response?.url() ?? page.url(),
          statusCode: response?.status() ?? 0,
          elapsed: msToDuration(Math.round(elapsedMs)),
        });
      } catch (err) {
        const mapped = playwrightErrorToDriverError(err);
        return create(NavigateResponseSchema, {
          elapsed: msToDuration(
            Math.round(Number(process.hrtime.bigint() - start) / 1_000_000),
          ),
          error: { code: mapped.code, message: mapped.message },
        });
      }
    });
  },
  async query(req: QueryRequest): Promise<QueryResponse> {
    if (!req.sessionId) {
      return queryError(
        DriverError_Code.INVALID_ARGUMENT,
        "session_id is required",
      );
    }
    const gate = await gateSession(sessions, req.sessionId);
    if (gate.kind === "unknown") {
      return queryError(
        DriverError_Code.INVALID_ARGUMENT,
        unknownSessionResponse(req.sessionId),
      );
    }
    if (req.kind === SelectorKind.UNSPECIFIED) {
      return queryError(
        DriverError_Code.INVALID_ARGUMENT,
        "selector kind is required; SELECTOR_KIND_UNSPECIFIED is not accepted",
      );
    }
    if (!req.selector) {
      return queryError(
        DriverError_Code.INVALID_ARGUMENT,
        "selector is required",
      );
    }

    const page = sessions.pageOf(req.sessionId);
    if (!page) {
      return queryError(
        DriverError_Code.INVALID_ARGUMENT,
        "no page is open for this session; call Navigate first",
      );
    }

    const locator = selectorKindToLocator(page, req.kind, req.selector);
    if (!locator) {
      return queryError(
        DriverError_Code.INVALID_ARGUMENT,
        `unsupported selector kind: ${req.kind}`,
      );
    }

    try {
      // Locator.all() materialises every match at call time. For
      // pages with many matches and small `limit` this is wasteful;
      // accept the cost until real workloads surface real numbers.
      const matches = await locator.all();
      const limited = req.limit > 0 ? matches.slice(0, req.limit) : matches;
      const ids = sessions.allocateRefs(req.sessionId, limited);
      return create(QueryResponseSchema, {
        elements: ids.map((opaqueId) => ({ opaqueId })),
      });
    } catch (err) {
      const mapped = playwrightErrorToDriverError(err);
      return queryError(mapped.code, mapped.message);
    }
  },
  async extract(req: ExtractRequest): Promise<ExtractResponse> {
    return timed(metrics?.extractDuration, async () => {
      if (!req.sessionId) {
        return extractError(
          DriverError_Code.INVALID_ARGUMENT,
          "session_id is required",
        );
      }
      const gate = await gateSession(sessions, req.sessionId);
      if (gate.kind === "unknown") {
        return extractError(
          DriverError_Code.INVALID_ARGUMENT,
          unknownSessionResponse(req.sessionId),
        );
      }
      const opaqueId = req.element?.opaqueId ?? "";
      if (!opaqueId) {
        return extractError(
          DriverError_Code.INVALID_ARGUMENT,
          "element.opaque_id is required",
        );
      }
      if (req.fields.length === 0) {
        return extractError(
          DriverError_Code.INVALID_ARGUMENT,
          "at least one field is required",
        );
      }

      // Capability gating: the runtime check fires before any DOM
      // work so an under-declared driver fails the request whole,
      // not partway. See ADR-0010, decision 3.
      for (const field of req.fields) {
        const missing = missingCapabilityForMode(field.mode, CAPABILITY_NAMES);
        if (missing) {
          return extractError(
            DriverError_Code.CAPABILITY_MISSING,
            `MODE_EVAL requires the ${missing} capability`,
          );
        }
      }

      const lookup = sessions.lookupRef(req.sessionId, opaqueId);
      if (lookup.status === "stale") {
        return extractError(
          DriverError_Code.INVALID_ARGUMENT,
          STALE_NAVIGATE_MESSAGE,
        );
      }
      if (lookup.status === "unknown" || !lookup.locator) {
        return extractError(
          DriverError_Code.INVALID_ARGUMENT,
          UNKNOWN_REF_MESSAGE,
        );
      }
      const locator = lookup.locator;

      const entries: { name: string; jsonValue: string }[] = [];
      for (const field of req.fields) {
        try {
          const value = await readField(
            locator,
            field.name,
            field.mode,
            field.arg,
          );
          entries.push({
            name: field.name,
            jsonValue: JSON.stringify(value),
          });
        } catch (err) {
          if (err instanceof ProtocolError) {
            return extractError(err.code, err.message);
          }
          const mapped = playwrightErrorToDriverError(err);
          return extractError(mapped.code, mapped.message);
        }
      }

      return create(ExtractResponseSchema, {
        values: create(ExtractedValuesSchema, {
          fields: entries.map((entry) =>
            create(ExtractedValues_EntrySchema, entry),
          ),
        }),
      });
    });
  },
  async screenshot(req: ScreenshotRequest): Promise<ScreenshotResponse> {
    if (!req.sessionId) {
      return screenshotError(
        DriverError_Code.INVALID_ARGUMENT,
        "session_id is required",
      );
    }
    const gate = await gateSession(sessions, req.sessionId);
    if (gate.kind === "unknown") {
      return screenshotError(
        DriverError_Code.INVALID_ARGUMENT,
        unknownSessionResponse(req.sessionId),
      );
    }
    if (req.scope === ScreenshotScope.UNSPECIFIED) {
      return screenshotError(
        DriverError_Code.INVALID_ARGUMENT,
        "scope is required; SCREENSHOT_SCOPE_UNSPECIFIED is not accepted",
      );
    }
    if (req.scope === ScreenshotScope.ELEMENT && !req.element?.opaqueId) {
      return screenshotError(
        DriverError_Code.INVALID_ARGUMENT,
        "element is required when scope is SCREENSHOT_SCOPE_ELEMENT",
      );
    }

    const page = sessions.pageOf(req.sessionId);
    if (!page) {
      return screenshotError(
        DriverError_Code.INVALID_ARGUMENT,
        "no page is open for this session; call Navigate first",
      );
    }

    // ADR-0011, decision 2: `_UNSPECIFIED` format defaults to PNG
    // (lossless, alpha-aware). JPEG carries a fixed quality of 80.
    const isJpeg = req.format === ScreenshotFormat.JPEG;
    const playwrightOptions: { type: "png" | "jpeg"; quality?: number } = isJpeg
      ? { type: "jpeg", quality: JPEG_QUALITY_DEFAULT }
      : { type: "png" };
    const contentType = isJpeg ? "image/jpeg" : "image/png";

    let buffer: Buffer;
    try {
      switch (req.scope) {
        case ScreenshotScope.VIEWPORT:
          buffer = await page.screenshot({
            ...playwrightOptions,
            fullPage: false,
          });
          break;
        case ScreenshotScope.FULL_PAGE:
          buffer = await page.screenshot({
            ...playwrightOptions,
            fullPage: true,
          });
          break;
        case ScreenshotScope.ELEMENT: {
          // `req.element.opaqueId` is non-empty here because the
          // argument-validation block above rejected the empty case.
          const opaqueId = req.element?.opaqueId ?? "";
          const lookup = sessions.lookupRef(req.sessionId, opaqueId);
          if (lookup.status === "stale") {
            return screenshotError(
              DriverError_Code.INVALID_ARGUMENT,
              STALE_NAVIGATE_MESSAGE,
            );
          }
          if (lookup.status === "unknown" || !lookup.locator) {
            return screenshotError(
              DriverError_Code.INVALID_ARGUMENT,
              UNKNOWN_REF_MESSAGE,
            );
          }
          buffer = await lookup.locator.screenshot(playwrightOptions);
          break;
        }
        default:
          return screenshotError(
            DriverError_Code.INVALID_ARGUMENT,
            `unsupported scope: ${req.scope}`,
          );
      }
    } catch (err) {
      const mapped = playwrightErrorToDriverError(err);
      return screenshotError(mapped.code, mapped.message);
    }

    // Buffer extends Uint8Array in Node, but `protoc-gen-es` types
    // the wire field as Uint8Array; the explicit copy keeps the
    // payload portable and decouples downstream consumers from
    // Node's Buffer pooling semantics.
    const image = new Uint8Array(buffer);

    if (image.byteLength > SCREENSHOT_PAYLOAD_WARN_BYTES) {
      // ADR-0011, decision 3.
      process.stderr.write(
        `screenshot payload ${image.byteLength} bytes exceeds 3MB warning threshold; ` +
          `v1alpha1 transport limit is ~4MB\n`,
      );
    }

    return create(ScreenshotResponseSchema, {
      image,
      contentType,
    });
  },
  async close(req: CloseRequest): Promise<CloseResponse> {
    if (!req.sessionId) {
      return closeError(
        DriverError_Code.INVALID_ARGUMENT,
        "session_id is required",
      );
    }
    // Close still validates the session against Redis: a Close from
    // a foreign instance would otherwise tear down nothing locally
    // (no browser to close) yet succeed silently. Restart-
    // invalidation surfaces to the client as `UNAVAILABLE` here for
    // the same reason it does on Navigate. The Redis-delete inside
    // `closeSession` is best-effort (§4.6); only the validate step
    // is gated.
    const gate = await gateSession(sessions, req.sessionId);
    if (gate.kind === "unknown") {
      return closeError(
        DriverError_Code.INVALID_ARGUMENT,
        `unknown session_id ${JSON.stringify(req.sessionId)}`,
      );
    }
    await sessions.closeSession(req.sessionId);
    if (metrics) {
      metrics.sessionsActive.add(-1, { kind: METRIC_KIND });
    }
    return create(CloseResponseSchema);
  },
});

// ADR-0021 §6 makes the gRPC standard health check a non-negotiable
// part of every adapter's surface. The conformance harness polls
// `Health.Check` until it returns SERVING; production deployments
// (R6.2 onward) wire the same endpoint into Compose / Kubernetes
// readiness probes. The Playwright adapter is not a `@grpc/grpc-js`
// server, so we register a manual implementation backed by the
// vendored `proto/grpc/health/v1/health.proto` bindings rather than
// relying on `@grpc/grpc-health-check`.
const servingResponse = create(HealthCheckResponseSchema, {
  status: HealthCheckResponse_ServingStatus.SERVING,
});

export const driverRoutes =
  (impl: DriverServiceImpl) =>
  (router: ConnectRouter): void => {
    router.service(Driver, impl);
    router.service(Health, {
      check: () => servingResponse,
      // Watch is implemented as a single-shot stream that emits the
      // current status and stays open. The harness never subscribes;
      // production health probes use Check, which is enough.
      async *watch() {
        yield servingResponse;
      },
    });
  };

export interface ServerHandle {
  readonly host: string;
  readonly port: number;
  shutdown(): Promise<void>;
}

const SHUTDOWN_DEADLINE_MS = 5_000;

export interface StartServerOptions {
  browserFactory?: BrowserFactory;
  sessions?: SessionManager;
  host?: string;
  // Optional alternative to passing a fully-constructed
  // `SessionManager`: provide a Redis client and the adapter's
  // `instance_id` and `startServer` will assemble one with the
  // default browser factory (or `browserFactory` if supplied).
  redis?: RedisClientLike;
  instanceId?: string;
  // W3.2 Cluster A: the §5.3 metric handles
  // `createDriverService` records into. `undefined` disables
  // emission (used by tests).
  metrics?: AdapterMetrics;
}

export async function startServer(
  port: number,
  options: StartServerOptions = {},
): Promise<ServerHandle> {
  const host = options.host ?? "0.0.0.0";

  let sessions = options.sessions;
  if (!sessions) {
    if (!options.redis || !options.instanceId) {
      throw new Error(
        "startServer requires either an explicit `sessions` SessionManager " +
          "or both `redis` and `instanceId` so it can construct one",
      );
    }
    sessions = new SessionManager({
      factory: options.browserFactory ?? defaultBrowserFactory,
      redis: options.redis,
      instanceId: options.instanceId,
    });
  }
  const impl = createDriverService(sessions, options.metrics);

  const handler = connectNodeAdapter({ routes: driverRoutes(impl) });
  const server = http2.createServer(handler);

  const boundPort = await new Promise<number>((resolve, reject) => {
    const onError = (err: Error) => {
      server.removeListener("listening", onListening);
      reject(err);
    };
    const onListening = () => {
      server.removeListener("error", onError);
      const addr = server.address();
      if (addr === null || typeof addr === "string") {
        reject(
          new Error(
            `expected an AF_INET listener, got ${typeof addr === "string" ? addr : "null"}`,
          ),
        );
        return;
      }
      resolve(addr.port);
    };
    server.once("error", onError);
    server.once("listening", onListening);
    server.listen(port, host);
  });

  let closed = false;
  const shutdown = async (): Promise<void> => {
    if (closed) return;
    closed = true;
    await new Promise<void>((resolve) => {
      const deadline = setTimeout(() => {
        process.stderr.write(
          `shutdown deadline reached after ${SHUTDOWN_DEADLINE_MS}ms; forcing close\n`,
        );
        resolve();
      }, SHUTDOWN_DEADLINE_MS);
      deadline.unref();
      server.close(() => {
        clearTimeout(deadline);
        resolve();
      });
    });
    // Tear down any browsers/contexts before exiting. A leaked
    // browser process would outlive the adapter — see ADR-0009,
    // decision 2 (no idle eviction means closeAll on shutdown is
    // the only cleanup path).
    await sessions.closeAll().catch((err: unknown) => {
      process.stderr.write(`session teardown error: ${String(err)}\n`);
    });
  };

  return { host, port: boundPort, shutdown };
}
