// SPDX-License-Identifier: Apache-2.0

import { create } from "@bufbuild/protobuf";
import { Code, ConnectError } from "@connectrpc/connect";
import type { Browser } from "playwright";
import { describe, expect, it, vi } from "vitest";

import { CAPABILITY_NAMES, DRIVER_VERSION } from "./capabilities.js";
import {
  ADAPTER_VERSION,
  PROTOCOL_VERSION,
  identity,
  resolveSocketPath,
} from "./index.js";
import { InitializeRequestSchema } from "./proto/spectre/driver/v1alpha1/driver_pb.js";
import { DriverError_Code } from "./proto/spectre/driver/v1alpha1/errors_pb.js";
import { createDriverService } from "./server.js";
import { SessionManager } from "./sessions.js";

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
  const browserFactory = vi.fn(async (): Promise<Browser> => {
    throw new Error("browser factory should not be invoked in unit tests");
  });
  const newService = () =>
    createDriverService(new SessionManager(browserFactory));

  it("Initialize returns a session id, registers it, and returns the declared capabilities", async () => {
    const sessions = new SessionManager(browserFactory);
    const service = createDriverService(sessions);
    const response = await service.initialize(
      create(InitializeRequestSchema, {
        protocolVersion: PROTOCOL_VERSION,
      }),
    );

    expect(response.sessionId).toMatch(
      /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/,
    );
    expect(sessions.has(response.sessionId)).toBe(true);
    expect(response.capabilities?.names).toEqual([...CAPABILITY_NAMES]);
    expect(response.capabilities?.driverVersion).toBe(DRIVER_VERSION);
    expect(response.capabilities?.runtimeVersion).toMatch(/^node@/);
    expect(response.error).toBeUndefined();
  });

  it("Navigate returns CODE_INVALID_ARGUMENT for an unknown session id without launching the browser", async () => {
    const factory = vi.fn(async (): Promise<Browser> => {
      throw new Error("must not launch");
    });
    const service = createDriverService(new SessionManager(factory));

    const response = await service.navigate({
      $typeName: "spectre.driver.v1alpha1.NavigateRequest",
      sessionId: "does-not-exist",
      url: "http://127.0.0.1/",
      wait: 0,
    } as never);

    expect(response.error?.code).toBe(DriverError_Code.INVALID_ARGUMENT);
    expect(response.error?.message).toMatch(/unknown session_id/);
    expect(factory).not.toHaveBeenCalled();
  });

  it("Navigate returns CODE_INVALID_ARGUMENT for a missing url", async () => {
    const sessions = new SessionManager(browserFactory);
    const service = createDriverService(sessions);
    const init = await service.initialize(
      create(InitializeRequestSchema, { protocolVersion: PROTOCOL_VERSION }),
    );

    const response = await service.navigate({
      $typeName: "spectre.driver.v1alpha1.NavigateRequest",
      sessionId: init.sessionId,
      url: "",
      wait: 0,
    } as never);

    expect(response.error?.code).toBe(DriverError_Code.INVALID_ARGUMENT);
    expect(response.error?.message).toMatch(/url is required/);
  });

  it("Navigate returns CODE_INVALID_ARGUMENT for a non-http(s) url", async () => {
    const sessions = new SessionManager(browserFactory);
    const service = createDriverService(sessions);
    const init = await service.initialize(
      create(InitializeRequestSchema, { protocolVersion: PROTOCOL_VERSION }),
    );

    const response = await service.navigate({
      $typeName: "spectre.driver.v1alpha1.NavigateRequest",
      sessionId: init.sessionId,
      url: "ftp://example.com/",
      wait: 0,
    } as never);

    expect(response.error?.code).toBe(DriverError_Code.INVALID_ARGUMENT);
    expect(response.error?.message).toMatch(/http\(s\)/);
  });

  it.each(["query", "extract", "screenshot", "close"] as const)(
    "%s returns Code.Unimplemented",
    async (rpc) => {
      const service = newService();
      const handler = service[rpc] as (req: unknown) => Promise<unknown>;
      await expect(handler({} as never)).rejects.toMatchObject({
        code: Code.Unimplemented,
      });
      await expect(handler({} as never)).rejects.toBeInstanceOf(ConnectError);
    },
  );
});
