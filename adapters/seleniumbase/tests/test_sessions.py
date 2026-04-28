"""Unit tests for the SeleniumBase session manager.

These tests use a mock driver factory and a ``fakeredis``-backed
:class:`RedisClient` so they run without launching a browser or
dialing a real Redis. The cases cover the contract the gRPC
handler depends on: lazy allocation, reuse, teardown, unknown-
session rejection, and (R4.3) the §5 restart-invalidation
``validate`` path against shared Redis state.
"""

from __future__ import annotations

import json
from collections.abc import Callable

import fakeredis
import pytest

from spectre_seleniumbase.redis_client import ADAPTER_NAME, RedisClient
from spectre_seleniumbase.sessions import SessionManager, UnknownSessionError

TEST_INSTANCE_ID = "instance-aaaa"


def _make_redis() -> tuple[RedisClient, fakeredis.FakeStrictRedis]:
    """Construct a fakeredis-backed :class:`RedisClient`.

    The fake retains state across the test, mirrors TTL behaviour,
    and is reset between tests by virtue of being function-scoped
    here. The same fake instance is exposed so assertions can poke
    at the raw store directly.
    """
    raw = fakeredis.FakeStrictRedis(decode_responses=True)
    return RedisClient(client=raw), raw


def _make_manager(
    instance_id: str = TEST_INSTANCE_ID,
    redis_client: RedisClient | None = None,
) -> tuple[SessionManager, list[_FakeDriver], RedisClient, fakeredis.FakeStrictRedis]:
    drivers, make = _factory()
    if redis_client is None:
        redis_client, raw = _make_redis()
    else:
        raw = redis_client.client
    mgr = SessionManager(factory=make, redis=redis_client, instance_id=instance_id)
    return mgr, drivers, redis_client, raw


class _FakeDriver:
    """Minimal stand-in for a SeleniumBase WebDriver."""

    def __init__(self, idx: int) -> None:
        self.idx = idx
        self.quit_count = 0

    def quit(self) -> None:
        self.quit_count += 1


class _FakeElement:
    """Minimal stand-in for a Selenium ``WebElement``."""

    def __init__(self, label: str) -> None:
        self.label = label


def _factory() -> tuple[list[_FakeDriver], Callable[[], _FakeDriver]]:
    drivers: list[_FakeDriver] = []

    def make_driver() -> _FakeDriver:
        driver = _FakeDriver(len(drivers))
        drivers.append(driver)
        return driver

    return drivers, make_driver


# -- register / has -----------------------------------------------------


def test_register_then_has() -> None:
    mgr, drivers, _, _ = _make_manager()
    mgr.register("s1")
    assert mgr.has("s1")
    assert not mgr.has("never-registered")
    assert drivers == []


def test_register_writes_session_metadata_to_redis() -> None:
    mgr, _, _, raw = _make_manager()
    mgr.register("s1")
    stored = raw.get(f"session:{ADAPTER_NAME}:s1")
    assert stored is not None
    payload = json.loads(stored)
    assert payload["session_id"] == "s1"
    assert payload["adapter"] == ADAPTER_NAME
    assert payload["adapter_instance_id"] == TEST_INSTANCE_ID


def test_register_propagates_redis_failure_and_leaves_state_unchanged() -> None:
    class _BoomRedis:
        def set_session(self, *_: object, **__: object) -> None:
            raise RuntimeError("redis offline")

        def get_session(self, *_: object) -> object:
            return None

        def delete_session(self, *_: object) -> None:
            return None

        def ping(self) -> None:
            return None

        def disconnect(self) -> None:
            return None

    drivers, make = _factory()
    mgr = SessionManager(
        factory=make,
        redis=_BoomRedis(),  # type: ignore[arg-type]
        instance_id=TEST_INSTANCE_ID,
    )
    with pytest.raises(RuntimeError, match="redis offline"):
        mgr.register("s1")
    assert not mgr.has("s1")


