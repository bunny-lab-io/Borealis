# ======================================================
# Data\Engine\Unit_Tests\test_edge_runtime.py
# Description: Verifies site-worker loading of persisted public-edge settings.
#
# API Endpoints (if applicable): None
# ======================================================

from __future__ import annotations

import json
from pathlib import Path

from Data.Engine.edge_runtime import load_settings


def test_legacy_dynamic_file_setting_moves_to_watched_core_file(tmp_path: Path) -> None:
    settings_path = tmp_path / "Engine" / "Services" / "traefik-edge" / "state" / "Settings.json"
    settings_path.parent.mkdir(parents=True, exist_ok=True)
    settings_path.write_text(
        json.dumps(
            {
                "fqdn": "borealis.example.test",
                "traefik_dynamic_config_path": str(
                    tmp_path / "Engine" / "Services" / "traefik-edge" / "config" / "dynamic.yml"
                ),
            }
        ),
        encoding="utf-8",
    )

    settings = load_settings(settings_path)

    assert settings.traefik_dynamic_config_path.endswith(
        "/Engine/Services/traefik-edge/config/dynamic/core.yml"
    )
