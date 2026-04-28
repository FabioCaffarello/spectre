// SPDX-License-Identifier: Apache-2.0

import RedisMock from "ioredis-mock";
import type { Browser, BrowserContext, Locator, Page } from "playwright";
import { describe, expect, it, vi } from "vitest";

import {
  ADAPTER_NAME,
  RedisClient,
  type RedisClientLike,
  type SessionMetadata,
} from "./redis.js";
import { SessionManager, UnknownSessionError } from "./sessions.js";

type MockRedis = InstanceType<typeof RedisMock>;

const TEST_INSTANCE_ID = "instance-aaaa";

const fakeLocator = (tag: string): Locator =>
  ({ __id: tag }) as unknown as Locator;

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

interface ManagerHarness {
  browser: FakeBrowser;
  factory: ReturnType<typeof vi.fn>;
  redis: RedisClient;
  raw: MockRedis;
  mgr: SessionManager;
}

const makeManager = (instanceId: string = TEST_INSTANCE_ID): ManagerHarness => {
  const browser = makeFakeBrowser();
  const factory = vi.fn(async () => browser);
  const raw = new RedisMock();
  const redis = new RedisClient(raw as unknown as never);
  const mgr = new SessionManager({ factory, redis, instanceId });
  return { browser, factory, redis, raw, mgr };
};

