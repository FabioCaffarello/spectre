"""Unit tests for the capability declarations and coherence invariant."""

from __future__ import annotations

import pytest

from spectre_seleniumbase.capabilities import (
    CAPABILITY_NAMES,
    EXTRACT_EVAL,
    JS_EXECUTION,
    MODE_EVAL,
    NAVIGATION,
    assert_capability_coherence,
    missing_capability_for_mode,
)


def test_pr9_declares_only_navigation() -> None:
    """ADR-0014 §1: PR9's capability list is ``["navigation"]``.

    Future PRs will append entries; this assertion guards against an
    accidental mass-declaration before the corresponding RPCs and
    conformance tests land.
    """
    assert CAPABILITY_NAMES == (NAVIGATION,)


def test_capability_names_is_alphabetical() -> None:
    """List order is the contract for the byte-for-byte assertion."""
    names = list(CAPABILITY_NAMES)
    assert names == sorted(names)


def test_coherence_accepts_navigation_only() -> None:
    # Single canonical case: PR9's tuple satisfies the invariant.
    assert_capability_coherence((NAVIGATION,))


def test_coherence_accepts_eval_with_js_execution() -> None:
    # Forward-looking: the tuple PR10 will declare must pass.
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


def test_non_eval_modes_never_gate() -> None:
    # MODE_TEXT_CONTENT (1), MODE_INNER_TEXT (2), etc. should never
    # trip the gate regardless of declared capabilities.
    for mode in (0, 1, 2, 3, 4, 5):
        assert missing_capability_for_mode(mode, ()) is None
