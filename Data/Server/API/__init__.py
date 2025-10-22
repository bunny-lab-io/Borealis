"""Bundled Python integrations exposed to the Borealis server.

Historically these modules lived under ``Python_API_Endpoints``; the package
has been renamed to ``API`` to better reflect its role. Import aliases are
registered so existing code that still references the legacy path continues to
work without modification.
"""

from __future__ import annotations

import sys
from importlib import import_module

from .ocr_engines import run_ocr_on_base64  # noqa: F401
from .script_engines import run_powershell_script  # noqa: F401

_CURRENT_PACKAGE = sys.modules[__name__]
sys.modules.setdefault("Python_API_Endpoints", _CURRENT_PACKAGE)
sys.modules.setdefault(
    "Python_API_Endpoints.ocr_engines",
    import_module("API.ocr_engines"),
)
sys.modules.setdefault(
    "Python_API_Endpoints.script_engines",
    import_module("API.script_engines"),
)

__all__ = ["run_ocr_on_base64", "run_powershell_script"]
