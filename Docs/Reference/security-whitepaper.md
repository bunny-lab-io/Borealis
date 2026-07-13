# Security Whitepaper

Borealis is built around layered trust. Agents do not become trusted because they can reach the Engine, operators do not become trusted because they know a password, and remote operations do not become trusted because a tunnel exists. Each layer has its own identity check, authorization gate, containment path, and audit trail.

This page starts with a plain-language security posture summary for evaluators, operators, and business stakeholders. Deeper implementation notes appear later on the page, and Codex-only source maps, endpoint lists, validation paths, and long workflows live in the final collapsed breakdown.

## Executive Summary

- Borealis is self-hosted: operators own the Engine host, network exposure, DNS, certificates, backups, and account lifecycle.
- Agents connect outbound to the Engine over CA-validated HTTPS and Borealis-managed WireGuard. Externally Accessible deployments use public CA validation; Internal-Only deployments use a Borealis local CA plus normal hostname validation. Internal-Only agents may use a persisted Engine IP fallback as a route hint, but the Engine FQDN remains the TLS identity. Endpoint inbound exposure is not required for normal remote operations.
- Device trust starts with operator-approved enrollment, device-generated Ed25519 identity, short-lived access tokens, hashed refresh tokens, and device status checks.
- Operator trust is protected by Aegis Cipher, MFA by default, WebAuthn passkeys, RBAC, site scoping, and strict session invalidation.
- Script and automation delivery is signed by the Engine. Agents verify signatures before execution.
- WireGuard is treated as encrypted transport, not blanket authorization. Borealis adds peer isolation, route validation, firewall rules, containment gates, and remote-operation checks above it.
- Quarantine and revocation give operators a containment path when a device becomes suspicious or should no longer receive work.

## Security Domains

### Token Trust and Device Identity

- Each enrolled agent has a unique Ed25519 identity key and proves key possession during enrollment.
- Access tokens are short-lived, refresh tokens are hashed server-side, and token-version bumps invalidate stale trust.
- Device containment states prevent quarantined, revoked, or decommissioned endpoints from receiving remote operations.

### WireGuard Security

- Agents keep outbound-only tunnels; Engine rejects broad or duplicate peer routes and locks each peer to one private `/32`.
- Engine always installs default-deny WireGuard firewall chains for invalid traffic, lateral agent traffic, internal-network forwarding, and new agent-initiated host access.
- Tunnel control runs without full privileged mode, and its socket accepts only expected WireGuard, route, and firewall operations.

### Operator Access and Sessions

- Aegis Cipher must be set up or unlocked before normal login, passkey, or operator API flows are available.
- MFA is enabled by default, WebAuthn passkeys are supported, and operator sessions are revalidated against user state.
- RBAC and site scoping limit which devices, jobs, credentials, and administrative actions an operator can reach.

### Script and Automation Safety

- Engine signs script payloads, and Agent enforces trusted delivery before execution.
- Jobs, quick runs, workflows, watchdog remediation, and Ansible target materialization respect site scope and device containment.
- Reusable machine credentials stay protected by Aegis Cipher and are never intended to appear in plaintext logs.

### Runtime and Container Boundaries

- The public edge is Traefik; Engine APIs and database services bind to loopback on the Engine host.
- Docker socket write access is isolated to `site-worker-orchestrator`; API reads container state through a read-only Docker proxy, and `job-scheduler` uses an authenticated Unix socket for lifecycle requests. Other Engine services do not join the Docker group or mount the socket.
- Most Engine containers run as the non-root `borealis-engine` runtime owner with read-only root filesystems, dropped capabilities, `no-new-privileges`, tmpfs `/tmp`, and profile-scaled resource limits. PostgreSQL uses the official non-root PostgreSQL UID to preserve database state ownership.
- WireGuard runtime uses explicit network capabilities, `/dev/net/tun`, and `no-new-privileges` instead of full privileged container mode.

### Monitoring, Audit, and Recovery

