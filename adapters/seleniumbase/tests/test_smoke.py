"""Smoke tests for the spectre_seleniumbase package."""

from __future__ import annotations

import pathlib

import pytest

from spectre_seleniumbase import PROTOCOL_VERSION, __version__
from spectre_seleniumbase.adapter import identity, resolve_socket_path


def test_protocol_version() -> None:
    assert PROTOCOL_VERSION == "spectre.driver.v1alpha1"


def test_identity_contains_version_and_protocol() -> None:
    out = identity()
    assert __version__ in out
    assert PROTOCOL_VERSION in out


def test_resolve_socket_path_prefers_cli_flag() -> None:
    path = resolve_socket_path(
        ["--socket=/tmp/cli.sock"],
        {"SPECTRE_DRIVER_SOCKET": "/tmp/env.sock"},
    )
    assert path == pathlib.Path("/tmp/cli.sock")


def test_resolve_socket_path_falls_back_to_env() -> None:
    path = resolve_socket_path([], {"SPECTRE_DRIVER_SOCKET": "/tmp/env.sock"})
    assert path == pathlib.Path("/tmp/env.sock")


def test_resolve_socket_path_rejects_relative() -> None:
    with pytest.raises(SystemExit, match="must be absolute"):
        resolve_socket_path(["--socket=relative.sock"], {})


def test_resolve_socket_path_requires_some_input() -> None:
    with pytest.raises(SystemExit, match="no socket path"):
        resolve_socket_path([], {})
