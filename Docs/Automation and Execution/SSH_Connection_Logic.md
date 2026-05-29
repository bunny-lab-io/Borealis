# Borealis SSH Connection Logic

## Purpose
Define standard SSH handshake, probing, credential selection, and Ansible inventory behavior for Borealis Engine-side SSH connections. This page documents the strategy proven by scheduled Ansible jobs 8 and 9 on the Bunny Lab mixed SSH fleet, and should be reused for future SSH-based Borealis features instead of reinventing per-feature connection logic.

## Scope
- Applies to Engine-initiated SSH over managed WireGuard.
- Current implementation is in scheduled Ansible playbook dispatch.
- Intended future consumers include remote shell-like SSH execution, SSH file transfer, watchdog remediation, ad hoc SSH commands, and any feature that needs Engine-to-device SSH.
- Does not replace WinRM, Agent socket jobs, or browser-facing remote shell.

## Current Code References
- Credential host-var rendering: `Data/Engine/Containers/api-backend/data/services/ansible/ssh_auth.py`
- Scheduled Ansible SSH decision logic: `Data/Engine/Containers/api-backend/data/services/API/scheduled_jobs/job_scheduler.py`
- Ansible workspace/config generation: `Data/Engine/Containers/api-backend/data/services/ansible/runner.py`
- Workflow Ansible target rendering: `Data/Engine/Containers/api-backend/data/services/workflows/runtime.py`
- Unit coverage: `Data/Engine/Unit_Tests/test_scheduled_jobs_api.py`, `Data/Engine/Unit_Tests/test_ansible_runner.py`, `Data/Engine/Unit_Tests/test_workflow_runtime.py`

## Design Goals
- Prefer deterministic auth mode per target instead of asking OpenSSH/Ansible to guess.
- Support one Borealis SSH credential containing both password and private key.
- Work across mixed fleets:
  - key-only servers with `PasswordAuthentication no`
  - password-capable servers with `PasswordAuthentication yes`
  - servers that accept both but only one credential material is valid
- Avoid spraying a shared password at key-only or key-capable hosts.
- Avoid `sshpass` account-lockout guard breaking whole multi-host playbook runs.
- Preserve normal Ansible recap behavior for task, connectivity, and auth failures.
- Keep secrets out of logs.
- Keep per-run SSH control sockets and temporary files isolated.

## High-Level Rule
Never pass a combined key+password inventory to Ansible unless there is no better information. A combined inventory lets OpenSSH try the key, then lets `sshpass` inject password during normal Ansible retries. That can turn one bad password into a run-wide auth failure pattern.

When both key and password exist, Borealis chooses exactly one final auth mode per target:
- `key`
- `password`
- `combined` only when Borealis cannot make a decision because required inputs are missing

## Required Inputs
Each SSH connection attempt needs:
- target host or WireGuard peer IP
- target SSH port
- username
- optional account password
- optional private key text
- optional private key passphrase
- optional become method/user/password
- active WireGuard tunnel and agent-side readiness for target port

Current Ansible SSH jobs use WireGuard peer IPs, not public DNS names, once remote targets are resolved.

## Processing Order
1. Resolve scheduled/ad hoc targets into concrete devices.
2. Resolve site scope and duplicate hostnames into stable inventory aliases.
3. Prepare or refresh WireGuard tunnels for selected devices.
4. Wait for agent-side `/api/agent/vpn/ready` for current tunnel and SSH port.
5. Load selected SSH credential.
6. Normalize private key text.
7. Reject passphrase-only private keys for Engine SSH, unless password exists.
8. If password exists with passphrase-protected key, ignore key and use password path.
9. If credential has only key, render key-only inventory.
10. If credential has only password, render password-only inventory.
11. If credential has both key and password, run mixed-auth probing.
12. Build Ansible inventory with one final auth mode per target.
13. Run Ansible from isolated workspace.
14. Record recap and target/run status.

## Mixed-Auth Probe Algorithm
Mixed auth means selected credential has both account password and private key.

Algorithm:
1. Run key-only probe first.
2. If key-only probe succeeds, choose `key`.
3. If key-only probe fails or times out, run exactly one password-only probe.
4. If password-only probe succeeds, choose `password`.
5. If password-only probe fails or times out, choose `key`.

Why this order:
- Key should be preferred because many servers disable password auth.
- Password should still be tested because many Linux servers allow both, but stored key may not be authorized for that account.
- Password should be tested in a controlled one-shot probe, not through Ansible retries.
- If both probes fail, key-only is safer than letting `sshpass` repeatedly send a bad password.

Current scheduler entrypoint:
- `_resolve_mixed_ssh_auth_mode(...)` in `job_scheduler.py`

Return modes:
- `key`: include private key, exclude password
- `password`: include password, exclude private key
- `combined`: include both only when probe inputs are incomplete

