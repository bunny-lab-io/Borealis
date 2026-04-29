# ======================================================
# Data\Engine\Unit_Tests\test_guacamole_proxy.py
# Description: Validates Apache Guacamole VNC tunnel protocol helpers.
#
# API Endpoints (if applicable): None
# ======================================================

from __future__ import annotations

from Data.Engine.services.RemoteDesktop.guacamole_proxy import (
    GuacamoleProtocolParser,
    GuacamoleVncSession,
    encode_instruction,
    guacamole_connect_arguments,
)


def test_guacamole_instruction_parser_handles_split_frames() -> None:
    parser = GuacamoleProtocolParser()

    assert parser.feed("4.arg") == []
    instructions = parser.feed("s,8.hostname,4.port;")

    assert instructions == [("args", ["hostname", "port"])]


def test_guacamole_encoder_round_trips_internal_ping() -> None:
    parser = GuacamoleProtocolParser()
    encoded = encode_instruction("", "ping", "123")

    assert parser.feed(encoded) == [("", ["ping", "123"])]


def test_guacamole_connect_arguments_are_server_side_only() -> None:
    session = GuacamoleVncSession(
        token="token",
        agent_id="agent-1",
        host="10.255.0.4",
        port=5900,
        password="secretpw",
        created_at=0,
        expires_at=120,
        role="controller",
    )

    values = guacamole_connect_arguments(
        session,
        [
            "hostname",
            "port",
            "password",
            "username",
            "read-only",
            "disable-display-resize",
            "color-depth",
            "autoretry",
            "resize-method",
        ],
    )

    assert values == ["10.255.0.4", "5900", "secretpw", "", "", "true", "24", "3", ""]
