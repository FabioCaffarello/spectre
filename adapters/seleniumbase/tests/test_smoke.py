"""Smoke tests for the spectre_seleniumbase package."""

from __future__ import annotations

from spectre_seleniumbase import PROTOCOL_VERSION, __version__
from spectre_seleniumbase.adapter import identity


def test_protocol_version() -> None:
    assert PROTOCOL_VERSION == "spectre.driver.v1alpha1"


def test_identity_contains_version_and_protocol() -> None:
    out = identity()
    assert __version__ in out
    assert PROTOCOL_VERSION in out
