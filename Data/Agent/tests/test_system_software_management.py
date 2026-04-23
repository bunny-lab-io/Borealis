from __future__ import annotations

import base64
import hashlib

import Data.Agent.Roles.system_software_management as software_management


def test_apply_software_icon_overrides_replaces_display_icon_and_preserves_original() -> None:
    rows = [
        {
            "name": "Contoso Agent",
            "version": "2.4.1",
            "source": "local_installed",
            "metadata": {
                "publisher": "Contoso Ltd",
                "install_location": r"C:\Program Files\Contoso Agent",
                "display_icon": r"C:\Program Files\Contoso Agent\agent.exe,0",
            },
        }
    ]

    updated = software_management.apply_software_icon_overrides(
        rows,
        [
            {
                "rule_id": "icon_override_contoso_agent",
                "name": "Contoso Agent",
                "display_icon": r"C:\Program Files\Contoso Agent\branding\agent.ico",
            }
        ],
    )

    metadata = updated[0]["metadata"]
    assert metadata["display_icon"] == r"C:\Program Files\Contoso Agent\branding\agent.ico"
    assert metadata["original_display_icon"] == r"C:\Program Files\Contoso Agent\agent.exe,0"
    assert metadata["display_icon_override"] == r"C:\Program Files\Contoso Agent\branding\agent.ico"
    assert metadata["display_icon_override_rule_id"] == "icon_override_contoso_agent"


def test_apply_software_icon_overrides_can_clear_icon_and_preserve_original() -> None:
    rows = [
        {
            "name": "Contoso Agent",
            "version": "2.4.1",
            "source": "local_installed",
            "metadata": {
                "publisher": "Contoso Ltd",
                "install_location": r"C:\Program Files\Contoso Agent",
                "display_icon": r"C:\Program Files\Contoso Agent\agent.exe,0",
                "icon_hash": "deadbeef",
            },
        }
    ]

    updated = software_management.apply_software_icon_overrides(
        rows,
        [
            {
                "rule_id": "icon_override_contoso_agent_clear",
                "name": "Contoso Agent",
                "clear_icon": True,
            }
        ],
    )

    metadata = updated[0]["metadata"]
    assert metadata["display_icon"] == ""
    assert metadata["original_display_icon"] == r"C:\Program Files\Contoso Agent\agent.exe,0"
    assert metadata["display_icon_override"] == ""
    assert metadata["display_icon_override_rule_id"] == "icon_override_contoso_agent_clear"
    assert metadata["display_icon_override_cleared"] is True
    assert "icon_hash" not in metadata


def test_collect_software_includes_windows_uninstall_metadata(monkeypatch) -> None:
    monkeypatch.setattr(software_management.platform, "system", lambda: "Windows")

    def fake_ps_json(script: str, timeout: int = 120):
        if "Get-AppxPackage" in script:
            return [
                {
                    "Name": "Contoso.App",
                    "Version": "3.0.0",
                    "Publisher": "CN=Contoso",
                    "InstallLocation": r"C:\Program Files\WindowsApps\Contoso.App",
                    "PackageFamilyName": "Contoso.App_1234567890abc",
                    "NonRemovable": True,
                }
            ]
        return [
            {
                "DisplayName": "Contoso Agent",
                "DisplayVersion": "2.4.1",
                "Publisher": "Contoso",
                "InstallLocation": r"C:\Program Files\Contoso",
                "InstallDate": "20260421",
                "EstimatedSize": 654321,
                "DisplayIcon": r"C:\Program Files\Contoso\contoso.exe,0",
                "UninstallString": "MsiExec.exe /I{11111111-2222-3333-4444-555555555555}",
                "QuietUninstallString": "MsiExec.exe /X{11111111-2222-3333-4444-555555555555} /qn /norestart",
                "WindowsInstaller": 1,
                "PSChildName": "{11111111-2222-3333-4444-555555555555}",
            }
        ]

    monkeypatch.setattr(software_management, "_ps_json", fake_ps_json)

    rows = software_management.collect_software()

    assert rows == [
        {
            "name": "Contoso Agent",
            "version": "2.4.1",
            "source": "local_installed",
            "metadata": {
                "publisher": "Contoso",
                "install_location": r"C:\Program Files\Contoso",
                "install_date": "20260421",
                "estimated_size_kb": 654321,
                "display_icon": r"C:\Program Files\Contoso\contoso.exe,0",
                "uninstall_string": "MsiExec.exe /I{11111111-2222-3333-4444-555555555555}",
                "quiet_uninstall_string": "MsiExec.exe /X{11111111-2222-3333-4444-555555555555} /qn /norestart",
                "product_code": "{11111111-2222-3333-4444-555555555555}",
                "windows_installer": True,
            },
        },
        {
            "name": "Contoso.App",
            "version": "3.0.0",
            "source": "windows_store",
            "metadata": {
                "publisher": "CN=Contoso",
                "install_location": r"C:\Program Files\WindowsApps\Contoso.App",
                "package_family_name": "Contoso.App_1234567890abc",
                "non_removable": True,
            },
        },
    ]


