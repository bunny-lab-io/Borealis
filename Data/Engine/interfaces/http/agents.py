"""Agent HTTP interface placeholders for the Engine."""

from __future__ import annotations

from flask import Blueprint, Flask, current_app, jsonify

from Data.Engine.services.container import EngineServiceContainer


blueprint = Blueprint("engine_agents", __name__, url_prefix="/api/agents")


def register(app: Flask, _services: EngineServiceContainer) -> None:
    """Attach agent management routes to *app*.

    Implementation will be populated as services migrate from the legacy server.
    """

    if "engine_agents" not in app.blueprints:
        app.register_blueprint(blueprint)


def _services() -> EngineServiceContainer:
    services = current_app.extensions.get("engine_services")
    if services is None:  # pragma: no cover - defensive
        raise RuntimeError("engine services not initialized")
    return services


def _normalize_agent_id(value: object) -> str:
    if value is None:
        return ""
    try:
        text = str(value)
    except Exception:
        return ""
    return text.strip()


def _should_hide_agent(agent_id: str, service_mode: str, is_script_agent: bool) -> bool:
    if service_mode != "currentuser":
        lowered = agent_id.lower()
        if lowered.endswith("-script") or is_script_agent:
            return True
    return False


@blueprint.route("", methods=["GET"])
def list_agents() -> object:
    services = _services()
    realtime = services.agent_realtime.snapshot()
    inventory_devices = services.device_inventory.list_agent_devices()

    agents: dict[str, dict] = {}

    for device in inventory_devices:
        summary = device.get("summary", {}) if isinstance(device, dict) else {}
        if not isinstance(summary, dict):
            summary = {}

        agent_id = _normalize_agent_id(
            summary.get("agent_id") or device.get("agent_id")
        )
        if not agent_id:
            continue

        realtime_entry = realtime.get(agent_id, {})
        service_mode = realtime_entry.get("service_mode", "currentuser")
        is_script_agent = bool(realtime_entry.get("is_script_agent"))
        if _should_hide_agent(agent_id, service_mode, is_script_agent):
            continue

        hostname = summary.get("hostname") or device.get("hostname")
        record = {
            "agent_id": agent_id,
            "hostname": (hostname or realtime_entry.get("hostname") or "unknown"),
            "agent_operating_system": (
                summary.get("operating_system")
                or summary.get("agent_operating_system")
                or realtime_entry.get("agent_operating_system")
                or "-"
            ),
            "last_seen": int(
                realtime_entry.get("last_seen")
                or summary.get("last_seen")
                or device.get("last_seen")
                or 0
            ),
            "status": realtime_entry.get("status") or device.get("status") or summary.get("status") or "Offline",
            "service_mode": service_mode,
            "collector_active": bool(realtime_entry.get("collector_active")),
            "is_script_agent": is_script_agent,
            "agent_hash": summary.get("agent_hash") or device.get("agent_hash") or "",
            "agent_guid": summary.get("agent_guid") or device.get("agent_guid") or "",
        }
        agents[agent_id] = record

    for agent_id, realtime_entry in realtime.items():
        if agent_id in agents:
            continue
        service_mode = realtime_entry.get("service_mode", "currentuser")
        is_script_agent = bool(realtime_entry.get("is_script_agent"))
        if _should_hide_agent(agent_id, service_mode, is_script_agent):
            continue

        agents[agent_id] = {
            "agent_id": agent_id,
            "hostname": realtime_entry.get("hostname") or "unknown",
            "agent_operating_system": realtime_entry.get("agent_operating_system") or "-",
            "last_seen": int(realtime_entry.get("last_seen") or 0),
            "status": realtime_entry.get("status") or "orphaned",
            "service_mode": service_mode,
            "collector_active": bool(realtime_entry.get("collector_active")),
            "is_script_agent": is_script_agent,
            "agent_hash": "",
            "agent_guid": "",
        }

    return jsonify(agents)


__all__ = ["register", "blueprint"]
