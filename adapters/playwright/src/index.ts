// SPDX-License-Identifier: Apache-2.0
//
// Spectre Playwright driver adapter — entry point.
//
// Resolves a TCP port from the SPECTRE_ADAPTER_GRPC_PORT env var
// (ADR-0021 §4 — production default 9091, harness allocates a free
// port at test time), starts the gRPC service on 0.0.0.0:<port>,
// registers the gRPC standard health check, and shuts down cleanly
// on SIGTERM or SIGINT. R2.2 retired the prior Unix-domain-socket
// transport; readiness is now signalled by Health.Check responding
// SERVING (ADR-0021 §6). The wire-level driver protocol contract is
// unchanged — see ADR-0008 for the original handshake design and
// ADR-0022 for the TCP transport contract.

import { fileURLToPath } from "node:url";
import { resolve as resolvePath } from "node:path";

import { file_spectre_driver_v1alpha1_driver } from "./proto/spectre/driver/v1alpha1/driver_pb.js";
import { startServer } from "./server.js";

export const PROTOCOL_VERSION: string =
  file_spectre_driver_v1alpha1_driver.proto.package;
export const ADAPTER_VERSION = "0.1.0-alpha.0";

export const PORT_ENV_VAR = "SPECTRE_ADAPTER_GRPC_PORT";

export function identity(): string {
  return `spectre-playwright ${ADAPTER_VERSION} (driver protocol ${PROTOCOL_VERSION})`;
}

export function resolvePort(env: NodeJS.ProcessEnv): number {
  const raw = env[PORT_ENV_VAR];
  if (raw === undefined || raw === "") {
    throw new Error(
      `${PORT_ENV_VAR} is required: set it to the TCP port the adapter should bind`,
    );
  }
  const port = Number.parseInt(raw, 10);
  if (!Number.isInteger(port) || String(port) !== raw.trim()) {
    throw new Error(
      `${PORT_ENV_VAR} must be a port number, got ${JSON.stringify(raw)}`,
    );
  }
  if (port < 0 || port > 65535) {
    throw new Error(`${PORT_ENV_VAR} must be between 0 and 65535, got ${port}`);
  }
  return port;
}

async function main(): Promise<void> {
  const port = resolvePort(process.env);
  const handle = await startServer(port);

  process.stderr.write(
    `${identity()} listening on ${handle.host}:${handle.port}\n`,
  );

  let shuttingDown = false;
  const shutdown = (signal: NodeJS.Signals): void => {
    if (shuttingDown) return;
    shuttingDown = true;
    process.stderr.write(`received ${signal}, shutting down\n`);
    handle
      .shutdown()
      .then(() => process.exit(0))
      .catch((err: unknown) => {
        process.stderr.write(`shutdown error: ${String(err)}\n`);
        process.exit(1);
      });
  };
  process.on("SIGTERM", shutdown);
  process.on("SIGINT", shutdown);
}

const entryArg = process.argv[1];
const isEntryPoint =
  entryArg !== undefined &&
  fileURLToPath(import.meta.url) === resolvePath(entryArg);

if (isEntryPoint) {
  main().catch((err: unknown) => {
    process.stderr.write(
      `fatal: ${String(err instanceof Error ? err.message : err)}\n`,
    );
    process.exit(1);
  });
}
