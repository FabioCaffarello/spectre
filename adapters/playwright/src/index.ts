// SPDX-License-Identifier: Apache-2.0
//
// Spectre Playwright driver adapter — entry point.
//
// Resolves a Unix domain socket path (CLI flag > env var > error),
// starts the gRPC service, prints a single readiness line on stdout,
// and shuts down cleanly on SIGTERM or SIGINT. See ADR-0008 for the
// full lifecycle contract.

import { isAbsolute, resolve as resolvePath } from "node:path";
import { fileURLToPath } from "node:url";
import { parseArgs } from "node:util";

import { file_spectre_driver_v1alpha1_driver } from "./proto/spectre/driver/v1alpha1/driver_pb.js";
import { startServer } from "./server.js";

export const PROTOCOL_VERSION: string =
  file_spectre_driver_v1alpha1_driver.proto.package;
export const ADAPTER_VERSION = "0.1.0-alpha.0";

export function identity(): string {
  return `spectre-playwright ${ADAPTER_VERSION} (driver protocol ${PROTOCOL_VERSION})`;
}

export function resolveSocketPath(
  argv: readonly string[],
  env: NodeJS.ProcessEnv,
): string {
  const { values } = parseArgs({
    args: [...argv],
    options: {
      socket: { type: "string" },
    },
    strict: true,
  });

  const candidate = values.socket ?? env.SPECTRE_DRIVER_SOCKET;
  if (!candidate) {
    throw new Error(
      "no socket path provided: pass --socket=<absolute-path> or set SPECTRE_DRIVER_SOCKET",
    );
  }
  if (!isAbsolute(candidate)) {
    throw new Error(
      `socket path must be absolute, got ${JSON.stringify(candidate)}`,
    );
  }
  return resolvePath(candidate);
}

async function main(): Promise<void> {
  const socketPath = resolveSocketPath(process.argv.slice(2), process.env);
  const handle = await startServer(socketPath);

  process.stdout.write(`ready unix:${handle.socketPath}\n`);
  process.stderr.write(
    `${identity()} listening on unix:${handle.socketPath}\n`,
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
