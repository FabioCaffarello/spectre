"""Unit tests for the SeleniumBase Redis client wrapper.

Uses ``fakeredis`` to exercise SET / GET / DEL / TTL semantics
without dialing a real Redis. The conformance suite (which DOES
talk to a running Redis) covers the integration; these tests
cover the wrapper's own contract.
"""

from __future__ import annotations

import json

import fakeredis

from spectre_seleniumbase.redis_client import (
    ADAPTER_NAME,
    SESSION_TTL_SECONDS,
    RedisClient,
    SessionMetadata,
)


def _sample_metadata(**overrides: object) -> SessionMetadata:
    base: SessionMetadata = {
        "session_id": "session-1",
        "adapter": ADAPTER_NAME,
        "adapter_instance_id": "aaaa",
        "created_at": "2026-04-28T00:00:00.000Z",
        "last_active_at": "2026-04-28T00:00:00.000Z",
        "metadata": {},
    }
    base.update(overrides)  # type: ignore[typeddict-item]
    return base


def _make_client() -> tuple[RedisClient, fakeredis.FakeStrictRedis]:
    raw = fakeredis.FakeStrictRedis(decode_responses=True)
    return RedisClient(client=raw), raw


def test_ping_succeeds_against_fakeredis() -> None:
    client, _ = _make_client()
    client.ping()  # raises on failure


def test_set_session_writes_json_under_session_adapter_id() -> None:
    client, raw = _make_client()
    meta = _sample_metadata()
    client.set_session("session-1", meta)
    stored = raw.get(f"session:{ADAPTER_NAME}:session-1")
    assert stored is not None
    assert json.loads(stored) == meta


def test_set_session_applies_one_hour_ttl() -> None:
    client, raw = _make_client()
    client.set_session("session-1", _sample_metadata())
    ttl = raw.ttl(f"session:{ADAPTER_NAME}:session-1")
    assert SESSION_TTL_SECONDS - 5 < ttl <= SESSION_TTL_SECONDS


def test_get_session_returns_parsed_metadata() -> None:
    client, _ = _make_client()
    meta = _sample_metadata(adapter_instance_id="bbbb")
    client.set_session("session-1", meta)
    fetched = client.get_session("session-1")
    assert fetched == meta


def test_get_session_returns_none_for_missing_key() -> None:
    client, _ = _make_client()
    assert client.get_session("ghost") is None


def test_delete_session_removes_the_key() -> None:
    client, raw = _make_client()
    client.set_session("session-1", _sample_metadata())
    assert raw.get(f"session:{ADAPTER_NAME}:session-1") is not None
    client.delete_session("session-1")
    assert raw.get(f"session:{ADAPTER_NAME}:session-1") is None


def test_delete_session_is_a_noop_on_missing_key() -> None:
    client, _ = _make_client()
    client.delete_session("ghost")  # must not raise
