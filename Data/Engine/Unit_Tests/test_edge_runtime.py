# ======================================================
# Data\Engine\Unit_Tests\test_edge_runtime.py
# Description: Verifies Borealis embedded Traefik runtime generation for the public edge.
#
# API Endpoints (if applicable): None
# ======================================================

from __future__ import annotations

from pathlib import Path

from Data.Engine.edge_runtime import LetsEncryptSettings, write_runtime_artifacts


def _build_settings(tmp_path: Path) -> LetsEncryptSettings:
    return LetsEncryptSettings(
        enabled=True,
        fqdn="borealis.example.com",
        acme_email="ops@example.com",
        public_base_url="https://borealis.example.com",
        public_vnc_path="/remote-desktop/vnc",
        public_wireguard_host="borealis.example.com",
        public_wireguard_port=30000,
        http_port=80,
        https_port=443,
        engine_upstream_host="127.0.0.1",
        engine_upstream_port=5000,
        vnc_upstream_host="127.0.0.1",
        vnc_upstream_port=4823,
        settings_path=str(tmp_path / "Engine" / "LetsEncrypt" / "Settings.json"),
        runtime_env_path=str(tmp_path / "Engine" / "LetsEncrypt" / "runtime.env"),
        acme_storage_path=str(tmp_path / "Engine" / "LetsEncrypt" / "acme.json"),
        traefik_static_config_path=str(tmp_path / "Engine" / "Traefik" / "traefik.yml"),
        traefik_dynamic_config_path=str(tmp_path / "Engine" / "Traefik" / "dynamic.yml"),
        logs_directory=str(tmp_path / "Engine" / "Logs"),
    )


def test_dynamic_config_excludes_acme_challenge_from_http_redirect(tmp_path: Path) -> None:
    settings = _build_settings(tmp_path)

    artifacts = write_runtime_artifacts(settings)
    dynamic_config = Path(artifacts["traefik_dynamic_config_path"]).read_text(encoding="utf-8")

    assert 'Host(`borealis.example.com`) && !PathPrefix(`/.well-known/acme-challenge/`)' in dynamic_config


def test_static_config_trusts_configured_reverse_proxy_for_client_ip(tmp_path: Path, monkeypatch) -> None:
    settings = _build_settings(tmp_path)
    monkeypatch.setenv("BOREALIS_TRAEFIK_TRUSTED_PROXY_IPS", "192.168.5.29/32, 10.42.0.0/16")

    artifacts = write_runtime_artifacts(settings)
    static_config = Path(artifacts["traefik_static_config_path"]).read_text(encoding="utf-8")

    assert static_config.count("forwardedHeaders:") == 2
    assert static_config.count("proxyProtocol:") == 1
    assert '        - "192.168.5.29/32"' in static_config
    assert '        - "10.42.0.0/16"' in static_config


def test_dynamic_config_routes_dev_ui_to_vite_and_keeps_api_on_engine(
    tmp_path: Path, monkeypatch
) -> None:
    settings = _build_settings(tmp_path)
    monkeypatch.setenv("BOREALIS_DEV_UI_PROXY_ENABLED", "1")

    artifacts = write_runtime_artifacts(settings)
    dynamic_config = Path(artifacts["traefik_dynamic_config_path"]).read_text(encoding="utf-8")

    assert "borealis-engine-api:" in dynamic_config
    assert 'PathPrefix(`/api`) || PathPrefix(`/socket.io`)' in dynamic_config
    assert "borealis-ui-dev:" in dynamic_config
    assert 'service: borealis-vite' in dynamic_config
    assert 'url: "http://127.0.0.1:5173"' in dynamic_config
    assert "borealis-https:" not in dynamic_config
