// SPDX-License-Identifier: Apache-2.0
//
// Session manager for the Playwright adapter.
//
// Owns the lazy `Browser` launch and the per-session `BrowserContext`
// + `Page` allocation. The contract:
//
//   - `register(sessionId)` — `Initialize` calls this to declare the
//     id. No browser work happens here; the registration is metadata
//     only. Calling it twice with the same id is a no-op.
//   - `has(sessionId)` — true once `register` has been called for the
//     id and `closeAll` has not yet been called. Used by `Navigate`
//     to reject unknown ids with `CODE_INVALID_ARGUMENT`.
//   - `getOrCreatePage(sessionId)` — first call for a registered id
//     launches the shared `Browser` (if not already) and creates a
//     fresh `BrowserContext` + `Page`. Subsequent calls with the same
//     id return the same `Page`. Throws if the id is not registered.
//   - `closeAll()` — closes every page, every context, the shared
//     `Browser`, and clears the registration set. Idempotent.
//
// The class is constructed with a `BrowserFactory` so unit tests can
// mock the Playwright surface without launching a real browser. See
// ADR-0009 for the lifecycle and reuse rationale.

import type { Browser, BrowserContext, Page } from "playwright";

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

  async closeAll(): Promise<void> {
    const sessions = [...this.sessions.values()];
    this.sessions.clear();
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
