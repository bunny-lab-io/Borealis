from Data.Agent.services.device_inventory import DeviceInventoryCollector
from Data.Agent.Roles import role_DeviceAudit as audit_module


class _DummyPsutil:
    @staticmethod
    def boot_time():
        return 1_700_000_000


def test_collect_details_normalizes_inventory(monkeypatch):
    monkeypatch.setattr(audit_module, "collect_summary", lambda cfg: {"hostname": "HOST-01"})

    def _fallback():
        return {
            "summary": {"hostname": "HOST-01", "external_ip": "", "last_user": "HOST-01$"},
            "software": [],
            "memory": [],
            "storage": [],
            "network": [],
        }

    monkeypatch.setattr(audit_module, "_build_details_fallback", _fallback)
    monkeypatch.setattr(audit_module, "collect_memory", lambda: [{"slot": "DIMM0", "capacity": 17179869184}])
    monkeypatch.setattr(audit_module, "collect_storage", lambda: [{"model": "NVMe", "size_gb": 512}])
    monkeypatch.setattr(audit_module, "collect_network", lambda: [{"name": "Ethernet", "ips": ["10.0.0.20", "127.0.0.1"]}])
    monkeypatch.setattr(audit_module, "collect_software", lambda: [{"name": "TestApp", "version": "1.2.3"}])
    monkeypatch.setattr(audit_module, "collect_cpu", lambda: {"name": "Intel", "logical_cores": 8})
    monkeypatch.setattr(audit_module, "detect_device_type", lambda: "Laptop")
    monkeypatch.setattr(audit_module, "_collect_last_user_registry", lambda: "ACME\\jane.doe")
    monkeypatch.setattr(audit_module, "_lookup_external_ip", lambda: "198.51.100.5")
    monkeypatch.setattr(audit_module, "psutil", _DummyPsutil())

    collector = DeviceInventoryCollector()
    details = collector.collect_details()
    summary = details["summary"]

    assert summary["hostname"] == "HOST-01"
    assert summary["device_type"] == "Laptop"
    assert summary["internal_ip"] == "10.0.0.20"
    assert summary["external_ip"] == "198.51.100.5"
    assert summary["last_user"] == "ACME\\jane.doe"
    assert summary["last_reboot"] == "2023-11-14 22:13:20"
    assert details["software"][0]["name"] == "TestApp"
    assert details["memory"][0]["capacity"] == 17179869184
    assert details["storage"][0]["model"] == "NVMe"
    assert details["network"][0]["ips"][0] == "10.0.0.20"
    assert details["cpu"]["name"] == "Intel"

    payload = collector.build_payload("agent-123")
    assert payload["hostname"] == "HOST-01"
    assert payload["details"]["summary"]["external_ip"] == "198.51.100.5"
