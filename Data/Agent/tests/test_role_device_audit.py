from __future__ import annotations

from Data.Agent.Roles import role_DeviceAudit as device_audit


def test_collect_software_includes_windows_uninstall_metadata(monkeypatch) -> None:
    monkeypatch.setattr(device_audit.platform, "system", lambda: "Windows")

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
                "UninstallString": "MsiExec.exe /I{11111111-2222-3333-4444-555555555555}",
                "QuietUninstallString": "MsiExec.exe /X{11111111-2222-3333-4444-555555555555} /qn /norestart",
                "WindowsInstaller": 1,
                "PSChildName": "{11111111-2222-3333-4444-555555555555}",
            }
        ]

    monkeypatch.setattr(device_audit, "_ps_json", fake_ps_json)

    rows = device_audit.collect_software()

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