- Enrollment approvals, rate-limit hits, authentication anomalies, script-signature failures, and agent activity are logged.
- Backup/Restore exports are encrypted with the Aegis-derived key and require the same Aegis Cipher for import.
- Force reset is available as a disaster-recovery path, but it destroys protected operator auth secrets and reusable credential secrets.

## Trust Boundaries

### Engine Host

The Engine host is the security root for Borealis. Operators should protect host SSH access, Docker permissions, DNS, firewall exposure, disk backups, and filesystem permissions. Borealis reduces application-level risk, but it cannot protect secrets if the Engine host itself is fully compromised.

### Public Edge

Traefik owns HTTP/HTTPS, certificate state, UI/API routing, Socket.IO routing, and VNC WebSocket routing. Externally Accessible browser and agent trust use normal public CA and hostname validation. Internal-Only trust uses a Borealis local CA distributed to agents through install commands and to browsers through operator-managed trust stores. Agent IP fallback changes only the TCP dial address after normal FQDN connection fails; HTTP host, TLS SNI, and certificate hostname validation still use the Engine FQDN. Internal Engine APIs stay behind the edge on loopback.

### Device Enrollment

An agent must present an enrollment or installer code, generate its own identity key, prove possession of that key, and wait for operator approval. Automatic local-network onboarding installs the agent, but it does not bypass approval.

### Remote Operations

Remote shell, remote desktop, file actions, process actions, service actions, scheduled jobs, quick jobs, workflows, and site-worker operations must pass device status, token trust, site scope, and operator authorization checks. WireGuard reachability alone is not enough.

### Automation Content

Scripts and assemblies are signed before delivery. Agents treat payloads as untrusted until signature verification succeeds. This protects against tampered payloads between Engine storage and endpoint execution.

## Current Security Notes

- Remote shell and remediation can run with high endpoint privilege. Use RBAC, site scoping, MFA, and containment workflows carefully.
- Engine host compromise remains high impact because Engine owns API state, Aegis-protected secrets, WireGuard keys, and signing keys.
- Directory authentication depends on secure LDAP/LDAPS configuration. Use LDAPS with trusted CA or reviewed pinned certificate where possible.
- Packet-level WireGuard validation still requires a deployed test Engine with at least two enrolled agents to prove runtime firewall behavior.
- macOS is not a productized agent target in current Borealis support matrix.

## Technical Security Model

### Engine Edge and Bootstrap

- Borealis renders Traefik runtime state under `Engine/Services/traefik-edge/state/` and `Engine/Services/traefik-edge/config/`. Let's Encrypt state is used for Externally Accessible deployments; Borealis local CA and leaf certificate state is used for Internal-Only deployments.
- Internal engine-only material such as WireGuard keys, code-signing keys, Aegis state, and auth secrets stays under Engine service runtime paths.
- First deployment follows `Set Aegis Cipher -> Create first administrator -> Complete MFA -> Enter normal Borealis`.
- Later Engine restarts follow `Unlock Aegis Cipher -> Enter normal Borealis login or passkey flow`.
- Normal authenticated endpoints are unavailable until Aegis setup or unlock is complete and the Engine reaches the `login_required` bootstrap phase.

### Operator Authentication

- Operator accounts support password plus TOTP MFA.
- WebAuthn passkeys are supported for direct browser sign-in once bootstrap is complete.
- Aegis protects stored password hashes, TOTP secrets, passkey cryptographic material, directory bind passwords, reusable credentials, and GitHub API token storage.
- Active sessions are revalidated against the operator row on authenticated requests. Deleted users, disabled directory cache entries, and deprovisioned directory users stop passing authorization checks without waiting for token expiry.
- Only an administrator can explicitly disable MFA for an operator account.

### Enrollment and Device Identity

- Device enrollment is gated by enrollment and installer codes with configurable expiration and usage limits.
- Enrollment requests are rate-limited by IP and identity fingerprint.
- Agents generate Ed25519 key pairs locally and prove possession with signed nonces.
- Engine records GUID, key fingerprint, approval state, token version, and audit metadata.
- Hostname and fingerprint conflicts are surfaced for operator decision instead of silently replacing trust.

