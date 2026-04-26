// SPDX-License-Identifier: Apache-2.0

import { describe, expect, it } from "vitest";

import {
  CAPABILITY_NAMES,
  EXTRACT_EVAL,
  JS_EXECUTION,
  assertCapabilityCoherence,
  hasCapability,
} from "./capabilities.js";

describe("CAPABILITY_NAMES", () => {
  it("declares the eleven PR5 capabilities", () => {
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
