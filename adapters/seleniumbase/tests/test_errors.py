"""Unit tests for the Selenium-to-DriverError mapping.

The mapping is the most reusable artifact of PR9 (and PR10/PR11
will add rows to it). These tests cover every Navigate-relevant
row in the ADR-0014 §3 table.
"""

from __future__ import annotations

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

from spectre_seleniumbase.errors import (
    STALE_PAGE_STATE_CHANGE_MESSAGE,
    selenium_error_to_driver_error,
)


def test_timeout_exception_maps_to_code_timeout() -> None:
    mapped = selenium_error_to_driver_error(TimeoutException("page load timed out"))
    assert mapped.code == errors_pb2.DriverError.CODE_TIMEOUT
    assert "timed out" in mapped.message


def test_dns_failure_maps_to_target_unreachable() -> None:
    exc = WebDriverException("net::ERR_NAME_NOT_RESOLVED resolving https://nope.invalid")
    mapped = selenium_error_to_driver_error(exc)
    assert mapped.code == errors_pb2.DriverError.CODE_TARGET_UNREACHABLE
    assert "ERR_NAME_NOT_RESOLVED" in mapped.message


def test_connection_refused_maps_to_target_unreachable() -> None:
    exc = WebDriverException("net::ERR_CONNECTION_REFUSED")
    mapped = selenium_error_to_driver_error(exc)
    assert mapped.code == errors_pb2.DriverError.CODE_TARGET_UNREACHABLE


def test_session_not_created_maps_to_internal_with_hint() -> None:
    exc = SessionNotCreatedException("could not create a new session")
    mapped = selenium_error_to_driver_error(exc)
    assert mapped.code == errors_pb2.DriverError.CODE_INTERNAL
    assert "seleniumbase install chromedriver" in mapped.message


def test_chromedriver_missing_message_maps_to_internal_with_hint() -> None:
    exc = WebDriverException("Message: 'chromedriver' executable needs to be in PATH")
    mapped = selenium_error_to_driver_error(exc)
    assert mapped.code == errors_pb2.DriverError.CODE_INTERNAL
    assert "seleniumbase install chromedriver" in mapped.message


def test_invalid_session_id_maps_to_internal() -> None:
    exc = InvalidSessionIdException("session id is invalid")
    mapped = selenium_error_to_driver_error(exc)
    assert mapped.code == errors_pb2.DriverError.CODE_INTERNAL


def test_generic_webdriver_exception_falls_back_to_internal() -> None:
    exc = WebDriverException("something exploded")
    mapped = selenium_error_to_driver_error(exc)
    assert mapped.code == errors_pb2.DriverError.CODE_INTERNAL
    assert "exploded" in mapped.message


def test_non_webdriver_exception_falls_back_to_internal() -> None:
    mapped = selenium_error_to_driver_error(RuntimeError("plain error"))
    assert mapped.code == errors_pb2.DriverError.CODE_INTERNAL
    assert mapped.message == "plain error"


# -- PR10 additions (ADR-0015 §2 and §4) --------------------------------


def test_stale_element_maps_to_invalid_argument_with_page_state_change_message() -> None:
    """The StaleElementReferenceException case is the SPA-mutation
    path. The post-Navigate stale path uses a different message,
    enforced by the Extract handler's pre-flight registry check;
    this row exists so any unhandled stale exception still maps
    cleanly."""

    exc = StaleElementReferenceException("stale element reference: element is not attached")
    mapped = selenium_error_to_driver_error(exc)
    assert mapped.code == errors_pb2.DriverError.CODE_INVALID_ARGUMENT
    assert mapped.message == STALE_PAGE_STATE_CHANGE_MESSAGE


def test_no_such_element_maps_to_invalid_argument() -> None:
    exc = NoSuchElementException("no such element: Unable to locate element")
    mapped = selenium_error_to_driver_error(exc)
    assert mapped.code == errors_pb2.DriverError.CODE_INVALID_ARGUMENT
    assert "no such element" in mapped.message.lower()


def test_invalid_selector_maps_to_invalid_argument() -> None:
    exc = InvalidSelectorException("invalid selector: Compound class names not permitted")
    mapped = selenium_error_to_driver_error(exc)
    assert mapped.code == errors_pb2.DriverError.CODE_INVALID_ARGUMENT


def test_element_not_interactable_maps_to_invalid_argument() -> None:
    exc = ElementNotInteractableException("element not interactable")
    mapped = selenium_error_to_driver_error(exc)
    assert mapped.code == errors_pb2.DriverError.CODE_INVALID_ARGUMENT


def test_move_target_out_of_bounds_maps_to_invalid_argument() -> None:
    exc = MoveTargetOutOfBoundsException("move target out of bounds")
    mapped = selenium_error_to_driver_error(exc)
    assert mapped.code == errors_pb2.DriverError.CODE_INVALID_ARGUMENT


def test_javascript_exception_maps_to_internal() -> None:
    exc = JavascriptException("javascript error: Cannot read properties of null")
    mapped = selenium_error_to_driver_error(exc)
    assert mapped.code == errors_pb2.DriverError.CODE_INTERNAL
    assert "javascript" in mapped.message.lower()


def test_message_strips_selenium_diagnostic_tail() -> None:
    exc = WebDriverException(
        "unknown error: net::ERR_CONNECTION_REFUSED\n"
        "  (Session info: chrome=130.0)\n"
        "Stacktrace:\n  at ..."
    )
    mapped = selenium_error_to_driver_error(exc)
    assert "\n" not in mapped.message
    assert "Session info" not in mapped.message
    assert mapped.code == errors_pb2.DriverError.CODE_TARGET_UNREACHABLE
