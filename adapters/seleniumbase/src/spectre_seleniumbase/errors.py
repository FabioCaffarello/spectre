"""Selenium-failure → DriverError mapping.

The gRPC handlers catch Selenium / SeleniumBase errors and return
a populated ``DriverError`` rather than letting the exception
propagate as a transport-level failure. The table below is the
single source of truth for that translation; ADR-0014 §3 records
the rationale for the Navigate-relevant rows and ADR-0015 §2 / §4
record the additions PR10 needed for ``Query``, ``Extract``, and
``Screenshot``.

The shape mirrors the Playwright adapter's ``errors.ts``: a
sequence of rules tried in order against the exception's class
and message, with a catch-all that collapses to
``CODE_INTERNAL`` so an unmapped Selenium failure never escapes
as a transport exception. The v1alpha1 ``DriverError.Code`` enum
is frozen (ADR-0004); the same enum gaps documented in ADR-0009
apply here (no dedicated ``UNAVAILABLE`` for missing binaries,
no ``NETWORK`` split, no ``UNKNOWN``).

Two stale-message constants are exported so the ``Extract`` and
element-scoped ``Screenshot`` handlers can keep the messages
identical across call sites and so the conformance tests can
match on them. ADR-0015 §2 explains why two messages share one
wire code.
"""

from __future__ import annotations

import re
from dataclasses import dataclass

from selenium.common.exceptions import (
    ElementNotInteractableException,
    InvalidSelectorException,
    InvalidSessionIdException,
    JavascriptException,
    MoveTargetOutOfBoundsException,
    NoSuchElementException,
    SessionNotCreatedException,
    StaleElementReferenceException,
    TimeoutException,
    WebDriverException,
)
from spectre.driver.v1alpha1 import errors_pb2

NETWORK_ERROR_PATTERN = re.compile(r"net::ERR_[A-Z_]+")
BROWSER_MISSING_PATTERN = re.compile(
    r"chrome not reachable|cannot find chrome|chromedriver|"
    r"unable to find binary|no such binary|chrome binary",
    re.IGNORECASE,
)

# Two distinct stale-ref messages, both carried over the wire as
# ``CODE_INVALID_ARGUMENT``. Documented in ADR-0015 §2.
STALE_NAVIGATE_MESSAGE = "element reference is stale; query was performed before a navigation"
STALE_PAGE_STATE_CHANGE_MESSAGE = "element became stale during page state change"
UNKNOWN_REF_MESSAGE = (
    "element reference not found in this session; "
    "ensure Query was called against the same session_id"
)


@dataclass(frozen=True)
class MappedError:
    """A protocol-level error code paired with a user-facing message."""

    code: int
    message: str


def _exc_message(exc: BaseException) -> str:
    """Best-effort textual rendering of a Selenium exception.

    Selenium's exceptions stringify with a long Java-style stack header;
    we strip the trailing ``(Session info:...)`` and stack lines so the
    DriverError message stays readable.
    """

    raw = str(exc) or exc.__class__.__name__
    # Selenium puts diagnostic context after a newline; drop it.
    return raw.split("\n", maxsplit=1)[0].strip()


def selenium_error_to_driver_error(exc: BaseException) -> MappedError:
    """Map a Selenium / SeleniumBase exception onto ``DriverError``.

    The first matching rule wins. Returns a :class:`MappedError` whose
    ``code`` is one of ``DriverError.Code`` (an integer enum) and whose
    ``message`` preserves the original diagnostic.
    """

    code_enum = errors_pb2.DriverError
    message = _exc_message(exc)

    if isinstance(exc, TimeoutException):
        return MappedError(code=code_enum.CODE_TIMEOUT, message=message)

    # SessionNotCreatedException is raised when the driver fails to
    # start — typically because Chrome or ChromeDriver is missing.
    # Surface an actionable hint alongside the original message.
    if isinstance(exc, SessionNotCreatedException) or BROWSER_MISSING_PATTERN.search(message):
        hint = (
            "install Chrome and run `seleniumbase install chromedriver` "
            "to install the matching driver"
        )
        return MappedError(
            code=code_enum.CODE_INTERNAL,
            message=f"{message}\n{hint}",
        )

    if NETWORK_ERROR_PATTERN.search(message):
        return MappedError(code=code_enum.CODE_TARGET_UNREACHABLE, message=message)

    # PR10 additions (ADR-0015 §2 and §4).
    #
    # StaleElementReferenceException is the SPA-mutation case: the
    # WebElement handle's underlying DOM node was detached without a
    # protocol-level Navigate. Maps to CODE_INVALID_ARGUMENT with the
    # distinct page-state-change message; the post-Navigate stale
    # message lives on the `Extract` handler's pre-flight registry
    # check, not on this exception.
    if isinstance(exc, StaleElementReferenceException):
        return MappedError(
            code=code_enum.CODE_INVALID_ARGUMENT,
            message=STALE_PAGE_STATE_CHANGE_MESSAGE,
        )

    # NoSuchElementException can fire on `find_element` (singular) or
    # on a `WebElement` method whose underlying lookup failed mid-
    # generation. The wire shape mirrors Playwright's "element not
    # found in current DOM" — same INVALID_ARGUMENT code, message
    # preserved from Selenium so operator logs stay informative.
    if isinstance(exc, NoSuchElementException):
        return MappedError(code=code_enum.CODE_INVALID_ARGUMENT, message=message)

    # InvalidSelectorException → INVALID_ARGUMENT. A malformed XPath
    # or CSS expression is a client error, not a driver fault.
    if isinstance(exc, InvalidSelectorException):
        return MappedError(code=code_enum.CODE_INVALID_ARGUMENT, message=message)

    # ElementNotInteractableException is rare on the v1alpha1 surface
    # (no click/type RPCs) but can fire on a Screenshot whose target
    # element is hidden in a way Selenium refuses to scroll. Map to
    # INVALID_ARGUMENT so the client knows the element was the issue.
    if isinstance(exc, ElementNotInteractableException):
        return MappedError(code=code_enum.CODE_INVALID_ARGUMENT, message=message)

    # MoveTargetOutOfBoundsException can surface on element-scoped
    # screenshots when Selenium auto-scrolls to bring the target into
    # view and the element is off the document. INVALID_ARGUMENT
    # matches the "this element ref is not capturable" framing.
    if isinstance(exc, MoveTargetOutOfBoundsException):
        return MappedError(code=code_enum.CODE_INVALID_ARGUMENT, message=message)

    # JavascriptException is what `execute_script` raises when the
    # script body throws. Maps to INTERNAL because the failure is
    # inside the page's JS, not in the protocol layer; the v1alpha1
    # enum has no dedicated client-script-error code.
    if isinstance(exc, JavascriptException):
        return MappedError(code=code_enum.CODE_INTERNAL, message=message)

    if isinstance(exc, InvalidSessionIdException):
        return MappedError(code=code_enum.CODE_INTERNAL, message=message)

    if isinstance(exc, WebDriverException):
        return MappedError(code=code_enum.CODE_INTERNAL, message=message)

    return MappedError(code=code_enum.CODE_INTERNAL, message=message)
