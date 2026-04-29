"""Smoke tests for the spectre_seleniumbase package."""

from __future__ import annotations

import pytest

from spectre_seleniumbase import PROTOCOL_VERSION, __version__
from spectre_seleniumbase.adapter import PORT_ENV_VAR, identity, resolve_port


def test_protocol_version() -> None:
    assert PROTOCOL_VERSION == "spectre.driver.v1alpha1"


def test_identity_contains_version_and_protocol() -> None:
    out = identity()
    assert __version__ in out
    assert PROTOCOL_VERSION in out


def test_resolve_port_reads_env_var() -> None:
    assert resolve_port({PORT_ENV_VAR: "8092"}) == 8092


def test_resolve_port_accepts_zero() -> None:
    assert resolve_port({PORT_ENV_VAR: "0"}) == 0


def test_resolve_port_requires_env_var() -> None:
    with pytest.raises(SystemExit, match=PORT_ENV_VAR):
        resolve_port({})


def test_resolve_port_rejects_empty_value() -> None:
    with pytest.raises(SystemExit, match=PORT_ENV_VAR):
        resolve_port({PORT_ENV_VAR: ""})


def test_resolve_port_rejects_non_integer() -> None:
    with pytest.raises(SystemExit, match="port number"):
        resolve_port({PORT_ENV_VAR: "abc"})


def test_resolve_port_rejects_out_of_range() -> None:
    with pytest.raises(SystemExit, match="between 0 and 65535"):
        resolve_port({PORT_ENV_VAR: "70000"})
