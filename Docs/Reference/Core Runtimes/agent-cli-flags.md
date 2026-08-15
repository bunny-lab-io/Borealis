# Agent CLI Flags

The Agent binary owns first install, service repair, update checks, metadata field changes, and a few internal helper modes. Use this page when running `Agent.exe` on Windows or `Agent` on Linux outside the WebUI.

!!! warning

    Agent install, update, service, watchdog, and uninstall commands must run elevated. On Windows, use an Administrator PowerShell session. On Linux, use root or `sudo`.

## Normal Commands

Use these commands for normal operator work. Replace URLs, enrollment codes, and field numbers with values from your Engine.

### Install Or Re-Deploy

=== "Windows"

    ```powershell
    .\Agent.exe --server-url "https://borealis.example.com" --site-enrollment-code "SITE-CODE"
    ```

    Windows downloaded `Agent.exe` enters bootstrap mode when no runtime flag is present. Bootstrap mode stages the runtime under `C:\Borealis`, reconciles Windows support dependencies, writes `agent.json`, creates the `BorealisAgent` service, and creates AutoUpdater and Watchdog scheduled tasks. If Borealis is already installed, the command performs an in-place redeploy: it stops Borealis-managed services, tasks, and processes, replaces `Agent.exe`, preserves existing identity and trust data in `agent.json`, and starts the service again. If Windows still holds the old binary open, bootstrap stages a deferred replacement and retries after the old process exits.

=== "Linux"

    ```bash
    chmod +x ./Agent
    sudo ./Agent --server-url "https://borealis.example.com" --site-enrollment-code "SITE-CODE"
    ```

    Linux uses the normal runtime parser. Supplying `--server-url` and `--site-enrollment-code` implies service install, stages the runtime under `/opt/Borealis/Agent/Agent`, writes `agent.json`, creates systemd units, and starts the Agent service. If Borealis is already installed, the command stops Borealis-managed systemd units and the WireGuard interface, preserves existing identity and trust data in `agent.json`, replaces the runtime binary, and starts the service again. The root WireGuard role installs missing `wireguard-tools` through the detected OS package manager when tunnel setup begins.

!!! warning

    Install and re-deploy inputs preserve existing Agent identity when `agent.json` is present. Always provide both `--server-url` and `--site-enrollment-code` when installing or re-enrolling a device.

!!! info "Internal-Only Engine"

    Internal-Only Engine install commands from Sites include `--trusted-engine-ca-b64` and `--server-ip-fallback` automatically when the Engine exposes local CA and fallback IP metadata. Use the WebUI command when possible so agents receive the Borealis local CA bundle and route hint. Agents reject raw IP `--server-url` values for install and enrollment; use the Engine FQDN.

??? note "Manual Internal-Only install"

    ```bash
    sudo ./Agent --server-url "https://borealis.internal.example" --site-enrollment-code "SITE-CODE" --trusted-engine-ca-b64 "<base64-ca-pem>" --server-ip-fallback "192.168.3.251"
    ```

### Metadata Fields

=== "Windows"

    ```powershell
    & "C:\Borealis\Agent.exe" --metadata get 1
    & "C:\Borealis\Agent.exe" --metadata set 1 "Asset tag 123"
    & "C:\Borealis\Agent.exe" --metadata set 1 ""
    ```

=== "Linux"

    ```bash
    sudo /opt/Borealis/Agent/Agent --metadata get 1
    sudo /opt/Borealis/Agent/Agent --metadata set 1 "Asset tag 123"
    sudo /opt/Borealis/Agent/Agent --metadata set 1 ""
    ```

`set` queues a local metadata update in `metadata-queue.json`. The next heartbeat sends it to the Engine, and the Engine acknowledgement removes the queued entry. `get` returns a pending queued value first; otherwise it reads the Engine value through the device-authenticated API. Empty `set` values queue a clear. Metadata field numbers are `1` through `500`, and decoded values are capped at `1024` characters.

### Update Check

