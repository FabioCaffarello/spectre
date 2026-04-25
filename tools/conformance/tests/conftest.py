"""Pytest fixtures for the conformance suite."""

from __future__ import annotations

from collections.abc import Iterator
from pathlib import Path

import pytest
import yaml

from spectre_conformance.harness import DriverHarness

REPO_ROOT = Path(__file__).resolve().parents[3]
PLAYWRIGHT_DIR = REPO_ROOT / "adapters" / "playwright"
PLAYWRIGHT_MANIFEST = PLAYWRIGHT_DIR / "driver.yaml"
PLAYWRIGHT_DIST = PLAYWRIGHT_DIR / "dist" / "index.js"


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
