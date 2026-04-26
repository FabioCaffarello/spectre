// SPDX-License-Identifier: Apache-2.0

import { errors as playwrightErrors } from "playwright";
import { describe, expect, it } from "vitest";

import { playwrightErrorToDriverError } from "./errors.js";
import { DriverError_Code } from "./proto/spectre/driver/v1alpha1/errors_pb.js";

describe("playwrightErrorToDriverError", () => {
  it("maps Playwright TimeoutError to CODE_TIMEOUT", () => {
    const err = new playwrightErrors.TimeoutError(
      "page.goto: Timeout 30000ms exceeded",
    );
    const mapped = playwrightErrorToDriverError(err);
    expect(mapped.code).toBe(DriverError_Code.TIMEOUT);
    expect(mapped.message).toContain("Timeout");
  });

  it("maps net::ERR_NAME_NOT_RESOLVED to CODE_TARGET_UNREACHABLE", () => {
    const err = new Error(
      "page.goto: net::ERR_NAME_NOT_RESOLVED at https://nope.invalid/",
    );
    const mapped = playwrightErrorToDriverError(err);
    expect(mapped.code).toBe(DriverError_Code.TARGET_UNREACHABLE);
  });

  it("maps net::ERR_CONNECTION_REFUSED to CODE_TARGET_UNREACHABLE", () => {
    const err = new Error(
      "page.goto: net::ERR_CONNECTION_REFUSED at http://127.0.0.1:1/",
    );
    expect(playwrightErrorToDriverError(err).code).toBe(
      DriverError_Code.TARGET_UNREACHABLE,
    );
  });

  it("maps a missing Chromium binary to CODE_INTERNAL with an actionable hint", () => {
    const err = new Error(
      "browserType.launch: Executable doesn't exist at /path/to/headless_shell",
    );
    const mapped = playwrightErrorToDriverError(err);
    expect(mapped.code).toBe(DriverError_Code.INTERNAL);
    expect(mapped.message).toMatch(/playwright install chromium/);
  });

  it("maps a closed-target error to CODE_INTERNAL", () => {
    const err = new Error(
      "page.goto: Target page, context or browser has been closed",
    );
    expect(playwrightErrorToDriverError(err).code).toBe(
      DriverError_Code.INTERNAL,
    );
  });

  it("falls through to CODE_INTERNAL for unrecognised errors and preserves the message", () => {
    const err = new Error("something bizarre happened");
    const mapped = playwrightErrorToDriverError(err);
    expect(mapped.code).toBe(DriverError_Code.INTERNAL);
    expect(mapped.message).toBe("something bizarre happened");
  });

  it("handles non-Error values without throwing", () => {
    expect(playwrightErrorToDriverError("string failure").code).toBe(
      DriverError_Code.INTERNAL,
    );
    expect(playwrightErrorToDriverError({ weird: 1 }).code).toBe(
      DriverError_Code.INTERNAL,
    );
    expect(playwrightErrorToDriverError(undefined).code).toBe(
      DriverError_Code.INTERNAL,
    );
  });
});
