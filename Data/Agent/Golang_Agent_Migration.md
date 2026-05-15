# Rewrite Borealis Agent In Go

Back to Docs Index: ../../Docs/index.md

## Decisions

1. Agent source now lives under `Data/Agent`; legacy Python agent source moved to `Data/Agent_Old`.
2. Fresh install runtime is one compiled Go binary named `Agent.exe`.
3. Windows `Agent.exe` owns bootstrap, deployment, repair, update checks, service task registration, and runtime execution.
4. Linux `Agent` owns deployment, self-staging, runtime execution, systemd service install/uninstall, and update timer installation.
5. Installed runtime uses one `config.json` beside `Agent.exe`.
6. `config.json` stores keys, tokens, trust material, enrollment code, server URL, agent identity, agent branch, and installed build ID only.
7. `config.json` protection uses filesystem permissions only: Windows ACL hardening is deferred and Windows files inherit from `C:\` for now; Linux remains root-owned `0600` with parent `0700`.
8. SQLite is not used in v1 because configuration is small, single-writer, and not query-heavy.
9. No installed Agent runtime path may depend on Python.
10. Non-migrated roles stay disabled or degraded in Go status payloads; no Python fallback.
11. First PR assumes clean installs only; no config cleanup or backward-compatible migration logic is carried for removed fields.

## Numbered Feature Backlog

1. Core binary, CLI flags, logging, bootstrap entrypoint, service install/uninstall.
2. `config.json` schema, atomic load/save, Windows inherited permissions, Linux ownership/permissions, and branch persistence.
3. Ed25519 identity, enrollment, token refresh, script-signing key persistence.
4. Engine REST client and Socket.IO transport.
5. Startup status, heartbeat, role-health payloads.
6. SYSTEM quick-job execution for PowerShell, Batch, Bash, env/variables, timeout, signed payload verification.
7. Windows CURRENTUSER helper broker and script execution.
8. Device audit inventory.
9. File management RPC and transfer endpoints.
10. Process management.
11. Service management.
12. Software inventory/actions.
13. WireGuard tunnel lifecycle.
14. Remote shell over WireGuard.
15. VNC lifecycle and credential broker.
16. Agent self-update and release channels.
17. Tray UI/status, optional later.
18. Legacy macros/node screenshot are retired from the Go Agent migration scope.
19. Linux deployment/bootstrap parity with Windows `Agent.exe`.

## Completed

1. Moved old agent codebase from `Data/Agent` to `Data/Agent_Old`.
2. Added Go module under `Data/Agent`.
3. Added `cmd/agent` runtime entrypoint with CLI flags.
4. Added compiled role registry foundation under `internal/roles`.
5. Added `internal/config` schema and atomic save path with Windows inherited permissions and Linux restricted permissions.
6. Added Ed25519 identity generation and persistence.
7. Added enrollment, token refresh, and authenticated REST helper.
8. Added minimal Engine.IO/Socket.IO client.
9. Added heartbeat/status payloads with Go runtime capability markers and full startup timeline milestones.
10. Added SYSTEM quick-job execution path with script decoding, env mapping, timeouts, and signature verification.
11. Added Windows bootstrap integration into same `Agent.exe` package.
12. Rewired Windows bootstrap deploy/repair to self-stage `Agent.exe`, write `config.json`, and run scheduled tasks directly.
13. Removed Python runtime staging from new Agent bootstrap path.
14. Added update artifact path for prebuilt Go `Agent.exe` payloads with deferred self-replacement on Windows.
15. Updated Agent unit test entrypoints to run Go tests and Windows/Linux builds.
16. Updated Engine onboarding lookup paths and install commands to use Go Agent artifacts under `Data/Agent/dist`.
17. Updated Agent runtime/security/testing docs for Go Agent, `config.json`, and legacy source relocation.
18. Updated SBOM Agent dependency inventory for Go runtime dependencies.
19. Built Windows `Agent.exe` and Linux `Agent` artifacts under `Data/Agent/dist`.
20. Validated Windows real-host enrollment, approval, startup timeline, and SYSTEM PowerShell quick-job execution.
21. Validated Linux real-host systemd service install/start and root-owned `0600` `config.json`.
22. Validated Linux real-host enrollment, online state, and SYSTEM Bash quick-job execution.
23. Added Windows CURRENTUSER direct session launch for signed PowerShell and Batch quick jobs through the SYSTEM runtime.
24. Validated Windows CURRENTUSER real-host script execution with user-writable Desktop canary success and root `C:\` write denial.
25. Preserved startup timeline telemetry across heartbeat role-health merges by moving timeline status into a startup context.
26. Cleaned PowerShell CLIXML progress/error output from Go quick-job result streams.
27. Simplified Site install commands and removed nonessential `config.json` runtime metadata.
28. Added Go device audit inventory for CPU, memory, storage, network, internal IP, device type, uptime, and last user via heartbeat payloads.
29. Validated Windows CURRENTUSER Batch quick-job execution on a real Windows target.
30. Improved Go device audit inventory for Windows OS/build, domain-qualified last user, model/serial, last reboot, removable media, and network link speed.
31. Added storage media classification so fixed disks can report SSD/HDD instead of only Fixed Disk.
32. Added motherboard/baseboard serial fallback when device BIOS/product serial is unavailable.
33. Validated Go device audit inventory on real Windows/Linux targets and marked device audit migration complete.
34. Added Go file management RPC role for browse, conflict preflight, lightweight text editing, mkdir, rename, move, copy/cut paste, delete, upload pull, and download artifact transfer.
35. Validated Go File Management on real hosts for CRUD actions, uploads, downloads, and full-folder upload/download transfers.
36. Added Go Process Management role for live process snapshots, cache reuse, parent/child metadata, and process termination.
37. Validated Go Process Management task termination on real Windows/Linux targets and Linux CPU/memory reporting.
38. Added process metric collectors for Windows CPU/disk activity plus Linux disk/network rate deltas and Windows TCP network rate deltas.
39. Validated Go Process Management CPU, disk, network, memory, and task termination metrics on real Windows/Linux targets and marked process management migration complete.
40. Added Go Service Management role for Windows service inventory, Linux systemd service inventory, Engine details publishing, role health, and operator start/stop/restart requests.
41. Validated Go Service Management inventory and service start/stop/restart actions on real Windows/Linux targets and marked service management migration complete.
42. Added Go Software Management role for Windows installed-app inventory, Linux dpkg/rpm inventory, Engine details publishing, refresh requests, role health, and post-uninstall inventory refresh through the SYSTEM quick-job lane.
43. Added Go Software Management Windows icon extraction/cache payloads with Engine icon override support, matching the legacy installed-software icon pipeline.
44. Validated Go Software Management inventory, icon cache payloads, and uninstall-triggered refresh behavior on a real Windows target and marked software management migration complete.
45. Added Go WireGuard tunnel role for persistent `/api/agent/vpn/ensure` polling, `vpn_tunnel_start` handling, signed orchestration token validation, side-by-side `wireguard.conf`, Windows tunnel-service apply using the `WireGuardTunnel$wireguard` service derived from the config basename, Linux `wg-quick` apply, firewall readiness, role health, and `/api/agent/vpn/ready` reporting.
46. Added startup cleanup of the install-root `Temp` directory so onboarding payload, state, and stdout leftovers do not persist after Agent runtime starts.
47. Fixed Windows WireGuard role-health refresh, serialized concurrent tunnel applies, and corrected firewall port creation to pass PowerShell port arrays instead of quoted comma-separated strings.
48. Moved Go WireGuard role logs from `Logs/VPN_Tunnel/tunnel.log` to `Logs/WireGuard/wireguard.log`.
49. Changed Windows bootstrap logging so `Logs/Agent/bootstrap.log` is truncated at each bootstrap start and contains only the latest run.
50. Split Windows bootstrap logging so `Logs/Agent/bootstrap.log` always receives verbose trace/marker output while console, stdout, and stderr show only operator-facing step/warn/error lines.
51. Fixed Linux WireGuard health checks to follow the side-by-side config basename interface (`wireguard`) and clean legacy `borealis` links during recovery.
52. Validated Go WireGuard tunnel lifecycle on real Windows/Linux targets and marked WireGuard migration complete.
53. Added Go Remote Shell role with WireGuard-scoped TCP listener, existing JSONL/base64 Engine bridge protocol, PowerShell/Bash subprocess execution, ready/keepalive pong handling, stdout correlation metadata, one active session replacement, log output, and heartbeat role health.
54. Validated Go Remote Shell over WireGuard on real Windows/Linux targets and marked Remote Shell migration complete.
55. Added Go VNC lifecycle and credential broker role for Windows UltraVNC always-on service management, runtime credential generation, UltraVNC config/password hash writing, Engine `/api/agent/vnc/ensure` bootstrap, `vnc_start`/`vnc_stop`/`vnc_refresh`/`vnc_credential_request` Socket.IO handling, firewall scope, listener readiness, logs, and role health.
56. Validated Go VNC lifecycle and credential broker on a real Windows target and marked VNC migration complete.
57. Added Engine release-channel packaging for Go Agent binary bundles containing prebuilt Windows and Linux agent artifacts, authenticated download manifests, SHA-256 validation, and release-channel UI wording.
58. Added Go Agent update request handling, runtime build/update status heartbeat payloads, Windows `--update-check` AutoUpdater task action, Linux `--update-check` binary staging, and Linux systemd updater service/timer support.
59. Added Linux deployment/bootstrap parity so a temp-downloaded `Agent` self-stages into `/opt/Borealis/Agent/Agent`, writes `config.json`, installs `borealis-agent.service`, and enables hourly update checks.
60. Added Windows CURRENTUSER same-binary helper sentinel broker for active desktop session readiness while keeping signed quick-job execution brokered by SYSTEM `CreateProcessAsUser`.
61. Retired legacy macros/node screenshot from the Go Agent migration scope by decision; no Go port will be implemented in this PR.
62. Fixed Windows healthy-bootstrap update checks so the temp bootstrap executable no longer tries to overwrite a locked installed `Agent.exe` before stopping the runtime, and so the AutoUpdater task is reconciled even when the installed build is already current.
63. Added `agent.branch` to `config.json` so Windows and Linux update checks remain pinned to the operator-selected install branch instead of falling back to Engine release-channel/main updates.
64. Moved installed build tracking into `config.json` as `agent.installed_build_id`; the Go Agent no longer creates or reads an `installed_build_id.txt` sidecar.
65. Moved Windows update archives and extracted repository payloads into `C:\Borealis\Temp\Updater`, cleaned `C:\Borealis\Temp` when update checks finish, and removed accidental legacy `C:\Borealis\Agent` update workspaces.
66. Removed persistent `update_status.json`; update checks now use `config.json` for branch/build identity and delete any old updater status sidecar instead of writing state/update_available metadata.
67. Changed Windows update cleanup so updater workspaces are removed immediately, full `C:\Borealis\Temp` cleanup is deferred until after bootstrap exits, and locked onboarding stdout handles no longer create operator-facing warnings.
68. Moved Windows dependency version tracking for WireGuard and UltraVNC into `config.json` under `dependency_versions`, removed `installed_version.txt` dependency markers, and cleaned transient `Dependencies` installer folders after dependency reconciliation.
69. Reorganized installed Agent logs into `Logs/Agent`, `Logs/WireGuard`, and `Logs/UltraVNC` category folders.
70. Ported Windows display topology collection into the Go VNC role and included display topology plus virtual bounds in VNC ensure, credential, and role-health payloads.

## In Progress

1. Validate Engine onboarding/update artifact contracts against a deployed Engine.

## Pending

1. Rework tray UI/status as optional Go/native helper or external UI.
2. Add fake Engine HTTP/Socket.IO harness coverage for live `connect_agent`, `quick_job_run`, `file_management_request`, `process_management_request`, `service_control_action`, `software_inventory_refresh_request`, `vpn_tunnel_start`, Remote Shell TCP bridge behavior, and VNC credential/start lifecycle behavior.
3. Add broader Engine contract tests for onboarding/update artifact path changes.

## Release-Blocking Technical Debt

1. Windows CURRENTUSER direct session quick-job execution and same-binary helper sentinels are implemented, but tray UI/status remains pending.
2. Linux CURRENTUSER is unsupported by design in first PR and must report explicit unsupported status.
3. Manual Windows acceptance has validated fresh enrollment, SYSTEM script execution, and CURRENTUSER PowerShell/Batch execution; update check validation and final config access-control decision remain open.
4. Manual Linux acceptance has validated service install, fresh enrollment, root SYSTEM Bash execution, and root-owned `0600` config; Linux release-channel update behavior still needs real-host acceptance.
5. Windows `config.json` ACL hardening is deferred because current install ACL changes blocked administrator repair/uninstall workflows during real-host testing.
6. Software Management has Go unit coverage, Engine contract compatibility, and real-host Windows uninstall/icon validation; Linux uninstall acceptance remains opportunistic because package removal is operator-risky.
