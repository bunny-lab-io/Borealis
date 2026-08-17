#!/usr/bin/env python3
"""Parse tracked Borealis Python without creating bytecode caches."""

from __future__ import annotations

import ast
from pathlib import Path
import subprocess
import sys


ROOT = Path(__file__).resolve().parents[2]
proc = subprocess.run(
    ["git", "ls-files", "--cached", "--others", "--exclude-standard", "-z", "--", "*.py"],
    cwd=ROOT,
    check=True,
    stdout=subprocess.PIPE,
)
paths = [ROOT / item.decode("utf-8") for item in proc.stdout.split(b"\0") if item]
failures: list[str] = []
parsed = 0
for path in paths:
    if not path.is_file():
        continue
    try:
        ast.parse(path.read_text(encoding="utf-8"), filename=path.relative_to(ROOT).as_posix())
        parsed += 1
    except (OSError, UnicodeError, SyntaxError) as exc:
        failures.append(f"{path.relative_to(ROOT)}: {exc}")
if failures:
    print("PYTHON SYNTAX FAIL:\n" + "\n".join(failures), file=sys.stderr)
    raise SystemExit(1)
print(f"PYTHON SYNTAX PASS: parsed {parsed} tracked Python files without bytecode output")