=== "Windows"

    ```powershell
    & "C:\Borealis\Agent.exe" --update-check --config-path "C:\Borealis\agent.json"
    ```

=== "Linux"

    ```bash
    sudo /opt/Borealis/Agent/Agent --update-check --config-path /opt/Borealis/Agent/agent.json
    ```

`--update-check` runs one local release-channel check. Stable channel uses the Engine release manifest. Unstable/source branch updates resolve the target commit and stage a branch build. The updater validates the candidate with `--validate-config` before replacing the runtime binary.

### Validate Configuration

=== "Windows"

    ```powershell
    & "C:\Borealis\Agent.exe" --validate-config --config-path "C:\Borealis\agent.json"
    ```

=== "Linux"

    ```bash
    sudo /opt/Borealis/Agent/Agent --validate-config --config-path /opt/Borealis/Agent/agent.json
    ```

Successful validation prints `agent config ok`. Validation checks that `agent.json` can be read and that its `schema_version` is not newer than the running Agent binary supports.

### Service Removal

=== "Windows"

    ```powershell
    & "C:\Borealis\Agent.exe" --uninstall-service
    ```

    Removes the `BorealisAgent` Windows service and deletes the AutoUpdater and Watchdog scheduled tasks. It leaves `C:\Borealis` and dependency installs in place.

=== "Linux"

    ```bash
    sudo /opt/Borealis/Agent/Agent --uninstall-service
    ```

    Stops and removes `borealis-agent.service`, `borealis-agent-updater.service`, `borealis-agent-updater.timer`, `borealis-agent-watchdog.service`, and `borealis-agent-watchdog.timer`. It leaves `/opt/Borealis` in place.

!!! danger

    Windows full cleanup uses bootstrap `-uninstall`, not runtime `--uninstall-service`. It removes Borealis-owned services, scheduled tasks, dependency install roots, processes, WireGuard tunnel services, UltraVNC services, and the Agent install directory.

    ```powershell
    .\Agent.exe -uninstall
    ```

## Windows Bootstrap Arguments

Downloaded Windows `Agent.exe` uses bootstrap mode unless one of the runtime flags listed in the next section is present. Bootstrap mode accepts only these arguments.

| Argument | Use | Notes |
| --- | --- | --- |
| `--server-url <url>` | Engine public URL for install, re-deploy, or repair. | Fresh bootstrap requires this with `--site-enrollment-code`. |
| `--site-enrollment-code <code>` | Site enrollment code. | Required for fresh bootstrap. `--enrollment-code` is accepted as an alias. |
| `--trusted-engine-ca-b64 <base64-pem>` | Borealis local CA bundle for Internal-Only Engine installs. | Stored in `agent.json` as `trust.engine_ca_pem`. |
| `--trusted-engine-ca-pem <pem>` | Borealis local CA PEM for Internal-Only Engine installs. | Prefer `--trusted-engine-ca-b64` for copied commands. |
| `--repo-ref <ref>` | Git branch, tag, or commit used for source/unstable bootstrap payloads. | Non-`main` refs default the release channel to `unstable`. |
| `--repo-branch <ref>` | Legacy alias for `--repo-ref`. | Prefer `--repo-ref`. |
| `--release-channel <channel>` | Agent release channel. | `stable`, `release`, and `releases` normalize to stable. `unstable`, `source`, `branch`, `repo`, and `repository` normalize to unstable. |
| `--server-ip-fallback <ip>` | Persist Internal-Only Engine IP route hint. | Stores `server_ip_fallback` in `agent.json`; HTTPS still uses the Engine FQDN for host, SNI, and certificate validation. |
| `--verbose` | Write verbose bootstrap diagnostics. | Also accepted as `-verbose`. |
| `-uninstall` | Full Windows Agent cleanup. | Single dash. Destructive. Removes Borealis-owned services, tasks, dependencies, and install state. |

## Runtime Flags

These flags are parsed by the cross-platform Agent runtime. On Windows, passing any runtime flag bypasses bootstrap mode.

