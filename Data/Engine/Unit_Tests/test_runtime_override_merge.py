from __future__ import annotations

from pathlib import Path

from Data.Engine.services.API.devices.runtime_override_merge import merge_override_payloads


def test_merge_icon_override_payloads_prefers_runtime_entry_by_name() -> None:
    source_payload = {
        "windows_icon_overrides": [
            {
                "rule_id": "icon_override_adobe_acrobat_64_bit",
                "name": "Adobe Acrobat (64-bit)",
                "display_icon": r"C:\Program Files\Adobe\Acrobat DC\Acrobat\Acrobat.exe,0",
            },
            {
                "rule_id": "icon_override_google_chrome",
                "name": "Google Chrome",
                "display_icon": r"C:\Program Files\Google\Chrome\Application\chrome.exe,0",
            },
        ]
    }
    runtime_payload = {
        "windows_icon_overrides": [
            {
                "rule_id": "icon_override_adobe_acrobat_64_bit_26_001_21431",
                "name": "Adobe Acrobat (64-bit)",
                "version": "26.001.21431",
                "publisher_contains_any": ["Adobe"],
                "display_icon": r"C:\Operator\Custom\Acrobat.exe,0",
            },
            {
                "rule_id": "icon_override_nextcloud",
                "name": "Nextcloud",
                "display_icon": r"C:\Program Files\Nextcloud\nextcloud.exe,0",
            },
        ]
    }

    merged = merge_override_payloads(source_payload, runtime_payload, kind="software_icons")

    assert merged == {
        "windows_icon_overrides": [
            {
                "rule_id": "icon_override_adobe_acrobat_64_bit_26_001_21431",
                "name": "Adobe Acrobat (64-bit)",
                "version": "26.001.21431",
                "publisher_contains_any": ["Adobe"],
                "display_icon": r"C:\Operator\Custom\Acrobat.exe,0",
            },
            {
                "rule_id": "icon_override_google_chrome",
                "name": "Google Chrome",
                "display_icon": r"C:\Program Files\Google\Chrome\Application\chrome.exe,0",
            },
            {
                "rule_id": "icon_override_nextcloud",
                "name": "Nextcloud",
                "display_icon": r"C:\Program Files\Nextcloud\nextcloud.exe,0",
            },
        ]
    }


def test_merge_uninstall_override_payloads_prefers_runtime_entry_by_name() -> None:
    source_payload = {
        "windows_uninstall_overrides": [
            {
                "rule_id": "uninstall_override_fedora_media_writer",
                "name": "Fedora Media Writer",
                "strategy": "direct_command",
                "quiet_uninstall_string": '"C:\\Program Files\\Fedora Media Writer\\uninstall.exe" /S',
            }
        ]
    }
    runtime_payload = {
        "windows_uninstall_overrides": [
            {
                "rule_id": "operator_override_fedora_media_writer",
                "name": "Fedora Media Writer",
                "strategy": "direct_command",
                "quiet_uninstall_string": '"C:\\Program Files\\Fedora Media Writer\\uninstall.exe" remove --confirm-command',
                "summary": "Operator-tested override.",
            }
        ]
    }

    merged = merge_override_payloads(source_payload, runtime_payload, kind="software_uninstall_overrides")

    assert merged == {
        "windows_uninstall_overrides": [
            {
                "rule_id": "operator_override_fedora_media_writer",
                "name": "Fedora Media Writer",
                "strategy": "direct_command",
                "quiet_uninstall_string": '"C:\\Program Files\\Fedora Media Writer\\uninstall.exe" remove --confirm-command',
                "summary": "Operator-tested override.",
            }
        ]
    }


def test_merge_uninstall_blocklist_payloads_matches_name_contains_any_overlap() -> None:
    source_payload = {
        "windows_quiet_uninstall_blocklist": [
            {
                "rule_id": "fedora_media_writer_quiet_string_interactive",
                "name_contains_any": ["Fedora Media Writer"],
                "quiet_args_any": ["/s"],
                "reason": "Official reason.",
            }
        ]
    }
    runtime_payload = {
        "windows_quiet_uninstall_blocklist": [
            {
                "rule_id": "operator_fedora_media_writer_block",
                "name_contains_any": ["Fedora Media Writer"],
                "quiet_args_any": ["/s"],
                "reason": "Operator reason.",
            }
        ]
    }

    merged = merge_override_payloads(source_payload, runtime_payload, kind="software_uninstall_blocklist")

    assert merged == {
        "windows_quiet_uninstall_blocklist": [
            {
                "rule_id": "operator_fedora_media_writer_block",
                "name_contains_any": ["Fedora Media Writer"],
                "quiet_args_any": ["/s"],
                "reason": "Operator reason.",
            }
        ]
    }


def test_runtime_override_merge_helper_stays_available() -> None:
    module_path = (
        Path(__file__).resolve().parents[1]
        / "Containers"
        / "api-backend"
        / "data"
        / "services"
        / "API"
        / "devices"
        / "runtime_override_merge.py"
    )
    content = module_path.read_text(encoding="utf-8")

    assert "def merge_override_payloads" in content
    assert "software_icons" in content
    assert "software_uninstall_overrides" in content
    assert "software_uninstall_blocklist" in content
