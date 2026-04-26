// SPDX-License-Identifier: Apache-2.0
//
// ElementRef registry for the Playwright adapter.
//
// `Query` allocates UUIDv4 ids for each Playwright `Locator` it
// returns; `Extract` looks the id up to recover the locator. Refs
// are scoped to a session and tagged with a generation counter
// that bumps on every `Navigate`. An `Extract` against a ref whose
// generation does not match the session's current generation is
// rejected with `CODE_INVALID_ARGUMENT` — see ADR-0010, decision 1
// (strict invalidation, fail-loud over fail-silent).
//
// The registry intentionally avoids stable / deterministic ids
// (selector + position hashes collide on similar DOMs) and avoids
// serialising Playwright `Locator`s onto the wire (couples the
// protocol to a vendor's internal representation). UUIDv4 plus a
// per-session map is the cheapest scheme that satisfies the
// "opaque on the wire, driver-agnostic protocol" contract.

import { randomUUID } from "node:crypto";
import type { Locator } from "playwright";

interface RegistryEntry {
  locator: Locator;
  generation: number;
}

export interface RefLookup {
  /**
   * `"unknown"` — the id was never issued for this session, or the
   * session has been closed and its registry entry removed.
   * `"stale"`  — the id was issued in an earlier generation that
   * has since been invalidated by a `Navigate`.
   * `"ok"`    — the locator can be returned.
   */
  readonly status: "ok" | "unknown" | "stale";
  readonly locator?: Locator;
}

export class ElementRegistry {
  // Outer map: sessionId -> { generation, refs }. The session entry
  // is created on demand by `allocateRefs` and removed by
  // `forgetSession`. The generation counter is a JavaScript number;
  // overflow is irrelevant in practice (Number.MAX_SAFE_INTEGER is
  // ~9e15).
  private readonly sessions = new Map<
    string,
    { generation: number; refs: Map<string, RegistryEntry> }
  >();

  /** Returns the current generation counter for a session, or zero
   * if the session has no registry entry yet. */
  currentGeneration(sessionId: string): number {
    return this.sessions.get(sessionId)?.generation ?? 0;
  }

  /** Increments the generation counter for a session. Prior refs
   * are *not* removed — they remain in the map so a subsequent
   * `lookupRef` can distinguish "stale" (id was issued in an older
   * generation) from "unknown" (id was never issued for this
   * session). Entries are dropped only when the session is
   * forgotten. Idempotent on a session that has never allocated. */
  bumpGeneration(sessionId: string): void {
    const entry = this.sessions.get(sessionId);
    if (entry) {
      entry.generation += 1;
      return;
    }
    this.sessions.set(sessionId, { generation: 1, refs: new Map() });
  }

  /** Removes the session's registry entry entirely. Called from
   * `Close`. */
  forgetSession(sessionId: string): void {
    this.sessions.delete(sessionId);
  }

  /** Allocates a UUIDv4 for each locator and stores the mapping at
   * the session's current generation. Returns the allocated ids in
   * the same order as the locators. */
  allocateRefs(sessionId: string, locators: readonly Locator[]): string[] {
    let entry = this.sessions.get(sessionId);
    if (!entry) {
      entry = { generation: 0, refs: new Map() };
      this.sessions.set(sessionId, entry);
    }
    const ids: string[] = [];
    for (const locator of locators) {
      const id = randomUUID();
      entry.refs.set(id, { locator, generation: entry.generation });
      ids.push(id);
    }
    return ids;
  }

  /** Looks up an id. Returns the locator only when the id exists
   * and its generation matches the session's current generation. */
  lookupRef(sessionId: string, id: string): RefLookup {
    const entry = this.sessions.get(sessionId);
    if (!entry) {
      return { status: "unknown" };
    }
    const ref = entry.refs.get(id);
    if (!ref) {
      return { status: "unknown" };
    }
    if (ref.generation !== entry.generation) {
      return { status: "stale" };
    }
    return { status: "ok", locator: ref.locator };
  }
}
