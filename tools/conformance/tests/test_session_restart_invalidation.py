"""Restart-invalidation conformance test (R4.3 / ADR-0023 §5).

The §5 contract: a session whose Redis metadata names a different
``adapter_instance_id`` than the live adapter's must surface as a
gRPC ``UNAVAILABLE`` error on every non-Initialize RPC. This is
the architectural key of R4.3 — every reference adapter
implements it the same way.

Test pattern (phase prompt §4.4): start two adapter instances
that share a Redis backing store but have distinct
``SPECTRE_ADAPTER_INSTANCE_ID`` values. Initialize a session via
instance A, verify Redis has the document, then dial instance B
with A's session_id and assert ``grpc.StatusCode.UNAVAILABLE``
with the documented message. The pattern uses parallel instances
rather than an actual restart because:

  - the mechanism we exercise (instance_id comparison) is the
    same;
  - parallel instances are deterministic — no race against
    process termination + Redis observation;
  - the harness teardown is a single call rather than a stop +
    restart sequence.

The test runs against each adapter independently because the
three implementations are independent code paths in three
languages (TS, Python, Go). All three must hold.

ADR references:
  - ADR-0023 §4 (keyspace + value schema)
  - ADR-0023 §5 R4.3 addendum (adapter_instance_id mechanism)
  - ADR-0023 §6 (Redis required at startup)
"""

from __future__ import annotations

import os
import shutil
from collections.abc import Iterator
from pathlib import Path

import grpc
import pytest
import redis
from spectre.driver.v1alpha1 import driver_pb2, driver_pb2_grpc

from spectre_conformance.harness import DriverHarness

REPO_ROOT = Path(__file__).resolve().parents[3]
PLAYWRIGHT_DIR = REPO_ROOT / "adapters" / "playwright"
PLAYWRIGHT_MANIFEST = PLAYWRIGHT_DIR / "driver.yaml"
PLAYWRIGHT_DIST = PLAYWRIGHT_DIR / "dist" / "index.js"

SELENIUMBASE_DIR = REPO_ROOT / "adapters" / "seleniumbase"
SELENIUMBASE_VENV_PY = SELENIUMBASE_DIR / ".venv" / "bin" / "python"

CURL_IMPERSONATE_DIR = REPO_ROOT / "adapters" / "curl-impersonate"
CURL_IMPERSONATE_MANIFEST = CURL_IMPERSONATE_DIR / "driver.yaml"
CURL_IMPERSONATE_BIN = CURL_IMPERSONATE_DIR / "bin" / "adapter"
CURL_IMPERSONATE_VARIANT = "curl_chrome116"

PER_TEST_DEADLINE_S = 60.0
INSTANCE_A = "instance-aaaa"
INSTANCE_B = "instance-bbbb"

DEFAULT_REDIS_URL = "redis://127.0.0.1:6379/0"


def _redis_url() -> str:
    """The Redis URL the conformance run dials.

    Honours ``SPECTRE_REDIS_URL`` so the test runs against the
    same Redis the adapters use; falls back to the local-dev
    default (matches ``.env.example``).
    """
    return os.environ.get("SPECTRE_REDIS_URL") or DEFAULT_REDIS_URL


def _redis_reachable() -> bool:
    """Best-effort: is the configured Redis available?"""
    try:
        client = redis.Redis.from_url(_redis_url(), socket_timeout=2.0)
        client.ping()
        client.close()
    except Exception:
        return False
    return True


@pytest.fixture
def redis_client() -> Iterator[redis.Redis]:
    """Yield a sync redis-py client pointed at the test Redis.

    Skips when Redis is unreachable so the test fails loudly only
    when the prerequisite is genuinely set up.
    """
    if not _redis_reachable():
        pytest.skip(
            f"redis at {_redis_url()} is not reachable; bring it up with "
            "`docker compose up -d redis`"
        )
    client = redis.Redis.from_url(_redis_url(), decode_responses=True)
    try:
        yield client
    finally:
        client.close()


def _harness_pair(
    command: list[str],
    cwd: Path | None,
) -> tuple[DriverHarness, DriverHarness]:
    """Build two DriverHarnesses with distinct instance_id_overrides.

    Both share the same command/cwd so they behave like two
    instances of the same adapter binary, exactly as the §5
    addendum specifies.
    """
    harness_a = DriverHarness(
        command=command,
        cwd=cwd,
        instance_id_override=INSTANCE_A,
    )
    harness_b = DriverHarness(
        command=command,
        cwd=cwd,
        instance_id_override=INSTANCE_B,
    )
    return harness_a, harness_b


def _initialize(harness: DriverHarness) -> str:
    stub = driver_pb2_grpc.DriverStub(harness.dial())
    request = driver_pb2.InitializeRequest(
        protocol_version="spectre.driver.v1alpha1",
        session=driver_pb2.SessionConfig(),
        requested_capabilities=[],
    )
    response = stub.Initialize(request, timeout=10.0)
    assert response.session_id, "Initialize must return a session_id"
    return str(response.session_id)


