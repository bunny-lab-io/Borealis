# ======================================================
# Data\Engine\Unit_Tests\test_core_api.py
# Description: Validates the Engine /health endpoint wiring.
#
# API Endpoints (if applicable): None
# ======================================================

from __future__ import annotations


def test_health_endpoint(engine_harness):
    client = engine_harness.app.test_client()
    response = client.get("/health")
    assert response.status_code == 200
    payload = response.get_json()
    assert payload == {"status": "ok"}
