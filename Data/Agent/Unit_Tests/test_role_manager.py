from __future__ import annotations

from pathlib import Path

import Data.Agent.role_manager as role_manager


class _HealthRegistry:
    def __init__(self) -> None:
        self.entries = []

    def register_role(self, role_name, *, context, reporter=None, role_label=None):
        self.entries.append(
            {
                "role_name": role_name,
                "context": context,
                "reporter": reporter,
                "role_label": role_label,
            }
        )
        return f"{context}:{role_name}"


def test_role_manager_skips_vnc_on_headless_linux(monkeypatch, tmp_path: Path) -> None:
    roles_dir = tmp_path / "Roles"
    roles_dir.mkdir()
    (roles_dir / "role_system_vnc.py").write_text(
        "\n".join(
            [
                'ROLE_NAME = "vnc"',
                'ROLE_CONTEXTS = ["system"]',
                'raise RuntimeError("vnc role should not import on headless linux")',
            ]
        ),
        encoding="utf-8",
    )
    registry = _HealthRegistry()
    logs = []
    monkeypatch.setattr(role_manager.os, "name", "posix", raising=False)
    monkeypatch.setattr(role_manager, "desktop_environment_active", lambda: False)

    manager = role_manager.RoleManager(
        base_dir=str(tmp_path),
        context="system",
        sio=None,
        agent_id="agent-1",
        config=None,
        loop=None,
        hooks={
            "role_health_registry": registry,
            "log_agent": lambda message, **_kwargs: logs.append(str(message)),
        },
    )

    manager.load()
    report = registry.entries[0]["reporter"]()

    assert "vnc" not in manager.roles
    assert report["status"] == "not_applicable"
    assert report["detail"] == "No Desktop Environment Active."
    assert any("Role skipped name=vnc" in entry for entry in logs)
