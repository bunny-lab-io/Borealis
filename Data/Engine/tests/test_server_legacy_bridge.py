from __future__ import annotations

from typing import Callable, Iterable

import pytest

pytest.importorskip("flask")

from Data.Engine.server import _FallbackDispatcher


def _wsgi_app(status: str, body: bytes) -> Callable:
    def _app(environ, start_response):  # type: ignore[override]
        start_response(status, [("Content-Type", "text/plain"), ("Content-Length", str(len(body)))])
        return [body]

    return _app


def _invoke(app: Callable, path: str = "/") -> tuple[str, bytes]:
    status_holder: dict[str, str] = {}
    body_parts: list[bytes] = []

    def _start_response(status: str, headers: Iterable[tuple[str, str]], exc_info=None):  # type: ignore[override]
        status_holder["status"] = status
        return body_parts.append

    environ = {"PATH_INFO": path, "REQUEST_METHOD": "GET", "wsgi.input": None}
    result = app(environ, _start_response)
    try:
        for chunk in result:
            body_parts.append(chunk)
    finally:
        close = getattr(result, "close", None)
        if callable(close):
            close()

    return status_holder.get("status", ""), b"".join(body_parts)


def test_fallback_dispatcher_primary_wins() -> None:
    primary = _wsgi_app("200 OK", b"engine")
    fallback = _wsgi_app("200 OK", b"legacy")
    dispatcher = _FallbackDispatcher(primary, fallback)

    status, body = _invoke(dispatcher)

    assert status == "200 OK"
    assert body == b"engine"


def test_fallback_dispatcher_uses_fallback_on_404() -> None:
    primary = _wsgi_app("404 Not Found", b"missing")
    fallback = _wsgi_app("200 OK", b"legacy")
    dispatcher = _FallbackDispatcher(primary, fallback)

    status, body = _invoke(dispatcher)

    assert status == "200 OK"
    assert body == b"legacy"
