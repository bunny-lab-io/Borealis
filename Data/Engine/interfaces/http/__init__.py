"""HTTP interface registration for the Borealis Engine."""

from __future__ import annotations

from flask import Flask


def register_http_interfaces(app: Flask) -> None:
    """Attach HTTP blueprints to *app*.

    The implementation is intentionally minimal for the initial scaffolding.
    """

    # Future phases will import and register blueprints here.
    return None


__all__ = ["register_http_interfaces"]