def test_register_is_idempotent() -> None:
    mgr, drivers, _, _ = _make_manager()
    mgr.register("s1")
    mgr.register("s1")
    assert mgr.has("s1")
    assert drivers == []


# -- driver lifecycle ---------------------------------------------------


def test_get_or_create_driver_lazy_then_reuse() -> None:
    mgr, drivers, _, _ = _make_manager()
    mgr.register("s1")

    first = mgr.get_or_create_driver("s1")
    second = mgr.get_or_create_driver("s1")

    assert first is second
    assert len(drivers) == 1
    assert drivers[0].idx == 0


def test_get_or_create_driver_rejects_unknown_session() -> None:
    mgr, _, _, _ = _make_manager()
    with pytest.raises(UnknownSessionError) as excinfo:
        mgr.get_or_create_driver("nope")
    assert excinfo.value.session_id == "nope"


def test_driver_of_returns_none_until_launched() -> None:
    mgr, _, _, _ = _make_manager()
    mgr.register("s1")
    assert mgr.driver_of("s1") is None
    mgr.get_or_create_driver("s1")
    assert mgr.driver_of("s1") is not None


def test_close_session_quits_driver_and_evicts() -> None:
    mgr, drivers, _, _ = _make_manager()
    mgr.register("s1")
    mgr.get_or_create_driver("s1")

    closed = mgr.close_session("s1")
    assert closed is True
    assert not mgr.has("s1")
    assert drivers[0].quit_count == 1


def test_close_session_deletes_redis_key() -> None:
    mgr, _, _, raw = _make_manager()
    mgr.register("s1")
    assert raw.get(f"session:{ADAPTER_NAME}:s1") is not None
    assert mgr.close_session("s1") is True
    assert raw.get(f"session:{ADAPTER_NAME}:s1") is None


def test_close_session_unknown_returns_false() -> None:
    mgr, _, _, _ = _make_manager()
    assert mgr.close_session("nope") is False


def test_close_session_swallows_quit_errors() -> None:
    mgr, drivers, _, _ = _make_manager()
    mgr.register("s1")
    driver = mgr.get_or_create_driver("s1")

    def boom() -> None:
        raise RuntimeError("simulated quit failure")

    driver.quit = boom
    assert mgr.close_session("s1") is True
    assert not mgr.has("s1")


def test_close_session_swallows_redis_delete_failure() -> None:
    """Best-effort delete (§4.6): a Redis blip during Close must not
    fail the call. The TTL is the safety net.
    """
    drivers, make = _factory()

    class _FlakyRedis:
        def __init__(self) -> None:
            self.set_calls = 0

        def set_session(self, *_: object, **__: object) -> None:
            self.set_calls += 1

        def get_session(self, *_: object) -> object:
            return None

        def delete_session(self, *_: object) -> None:
            raise RuntimeError("redis blip")

        def ping(self) -> None:
            return None

        def disconnect(self) -> None:
            return None

    flaky = _FlakyRedis()
    mgr = SessionManager(
        factory=make,
        redis=flaky,  # type: ignore[arg-type]
        instance_id=TEST_INSTANCE_ID,
    )
    mgr.register("s1")
    assert mgr.close_session("s1") is True
    assert not mgr.has("s1")


def test_close_all_quits_every_driver_and_is_idempotent() -> None:
    mgr, drivers, _, _ = _make_manager()
    mgr.register("s1")
    mgr.register("s2")
    mgr.get_or_create_driver("s1")
    mgr.get_or_create_driver("s2")

    mgr.close_all()
    assert not mgr.has("s1")
    assert not mgr.has("s2")
    assert drivers[0].quit_count == 1
    assert drivers[1].quit_count == 1

    mgr.close_all()
    assert drivers[0].quit_count == 1


def test_close_all_skips_sessions_with_no_driver() -> None:
    mgr, drivers, _, _ = _make_manager()
    mgr.register("s1")

    mgr.close_all()
    assert not mgr.has("s1")
    assert drivers == []


# -- validate / restart invalidation ------------------------------------


