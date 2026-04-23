from __future__ import annotations

from Data.Agent.Roles import role_system_device_auditor as device_audit


def test_build_details_fallback_excludes_software_inventory(monkeypatch) -> None:
    monkeypatch.setattr(device_audit, "collect_summary", lambda _config: {"hostname": "test-device"})
    monkeypatch.setattr(device_audit, "collect_memory", lambda: [{"slot": "DIMM0"}])
    monkeypatch.setattr(device_audit, "collect_storage", lambda: [{"mount": "C:"}])
    monkeypatch.setattr(device_audit, "collect_network", lambda: [{"name": "eth0"}])
    monkeypatch.setattr(device_audit, "collect_sessions", lambda: [{"username": "operator"}])
    monkeypatch.setattr(device_audit, "collect_processes", lambda: [{"pid": 1234}])

    details = device_audit._build_details_fallback()

    assert "software" not in details
    assert "software_icon_payloads" not in details
