"""Helpers for normalising the Engine runtime filesystem layout."""

from __future__ import annotations

import shutil
from dataclasses import dataclass
from pathlib import Path
from typing import Iterable, Tuple


@dataclass(frozen=True, slots=True)
class RuntimeLayoutResult:
    """Summary describing how the runtime layout was normalised."""

    legacy_root: Path
    moves: Tuple[Tuple[Path, Path], ...]
    removed: Tuple[Path, ...]
    reason: str | None = None

    @property
    def changed(self) -> bool:
        """Return ``True`` if any filesystem mutations were performed."""

        return bool(self.moves or self.removed)


def _collect_entries(directory: Path) -> Iterable[Path]:
    try:
        yield from directory.iterdir()
    except FileNotFoundError:
        return


def normalise_runtime_layout(runtime_root: Path) -> RuntimeLayoutResult:
    """Move legacy ``Engine/Data/Engine`` children into ``Engine/`` directly."""

    legacy_root = (runtime_root / "Data" / "Engine").resolve()
    if not legacy_root.exists():
        return RuntimeLayoutResult(legacy_root=legacy_root, moves=tuple(), removed=tuple(), reason="legacy-missing")

    moves: list[tuple[Path, Path]] = []
    for child in list(_collect_entries(legacy_root)):
        destination = runtime_root / child.name

        if destination.exists():
            if destination.is_dir():
                shutil.rmtree(destination)
            else:
                destination.unlink()

        shutil.move(str(child), str(destination))
        moves.append((child, destination.resolve()))

    removed: list[Path] = []
    for candidate in (legacy_root, legacy_root.parent):
        try:
            candidate.rmdir()
        except OSError:
            continue
        else:
            removed.append(candidate.resolve())

    return RuntimeLayoutResult(legacy_root=legacy_root, moves=tuple(moves), removed=tuple(removed))


__all__ = ["RuntimeLayoutResult", "normalise_runtime_layout"]
