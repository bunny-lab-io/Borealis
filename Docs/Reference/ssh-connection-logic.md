# SSH Connection Logic

Define Borealis Engine-side SSH behavior for Ansible and future SSH-backed features. This is reference material for implementation and troubleshooting, not normal operator guidance.

## Rule

When an SSH credential has both password and private key, Borealis chooses one final auth mode per target instead of blindly passing both to Ansible.

Final modes:

- `key`
- `password`
- `combined` only when required inputs are missing and Borealis cannot decide

## Processing Order

1. Resolve targets into concrete devices.
2. Resolve site scope and duplicate hostnames into stable inventory aliases.
3. Prepare or refresh WireGuard tunnels.
4. Wait for agent-side tunnel readiness for selected SSH port.
5. Load selected credential.
6. Normalize private key text.
7. Reject passphrase-only private keys unless password exists.
8. If only key exists, render key-only inventory.
9. If only password exists, render password-only inventory.
10. If both exist, run mixed-auth probing.
11. Build Ansible inventory with one final auth mode per target.
12. Run Ansible from isolated workspace.
13. Record recap and target/run status.

## Mixed-Auth Probe

1. Run key-only probe first.
2. If key probe succeeds, choose `key`.
3. If key probe fails or times out, run one password-only probe.
4. If password probe succeeds, choose `password`.
5. If password probe fails or times out, choose `key`.

Reason: bad password retries through `sshpass` can trigger account lockout behavior across a multi-host run.

## Inventory Behavior

Key-only inventory includes `ansible_ssh_private_key_file` and disables password/kbd-interactive authentication.

Password-only inventory includes `ansible_password` and `ansible_ssh_password_mechanism: sshpass`.

Combined inventory is fallback-only and should not be normal output when probes have enough information.

## Runtime Defaults

- Control sockets live under short per-run directories beneath `/tmp/ansible_controlplane`.
- Workspaces live under `Engine/Services/api-backend/cache/Ansible/Generated/Runtime/<run_id>`.
- Host key checking is disabled because targets use ephemeral WireGuard peer IPs.
- Remote temp defaults to `/tmp/.ansible-borealis`.
- SSH transfer defaults to `scp` with `-O`.
- Each site worker runs at most two Ansible controller processes by default. Additional individual runs wait for controller capacity.
- Scheduled SSH and WinRM admission waits up to 60 seconds for WireGuard and target-port readiness before skipping unavailable targets.
- Job History renders that persisted deadline as `Establishing Connection - 60s Remaining` and updates it once per second, including after operator navigates away and returns.

## Failure Reading

- `permission_denied` during key probe: key not accepted for user/host; try password probe if present.
- `ssh_session_timeout` during key probe: inconclusive; try password probe if present.
- `permission_denied` during password probe: password not accepted or password auth denied; keep key-only.
- `ssh_session_timeout` during password probe: inconclusive; keep key-only.
- Ansible recap `Permission denied (publickey,password)`: final selected auth material failed at OpenSSH layer.

??? example "Detailed Codex Breakdown"

    ### Related documentation

    - [Ansible Playbooks](../Using%20the%20Platform/Assemblies/ansible-playbooks.md)
    - [Scheduled Jobs](../Using%20the%20Platform/scheduled-jobs.md)
    - [Remote Shell](../Using%20the%20Platform/remote-shell.md)
    - [Credential Management](../Using%20the%20Platform/credential-management.md)

    ### Source map

    - Credential host-var rendering: `Data/Engine/Containers/api-backend/data/services/ansible/ssh_auth.py`
    - Scheduled Ansible decision logic: `Data/Engine/Containers/api-backend/cmd/api-backend/scheduler_execution.go`
    - Ansible workspace/config generation: `Data/Engine/Containers/api-backend/data/services/ansible/runner.py`
    - Workflow Ansible target rendering: `Data/Engine/Containers/api-backend/cmd/api-backend/workflows_runtime.go`
    - Unit coverage: Go scheduler/workflow tests and `Data/Engine/Unit_Tests/test_ansible_runner.py`

    ### Guardrails

    - Reuse `apply_ssh_credential_host_vars(...)`.
    - Keep scheduler auth resolver behavior authoritative until a shared SSH auth service is extracted.
    - Never log password, private key, key passphrase, become password, or secret-bearing command lines.
    - If SSH behavior changes, run:

    ```bash
    ./Engine_Unit_Tests.sh --domain scheduler
    ./Engine_Unit_Tests.sh --domain ansible
    ```