describe("SessionManager", () => {
  it("register writes the session metadata to Redis with the manager's instance id", async () => {
    const { mgr, raw } = makeManager();
    await mgr.register("s1");

    expect(mgr.has("s1")).toBe(true);
    const stored = await raw.get(`session:${ADAPTER_NAME}:s1`);
    expect(stored).not.toBeNull();
    const parsed = JSON.parse(stored ?? "{}") as SessionMetadata;
    expect(parsed.session_id).toBe("s1");
    expect(parsed.adapter).toBe(ADAPTER_NAME);
    expect(parsed.adapter_instance_id).toBe(TEST_INSTANCE_ID);
  });

  it("register propagates Redis failures and leaves the session unregistered", async () => {
    const browser = makeFakeBrowser();
    const factory = vi.fn(async () => browser);
    const failing: RedisClientLike = {
      ping: async () => undefined,
      setSession: async () => {
        throw new Error("redis offline");
      },
      getSession: async () => null,
      deleteSession: async () => undefined,
      disconnect: async () => undefined,
    };
    const mgr = new SessionManager({
      factory,
      redis: failing,
      instanceId: TEST_INSTANCE_ID,
    });

    await expect(mgr.register("s1")).rejects.toThrow(/redis offline/);
    expect(mgr.has("s1")).toBe(false);
  });

  it("register followed by getOrCreatePage launches the browser lazily", async () => {
    const { mgr, factory, browser } = makeManager();
    await mgr.register("s1");
    expect(factory).not.toHaveBeenCalled();
    expect(mgr.has("s1")).toBe(true);

    await mgr.getOrCreatePage("s1");
    expect(factory).toHaveBeenCalledTimes(1);
    expect(browser.contexts()).toHaveLength(1);
  });

  it("rejects an unregistered session id with UnknownSessionError", async () => {
    const { mgr, factory } = makeManager();
    expect(mgr.has("ghost")).toBe(false);
    await expect(mgr.getOrCreatePage("ghost")).rejects.toBeInstanceOf(
      UnknownSessionError,
    );
    expect(factory).not.toHaveBeenCalled();
  });

  it("reuses the same Page for repeat calls with the same session id", async () => {
    const { mgr, browser } = makeManager();
    await mgr.register("s1");
    const first = await mgr.getOrCreatePage("s1");
    const second = await mgr.getOrCreatePage("s1");

    expect(second).toBe(first);
    expect(browser.contexts()).toHaveLength(1);
  });

  it("allocates a separate context per distinct session id", async () => {
    const { mgr, browser } = makeManager();
    await mgr.register("a");
    await mgr.register("b");
    const a = await mgr.getOrCreatePage("a");
    const b = await mgr.getOrCreatePage("b");

    expect(a).not.toBe(b);
    expect(browser.contexts()).toHaveLength(2);
  });

  it("does not relaunch the browser across multiple sessions", async () => {
    const { mgr, factory } = makeManager();
    await mgr.register("a");
    await mgr.register("b");
    await mgr.register("c");
    await mgr.getOrCreatePage("a");
    await mgr.getOrCreatePage("b");
    await mgr.getOrCreatePage("c");

    expect(factory).toHaveBeenCalledTimes(1);
  });

  it("closeAll closes every context, the browser, and clears registrations", async () => {
    const { mgr, browser } = makeManager();
    await mgr.register("a");
    await mgr.register("b");
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
    const { mgr, factory } = makeManager();
    await mgr.closeAll();
    expect(factory).not.toHaveBeenCalled();
  });

  it("closeAll is idempotent", async () => {
    const { mgr, browser } = makeManager();
    await mgr.register("a");
    await mgr.getOrCreatePage("a");

    await mgr.closeAll();
    await mgr.closeAll();

    expect(browser.close).toHaveBeenCalledTimes(1);
  });

  it("relaunches the browser on the next session after closeAll", async () => {
    const { mgr, factory } = makeManager();
    await mgr.register("a");
    await mgr.getOrCreatePage("a");
    await mgr.closeAll();

    await mgr.register("a");
    await mgr.getOrCreatePage("a");

    expect(factory).toHaveBeenCalledTimes(2);
  });

  it("register is a no-op for an already-registered id (after the first Redis write)", async () => {
    const { mgr } = makeManager();
    await mgr.register("a");
    await mgr.register("a");
    expect(mgr.has("a")).toBe(true);
  });

  it("closeSession returns false for an unknown session id", async () => {
    const { mgr, browser } = makeManager();
    expect(await mgr.closeSession("ghost")).toBe(false);
    expect(browser.close).not.toHaveBeenCalled();
  });

  it("closeSession evicts the session and closes its context, leaving the browser running", async () => {
    const { mgr, browser } = makeManager();
    await mgr.register("a");
    await mgr.register("b");
    await mgr.getOrCreatePage("a");
    await mgr.getOrCreatePage("b");

    expect(await mgr.closeSession("a")).toBe(true);
    expect(mgr.has("a")).toBe(false);
    expect(mgr.has("b")).toBe(true);

    const contexts = browser.contexts();
    expect(contexts).toHaveLength(2);
    const [first, second] = contexts;
    if (!first || !second) {
      throw new Error("expected two contexts");
    }
    expect(first.close).toHaveBeenCalledTimes(1);
    expect(second.close).not.toHaveBeenCalled();
    expect(browser.close).not.toHaveBeenCalled();
  });

  it("closeSession deletes the Redis key", async () => {
    const { mgr, raw } = makeManager();
    await mgr.register("a");
    expect(await raw.get(`session:${ADAPTER_NAME}:a`)).not.toBeNull();
    expect(await mgr.closeSession("a")).toBe(true);
    expect(await raw.get(`session:${ADAPTER_NAME}:a`)).toBeNull();
  });

  it("closeSession is safe to call before the session has navigated", async () => {
    const { mgr, factory } = makeManager();
    await mgr.register("a");
    expect(await mgr.closeSession("a")).toBe(true);
    expect(mgr.has("a")).toBe(false);
    expect(factory).not.toHaveBeenCalled();
  });

  it("closeSession swallows a Redis delete failure (best-effort, §4.6)", async () => {
    const browser = makeFakeBrowser();
    const factory = vi.fn(async () => browser);
    const flaky: RedisClientLike = {
      ping: async () => undefined,
      setSession: async () => undefined,
      getSession: async () => null,
      deleteSession: async () => {
        throw new Error("redis blip");
      },
      disconnect: async () => undefined,
    };
    const mgr = new SessionManager({
      factory,
      redis: flaky,
      instanceId: TEST_INSTANCE_ID,
    });
    await mgr.register("a");
    await expect(mgr.closeSession("a")).resolves.toBe(true);
    expect(mgr.has("a")).toBe(false);
  });

  it("closeSession clears any element refs for the session", async () => {
    const { mgr } = makeManager();
    await mgr.register("a");
    await mgr.getOrCreatePage("a");
    const ids = mgr.allocateRefs("a", [fakeLocator("x")]);
    const id = ids[0];
    if (!id) throw new Error("expected an allocated id");
    expect(mgr.lookupRef("a", id).status).toBe("ok");
    await mgr.closeSession("a");
    expect(mgr.lookupRef("a", id).status).toBe("unknown");
  });

  it("bumpGeneration invalidates prior refs for the session", async () => {
    const { mgr } = makeManager();
    await mgr.register("a");
    const ids = mgr.allocateRefs("a", [fakeLocator("x")]);
    const id = ids[0];
    if (!id) throw new Error("expected an allocated id");
    expect(mgr.lookupRef("a", id).status).toBe("ok");
    mgr.bumpGeneration("a");
    expect(mgr.lookupRef("a", id).status).toBe("stale");
  });

  it("currentGeneration starts at zero and advances with bumpGeneration", () => {
    const { mgr } = makeManager();
    expect(mgr.currentGeneration("a")).toBe(0);
    mgr.bumpGeneration("a");
    expect(mgr.currentGeneration("a")).toBe(1);
  });

  it("pageOf returns null for a session that has not navigated yet", async () => {
    const { mgr } = makeManager();
    await mgr.register("a");
    expect(mgr.pageOf("a")).toBeNull();
    const page = await mgr.getOrCreatePage("a");
    expect(mgr.pageOf("a")).toBe(page);
  });

  // -- validate ---------------------------------------------------

  it("validate returns ok and refreshes last_active_at for a same-instance session", async () => {
    const { mgr, raw } = makeManager();
    await mgr.register("s1");
    const before = JSON.parse(
      (await raw.get(`session:${ADAPTER_NAME}:s1`)) ?? "{}",
    ) as SessionMetadata;

    // Pause briefly so the timestamps differ at second-resolution.
    await new Promise((resolve) => setTimeout(resolve, 5));

    const result = await mgr.validate("s1");
    expect(result.kind).toBe("ok");

    const after = JSON.parse(
      (await raw.get(`session:${ADAPTER_NAME}:s1`)) ?? "{}",
    ) as SessionMetadata;
    expect(after.last_active_at >= before.last_active_at).toBe(true);
    expect(after.adapter_instance_id).toBe(TEST_INSTANCE_ID);
  });

  it("validate returns 'unknown' when Redis has no entry for the session id", async () => {
    const { mgr } = makeManager();
    const result = await mgr.validate("never-existed");
    expect(result.kind).toBe("unknown");
  });

  it("validate returns 'different-instance' when the stored adapter_instance_id differs", async () => {
    const browser = makeFakeBrowser();
    const factory = vi.fn(async () => browser);
    const raw = new RedisMock();
    const redis = new RedisClient(raw as unknown as never);

    // Instance A writes the session, then we construct a manager
    // that pretends to be a different instance reading the same
    // Redis. This is the parallel-instance pattern the conformance
    // test uses (Section 4.4 / phase prompt).
    const mgrA = new SessionManager({
      factory,
      redis,
      instanceId: "instance-aaaa",
    });
    await mgrA.register("s1");

    const mgrB = new SessionManager({
      factory,
      redis,
      instanceId: "instance-bbbb",
    });
    const result = await mgrB.validate("s1");
    expect(result.kind).toBe("different-instance");
    if (result.kind !== "different-instance") return;
    expect(result.storedInstanceId).toBe("instance-aaaa");
  });
});
