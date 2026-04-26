// SPDX-License-Identifier: Apache-2.0
//
// Spectre Playwright driver — gRPC service implementation.
//
// PR4 implements `Initialize` and `Navigate`. The remaining four
// RPCs (`Query`, `Extract`, `Screenshot`, `Close`) return
// `Code.Unimplemented` so a misconfigured client gets a structured
// gRPC status rather than a hang. See ADR-0008 (handshake) and
// ADR-0009 (Navigate, session lifecycle, error mapping).

import { create } from "@bufbuild/protobuf";
import { DurationSchema } from "@bufbuild/protobuf/wkt";
import { Code, ConnectError, type ConnectRouter } from "@connectrpc/connect";
import { connectNodeAdapter } from "@connectrpc/connect-node";
import { randomUUID } from "node:crypto";
import { existsSync } from "node:fs";
import { unlink } from "node:fs/promises";
import * as http2 from "node:http2";
import { chromium } from "playwright";

import { CAPABILITY_NAMES, DRIVER_VERSION } from "./capabilities.js";
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
  type QueryRequest,
  type QueryResponse,
  QueryResponseSchema,
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
  async query(_req: QueryRequest): Promise<QueryResponse> {
    unimplemented("Query");
    return create(QueryResponseSchema);
  },
  async extract(_req: ExtractRequest): Promise<ExtractResponse> {
    unimplemented("Extract");
    return create(ExtractResponseSchema);
  },
  async screenshot(_req: ScreenshotRequest): Promise<ScreenshotResponse> {
    unimplemented("Screenshot");
    return create(ScreenshotResponseSchema);
  },
  async close(_req: CloseRequest): Promise<CloseResponse> {
    unimplemented("Close");
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