def _navigate_expecting_unavailable(
    harness: DriverHarness,
    session_id: str,
) -> grpc.RpcError:
    """Issue a Navigate against ``harness`` and expect UNAVAILABLE.

    The §5 contract surfaces foreign-instance sessions as
    ``grpc.StatusCode.UNAVAILABLE`` with a message containing
    "different adapter instance" — every adapter's mapping is
    identical (Connect's Code.Unavailable for TS,
    grpc.StatusCode.UNAVAILABLE for Python, codes.Unavailable
    for Go all map to the same wire status).
    """
    stub = driver_pb2_grpc.DriverStub(harness.dial())
    request = driver_pb2.NavigateRequest(
        session_id=session_id,
        url="http://127.0.0.1:1/",  # never dialed; gate fires first
    )
    with pytest.raises(grpc.RpcError) as excinfo:
        stub.Navigate(request, timeout=10.0)
    rpc_err = excinfo.value
    assert rpc_err.code() == grpc.StatusCode.UNAVAILABLE, (
        f"expected UNAVAILABLE, got {rpc_err.code()}: {rpc_err.details()}"
    )
    assert "different adapter instance" in (rpc_err.details() or ""), (
        f"unexpected error details: {rpc_err.details()!r}"
    )
    return rpc_err


def _assert_redis_has_session(
    redis_client: redis.Redis,
    adapter_name: str,
    session_id: str,
    expected_instance_id: str,
) -> None:
    """Verify the metadata Redis stores for a freshly Initialized session."""
    import json

    raw = redis_client.get(f"session:{adapter_name}:{session_id}")
    assert raw is not None, (
        f"redis must hold the session document under "
        f"session:{adapter_name}:{session_id} after Initialize"
    )
    payload = json.loads(raw)
    assert payload["session_id"] == session_id
    assert payload["adapter"] == adapter_name
    assert payload["adapter_instance_id"] == expected_instance_id


# -- Playwright -------------------------------------------------------


@pytest.mark.timeout(PER_TEST_DEADLINE_S)
def test_playwright_session_invalidation_on_adapter_restart(
    redis_client: redis.Redis,
) -> None:
    """ADR-0023 §5 / R4.3 addendum — Playwright adapter."""
    if not PLAYWRIGHT_DIST.exists():
        pytest.skip(f"playwright adapter not built at {PLAYWRIGHT_DIST}; run `just pw-build` first")

    harness_a, harness_b = _harness_pair(
        command=["node", str(PLAYWRIGHT_DIST)],
        cwd=PLAYWRIGHT_DIR,
    )
    with harness_a, harness_b:
        session_id = _initialize(harness_a)
        _assert_redis_has_session(redis_client, "playwright", session_id, INSTANCE_A)

        _navigate_expecting_unavailable(harness_b, session_id)

        # Instance A still owns the session: closing it cleans up.
        stub_a = driver_pb2_grpc.DriverStub(harness_a.dial())
        close_resp = stub_a.Close(
            driver_pb2.CloseRequest(session_id=session_id),
            timeout=10.0,
        )
        assert not close_resp.HasField("error"), (
            f"Close on instance A should succeed: {close_resp.error}"
        )


# -- SeleniumBase -----------------------------------------------------


@pytest.mark.timeout(PER_TEST_DEADLINE_S)
def test_seleniumbase_session_invalidation_on_adapter_restart(
    redis_client: redis.Redis,
) -> None:
    """ADR-0023 §5 / R4.3 addendum — SeleniumBase adapter."""
    if not SELENIUMBASE_VENV_PY.exists():
        pytest.skip(
            f"seleniumbase adapter venv not present at {SELENIUMBASE_VENV_PY}; "
            "run `just sb-bootstrap` first"
        )

    harness_a, harness_b = _harness_pair(
        command=[str(SELENIUMBASE_VENV_PY), "-m", "spectre_seleniumbase.adapter"],
        cwd=SELENIUMBASE_DIR,
    )
    with harness_a, harness_b:
        session_id = _initialize(harness_a)
        _assert_redis_has_session(redis_client, "seleniumbase", session_id, INSTANCE_A)

        _navigate_expecting_unavailable(harness_b, session_id)

        stub_a = driver_pb2_grpc.DriverStub(harness_a.dial())
        close_resp = stub_a.Close(
            driver_pb2.CloseRequest(session_id=session_id),
            timeout=10.0,
        )
        assert not close_resp.HasField("error"), (
            f"Close on instance A should succeed: {close_resp.error}"
        )


# -- curl-impersonate -------------------------------------------------


@pytest.mark.timeout(PER_TEST_DEADLINE_S)
def test_curl_impersonate_session_invalidation_on_adapter_restart(
    redis_client: redis.Redis,
) -> None:
    """ADR-0023 §5 / R4.3 addendum — curl-impersonate adapter."""
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

    harness_a, harness_b = _harness_pair(
        command=[str(CURL_IMPERSONATE_BIN)],
        cwd=CURL_IMPERSONATE_DIR,
    )
    with harness_a, harness_b:
        session_id = _initialize(harness_a)
        _assert_redis_has_session(redis_client, "curl-impersonate", session_id, INSTANCE_A)

        _navigate_expecting_unavailable(harness_b, session_id)

        stub_a = driver_pb2_grpc.DriverStub(harness_a.dial())
        close_resp = stub_a.Close(
            driver_pb2.CloseRequest(session_id=session_id),
            timeout=10.0,
        )
        assert not close_resp.HasField("error"), (
            f"Close on instance A should succeed: {close_resp.error}"
        )
