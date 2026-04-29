"""Capabilities declared by the SeleniumBase adapter at handshake time.

Each capability lands once its RPC and the conformance tests for it
ship together — see ADR-0014 §1. The declared set is ``navigation``,
``js_execution``, the four ``query_*`` names, the four ``extract_*``
names, and **two** of the three ``screenshot_*`` names.
``screenshot_full_page`` is intentionally absent — Selenium
WebDriver has no reliable, browser-independent full-page capture
API and the capability progression contract says declared = tested.
ADR-0015 §5 records the rationale; this is the first time a driver
declares a strict subset of another driver's capabilities.

The exported value MUST stay in lockstep with ``driver.yaml``'s
``capabilities:`` block — the conformance suite asserts byte-for-byte
equality at runtime, including order.

The capability mechanism splits into two roles (see ADR-0010 §3):

- Descriptive declarations describe what the adapter can do so a
  future engine can plan whether a job will succeed against this
  driver. They do not gate behaviour at runtime.
- Gating capabilities bind a specific runtime path to a declared
  capability. v1alpha1 has one gating capability: ``js_execution``,
  which gates ``MODE_EVAL`` extracts. The Extract handler calls
  ``missing_capability_for_mode`` at the start of each request; an
  under-declared driver fails the whole request with
  ``CODE_CAPABILITY_MISSING`` rather than partial-success.

Order: alphabetical by capability name. The conformance suite
asserts list-equality (not set-equality) between this constant
and the manifest, so a deterministic order is required.
"""

from __future__ import annotations

# --- Capability constants -------------------------------------------------

EXTRACT_ATTRIBUTE = "extract_attribute"
EXTRACT_EVAL = "extract_eval"
EXTRACT_HTML = "extract_html"
EXTRACT_TEXT = "extract_text"
JS_EXECUTION = "js_execution"
NAVIGATION = "navigation"
QUERY_ATTRIBUTE = "query_attribute"
QUERY_CSS = "query_css"
QUERY_TEXT = "query_text"
QUERY_XPATH = "query_xpath"
SCREENSHOT_ELEMENT = "screenshot_element"
# ``screenshot_full_page`` exists as a constant for cross-reference
# in the Screenshot handler (which rejects FULL_PAGE requests with a
# message naming this string), but is **not** added to
# ``CAPABILITY_NAMES``. ADR-0015 §5 records the omission.
SCREENSHOT_FULL_PAGE = "screenshot_full_page"
SCREENSHOT_VIEWPORT = "screenshot_viewport"

CAPABILITY_NAMES: tuple[str, ...] = (
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
"""The declared capability list. ``driver.yaml`` mirrors this exactly.

Twelve entries in alphabetical order, matching the Playwright
adapter's eleven content-and-navigation capabilities plus
``js_execution`` and the two screenshot capabilities the
SeleniumBase implementation can deliver honestly. The thirteenth
Playwright name (``screenshot_full_page``) is omitted; ADR-0015 §5.
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

    The Extract handler calls this before evaluating each field; a
    declared capability list missing ``js_execution`` rejects every
    ``MODE_EVAL`` field at the start of the request.
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
