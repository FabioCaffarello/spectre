// SPDX-License-Identifier: Apache-2.0
//
// Session manager for the Playwright adapter.
//
// Owns the lazy `Browser` launch and the per-session `BrowserContext`
// + `Page` allocation, plus the per-session `ElementRegistry` that
// backs `Query` and `Extract`. The contract:
//
//   - `register(sessionId)` — `Initialize` calls this to declare the
//     id. No browser work happens here; the registration is metadata
//     only. Calling it twice with the same id is a no-op.
//   - `has(sessionId)` — true once `register` has been called for the
//     id and `closeSession`/`closeAll` have not yet evicted it. Used
//     by every RPC to reject unknown ids with
//     `CODE_INVALID_ARGUMENT`.
//   - `getOrCreatePage(sessionId)` — first call for a registered id
//     launches the shared `Browser` (if not already) and creates a
//     fresh `BrowserContext` + `Page`. Subsequent calls with the same
//     id return the same `Page`. Throws if the id is not registered.
//   - `bumpGeneration(sessionId)` — invalidates every prior
//     `ElementRef` for the session by incrementing its generation
//     counter and clearing the registry entries. Called after every
//     successful `Navigate`. See ADR-0010.
//   - `currentGeneration(sessionId)` — returns the session's current
//     generation counter; used by `Query` and `Extract`.
//   - `allocateRefs(sessionId, locators)` — allocates UUIDs for the
//     locators in the session's registry.
//   - `lookupRef(sessionId, id)` — resolves a UUID back to a locator,
//     or returns a `"stale"`/`"unknown"` status the handler can map
//     to a clean `DriverError`.
//   - `closeSession(sessionId)` — closes the per-session
//     `BrowserContext` (and its `Page`), evicts the session entry,
//     and forgets all ElementRefs for the session. Does *not* close
//     the shared `Browser`; other sessions continue.
//   - `closeAll()` — closes every page, every context, the shared
//     `Browser`, and clears every registration. Idempotent.
//
// The class is constructed with a `BrowserFactory` so unit tests can
// mock the Playwright surface without launching a real browser. See
// ADR-0009 for the lifecycle rationale and ADR-0010 for the element
// lifecycle.

import type { Browser, BrowserContext, Locator, Page } from "playwright";

import { ElementRegistry, type RefLookup } from "./elements.js";

export type BrowserFactory = () => Promise<Browser>;

interface Session {
  context: BrowserContext;
  page: Page;
}

export class UnknownSessionError extends Error {
  readonly sessionId: string;
  constructor(sessionId: string) {
    super(`unknown session_id: ${JSON.stringify(sessionId)}`);
    this.name = "UnknownSessionError";
    this.sessionId = sessionId;
  }
}

export class SessionManager {
  private readonly factory: BrowserFactory;
  private browserPromise: Promise<Browser> | null = null;
  private readonly registered = new Set<string>();
  private readonly sessions = new Map<string, Session>();
  private readonly elements = new ElementRegistry();

  constructor(factory: BrowserFactory) {
    this.factory = factory;
  }

  register(sessionId: string): void {
    this.registered.add(sessionId);
  }

  has(sessionId: string): boolean {
    return this.registered.has(sessionId);
  }

  async getOrCreatePage(sessionId: string): Promise<Page> {
    if (!this.registered.has(sessionId)) {
      throw new UnknownSessionError(sessionId);
    }
    const existing = this.sessions.get(sessionId);
    if (existing) {
      return existing.page;
    }
    const browser = await this.getBrowser();
    const context = await browser.newContext();
    const page = await context.newPage();
    this.sessions.set(sessionId, { context, page });
    return page;
  }

  /** Returns the live `Page` for a session if one exists, or null
   * if the session is registered but has not yet navigated. */
  pageOf(sessionId: string): Page | null {
    return this.sessions.get(sessionId)?.page ?? null;
  }

  bumpGeneration(sessionId: string): void {
    this.elements.bumpGeneration(sessionId);
  }

  currentGeneration(sessionId: string): number {
    return this.elements.currentGeneration(sessionId);
  }

  allocateRefs(sessionId: string, locators: readonly Locator[]): string[] {
    return this.elements.allocateRefs(sessionId, locators);
  }

  lookupRef(sessionId: string, id: string): RefLookup {
    return this.elements.lookupRef(sessionId, id);
  }

  async closeSession(sessionId: string): Promise<boolean> {
    if (!this.registered.has(sessionId)) {
      return false;
    }
    this.registered.delete(sessionId);
    this.elements.forgetSession(sessionId);
    const session = this.sessions.get(sessionId);
    if (session) {
      this.sessions.delete(sessionId);
      // Closing the context closes its pages; we never close the
      // shared Browser here — other sessions continue.
      await session.context.close().catch(() => undefined);
    }
    return true;
  }

  async closeAll(): Promise<void> {
    const sessions = [...this.sessions.values()];
    this.sessions.clear();
    for (const id of [...this.registered]) {
      this.elements.forgetSession(id);
    }
    this.registered.clear();
    for (const { context } of sessions) {
      await context.close().catch(() => undefined);
    }

    if (this.browserPromise) {
      const browser = await this.browserPromise.catch(() => null);
      this.browserPromise = null;
      if (browser) {
        await browser.close().catch(() => undefined);
      }
    }
  }

  private getBrowser(): Promise<Browser> {
    if (!this.browserPromise) {
      this.browserPromise = this.factory();
    }
    return this.browserPromise;
  }
}
