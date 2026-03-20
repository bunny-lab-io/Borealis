[Back to Docs Index](../index.md) | [Index (HTML)](../index.html)

# Ansible Playbooks

## Purpose
Capture the current Borealis Ansible variable model, the supported execution path, and provide a working assembly example for Linux agent bootstrap.

## Direct Answer
- Do not use Borealis PowerShell-style `$env:variableName` references inside an Ansible playbook assembly.
- Borealis Ansible assemblies should reference variables with Ansible/Jinja syntax such as `{{ server_url }}` and `{{ enrollment_code }}`.
- Borealis Ansible playbooks currently work through Engine-side scheduled jobs on the Linux Engine, including `local`, `ssh`, and `winrm` execution contexts.
- For the Borealis Linux bootstrap flow, you can either reference the assembly variables directly in the bootstrap command or map them into `BOREALIS_SERVER_URL` and `BOREALIS_ENROLLMENT_CODE` via `environment:`.

## Recommended Pattern
Use Borealis assembly variables as Ansible `extra_vars`, stage a local runner script on the target, and launch it in a detached way.

Example playbook:

```yaml
---
- name: Bootstrap Borealis Linux agent
  hosts: all
  gather_facts: false
  become: true
  vars:
    bootstrap_runtime_dir: /run/borealis-agent-bootstrap
    bootstrap_runner: "{{ bootstrap_runtime_dir }}/runner.sh"
    bootstrap_log: "{{ bootstrap_runtime_dir }}/bootstrap.log"
  tasks:
    - name: Create ephemeral Borealis bootstrap runtime directory
      ansible.builtin.file:
        path: "{{ bootstrap_runtime_dir }}"
        state: directory
        mode: "0700"

    - name: Stage detached Borealis bootstrap runner
      ansible.builtin.copy:
        dest: "{{ bootstrap_runner }}"
        mode: "0700"
        content: |
          #!/usr/bin/env bash
          set -o nounset
          set -o pipefail
          runner_path={{ bootstrap_runner | quote }}
          bootstrap_log={{ bootstrap_log | quote }}
          trap 'rm -f "$runner_path"' EXIT
          : >"$bootstrap_log"
          exec >>{{ bootstrap_log | quote }} 2>&1
          echo "[$(date -Is)] Starting Borealis bootstrap."
          export BOREALIS_SERVER_URL={{ server_url | quote }}
          export BOREALIS_ENROLLMENT_CODE={{ enrollment_code | quote }}
          if curl -fsSL {{ bootstrap_url | quote }} | bash -s -- --agent; then
            rc=0
          else
            rc=$?
          fi
          echo "[$(date -Is)] Borealis bootstrap exited with rc=${rc}."
          exit "${rc}"
      no_log: true

    - name: Launch detached Borealis bootstrap runner
      ansible.builtin.shell: |
        set -o errexit
        set -o nounset
        set -o pipefail
        if command -v systemd-run >/dev/null 2>&1 && [[ -d /run/systemd/system ]]; then
          unit_name="borealis-agent-bootstrap-$(date +%s)"
          systemd-run \
            --unit "${unit_name}" \
            --description "Borealis agent bootstrap" \
            /bin/bash {{ bootstrap_runner | quote }}
        else
          nohup /bin/bash {{ bootstrap_runner | quote }} </dev/null >/dev/null 2>&1 &
        fi
      args:
        executable: /bin/bash
      async: 45
      poll: 0
      changed_when: true
```

This still uses the same Borealis bootstrap flow, but it launches the work in a detached local process so the install can keep running even if the target drops off the Engine-managed transport while the old agent is being replaced.

Recommended assembly variables:

- `bootstrap_url`
- `server_url`
- `enrollment_code`

The staged runner uses environment variables instead of putting the enrollment code directly on the process command line.
The runner and log live under `/run`, so they stay ephemeral, the runner deletes itself on exit, and the log file is truncated on each launch instead of growing across repeated runs.

This pattern is best suited for agent redeploy/repair scenarios on already-managed devices. It does not remove the current Borealis requirement that Engine-side `ssh` and `winrm` scheduled jobs target devices that already have enough Borealis state for WireGuard-backed targeting.

## Example Playbook
- The plain playbook lives at [`ansible-linux-agent-bootstrap.yml`](ansible-linux-agent-bootstrap.yml).
- The example uses snake_case variable names because that is the safest Ansible style for `extra_vars`.
- In the Borealis assembly editor, define the variables `bootstrap_url`, `server_url`, and `enrollment_code` so they can be passed into this playbook.

## Current Runtime Caveat
- Borealis Ansible playbooks currently execute through the Engine-side scheduled-job pipeline.
- Quick-run for Ansible remains unimplemented in the current Engine runtime.
- Recap/report APIs and some recap-oriented UI surfaces are still being rounded out.

## Related Documentation
- [Assemblies and Quick Jobs](../assemblies.md)
- [Scheduled Jobs](../scheduled-jobs.md)
- [Getting Started](../getting-started.md)
- [Agent Runtime](../agent-runtime.md)

## Codex Agent (Detailed)
### Variable syntax rules
- PowerShell assemblies use the Borealis `$env:VAR` rewrite path.
- Ansible assemblies do not use that rewrite path.
- Borealis passes Ansible assembly variable values through the Engine as `variable_values`, and the Ansible runner writes them to `--extra-vars`.
- Because of that, Ansible playbooks should reference the variables directly as Jinja values, not as PowerShell environment expressions.

### Why the direct-Jinja command works well
- Borealis passes Ansible assembly variables into `ansible-playbook` as `--extra-vars`.
- That means the editor can keep playbooks in normal Ansible form such as `{{ server_url }}` without a Borealis-specific rewrite step.
- The included bootstrap example still mirrors the original command closely, which makes it easy for operators to audit.

### Why the detached runner pattern helps
- `install_or_update_borealis_agent()` stops the current agent supervision before staging and restarting the agent runtime.
- Launching the bootstrap work through `systemd-run` or `nohup` lets the install continue on the remote host even if the session that launched it disappears.
- The runner writes progress to an ephemeral local log file under `/run`, and that log is overwritten on each launch so repeated redeploys do not accumulate output on disk.

### Why the environment mapping remains useful
- `bootstrap.sh` forwards arguments into `Borealis.sh`.
- `Borealis.sh` already checks `BOREALIS_SERVER_URL` and `BOREALIS_ENROLLMENT_CODE`.
- Exporting those variables inside the staged runner keeps the enrollment code off the shell command line while still using the stock Borealis bootstrap flow.

### Recommended naming
- Prefer `server_url` over `serverURL`.
- Prefer `enrollment_code` over `enrollmentCode`.
- Lowercase underscore names are the safest Ansible variable style and reduce ambiguity across YAML, Jinja, and future templating.

### Example assembly shape
- For this playbook, the assembly only needs matching Borealis variables plus the YAML body.

### Execution expectations
- Treat the included YAML as the reference playbook for the currently supported scheduled-job path.
- The working path today is Engine-side scheduled execution; quick-run and some surrounding UX are still incomplete.
