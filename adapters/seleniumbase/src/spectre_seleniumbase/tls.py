"""Server-side mTLS for the SeleniumBase adapter (ADR-0032 §4.2, W3.4).

Symmetric to the curl-impersonate Go adapter (`internal/tls/...`)
and the engine Rust `src/tls/...` — same three env vars
(``SPECTRE_TLS_{CERT,KEY,CA}_PATH``), same Mode classification
(Plaintext / Mutual / partial → fail-fast).

ADR-0032 §5.1 (Python row) accepts restart-on-rotation as the
Python reload model: ``grpc.ssl_server_credentials`` takes static
keypair bytes; cert-manager rotation triggers a Pod restart via
the chart's annotation pattern (60-day rotation lead, 30-day
window — Pod restarts within the cadence are operationally
acceptable per ADR-0032 §5.2).
"""

from __future__ import annotations

import enum
import os
from collections.abc import Callable
from dataclasses import dataclass
from pathlib import Path

import grpc

CERT_PATH_ENV = "SPECTRE_TLS_CERT_PATH"
KEY_PATH_ENV = "SPECTRE_TLS_KEY_PATH"
CA_PATH_ENV = "SPECTRE_TLS_CA_PATH"


class TlsMode(enum.Enum):
    """Resolved TLS posture."""

    PLAINTEXT = "plaintext"
    MUTUAL = "mutual"


@dataclass(frozen=True)
class TlsConfig:
    """Configuration handle the adapter builds at startup."""

    mode: TlsMode
    cert_path: Path | None = None
    key_path: Path | None = None
    ca_path: Path | None = None


class TlsConfigError(RuntimeError):
    """Raised when one or two of the three TLS env vars are set but
    not all three. The chart's ``_helpers.tpl::spectre.tlsEnv`` wires
    all three together, so a partial state is hand-rolled misconfig.
    """


def detect_mode(getenv: Callable[[str], str | None] = os.environ.get) -> TlsConfig:
    """Resolve the TLS posture from process env.

    All three vars set → ``MUTUAL``; all three unset (or empty) →
    ``PLAINTEXT``; partial → :class:`TlsConfigError`.
    """
    cert = getenv(CERT_PATH_ENV) or None
    key = getenv(KEY_PATH_ENV) or None
    ca = getenv(CA_PATH_ENV) or None

    set_count = sum(1 for v in (cert, key, ca) if v)
    if set_count == 0:
        return TlsConfig(mode=TlsMode.PLAINTEXT)
    if set_count == 3:
        assert cert is not None and key is not None and ca is not None
        return TlsConfig(
            mode=TlsMode.MUTUAL,
            cert_path=Path(cert),
            key_path=Path(key),
            ca_path=Path(ca),
        )

    set_vars = [
        name
        for name, value in (
            (CERT_PATH_ENV, cert),
            (KEY_PATH_ENV, key),
            (CA_PATH_ENV, ca),
        )
        if value
    ]
    unset_vars = [
        name
        for name, value in (
            (CERT_PATH_ENV, cert),
            (KEY_PATH_ENV, key),
            (CA_PATH_ENV, ca),
        )
        if not value
    ]
    msg = (
        f"tls: partial env config — {set_vars} set, {unset_vars} unset; "
        f"all three of {CERT_PATH_ENV}, {KEY_PATH_ENV}, {CA_PATH_ENV} must "
        "be set together (mTLS) or all unset (plaintext)"
    )
    raise TlsConfigError(msg)


def build_server_credentials(config: TlsConfig) -> grpc.ServerCredentials | None:
    """Build gRPC server credentials for the resolved Config.

    Plaintext mode returns ``None`` so the caller binds via
    ``add_insecure_port(...)``. Mutual mode returns a
    ``grpc.ssl_server_credentials`` instance with
    ``require_client_auth=True`` — the adapter rejects dials that
    don't present a client certificate signed by the trust bundle's
    CA (ADR-0032 §4.2). Static load at startup; rotation requires
    Pod restart (ADR-0032 §5.1 Python).
    """
    if config.mode is TlsMode.PLAINTEXT:
        return None
    assert config.cert_path is not None
    assert config.key_path is not None
    assert config.ca_path is not None

    cert_pem = config.cert_path.read_bytes()
    key_pem = config.key_path.read_bytes()
    ca_pem = config.ca_path.read_bytes()

    return grpc.ssl_server_credentials(
        [(key_pem, cert_pem)],
        root_certificates=ca_pem,
        require_client_auth=True,
    )