### Token and DPoP Handling

- Access tokens are EdDSA JWTs with a short lifetime, defaulting to about 15 minutes.
- Refresh tokens use a longer sliding window, are used only for refresh, and are stored as hashes in PostgreSQL.
- The agent stores token material in protected `agent.json` with device identity and signing trust.
- DPoP proof validation can bind refresh-token use to a key thumbprint and reject replay attempts.
- Device status, fingerprint match, token version, refresh-token expiry, and revocation state are checked before new access tokens are issued.

### Agent Runtime

- Supported Windows agent traffic is owned by the SYSTEM runtime.
- Per-session helpers do not enroll, do not store Engine tokens, and communicate with the local SYSTEM broker over local IPC.
- Agent API and Socket.IO calls flow through the Go auth client, which refreshes tokens before retrying authenticated calls.
- Internal-Only `server_ip_fallback` is a route hint, not a trust anchor. Agents still require the configured Engine FQDN and trusted CA validation for HTTPS. Linux WireGuard setup may rewrite the local endpoint to the fallback IP after FQDN DNS failure; WireGuard server public key validation still authenticates the tunnel peer.
- Script payloads are rejected when signature verification fails.
- Agent logs bootstrap, enrollment, token refresh, role health, and signature events under `Agent/Logs`.

### WireGuard Agent to Engine Tunnels

- Borealis moved remote transport from a bespoke reverse tunnel stack to WireGuard for encrypted UDP transport and resilient reconnect behavior.
- Agents ensure the tunnel at boot, keep it outbound-only, and reuse one live VPN tunnel per agent across operators.
- Engine issues short-lived, Ed25519-signed tunnel material that the agent verifies before bringing the tunnel up.
- Each agent gets one host-only `/32`. Engine peer mutation rejects duplicate `/32` assignments, broad prefixes, Engine-address reuse, and duplicate peer public keys.
- Agent runtime also rejects broad tunnel routes in received session material.
- Engine listener reconcile always installs `BOREALIS-WG-INPUT` and `BOREALIS-WG-FWD` iptables chains. No environment flag disables those chains.
- Firewall chains drop invalid packets, allow established/related return traffic, drop new agent-originated host ingress over the tunnel, drop agent-to-agent forwarding, and drop agent-originated forwarding toward other networks.
- Agent-local firewall rules allow only Engine `/32` access to explicitly issued tunnel ports. Defaults are `47002`, `5900`, and `22`; additional ports are added only when a session or scheduled transport requires them.
- The WireGuard control socket accepts only expected `wg`, `wg-quick`, `ip`, and `iptables` operations for Borealis tunnel setup.

### Containment and Revocation

- Quarantine and revocation routes bump the device token version, mark device status, revoke active VNC collaboration state, and remove active WireGuard peers.
- `quarantined` devices can keep basic heartbeat and token-refresh paths, but script polling returns a quarantined idle response.
- VPN, VNC, remote shell, remote desktop, site-worker remote-op tokens, worker sockets, and scheduled transport preparation are blocked for quarantined devices.
- `revoked` devices also have refresh tokens revoked and cannot refresh trust.
- `decommissioned` devices are treated as non-active for remote-operation admission.

### Code Signing and Automation Delivery

- Script delivery is code-signed with an Ed25519 key stored under `Engine/Services/api-backend/secrets/Certificates/Code-Signing`.
- Agents verify signatures with the pinned server signing key before execution.
- Signature failure stops execution and creates agent-side logs.
- Quick jobs, scheduled jobs, workflows, watchdog remediation, and Engine-side Ansible target materialization must pass target scope and device status checks before dispatch.

### Automatic Local-Network Enrollment

