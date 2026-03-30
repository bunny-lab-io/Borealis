from __future__ import annotations

from Data.Agent import runtime_paths


def test_runtime_paths_resolve_agent_runtime_root_from_repo_tree(tmp_path) -> None:
    repo_root = tmp_path / "Borealis"
    (repo_root / "Borealis.ps1").write_text("", encoding="utf-8")
    script_path = repo_root / "Data" / "Agent" / "Roles" / "role_Test.py"
    script_path.parent.mkdir(parents=True, exist_ok=True)
    script_path.write_text("# test", encoding="utf-8")

    assert runtime_paths.find_project_root(script_path) == repo_root
    assert runtime_paths.agent_runtime_root(script_path) == repo_root / "Agent"
    assert runtime_paths.agent_logs_root(script_path) == repo_root / "Agent" / "Logs"
    assert runtime_paths.agent_borealis_root(script_path) == repo_root / "Agent" / "Borealis"
