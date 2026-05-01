# ======================================================
# Data\Engine\services\ansible\ssh_auth.py
# Description: Shared SSH authentication inventory helpers for Engine-side Ansible runs.
# ======================================================

"""Helpers for building Borealis Ansible SSH host variables."""

from __future__ import annotations

from typing import Any, Mapping, MutableMapping


_BOREALIS_COMBINED_AUTH_EXTRA_ARGS = (
    "-o IdentitiesOnly=yes "
    "-o PreferredAuthentications=publickey,password,keyboard-interactive "
    "-o PubkeyAuthentication=yes "
    "-o PasswordAuthentication=yes "
    "-o KbdInteractiveAuthentication=yes"
)
_BOREALIS_KEY_ONLY_AUTH_EXTRA_ARGS = (
    "-o IdentitiesOnly=yes "
    "-o BatchMode=yes "
    "-o PreferredAuthentications=publickey "
    "-o PubkeyAuthentication=yes "
    "-o PasswordAuthentication=no "
    "-o KbdInteractiveAuthentication=no"
)


def _merge_ssh_extra_args(existing: Any, addition: str) -> str:
    current = str(existing or "").strip()
    if not current:
        return addition
    if addition in current:
        return current
    return f"{current} {addition}"


def apply_ssh_credential_host_vars(
    host_vars: MutableMapping[str, Any],
    credential: Mapping[str, Any] | None,
    *,
    private_key_path: str = "",
    include_password: bool = True,
    include_private_key: bool = True,
) -> None:
    """Apply SSH credential fields to Ansible host vars without exposing secret values in logs."""

    if not credential:
        return

    username = str(credential.get("username") or "").strip()
    password = str(credential.get("password") or "").strip()
    become_method = str(credential.get("become_method") or "").strip()
    become_username = str(credential.get("become_username") or "").strip()
    become_password = str(credential.get("become_password") or "").strip()

    if username:
        host_vars["ansible_user"] = username
    if password and include_password:
        host_vars["ansible_password"] = password
        host_vars["ansible_ssh_password_mechanism"] = "sshpass"
    if private_key_path and include_private_key:
        host_vars["ansible_ssh_private_key_file"] = private_key_path
        host_vars["ansible_ssh_extra_args"] = _merge_ssh_extra_args(
            host_vars.get("ansible_ssh_extra_args"),
            _BOREALIS_COMBINED_AUTH_EXTRA_ARGS if include_password else _BOREALIS_KEY_ONLY_AUTH_EXTRA_ARGS,
        )
    if become_method:
        host_vars["ansible_become"] = True
        host_vars["ansible_become_method"] = become_method
        if become_username:
            host_vars["ansible_become_user"] = become_username
        if become_password:
            host_vars["ansible_become_password"] = become_password