- Sites > Onboard Devices creates scheduler-backed enrollment jobs for local-network Linux and Windows targets.
- Operators provide site, device OS, discovery scope, stored credential, install branch, and schedule.
- Linux enrollment uses SSH. Windows enrollment tries SMB `ADMIN$` plus Remote Service Control Manager, then scheduled task, then WMI/DCOM process creation, then WinRM.
- Borealis writes non-secret onboarding correlation to agent settings so pending approvals can show source context.
- Manual approval remains the trust boundary. Successful remote install means the agent reached the approval queue, not that the device is trusted.

### Directory Authentication

- Directory authentication supports LDAP/LDAPS user-bind providers and Active Directory-compatible LDAP/LDAPS simple bind.
- LDAPS providers can use system trust, uploaded CA PEM, or an operator-reviewed pinned peer certificate downloaded from the LDAP server.
- Provider-scoped host overrides let Borealis connect to a configured IP while keeping FQDN SNI and certificate validation intact.
- Directory users are cached just-in-time in `users`, keep Borealis TOTP MFA, and cannot register Borealis passkeys.

### Logging and Audit

- Engine logs live under `Engine/Services/api-backend/logs`.
- Agent logs live under `Agent/Logs`.
- Enrollment approvals, rate-limit hits, signature failures, refresh-token outcomes, and auth anomalies are logged for incident review.
- Recent wrong-code enrollment attempts are surfaced in the Device Approval Queue.

## Operational Security Checklist

- Keep Engine host patched, backed up, and firewalled.
- Expose only required public ports: HTTP/HTTPS for Traefik and UDP WireGuard for remote operations.
- Use a real FQDN for agents and browsers. Externally Accessible deployments need public DNS and public CA certificates. Internal-Only deployments should use private DNS, Borealis local CA distribution to agents, and browser trust store installation for operators. Agents without private DNS can use the generated IP fallback while preserving FQDN TLS validation.
- Keep Aegis Cipher recoverable by trusted administrators.
- Require MFA for operators and prefer passkeys where possible.
- Use RBAC and site scoping so operators see only devices they own.
- Quarantine devices at first sign of suspicious behavior.
- Validate WireGuard behavior in a disposable test Engine before relying on a new security-sensitive network change.

