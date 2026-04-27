// SPDX-License-Identifier: Apache-2.0

import { create } from "@bufbuild/protobuf";
import type { Browser, BrowserContext, Page } from "playwright";
import { describe, expect, it, vi } from "vitest";

import { CAPABILITY_NAMES, DRIVER_VERSION } from "./capabilities.js";
import {
  ADAPTER_VERSION,
  PORT_ENV_VAR,
  PROTOCOL_VERSION,
  identity,
  resolvePort,
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

describe("resolvePort", () => {
  it("reads SPECTRE_ADAPTER_GRPC_PORT", () => {
    expect(resolvePort({ [PORT_ENV_VAR]: "9091" })).toBe(9091);
  });

  it("accepts port 0 for kernel-assigned binding", () => {
    expect(resolvePort({ [PORT_ENV_VAR]: "0" })).toBe(0);
  });

  it("rejects a missing env var", () => {
    expect(() => resolvePort({})).toThrow(new RegExp(PORT_ENV_VAR));
  });

  it("rejects an empty env var", () => {
    expect(() => resolvePort({ [PORT_ENV_VAR]: "" })).toThrow(
      new RegExp(PORT_ENV_VAR),
    );
  });

  it("rejects a non-integer value", () => {
    expect(() => resolvePort({ [PORT_ENV_VAR]: "abc" })).toThrow(/port number/);
  });

  it("rejects an out-of-range port", () => {
    expect(() => resolvePort({ [PORT_ENV_VAR]: "70000" })).toThrow(
      /between 0 and 65535/,
    );
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

  it("screenshot returns CODE_INVALID_ARGUMENT for an unknown session id", async () => {
    const service = newService();
    const response = await service.screenshot({
      $typeName: "spectre.driver.v1alpha1.ScreenshotRequest",
      sessionId: "ghost",
      scope: 1,
      format: 1,
    } as never);
    expect(response.error?.code).toBe(DriverError_Code.INVALID_ARGUMENT);
    expect(response.error?.message).toMatch(/unknown session_id/);
  });

  it("screenshot returns CODE_INVALID_ARGUMENT for SCREENSHOT_SCOPE_UNSPECIFIED", async () => {
    const sessions = new SessionManager(browserFactory);
    const service = createDriverService(sessions);
    const init = await service.initialize(
      create(InitializeRequestSchema, { protocolVersion: PROTOCOL_VERSION }),
    );
    const response = await service.screenshot({
      $typeName: "spectre.driver.v1alpha1.ScreenshotRequest",
      sessionId: init.sessionId,
      scope: 0,
      format: 1,
    } as never);
    expect(response.error?.code).toBe(DriverError_Code.INVALID_ARGUMENT);
    expect(response.error?.message).toMatch(/SCREENSHOT_SCOPE_UNSPECIFIED/);
  });

  it("screenshot returns CODE_INVALID_ARGUMENT for ELEMENT scope without an element", async () => {
    const sessions = new SessionManager(browserFactory);
    const service = createDriverService(sessions);
    const init = await service.initialize(
      create(InitializeRequestSchema, { protocolVersion: PROTOCOL_VERSION }),
    );
    const response = await service.screenshot({
      $typeName: "spectre.driver.v1alpha1.ScreenshotRequest",
      sessionId: init.sessionId,
      scope: 3,
      format: 1,
    } as never);
    expect(response.error?.code).toBe(DriverError_Code.INVALID_ARGUMENT);
    expect(response.error?.message).toBe(
      "element is required when scope is SCREENSHOT_SCOPE_ELEMENT",
    );
  });

  it("screenshot does not bump the session generation (read-only contract — ADR-0011 decision 4)", async () => {
    // A fake browser whose page returns a fixed PNG buffer from
    // `screenshot()`. Just enough surface for the handler to walk
    // through the VIEWPORT path without a real Chromium.
    const PNG_HEADER = Buffer.from([0x89, 0x50, 0x4e, 0x47]);
    const fakePage = {
      screenshot: vi.fn(async () => PNG_HEADER),
    } as unknown as Page;
    const fakeContext = {
      newPage: vi.fn(async () => fakePage),
      close: vi.fn(async () => undefined),
    } as unknown as BrowserContext;
    const fakeBrowser = {
      newContext: vi.fn(async () => fakeContext),
      close: vi.fn(async () => undefined),
    } as unknown as Browser;
    const sessions = new SessionManager(async () => fakeBrowser);
    const service = createDriverService(sessions);

    const init = await service.initialize(
      create(InitializeRequestSchema, { protocolVersion: PROTOCOL_VERSION }),
    );
    // Mimic a post-Navigate state: the page is open and the
    // generation counter has been bumped to 1. Screenshot must not
    // touch either.
    await sessions.getOrCreatePage(init.sessionId);
    sessions.bumpGeneration(init.sessionId);
    const generationBefore = sessions.currentGeneration(init.sessionId);
    expect(generationBefore).toBe(1);

    const response = await service.screenshot({
      $typeName: "spectre.driver.v1alpha1.ScreenshotRequest",
      sessionId: init.sessionId,
      scope: 1,
      format: 1,
    } as never);

    expect(response.error?.code).toBeFalsy();
    expect(response.contentType).toBe("image/png");
    expect(response.image.byteLength).toBeGreaterThan(0);
    expect(sessions.currentGeneration(init.sessionId)).toBe(generationBefore);
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
      element: {
        $typeName: "spectre.driver.v1alpha1.ElementRef",
        opaqueId: "",
      },
      fields: [],
    } as never);
    expect(response.error?.code).toBe(DriverError_Code.INVALID_ARGUMENT);
  });
});
