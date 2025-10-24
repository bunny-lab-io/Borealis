from __future__ import annotations

import os
from contextlib import contextmanager
from types import SimpleNamespace
from unittest.mock import MagicMock, patch

from flask import Flask

from Data.Engine.bootstrapper import bootstrap


class _DummyScheduler:
    def __init__(self) -> None:
        self.started_with = None

    def start(self, socketio: object) -> None:
        self.started_with = socketio


@contextmanager
def _fake_scope(_path):
    yield MagicMock()


def test_bootstrap_exposes_tls_material(monkeypatch, tmp_path):
    """Bootstrap should surface TLS paths for the Vite dev server."""

    webui = tmp_path / "Data" / "Server" / "WebUI"
    webui.mkdir(parents=True)
    (webui / "index.html").write_text("<html></html>", encoding="utf-8")

    monkeypatch.setenv("BOREALIS_ROOT", str(tmp_path))
    for key in ("BOREALIS_TLS_CERT", "BOREALIS_TLS_KEY", "BOREALIS_CERT_DIR", "BOREALIS_TLS_BUNDLE"):
        monkeypatch.delenv(key, raising=False)

    cert_dir = tmp_path / "Certificates" / "Server"
    cert_dir.mkdir(parents=True)
    cert_path = cert_dir / "borealis-server-cert.pem"
    key_path = cert_dir / "borealis-server-key.pem"
    bundle_path = cert_dir / "borealis-server-bundle.pem"
    for path in (cert_path, key_path, bundle_path):
        path.write_text("dummy", encoding="utf-8")

    dummy_app = Flask(__name__)
    dummy_scheduler = _DummyScheduler()
    dummy_services = SimpleNamespace(scheduler_service=dummy_scheduler)

    with (
        patch("Data.Engine.bootstrapper.ensure_certificate", return_value=(cert_path, key_path, bundle_path)),
        patch("Data.Engine.bootstrapper.sqlite_connection.connection_factory", return_value=lambda path=cert_path: MagicMock()),
        patch("Data.Engine.bootstrapper.sqlite_connection.connection_scope", side_effect=_fake_scope),
        patch("Data.Engine.bootstrapper.sqlite_migrations.apply_all"),
        patch("Data.Engine.bootstrapper.sqlite_migrations.ensure_default_admin"),
        patch("Data.Engine.bootstrapper.create_app", return_value=dummy_app),
        patch("Data.Engine.bootstrapper.build_service_container", return_value=dummy_services),
        patch("Data.Engine.bootstrapper.register_http_interfaces"),
        patch("Data.Engine.bootstrapper.create_socket_server", return_value=None),
        patch("Data.Engine.bootstrapper.register_ws_interfaces"),
    ):
        runtime = bootstrap()

    assert runtime.app is dummy_app
    assert dummy_scheduler.started_with is None

    assert os.environ["BOREALIS_TLS_CERT"] == str(cert_path)
    assert os.environ["BOREALIS_TLS_KEY"] == str(key_path)
    assert os.environ["BOREALIS_CERT_DIR"] == str(cert_dir)
    assert os.environ["BOREALIS_TLS_BUNDLE"] == str(bundle_path)
