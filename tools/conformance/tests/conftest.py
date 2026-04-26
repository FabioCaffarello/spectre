"""Pytest fixtures for the conformance suite."""

from __future__ import annotations

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