def test_attach_windows_software_icons_adds_icon_hash_and_deduped_payloads(monkeypatch) -> None:
    monkeypatch.setattr(software_management.platform, "system", lambda: "Windows")
    icon_png = base64.b64encode(b"png-icon-bytes").decode("ascii")

    def fake_extract(hints):
        return {
            r"C:\Program Files\Contoso\contoso.exe,0": {
                "mime_type": "image/png",
                "data_base64": icon_png,
            }
        }

    monkeypatch.setattr(software_management, "_extract_windows_icon_payloads_by_hint", fake_extract)

    rows = [
        {
            "name": "Contoso Agent",
            "version": "2.4.1",
            "source": "local_installed",
            "metadata": {
                "display_icon": r"C:\Program Files\Contoso\contoso.exe,0",
            },
        },
        {
            "name": "Contoso Agent Tools",
            "version": "2.4.1",
            "source": "local_installed",
            "metadata": {
                "display_icon": r"C:\Program Files\Contoso\contoso.exe,0",
            },
        },
    ]

    payloads, icon_hash_by_key = software_management.attach_windows_software_icons(rows)

    expected_hash = hashlib.sha256(b"png-icon-bytes").hexdigest()
    assert payloads == [
        {
            "icon_hash": expected_hash,
            "mime_type": "image/png",
            "data_base64": icon_png,
        }
    ]
    assert rows[0]["metadata"]["icon_hash"] == expected_hash
    assert rows[1]["metadata"]["icon_hash"] == expected_hash
    assert icon_hash_by_key == {
        "contoso agent::2.4.1::local_installed": expected_hash,
        "contoso agent tools::2.4.1::local_installed": expected_hash,
    }


def test_attach_windows_software_icons_drops_cached_hash_when_icon_is_cleared(monkeypatch) -> None:
    monkeypatch.setattr(software_management.platform, "system", lambda: "Windows")

    rows = [
        {
            "name": "Contoso Agent",
            "version": "2.4.1",
            "source": "local_installed",
            "metadata": {
                "display_icon": "",
                "display_icon_override_cleared": True,
                "icon_hash": "stalehash",
            },
        }
    ]

    payloads, icon_hash_by_key = software_management.attach_windows_software_icons(
        rows,
        previous_icon_hash_by_key={
            "contoso agent::2.4.1::local_installed": "stalehash",
        },
    )

    assert payloads == []
    assert icon_hash_by_key == {}
    assert "icon_hash" not in rows[0]["metadata"]


