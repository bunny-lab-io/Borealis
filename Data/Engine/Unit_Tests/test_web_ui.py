from __future__ import annotations


def test_web_ui_root_serves_index(engine_harness):
    client = engine_harness.app.test_client()
    response = client.get("/")
    assert response.status_code == 200
    body = response.get_data(as_text=True)
    assert "Engine Test UI" in body


def test_web_ui_serves_static_assets(engine_harness):
    client = engine_harness.app.test_client()
    response = client.get("/assets/example.txt")
    assert response.status_code == 200
    assert response.get_data(as_text=True) == "asset"


def test_web_ui_spa_fallback(engine_harness):
    client = engine_harness.app.test_client()
    response = client.get("/devices")
    assert response.status_code == 200
    assert "Engine Test UI" in response.get_data(as_text=True)
