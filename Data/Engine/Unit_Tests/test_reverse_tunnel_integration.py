# ======================================================
# Data\Engine\Unit_Tests\test_reverse_tunnel_integration.py
# Description: Integration test that exercises a full reverse tunnel PowerShell round-trip
#              against a running Engine + Agent (requires live services).
#
# Requirements:
#   - Environment variables must be set to point at a live Engine + Agent:
#       TUNNEL_TEST_HOST        (e.g., https://localhost:5000)
#       TUNNEL_TEST_AGENT_ID    (agent_id/agent_guid for the target device)
#       TUNNEL_TEST_BEARER      (Authorization bearer token for an admin/operator)
#   - A live Agent must be reachable and allowed to establish the reverse tunnel.
#   - TLS verification can be controlled via TUNNEL_TEST_VERIFY ("false" to disable).
#
# API Endpoints (if applicable):
#   - POST /api/tunnel/request
#   - Socket.IO namespace /tunnel (join, ps_open, ps_send, ps_poll)
# ======================================================

from __future__ import annotations

import os
import time

import pytest
import requests
import socketio


HOST = os.environ.get("TUNNEL_TEST_HOST", "").strip()
AGENT_ID = os.environ.get("TUNNEL_TEST_AGENT_ID", "").strip()
BEARER = os.environ.get("TUNNEL_TEST_BEARER", "").strip()
VERIFY_ENV = os.environ.get("TUNNEL_TEST_VERIFY", "").strip().lower()
VERIFY = False if VERIFY_ENV in {"false", "0", "no"} else True

SKIP_MSG = (
    "Live tunnel test skipped (set TUNNEL_TEST_HOST, TUNNEL_TEST_AGENT_ID, TUNNEL_TEST_BEARER to run)"
)


def _require_env() -> None:
    if not HOST or not AGENT_ID or not BEARER:
        pytest.skip(SKIP_MSG)


def _make_session() -> requests.Session:
    sess = requests.Session()
    sess.verify = VERIFY
    sess.headers.update({"Authorization": f"Bearer {BEARER}"})
    return sess


def test_reverse_tunnel_powershell_roundtrip() -> None:
    _require_env()
    sess = _make_session()

    # 1) Request a tunnel lease
    resp = sess.post(
        f"{HOST}/api/tunnel/request",
        json={"agent_id": AGENT_ID, "protocol": "ps", "domain": "ps"},
    )
    assert resp.status_code == 200, f"lease request failed: {resp.status_code} {resp.text}"
    lease = resp.json()
    tunnel_id = lease["tunnel_id"]

    # 2) Connect to Socket.IO /tunnel namespace
    sio = socketio.Client()
    sio.connect(
        HOST,
        namespaces=["/tunnel"],
        headers={"Authorization": f"Bearer {BEARER}"},
        transports=["websocket"],
        wait_timeout=10,
    )

    # 3) Join tunnel and open PS channel
    join_resp = sio.call("join", {"tunnel_id": tunnel_id}, namespace="/tunnel", timeout=10)
    assert join_resp.get("status") == "ok", f"join failed: {join_resp}"

    open_resp = sio.call("ps_open", {"cols": 120, "rows": 32}, namespace="/tunnel", timeout=10)
    assert not open_resp.get("error"), f"ps_open failed: {open_resp}"

    # 4) Send a command
    send_resp = sio.call("ps_send", {"data": 'Write-Host "Hello World"\r\n'}, namespace="/tunnel", timeout=10)
    assert not send_resp.get("error"), f"ps_send failed: {send_resp}"

    # 5) Poll for output
    output_text = ""
    deadline = time.time() + 15
    while time.time() < deadline:
        poll_resp = sio.call("ps_poll", {}, namespace="/tunnel", timeout=10)
        if poll_resp.get("error"):
            pytest.fail(f"ps_poll failed: {poll_resp}")
        lines = poll_resp.get("output") or []
        output_text += "".join(lines)
        if "Hello World" in output_text:
            break
        time.sleep(0.5)

    sio.disconnect()

    assert "Hello World" in output_text, f"expected command output not found; got: {output_text!r}"
