"""Unit tests for the Selenium-to-DriverError mapping.

The mapping is the most reusable artifact of PR9 (and PR10/PR11
will add rows to it). These tests cover every Navigate-relevant
row in the ADR-0014 §3 table.
"""

from __future__ import annotations

from selenium.common.exceptions import (
    InvalidSessionIdException,
    SessionNotCreatedException,
    TimeoutException,
    WebDriverException,
)
from spectre.driver.v1alpha1 import errors_pb2

from spectre_seleniumbase.errors import selenium_error_to_driver_error


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
