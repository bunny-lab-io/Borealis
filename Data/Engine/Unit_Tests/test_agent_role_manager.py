# ======================================================
# Data\Engine\Unit_Tests\test_agent_role_manager.py
# Description: Ensures agent roles resolve the current agent ID instead
#              of keeping a stale boot-time snapshot.
# ======================================================

from __future__ import annotations

import importlib.util
from pathlib import Path


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
