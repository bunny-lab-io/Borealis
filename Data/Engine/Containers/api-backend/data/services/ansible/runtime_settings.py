"""Runtime settings for Engine-side scheduled Ansible execution."""

from __future__ import annotations

import json
import os
from pathlib import Path
from typing import Any, Dict, Mapping, Optional

_ANSIBLE_RUNNER_SETTINGS_PATH_ENV = "BOREALIS_ANSIBLE_RUNNER_SETTINGS_PATH"
_ANSIBLE_RUNNER_JOB_LIMIT_ENV = "BOREALIS_ANSIBLE_RUNNER_JOB_CONCURRENCY_LIMIT"
_ANSIBLE_RUNNER_GLOBAL_LIMIT_ENV = "BOREALIS_ANSIBLE_RUNNER_GLOBAL_CONCURRENCY_LIMIT"

DEFAULT_ANSIBLE_RUNNER_JOB_CONCURRENCY_LIMIT = 20
DEFAULT_ANSIBLE_RUNNER_GLOBAL_CONCURRENCY_LIMIT = 50


def _project_root() -> Path:
    root_env = str(os.getenv("BOREALIS_PROJECT_ROOT", "") or "").strip()
    if root_env:
        return Path(root_env).expanduser().resolve()
    current = Path(__file__).resolve().parent
    for candidate in (current, *current.parents):
        if (candidate / "Borealis.sh").is_file():
            return candidate
    return Path.cwd().resolve()


def _default_settings_path() -> Path:
    override = str(os.getenv(_ANSIBLE_RUNNER_SETTINGS_PATH_ENV, "") or "").strip()
    if override:
        return Path(override).expanduser()
    return (
        _project_root()
        / "Engine"
        / "Services"
        / "api-backend"
        / "config"
        / "ansible_runner_settings.json"
    )


def _coerce_positive_int(value: Any, default: int) -> int:
    try:
        parsed = int(value)
    except Exception:
        return int(default)
    return max(1, parsed)


def _default_settings() -> Dict[str, int]:
    return {
        "job_concurrency_limit": _coerce_positive_int(
            os.getenv(_ANSIBLE_RUNNER_JOB_LIMIT_ENV),
            DEFAULT_ANSIBLE_RUNNER_JOB_CONCURRENCY_LIMIT,
        ),
        "global_concurrency_limit": _coerce_positive_int(
            os.getenv(_ANSIBLE_RUNNER_GLOBAL_LIMIT_ENV),
            DEFAULT_ANSIBLE_RUNNER_GLOBAL_CONCURRENCY_LIMIT,
        ),
    }


class AnsibleRunnerSettingsStore:
    """Persists live scheduled-ansible runner limits as JSON."""

    def __init__(self, path: Optional[Path] = None) -> None:
        self.path = (path or _default_settings_path()).expanduser()
        self.path.parent.mkdir(parents=True, exist_ok=True)

    def load(self) -> Dict[str, int]:
        defaults = _default_settings()
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
            "job_concurrency_limit": _coerce_positive_int(
                data.get("job_concurrency_limit"),
                defaults["job_concurrency_limit"],
            ),
            "global_concurrency_limit": _coerce_positive_int(
                data.get("global_concurrency_limit"),
                defaults["global_concurrency_limit"],
            ),
        }

    def save(self, mapping: Mapping[str, Any]) -> Dict[str, int]:
        current = self.load()
        updated = {
            "job_concurrency_limit": _coerce_positive_int(
                mapping.get("job_concurrency_limit", current["job_concurrency_limit"]),
                current["job_concurrency_limit"],
            ),
            "global_concurrency_limit": _coerce_positive_int(
                mapping.get("global_concurrency_limit", current["global_concurrency_limit"]),
                current["global_concurrency_limit"],
            ),
        }
        tmp_path = self.path.with_suffix(".tmp")
        with tmp_path.open("w", encoding="utf-8") as handle:
            json.dump(updated, handle, indent=2, sort_keys=True)
        tmp_path.replace(self.path)
        return updated


def load_ansible_runner_settings(path: Optional[Path] = None) -> Dict[str, int]:
    return AnsibleRunnerSettingsStore(path).load()


def save_ansible_runner_settings(
    mapping: Mapping[str, Any],
    path: Optional[Path] = None,
) -> Dict[str, int]:
    return AnsibleRunnerSettingsStore(path).save(mapping)
