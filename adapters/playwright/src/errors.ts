// SPDX-License-Identifier: Apache-2.0
//
// Playwright-failure → DriverError mapping.
//
// `Navigate` (and every future RPC) catches Playwright errors and
// returns a populated `NavigateResponse.error` rather than letting
// the exception propagate as a transport-level failure. The mapping
// table below is the single source of truth for that translation.
//
// The shape of the table is intentionally simple: a sequence of
// rules tried in order against the error's message and constructor
// name. The first match wins; the catch-all collapses to
// `CODE_INTERNAL` so an unmapped Playwright failure never escapes
// as a transport exception. See ADR-0009 for the full table and
// rationale, including the v1alpha1 enum gaps that force several
// rows to share `CODE_INTERNAL`.

import { errors as playwrightErrors } from "playwright";

import { DriverError_Code } from "./proto/spectre/driver/v1alpha1/errors_pb.js";

export interface MappedError {
  code: DriverError_Code;
  message: string;
}

const NETWORK_ERROR_PATTERN = /net::ERR_[A-Z_]+/;
const TARGET_CLOSED_PATTERN =
  /Target (page, context or browser|page|context|browser) (has been )?closed/i;
const BROWSER_MISSING_PATTERN =
  /Executable doesn't exist|playwright install|browserType\.launch/i;

const errorMessage = (err: unknown): string => {
  if (err instanceof Error) {
    return err.message;
  }
  if (typeof err === "string") {
    return err;
  }
  try {
    return JSON.stringify(err);
  } catch {
    return String(err);
  }
};

const errorName = (err: unknown): string => {
  if (err instanceof Error) {
    return err.constructor.name;
  }
  return "";
};

export const playwrightErrorToDriverError = (err: unknown): MappedError => {
  const message = errorMessage(err);
  const name = errorName(err);

  if (err instanceof playwrightErrors.TimeoutError || name === "TimeoutError") {
    return { code: DriverError_Code.TIMEOUT, message };
  }

  if (NETWORK_ERROR_PATTERN.test(message)) {
    return { code: DriverError_Code.TARGET_UNREACHABLE, message };
  }

  if (BROWSER_MISSING_PATTERN.test(message)) {
    return {
      code: DriverError_Code.INTERNAL,
      message: `${message}\nrun \`pnpm exec playwright install chromium\` to install the browser binary`,
    };
  }

  if (TARGET_CLOSED_PATTERN.test(message)) {
    return { code: DriverError_Code.INTERNAL, message };
  }

  return { code: DriverError_Code.INTERNAL, message };
};
