// SPDX-License-Identifier: Apache-2.0

import type { Browser, BrowserContext, Locator, Page } from "playwright";
import { describe, expect, it, vi } from "vitest";

import { SessionManager, UnknownSessionError } from "./sessions.js";

const fakeLocator = (tag: string): Locator => ({ __id: tag }) as unknown as Locator;

interface FakeBrowser extends Browser {
  contexts(): BrowserContext[];
  closed: boolean;
}

const makeFakeBrowser = (): FakeBrowser => {
  const contexts: BrowserContext[] = [];
  const browser = {
    closed: false,
    contexts: () => contexts,
    newContext: vi.fn(async (): Promise<BrowserContext> => {
      const pages: Page[] = [];
      const context = {
        newPage: vi.fn(async (): Promise<Page> => {
          const page = { __id: pages.length } as unknown as Page;
          pages.push(page);
          return page;
        }),
        close: vi.fn(async (): Promise<void> => {
          (context as unknown as { closed: boolean }).closed = true;
        }),
      } as unknown as BrowserContext;
      contexts.push(context);
      return context;
    }),
    close: vi.fn(async (): Promise<void> => {
      browser.closed = true;
    }),
  } as unknown as FakeBrowser;
  return browser;
};

describe("SessionManager", () => {
  it("register followed by getOrCreatePage launches the browser lazily", async () => {
    const browser = makeFakeBrowser();
    const factory = vi.fn(async () => browser);
    const mgr = new SessionManager(factory);

    mgr.register("s1");
    expect(factory).not.toHaveBeenCalled();
    expect(mgr.has("s1")).toBe(true);

    await mgr.getOrCreatePage("s1");
    expect(factory).toHaveBeenCalledTimes(1);
    expect(browser.contexts()).toHaveLength(1);
  });

  it("rejects an unregistered session id with UnknownSessionError", async () => {
    const browser = makeFakeBrowser();
    const factory = vi.fn(async () => browser);
    const mgr = new SessionManager(factory);

    expect(mgr.has("ghost")).toBe(false);
    await expect(mgr.getOrCreatePage("ghost")).rejects.toBeInstanceOf(
      UnknownSessionError,
    );
    // Browser must not be launched for an unknown session.
    expect(factory).not.toHaveBeenCalled();
  });

  it("reuses the same Page for repeat calls with the same session id", async () => {
    const browser = makeFakeBrowser();
    const mgr = new SessionManager(async () => browser);

    mgr.register("s1");
    const first = await mgr.getOrCreatePage("s1");
    const second = await mgr.getOrCreatePage("s1");

    expect(second).toBe(first);
    expect(browser.contexts()).toHaveLength(1);
  });

  it("allocates a separate context per distinct session id", async () => {
    const browser = makeFakeBrowser();
    const mgr = new SessionManager(async () => browser);

    mgr.register("a");
    mgr.register("b");
    const a = await mgr.getOrCreatePage("a");
    const b = await mgr.getOrCreatePage("b");

    expect(a).not.toBe(b);
    expect(browser.contexts()).toHaveLength(2);
  });

  it("does not relaunch the browser across multiple sessions", async () => {
    const browser = makeFakeBrowser();
    const factory = vi.fn(async () => browser);
    const mgr = new SessionManager(factory);

    mgr.register("a");
    mgr.register("b");
    mgr.register("c");
    await mgr.getOrCreatePage("a");
    await mgr.getOrCreatePage("b");
    await mgr.getOrCreatePage("c");

    expect(factory).toHaveBeenCalledTimes(1);
  });

  it("closeAll closes every context, the browser, and clears registrations", async () => {
    const browser = makeFakeBrowser();
    const mgr = new SessionManager(async () => browser);

    mgr.register("a");
    mgr.register("b");
    await mgr.getOrCreatePage("a");
    await mgr.getOrCreatePage("b");

    await mgr.closeAll();

    for (const context of browser.contexts()) {
      expect(context.close).toHaveBeenCalledTimes(1);
    }
    expect(browser.close).toHaveBeenCalledTimes(1);
    expect(mgr.has("a")).toBe(false);
    expect(mgr.has("b")).toBe(false);
  });

  it("closeAll is safe to call before any session is allocated", async () => {
    const factory = vi.fn(async () => makeFakeBrowser());
    const mgr = new SessionManager(factory);

    await mgr.closeAll();
    expect(factory).not.toHaveBeenCalled();
  });

  it("closeAll is idempotent", async () => {
    const browser = makeFakeBrowser();
    const mgr = new SessionManager(async () => browser);
    mgr.register("a");
    await mgr.getOrCreatePage("a");

    await mgr.closeAll();
    await mgr.closeAll();

    expect(browser.close).toHaveBeenCalledTimes(1);
  });

  it("relaunches the browser on the next session after closeAll", async () => {
    const factory = vi.fn(async () => makeFakeBrowser());
    const mgr = new SessionManager(factory);

    mgr.register("a");
    await mgr.getOrCreatePage("a");
    await mgr.closeAll();

    mgr.register("a");
    await mgr.getOrCreatePage("a");

    expect(factory).toHaveBeenCalledTimes(2);
  });

  it("register is a no-op for an already-registered id", () => {
    const mgr = new SessionManager(async () => makeFakeBrowser());
    mgr.register("a");
    mgr.register("a");
    expect(mgr.has("a")).toBe(true);
  });

  it("closeSession returns false for an unknown session id", async () => {
    const browser = makeFakeBrowser();
    const mgr = new SessionManager(async () => browser);
    expect(await mgr.closeSession("ghost")).toBe(false);
    expect(browser.close).not.toHaveBeenCalled();
  });

  it("closeSession evicts the session and closes its context, leaving the browser running", async () => {
    const browser = makeFakeBrowser();
    const mgr = new SessionManager(async () => browser);

    mgr.register("a");
    mgr.register("b");
    await mgr.getOrCreatePage("a");
    await mgr.getOrCreatePage("b");

    expect(await mgr.closeSession("a")).toBe(true);
    expect(mgr.has("a")).toBe(false);
    expect(mgr.has("b")).toBe(true);

    const contexts = browser.contexts();
    expect(contexts).toHaveLength(2);
    expect(contexts[0].close).toHaveBeenCalledTimes(1);
    expect(contexts[1].close).not.toHaveBeenCalled();
    expect(browser.close).not.toHaveBeenCalled();
  });

  it("closeSession is safe to call before the session has navigated", async () => {
    const browser = makeFakeBrowser();
    const factory = vi.fn(async () => browser);
    const mgr = new SessionManager(factory);

    mgr.register("a");
    expect(await mgr.closeSession("a")).toBe(true);
    expect(mgr.has("a")).toBe(false);
    expect(factory).not.toHaveBeenCalled();
  });

  it("closeSession clears any element refs for the session", async () => {
    const browser = makeFakeBrowser();
    const mgr = new SessionManager(async () => browser);
    mgr.register("a");
    await mgr.getOrCreatePage("a");
    const [id] = mgr.allocateRefs("a", [fakeLocator("x")]);
    expect(mgr.lookupRef("a", id).status).toBe("ok");
    await mgr.closeSession("a");
    expect(mgr.lookupRef("a", id).status).toBe("unknown");
  });

  it("bumpGeneration invalidates prior refs for the session", async () => {
    const mgr = new SessionManager(async () => makeFakeBrowser());
    mgr.register("a");
    const [id] = mgr.allocateRefs("a", [fakeLocator("x")]);
    expect(mgr.lookupRef("a", id).status).toBe("ok");
    mgr.bumpGeneration("a");
    expect(mgr.lookupRef("a", id).status).toBe("stale");
  });

  it("currentGeneration starts at zero and advances with bumpGeneration", () => {
    const mgr = new SessionManager(async () => makeFakeBrowser());
    expect(mgr.currentGeneration("a")).toBe(0);
    mgr.bumpGeneration("a");
    expect(mgr.currentGeneration("a")).toBe(1);
  });

  it("pageOf returns null for a session that has not navigated yet", async () => {
    const mgr = new SessionManager(async () => makeFakeBrowser());
    mgr.register("a");
    expect(mgr.pageOf("a")).toBeNull();
    const page = await mgr.getOrCreatePage("a");
    expect(mgr.pageOf("a")).toBe(page);
  });
});
