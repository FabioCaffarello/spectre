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

  it("screenshot still returns Code.Unimplemented", async () => {
    const service = newService();
    await expect(
      service.screenshot({} as never),
    ).rejects.toMatchObject({ code: Code.Unimplemented });
    await expect(
      service.screenshot({} as never),
    ).rejects.toBeInstanceOf(ConnectError);
  });

  it("close returns CODE_INVALID_ARGUMENT for an unknown session id", async () => {
    const service = newService();
    const response = await service.close({
      $typeName: "spectre.driver.v1alpha1.CloseRequest",
      sessionId: "ghost",
    } as never);
    expect(response.error?.code).toBe(DriverError_Code.INVALID_ARGUMENT);
    expect(response.error?.message).toMatch(/unknown session_id/);
  });

  it("query returns CODE_INVALID_ARGUMENT for SELECTOR_KIND_UNSPECIFIED", async () => {
    const sessions = new SessionManager(browserFactory);
    const service = createDriverService(sessions);
    const init = await service.initialize(
      create(InitializeRequestSchema, { protocolVersion: PROTOCOL_VERSION }),
    );
    const response = await service.query({
      $typeName: "spectre.driver.v1alpha1.QueryRequest",
      sessionId: init.sessionId,
      selector: "body",
      kind: 0,
      limit: 0,
    } as never);
    expect(response.error?.code).toBe(DriverError_Code.INVALID_ARGUMENT);
    expect(response.error?.message).toMatch(/SELECTOR_KIND_UNSPECIFIED/);
  });

  it("extract returns CODE_INVALID_ARGUMENT for an empty opaque_id", async () => {
    const sessions = new SessionManager(browserFactory);
    const service = createDriverService(sessions);
    const init = await service.initialize(
      create(InitializeRequestSchema, { protocolVersion: PROTOCOL_VERSION }),
    );
    const response = await service.extract({
      $typeName: "spectre.driver.v1alpha1.ExtractRequest",
      sessionId: init.sessionId,
      element: { $typeName: "spectre.driver.v1alpha1.ElementRef", opaqueId: "" },
      fields: [],
    } as never);
    expect(response.error?.code).toBe(DriverError_Code.INVALID_ARGUMENT);
  });

});
