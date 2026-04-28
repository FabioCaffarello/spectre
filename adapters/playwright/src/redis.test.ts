// SPDX-License-Identifier: Apache-2.0

import RedisMock from "ioredis-mock";
import { describe, expect, it } from "vitest";

import {
  ADAPTER_NAME,
  RedisClient,
  SESSION_TTL_SECONDS,
  type SessionMetadata,
} from "./redis.js";

type MockRedis = InstanceType<typeof RedisMock>;

const sampleMetadata = (overrides: Partial<SessionMetadata> = {}): SessionMetadata => ({
  session_id: "session-1",
  adapter: ADAPTER_NAME,
  adapter_instance_id: "aaaa",
  created_at: "2026-04-28T00:00:00.000Z",
  last_active_at: "2026-04-28T00:00:00.000Z",
  metadata: {},
  ...overrides,
});

const newClient = (): { client: RedisClient; raw: MockRedis } => {
  const raw = new RedisMock();
  return { client: new RedisClient(raw as unknown as never), raw };
};

describe("RedisClient", () => {
  it("ping resolves when redis is reachable", async () => {
    const { client } = newClient();
    await expect(client.ping()).resolves.toBeUndefined();
  });

  it("setSession writes the JSON document under session:<adapter>:<id>", async () => {
    const { client, raw } = newClient();
    const meta = sampleMetadata();

    await client.setSession("session-1", meta);

    const stored = await raw.get(`session:${ADAPTER_NAME}:session-1`);
    expect(stored).not.toBeNull();
    expect(JSON.parse(stored ?? "{}")).toEqual(meta);
  });

  it("setSession applies the 1-hour TTL", async () => {
    const { client, raw } = newClient();
    await client.setSession("session-1", sampleMetadata());

    const ttl = await raw.ttl(`session:${ADAPTER_NAME}:session-1`);
    // ioredis-mock returns the TTL in seconds; allow a small slack
    // for any test-runner hiccup.
    expect(ttl).toBeGreaterThan(SESSION_TTL_SECONDS - 5);
    expect(ttl).toBeLessThanOrEqual(SESSION_TTL_SECONDS);
  });

  it("getSession returns the parsed document for an existing key", async () => {
    const { client } = newClient();
    const meta = sampleMetadata({ adapter_instance_id: "bbbb" });
    await client.setSession("session-1", meta);

    const fetched = await client.getSession("session-1");
    expect(fetched).toEqual(meta);
  });

  it("getSession returns null for a missing key", async () => {
    const { client } = newClient();
    expect(await client.getSession("ghost")).toBeNull();
  });

  it("deleteSession removes the key", async () => {
    const { client, raw } = newClient();
    await client.setSession("session-1", sampleMetadata());
    expect(await raw.get(`session:${ADAPTER_NAME}:session-1`)).not.toBeNull();

    await client.deleteSession("session-1");
    expect(await raw.get(`session:${ADAPTER_NAME}:session-1`)).toBeNull();
  });

  it("deleteSession is a no-op on a missing key", async () => {
    const { client } = newClient();
    await expect(client.deleteSession("ghost")).resolves.toBeUndefined();
  });
});