## Probe Command Behavior
Probe function:
- `_preflight_ssh_session(...)` in `job_scheduler.py`

Probe uses system `ssh` plus `pexpect` from inside api-backend.

Base SSH options:
```text
-T
-o ConnectTimeout=<timeout>
-o ConnectionAttempts=1
-o UserKnownHostsFile=<temp known_hosts>
-o GlobalKnownHostsFile=/dev/null
-o StrictHostKeyChecking=no
-o UpdateHostKeys=no
-o PreferredAuthentications=publickey,password,keyboard-interactive
-o PubkeyAuthentication=yes
-o PasswordAuthentication=yes
-o KbdInteractiveAuthentication=yes
-o NumberOfPasswordPrompts=1
-o ServerAliveInterval=5
-o ServerAliveCountMax=1
-p <port>
```

Key-only probe adds:
```text
-o IdentityFile=<temp key path>
-o IdentitiesOnly=yes
-o BatchMode=yes
-o PreferredAuthentications=publickey
-o PasswordAuthentication=no
-o KbdInteractiveAuthentication=no
```

Password-only probe omits `IdentityFile` and uses one password prompt maximum.

Probe remote command:
```text
printf '%s\n' __BOREALIS_LOGIN_OK__ &&
mkdir -p /tmp/.ansible-borealis &&
printf '%s\n' __BOREALIS_READY__
```

If sudo/become validation is requested, a sudo probe step is inserted between login and ready markers.

Probe success:
- ready marker observed
- returns empty string

Probe failures:
- `ssh_client_unavailable`
- `ssh_probe_dependency_unavailable`
- `ssh_session_timeout`
- `ssh_password_required`
- `sudo_password_required`
- `permission_denied`
- `ssh_session_failed:<first 80 chars of transcript>`
- `ssh_session_failed`

## Probe Timeout
Default timeout:
- `BOREALIS_SHARED_ANSIBLE_SSH_SESSION_TIMEOUT_SECONDS`
- default value: `20`

Timeout is intentionally not treated as password failure. A target can time out during key probe but later succeed with password. Bunny Lab confirmed this on mixed password-capable Linux devices.

Timeout handling:
- key probe timeout -> run password probe
- password probe timeout -> keep key-only inventory

## Inventory Rendering
Use shared helper:
- `apply_ssh_credential_host_vars(...)` in `services/ansible/ssh_auth.py`

Common fields:
- `ansible_connection = ssh`
- `ansible_host = <WireGuard peer IP>`
- `ansible_user = <credential username>`
- `ansible_port = <port>` only when not `22`
- become fields when present

Password-only inventory:
```yaml
ansible_user: nicole
ansible_password: <redacted>
ansible_ssh_password_mechanism: sshpass
```

Key-only inventory:
```yaml
ansible_user: nicole
ansible_ssh_private_key_file: "{{BOREALIS_RUNTIME_DIR}}/auth/id_borealis_ssh"
ansible_ssh_extra_args: >-
  -o IdentitiesOnly=yes
  -o BatchMode=yes
  -o PreferredAuthentications=publickey
  -o PubkeyAuthentication=yes
  -o PasswordAuthentication=no
  -o KbdInteractiveAuthentication=no
```

Combined inventory, fallback only:
```yaml
ansible_user: nicole
ansible_password: <redacted>
ansible_ssh_password_mechanism: sshpass
ansible_ssh_private_key_file: "{{BOREALIS_RUNTIME_DIR}}/auth/id_borealis_ssh"
ansible_ssh_extra_args: >-
  -o IdentitiesOnly=yes
  -o PreferredAuthentications=publickey,password,keyboard-interactive
  -o PubkeyAuthentication=yes
  -o PasswordAuthentication=yes
  -o KbdInteractiveAuthentication=yes
```

## Ansible Runtime Settings
Engine-generated `ansible.cfg` includes:
```ini
[defaults]
host_key_checking = False
retry_files_enabled = False
interpreter_python = auto_silent
remote_tmp = /tmp/.ansible-borealis

[ssh_connection]
control_path_dir = <short per-run control dir>
password_mechanism = sshpass
ssh_common_args = -o ControlMaster=no -o ControlPersist=no ...
```

Important choices:
- `password_mechanism = sshpass` keeps Ansible password behavior explicit.
- Control sockets live in a short per-run directory under `/tmp/ansible_controlplane`.
- Workspaces live under `Engine/Services/api-backend/cache/Ansible/Generated/Runtime/<run_id>`.
- Workspaces and control dirs are deleted after run finalization.
- Host key checking is disabled because Borealis targets ephemeral WireGuard peer IPs.
- `remote_tmp` is `/tmp/.ansible-borealis`.

## File Transfer Defaults
Scheduled SSH playbooks default to:
- `ansible_ssh_transfer_method = scp`
- `ansible_scp_extra_args = -O`

