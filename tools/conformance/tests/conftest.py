"""Pytest fixtures for the conformance suite."""

from __future__ import annotations

import shutil
from collections.abc import Iterator
from pathlib import Path

import pytest
import yaml

from spectre_conformance.harness import DriverHarness
from spectre_conformance.http_fixture import LocalHttpServer

REPO_ROOT = Path(__file__).resolve().parents[3]
PLAYWRIGHT_DIR = REPO_ROOT / "adapters" / "playwright"
PLAYWRIGHT_MANIFEST = PLAYWRIGHT_DIR / "driver.yaml"
PLAYWRIGHT_DIST = PLAYWRIGHT_DIR / "dist" / "index.js"

SELENIUMBASE_DIR = REPO_ROOT / "adapters" / "seleniumbase"
SELENIUMBASE_MANIFEST = SELENIUMBASE_DIR / "driver.yaml"
# The SeleniumBase adapter runs from its own uv-managed virtualenv —
# the conformance suite's venv has spectre-driver-protocol but not
# seleniumbase. Pointing at the adapter's `python` keeps environments
# isolated, mirroring how the Playwright fixture uses the adapter's
# own Node toolchain via `dist/index.js`.
SELENIUMBASE_VENV_PY = SELENIUMBASE_DIR / ".venv" / "bin" / "python"

CURL_IMPERSONATE_DIR = REPO_ROOT / "adapters" / "curl-impersonate"
CURL_IMPERSONATE_MANIFEST = CURL_IMPERSONATE_DIR / "driver.yaml"
CURL_IMPERSONATE_BIN = CURL_IMPERSONATE_DIR / "bin" / "adapter"
CURL_IMPERSONATE_VARIANT = "curl_chrome116"


@pytest.fixture(scope="session")
def local_http_server() -> Iterator[LocalHttpServer]:
    """Yield a started local HTTP server with the four conformance routes.

    Session-scoped so all tests in a pytest invocation share a single
    server. See ``spectre_conformance.http_fixture`` for the routes
    and ADR-0009 for the rationale (no public-internet calls in
    conformance tests).
    """

    with LocalHttpServer() as server:
        yield server


@pytest.fixture
def playwright_adapter() -> Iterator[DriverHarness]:
    """Yield a started DriverHarness pointed at the Playwright adapter.

    Skips if the adapter has not been built. Locally that's a sign
    the developer should run ``just pw-build``; in CI ``just
    conf-test`` depends on ``just pw-build`` so the artifact is
    always present when the tests run.
    """

    if not PLAYWRIGHT_DIST.exists():
        pytest.skip(f"playwright adapter not built at {PLAYWRIGHT_DIST}; run `just pw-build` first")

    harness = DriverHarness.from_driver_yaml(PLAYWRIGHT_MANIFEST)
    with harness:
        yield harness


@pytest.fixture
def playwright_manifest() -> dict[str, object]:
    """Return the parsed Playwright ``driver.yaml`` manifest."""

    return yaml.safe_load(PLAYWRIGHT_MANIFEST.read_text())  # type: ignore[no-any-return]


@pytest.fixture
def seleniumbase_adapter() -> Iterator[DriverHarness]:
    """Yield a started DriverHarness pointed at the SeleniumBase adapter.

    Skips when the adapter's uv-managed virtualenv is missing — that
    is the local signal to run ``just sb-bootstrap`` (or, equivalently,
    ``cd adapters/seleniumbase && uv sync --all-extras --dev``). CI's
    ``just conf-test`` depends on ``just sb-bootstrap`` so the venv is
    always present when the tests run there.

    Unlike the Playwright fixture, this one does *not* call
    ``DriverHarness.from_driver_yaml`` — the manifest's command is
    ``["python", "-m", ...]`` and PATH-resolved ``python`` would
    typically point at the conformance venv (which has no
    seleniumbase install). The fixture instead substitutes the
    adapter's own venv python so each adapter stays in its own
    isolated environment, mirroring how the Playwright adapter uses
    its own ``node`` runtime.
    """

    if not SELENIUMBASE_VENV_PY.exists():
        pytest.skip(
            f"seleniumbase adapter venv not present at {SELENIUMBASE_VENV_PY}; "
            "run `just sb-bootstrap` first"
        )

    harness = DriverHarness(
        command=[str(SELENIUMBASE_VENV_PY), "-m", "spectre_seleniumbase.adapter"],
        cwd=SELENIUMBASE_DIR,
    )
    with harness:
        yield harness


@pytest.fixture
def seleniumbase_manifest() -> dict[str, object]:
    """Return the parsed SeleniumBase ``driver.yaml`` manifest."""

    return yaml.safe_load(SELENIUMBASE_MANIFEST.read_text())  # type: ignore[no-any-return]


@pytest.fixture
def curl_impersonate_adapter() -> Iterator[DriverHarness]:
    """Yield a started DriverHarness pointed at the curl-impersonate adapter.

    Skips when:

    - the adapter binary has not been built (run ``just curl-imp-build``);
    - the curl-impersonate variant the adapter expects is not on
      PATH (install it from
      <https://github.com/lwthiker/curl-impersonate/releases>).

    CI's ``just conf-test`` depends on ``just ci-bootstrap`` which
    builds the adapter and ensures the curl-impersonate binary is
    available before the tests run.

    PR11 implements ``Initialize`` and ``Navigate``; the other
    RPCs return ``codes.Unimplemented`` until PR12. ADR-0016
    records the third-runtime decisions.
    """

    if not CURL_IMPERSONATE_BIN.exists():
        pytest.skip(
            f"curl-impersonate adapter binary not built at {CURL_IMPERSONATE_BIN}; "
            "run `just curl-imp-build` first"
        )
    if shutil.which(CURL_IMPERSONATE_VARIANT) is None:
        pytest.skip(
            f"{CURL_IMPERSONATE_VARIANT} not on PATH; install from "
            "https://github.com/lwthiker/curl-impersonate/releases"
        )

    harness = DriverHarness.from_driver_yaml(CURL_IMPERSONATE_MANIFEST)
    with harness:
        yield harness


@pytest.fixture
def curl_impersonate_manifest() -> dict[str, object]:
    """Return the parsed curl-impersonate ``driver.yaml`` manifest."""

    return yaml.safe_load(CURL_IMPERSONATE_MANIFEST.read_text())  # type: ignore[no-any-return]
