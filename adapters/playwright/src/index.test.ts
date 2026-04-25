// SPDX-License-Identifier: Apache-2.0

import { create } from "@bufbuild/protobuf";
import { Code, ConnectError } from "@connectrpc/connect";
import { describe, expect, it } from "vitest";

import { CAPABILITY_NAMES, DRIVER_VERSION } from "./capabilities.js";
import {
  ADAPTER_VERSION,
  PROTOCOL_VERSION,
  identity,
  resolveSocketPath,
} from "./index.js";
import { InitializeRequestSchema } from "./proto/spectre/driver/v1alpha1/driver_pb.js";
import { createDriverService } from "./server.js";

describe("identity", () => {
  it("contains the protocol version", () => {
    expect(identity()).toContain(PROTOCOL_VERSION);
  });

  it("contains the adapter version", () => {
    expect(identity()).toContain(ADAPTER_VERSION);
  });

  it("targets the v1alpha1 protocol path", () => {
    expect(PROTOCOL_VERSION).toBe("spectre.driver.v1alpha1");
  });
});

describe("resolveSocketPath", () => {
  it("prefers --socket over the env var", () => {
    expect(
      resolveSocketPath(["--socket=/tmp/a.sock"], {
        SPECTRE_DRIVER_SOCKET: "/tmp/b.sock",
      }),
    ).toBe("/tmp/a.sock");
  });

  it("falls back to SPECTRE_DRIVER_SOCKET", () => {
    expect(
      resolveSocketPath([], { SPECTRE_DRIVER_SOCKET: "/tmp/c.sock" }),
    ).toBe("/tmp/c.sock");
  });

  it("rejects relative paths", () => {
    expect(() => resolveSocketPath(["--socket=relative.sock"], {})).toThrow(
      /absolute/,
    );
  });

  it("errors when no source is set", () => {
    expect(() => resolveSocketPath([], {})).toThrow(/no socket path/);
  });
});

describe("createDriverService", () => {
  const service = createDriverService();

  it("Initialize returns a session id and the declared capabilities", async () => {
    const response = await service.initialize(
      create(InitializeRequestSchema, {
        protocolVersion: PROTOCOL_VERSION,
      }),
    );

    expect(response.sessionId).toMatch(
      /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/,
    );
    expect(response.capabilities?.names).toEqual([...CAPABILITY_NAMES]);
    expect(response.capabilities?.driverVersion).toBe(DRIVER_VERSION);
    expect(response.capabilities?.runtimeVersion).toMatch(/^node@/);
    expect(response.error).toBeUndefined();
  });

  it.each(["navigate", "query", "extract", "screenshot", "close"] as const)(
    "%s returns Code.Unimplemented",
    async (rpc) => {
      const handler = service[rpc] as (req: unknown) => Promise<unknown>;
      await expect(handler({} as never)).rejects.toMatchObject({
        code: Code.Unimplemented,
      });
      await expect(handler({} as never)).rejects.toBeInstanceOf(ConnectError);
    },
  );
});