Reason:
- Some WireGuard-backed peers stalled in SFTP subsystem.
- Some later stalled in `piped`/`dd` transfer path.
- OpenSSH 9 SCP compatibility needs `-O`.

Overrides:
- `BOREALIS_SHARED_ANSIBLE_SSH_TRANSFER_METHOD`
- `BOREALIS_SHARED_ANSIBLE_SCP_EXTRA_ARGS`

## Shared vs Individual Runs
Shared run (`ssh`):
- one playbook process
- one generated inventory containing all eligible targets
- one recap showing all hosts
- auth mode still chosen per target

Individual run (`ssh_individual`):
- one playbook process per target
- one-host inventory per run
- per-device stdout/stderr/status
- same auth selection logic

Both modes must use same probe and inventory rules.

## Logging
Mixed auth decision log:
```text
mixed ssh credential auth probe selected mode |
host=<ip> port=<port>
auth_mode=<key|password|combined>
probe_result=<summary>
key_probe_result=<summary>
password_probe_result=<summary|not_run|password_accepted>
```

Do log:
- host/IP
- port
- selected auth mode
- summarized probe result
- run ID, job ID, component context where available

Do not log:
- password
- private key
- private key passphrase
- become password
- full command containing secrets

## Security Considerations
- Private key is written only into per-run runtime files with mode `0600`.
- Probe key file is temporary and removed in `finally`.
- Probe known_hosts is temporary and removed in `finally`.
- Password is only sent by `pexpect` when password probe or sudo probe explicitly needs it.
- Normal Ansible should never receive password for a target unless password probe succeeded.
- Passphrase-protected SSH keys are not supported in Engine Ansible today. If password also exists, Borealis falls back to password. If no password exists, run fails with `credential_private_key_passphrase_unsupported`.

## Failure Interpretation
`permission_denied` during key probe:
- stored private key is not accepted for that username on that host
- run password probe if password exists

`ssh_session_timeout` during key probe:
- key path inconclusive
- run password probe if password exists

`permission_denied` during password probe:
- stored password is not accepted, or host denies password for that user/source
- keep key-only inventory to avoid bad password retry spam

`ssh_session_timeout` during password probe:
- password path inconclusive
- keep key-only inventory

Ansible recap `Permission denied (publickey,password)` after final auth selection:
- final selected auth material failed at OpenSSH layer
- compare scheduler probe log with host sshd configuration
- if `auth_mode=key`, inspect authorized_keys and username
- if `auth_mode=password`, inspect stored password and server `PasswordAuthentication`

## Bunny Lab Validation Pattern
Known successful mixed fleet behavior:
- key-only hosts:
  - `PasswordAuthentication no`
  - `PubkeyAuthentication yes`
  - final mode should be `key`
- password-capable hosts:
  - `PasswordAuthentication yes`
  - `PubkeyAuthentication yes`
  - final mode may be `key` if key accepted
  - final mode may be `password` if key fails but password probe succeeds

Jobs 8 and 9 validated:
- individual playbook runs against 12 Linux devices
- shared playbook run against same 12 Linux devices
- all devices succeeded after per-target auth selection used controlled probes.

## Future Standardization Requirements
All future Borealis SSH features should share one SSH auth resolver instead of duplicating scheduler logic. The reusable API should accept:
- host
- port
- username
- password
- private key
- private key passphrase
- become settings
- timeout settings
- optional feature/run context for logs

It should return:
- selected auth mode
- sanitized probe summary
- host vars or equivalent SSH client config
- temporary runtime files to stage
- user-safe failure reason

Do not let future features:
- pass both key and password blindly
- run password retries without prior password probe success
- use long-lived control sockets across runs
- log secret-bearing command lines
- infer password support from sshd config alone

## Related Documentation
- [Scheduled Jobs](scheduled-jobs.md)
- [VPN and Remote Access](../Using%20the%20Platform/vpn-and-remote-access.md)
- [Logging and Operations](../Using%20the%20Platform/logging-and-operations.md)

??? example "Detailed Codex Breakdown"

    - Reuse `apply_ssh_credential_host_vars(...)` for Ansible inventory rendering.
    - Keep `_resolve_mixed_ssh_auth_mode(...)` behavior as source of truth until a shared SSH auth service is extracted.
    - If editing SSH behavior, run at least:
    ```bash
    ./Engine_Unit_Tests.sh --domain scheduler
    ./Engine_Unit_Tests.sh --domain ansible
    ```
    - Add focused tests for:
      - key accepted
      - key denied then password accepted
      - key denied then password denied
      - key timeout then password accepted
      - key timeout then password timeout/denied
      - passphrase-only private key
    - After deployment, validate both shared and individual Ansible modes against mixed key-only/password-capable hosts.