??? example "Detailed Codex Breakdown"

    ### API endpoints

    - `POST /api/agent/enroll/request` (No Authentication) - start enrollment.
    - `POST /api/agent/enroll/poll` (No Authentication) - finalize enrollment after approval.
    - `POST /api/agent/token/refresh` (Refresh Token) - mint a new access token.
    - `POST /api/devices/<guid>/quarantine` (Admin) - mark a device quarantined, bump token version, and disconnect active VPN/VNC runtime state.
    - `POST /api/devices/<guid>/unquarantine` (Admin) - return a quarantined device to active state and bump token version so stale access tokens are not reused.
    - `POST /api/devices/<guid>/revoke` (Admin) - mark a device revoked, bump token version, revoke refresh tokens, and disconnect active VPN/VNC runtime state.
    - `GET /api/bootstrap/state` (No Authentication) - return the public bootstrap phase (`aegis_setup_required`, `aegis_unlock_required`, `admin_setup_required`, `admin_recovery_required`, `login_required`).
    - `POST /api/bootstrap/aegis/setup` (No Authentication) - configure Aegis before any login UI is available.
    - `POST /api/bootstrap/aegis/unlock` (No Authentication) - unlock Aegis after restart before any login UI is available.
    - `POST /api/bootstrap/admin/setup` (No Authentication, bootstrap only) - create the first administrator after Aegis setup.
    - `POST /api/bootstrap/admin/recover` (No Authentication, bootstrap only) - recover an existing administrator after Aegis force reset.
    - `POST /api/bootstrap/admin/mfa/verify` (No Authentication, bootstrap MFA pending) - finalize first-admin setup or admin recovery and issue the normal operator session.
    - `POST /api/bootstrap/backup/analyze` (No Authentication, bootstrap only) - decrypt and validate encrypted Engine configuration backup JSON before normal login is enabled, then return high-level import counts without changing state.
    - `POST /api/bootstrap/backup/restore` (No Authentication, bootstrap only) - restore an encrypted Engine configuration backup before normal login is enabled.
    - `POST /api/auth/login` (No Authentication, bootstrap phase `login_required` only) - operator login.
    - `POST /api/auth/logout` (Token Authenticated) - operator logout.
    - `POST /api/auth/password/reset` (Token Authenticated) - verify the current operator password and replace it with a new Aegis-protected password hash.
    - `POST /api/auth/mfa/verify` (Token Authenticated, MFA pending, bootstrap phase `login_required` only) - verify MFA.
    - `POST /api/auth/mfa/reset` (Token Authenticated) - clear the current operator's authenticator-app secret so MFA setup is required on the next password login. Passkeys remain available for direct sign-in.
    - `POST /api/auth/passkeys/register/options` (Token Authenticated) - start a passkey registration ceremony.
    - `POST /api/auth/passkeys/register/verify` (Token Authenticated) - verify a passkey registration response and store the credential.
    - `POST /api/auth/passkeys/authenticate/options` (No Authentication, bootstrap phase `login_required` only) - start a passkey sign-in ceremony.
    - `POST /api/auth/passkeys/authenticate/verify` (No Authentication, bootstrap phase `login_required` only) - verify a passkey sign-in response and complete login.
    - `GET /api/auth/passkeys` (Token Authenticated) - list the current operator's passkeys.
    - `PATCH /api/auth/passkeys/<int:passkey_id>` (Token Authenticated) - rename one of the current operator's passkeys.
    - `DELETE /api/auth/passkeys/<int:passkey_id>` (Token Authenticated) - remove one of the current operator's passkeys.
    - `GET /api/auth/me` (Token Authenticated) - current operator profile, including MFA-enabled state, auth source, and passkey count.
    - `GET /api/directory/providers` (Admin) - list directory providers.
    - `POST /api/directory/providers` (Admin) - create a directory provider.
    - `PATCH /api/directory/providers/<int:provider_id>` (Admin) - update or enable/disable a directory provider.
    - `DELETE /api/directory/providers/<int:provider_id>` (Admin) - delete an unused directory provider.
    - `POST /api/directory/providers/<int:provider_id>/test` (Admin) - test provider connectivity.
    - `POST /api/directory/providers/<int:provider_id>/sync` (Admin) - sync cached directory users.
    - `POST /api/users/<username>/directory-cache` (Admin) - disable or re-enable a cached directory user.
    - `GET /api/admin/enrollment-codes` (Admin) - list static site enrollment codes.
    - `POST /api/admin/enrollment-codes` (Admin) - deprecated (returns 410; use site APIs).
    - `DELETE /api/admin/enrollment-codes/<code_id>` (Admin) - deprecated (returns 410; use site APIs).
    - `POST /api/agent/vpn/ensure` (Device Authenticated) - create or refresh WireGuard tunnel material when device is active.
    - `POST /api/agent/vpn/ready` (Device Authenticated) - report tunnel readiness when device is active.
    - `POST /api/agent/vnc/ensure` (Device Authenticated) - prepare VNC tunnel material when device is active.
    - `POST /api/tunnel/connect` (Operator Authenticated) - request remote tunnel connectivity for an active device.
    - `GET /api/tunnel/status` (Operator Authenticated) - inspect tunnel status for an active device.
    - `GET /api/tunnel/active` (Operator Authenticated) - list active tunnel state.
    - `POST /api/remote-ops/session` (Operator Authenticated) - request remote operation session token after device status and scope checks.

    ### Related documentation

    - [Agent Runtime](../Reference/Core%20Runtimes/agent-runtime.md)
    - [Engine Runtime](../Reference/Core%20Runtimes/engine-runtime.md)
    - [Docker Stack Breakdown](../Reference/Core%20Runtimes/Stack_Breakdown.md)
    - [Device Approvals](../Using%20the%20Platform/device-approvals.md)
    - [API Reference](../Reference/Data%20and%20Schema/api-reference.md)
    - [Engine Deployment](../Engine/deploying-the-engine.md)

    ### Source map

    - Engine auth bootstrap: `Data/Engine/Containers/api-backend/cmd/api-backend/bootstrap_*.go`.
    - Operator auth and passkeys: `Data/Engine/Containers/api-backend/cmd/api-backend/auth_*.go`.
    - Directory providers: `Data/Engine/Containers/api-backend/cmd/api-backend/directory_*.go`.
    - Device enrollment and approvals: `Data/Engine/Containers/api-backend/cmd/api-backend/device_enrollment.go` and `Data/Engine/Containers/api-backend/cmd/api-backend/device_approvals.go`.
    - Device containment routes: `Data/Engine/Containers/api-backend/cmd/api-backend/device_security.go`.
    - Engine tunnel runtime: `Data/Engine/Containers/api-backend/cmd/api-backend/vpn_tunnel.go`.
    - Agent VPN routes: `Data/Engine/Containers/api-backend/cmd/api-backend/agent_vpn_runtime.go`.
    - Remote-operation session authorization: `Data/Engine/Containers/api-backend/cmd/api-backend/remote_ops_sessions.go`.
    - Worker-backed command execution gate: `Data/Engine/Containers/api-backend/cmd/api-backend/device_processes.go`.
    - Scheduled target filtering: `Data/Engine/Containers/api-backend/cmd/api-backend/scheduled_jobs_rerun.go`.
    - Site-worker socket assignment gate: `Data/Engine/Containers/api-backend/data/services/job_scheduler/worker_socket.py`.
    - Agent WireGuard route validation: `Data/Agent/internal/roles/wireguard_tunnel/wireguard_tunnel.go`.
    - Agent auth client and token lifecycle: `Data/Agent/internal/auth`.
    - Agent token and key storage: `Data/Agent/internal/config`.
    - WireGuard control socket: `Data/Engine/Containers/wireguard-tunnel/control_server.py`.
    - WireGuard tunnel container boundary: `Data/Engine/Containers/compose.yaml` and `Data/Engine/Containers/wireguard-tunnel/Dockerfile`.

    ### Key material locations

    - Embedded edge ACME state: `Engine/Services/traefik-edge/state/acme.json`.
    - Embedded Traefik runtime config: `Engine/Services/traefik-edge/config/traefik.yml` and `Engine/Services/traefik-edge/config/dynamic/core.yml`.
    - Operator session secret: `Engine/Services/api-backend/secrets/engine_secret.txt`.
    - Script signing keys: `Engine/Services/api-backend/secrets/Certificates/Code-Signing/borealis-script-ed25519.key` and `.pub`.
    - Agent identity keys, tokens, GUID, agent ID, enrollment code, and signing trust: protected `agent.json` beside installed `Agent.exe`.

    ### WireGuard runtime behavior

    - `wireGuardRuntime.validatePeerPolicyLocked` enforces one IPv4 `/32` per agent peer, requires peer IPs inside `BOREALIS_WIREGUARD_PEER_NETWORK`, rejects Engine `/32` reuse, rejects duplicate peer public keys, and validates desired reconcile state before mutating the listener.
    - `parseWireGuardRuntimePrefixes` rejects unsafe overlay configuration before runtime starts: Engine address must be private IPv4 `/32`, peer network must be private IPv4 `/16` through `/30`, and the Engine address must sit inside that peer network.
    - `wireGuardRuntime.ensureLinuxFirewallLocked` always creates deterministic `BOREALIS-WG-INPUT` and `BOREALIS-WG-FWD` chains. No environment flag disables those firewall chains.
    - Engine-side WireGuard firewall chains drop invalid packets, accept only established/related return traffic, drop new agent-originated host ingress over the tunnel, drop agent-to-agent forwarding, and drop agent-originated forwarding toward other networks.
    - `wireguard-tunnel/control_server.py` rejects arbitrary privileged commands. It only accepts expected WireGuard listener, peer, interface, route, and Borealis firewall command shapes under service-local config and secret paths.
    - `traefik-edge` is a root container exception for host-network low-port binding. It uses UID `0` with the Borealis runtime group and only `NET_BIND_SERVICE`; it does not run with Compose `privileged: true`.
    - `wireguard-tunnel` is a root container exception for WireGuard interface setup. It uses UID `0` with the Borealis runtime group, host networking, `/dev/net/tun`, `NET_ADMIN`, `NET_RAW`, and `no-new-privileges`; it does not run with Compose `privileged: true`.
    - WireGuard key and listener config files are `0640 borealis-engine:borealis-engine` so the API backend can write them and the root-but-capability-dropped tunnel container can read them through its runtime group.
    - `Engine/Deploy/runtime.env` and `Engine/Deploy/compose.env` are `0640 root:borealis-engine` because `site-worker-orchestrator` must read them for worker launch and Compose snapshot operations. They are not world-readable.
    - `deviceAllowsRemoteAccess` blocks non-active device status for `/api/agent/vpn/ensure`, `/api/agent/vpn/ready`, `/api/agent/vnc/ensure`, `/api/tunnel/connect`, `/api/tunnel/status`, `/api/tunnel/active`, `/api/remote-ops/session`, worker-backed process/quick-run/maintenance dispatch, and scheduled target materialization.
    - Site-worker socket authentication rejects quarantined, revoked, and decommissioned devices before registering host-service sockets or file-transfer agent sessions.
    - Site workers still run with host networking because Traefik routes and local Engine APIs currently address per-worker loopback ports. They run as the `borealis-engine` runtime owner with `no-new-privileges`, dropped capabilities, read-only root filesystem, tmpfs `/tmp`, PID/memory/CPU caps, and no `/var/run/docker.sock` mount. Only `site-worker-orchestrator` owns write access to the host Docker socket, and `job-scheduler` reaches it through an Engine-internal Unix socket with HMAC authentication.

    ### Enrollment workflow

    ```mermaid
    sequenceDiagram
        participant Operator
        participant Engine
        participant SYS as "SYSTEM Agent"
        participant HELPER as "Session Helper"

        Operator->>Engine: Request installer or enrollment code
        Engine-->>Operator: Deliver hashed code record
        Note over Operator,Engine: Human-controlled code binds enrollment to expected device

        SYS->>Engine: Initiate TLS session
        Engine-->>SYS: Present CA-trusted certificate
        Note over SYS,Engine: Public or Borealis local CA validation plus hostname checks stop common MITM paths

        SYS->>SYS: Generate Ed25519 identity key pair
        Note right of SYS: Private key stored in protected agent.json

        SYS->>Engine: Enrollment request with code, public key, fingerprint
        Engine->>Operator: Show pending approval
        Operator-->>Engine: Approve device enrollment
        Engine-->>SYS: Send enrollment nonce
        SYS->>Engine: Return signed nonce to prove key possession
        Engine->>Engine: Verify signature and record GUID plus key fingerprint
        Engine->>SYS: Issue GUID, access token, refresh token, signing key
        SYS->>SYS: Store GUID and tokens in protected agent.json

        loop Secure sessions
            SYS->>Engine: Heartbeat and job polling with Bearer token
            Engine-->>SYS: Return work or refreshed state
            SYS->>Engine: Refresh request before access token expiry
        end

        Engine-->>SYS: Deliver signed script payload
        SYS->>SYS: Verify signature before execution
        SYS->>HELPER: Launch local helper only when current-user context needed
        Note over SYS,HELPER: Helper holds no Engine token and receives broker-verified work over local IPC
    ```

    ### Code-signed remote script workflow

    ```mermaid
    sequenceDiagram
        participant Operator
        participant Engine
        participant SYS as "SYSTEM Agent"
        participant HELPER as "Session Helper"

        Operator->>Engine: Upload or author script
        Engine->>Engine: Store script and metadata
        Operator->>Engine: Request execution on device and context
        Engine->>Engine: Load Ed25519 code-signing key
        Engine->>Engine: Sign script hash and execution manifest
        Engine->>Engine: Enqueue job for target host

        loop Agent job polling
            SYS->>Engine: REST heartbeat and job poll with Bearer token
            Engine-->>SYS: Pending job payload
        end

        alt SYSTEM context
            SYS->>SYS: Verify HTTPS trust, token freshness, signature, and script hash
            SYS->>SYS: Execute in SYSTEM runtime
            SYS-->>Engine: Return status, output, telemetry
        else Current-user context
            SYS->>SYS: Verify HTTPS trust, token freshness, signature, and script hash
            SYS->>HELPER: Forward broker-verified payload over local IPC
            HELPER->>HELPER: Execute within interactive user session
            HELPER-->>SYS: Return status, output, telemetry
            SYS-->>Engine: Return result
        end
    ```

    ### Refresh-token workflow details

    - Enrollment returns `guid`, access token, refresh token, and Engine signing key.
    - Agent persists GUID, access token, refresh token, expiry metadata, identity keys, and signing trust through `Data/Agent/internal/config`.
    - Base refresh-token TTL is 90 days.
    - Successful refresh resets `expires_at` to now plus 90 days.
    - Expiry is enforced by Engine clock.
    - Access tokens are EdDSA JWTs with default `expires_in = 900`.
    - Refresh tokens are used only to obtain new access tokens.
    - If refresh token is missing or invalid, the agent re-enrolls when an installer or enrollment code path is available.

    ### Common failure modes

    - `fingerprint_mismatch`: agent identity changed or identity data was wiped.
    - `token_version_mismatch`: device token version was bumped by containment or trust reset.
    - `refresh_token_expired`: agent stayed offline longer than refresh-token sliding window.
    - `refresh_token_revoked`: refresh trust was explicitly revoked.
    - `dpop_invalid`: DPoP proof missing or malformed.
    - `dpop_replayed`: DPoP proof was reused.
    - `wireguard_peer_allowed_ip_must_be_ipv4_32`: peer route was not host-only.
    - `wireguard_peer_allowed_ip_outside_peer_network`: peer route escaped configured overlay network.
    - `wireguard_public_key_already_assigned`: peer public key collided with another agent.

    ### Validation

    - Engine focused Go tests: `cd Data/Engine/Containers/api-backend && /opt/Borealis/Dependencies/Go/go1.23.12/bin/go test ./cmd/api-backend`.
    - Agent focused Go tests: `cd Data/Agent && /opt/Borealis/Dependencies/Go/go1.22.12/bin/go test ./internal/roles/wireguard_tunnel`.
    - Control socket tests: `/opt/Borealis/.cache/codex-engine-tests/bin/python3 -m pytest Data/Engine/Unit_Tests/test_wireguard_control_server.py`.
    - Engine remote-access domain wrapper: `BOREALIS_ENGINE_TEST_PYTHON=/opt/Borealis/.cache/codex-engine-tests/bin/python3 ./Engine_Unit_Tests.sh --domain remote-access`.
    - Runtime network validation still requires a disposable deployed Engine with at least two enrolled agents to prove packet-level agent-to-agent, agent-to-internal, and quarantine/revocation denial.

    ### Where to update docs when security changes

    - Update this page for security posture, trust model, containment, token, WireGuard, bootstrap, auth, and code-signing changes.
    - Update [Engine Runtime](../Reference/Core%20Runtimes/engine-runtime.md) when container, service, runtime path, or deployment boundary changes.
    - Update [Docker Stack Breakdown](../Reference/Core%20Runtimes/Stack_Breakdown.md) when Compose services, capabilities, mounts, sockets, or host networking assumptions change.
    - Update [API Reference](../Reference/Data%20and%20Schema/api-reference.md) if security-related endpoints are added or changed.
    - Update [SBOM](SBOM.md) if security work adds, removes, vendors, or downloads third-party software.
