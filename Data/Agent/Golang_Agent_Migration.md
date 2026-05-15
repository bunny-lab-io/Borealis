# Rewrite Borealis Agent In Go

Back to Docs Index: ../../Docs/index.md

## Decisions

1. Agent source now lives under `Data/Agent`; legacy Python agent source moved to `Data/Agent_Old`.
2. Fresh install runtime is one compiled Go binary named `Agent.exe`.
3. Windows `Agent.exe` owns bootstrap, deployment, repair, update checks, service task registration, and runtime execution.
4. Linux `Agent` owns runtime execution and systemd service install/uninstall; Linux bootstrap parity is tracked as pending.
5. Installed runtime uses one `config.json` beside `Agent.exe`.
6. `config.json` stores keys, tokens, trust material, enrollment code, runtime flags, and agent identity.
7. `config.json` protection uses filesystem permissions only: Windows ACL hardening is deferred and Windows files inherit from `C:\` for now; Linux remains root-owned `0600` with parent `0700`.
8. SQLite is not used in v1 because configuration is small, single-writer, and not query-heavy.
9. No installed Agent runtime path may depend on Python.
10. Non-migrated roles stay disabled or degraded in Go status payloads; no Python fallback.

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
9. Added heartbeat/status payloads with Go runtime capability markers.
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

## In Progress

1. Validate Windows bootstrap behavior on a real Windows host.
2. Validate Linux systemd service install on a real Linux target.
3. Validate Engine onboarding/update artifact contracts against a deployed Engine.
4. Finish Windows CURRENTUSER helper broker in Go.

## Pending

1. Implement Windows CURRENTUSER helper broker in Go using same binary helper mode.
2. Implement device audit inventory role in Go.
3. Implement file management RPC and transfer endpoints in Go.
4. Implement process management role in Go.
5. Implement service management role in Go.
6. Implement software inventory/actions role in Go.
7. Implement WireGuard role in Go.
8. Implement remote shell over WireGuard in Go.
9. Implement VNC lifecycle and credential broker in Go.
10. Implement full release-channel update format and Engine artifact packaging for Go binaries.
11. Implement Linux `Agent` deployment/bootstrap parity.
12. Decide whether macros/node screenshot should be retired or ported.
13. Rework tray UI/status as optional Go/native helper or external UI.
14. Add fake Engine HTTP/Socket.IO harness coverage for live `connect_agent` and `quick_job_run`.
15. Add Engine contract tests for onboarding/update artifact path changes.

## Release-Blocking Technical Debt

1. Windows CURRENTUSER is still degraded until Go helper broker supports user-session script execution.
2. Linux CURRENTUSER is unsupported by design in first PR and must report explicit unsupported status.
3. Release-channel updater requires prebuilt `Agent.exe` artifacts; source-zip self-build updates are not supported on installed hosts.
4. Engine release-channel packaging must publish prebuilt Go Windows/Linux artifacts before fleet updates can use fresh Go binaries.
5. Manual Windows acceptance needs fresh enrollment, SYSTEM script execution, CURRENTUSER behavior validation, update check validation, and final config access-control decision.
6. Manual Linux acceptance needs fresh enrollment, root SYSTEM Bash execution, systemd service install, update behavior decision, and non-root config read denial.
7. Windows `config.json` ACL hardening is deferred because current install ACL changes blocked administrator repair/uninstall workflows during real-host testing.