| Flag | Windows | Linux | Use |
| --- | --- | --- | --- |
| `--config-path <path>` | Full | Full | Use a specific `agent.json`. Defaults beside the running binary. |
| `--server-url <url>` | Full | Full | Override or persist Engine URL during install, runtime start, or update check. |
| `--server-ip-fallback <ip>` | Full | Full | Persist an Internal-Only Engine IP route hint in `agent.json` as `server_ip_fallback`. The Agent still uses `--server-url` for request host, SNI, and certificate hostname validation. Linux WireGuard setup uses it only after the FQDN endpoint fails DNS resolution. |
| `--site-enrollment-code <code>` | Full | Full | Enrollment code for runtime install/start. Alias target is shared with `--enrollment-code`. |
| `--enrollment-code <code>` | Full | Full | Runtime alias for `--site-enrollment-code`. Not accepted by Windows bootstrap mode. |
| `--trusted-engine-ca-b64 <base64-pem>` | Full | Full | Persist Borealis local CA bundle for Internal-Only Engine HTTPS validation. |
| `--trusted-engine-ca-pem <pem>` | Full | Full | Persist Borealis local CA PEM. Prefer base64 form for shell-safe commands. |
| `--repo-ref <ref>` | Full | Full | Set or update Agent branch/ref. Non-`main` refs infer unstable release channel unless `--release-channel` overrides it. |
| `--release-channel <channel>` | Full | Full | Set or update release channel. Accepted values normalize the same as Windows bootstrap mode. |
| `--verbose` | Full | Full | Mirror runtime logs to stdout. Bootstrap also accepts `-verbose` on Windows. |
| `--once` | Full | Full | Authenticate, start roles enough to post one status/heartbeat cycle, then exit before Socket.IO steady state. Useful for diagnostics. |
| `--install-service` | Full | Full | Install or repair managed service. Linux also writes updater/watchdog systemd units and timers. Windows runtime path creates or updates the `BorealisAgent` service; normal Windows bootstrap creates the support tasks. |
| `--uninstall-service` | Full | Full | Remove managed service and updater/watchdog task or timer entries. Leaves install root and dependency state in place. |
| `--update-check` | Full | Full | Run one local update check. Can be combined with `--config-path`, `--server-url`, `--repo-ref`, `--release-channel`, and `--verbose`. |
| `--watchdog-check` | Full | Full | Run one local watchdog pass. Normally scheduled every minute by Windows Task Scheduler or Linux systemd timer. Repairs missing/stopped/stale Agent service state. |
| `--validate-config` | Full | Full | Validate `agent.json` compatibility and exit. Used by update candidates before replacement. |
| `--metadata` | Full | Full | Run metadata subcommand: `--metadata get <field>` or `--metadata set <field> <value>`. |
| `--version` | Full | Full | Print compiled Agent version/build ID and exit. |
| `--service` | Full | Full | Run as managed Agent service. Internal service-manager entrypoint. |
| `--finalize-update` | Full | Full | Finalize deferred binary replacement by writing the installed build ID after verification. Internal updater path. |
| `--build-id <id>` | Full | Full | Build ID written by `--finalize-update`. Internal updater path. |
| `--expected-sha256 <sha256>` | Full | Full | Expected running binary hash checked by `--finalize-update`. Internal updater path. |
| `--helper` | Full | Full parser only | Run current-user helper sentinel. Windows uses this for tray/helper sessions; Linux helper mode has no operator workflow. |
| `--helper-session-id <id>` | Full | Full parser only | Windows helper session ID. Internal helper broker path. |
| `--helper-state-dir <path>` | Full | Full parser only | Windows helper status directory. Internal helper broker path. |

!!! tip

    Prefer WebUI enrollment and normal install commands for new devices. Use runtime flags directly for diagnostics, repair, update checks, metadata automation, or support flows.

## Exit Behavior

