"""Capabilities declared by the SeleniumBase adapter at handshake time.

Each capability lands once its RPC and the conformance tests for it
ship together — see ADR-0014 §1. PR9 declares ``navigation`` only.
PR10 will add ``js_execution`` plus the four ``query_*`` and four
``extract_*`` names; PR11 will add the three ``screenshot_*`` names.
The exported value MUST stay in lockstep with ``driver.yaml``'s
``capabilities:`` block — the conformance suite asserts byte-for-byte
equality at runtime, including order.

The capability mechanism splits into two roles (see ADR-0010):

- Descriptive declarations describe what the adapter can do so a
  future engine can plan whether a job will succeed against this
  driver. They do not gate behaviour at runtime.
- Gating capabilities bind a specific runtime path to a declared
  capability. v1alpha1 has one gating capability: ``js_execution``,
  which gates ``MODE_EVAL`` extracts. PR9 declares neither
  ``js_execution`` nor ``extract_eval``, so the gate is a no-op
  today; the function exists so PR10 can rely on it.

Order: alphabetical by capability name. The conformance suite
asserts list-equality (not set-equality) between this constant
and the manifest, so a deterministic order is required.
"""

from __future__ import annotations

# --- Capability constants -------------------------------------------------

# PR10 will introduce these:
EXTRACT_ATTRIBUTE = "extract_attribute"
EXTRACT_EVAL = "extract_eval"
EXTRACT_HTML = "extract_html"
EXTRACT_TEXT = "extract_text"
JS_EXECUTION = "js_execution"
QUERY_ATTRIBUTE = "query_attribute"
QUERY_CSS = "query_css"
QUERY_TEXT = "query_text"
QUERY_XPATH = "query_xpath"

# PR9 declares this one only:
NAVIGATION = "navigation"

# PR11 will introduce these:
SCREENSHOT_ELEMENT = "screenshot_element"
SCREENSHOT_FULL_PAGE = "screenshot_full_page"
SCREENSHOT_VIEWPORT = "screenshot_viewport"

CAPABILITY_NAMES: tuple[str, ...] = (NAVIGATION,)
"""The declared capability list. ``driver.yaml`` mirrors this exactly.

PR9 declares ``navigation`` only (ADR-0014 §1); future PRs append
the names whose RPCs and conformance tests land in the same PR.
"""

DRIVER_VERSION = "0.1.0a0"

# --- MODE_EVAL gate -------------------------------------------------------

# Mirrors `Field.Mode.MODE_EVAL` in extraction.proto. Defined as a
# plain int constant so the coherence check stays a pure function and
# does not have to import the generated bindings at module load. See
# ADR-0010 §3.
MODE_EVAL = 6


def has_capability(declared: tuple[str, ...], name: str) -> bool:
    """Return ``True`` if the declared list contains the named capability."""
    return name in declared


def missing_capability_for_mode(mode: int, declared: tuple[str, ...]) -> str | None:
    """Return the missing capability name if a ``Field.Mode`` would be gated.

    PR9 has no Extract handler so this function is unused on the live
    path. PR10 will call it before evaluating each field. Defining it
    now keeps the gate symmetric with the Playwright adapter and lets
    PR10 add the call site without touching this module.
    """
    if mode == MODE_EVAL and not has_capability(declared, JS_EXECUTION):
        return JS_EXECUTION
    return None


def assert_capability_coherence(declared: tuple[str, ...]) -> None:
    """Raise if the declared list violates a coherence invariant.

    Currently:

    - ``extract_eval`` declared without ``js_execution`` is a
      contradiction (the runtime gate would reject every ``MODE_EVAL``
      call).

    Extensible: add rows as new capabilities imply each other. The same
    invariant ADR-0010 introduced for the Playwright adapter applies to
    every driver; ADR-0014 §1 records the carry-over.
    """
    if EXTRACT_EVAL in declared and JS_EXECUTION not in declared:
        raise ValueError("capability coherence violation: extract_eval requires js_execution")


# Run the coherence check at module load so an inconsistent
# CAPABILITY_NAMES tuple fails the import rather than the first RPC.
assert_capability_coherence(CAPABILITY_NAMES)
