// SPDX-License-Identifier: Apache-2.0
//
// Spectre Playwright driver — gRPC service implementation.
//
// PR4 implemented `Initialize` and `Navigate`; PR5 adds `Close`,
// `Query`, and `Extract`. `Screenshot` is the only remaining
// unimplemented RPC and returns `Code.Unimplemented` so a
// misconfigured client gets a structured gRPC status rather than a
// hang. See ADR-0008 (handshake), ADR-0009 (Navigate / session
// lifecycle / error mapping), and ADR-0010 (element lifecycle and
// capability gating).

import { create } from "@bufbuild/protobuf";
import { DurationSchema } from "@bufbuild/protobuf/wkt";
import { Code, ConnectError, type ConnectRouter } from "@connectrpc/connect";
import { connectNodeAdapter } from "@connectrpc/connect-node";
import { randomUUID } from "node:crypto";
import { existsSync } from "node:fs";
import { unlink } from "node:fs/promises";
import * as http2 from "node:http2";
import { chromium, type Locator, type Page } from "playwright";

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
import {
  type BrowserFactory,
  SessionManager,
  UnknownSessionError,
} from "./sessions.js";

export interface DriverServiceImpl {
  initialize(req: InitializeRequest): Promise<InitializeResponse>;
  navigate(req: NavigateRequest): Promise<NavigateResponse>;
  query(req: QueryRequest): Promise<QueryResponse>;
  extract(req: ExtractRequest): Promise<ExtractResponse>;
  screenshot(req: ScreenshotRequest): Promise<ScreenshotResponse>;
  close(req: CloseRequest): Promise<CloseResponse>;
}

const DEFAULT_NAVIGATE_TIMEOUT_MS = 30_000;

const unimplemented = (rpc: string): never => {
  throw new ConnectError(
    `${rpc} is not implemented in spectre-playwright ${DRIVER_VERSION}`,
    Code.Unimplemented,
  );
};

const defaultBrowserFactory: BrowserFactory = () => chromium.launch();

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

export const createDriverService = (
  sessions: SessionManager,
): DriverServiceImpl => ({
  async initialize(_req: InitializeRequest): Promise<InitializeResponse> {
    const sessionId = randomUUID();
    sessions.register(sessionId);
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
    if (!req.sessionId) {
      return errorResponse(
        DriverError_Code.INVALID_ARGUMENT,
        "session_id is required",
      );
    }
    if (!sessions.has(req.sessionId)) {
      return errorResponse(
        DriverError_Code.INVALID_ARGUMENT,
        `unknown session_id ${JSON.stringify(req.sessionId)}; call Initialize first`,
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

    const timeoutMs = durationToMs(req.timeout) ?? DEFAULT_NAVIGATE_TIMEOUT_MS;
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
  },
  async query(req: QueryRequest): Promise<QueryResponse> {
    if (!req.sessionId) {
      return queryError(
        DriverError_Code.INVALID_ARGUMENT,
        "session_id is required",
      );
    }
    if (!sessions.has(req.sessionId)) {
      return queryError(
        DriverError_Code.INVALID_ARGUMENT,
        `unknown session_id ${JSON.stringify(req.sessionId)}; call Initialize first`,
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
    if (!req.sessionId) {
      return extractError(
        DriverError_Code.INVALID_ARGUMENT,
        "session_id is required",
      );
    }
    if (!sessions.has(req.sessionId)) {
      return extractError(
        DriverError_Code.INVALID_ARGUMENT,
        `unknown session_id ${JSON.stringify(req.sessionId)}; call Initialize first`,
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
  },
  async screenshot(_req: ScreenshotRequest): Promise<ScreenshotResponse> {
    unimplemented("Screenshot");
    return create(ScreenshotResponseSchema);
  },
  async close(req: CloseRequest): Promise<CloseResponse> {
    if (!req.sessionId) {
      return closeError(
        DriverError_Code.INVALID_ARGUMENT,
        "session_id is required",
      );
    }
    const closed = await sessions.closeSession(req.sessionId);
    if (!closed) {
      return closeError(
        DriverError_Code.INVALID_ARGUMENT,
        `unknown session_id ${JSON.stringify(req.sessionId)}`,
      );
    }
    return create(CloseResponseSchema);
  },
});

export const driverRoutes =
  (impl: DriverServiceImpl) =>
  (router: ConnectRouter): void => {
    router.service(Driver, impl);
  };

export interface ServerHandle {
  readonly socketPath: string;
  shutdown(): Promise<void>;
}

const SHUTDOWN_DEADLINE_MS = 5_000;

export interface StartServerOptions {
  browserFactory?: BrowserFactory;
  sessions?: SessionManager;
}

export async function startServer(
  socketPath: string,
  options: StartServerOptions = {},
): Promise<ServerHandle> {
  if (existsSync(socketPath)) {
    await unlink(socketPath);
  }

  const sessions =
    options.sessions ??
    new SessionManager(options.browserFactory ?? defaultBrowserFactory);
  const impl = createDriverService(sessions);

  const handler = connectNodeAdapter({ routes: driverRoutes(impl) });
  const server = http2.createServer(handler);

  await new Promise<void>((resolve, reject) => {
    const onError = (err: Error) => {
      server.removeListener("listening", onListening);
      reject(err);
    };
    const onListening = () => {
      server.removeListener("error", onError);
      resolve();
    };
    server.once("error", onError);
    server.once("listening", onListening);
    server.listen(socketPath);
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
    // Tear down any browsers/contexts before unlinking the socket.
    // A leaked browser process would outlive the adapter — see
    // ADR-0009, decision 2 (no eviction in PR4 means closeAll on
    // shutdown is the only cleanup path).
    await sessions.closeAll().catch((err: unknown) => {
      process.stderr.write(`session teardown error: ${String(err)}\n`);
    });
    if (existsSync(socketPath)) {
      await unlink(socketPath).catch(() => undefined);
    }
  };

  return { socketPath, shutdown };
}
