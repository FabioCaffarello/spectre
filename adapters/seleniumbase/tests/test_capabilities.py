"""Unit tests for the capability declarations and coherence invariant."""

from __future__ import annotations

import pytest

from spectre_seleniumbase.capabilities import (
    CAPABILITY_NAMES,
    EXTRACT_ATTRIBUTE,
    EXTRACT_EVAL,
    EXTRACT_HTML,
    EXTRACT_TEXT,
    JS_EXECUTION,
    MODE_EVAL,
    NAVIGATION,
    QUERY_ATTRIBUTE,
    QUERY_CSS,
    QUERY_TEXT,
    QUERY_XPATH,
    SCREENSHOT_ELEMENT,
    SCREENSHOT_FULL_PAGE,
    SCREENSHOT_VIEWPORT,
    assert_capability_coherence,
    missing_capability_for_mode,
)


def test_pr10_declares_twelve_alphabetical_capabilities() -> None:
    """ADR-0015 §5: SeleniumBase declares twelve capability names —
    the eleven content-and-navigation names plus the two screenshot
    names it can deliver honestly. ``screenshot_full_page`` is
    deliberately absent."""

    assert CAPABILITY_NAMES == (
        EXTRACT_ATTRIBUTE,
        EXTRACT_EVAL,
        EXTRACT_HTML,
        EXTRACT_TEXT,
        JS_EXECUTION,
        NAVIGATION,
        QUERY_ATTRIBUTE,
        QUERY_CSS,
        QUERY_TEXT,
        QUERY_XPATH,
        SCREENSHOT_ELEMENT,
        SCREENSHOT_VIEWPORT,
    )
    assert len(CAPABILITY_NAMES) == 12


def test_screenshot_full_page_is_omitted_from_declared_list() -> None:
    """The single most architecturally significant assertion in this
    suite — ADR-0015 §5. SeleniumBase declares a strict subset of the
    Playwright adapter's screenshot capabilities."""

    assert SCREENSHOT_FULL_PAGE not in CAPABILITY_NAMES


def test_capability_names_is_alphabetical() -> None:
    """List order is the contract for the byte-for-byte assertion."""
    names = list(CAPABILITY_NAMES)
    assert names == sorted(names)


def test_coherence_accepts_pr10_list() -> None:
    """The PR10 declared tuple must satisfy the coherence invariant."""
    assert_capability_coherence(CAPABILITY_NAMES)


def test_coherence_accepts_eval_with_js_execution() -> None:
    assert_capability_coherence(
        (EXTRACT_EVAL, JS_EXECUTION, NAVIGATION),
    )


def test_coherence_rejects_eval_without_js_execution() -> None:
    with pytest.raises(ValueError, match="extract_eval requires js_execution"):
        assert_capability_coherence((EXTRACT_EVAL, NAVIGATION))


def test_coherence_accepts_empty_list() -> None:
    """A driver with no declared capabilities is technically coherent —
    the invariant only fires when ``extract_eval`` is present without
    ``js_execution``."""
    assert_capability_coherence(())


def test_mode_eval_gate_misses_when_js_execution_not_declared() -> None:
    assert missing_capability_for_mode(MODE_EVAL, (NAVIGATION,)) == JS_EXECUTION


def test_mode_eval_gate_passes_when_js_execution_declared() -> None:
    assert missing_capability_for_mode(MODE_EVAL, (JS_EXECUTION,)) is None


def test_mode_eval_gate_passes_against_pr10_declared_list() -> None:
    """The runtime gate confirms that the live SeleniumBase list lets
    MODE_EVAL through; the negative case is covered by
    ``test_mode_eval_gate_misses_when_js_execution_not_declared``."""

    assert missing_capability_for_mode(MODE_EVAL, CAPABILITY_NAMES) is None


def test_non_eval_modes_never_gate() -> None:
    # MODE_TEXT_CONTENT (1), MODE_INNER_TEXT (2), etc. should never
    # trip the gate regardless of declared capabilities.
    for mode in (0, 1, 2, 3, 4, 5):
        assert missing_capability_for_mode(mode, ()) is None
