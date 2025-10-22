"""Integration helpers wrapping Python API adapters."""

from __future__ import annotations

from functools import cached_property
from typing import Any


class _LazyModule:
    def __init__(self, import_path: str) -> None:
        self._import_path = import_path

    @cached_property
    def module(self) -> Any:
        components = self._import_path.split(".")
        module = __import__(self._import_path, fromlist=[components[-1]])
        return module


class OcrIntegration(_LazyModule):
    def __init__(self) -> None:
        super().__init__("Data.Server.app.services.integrations.ocr_engines")

    def run_ocr_on_base64(self, *args: Any, **kwargs: Any) -> Any:
        return self.module.run_ocr_on_base64(*args, **kwargs)


class ScriptIntegration(_LazyModule):
    def __init__(self) -> None:
        super().__init__("Data.Server.app.services.integrations.script_engines")

    def run_powershell_script(self, *args: Any, **kwargs: Any) -> Any:
        return self.module.run_powershell_script(*args, **kwargs)


class IntegrationRegistry:
    """Exposes Python API integrations as lazily-loaded services."""

    def __init__(self) -> None:
        self.ocr = OcrIntegration()
        self.scripting = ScriptIntegration()


__all__ = ["IntegrationRegistry", "OcrIntegration", "ScriptIntegration"]