- `--metadata` returns `0` when get/set succeeds, `1` for runtime/API errors, and `2` for bad usage.
- Windows bootstrap returns `0` when install, explicit re-deploy, or uninstall completes. It returns `73` only when no explicit re-deploy input was supplied and an existing Agent is already enrolled or repaired.
- `--validate-config`, `--update-check`, `--watchdog-check`, `--install-service`, and `--uninstall-service` return nonzero when their local operation fails.

??? example "Detailed Codex Breakdown"

    ### Related documentation

    - [Agent Runtime](agent-runtime.md)
    - [Unit Testing](../Unit_Testing.md)
    - [Architecture Overview](../architecture-overview.md)

    ### Source map

    - Cross-platform runtime flag registration: `Data/Agent/cmd/agent/main.go`.
    - Windows bootstrap parser: `Data/Agent/cmd/agent/bootstrap-config.go`.
    - Windows runtime-flag bootstrap bypass: `Data/Agent/cmd/agent/bootstrap_entry_windows.go`.
    - Windows bootstrap install/redeploy flow: `Data/Agent/cmd/agent/bootstrap-main-windows.go`.
    - Windows full uninstall flow: `Data/Agent/cmd/agent/bootstrap-uninstall.go`.
    - Windows service and scheduled-task wiring: `Data/Agent/internal/runtime/service_windows.go` and `Data/Agent/cmd/agent/bootstrap-tasks.go`.
    - Linux service and timer wiring: `Data/Agent/internal/runtime/service_unix.go`.
    - Standalone update checks: `Data/Agent/cmd/agent/update_standalone_windows.go` and `Data/Agent/cmd/agent/update_standalone_unix.go`.
    - Engine IP fallback dial policy: `Data/Agent/internal/auth/client.go`.
    - Socket.IO fallback dial hook: `Data/Agent/internal/transport/socketio.go`.
    - Deferred update finalization: `Data/Agent/cmd/agent/update_finalize.go`.
    - Metadata queue implementation: `Data/Agent/internal/config/config.go`.

    ### Runtime behavior

    - Windows has two argument surfaces. If no runtime flag from `hasRuntimeFlag()` is present, `Agent.exe` runs bootstrap mode and accepts only the Windows bootstrap arguments. If a runtime flag is present, it skips bootstrap and uses the standard Go `flag` parser. Windows bootstrap mode accepts `--server-ip-fallback` so WebUI install commands can persist Internal-Only route metadata without switching into runtime flag mode.
    - Linux has one argument surface: the standard Go `flag` parser in `main.go`.
    - `--server-url` or `--site-enrollment-code` implies `--install-service` in the runtime parser. `--repo-ref` alone does not imply install-service.
    - Fresh install detection treats `--server-url` or `--site-enrollment-code`/`--enrollment-code` as fresh-deploy intent. Validation requires both server URL and enrollment code before service installation starts. Re-deploy stops Borealis-managed components and preserves existing `agent.json` identity/trust state instead of wiping the install root.
    - Fresh install and runtime server URL overrides require an Engine FQDN. Raw IPs and `localhost` are rejected before enrollment config is written.
    - `--server-ip-fallback` stores a bare non-loopback IP as `server_ip_fallback`. REST, update, file-transfer, software-override, and Socket.IO connections first try the FQDN normally. If that TCP dial fails, they connect to the fallback IP while keeping the original FQDN as the HTTP host and TLS SNI name. Linux WireGuard setup first tries the Engine-provided FQDN endpoint and rewrites the local `wireguard.conf` endpoint to the fallback IP only when `wg-quick up` reports endpoint DNS resolution failure.
    - `--trusted-engine-ca-b64` decodes and stores the Borealis local CA PEM in `agent.json` at `trust.engine_ca_pem`. The Go auth client appends that CA to the system trust pool for REST and Socket.IO without disabling hostname validation.
    - Metadata field selectors accept normal forms such as `1`, `field_001`, `field-1`, and `metadata1`, but operator docs should use numeric field numbers.
