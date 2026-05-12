"""Unit tests for the SeleniumBase adapter's TLS detection."""

from __future__ import annotations

import pytest

from spectre_seleniumbase.tls import (
    CA_PATH_ENV,
    CERT_PATH_ENV,
    KEY_PATH_ENV,
    TlsConfig,
    TlsConfigError,
    TlsMode,
    build_server_credentials,
    detect_mode,
)


def make_getter(env: dict[str, str]):
    return lambda name: env.get(name)


def test_detect_mode_all_unset_is_plaintext() -> None:
    cfg = detect_mode(make_getter({}))
    assert cfg.mode is TlsMode.PLAINTEXT


def test_detect_mode_all_set_is_mutual() -> None:
    cfg = detect_mode(
        make_getter(
            {
                CERT_PATH_ENV: "/etc/spectre/tls/tls.crt",
                KEY_PATH_ENV: "/etc/spectre/tls/tls.key",
                CA_PATH_ENV: "/etc/spectre/tls/ca.crt",
            }
        )
    )
    assert cfg.mode is TlsMode.MUTUAL
    assert cfg.cert_path is not None
    assert cfg.cert_path.name == "tls.crt"
    assert cfg.key_path is not None
    assert cfg.key_path.name == "tls.key"
    assert cfg.ca_path is not None
    assert cfg.ca_path.name == "ca.crt"


def test_detect_mode_partial_raises() -> None:
    with pytest.raises(TlsConfigError) as exc:
        detect_mode(
            make_getter(
                {
                    CERT_PATH_ENV: "/etc/spectre/tls/tls.crt",
                    KEY_PATH_ENV: "/etc/spectre/tls/tls.key",
                    # CA deliberately unset
                }
            )
        )
    assert CA_PATH_ENV in str(exc.value)


def test_detect_mode_empty_string_treated_as_unset() -> None:
    cfg = detect_mode(
        make_getter({CERT_PATH_ENV: "", KEY_PATH_ENV: "", CA_PATH_ENV: ""})
    )
    assert cfg.mode is TlsMode.PLAINTEXT


def test_build_server_credentials_plaintext_returns_none() -> None:
    cfg = TlsConfig(mode=TlsMode.PLAINTEXT)
    assert build_server_credentials(cfg) is None
