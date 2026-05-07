# ======================================================
# Data\Engine\Unit_Tests\test_agent_role_manager.py
# Description: Ensures agent roles resolve the current agent ID instead
#              of keeping a stale boot-time snapshot.
# ======================================================

from __future__ import annotations

import importlib.util
from pathlib import Path
import sys


def _load_role_manager():
    module_path = Path(__file__).resolve().parents[2] / "Agent" / "role_manager.py"
    spec = importlib.util.spec_from_file_location("agent_role_manager", module_path)
    assert spec and spec.loader
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module.RoleManager


def test_role_manager_ctx_reads_live_agent_id_from_hook() -> None:
    RoleManager = _load_role_manager()
    state = {"agent_id": "LAB-AIO-01_08FB4B0D-FE6B-4D41-B09B-7947851BFD7A_SYSTEM"}

    ctx = RoleManager.Ctx(
        sio=None,
        agent_id=state["agent_id"],
        config=None,
        loop=None,
        hooks={"get_agent_id": lambda: state["agent_id"]},
    )

    assert ctx.agent_id == "LAB-AIO-01_08FB4B0D-FE6B-4D41-B09B-7947851BFD7A_SYSTEM"

    state["agent_id"] = "LAB-AIO-01_1B60AFE7-4AA7-4AD8-B18B-23F5F98209EE_SYSTEM"

    assert ctx.agent_id == "LAB-AIO-01_1B60AFE7-4AA7-4AD8-B18B-23F5F98209EE_SYSTEM"


def test_role_manager_ctx_falls_back_to_boot_agent_id_when_hook_empty() -> None:
    RoleManager = _load_role_manager()
    ctx = RoleManager.Ctx(
        sio=None,
        agent_id="LAB-AIO-01_1B60AFE7-4AA7-4AD8-B18B-23F5F98209EE_SYSTEM",
        config=None,
        loop=None,
        hooks={"get_agent_id": lambda: ""},
    )

    assert ctx.agent_id == "LAB-AIO-01_1B60AFE7-4AA7-4AD8-B18B-23F5F98209EE_SYSTEM"


class _Registry:
    def __init__(self) -> None:
        self.entries = {}

    def register_role(self, role_name, *, context, reporter=None, role_label=None):
        role_id = f"{context}:{role_name}"
        self.entries[role_id] = {
            "role_name": role_name,
            "context": context,
            "reporter": reporter,
            "role_label": role_label,
        }
        return role_id

    def unregister_role(self, role_name, *, context):
        self.entries.pop(f"{context}:{role_name}", None)


def test_role_manager_registers_init_failure_in_role_health(tmp_path) -> None:
    RoleManager = _load_role_manager()
    roles_dir = tmp_path / "Roles"
    roles_dir.mkdir(parents=True, exist_ok=True)
    (roles_dir / "role_BrokenSystem.py").write_text(
        "\n".join(
            [
                "ROLE_NAME = 'BrokenSystem'",
                "ROLE_CONTEXTS = ['system']",
                "",
                "class Role:",
                "    def __init__(self, ctx):",
                "        raise RuntimeError('boom')",
            ]
        ),
        encoding="utf-8",
    )
    registry = _Registry()
    manager = RoleManager(
        base_dir=str(tmp_path),
        context="system",
        sio=None,
        agent_id="LAB-TEST-01_SYSTEM",
        config=None,
        loop=None,
        hooks={"role_health_registry": registry},
    )

    manager.load()

    entry = registry.entries.get("system:BrokenSystem")
    assert entry is not None
    payload = entry["reporter"]()
    assert payload["status"] == "unhealthy"
    assert payload["details"]["stage"] == "init"
    assert payload["details"]["path"].endswith("role_BrokenSystem.py")


def test_role_manager_registers_import_failure_for_matching_context(tmp_path) -> None:
    RoleManager = _load_role_manager()
    roles_dir = tmp_path / "Roles"
    roles_dir.mkdir(parents=True, exist_ok=True)
    (roles_dir / "role_BrokenImport.py").write_text(
        "\n".join(
            [
                "ROLE_NAME = 'BrokenImport'",
                "ROLE_CONTEXTS = ['system']",
                "",
                "raise RuntimeError('import boom')",
            ]
        ),
        encoding="utf-8",
    )
    registry = _Registry()
    manager = RoleManager(
        base_dir=str(tmp_path),
        context="system",
        sio=None,
        agent_id="LAB-TEST-01_SYSTEM",
        config=None,
        loop=None,
        hooks={"role_health_registry": registry},
    )

    manager.load()

    entry = registry.entries.get("system:BrokenImport")
    assert entry is not None
    payload = entry["reporter"]()
    assert payload["status"] == "unhealthy"
    assert payload["details"]["stage"] == "import"


def test_role_manager_adds_project_data_agent_to_import_path(tmp_path, monkeypatch) -> None:
    monkeypatch.delitem(sys.modules, "runtime_paths", raising=False)
    monkeypatch.setattr(sys, "path", list(sys.path))
    RoleManager = _load_role_manager()
    repo_root = tmp_path / "Borealis"
    repo_root.mkdir(parents=True, exist_ok=True)
    (repo_root / "Agent.ps1").write_text("", encoding="utf-8")

    runtime_root = repo_root / "Agent" / "Borealis"
    roles_dir = runtime_root / "Roles"
    roles_dir.mkdir(parents=True, exist_ok=True)

    source_agent_dir = repo_root / "Data" / "Agent"
    source_agent_dir.mkdir(parents=True, exist_ok=True)
    (source_agent_dir / "runtime_paths.py").write_text(
        "\n".join(
            [
                "def marker():",
                "    return 'runtime-paths-source'",
            ]
        ),
        encoding="utf-8",
    )
    (roles_dir / "role_RuntimePathProbe.py").write_text(
        "\n".join(
            [
                "from runtime_paths import marker",
                "",
                "ROLE_NAME = 'RuntimePathProbe'",
                "ROLE_CONTEXTS = ['system']",
                "",
                "class Role:",
                "    def __init__(self, ctx):",
                "        self.value = marker()",
            ]
        ),
        encoding="utf-8",
    )

    manager = RoleManager(
        base_dir=str(runtime_root),
        context="system",
        sio=None,
        agent_id="LAB-TEST-01_SYSTEM",
        config=None,
        loop=None,
    )

    manager.load()

    loaded = manager.roles.get("RuntimePathProbe")
    assert loaded is not None
    assert loaded.value == "runtime-paths-source"
