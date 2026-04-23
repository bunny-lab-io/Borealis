from __future__ import annotations

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
                "source": "local_installed",
                "name": "Contoso Agent",
                "version": "2.4.1",
                "publisher_contains_any": ["Contoso Ltd"],
                "display_icon": r"C:\Program Files\Contoso Agent\branding\agent.ico",
            }
        ],
    )

    metadata = updated[0]["metadata"]
    assert metadata["display_icon"] == r"C:\Program Files\Contoso Agent\branding\agent.ico"
    assert metadata["original_display_icon"] == r"C:\Program Files\Contoso Agent\agent.exe,0"
    assert metadata["display_icon_override"] == r"C:\Program Files\Contoso Agent\branding\agent.ico"
    assert metadata["display_icon_override_rule_id"] == "icon_override_contoso_agent"
