# ======================================================
# Data\Engine\Unit_Tests\test_edge_runtime.py
# Description: Verifies Borealis embedded Traefik runtime generation for the public edge.
#
# API Endpoints (if applicable): None
# ======================================================

from __future__ import annotations

from pathlib import Path

from Data.Engine.edge_runtime import LetsEncryptSettings, write_runtime_artifacts


def test_dynamic_config_excludes_acme_challenge_from_http_redirect(tmp_path: Path) -> None:
    settings = LetsEncryptSettings(
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

    artifacts = write_runtime_artifacts(settings)
    dynamic_config = Path(artifacts["traefik_dynamic_config_path"]).read_text(encoding="utf-8")

    assert 'Host(`borealis.example.com`) && !PathPrefix(`/.well-known/acme-challenge/`)' in dynamic_config
