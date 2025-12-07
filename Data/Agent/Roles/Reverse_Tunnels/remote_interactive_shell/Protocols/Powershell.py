"""Expose the PowerShell channel under the domain path, with file-based import fallback."""
from __future__ import annotations

import importlib.util
from pathlib import Path

powershell_module = None

# Attempt package-relative import first
try:  # pragma: no cover - best effort
    from ....ReverseTunnel import tunnel_Powershell as powershell_module  # type: ignore
except Exception:
    powershell_module = None

# Fallback: load directly from file path to survive non-package runtimes
if powershell_module is None:
    try:
        base = Path(__file__).resolve().parents[3] / "ReverseTunnel" / "tunnel_Powershell.py"
        spec = importlib.util.spec_from_file_location("tunnel_Powershell", base)
        if spec and spec.loader:
            module = importlib.util.module_from_spec(spec)
            spec.loader.exec_module(module)  # type: ignore
            powershell_module = module
    except Exception:
        powershell_module = None

if powershell_module and hasattr(powershell_module, "PowershellChannel"):
    PowershellChannel = powershell_module.PowershellChannel  # type: ignore
else:  # pragma: no cover - safety guard
    class PowershellChannel:  # type: ignore
        def __init__(self, *args, **kwargs):
            raise ImportError("PowerShell channel unavailable")


__all__ = ["PowershellChannel"]
