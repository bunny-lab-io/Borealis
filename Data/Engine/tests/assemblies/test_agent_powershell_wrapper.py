# ======================================================
# Data\Engine\tests\assemblies\test_agent_powershell_wrapper.py
# Description: Guards the agent PowerShell wrapper so advanced script preambles
#              remain valid after Borealis injects environment variables.
#
# API Endpoints (if applicable): None
# ======================================================

from __future__ import annotations

import importlib
import sys
import textwrap
import types

import pytest


def _install_agent_role_stubs(monkeypatch: pytest.MonkeyPatch) -> None:
    pyside6_module = types.ModuleType("PySide6")
    pyside6_module.QtCore = types.SimpleNamespace()
    pyside6_module.QtWidgets = types.SimpleNamespace()
    pyside6_module.QtGui = types.SimpleNamespace()
    monkeypatch.setitem(sys.modules, "PySide6", pyside6_module)

    pyqt5_module = types.ModuleType("PyQt5")
    pyqt5_module.QtCore = types.SimpleNamespace()
    pyqt5_module.QtWidgets = types.SimpleNamespace()
    pyqt5_module.QtGui = types.SimpleNamespace()
    monkeypatch.setitem(sys.modules, "PyQt5", pyqt5_module)

    signature_utils = types.ModuleType("signature_utils")
    signature_utils.decode_script_bytes = lambda *args, **kwargs: b""
    signature_utils.verify_and_store_script_signature = lambda *args, **kwargs: True
    monkeypatch.setitem(sys.modules, "signature_utils", signature_utils)


@pytest.mark.parametrize(
    ("module_name", "timeout_seconds"),
    [
        ("Data.Agent.Roles.role_currentuser_context", 0),
        ("Data.Agent.Roles.role_currentuser_context", 30),
        ("Data.Agent.Roles.role_system_context", 0),
        ("Data.Agent.Roles.role_system_context", 30),
    ],
)
def test_powershell_wrapper_preserves_advanced_script_preamble(
    monkeypatch: pytest.MonkeyPatch,
    module_name: str,
    timeout_seconds: int,
) -> None:
    _install_agent_role_stubs(monkeypatch)
    sys.modules.pop(module_name, None)
    module = importlib.import_module(module_name)

    content = textwrap.dedent(
        """\
        [CmdletBinding()]
        param(
            [string]$Version = '2501'
        )

        Write-Host $Version
        Write-Host $env:VERSION
        """
    )

    wrapped = module._build_wrapped_script(content, {"VERSION": "2501"}, timeout_seconds)

    assert "$__BorealisScript = {\n" in wrapped
    prelude, script_and_tail = wrapped.split("$__BorealisScript = {\n", 1)
    script_body, tail = script_and_tail.split("\n}\n", 1)

    assert "SetEnvironmentVariable('VERSION', '2501', 'Process')" in prelude
    assert script_body.startswith(content)
    assert "[CmdletBinding()]\nparam(" in script_body
    assert "SetEnvironmentVariable('VERSION', '2501', 'Process')" not in script_body

    if timeout_seconds > 0:
        assert "Start-Job -ScriptBlock $__BorealisScript" in tail
    else:
        assert tail == "& $__BorealisScript\n"