def test_extract_windows_icon_payloads_by_hint_batches_large_requests(monkeypatch) -> None:
    monkeypatch.setattr(software_management.platform, "system", lambda: "Windows")
    monkeypatch.setattr(software_management, "_SOFTWARE_ICON_EXTRACTION_BATCH_SIZE", 2)

    observed_batches = []

    def fake_extract_batch(specs):
        observed_batches.append([item["hint"] for item in specs])
        return {
            str(item["hint"]): {
                "mime_type": "image/png",
                "data_base64": base64.b64encode(str(item["hint"]).encode("utf-8")).decode("ascii"),
            }
            for item in specs
        }

    monkeypatch.setattr(software_management, "_extract_windows_icon_payload_batch", fake_extract_batch)

    payloads = software_management._extract_windows_icon_payloads_by_hint(
        [
            r"C:\Program Files\Contoso\contoso.exe,0",
            r"C:\Program Files\Fabrikam\fabrikam.exe,0",
            r"C:\Program Files\Northwind\northwind.exe,0",
            r"C:\Program Files\Contoso\contoso.exe,0",
        ]
    )

    assert observed_batches == [
        [
            r"C:\Program Files\Contoso\contoso.exe,0",
            r"C:\Program Files\Fabrikam\fabrikam.exe,0",
        ],
        [
            r"C:\Program Files\Northwind\northwind.exe,0",
        ],
    ]
    assert payloads == {
        r"C:\Program Files\Contoso\contoso.exe,0": {
            "mime_type": "image/png",
            "data_base64": base64.b64encode(r"C:\Program Files\Contoso\contoso.exe,0".encode("utf-8")).decode("ascii"),
        },
        r"C:\Program Files\Fabrikam\fabrikam.exe,0": {
            "mime_type": "image/png",
            "data_base64": base64.b64encode(r"C:\Program Files\Fabrikam\fabrikam.exe,0".encode("utf-8")).decode("ascii"),
        },
        r"C:\Program Files\Northwind\northwind.exe,0": {
            "mime_type": "image/png",
            "data_base64": base64.b64encode(r"C:\Program Files\Northwind\northwind.exe,0".encode("utf-8")).decode("ascii"),
        },
    }


def test_extract_windows_icon_payload_batch_uses_native_resource_extraction(monkeypatch) -> None:
    observed = {}

    def fake_ps_json_file(script, timeout=120):
        observed["script"] = script
        observed["timeout"] = timeout
        return []

    monkeypatch.setattr(software_management, "_ps_json_file", fake_ps_json_file)

    payloads = software_management._extract_windows_icon_payload_batch(
        [
            {
                "hint": r"C:\Program Files\Contoso\contoso.exe,7",
                "path": r"C:\Program Files\Contoso\contoso.exe",
                "index": 7,
            }
        ]
    )

    assert payloads == {}
    script = observed["script"]
    assert "ExtractIconEx" in script
    assert "DestroyIcon" in script
    assert "nIconIndex" in script
    assert "-IconIndex ([int]$spec.index)" in script


def test_extract_windows_icon_payload_batch_uses_file_backed_powershell_runner(monkeypatch) -> None:
    observed = {}

    def fake_ps_json_file(script_text: str, timeout: int = 60):
        observed["script"] = script_text
        observed["timeout"] = timeout
        return {
            "hint": r"C:\Program Files\Contoso\contoso.exe,0",
            "mime_type": "image/png",
            "data_base64": base64.b64encode(b"contoso-icon").decode("ascii"),
        }

    monkeypatch.setattr(software_management, "_ps_json_file", fake_ps_json_file)

    payloads = software_management._extract_windows_icon_payload_batch(
        [
            {
                "hint": r"C:\Program Files\Contoso\contoso.exe,0",
                "path": r"C:\Program Files\Contoso\contoso.exe",
                "index": 0,
            }
        ]
    )

    assert "Get-BorealisIconPayload" in observed["script"]
    assert "ExtractAssociatedIcon" in observed["script"]
    assert observed["timeout"] >= 120
    assert payloads == {
        r"C:\Program Files\Contoso\contoso.exe,0": {
            "mime_type": "image/png",
            "data_base64": base64.b64encode(b"contoso-icon").decode("ascii"),
        }
    }
