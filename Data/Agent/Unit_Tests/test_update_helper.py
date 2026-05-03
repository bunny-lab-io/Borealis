from __future__ import annotations

from Data.Agent import update_helper


def test_update_helper_recovers_legacy_auth_material(tmp_path, monkeypatch) -> None:
    repo_root = tmp_path / "Borealis"
    canonical_settings = repo_root / "Agent" / "Borealis" / "Settings"
    legacy_settings = repo_root / "Agent" / "Settings"

    canonical_settings.mkdir(parents=True, exist_ok=True)
    legacy_settings.mkdir(parents=True, exist_ok=True)
    (repo_root / "Borealis.ps1").write_text("", encoding="utf-8")

    (legacy_settings / "server_url.txt").write_text("borealis.example.com\n", encoding="utf-8")
    (legacy_settings / "agent_GUID").write_text("ABC-123\n", encoding="utf-8")
    (legacy_settings / "refresh.token").write_bytes(b"legacy-refresh-token")
    (legacy_settings / "access.jwt").write_text("legacy-access-token\n", encoding="utf-8")
    (legacy_settings / "access.meta.json").write_text('{"access_expires_at": 9999999999}', encoding="utf-8")

    monkeypatch.setattr(update_helper, "_resolve_project_root", lambda: repo_root)
    monkeypatch.setattr(
        update_helper,
        "installed_build_id_path",
        lambda: canonical_settings / "installed_build_id.txt",
    )
    monkeypatch.delenv("BOREALIS_AGENT_SETTINGS_DIR", raising=False)
    monkeypatch.delenv("BOREALIS_SERVER_URL", raising=False)

    resolved_settings = update_helper._settings_dir()

    assert resolved_settings == canonical_settings
    assert (canonical_settings / "server_url.txt").read_text(encoding="utf-8").strip() == "borealis.example.com"
    assert (canonical_settings / "Agent_GUID.txt").read_text(encoding="utf-8").strip() == "ABC-123"
    assert (canonical_settings / "refresh.token").read_bytes() == b"legacy-refresh-token"
    assert update_helper._read_server_url() == "https://borealis.example.com"

    store = update_helper._keystore()
    assert store.load_guid() == "ABC-123"
    assert store.load_refresh_token() == "legacy-refresh-token"


def test_update_helper_preserves_active_updater_log() -> None:
    assert update_helper._should_exclude_relative("Updater.log")
