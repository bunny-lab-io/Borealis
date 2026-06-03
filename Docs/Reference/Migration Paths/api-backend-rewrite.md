# API Backend Rewrite

Current tracker for moving `api-backend` route ownership from Flask/Python to Go. This page keeps current cutover state, Python compatibility dependencies, and next cleanup targets visible without preserving old milestone history.

## Current Runtime

| Field | State |
| --- | --- |
| Branch | `feature/rewrite-api-backend-in-golang` |
| PR | [#232](https://github.com/bunny-lab-io/Borealis/pull/232) |
| Active milestone | `M17: Go API Domain Porting` |
| Last updated | 2026-06-03 |
| Current architecture | Go binary owns public loopback `127.0.0.1:5000`; Python compatibility backend remains supervised on `127.0.0.1:5001`; unported routes proxy to Python. |
| Latest verified commit | `5385c90a Port bootstrap login MFA flow to Go`; current working slice removes Python metadata/filter/view, site/admin approval, remote-op session broker, Agent enrollment/token/update/script/hash/repo, auth/profile/user/RBAC, server-admin, directory-read, credential-read, server log-management, public notification, public bootstrap/Aegis duplicate route fallbacks, and local user create/password-reset fallbacks. |
| Rewrite posture | Clean cutover by domain is preferred. After a domain is fully Go-owned and smoke-tested, delete matching Python route code instead of leaving stale fallback surface. |

## Status Legend

| Status | Meaning |
| --- | --- |
| `Cutover` | Public route behavior is Go-owned. Old Python route code is obsolete once duplicate-route audit and smoke pass. |
| `Hybrid` | Go owns part of domain, but Python still owns runtime state, challenge/session state, broadcast fanout, streaming state, or related mutation routes. |
| `Python` | Domain still runs through Python compatibility backend. |
| `Removed` | Route surface intentionally deleted from both WebUI and API. |

## Rewrite Inventory

| Domain | Status | Go-owned now | Python dependency | Cutover cleanup |
| --- | --- | --- | --- | --- |
| Gateway, health, Go status | `Cutover` | Public listener, health/status responses, Python process supervision, reverse proxy for unported routes. | Python still must stay alive while any `Hybrid` or `Python` domain remains. | Keep proxy until inventory has no Python-owned rows. |
| Auth session, profile, logout | `Cutover` | Borealis token verification, `/api/auth/me`, logout, session invalidation checks. | Passkey and directory flows still rely on Python session/challenge state. | Duplicate Python profile/logout route handlers removed in current cleanup slice. |
| Bootstrap, Aegis unlock, local login, local MFA | `Hybrid` | Bootstrap state, Aegis bootstrap setup/unlock, local password login, signed pending-MFA token, local TOTP verification, public Aegis status. | Admin bootstrap setup/recovery, directory login, WebAuthn challenge state, Aegis rotate/force-reset, public setup/unlock/rotate/force-reset, and internal Aegis bridge still live in Python. | Python public bootstrap state/setup/unlock and public Aegis status duplicates removed in current cleanup slice. Port remaining Aegis lifecycle/admin bootstrap routes, then remove retained Python handlers and internal bridge. |
| Passkeys | `Hybrid` | Current-user passkey list, label update, delete. | WebAuthn registration/authentication ceremonies and challenge storage remain Python-owned. | Duplicate Python passkey list/update/delete handlers removed in current cleanup slice. Move WebAuthn challenge/session storage to Go before deleting ceremony routes. |
| Users, RBAC, MFA admin, site assignments | `Cutover` | User list/create/delete, role update, admin password reset, current-user password reset, MFA enable/disable/reset, current-user MFA reset, assignment selection/assign. | Directory cached-user enable/disable remains part of Directory Services. | Python `access_management/users.py` deleted in current cleanup slice. |
| Directory services | `Hybrid` | Provider and directory-site reads. | Provider/site mutations plus directory login remain Python-owned. | Duplicate Python provider/site read handlers removed in current cleanup slice. Port directory manager mutations and directory login pending-state handling. |
| Credentials and GitHub token | `Hybrid` | Credential reads and detail reads. | Credential create/update/delete and GitHub token read/write still need Python secret mutation path. | Duplicate Python credential list/detail handlers removed in current cleanup slice. Port Aegis-backed secret mutation in Go, then delete Python credential/token routes. |
| Server admin, logs, settings | `Hybrid` | Server time read, overview, runtime settings reads/mutations, worker list/recreate, logs, log retention/deletion, and container service actions. | Agent release-channel mutation/refresh, WireGuard recovery, and non-container systemd restart fallback remain Python-owned. | Duplicate Python read/settings/container-action routes and isolated Python log-management module removed in current cleanup slice. Port remaining server mutations before deleting `server/info.py`. |
| Timezone management | `Removed` | No WebUI timezone changer and no Go route for timezone POST/PUT. | None. Operators manage host timezone from host CLI. | Keep API absent. Do not reintroduce WebUI timezone mutation. |
| Agent enrollment, token, update, script, metadata, hash, repo manifest | `Cutover` | Enrollment request/poll, token refresh, script request, Agent metadata read, software-management override read, update manifest/download, hash update/list, repo hash read. | Agent heartbeat/status and live callback domains remain separate Python rows. | Python token/enrollment packages, duplicate Agent read/update/script handlers, and hash/repo handlers removed in current cleanup slice. |
| Agent heartbeat, status, VPN/VNC callbacks | `Python` | None for heartbeat/status callback domain. | Heartbeat, status, VPN ensure/ready, and VNC ensure callbacks remain Python-owned. | Port carefully. Preserve status fanout, role health normalization, update reconciliation, and short DB connection lifecycle. |
| Device inventory, search, details, description, release channel | `Hybrid` | Device list/search/detail, per-device detail, description update, Agent release-channel assignment, agents list. | High-risk device purge remains Python-owned. | Port purge separately with explicit destructive-path tests. |
| Metadata fields, device metadata, list views, filters | `Cutover` | Metadata field reads/updates, per-device metadata, saved device-list views, filter CRUD/search/preview/usage/archive/clone. | No required Python runtime dependency known. | Python API route registration and duplicate route modules removed in current cleanup slice; keep shared metadata/filter helpers used by other Python domains. |
| Sites, enrollment codes, device approvals | `Cutover` | Site list/create/delete/assign/rename/auto-approval, site device map, enrollment-code list/deprecated responses/delete, device approval approve/deny. | No required Python runtime dependency known. | Python API route registration, mixed site handlers, and isolated admin approval route module removed in current cleanup slice. |
| Software, process, service, Agent maintenance | `Hybrid` | Software audit, icon, refresh, override/block actions, bulk software actions, process list/terminate, service list/action, bulk Agent maintenance queueing, single-device Agent update dispatch. | Software uninstall job dispatch still depends on Python scheduled-job creation/broadcast behavior. | Port uninstall dispatch after scheduled-job create/update parity exists. |
| Remote-operation session broker | `Cutover` | Signed scoped operation-token broker for shell, desktop, and file capabilities. | No required Python runtime dependency known. | Python route registration and isolated broker module removed in current cleanup slice. |
| Remote shell | `Python` | No native Go shell establish/disconnect routes. | Shell establish/disconnect and shell socket runtime remain Python-owned. | Port or retire shell establish/disconnect API surface after current UI route path is audited. |
| Remote file management | `Hybrid` | Browse roots/children, upload conflict check, text read/write, mkdir, rename, move, delete, paste. | Upload/download transfer streaming, transfer status/cancel/content, and Agent transfer callbacks remain Python-owned. | Port transfer state and streaming helpers before deleting Python file-transfer routes. |
| Remote desktop, tunnel, VPN runtime | `Hybrid` | VNC viewer capability read. | VNC establish/session/disconnect/handoff, tunnel connect/status/active, and VPN session tracking remain Python-owned. | Needs Go runtime managers or explicit retained-Python exception. |
| Assemblies, scripts, quick execution | `Python` | None for assemblies/editor/cache/quick execution domain. | Assemblies cache, editor behavior, dirty queue, and quick run routes remain Python-owned. | Port cache/editor/dirty queue as one domain to avoid split-brain state. |
| Workflows | `Hybrid` | Workflow run reads, node reads, webhook CRUD. | Workflow execution, editor access, run resolution, and webhook trigger execution remain Python-owned. | Port execution manager and resolution/broadcast path before deleting Python workflow routes. |
| Watchdogs | `Hybrid` | Watchdog metadata, list/detail, incident list/count reads. | Watchdog mutations, evaluation, incident acknowledgement, and remediation dispatch remain Python-owned. | Port mutation/evaluation path with incident-state tests. |
| Scheduled and onboarding jobs | `Hybrid` | Job list/detail, delete, run history clear, toggle, device targets, onboarding target read, internal public-base-url and host-service-event helpers. | Job create/update/rerun/redeploy plus internal credential, VPN, and workflow scheduler helpers remain Python-owned. | Port scheduler mutations and helper flows before deleting Python job routes. |
| Notifications | `Hybrid` | Public authenticated notification payload validation/dispatch route. | Python still owns Socket.IO operator broadcast fanout through internal broadcast endpoint. | Python public notification duplicate removed in current cleanup slice. Build Go realtime/operator-toast channel, then remove Python internal notification broadcast. |

## Cleanup Queue

| Priority | Target | Why |
| --- | --- | --- |
| Done | Metadata fields, device metadata, list views, filters | Python route registration removed, isolated Python route files deleted, mixed device-list-view handlers removed, Go handlers no longer fallback for this domain. |
| Done | Sites, enrollment codes, device approvals | Python route registration removed, isolated Python admin approval module deleted, mixed site handlers removed, Go handlers no longer fallback for this domain. |
| Done | Remote-operation session broker | Python route registration removed and isolated Python broker module deleted. |
| Done | Agent enrollment/token/update/script/hash/repo routes | Python token/enrollment packages deleted, duplicate Agent read/update/script handlers removed, mixed hash/repo handlers removed. |
| Done | Auth profile/logout and simple user/RBAC duplicates | Python profile/logout, passkey list/update/delete, user list/delete/role, MFA admin/reset, and site-assignment route handlers removed; ceremony/password-reset flows retained. |
| Done | Server admin duplicate reads and settings | Python server time, overview, worker read/recreate, ansible/site-worker settings, Agent release-channel GET, and container service-action route handlers removed; Go fallbacks tightened for fully cutover paths. |
| Done | Directory and credential read duplicates | Python directory provider/site GET handlers and credential list/detail GET handlers removed; mutation fallbacks retained. |
| Done | Low-risk duplicate/public cleanup | Python `authentication.py`, `server/log_management.py`, public notification dispatch route, public bootstrap state/setup/unlock duplicates, and public Aegis status duplicate removed; internal bridges and retained Python lifecycle routes left intact. |
| Done | User create and password reset | Go owns local user create, admin password reset, and current-user password reset; Python `access_management/users.py` deleted. |
| 1 | Agent release-channel mutation/refresh and WireGuard recovery | Port remaining server-admin Python mutations, then delete retained `server/info.py` route surface. |
| 2 | Timezone API | Already removed. Keep removed. |

## Hard Python Dependencies

| Dependency | Blocks |
| --- | --- |
| Aegis runtime/session bridge | Full auth/bootstrap/Aegis cutover, credential secret mutations, GitHub token mutation. |
| WebAuthn challenge state | Passkey registration and passkey login cutover. |
| Directory login pending state | Directory authentication cutover. |
| Operator realtime broadcast | Notification cutover and some job/workflow/watchdog UX parity. |
| File transfer streaming state | Upload/download transfer route deletion. |
| Remote desktop/tunnel runtime managers | VNC, VPN, tunnel callback cutover. |
| Assemblies cache and dirty queue | Assemblies/editor/quick-run cutover. |
| Scheduler mutation engine | Scheduled-job create/update/rerun/redeploy, software uninstall dispatch, workflow-backed scheduled helpers. |
| Agent heartbeat/status fanout | Agent heartbeat/status callback cutover. |

## Validation Snapshot

| Slice | Last reported validation |
| --- | --- |
| Agent maintenance/update Go port | `go test ./...`, `build-api-backend.sh`, `sudo bash Engine.sh deploy prod`, safe live route smoke. |
| Notification Go dispatch | Go tests, build, deploy, safe notification smoke through Python broadcast bridge. |
| Bootstrap/Aegis/local login/MFA Go port | Go tests, build, deploy, live `/health`, `/api/bootstrap/state`, `/api/aegis/status`, local login/MFA-safe checks. |
| Metadata/filter/view Python route cleanup | `go test ./...`, `build-api-backend.sh`, Python syntax compile for touched Flask modules, `sudo bash Engine.sh deploy prod`, public Go 401 smoke, internal Python 404 route-removal smoke. |
| Site/admin/remote-op broker Python route cleanup | `go test ./...`, `build-api-backend.sh`, Python syntax compile for touched Flask modules, `sudo bash Engine.sh deploy prod`, public Go 401 smoke, internal Python route-file absence and route-removal smoke. |
| Agent enrollment/token/update/script/hash/repo Python route cleanup | Python syntax compile for touched Flask modules, no Python duplicate route residue, `go test ./...`, `build-api-backend.sh`, `sudo bash Engine.sh deploy prod`, public Go route smoke, exact POST validation smoke, internal Python 404 route-removal smoke. |
| Auth/profile/user/RBAC Python route cleanup | Python syntax compile for touched Flask modules, no Python duplicate route residue, `go test ./...`, `build-api-backend.sh`, `sudo bash Engine.sh deploy prod`, public Go smoke, runtime Python source scan, internal Python route-removal smoke. |
| Server-admin Python route cleanup | Python syntax compile for touched Flask module, no Python duplicate route residue, `go test ./...`, `build-api-backend.sh`, `sudo bash Engine.sh deploy prod`, public Go smoke, retained Python mutation smoke. |
| Directory/credential read cleanup | Python syntax compile for touched Flask modules, no Python duplicate route residue, `go test ./...`, `build-api-backend.sh`, `sudo bash Engine.sh deploy prod`, public Go smoke, retained Python mutation smoke. |
| Low-risk duplicate/public cleanup | Python syntax compile for touched Flask modules, no Python duplicate route residue, `go test ./...`, `build-api-backend.sh`, `sudo bash Engine.sh deploy prod`, public Go smoke, internal Python route-removal smoke, operator WebUI smoke for login/logout, Server Info, Log Management, device inventory, filters/views, sites/approvals, Access Management, and credential reads, `git diff --check`. |
| User create/password reset Go cutover | Go unit tests for create/admin reset/current-user reset dispatch, Python syntax compile for retained auth module, no Python user route residue, `go test ./...`, `build-api-backend.sh`, `sudo bash Engine.sh deploy prod`, safe route smoke, `git diff --check`. |
| Tracker cleanup | Run `git diff --check` after doc edit. |

## Next Work

1. Pick one `Cutover` row, audit duplicate Python routes, delete obsolete Python handlers, run focused route tests, then smoke WebUI path.
2. Prefer remaining server-admin mutations next. Metadata/filter/view, site/admin, remote-op broker, Agent duplicate cleanup, simple auth/user/RBAC cleanup, server-admin duplicate cleanup, directory/credential read cleanup, low-risk duplicate/public cleanup, and user create/password-reset cleanup already removed Python route surface.
3. Continue Aegis/bootstrap/login/passkey work only as full domain cutover: port missing Go state first, then delete Python ceremony/lifecycle routes.
4. Keep Python process until every `Hybrid` and `Python` row is either ported or explicitly accepted as retained Python.

??? example "Detailed Codex Breakdown"

    ### Source map

    - Go gateway and native handlers: `Data/Engine/Containers/api-backend/cmd/api-backend/`.
    - Python compatibility routes: `Data/Engine/Containers/api-backend/data/services/`.
    - Public Go listener: `127.0.0.1:5000`.
    - Compatibility backend listener: `127.0.0.1:5001`.
    - Route ownership rule: Go registrations in `main.go` win first; unregistered route paths proxy to Python.

    ### Current cutover rule

    Porting a handler to Go is not enough for final cutover. Final cutover requires:

    1. Public route registered in Go.
    2. Behavior parity verified by focused test or live smoke.
    3. No required Python runtime manager, challenge state, stream state, or broadcast path.
    4. Duplicate Python route deleted or intentionally retained with reason.
    5. `git diff --check` passes.

    ### Removal guidance

    Do not delete Python process supervision while any `Hybrid` or `Python` inventory row remains. Delete Python route groups surgically after each domain clears cutover checks.
