# Rewrite Borealis Agent In Go

Back to Docs Index: ../../Docs/index.md

## Decisions

1. Agent source now lives under `Data/Agent`; legacy Python agent source moved to `Data/Agent_Old`.
2. Fresh install runtime is one compiled Go binary named `Agent.exe`.
3. Windows `Agent.exe` owns bootstrap, deployment, repair, update checks, service task registration, and runtime execution.
4. Linux `Agent` owns runtime execution and systemd service install/uninstall; Linux bootstrap parity is tracked as pending.
5. Installed runtime uses one `config.json` beside `Agent.exe`.
6. `config.json` stores keys, tokens, trust material, enrollment code, server URL, and agent identity only.
7. `config.json` protection uses filesystem permissions only: Windows ACL hardening is deferred and Windows files inherit from `C:\` for now; Linux remains root-owned `0600` with parent `0700`.
8. SQLite is not used in v1 because configuration is small, single-writer, and not query-heavy.
9. No installed Agent runtime path may depend on Python.
10. Non-migrated roles stay disabled or degraded in Go status payloads; no Python fallback.
11. First PR assumes clean installs only; no config cleanup or backward-compatible migration logic is carried for removed fields.

## Numbered Feature Backlog

1. Core binary, CLI flags, logging, bootstrap entrypoint, service install/uninstall.
2. `config.json` schema, atomic load/save, Windows inherited permissions, Linux ownership/permissions.
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
18. Legacy macros/node screenshot, decide retire vs port.
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
45. Added Go WireGuard tunnel role for persistent `/api/agent/vpn/ensure` polling, `vpn_tunnel_start` handling, signed orchestration token validation, side-by-side `wireguard.conf`, Windows tunnel-service apply, Linux `wg-quick` apply, firewall readiness, role health, and `/api/agent/vpn/ready` reporting.
46. Added startup cleanup of the install-root `Temp` directory so onboarding payload, state, and stdout leftovers do not persist after Agent runtime starts.

## In Progress

1. Validate Engine onboarding/update artifact contracts against a deployed Engine.
2. Validate Go WireGuard tunnel lifecycle on real Windows/Linux targets after redeploy.

## Pending

1. Implement full Windows CURRENTUSER helper/tray broker in Go using same binary helper mode for richer session inventory and long-lived desktop helpers.
2. Implement remote shell over WireGuard in Go.
3. Implement VNC lifecycle and credential broker in Go.
4. Implement full release-channel update format and Engine artifact packaging for Go binaries.
5. Implement Linux `Agent` deployment/bootstrap parity.
6. Decide whether macros/node screenshot should be retired or ported.
7. Rework tray UI/status as optional Go/native helper or external UI.
8. Add fake Engine HTTP/Socket.IO harness coverage for live `connect_agent`, `quick_job_run`, `file_management_request`, `process_management_request`, `service_control_action`, `software_inventory_refresh_request`, and `vpn_tunnel_start`.
9. Add Engine contract tests for onboarding/update artifact path changes.

## Release-Blocking Technical Debt

1. Windows CURRENTUSER direct session quick-job execution is implemented, but full helper/tray broker parity is still pending.
2. Linux CURRENTUSER is unsupported by design in first PR and must report explicit unsupported status.
3. Release-channel updater requires prebuilt `Agent.exe` artifacts; source-zip self-build updates are not supported on installed hosts.
4. Engine release-channel packaging must publish prebuilt Go Windows/Linux artifacts before fleet updates can use fresh Go binaries.
5. Manual Windows acceptance has validated fresh enrollment, SYSTEM script execution, and CURRENTUSER PowerShell/Batch execution; update check validation and final config access-control decision remain open.
6. Manual Linux acceptance has validated service install, fresh enrollment, root SYSTEM Bash execution, and root-owned `0600` config; update behavior decision remains open.
7. Windows `config.json` ACL hardening is deferred because current install ACL changes blocked administrator repair/uninstall workflows during real-host testing.
8. Software Management has Go unit coverage, Engine contract compatibility, and real-host Windows uninstall/icon validation; Linux uninstall acceptance remains opportunistic because package removal is operator-risky.
