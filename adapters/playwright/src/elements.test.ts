// SPDX-License-Identifier: Apache-2.0

import type { Locator } from "playwright";
import { describe, expect, it } from "vitest";

import { ElementRegistry } from "./elements.js";

const fakeLocator = (tag: string): Locator => ({ __id: tag }) as unknown as Locator;

describe("ElementRegistry", () => {
  it("allocates a distinct UUID per locator", () => {
    const reg = new ElementRegistry();
    const ids = reg.allocateRefs("s1", [fakeLocator("a"), fakeLocator("b")]);
    expect(ids).toHaveLength(2);
    expect(ids[0]).not.toBe(ids[1]);
    expect(ids[0]).toMatch(
      /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/,
    );
  });

  it("returns the stored locator on a successful lookup", () => {
    const reg = new ElementRegistry();
    const locator = fakeLocator("a");
    const [id] = reg.allocateRefs("s1", [locator]);
    const lookup = reg.lookupRef("s1", id);
    expect(lookup.status).toBe("ok");
    expect(lookup.locator).toBe(locator);
  });

  it("returns 'unknown' for a session that has never allocated", () => {
    const reg = new ElementRegistry();
    expect(reg.lookupRef("ghost", "00000000-0000-0000-0000-000000000000").status).toBe(
      "unknown",
    );
  });

  it("returns 'unknown' for an id that was never issued", () => {
    const reg = new ElementRegistry();
    reg.allocateRefs("s1", [fakeLocator("a")]);
    expect(reg.lookupRef("s1", "deadbeef-dead-beef-dead-beefdeadbeef").status).toBe(
      "unknown",
    );
  });

  it("returns 'stale' for an id whose generation has been bumped", () => {
    const reg = new ElementRegistry();
    const [id] = reg.allocateRefs("s1", [fakeLocator("a")]);
    reg.bumpGeneration("s1");
    const lookup = reg.lookupRef("s1", id);
    expect(lookup.status).toBe("stale");
    expect(lookup.locator).toBeUndefined();
  });

  it("treats refs allocated after bumpGeneration as live again", () => {
    const reg = new ElementRegistry();
    reg.allocateRefs("s1", [fakeLocator("a")]);
    reg.bumpGeneration("s1");
    const [freshId] = reg.allocateRefs("s1", [fakeLocator("b")]);
    expect(reg.lookupRef("s1", freshId).status).toBe("ok");
  });

  it("bumpGeneration is independent across sessions", () => {
    const reg = new ElementRegistry();
    const [idA] = reg.allocateRefs("a", [fakeLocator("x")]);
    const [idB] = reg.allocateRefs("b", [fakeLocator("y")]);
    reg.bumpGeneration("a");
    expect(reg.lookupRef("a", idA).status).toBe("stale");
    expect(reg.lookupRef("b", idB).status).toBe("ok");
  });

  it("forgetSession drops every ref for the session", () => {
    const reg = new ElementRegistry();
    const [id] = reg.allocateRefs("s1", [fakeLocator("a")]);
    reg.forgetSession("s1");
    expect(reg.lookupRef("s1", id).status).toBe("unknown");
  });

  it("currentGeneration starts at zero and advances on bump", () => {
    const reg = new ElementRegistry();
    expect(reg.currentGeneration("s1")).toBe(0);
    reg.allocateRefs("s1", [fakeLocator("a")]);
    expect(reg.currentGeneration("s1")).toBe(0);
    reg.bumpGeneration("s1");
    expect(reg.currentGeneration("s1")).toBe(1);
    reg.bumpGeneration("s1");
    expect(reg.currentGeneration("s1")).toBe(2);
  });

  it("allocateRefs with an empty list is a no-op that returns []", () => {
    const reg = new ElementRegistry();
    expect(reg.allocateRefs("s1", [])).toEqual([]);
  });
});
