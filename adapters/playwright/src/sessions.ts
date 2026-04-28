// SPDX-License-Identifier: Apache-2.0
//
// Session manager for the Playwright adapter.
//
// Owns the lazy `Browser` launch, the per-session `BrowserContext`
// + `Page` allocation, and the per-session `ElementRegistry` that
// backs `Query` and `Extract`. R4.3 externalises session metadata
// to Redis (ADR-0023 §4 + §5): `register` writes the metadata,
// `validate` reads it on every non-Initialize RPC, `closeSession`
// deletes the key. The runtime objects (`Browser`, `BrowserContext`,
// `Page`, ElementRegistry entries) stay process-local — the §5
// restart-invalidation contract makes Pod restart equivalent to
// session loss for clients.
//
// Contract (R4.3):
//
//   - `register(sessionId)` — `Initialize` calls this. Writes a
//     `SessionMetadata` document to Redis with the current
//     `adapter_instance_id` and adds the id to the local
//     registered set. Throws if Redis is unreachable; the caller
//     maps the failure to gRPC `UNAVAILABLE`.
//   - `validate(sessionId)` — every non-Initialize RPC calls this
//     before doing any work. Returns one of three kinds:
//       * `ok` — Redis has the session and the stored
//         `adapter_instance_id` matches; TTL refreshed and
//         `last_active_at` updated.
//       * `unknown` — Redis has no entry for this session_id
//         (never created or TTL-expired). Caller maps to
//         `INVALID_ARGUMENT` per ADR-0009 §2.
//       * `different-instance` — Redis has the session but it
//         belongs to a different adapter instance (the §5
//         restart-invalidation case). Caller raises gRPC
//         `UNAVAILABLE` with the message "session belongs to a
//         different adapter instance; client must re-Initialize".
//   - `has(sessionId)` — true once `register` has been called for
//     the id locally and `closeSession`/`closeAll` have not yet
//     evicted it. Used for cheap membership checks where Redis
//     is not authoritative (the `closeSession` early-out path).
//   - `getOrCreatePage(sessionId)` — first call for a registered
//     id launches the shared `Browser` (if not already) and
//     creates a fresh `BrowserContext` + `Page`. Subsequent calls
//     with the same id return the same `Page`.
//   - `pageOf(sessionId)` — returns the live `Page` for a session
//     if one exists, or null if the session is registered but has
//     not yet navigated.
//   - `bumpGeneration` / `currentGeneration` / `allocateRefs` /
//     `lookupRef` — element-registry delegation, unchanged from
//     PR5/ADR-0010.
//   - `closeSession(sessionId)` — deletes the Redis key
//     (best-effort), evicts the local registration, forgets all
//     ElementRefs, and closes the per-session `BrowserContext`.
//   - `closeAll()` — closes every page, every context, the shared
//     `Browser`, and clears every local registration. Idempotent.
//     Does not enumerate Redis keys for deletion — restart
//     invalidation handles abandoned keys via TTL expiry.
//
// The class is constructed with a `BrowserFactory`, a
// `RedisClient`, and the adapter's `instanceId`. Unit tests pass
// an `ioredis-mock` instance and any `instanceId` string.

import type { Browser, BrowserContext, Locator, Page } from "playwright";

import { ElementRegistry, type RefLookup } from "./elements.js";
import {
  ADAPTER_NAME,
  type RedisClientLike,
  type SessionMetadata,
} from "./redis.js";

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

export type ValidationResult =
  | { kind: "ok"; metadata: SessionMetadata }
  | { kind: "unknown" }
  | { kind: "different-instance"; storedInstanceId: string };

export interface SessionManagerOptions {
  factory: BrowserFactory;
  redis: RedisClientLike;
  instanceId: string;
}

export class SessionManager {
  private readonly factory: BrowserFactory;
  private readonly redis: RedisClientLike;
  private readonly instanceId: string;
  private browserPromise: Promise<Browser> | null = null;
  private readonly registered = new Set<string>();
  private readonly sessions = new Map<string, Session>();
  private readonly elements = new ElementRegistry();

  constructor(options: SessionManagerOptions) {
    this.factory = options.factory;
    this.redis = options.redis;
    this.instanceId = options.instanceId;
  }

  /** Returns the adapter_instance_id this manager writes to Redis.
   * Exposed for tests; the value is generated once at startup and
   * never changes. */
  get adapterInstanceId(): string {
    return this.instanceId;
  }

  async register(sessionId: string): Promise<void> {
    const now = new Date().toISOString();
    const metadata: SessionMetadata = {
      session_id: sessionId,
      adapter: ADAPTER_NAME,
      adapter_instance_id: this.instanceId,
      created_at: now,
      last_active_at: now,
      metadata: {},
    };
    // Writes Redis first; if it fails the caller exits Initialize
    // with UNAVAILABLE and the local set is never updated. Order
    // matters: a Redis failure must leave the SessionManager
    // unaware of the id so a retry produces a fresh write rather
    // than the appearance of a registered-but-not-stored session.
    await this.redis.setSession(sessionId, metadata);
    this.registered.add(sessionId);
  }

  has(sessionId: string): boolean {
    return this.registered.has(sessionId);
  }

  async validate(sessionId: string): Promise<ValidationResult> {
    const metadata = await this.redis.getSession(sessionId);
    if (metadata === null) {
      return { kind: "unknown" };
    }
    if (metadata.adapter_instance_id !== this.instanceId) {
      return {
        kind: "different-instance",
        storedInstanceId: metadata.adapter_instance_id,
      };
    }
    // Refresh `last_active_at` and the TTL via a SET-with-EX
    // round-trip. ADR-0023 §5 R4.3 addendum and the phase prompt
    // §4.5 commit to last-write-wins: a second concurrent
    // validate call may overwrite the timestamp by a few ms,
    // which is harmless.
    metadata.last_active_at = new Date().toISOString();
    await this.redis.setSession(sessionId, metadata);
    return { kind: "ok", metadata };
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
    // Best-effort delete; the TTL is the safety net if Redis is
    // briefly unreachable. ADR-0023 §5 / phase prompt §4.6.
    await this.redis.deleteSession(sessionId).catch((err: unknown) => {
      process.stderr.write(
        `redis delete failed for session ${sessionId}: ${String(err)}\n`,
      );
    });
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
