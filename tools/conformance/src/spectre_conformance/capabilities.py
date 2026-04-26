"""Canonical capability-name constants.

The Driver Protocol uses ``repeated string`` for capability
declarations (see ``proto/spectre/driver/v1alpha1/capabilities.proto``).
Test code references the constants below rather than embedding raw
string literals; a typo at the call site becomes an import-time
``AttributeError`` instead of a silent runtime mismatch.

The list mirrors the comment block in ``capabilities.proto``. PR3
declared no capabilities at runtime; PR4 added ``navigation`` and
``js_execution``; PR5 added the eight ``query_*``/``extract_*``
names covering ``Query`` and ``Extract``; PR6 adds the three
``screenshot_*`` names covering the three ``Screenshot`` scopes.
Future RPCs add to ``ALL`` as they ship.
"""

from __future__ import annotations

from typing import Final

NAVIGATION: Final[str] = "navigation"
JS_EXECUTION: Final[str] = "js_execution"
QUERY_CSS: Final[str] = "query_css"
QUERY_XPATH: Final[str] = "query_xpath"
QUERY_TEXT: Final[str] = "query_text"
QUERY_ATTRIBUTE: Final[str] = "query_attribute"
EXTRACT_TEXT: Final[str] = "extract_text"
EXTRACT_HTML: Final[str] = "extract_html"
EXTRACT_ATTRIBUTE: Final[str] = "extract_attribute"
EXTRACT_EVAL: Final[str] = "extract_eval"

SCREENSHOT_ELEMENT: Final[str] = "screenshot_element"
SCREENSHOT_FULL_PAGE: Final[str] = "screenshot_full_page"
SCREENSHOT_VIEWPORT: Final[str] = "screenshot_viewport"

NETWORK_INTERCEPT: Final[str] = "network_intercept"
COOKIES_PERSIST: Final[str] = "cookies_persist"
HEADER_OVERRIDES: Final[str] = "header_overrides"
PROXY_PER_SESSION: Final[str] = "proxy_per_session"
CDP_PASSTHROUGH: Final[str] = "cdp_passthrough"
MULTIPAGE_CONCURRENT: Final[str] = "multipage_concurrent"

ALL: Final[frozenset[str]] = frozenset(
    {
        NAVIGATION,
        JS_EXECUTION,
        QUERY_CSS,
        QUERY_XPATH,
        QUERY_TEXT,
        QUERY_ATTRIBUTE,
        EXTRACT_TEXT,
        EXTRACT_HTML,
        EXTRACT_ATTRIBUTE,
        EXTRACT_EVAL,
        SCREENSHOT_ELEMENT,
        SCREENSHOT_FULL_PAGE,
        SCREENSHOT_VIEWPORT,
        NETWORK_INTERCEPT,
        COOKIES_PERSIST,
        HEADER_OVERRIDES,
        PROXY_PER_SESSION,
        CDP_PASSTHROUGH,
        MULTIPAGE_CONCURRENT,
    }
)