def test_validate_returns_ok_for_same_instance() -> None:
    mgr, _, _, raw = _make_manager()
    mgr.register("s1")
    result = mgr.validate("s1")
    assert result.kind == "ok"
    # last_active_at refreshed on validate.
    stored = json.loads(raw.get(f"session:{ADAPTER_NAME}:s1"))
    assert stored["adapter_instance_id"] == TEST_INSTANCE_ID
    assert "last_active_at" in stored


def test_validate_returns_unknown_when_redis_has_no_entry() -> None:
    mgr, _, _, _ = _make_manager()
    result = mgr.validate("never-existed")
    assert result.kind == "unknown"


def test_validate_returns_different_instance_for_foreign_session() -> None:
    """Two managers, same Redis: A registers, B validates, B sees
    'different_instance' — the parallel-instance pattern the
    conformance suite uses against real adapter subprocesses.
    """
    redis_client, _ = _make_redis()
    mgr_a, _, _, _ = _make_manager("instance-aaaa", redis_client=redis_client)
    mgr_b, _, _, _ = _make_manager("instance-bbbb", redis_client=redis_client)

    mgr_a.register("s1")
    result = mgr_b.validate("s1")
    assert result.kind == "different_instance"
    assert result.stored_instance_id == "instance-aaaa"


# -- ElementRegistry integration ----------------------------------------


def test_register_element_returns_resolvable_uuid() -> None:
    mgr, _, _, _ = _make_manager()
    mgr.register("s1")

    element = _FakeElement("x")
    ref_id = mgr.register_element("s1", element)
    lookup = mgr.lookup_element("s1", ref_id)
    assert lookup.status == "ok"
    assert lookup.element is element


def test_register_elements_preserves_order() -> None:
    mgr, _, _, _ = _make_manager()
    mgr.register("s1")

    elements = [_FakeElement("a"), _FakeElement("b"), _FakeElement("c")]
    ids = mgr.register_elements("s1", elements)
    for ref_id, element in zip(ids, elements, strict=True):
        lookup = mgr.lookup_element("s1", ref_id)
        assert lookup.status == "ok"
        assert lookup.element is element


def test_bump_generation_marks_prior_refs_stale() -> None:
    """Refs allocated in an earlier generation are stale after Navigate."""
    mgr, _, _, _ = _make_manager()
    mgr.register("s1")

    ref_id = mgr.register_element("s1", _FakeElement("pre-nav"))
    mgr.bump_generation("s1")

    lookup = mgr.lookup_element("s1", ref_id)
    assert lookup.status == "stale"
    assert lookup.element is None


def test_lookup_element_unknown_id_distinguished_from_stale() -> None:
    """An id that was never issued returns ``unknown``, not ``stale``.

    The two cases share ``CODE_INVALID_ARGUMENT`` on the wire but
    map to distinct messages — the post-Navigate ``stale`` message
    versus the unknown-ref message. See ADR-0010 §1 and ADR-0015 §2.
    """
    mgr, _, _, _ = _make_manager()
    mgr.register("s1")

    lookup = mgr.lookup_element("s1", "00000000-0000-0000-0000-000000000000")
    assert lookup.status == "unknown"


def test_close_session_clears_element_registry() -> None:
    mgr, _, _, _ = _make_manager()
    mgr.register("s1")
    ref_id = mgr.register_element("s1", _FakeElement("x"))

    mgr.close_session("s1")

    mgr.register("s1")
    lookup = mgr.lookup_element("s1", ref_id)
    assert lookup.status == "unknown"


def test_close_all_clears_element_registry() -> None:
    mgr, _, _, _ = _make_manager()
    mgr.register("s1")
    mgr.register("s2")
    s1_ref = mgr.register_element("s1", _FakeElement("a"))
    s2_ref = mgr.register_element("s2", _FakeElement("b"))

    mgr.close_all()

    mgr.register("s1")
    mgr.register("s2")
    assert mgr.lookup_element("s1", s1_ref).status == "unknown"
    assert mgr.lookup_element("s2", s2_ref).status == "unknown"
