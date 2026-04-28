"""Redis client wrapper for the SeleniumBase adapter.

R4.3 externalises adapter session metadata to Redis (ADR-0023
§4 + §5). Keys live under ``session:<adapter>:<session_id>``;
values are JSON documents that include ``adapter_instance_id``,
the per-process UUID the §5 R4.3 addendum specifies as the
restart-invalidation mechanism.

The wrapper is intentionally thin: it owns one ``redis.Redis``
client for the adapter's lifetime and exposes the four
session-shaped operations the SessionManager needs (set, get,
delete, ping). Reconnection on transient failures is handled
by the underlying client; failed RPCs surface as connection
errors that the caller maps to gRPC ``UNAVAILABLE``.

The adapter name is fixed to ``seleniumbase`` (matches
``jobs.driver`` in Postgres and the future Kafka header
value); the SessionManager passes session ids through verbatim.
"""

from __future__ import annotations

import contextlib
import json
from dataclasses import dataclass
from typing import Any, TypedDict, cast

import redis

ADAPTER_NAME = "seleniumbase"

# 1-hour idle TTL per ADR-0023 §4. Refreshed on every read/write.
SESSION_TTL_SECONDS = 3600


class SessionMetadata(TypedDict):
    """Schema for the JSON document stored under each session key.

    Mirrors the Playwright (TS) and curl-impersonate (Go)
    counterparts byte-for-byte so the conformance harness can
    decode any adapter's session document with the same keys.
    """

    session_id: str
    adapter: str
    adapter_instance_id: str
    created_at: str
    last_active_at: str
    metadata: dict[str, Any]


def _session_key(session_id: str) -> str:
    return f"session:{ADAPTER_NAME}:{session_id}"


@dataclass
class RedisClient:
    """Thin wrapper around a synchronous ``redis.Redis`` client."""

    client: redis.Redis

    @classmethod
    def from_url(cls, url: str) -> RedisClient:
        """Construct a client from a redis:// URL.

        ``decode_responses=True`` keeps the SET / GET path on
        ``str`` rather than ``bytes``; the JSON marshal/unmarshal
        is one fewer copy this way.
        """
        client = redis.Redis.from_url(url, decode_responses=True)
        return cls(client=client)

    def ping(self) -> None:
        """Verify the connection. Raises on failure.

        ADR-0023 §6 makes Redis required at adapter startup, so
        callers exit non-zero if this raises (see
        ``adapter.serve``).
        """
        self.client.ping()

    def set_session(self, session_id: str, value: SessionMetadata) -> None:
        self.client.set(
            _session_key(session_id),
            json.dumps(value),
            ex=SESSION_TTL_SECONDS,
        )

    def get_session(self, session_id: str) -> SessionMetadata | None:
        raw = self.client.get(_session_key(session_id))
        if raw is None:
            return None
        return cast(SessionMetadata, json.loads(raw))

    def delete_session(self, session_id: str) -> None:
        self.client.delete(_session_key(session_id))

    def disconnect(self) -> None:
        # ``close()`` is the modern method; ``connection_pool.disconnect``
        # is what shutdown actually needs to release sockets. Disconnect
        # must never raise on shutdown.
        with contextlib.suppress(Exception):
            self.client.close()
