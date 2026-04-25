// SPDX-License-Identifier: Apache-2.0
//
// Spectre Playwright driver — gRPC service implementation.
//
// PR3 implements `Initialize` only. The other five RPCs return
// `Code.Unimplemented` so a misconfigured client gets a structured
// gRPC status rather than a hang. See ADR-0008.

import { create } from "@bufbuild/protobuf";
import { Code, ConnectError, type ConnectRouter } from "@connectrpc/connect";
import { connectNodeAdapter } from "@connectrpc/connect-node";
import { randomUUID } from "node:crypto";
import { existsSync } from "node:fs";
import { unlink } from "node:fs/promises";
import * as http2 from "node:http2";

import { CAPABILITY_NAMES, DRIVER_VERSION } from "./capabilities.js";
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
} from "./proto/spectre/driver/v1alpha1/driver_pb.js";
import {
  type ExtractRequest,
  type ExtractResponse,
  ExtractResponseSchema,
  type QueryRequest,
  type QueryResponse,
  QueryResponseSchema,
} from "./proto/spectre/driver/v1alpha1/extraction_pb.js";

export interface DriverServiceImpl {
  initialize(req: InitializeRequest): Promise<InitializeResponse>;
  navigate(req: NavigateRequest): Promise<NavigateResponse>;
  query(req: QueryRequest): Promise<QueryResponse>;
  extract(req: ExtractRequest): Promise<ExtractResponse>;
  screenshot(req: ScreenshotRequest): Promise<ScreenshotResponse>;
  close(req: CloseRequest): Promise<CloseResponse>;
}

const unimplemented = (rpc: string): never => {
  throw new ConnectError(
    `${rpc} is not implemented in spectre-playwright ${DRIVER_VERSION}`,
    Code.Unimplemented,
  );
};

export function createDriverService(): DriverServiceImpl {
  return {
    async initialize(_req: InitializeRequest): Promise<InitializeResponse> {
      const capabilities = create(CapabilitiesSchema, {
        names: [...CAPABILITY_NAMES],
        driverVersion: DRIVER_VERSION,
        runtimeVersion: `node@${process.version}`,
      });
      return create(InitializeResponseSchema, {
        sessionId: randomUUID(),
        capabilities,
      });
    },
    async navigate(_req: NavigateRequest): Promise<NavigateResponse> {
      unimplemented("Navigate");
      return create(NavigateResponseSchema);
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
  };
}

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

export async function startServer(
  socketPath: string,
  impl: DriverServiceImpl = createDriverService(),
): Promise<ServerHandle> {
  if (existsSync(socketPath)) {
    await unlink(socketPath);
  }

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
    if (existsSync(socketPath)) {
      await unlink(socketPath).catch(() => undefined);
    }
  };

  return { socketPath, shutdown };
}
