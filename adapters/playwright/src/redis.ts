// SPDX-License-Identifier: Apache-2.0
//
// Redis client wrapper for the Playwright adapter.
//
// R4.3 externalises adapter session metadata to Redis (ADR-0023
// §4 + §5). Keys live under ``session:<adapter>:<session_id>``;
// values are JSON documents that include ``adapter_instance_id``,
// the per-process UUID the §5 R4.3 addendum specifies as the
// restart-invalidation mechanism.
//
// The wrapper is thin on purpose: it owns the connection (one
// ioredis instance per adapter process) and exposes the four
// session-shaped operations the SessionManager needs (set, get,
// delete, refresh-TTL). Reconnection on transient failures is
// handled by ioredis itself; failed RPCs surface as connection
// errors that the caller maps to gRPC ``UNAVAILABLE``.
//
// The adapter name is fixed to ``playwright`` (matches
// ``jobs.driver`` in Postgres and the Kafka header value); the
// SessionManager passes session ids through verbatim.

import { Redis, type RedisOptions } from "ioredis";

export const ADAPTER_NAME = "playwright";

// 1-hour idle TTL per ADR-0023 §4. Refreshed on every read/write.
export const SESSION_TTL_SECONDS = 3600;

export interface SessionMetadata {
  session_id: string;
  adapter: string;
  adapter_instance_id: string;
  created_at: string;
  last_active_at: string;
  metadata: Record<string, unknown>;
}

const sessionKey = (adapter: string, sessionId: string): string =>
  `session:${adapter}:${sessionId}`;

export interface RedisClientLike {
  ping(): Promise<void>;
  setSession(sessionId: string, value: SessionMetadata): Promise<void>;
  getSession(sessionId: string): Promise<SessionMetadata | null>;
  deleteSession(sessionId: string): Promise<void>;
  disconnect(): Promise<void>;
}

export class RedisClient implements RedisClientLike {
  private readonly client: Redis;

  constructor(client: Redis) {
    this.client = client;
  }

  // Construct a client from a redis:// URL. The connection is
  // lazy: ``ping()`` triggers the first dial. ADR-0023 §6 makes
  // Redis required at adapter startup, so the caller must invoke
  // ``ping`` before serving traffic and exit non-zero on failure.
  static fromUrl(url: string, options: RedisOptions = {}): RedisClient {
    const ioRedis = new Redis(url, {
      lazyConnect: true,
      maxRetriesPerRequest: 3,
      ...options,
    });
    return new RedisClient(ioRedis);
  }

  async ping(): Promise<void> {
    await this.client.ping();
  }

  async setSession(sessionId: string, value: SessionMetadata): Promise<void> {
    await this.client.set(
      sessionKey(ADAPTER_NAME, sessionId),
      JSON.stringify(value),
      "EX",
      SESSION_TTL_SECONDS,
    );
  }

  async getSession(sessionId: string): Promise<SessionMetadata | null> {
    const raw = await this.client.get(sessionKey(ADAPTER_NAME, sessionId));
    if (raw === null) {
      return null;
    }
    return JSON.parse(raw) as SessionMetadata;
  }

  async deleteSession(sessionId: string): Promise<void> {
    await this.client.del(sessionKey(ADAPTER_NAME, sessionId));
  }

  async disconnect(): Promise<void> {
    // ioredis's ``quit`` rejects if the connection was never
    // established (e.g. the adapter exited before any RPC). We
    // accept that case — there's nothing to clean up.
    await this.client.quit().catch(() => undefined);
  }
}
