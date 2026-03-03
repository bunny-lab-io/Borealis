# ======================================================
# Data\Engine\Unit_Tests\test_engine_secret_config.py
# Description: Validates Engine secret key generation and persistence behavior in runtime configuration.
#
# API Endpoints (if applicable): None
# ======================================================

from __future__ import annotations

from pathlib import Path

from Data.Engine.config import load_runtime_config


def _base_config(tmp_path: Path) -> dict:
    tls_dir = tmp_path / "tls"
    tls_dir.mkdir(parents=True, exist_ok=True)
    cert_path = tls_dir / "server-cert.pem"
    key_path = tls_dir / "server-key.pem"
    bundle_path = tls_dir / "server-bundle.pem"
    cert_path.write_text("cert", encoding="utf-8")
    key_path.write_text("key", encoding="utf-8")
    bundle_path.write_text("bundle", encoding="utf-8")

    return {
        "DATABASE_PATH": str(tmp_path / "engine.sqlite3"),
        "TLS_CERT_PATH": str(cert_path),
        "TLS_KEY_PATH": str(key_path),
        "TLS_BUNDLE_PATH": str(bundle_path),
    }


def test_runtime_config_generates_and_reuses_engine_secret(tmp_path: Path) -> None:
    config = _base_config(tmp_path)
    secret_path = tmp_path / "engine" / "engine_secret.txt"
    config["ENGINE_SECRET_PATH"] = str(secret_path)

    first = load_runtime_config(config).secret_key
    second = load_runtime_config(config).secret_key

    assert secret_path.is_file()
    assert len(first) >= 32
    assert second == first


def test_runtime_config_prefers_explicit_secret_without_file_creation(tmp_path: Path) -> None:
    config = _base_config(tmp_path)
    secret_path = tmp_path / "engine" / "engine_secret.txt"
    config["ENGINE_SECRET_PATH"] = str(secret_path)
    config["SECRET_KEY"] = "explicit-engine-secret-override-value"

    settings = load_runtime_config(config)

    assert settings.secret_key == "explicit-engine-secret-override-value"
    assert not secret_path.exists()


def test_runtime_config_ignores_env_secret_and_uses_file(tmp_path: Path, monkeypatch) -> None:
    monkeypatch.setenv("BOREALIS_SECRET", "legacy-shared-secret")

    config = _base_config(tmp_path)
    secret_path = tmp_path / "engine" / "engine_secret.txt"
    config["ENGINE_SECRET_PATH"] = str(secret_path)

    settings = load_runtime_config(config)

    assert settings.secret_key != "legacy-shared-secret"
    assert secret_path.exists()
