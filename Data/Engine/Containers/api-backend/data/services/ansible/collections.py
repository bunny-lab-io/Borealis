# ======================================================
# Data\Engine\services\ansible\collections.py
# Description: Stages Engine Ansible collection manifests and installs missing collections for worker runtimes.
#
# API Endpoints (if applicable): None
# ======================================================

"""Ansible collection staging helpers for worker-owned Engine execution."""

from __future__ import annotations

import importlib.util
import logging
import os
import shutil
import subprocess
import sys
from pathlib import Path
from typing import Optional


def _project_root() -> Path:
    root_env = os.environ.get("BOREALIS_PROJECT_ROOT")
    if root_env:
        return Path(root_env).expanduser().resolve()

    current = Path(__file__).resolve()
    for candidate in (current.parent, *current.parents):
        if (candidate / "Engine.sh").is_file():
            return candidate
        if (candidate / "Data" / "Engine" / "Ansible" / "collections.yml").is_file():
            return candidate
        if (candidate / "Data" / "Engine" / "Containers" / "api-backend" / "data" / "Ansible" / "collections.yml").is_file():
            return candidate

    raise RuntimeError("Unable to locate the Borealis project root for Engine Ansible collection staging.")


def _resolve_ansible_collections_manifest(project_root: Path) -> Optional[Path]:
    candidates = (
        project_root / "Data" / "Engine" / "Ansible" / "collections.yml",
        project_root / "Data" / "Engine" / "Containers" / "api-backend" / "data" / "Ansible" / "collections.yml",
        Path(__file__).resolve().parents[2] / "Ansible" / "collections.yml",
    )
    for candidate in candidates:
        if candidate.is_file():
            return candidate
    return None


def _parse_ansible_collection_names(manifest_path: Path) -> list[str]:
    names: list[str] = []
    try:
        lines = manifest_path.read_text(encoding="utf-8").splitlines()
    except OSError:
        return names
    for line in lines:
        stripped = line.split("#", 1)[0].strip()
        if not stripped.startswith("- name:"):
            continue
        name = stripped.split(":", 1)[1].strip().strip("'\"")
        if name:
            names.append(name)
    return names


def _ansible_collection_installed(collections_root: Path, collection_name: str) -> bool:
    parts = [part for part in collection_name.split(".") if part]
    if len(parts) < 2:
        return False
    return (collections_root / "ansible_collections" / parts[0] / parts[1]).is_dir()


def _resolve_ansible_galaxy_command() -> Optional[list[str]]:
    env_cmd = os.environ.get("BOREALIS_ANSIBLE_GALAXY_CMD")
    if env_cmd:
        return [env_cmd]

    executable_name = "ansible-galaxy.exe" if os.name == "nt" else "ansible-galaxy"
    candidate = Path(sys.executable).resolve().parent / executable_name
    if candidate.is_file():
        return [str(candidate)]

    resolved = shutil.which(executable_name)
    if resolved:
        return [resolved]

    if importlib.util.find_spec("ansible.cli.galaxy") is not None:
        return [str(sys.executable), "-m", "ansible.cli.galaxy"]

    return None


def stage_ansible_collections(logger: Optional[logging.Logger] = None) -> Optional[Path]:
    """Stage Borealis Ansible collections for worker-owned execution."""

    logger = logger or logging.getLogger(__name__)
    project_root = _project_root()
    source_manifest = _resolve_ansible_collections_manifest(project_root)
    if source_manifest is None:
        logger.warning("Ansible collections manifest not found; worker Ansible collections will not be auto-installed.")
        return None

    runtime_root = Path(
        os.environ.get("BOREALIS_ANSIBLE_RUNTIME_ROOT")
        or project_root / "Engine" / "Services" / "api-backend" / "cache" / "Ansible"
    ).expanduser()
    collections_root = runtime_root / "collections"
    runtime_root.mkdir(parents=True, exist_ok=True)
    collections_root.mkdir(parents=True, exist_ok=True)

    runtime_manifest = runtime_root / "collections.yml"
    source_text = source_manifest.read_text(encoding="utf-8")
    if not runtime_manifest.is_file() or runtime_manifest.read_text(encoding="utf-8") != source_text:
        runtime_manifest.write_text(source_text, encoding="utf-8")
        logger.info("Staged Ansible collections manifest from %s to %s", source_manifest, runtime_manifest)
    else:
        logger.info("Ansible collections manifest already staged at %s", runtime_manifest)

    required_collections = _parse_ansible_collection_names(runtime_manifest)
    missing_collections = [
        name for name in required_collections
        if not _ansible_collection_installed(collections_root, name)
    ]
    if not missing_collections:
        logger.info("Ansible collections ready at %s", collections_root)
        return runtime_manifest

    command = _resolve_ansible_galaxy_command()
    if not command:
        logger.warning(
            "Ansible collections missing but ansible-galaxy is unavailable: %s",
            ", ".join(missing_collections),
        )
        return runtime_manifest

    install_command = command + [
        "collection",
        "install",
        "-r",
        str(runtime_manifest),
        "-p",
        str(collections_root),
    ]
    env = os.environ.copy()
    env["ANSIBLE_COLLECTIONS_PATH"] = str(collections_root)
    env["ANSIBLE_COLLECTIONS_PATHS"] = str(collections_root)
    logger.info("Installing missing Ansible collections: %s", ", ".join(missing_collections))
    completed = subprocess.run(
        install_command,
        cwd=str(project_root),
        env=env,
        capture_output=True,
        text=True,
        check=False,
    )
    if completed.returncode != 0:
        logger.error(
            "Ansible collection install failed with exit code %s: %s\nstdout: %s\nstderr: %s",
            completed.returncode,
            " ".join(install_command),
            (completed.stdout or "").strip(),
            (completed.stderr or "").strip(),
        )
        return runtime_manifest

    still_missing = [
        name for name in required_collections
        if not _ansible_collection_installed(collections_root, name)
    ]
    if still_missing:
        logger.error(
            "Ansible collection install completed, but collections are still missing: %s",
            ", ".join(still_missing),
        )
    else:
        logger.info("Installed Ansible collections into %s", collections_root)
    return runtime_manifest


def main() -> None:
    logging.basicConfig(level=logging.INFO, format="%(levelname)s:%(name)s:%(message)s")
    logger = logging.getLogger("borealis.ansible.collections")
    try:
        stage_ansible_collections(logger=logger)
    except Exception:
        logger.exception("Failed to stage worker Ansible collections.")


if __name__ == "__main__":  # pragma: no cover - container entrypoint helper
    main()
