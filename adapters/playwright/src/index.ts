// SPDX-License-Identifier: Apache-2.0
//
// Spectre Playwright driver adapter — entry point.
//
// Resolves a TCP port from `SPECTRE_ADAPTER_GRPC_PORT`, a Redis URL
// from `SPECTRE_REDIS_URL`, and (optionally — production never sets
// it) the `SPECTRE_ADAPTER_INSTANCE_ID` override used by the
// restart-invalidation conformance test. Pings Redis on startup
// (ADR-0023 §6 — Redis required) and exits non-zero on failure so
// `docker compose --depends_on.condition: service_healthy` and
// equivalent Helm readiness gates surface the dependency cleanly.
// Then starts the gRPC service on `0.0.0.0:<port>`, registers the
// gRPC standard health check (ADR-0021 §6), and shuts down on
// SIGTERM/SIGINT.
//
// The wire-level driver protocol contract is unchanged — see
// ADR-0008 for the original handshake design, ADR-0022 for the TCP
// transport contract, and ADR-0023 §4/§5 for the Redis keyspace and
// the restart-invalidation mechanism this adapter participates in.

import { fileURLToPath } from "node:url";
import { resolve as resolvePath } from "node:path";
import { randomUUID } from "node:crypto";

import { createLogger, setLogger } from "./logging.js";
import { file_spectre_driver_v1alpha1_driver } from "./proto/spectre/driver/v1alpha1/driver_pb.js";
import { RedisClient } from "./redis.js";
import { defaultBrowserFactory, startServer } from "./server.js";
import { SessionManager } from "./sessions.js";
import { initTelemetry, resolveMetricsPort } from "./telemetry.js";

export const PROTOCOL_VERSION: string =
  file_spectre_driver_v1alpha1_driver.proto.package;
export const ADAPTER_VERSION = "0.1.0-alpha.0";

export const PORT_ENV_VAR = "SPECTRE_ADAPTER_GRPC_PORT";
export const REDIS_URL_ENV_VAR = "SPECTRE_REDIS_URL";
export const INSTANCE_ID_ENV_VAR = "SPECTRE_ADAPTER_INSTANCE_ID";

// Local-dev default; ADR-0023 §9 (Compose) and the local justfile
// both surface this URL via `.env.example`. Production deployments
// must always set the env var explicitly — defaulting to localhost
// here is purely a developer convenience.
const DEFAULT_REDIS_URL = "redis://127.0.0.1:6379/0";

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

export function resolveRedisUrl(env: NodeJS.ProcessEnv): string {
  const raw = env[REDIS_URL_ENV_VAR];
  if (raw === undefined || raw === "") {
    return DEFAULT_REDIS_URL;
  }
  return raw;
}

// Returns the value of `SPECTRE_ADAPTER_INSTANCE_ID` if set (test
// hook only — see ADR-0023 §5 R4.3 addendum and the phase prompt
// §4.1) or a freshly generated UUID per process start. The
// generated value is the §5 restart-invalidation key: a Pod or
// container restart yields a new UUID; sessions written by the
// previous incarnation become foreign-instance and the next RPC
// against them returns gRPC `UNAVAILABLE`.
export function resolveInstanceId(env: NodeJS.ProcessEnv): string {
  const raw = env[INSTANCE_ID_ENV_VAR];
  if (raw !== undefined && raw !== "") {
    return raw;
  }
  return randomUUID();
}

async function main(): Promise<void> {
  // W3.2 Cluster A: configure Pino JSON stdout + init OTel SDK
  // BEFORE any other startup work so the redis-dial + sweep
  // messages emit in the canonical ADR-0031 §3.4 schema. The
  // PrometheusExporter self-hosts the `/metrics` HTTP server on
  // `SPECTRE_METRICS_PORT` (default 9090 per ADR-0031 §3.3).
  const log = createLogger(ADAPTER_VERSION);
  setLogger(log);
  const telemetry = await initTelemetry({
    serviceVersion: ADAPTER_VERSION,
    metricsPort: resolveMetricsPort(process.env),
  });
  log.info(
    { metrics_port: resolveMetricsPort(process.env) },
    "telemetry ready",
  );

  const port = resolvePort(process.env);
  const redisUrl = resolveRedisUrl(process.env);
  const instanceId = resolveInstanceId(process.env);

  const redis = RedisClient.fromUrl(redisUrl);
  try {
    await redis.ping();
  } catch (err) {
    log.error(
      {
        redis_url: redisUrl,
        error: err instanceof Error ? err.message : String(err),
      },
      "redis ping failed",
    );
    process.exit(1);
  }
  log.info(
    { redis_url: redisUrl, adapter_instance_id: instanceId },
    "redis ready",
  );

  const sessions = new SessionManager({
    factory: defaultBrowserFactory,
    redis,
    instanceId,
  });

  const handle = await startServer(port, {
    sessions,
    metrics: telemetry.metrics,
  });

  log.info(
    {
      binary: "spectre-playwright",
      version: ADAPTER_VERSION,
      protocol: PROTOCOL_VERSION,
      grpc_port: handle.port,
    },
    "adapter listening",
  );

  let shuttingDown = false;
  const shutdown = (signal: NodeJS.Signals): void => {
    if (shuttingDown) return;
    shuttingDown = true;
    log.info({ signal }, "shutting down");
    handle
      .shutdown()
      .then(() => redis.disconnect())
      .then(() => telemetry.shutdown())
      .then(() => process.exit(0))
      .catch((err: unknown) => {
        log.error({ error: String(err) }, "shutdown error");
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
