from __future__ import annotations

import threading

import Data.Agent.Roles.role_system_software_management as software_role


class _FakeClient:
    def __init__(self) -> None:
        self.calls = []

    def post_json(self, path, payload, require_auth=False):
        self.calls.append((path, payload, require_auth))


def _make_role() -> software_role.Role:
    role = software_role.Role.__new__(software_role.Role)
    role.ctx = type("Ctx", (), {"agent_id": "agent-1"})()
    role._log_hook = None
    role._http_client_factory = None
    role._build_id_getter = None
    role._service_mode = "system"
    role._stop = None
    role._wakeup = None
    role._lock = threading.RLock()
    role._thread = None
    role._last_error = ""
    role._last_refresh_at = 0
    role._last_software_count = 0
    role._last_icon_payload_count = 0
    role._last_icon_override_count = 0
    role._fast_poll_until = 0.0
    role._last_software_icon_signature = ""
    role._last_software_icon_hash_by_key = {}
    role._icon_overrides = []
    role._supported = True
    role._unsupported_reason = ""
    role.role_health_label = "Software Management"
    return role


def test_publish_snapshot_posts_partial_agent_details_payload() -> None:
    client = _FakeClient()
    role = _make_role()
    role._http_client_factory = lambda: client
    role._build_id_getter = lambda: "build-123"

    role._publish_snapshot(
        {
            "software": [
                {
                    "name": "Contoso Agent",
                    "version": "2.4.1",
                    "source": "local_installed",
                }
            ],
            "software_icon_payloads": [
                {
                    "icon_hash": "abc123",
                    "mime_type": "image/png",
                    "data_base64": "ZmFrZS1wbmc=",
                }
            ],
        }
    )

    assert client.calls
    path, payload, require_auth = client.calls[0]
    assert path == "/api/agent/details"
    assert require_auth is True
    assert payload["agent_id"] == "agent-1"
    assert payload["agent_build_id"] == "build-123"
    assert payload["service_mode"] == "system"
    assert payload["details"]["software"] == [
        {
            "name": "Contoso Agent",
            "version": "2.4.1",
            "source": "local_installed",
        }
    ]
    assert payload["details"]["software_icon_payloads"] == [
        {
            "icon_hash": "abc123",
            "mime_type": "image/png",
            "data_base64": "ZmFrZS1wbmc=",
        }
    ]


def test_collect_and_publish_refreshes_icon_signature_cache(monkeypatch) -> None:
    role = _make_role()
    published = []
    role._publish_snapshot = lambda snapshot: published.append(snapshot)
    role._fetch_icon_overrides = lambda: [{"rule_id": "icon_override_contoso"}]

    monkeypatch.setattr(
        software_role,
        "build_software_inventory_snapshot",
        lambda **kwargs: {
            "software": [{"name": "Contoso Agent", "version": "2.4.1", "source": "local_installed"}],
            "software_icon_payloads": [],
            "software_icon_hash_by_key": {"contoso agent::2.4.1::local_installed": "hash-1"},
            "software_icon_signature": "signature-1",
        },
    )

    role._collect_and_publish()

    assert published
    assert role._last_software_icon_signature == "signature-1"
    assert role._last_software_icon_hash_by_key == {
        "contoso agent::2.4.1::local_installed": "hash-1"
    }


def test_collect_and_publish_passes_icon_overrides_to_snapshot_builder(monkeypatch) -> None:
    role = _make_role()
    role._publish_snapshot = lambda snapshot: None
    role._fetch_icon_overrides = lambda: [{"rule_id": "icon_override_contoso"}]
    captured = {}

    def _fake_build_snapshot(**kwargs):
        captured.update(kwargs)
        return {
            "software": [],
            "software_icon_payloads": [],
            "software_icon_hash_by_key": {},
            "software_icon_signature": "",
        }

    monkeypatch.setattr(software_role, "build_software_inventory_snapshot", _fake_build_snapshot)

    role._collect_and_publish()

    assert captured["icon_overrides"] == [{"rule_id": "icon_override_contoso"}]
