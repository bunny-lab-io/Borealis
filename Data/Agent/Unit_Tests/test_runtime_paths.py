from __future__ import annotations

from pathlib import Path

from Data.Agent import runtime_paths


def test_runtime_paths_resolve_agent_runtime_root_from_repo_tree(tmp_path) -> None:
    repo_root = tmp_path / "Borealis"
    repo_root.mkdir(parents=True, exist_ok=True)
    (repo_root / "Borealis.ps1").write_text("", encoding="utf-8")
    script_path = repo_root / "Data" / "Agent" / "Roles" / "role_Test.py"
    script_path.parent.mkdir(parents=True, exist_ok=True)
    script_path.write_text("# test", encoding="utf-8")

    assert runtime_paths.find_project_root(script_path) == repo_root
    assert runtime_paths.agent_runtime_root(script_path) == repo_root / "Agent"
    assert runtime_paths.agent_logs_root(script_path) == repo_root / "Agent" / "Logs"
    assert runtime_paths.agent_borealis_root(script_path) == repo_root / "Agent" / "Borealis"


def test_runtime_paths_ignore_stale_override_when_running_tree_is_known(tmp_path, monkeypatch) -> None:
    repo_root = tmp_path / "C" / "Borealis"
    (repo_root / "Borealis.ps1").parent.mkdir(parents=True, exist_ok=True)
    (repo_root / "Borealis.ps1").write_text("", encoding="utf-8")
    script_path = repo_root / "Data" / "Agent" / "agent.py"
    script_path.parent.mkdir(parents=True, exist_ok=True)
    script_path.write_text("# test", encoding="utf-8")

    stale_root = tmp_path / "D" / "Github" / "Borealis"
    stale_root.mkdir(parents=True, exist_ok=True)
    monkeypatch.setenv("BOREALIS_PROJECT_ROOT", str(stale_root))

    assert runtime_paths.find_project_root(script_path) == repo_root


def test_runtime_paths_keep_override_when_running_tree_is_inside_override(tmp_path, monkeypatch) -> None:
    repo_root = tmp_path / "C" / "Borealis"
    (repo_root / "Borealis.ps1").parent.mkdir(parents=True, exist_ok=True)
    (repo_root / "Borealis.ps1").write_text("", encoding="utf-8")
    script_path = repo_root / "Data" / "Agent" / "agent.py"
    script_path.parent.mkdir(parents=True, exist_ok=True)
    script_path.write_text("# test", encoding="utf-8")

    monkeypatch.setenv("BOREALIS_PROJECT_ROOT", str(repo_root))

    assert runtime_paths.find_project_root(script_path) == Path(repo_root)
