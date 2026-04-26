"""Unit tests for the SeleniumBase ElementRegistry.

Mirrors the Playwright ``elements.test.ts`` suite from PR5: the
registry is the load-bearing contract for the strict
ElementRef invalidation rule, and these tests are how we know
the contract holds without standing up Chrome.
"""

from __future__ import annotations

from spectre_seleniumbase.elements import ElementRegistry


class _FakeElement:
    """Minimal stand-in for a Selenium ``WebElement``."""

    def __init__(self, label: str) -> None:
        self.label = label


def test_current_generation_zero_for_unknown_session() -> None:
    registry = ElementRegistry()
    assert registry.current_generation("never-touched") == 0


def test_allocate_refs_returns_unique_ids() -> None:
    registry = ElementRegistry()
    a = _FakeElement("a")
    b = _FakeElement("b")
    ids = registry.allocate_refs("s1", [a, b])
    assert len(ids) == 2
    assert ids[0] != ids[1]


def test_lookup_ref_returns_element_when_generation_matches() -> None:
    registry = ElementRegistry()
    element = _FakeElement("x")
    [ref_id] = registry.allocate_refs("s1", [element])
    lookup = registry.lookup_ref("s1", ref_id)
    assert lookup.status == "ok"
    assert lookup.element is element


def test_lookup_ref_returns_stale_after_generation_bump() -> None:
    registry = ElementRegistry()
    element = _FakeElement("x")
    [ref_id] = registry.allocate_refs("s1", [element])
    registry.bump_generation("s1")
    lookup = registry.lookup_ref("s1", ref_id)
    assert lookup.status == "stale"
    assert lookup.element is None


def test_lookup_ref_unknown_id_returns_unknown() -> None:
    registry = ElementRegistry()
    registry.allocate_refs("s1", [_FakeElement("x")])
    lookup = registry.lookup_ref("s1", "00000000-0000-0000-0000-000000000000")
    assert lookup.status == "unknown"


def test_lookup_ref_unknown_session_returns_unknown() -> None:
    registry = ElementRegistry()
    lookup = registry.lookup_ref("nope", "00000000-0000-0000-0000-000000000000")
    assert lookup.status == "unknown"


def test_forget_session_drops_all_refs() -> None:
    registry = ElementRegistry()
    [ref_id] = registry.allocate_refs("s1", [_FakeElement("x")])
    registry.forget_session("s1")
    lookup = registry.lookup_ref("s1", ref_id)
    assert lookup.status == "unknown"
    assert registry.current_generation("s1") == 0


def test_bump_generation_is_idempotent_on_fresh_session() -> None:
    registry = ElementRegistry()
    registry.bump_generation("brand-new")
    assert registry.current_generation("brand-new") == 1
    registry.bump_generation("brand-new")
    assert registry.current_generation("brand-new") == 2


def test_allocate_refs_after_bump_uses_new_generation() -> None:
    registry = ElementRegistry()
    [old_id] = registry.allocate_refs("s1", [_FakeElement("old")])
    registry.bump_generation("s1")
    [new_id] = registry.allocate_refs("s1", [_FakeElement("new")])

    # Old id is stale; new id is fresh.
    assert registry.lookup_ref("s1", old_id).status == "stale"
    assert registry.lookup_ref("s1", new_id).status == "ok"


def test_session_isolation() -> None:
    """Refs allocated for one session are unknown to another."""
    registry = ElementRegistry()
    [ref_id] = registry.allocate_refs("s1", [_FakeElement("x")])
    assert registry.lookup_ref("s2", ref_id).status == "unknown"
