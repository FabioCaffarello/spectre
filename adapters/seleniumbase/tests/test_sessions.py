"""Unit tests for the SeleniumBase session manager.

These tests use a mock driver factory so they run without launching
a real browser. The single-purpose tests cover the contract the
gRPC handler depends on: lazy allocation, reuse, teardown, and
unknown-session rejection.
"""

from __future__ import annotations

from collections.abc import Callable

import pytest

from spectre_seleniumbase.sessions import SessionManager, UnknownSessionError


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


def test_register_then_has() -> None:
    drivers, make = _factory()
    mgr = SessionManager(factory=make)
    mgr.register("s1")
    assert mgr.has("s1")
    assert not mgr.has("never-registered")
    # No driver launched on register-only.
    assert drivers == []


def test_register_is_idempotent() -> None:
    drivers, make = _factory()
    mgr = SessionManager(factory=make)
    mgr.register("s1")
    mgr.register("s1")
    assert mgr.has("s1")
    assert drivers == []


def test_get_or_create_driver_lazy_then_reuse() -> None:
    drivers, make = _factory()
    mgr = SessionManager(factory=make)
    mgr.register("s1")

    first = mgr.get_or_create_driver("s1")
    second = mgr.get_or_create_driver("s1")

    assert first is second
    assert len(drivers) == 1
    assert drivers[0].idx == 0


def test_get_or_create_driver_rejects_unknown_session() -> None:
    _, make = _factory()
    mgr = SessionManager(factory=make)

    with pytest.raises(UnknownSessionError) as excinfo:
        mgr.get_or_create_driver("nope")
    assert excinfo.value.session_id == "nope"


def test_driver_of_returns_none_until_launched() -> None:
    _, make = _factory()
    mgr = SessionManager(factory=make)
    mgr.register("s1")
    assert mgr.driver_of("s1") is None
    mgr.get_or_create_driver("s1")
    assert mgr.driver_of("s1") is not None


def test_close_session_quits_driver_and_evicts() -> None:
    drivers, make = _factory()
    mgr = SessionManager(factory=make)
    mgr.register("s1")
    mgr.get_or_create_driver("s1")

    closed = mgr.close_session("s1")
    assert closed is True
    assert not mgr.has("s1")
    assert drivers[0].quit_count == 1


def test_close_session_unknown_returns_false() -> None:
    _, make = _factory()
    mgr = SessionManager(factory=make)
    assert mgr.close_session("nope") is False


def test_close_session_swallows_quit_errors() -> None:
    drivers, make = _factory()
    mgr = SessionManager(factory=make)
    mgr.register("s1")
    driver = mgr.get_or_create_driver("s1")

    def boom() -> None:
        raise RuntimeError("simulated quit failure")

    driver.quit = boom
    assert mgr.close_session("s1") is True
    assert not mgr.has("s1")


def test_close_all_quits_every_driver_and_is_idempotent() -> None:
    drivers, make = _factory()
    mgr = SessionManager(factory=make)
    mgr.register("s1")
    mgr.register("s2")
    mgr.get_or_create_driver("s1")
    mgr.get_or_create_driver("s2")

    mgr.close_all()
    assert not mgr.has("s1")
    assert not mgr.has("s2")
    assert drivers[0].quit_count == 1
    assert drivers[1].quit_count == 1

    # Second call must not throw and must not re-quit.
    mgr.close_all()
    assert drivers[0].quit_count == 1


def test_close_all_skips_sessions_with_no_driver() -> None:
    drivers, make = _factory()
    mgr = SessionManager(factory=make)
    mgr.register("s1")  # registered but never navigated

    mgr.close_all()
    assert not mgr.has("s1")
    assert drivers == []  # factory never called


# -- ElementRegistry integration ----------------------------------------


def test_register_element_returns_resolvable_uuid() -> None:
    _, make = _factory()
    mgr = SessionManager(factory=make)
    mgr.register("s1")

    element = _FakeElement("x")
    ref_id = mgr.register_element("s1", element)
    lookup = mgr.lookup_element("s1", ref_id)
    assert lookup.status == "ok"
    assert lookup.element is element


def test_register_elements_preserves_order() -> None:
    _, make = _factory()
    mgr = SessionManager(factory=make)
    mgr.register("s1")

    elements = [_FakeElement("a"), _FakeElement("b"), _FakeElement("c")]
    ids = mgr.register_elements("s1", elements)
    for ref_id, element in zip(ids, elements, strict=True):
        lookup = mgr.lookup_element("s1", ref_id)
        assert lookup.status == "ok"
        assert lookup.element is element


def test_bump_generation_marks_prior_refs_stale() -> None:
    """Refs allocated in an earlier generation are stale after Navigate."""
    _, make = _factory()
    mgr = SessionManager(factory=make)
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
    _, make = _factory()
    mgr = SessionManager(factory=make)
    mgr.register("s1")

    lookup = mgr.lookup_element("s1", "00000000-0000-0000-0000-000000000000")
    assert lookup.status == "unknown"


def test_close_session_clears_element_registry() -> None:
    _, make = _factory()
    mgr = SessionManager(factory=make)
    mgr.register("s1")
    ref_id = mgr.register_element("s1", _FakeElement("x"))

    mgr.close_session("s1")

    # Re-register to demonstrate the entry is gone, not just hidden by
    # the missing session.
    mgr.register("s1")
    lookup = mgr.lookup_element("s1", ref_id)
    assert lookup.status == "unknown"


def test_close_all_clears_element_registry() -> None:
    _, make = _factory()
    mgr = SessionManager(factory=make)
    mgr.register("s1")
    mgr.register("s2")
    s1_ref = mgr.register_element("s1", _FakeElement("a"))
    s2_ref = mgr.register_element("s2", _FakeElement("b"))

    mgr.close_all()

    mgr.register("s1")
    mgr.register("s2")
    assert mgr.lookup_element("s1", s1_ref).status == "unknown"
    assert mgr.lookup_element("s2", s2_ref).status == "unknown"
