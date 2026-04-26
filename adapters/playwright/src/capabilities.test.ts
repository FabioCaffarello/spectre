// SPDX-License-Identifier: Apache-2.0

import { describe, expect, it } from "vitest";

import {
  CAPABILITY_NAMES,
  EXTRACT_EVAL,
  JS_EXECUTION,
  MODE_EVAL,
  assertCapabilityCoherence,
  hasCapability,
  missingCapabilityForMode,
} from "./capabilities.js";

describe("CAPABILITY_NAMES", () => {
  it("declares the thirteen PR6 capabilities", () => {
    expect(CAPABILITY_NAMES).toEqual([
      "extract_attribute",
      "extract_eval",
      "extract_html",
      "extract_text",
      "js_execution",
      "navigation",
      "query_attribute",
      "query_css",
      "query_text",
      "query_xpath",
      "screenshot_element",
      "screenshot_full_page",
      "screenshot_viewport",
    ]);
  });

  it("is alphabetically sorted", () => {
    const sorted = [...CAPABILITY_NAMES].sort();
    expect([...CAPABILITY_NAMES]).toEqual(sorted);
  });

  it("contains no duplicates", () => {
    expect(new Set(CAPABILITY_NAMES).size).toBe(CAPABILITY_NAMES.length);
  });
});

describe("assertCapabilityCoherence", () => {
  it("accepts the canonical capability list", () => {
    expect(() => assertCapabilityCoherence(CAPABILITY_NAMES)).not.toThrow();
  });

  it("rejects extract_eval without js_execution", () => {
    const broken = CAPABILITY_NAMES.filter((c) => c !== JS_EXECUTION);
    expect(broken).toContain(EXTRACT_EVAL);
    expect(broken).not.toContain(JS_EXECUTION);
    expect(() => assertCapabilityCoherence(broken)).toThrow(
      /extract_eval requires js_execution/,
    );
  });

  it("accepts extract_eval together with js_execution", () => {
    expect(() =>
      assertCapabilityCoherence([EXTRACT_EVAL, JS_EXECUTION]),
    ).not.toThrow();
  });

  it("accepts neither extract_eval nor js_execution", () => {
    expect(() => assertCapabilityCoherence(["navigation"])).not.toThrow();
  });
});

describe("hasCapability", () => {
  it("reports true when the name is in the list", () => {
    expect(hasCapability(CAPABILITY_NAMES, JS_EXECUTION)).toBe(true);
  });

  it("reports false when the name is absent", () => {
    expect(hasCapability(CAPABILITY_NAMES, "no_such_cap")).toBe(false);
  });
});

describe("missingCapabilityForMode", () => {
  // The MODE_* constants in extraction_pb.ts mirror the proto:
  // UNSPECIFIED=0, TEXT_CONTENT=1, INNER_TEXT=2, INNER_HTML=3,
  // OUTER_HTML=4, ATTR=5, EVAL=6.
  it("returns js_execution when MODE_EVAL is requested but js_execution is missing", () => {
    expect(missingCapabilityForMode(MODE_EVAL, ["navigation"])).toBe(
      JS_EXECUTION,
    );
  });

  it("returns null when MODE_EVAL is requested and js_execution is declared", () => {
    expect(missingCapabilityForMode(MODE_EVAL, CAPABILITY_NAMES)).toBeNull();
  });

  it("returns null for non-EVAL modes regardless of js_execution", () => {
    // MODE_TEXT_CONTENT = 1.
    expect(missingCapabilityForMode(1, [])).toBeNull();
    // MODE_ATTR = 5.
    expect(missingCapabilityForMode(5, ["navigation"])).toBeNull();
  });

  it("MODE_EVAL is the integer the proto enum encodes", () => {
    expect(MODE_EVAL).toBe(6);
  });
});
