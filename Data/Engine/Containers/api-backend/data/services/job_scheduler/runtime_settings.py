"""Runtime settings for job scheduler site workers."""

from __future__ import annotations

import json
import os
from pathlib import Path
from typing import Any, Dict, Mapping, Optional

SITE_WORKER_SETTINGS_PATH_ENV = "BOREALIS_SITE_WORKER_SETTINGS_PATH"
SITE_WORKER_SCHEDULED_CONCURRENCY_ENV = "BOREALIS_SITE_WORKER_SCHEDULED_CONCURRENCY"

DEFAULT_SITE_WORKER_SCHEDULED_CONCURRENCY = 5
MAX_SITE_WORKER_SCHEDULED_CONCURRENCY = 32


def _project_root() -> Path:
    root_env = str(os.getenv("BOREALIS_PROJECT_ROOT", "") or "").strip()
    if root_env:
        return Path(root_env).expanduser().resolve()
    current = Path(__file__).resolve().parent
    for candidate in (current, *current.parents):
        if (candidate / "Engine.sh").is_file():
            return candidate
    return Path.cwd().resolve()


def default_site_worker_settings_path() -> Path:
    override = str(os.getenv(SITE_WORKER_SETTINGS_PATH_ENV, "") or "").strip()
    if override:
        return Path(override).expanduser()
    return (
        _project_root()
        / "Engine"
        / "Services"
        / "api-backend"
        / "config"
        / "site_worker_settings.json"
    )


def coerce_site_worker_scheduled_concurrency(value: Any, default: int = DEFAULT_SITE_WORKER_SCHEDULED_CONCURRENCY) -> int:
    try:
        parsed = int(value)
    except Exception:
        parsed = int(default)
    return min(MAX_SITE_WORKER_SCHEDULED_CONCURRENCY, max(1, parsed))


def default_site_worker_settings() -> Dict[str, int]:
    return {
        "scheduled_task_concurrency_limit": coerce_site_worker_scheduled_concurrency(
            os.getenv(SITE_WORKER_SCHEDULED_CONCURRENCY_ENV),
            DEFAULT_SITE_WORKER_SCHEDULED_CONCURRENCY,
        )
    }


class SiteWorkerSettingsStore:
    """Persists live site-worker scheduling settings as JSON."""

    def __init__(self, path: Optional[Path] = None) -> None:
        self.path = (path or default_site_worker_settings_path()).expanduser()
        self.path.parent.mkdir(parents=True, exist_ok=True)

    def load(self) -> Dict[str, int]:
        defaults = default_site_worker_settings()
        if str(os.getenv(SITE_WORKER_SCHEDULED_CONCURRENCY_ENV, "") or "").strip():
            return defaults
        try:
            with self.path.open("r", encoding="utf-8") as handle:
                data = json.load(handle)
        except FileNotFoundError:
            return defaults
        except Exception:
            return defaults
        if not isinstance(data, dict):
            return defaults
        return {
            "scheduled_task_concurrency_limit": coerce_site_worker_scheduled_concurrency(
                data.get("scheduled_task_concurrency_limit"),
                defaults["scheduled_task_concurrency_limit"],
            )
        }

    def save(self, mapping: Mapping[str, Any]) -> Dict[str, int]:
        current = self.load()
        updated = {
            "scheduled_task_concurrency_limit": coerce_site_worker_scheduled_concurrency(
                mapping.get("scheduled_task_concurrency_limit", current["scheduled_task_concurrency_limit"]),
                current["scheduled_task_concurrency_limit"],
            )
        }
        tmp_path = self.path.with_suffix(".tmp")
        with tmp_path.open("w", encoding="utf-8") as handle:
            json.dump(updated, handle, indent=2, sort_keys=True)
        tmp_path.replace(self.path)
        return updated


def load_site_worker_settings(path: Optional[Path] = None) -> Dict[str, int]:
    return SiteWorkerSettingsStore(path).load()


def save_site_worker_settings(
    mapping: Mapping[str, Any],
    path: Optional[Path] = None,
) -> Dict[str, int]:
    return SiteWorkerSettingsStore(path).save(mapping)
