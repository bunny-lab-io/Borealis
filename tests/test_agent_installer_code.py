import json
import sys

import pytest


@pytest.fixture
def agent_module(tmp_path, monkeypatch):
    settings_dir = tmp_path / "Agent" / "Borealis" / "Settings"
    settings_dir.mkdir(parents=True)
    system_config = settings_dir / "agent_settings_SYSTEM.json"
    system_config.write_text(json.dumps({
        "config_file_watcher_interval": 2,
        "agent_id": "",
        "regions": {},
        "installer_code": "",
    }, indent=2))
    current_config = settings_dir / "agent_settings_CURRENTUSER.json"
    current_config.write_text(json.dumps({
        "config_file_watcher_interval": 2,
        "agent_id": "",
        "regions": {},
        "installer_code": "",
    }, indent=2))

    monkeypatch.setenv("BOREALIS_ROOT", str(tmp_path))
    monkeypatch.setenv("BOREALIS_AGENT_MODE", "system")
    monkeypatch.setenv("BOREALIS_AGENT_CONFIG", "")
    monkeypatch.setitem(sys.modules, "PyQt5", None)
    monkeypatch.setitem(sys.modules, "qasync", None)
    monkeypatch.setattr(sys, "argv", ["agent.py", "--system-service", "--config", "SYSTEM"], raising=False)

    agent = pytest.importorskip(
        "Data.Agent.agent", reason="agent module requires optional dependencies"
    )
    return agent, system_config


def test_shared_installer_code_cache_allows_system_reuse(agent_module, tmp_path):
    agent, system_config = agent_module

    client = agent.AgentHttpClient()
    shared_code = "SHARED-CODE-1234"
    client.key_store.cache_installer_code(shared_code, consumer="CURRENTUSER")

    # System agent should discover the cached code even though its config is empty.
    resolved = client._resolve_installer_code()
    assert resolved == shared_code

    # Config should now persist the adopted code to avoid repeated lookups.
    data = json.loads(system_config.read_text())
    assert data.get("installer_code") == shared_code

    # After enrollment completes, the cache should be cleared for future runs.
    client._consume_installer_code()
    assert client.key_store.load_cached_installer_code() is None
    data = json.loads(system_config.read_text())
    assert data.get("installer_code") == ""
