"""Canonical capability-name constants.

The Driver Protocol uses ``repeated string`` for capability
declarations (see ``proto/spectre/driver/v1alpha1/capabilities.proto``).
Test code references the constants below rather than embedding raw
string literals; a typo at the call site becomes an import-time
``AttributeError`` instead of a silent runtime mismatch.

The list mirrors the comment block in ``capabilities.proto``. PR3
declares no capabilities at runtime — these constants exist so the
conformance suite has somewhere to import from once PR4 onward
implements real RPCs.
"""

from __future__ import annotations

from typing import Final

NAVIGATION: Final[str] = "navigation"
JS_EXECUTION: Final[str] = "js_execution"
NETWORK_INTERCEPT: Final[str] = "network_intercept"
SCREENSHOT_FULL_PAGE: Final[str] = "screenshot_full_page"
COOKIES_PERSIST: Final[str] = "cookies_persist"
HEADER_OVERRIDES: Final[str] = "header_overrides"
PROXY_PER_SESSION: Final[str] = "proxy_per_session"
CDP_PASSTHROUGH: Final[str] = "cdp_passthrough"
MULTIPAGE_CONCURRENT: Final[str] = "multipage_concurrent"

ALL: Final[frozenset[str]] = frozenset(
    {
        NAVIGATION,
        JS_EXECUTION,
        NETWORK_INTERCEPT,
        SCREENSHOT_FULL_PAGE,
        COOKIES_PERSIST,
        HEADER_OVERRIDES,
        PROXY_PER_SESSION,
        CDP_PASSTHROUGH,
        MULTIPAGE_CONCURRENT,
    }
)
