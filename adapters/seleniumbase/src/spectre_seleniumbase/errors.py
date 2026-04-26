"""Selenium-failure → DriverError mapping.

The gRPC handlers catch Selenium / SeleniumBase errors and return
a populated ``DriverError`` rather than letting the exception
propagate as a transport-level failure. The table below is the
single source of truth for that translation; ADR-0014 §3 records
the rationale.

The shape mirrors the Playwright adapter's ``errors.ts``: a
sequence of rules tried in order against the exception's class
and message, with a catch-all that collapses to
``CODE_INTERNAL`` so an unmapped Selenium failure never escapes
as a transport exception. The v1alpha1 ``DriverError.Code`` enum
is frozen (ADR-0004); the same enum gaps documented in ADR-0009
apply here (no dedicated ``UNAVAILABLE`` for missing binaries,
no ``NETWORK`` split, no ``UNKNOWN``).
"""

from __future__ import annotations

import re
from dataclasses import dataclass

from selenium.common.exceptions import (
    InvalidSessionIdException,
    SessionNotCreatedException,
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

    if isinstance(exc, InvalidSessionIdException):
        return MappedError(code=code_enum.CODE_INTERNAL, message=message)

    if isinstance(exc, WebDriverException):
        return MappedError(code=code_enum.CODE_INTERNAL, message=message)

    return MappedError(code=code_enum.CODE_INTERNAL, message=message)
