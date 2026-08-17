#!/usr/bin/env python3
"""Keep POSIX and PowerShell Agent validation lanes behaviorally aligned."""

from pathlib import Path
import sys


ROOT = Path(__file__).resolve().parents[2]
LINUX = (ROOT / "Tests/run-agent.sh").read_text(encoding="utf-8").lower()
WINDOWS = (ROOT / "Tests/run-agent-windows.ps1").read_text(encoding="utf-8").lower()

REQUIRED = {
    "format": ("check_gofmt", "gofmt"),
    "tidy": ("check_go_mod_tidy", '"mod", "tidy"'),
    "vet": (" vet ./...", '"vet", "./..."'),
    "test": (" test ./...", '"test", "./..."'),
    "windows build": ("goos=windows", 'goos = "windows"'),
    "linux build": ("goos=linux", 'goos = "linux"'),
    "timeout": ("run_timed", "waitforexit"),
}

failures = [name for name, (linux_token, windows_token) in REQUIRED.items() if linux_token not in LINUX or windows_token not in WINDOWS]
if failures:
    print("AGENT RUNNER PARITY FAIL: missing aligned behavior: " + ", ".join(failures), file=sys.stderr)
    raise SystemExit(1)
print("AGENT RUNNER PARITY PASS: Linux and Windows lanes cover format, tidy, vet, test, build, and timeout")
