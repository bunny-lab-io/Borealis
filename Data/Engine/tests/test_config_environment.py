"""Tests for environment configuration helpers."""

from __future__ import annotations

from pathlib import Path

from Data.Engine.config.environment import load_environment


def test_static_root_prefers_engine_runtime(tmp_path, monkeypatch):
    """Engine static root should prefer the staged web-interface build."""

    engine_build = tmp_path / "Engine" / "web-interface" / "build"
    engine_build.mkdir(parents=True)
    (engine_build / "index.html").write_text("<html></html>", encoding="utf-8")

    # Ensure other fallbacks exist but should not be selected while the Engine
    # runtime assets are present.
    legacy_build = tmp_path / "Data" / "Server" / "WebUI" / "build"
    legacy_build.mkdir(parents=True)
    (legacy_build / "index.html").write_text("legacy", encoding="utf-8")

    monkeypatch.setenv("BOREALIS_ROOT", str(tmp_path))
    monkeypatch.delenv("BOREALIS_STATIC_ROOT", raising=False)

    settings = load_environment()

    assert settings.flask.static_root == engine_build.resolve()


def test_static_root_env_override(tmp_path, monkeypatch):
    """Explicit overrides should win over filesystem detection."""

    override = tmp_path / "custom" / "build"
    override.mkdir(parents=True)
    (override / "index.html").write_text("override", encoding="utf-8")

    monkeypatch.setenv("BOREALIS_ROOT", str(tmp_path))
    monkeypatch.setenv("BOREALIS_STATIC_ROOT", str(override))

    settings = load_environment()

    assert settings.flask.static_root == override.resolve()

    monkeypatch.delenv("BOREALIS_STATIC_ROOT", raising=False)
    monkeypatch.delenv("BOREALIS_ROOT", raising=False)


def test_static_root_falls_back_to_legacy_source(tmp_path, monkeypatch):
    """Legacy WebUI source should be served when no build assets exist."""

    legacy_source = tmp_path / "Data" / "Server" / "WebUI"
    legacy_source.mkdir(parents=True)
    (legacy_source / "index.html").write_text("<html></html>", encoding="utf-8")

    monkeypatch.setenv("BOREALIS_ROOT", str(tmp_path))
    monkeypatch.delenv("BOREALIS_STATIC_ROOT", raising=False)

    settings = load_environment()

    assert settings.flask.static_root == legacy_source.resolve()

    monkeypatch.delenv("BOREALIS_ROOT", raising=False)


def test_resolve_project_root_defaults_to_repository(monkeypatch):
    """The project root should resolve to the repository checkout."""

    monkeypatch.delenv("BOREALIS_ROOT", raising=False)
    from Data.Engine.config import environment as env_module

    expected = Path(env_module.__file__).resolve().parents[3]

    assert env_module._resolve_project_root() == expected
