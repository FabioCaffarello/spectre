// SPDX-License-Identifier: Apache-2.0

import { describe, expect, it } from "vitest";

import { ADAPTER_VERSION, PROTOCOL_VERSION, identity } from "./index.js";

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
