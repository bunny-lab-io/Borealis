# Security Whitepaper

Borealis is built around layered trust. Agents do not become trusted because they can reach the Engine, operators do not become trusted because they know a password, and remote operations do not become trusted because a tunnel exists. Each layer has its own identity check, authorization gate, containment path, and audit trail.

This page starts with a plain-language security posture summary for evaluators, operators, and business stakeholders. Deeper implementation notes appear later on the page, and Codex-only source maps, endpoint lists, validation paths, and long workflows live in the final collapsed breakdown.

## Executive Summary

- Borealis is self-hosted: operators own the Engine host, network exposure, DNS, certificates, backups, and account lifecycle.
- Stable Engine installation uses exact immutable GitHub release, release-asset SHA-256 digests, Borealis install manifest, and full source commit SHA. Stable bootstrap has no mutable branch or latest-release fallback.
- Agents connect outbound to the Engine over CA-validated HTTPS and Borealis-managed WireGuard. Externally Accessible deployments use public CA validation; Internal-Only deployments use a Borealis local CA plus normal hostname validation. Internal-Only agents may use a persisted Engine IP fallback as a route hint, but the Engine FQDN remains the TLS identity. Endpoint inbound exposure is not required for normal remote operations.
- Device trust starts with operator-approved enrollment, device-generated Ed25519 identity, short-lived access tokens, hashed refresh tokens, and device status checks.
- Operator trust is protected by Aegis Cipher, MFA by default, WebAuthn passkeys, RBAC, site scoping, and strict session invalidation.
- Public Engine APIs and WebUI text-entry paths treat operator, browser, and agent-supplied text as untrusted. Borealis validates field shape, length, encoding, enum values, paths, URLs, registry data, regex patterns, and operational content before those values reach domain handlers.
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

### Input Validation and Data Hygiene

- Public Engine API handlers validate path, query, and body input before domain work. Malformed shared-validation payloads return HTTP `400` with `validation_failed` and field-level errors where practical.
- Borealis WebUI text fields use shared field-class validation before submit, and the Engine repeats validation server-side so browser checks are not the trust boundary.
- Validation is context-aware. Borealis preserves legitimate syntax for scripts, JSON, private keys, passwords, LDAP filters and DNs, command arguments, absolute remote paths, registry data, regex patterns, and WebAuthn passkey payloads while still enforcing size, UTF-8, control-character, enum, parser, and domain rules.
- Output paths that render rich UI content escape user-controlled values unless markup is known app-generated notification emphasis.

### Runtime and Container Boundaries

- The public edge is Traefik; Engine APIs stay cluster-internal behind the `api-backend` ClusterIP Service, and PostgreSQL is ClusterIP-only inside the Borealis K3s namespace.
- K3s adds a single-node Kubernetes control plane on the Engine host. K3s hosts Longhorn storage controllers, the restricted `borealis-operator`, the authoritative PostgreSQL StatefulSet, the authoritative API backend pod, the authoritative `job-scheduler` pod, the authoritative `wireguard-tunnel` pod, the authoritative `traefik-edge` pod, the production WebUI ClusterIP workload, the authoritative guacd ClusterIP workload, and operator-managed site-worker pods.
- Long-running Engine services do not mount the Docker socket after Stage 11 Compose retirement. K3s `job-scheduler` uses `borealis-operator` for K3s site-worker/workload lifecycle requests and cannot issue raw Kubernetes changes.
- Most Engine containers and K3s pods, including `remote-desktop-guacd`, run as the non-root `borealis-engine` runtime owner where supported with read-only root filesystems, dropped capabilities, `no-new-privileges`, tmpfs `/tmp`, and profile-scaled resource limits. K3s PostgreSQL uses the official non-root PostgreSQL UID on a Longhorn-backed PVC.
- Cross-service bind mounts are narrowed to explicit runtime contracts. `job-scheduler` owns site-worker Traefik route files, while dynamic `site-worker-*` containers do not mount Traefik config and cannot write those route files. WebUI source mounts are read-only inside the container.
- Only runtimes that need root keep it: K3s `traefik-edge` for host-network low-port binding and strict ACME-state access, and K3s `wireguard-tunnel` for WireGuard interface setup. These are documented exceptions, not default service behavior.
- K3s `remote-desktop-guacd` is the ClusterIP-only guacd target for API and site-worker VNC flows and does not mount the Docker socket. The K3s API backend owns API traffic through the `api-backend.borealis.svc.cluster.local:5001` ClusterIP Service and still has no Kubernetes API or Docker socket access.
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

- Stable installation begins with downloaded `Install-Engine.sh` release asset. Pipe-to-shell execution is blocked so operator can inspect file and bootstrap can hash its own on-disk bytes.
- Bootstrap requires exact stable tag, published non-prerelease release, GitHub immutable-release flag, supported Linux architecture, complete uploaded asset set, and GitHub-provided SHA-256 digests. It verifies its own asset, manifest asset, repository/release/platform/artifact URL identity, `Engine.sh` asset, release tag commit, and downloaded file sizes before execution.
- Verified release and full commit SHA are passed to `Engine.sh`. Stable sync fetches exact tag ref and verifies both tag target and checked-out `HEAD`; it never selects latest tag or falls back to `main`. Mutable ref sync requires explicit `--release-channel unstable` and remains development-only.
- Release packaging uses draft-first publication. Maintainer creates stable draft, workflow checks out exact tag and uploads `Install-Engine.sh`, `Engine.sh`, `borealis-engine-install-manifest.json`, and `SHA256SUMS`, then maintainer publishes release. Repository immutable releases lock tag and assets at publication.
- Controls address mutable branch drift, stale latest selection, tag/SHA disagreement, missing assets, asset substitution, and transport/storage corruption. They do not make malicious reviewed release code safe, protect compromised GitHub repository administration before publication, or protect already-compromised Engine host. GitHub HTTPS API and repository administration remain supply-chain trust roots.
- Fresh install and version-changing update fail closed when GitHub metadata or assets are unavailable. Existing standalone checkout can reconcile same source release without network source sync; cluster updates remain controlled through Cluster Management.
- Borealis renders Traefik runtime state under `Engine/Services/traefik-edge/state/` and `Engine/Services/traefik-edge/config/`. Let's Encrypt state is used for Externally Accessible deployments; Borealis local CA and leaf certificate state is used for Internal-Only deployments.
- Internal engine-only material such as WireGuard keys, code-signing keys, Aegis state, and auth secrets stays under Engine service runtime paths.
- First deployment follows `Set Aegis Cipher -> Create first administrator -> Complete MFA -> Enter normal Borealis`.
- Later Engine restarts follow `Unlock Aegis Cipher -> Enter normal Borealis login or passkey flow`.
- Normal authenticated endpoints are unavailable until Aegis setup or unlock is complete and the Engine reaches the `login_required` bootstrap phase.

### Engine API Input Boundary

Borealis validates public API input by domain before processing state changes or dispatching remote work.

- Covered public API domains include bootstrap and authentication, passkeys, users, directory providers, reusable credentials, sites, device inventory and search, metadata fields, device filters and saved views, scheduler and onboarding jobs, assemblies and workflows, watchdogs, patch and software management, remote files, registry, processes, services, shell, VNC, tunnel setup, agent enrollment, agent tokens, agent status, agent details, agent downloads, and agent software-management payloads.
- Validation checks include bounded JSON decoding, UTF-8 and NUL/control-character rejection, maximum lengths, enum allowlists, identifiers, slugs, hostnames, IPs, CIDRs, URLs, paths, registry names, regex compilation, and domain-specific payload shape.
- Internal HMAC worker/operator routes use their own narrow command contracts and are not treated as public operator or agent input surfaces.

### Engine Container Hardening

Borealis Engine containers are deployed with least-privilege defaults and only receive the Linux capabilities, filesystem access, Docker access, and resource headroom they need to operate.

- `Engine.sh` creates or repairs a `borealis-engine` system user/group and writes numeric runtime IDs into the Compose environment during every deploy.
- Normal Engine services run as the `borealis-engine` runtime owner, use read-only root filesystems, drop default Linux capabilities, set `no-new-privileges`, use tmpfs for writable scratch paths, and receive profile-scaled CPU, memory, and PID caps.
- K3s PostgreSQL uses the official PostgreSQL non-root UID with the Borealis runtime group so imported database state stays compatible with the upstream image.
- `traefik-edge` is a root exception because it binds host-network ports `80` and `443` and must renew strict ACME storage owned by the Borealis runtime user for backup export. It drops all default capabilities and adds only `NET_BIND_SERVICE` and `DAC_OVERRIDE`.
- K3s `wireguard-tunnel` is a root exception because it creates and reconciles the WireGuard interface. It uses `hostNetwork`, `/dev/net/tun`, `NET_ADMIN`, and `NET_RAW`, with `no-new-privileges`, read-only root filesystem, tmpfs scratch paths, no ServiceAccount token, and a constrained control socket instead of full privileged mode.
- Server Info `traefik-edge reload` queues a K3s scheduler action that calls the HMAC-authenticated `borealis-operator` API and restarts the known `traefik-edge` Deployment. No Docker helper container is launched for this path after Stage 11.
- Docker socket access is removed from long-running Engine services after Stage 11. K3s worker metrics come through `borealis-operator` instead of Docker socket reads.
- K3s `job-scheduler` reaches K3s lifecycle operations through the HMAC-authenticated `borealis-operator` API. It has no ServiceAccount token, kubeconfig, Kubernetes API credential, or Docker socket.
- Dynamic `site-worker-*` containers run non-root with dropped capabilities, `no-new-privileges`, read-only root filesystems, tmpfs `/tmp`, PID/memory/CPU caps, read-only API config and secrets, writable API cache/log paths, no Docker socket, and no Traefik config mount.
- K3s `remote-desktop-guacd` runs non-root, mounts only read-only host timezone data, exposes only a ClusterIP Service, and does not mount the Docker socket.
- WebUI source mounts are read-only inside the WebUI container; dev-mode edits happen from the host runtime source directory, while Vite cache writes stay under `/tmp`.
- Cross-service mounts use explicit path contracts. `job-scheduler` is the single writer for site-worker Traefik route files; site workers keep database state but cannot write route files.

### K3s Cluster Security

K3s is a host-level control-plane baseline plus locked-down workload migration path, not a broad runtime mutation API. It gives Borealis a future migration target while each workload keeps an explicit cutover and rollback boundary.

Multi-node mode adds narrow cluster-controller RBAC plus root-owned `borealis-node-manager`. Node manager accepts only enroll, fetch, fast-forward checkout, revision redeploy/promotion, health inspection, application drain, edge fencing, persistent K3s member fencing, exact-version probe conformance, and status operations authenticated over local Unix socket. Rolling redeploy atomically installs target manager and uses shell-free root-only transient activation after node-action Jobs become idle; activator requires pinned commit SHA, matching target worktree, its exact staged executable inode, and fixed service restart. It has no arbitrary command or remote-shell operation. K3s secrets encryption, checksum-pinned infrastructure manifests, immutable application/upgrade images, explicit peer firewall allowlists, one fixed Cluster Virtual IP lease, and per-node required affinity bound cluster trust surface.

Cluster Aegis unlock propagation uses dedicated TLS 1.3 listener, cert-manager-issued mutual certificate, headless API-peer Service, 1 KiB request limit, strict 32-byte key encoding, and local verification-token proof before memory install. Shared certificate authenticates Borealis API replica membership. Active and isolated candidate API Deployments share only dedicated Aegis-peer selector; unlocked replicas periodically seed replacements before promotion without admitting candidates to public API Service. Key never enters PostgreSQL or Kubernetes Secret.

!!! bug "Shared Engine identity qualification remains open"
    An unlocked, healthy cluster can still reject an operator session on another replica when nodes initialized separate signing identities. Keep the existing Engine identity and private backups intact, retain the failing replica checks, and pause release qualification until the identity repair tracked in [#519](https://github.com/bunny-lab-io/Borealis/issues/519) passes. A fresh login or healthy Kubernetes probes do not prove cross-replica authentication or Agent signing trust.

Running replicas reload validated TLS identity and independently managed CA trust without restarting key holders. Expired identities fail closed, explicit CA retirement is preserved across malformed leaf updates, and each key transfer requires a fresh TLS handshake with no redirect following. Follow the [certificate renewal and CA rotation procedure](../Engine/managing-engine-clusters.md#certificate-renewal-and-ca-rotation); an all-cold restart still requires operator unlock.

Safe K3s removal writes root-only persistent fence marker and schedules local `k3s.service` disable/stop before controller deletes Kubernetes Node resource. Emergency path never contacts target and requires operator attestation host is powered off. Cluster controller may delete Node resources and create system-upgrade-controller Plans; those permissions stay isolated from namespace operator and ordinary API workloads.

- `Engine.sh` writes K3s bootstrap and fixed operator manifests during deployment. Runtime services do not receive kubeconfig, Kubernetes API credentials, or kubectl access; they must call `borealis-operator` for allowlisted K3s lifecycle work.
- Borealis writes its K3s config as `/etc/rancher/k3s/config.yaml.d/10-borealis.yaml` and records the desired hash in `Engine/Deploy/k3s-baseline.sha256`.
- Bundled K3s Traefik and ServiceLB stay disabled so the Borealis-managed K3s `traefik-edge` workload remains the only ingress owner, certificate owner, and dynamic route-file reader.
- K3s kubeconfig is rendered root-only by default. Deploy fails if the kubeconfig is group-readable or world-readable.
- `borealis-k3s-api-firewall.service` installs a host iptables chain for TCP `6443`, allows loopback and IPv4 K3s CNI/flannel traffic, then drops other inbound API traffic.
- K3s Secret encryption at rest is enabled for Kubernetes objects, but Aegis remains the Borealis security model for protected operator, credential, token, and signing material.
- Generated runtime-env K3s Secrets are deployment plumbing rendered from Engine deploy env files. They do not become the source of truth for Aegis state, Engine secret files, signing keys, credentials, passkeys, or WireGuard keys.
- The `borealis` namespace and K3s nodes receive Borealis labels plus `borealis.io/k3s-config-hash` annotations for ownership and drift review.
- Longhorn runs in `longhorn-system` as K3s storage infrastructure for future Borealis PVC-backed workloads. It requires host iSCSI support and storage-controller/CSI privileges, so it should be treated as Engine-host infrastructure with the same trust weight as K3s itself.
- `Engine.sh deploy` may install or verify the Longhorn host iSCSI dependency and apply the pinned Longhorn manifest, but normal deploy does not delete Longhorn volumes, PVCs, PostgreSQL state, or Aegis-protected material. Borealis clears the upstream Longhorn default-StorageClass annotation after manifest apply and creates the explicit-use `borealis-longhorn` StorageClass for fresh single-node PVCs. Existing PostgreSQL PVCs keep their current StorageClass during redeploy so Kubernetes immutable storage fields are not churned.
- Stage 9 K3s PostgreSQL is the authoritative database runtime with one Longhorn PVC and ClusterIP-only Services. It receives generated PostgreSQL credentials through a K3s Secret, initializes data under a PVC subdirectory, and uses a short-lived root init container with only `CHOWN`, `DAC_OVERRIDE`, and `FOWNER` to repair PVC ownership across first-run and retry paths. The PostgreSQL runtime container remains non-root with dropped capabilities. Cutover quiesces K3s API/scheduler/site-worker writers, imports a final logical snapshot from Compose PostgreSQL, moves runtime `BOREALIS_DATABASE_URL` to `postgres-db.borealis.svc`, runs schema initialization as a K3s Job, and retires stale Compose PostgreSQL containers without deleting the Longhorn PVC.
- `borealis-operator` runs as a K3s Deployment with a namespace-scoped ServiceAccount, non-root user, dropped capabilities, read-only root filesystem, `no-new-privileges`, `RuntimeDefault` seccomp, probes, and resource caps.
- `borealis-operator` RBAC allows `get` and `list` for pods, services, Deployments, ReplicaSets, StatefulSets, and Metrics Server podmetrics inside the `borealis` namespace. Lifecycle permissions add `patch` only for named Borealis Deployments/StatefulSets, plus pod `create`/`delete` for fixed site-worker pod lifecycle. It has no Kubernetes Secret, node, cluster-scope, arbitrary service account, raw YAML, arbitrary hostPath, or privileged pod permission through the Borealis API.
- The operator API is ClusterIP-only and HMAC authenticated with `X-Borealis-Operator-Token`. Status verbs are `GetClusterSummary`, `ListWorkloads`, `GetWorkloadStatus`, and `ListSiteWorkers`. Lifecycle verbs are `RolloutKnownWorkload`, `RestartKnownWorkload`, `ScaleKnownWorkload`, `LaunchSiteWorker`, and `RetireSiteWorker`.
- The K3s API backend can query operator status through `BOREALIS_OPERATOR_BASE_URL`, but it remains Kubernetes-blind and exposes only authenticated admin status to operators.
- The K3s `api-backend` pod has no ServiceAccount token, no kubeconfig, no Docker socket, and no host networking. It receives generated runtime env through the K3s Secret `borealis-api-backend-runtime-env`, listens on pod networking behind the `api-backend` ClusterIP Service on port `5001`, reaches PostgreSQL through `postgres-db.borealis.svc`, and owns watchdog/background loops through `BOREALIS_API_BACKGROUND_LOOPS=1` after Compose API retirement.
- The K3s `job-scheduler` pod has no ServiceAccount token, no kubeconfig, no Docker socket, and no host networking. It receives generated runtime env through the K3s Secret `borealis-job-scheduler-runtime-env`, reaches PostgreSQL through `postgres-db.borealis.svc`, calls the K3s API backend through `api-backend.borealis.svc.cluster.local:5001`, and uses `Recreate` rollout strategy to avoid duplicate scheduler loops.
- The K3s `wireguard-tunnel` pod has no ServiceAccount token, no kubeconfig, and no Docker socket. It receives generated runtime env through the K3s Secret `borealis-wireguard-tunnel-runtime-env`, is pinned to Engine nodes, mounts only `/dev/net/tun` plus `Engine/Services/wireguard-tunnel`, creates the existing `0660` WireGuard Unix control socket for the API backend runtime group, and is reconciled before API startup during deploy.
- K3s `webui-frontend` and `remote-desktop-guacd` pods run without ServiceAccount tokens, use non-root runtime IDs, dropped capabilities, read-only root filesystems, `RuntimeDefault` seccomp, `/tmp` memory-backed `emptyDir`, CPU/memory caps, and ClusterIP-only Services. Production WebUI traffic reaches K3s through Borealis-managed K3s `traefik-edge`; Borealis does not use Kubernetes Ingress for this path, and guacd remains internal-only through its ClusterIP Service.
- K3s `traefik-edge` runs one host-network pod with no ServiceAccount token, read-only root filesystem, tmpfs `/tmp`, `RuntimeDefault` seccomp, dropped default capabilities, and only `NET_BIND_SERVICE` plus `DAC_OVERRIDE`. It mounts only `Engine/Services/traefik-edge` so it can keep existing ACME/local CA state and hotload watched dynamic route files.
- K3s site-worker pods run without ServiceAccount tokens and use pod networking behind per-worker ClusterIP Services for remote-operation routing. They receive only fixed API logs/cache/config/secrets hostPath mounts, use `postgres-db.borealis.svc` for database access, and still run non-root with dropped capabilities, read-only root filesystem, `RuntimeDefault` seccomp, and memory-backed `/tmp`.
- The WebUI dev bridge uses a fixed allowlist of read-only hostPath mounts for `Engine/Services/webui-frontend/data/web-interface/` HMR parity. Runtime services cannot request arbitrary hostPath mounts through the operator API.

### Operator Authentication

- Operator accounts support password plus TOTP MFA.
- WebAuthn passkeys are supported for direct browser sign-in once bootstrap is complete.
- Aegis protects stored password hashes, TOTP secrets, passkey cryptographic material, directory bind passwords, reusable credentials, and GitHub API token storage.
- Active sessions are revalidated against the operator row on authenticated requests. Deleted users, disabled directory cache entries, and deprovisioned directory users stop passing authorization checks without waiting for token expiry.
- Only an administrator can explicitly disable MFA for an operator account.
- Cluster mutations require an active administrator session but do not impose a separate short-lived step-up window. Destructive cluster actions retain exact typed confirmations, health and quorum gates, exclusive-operation fencing, and actor audit records.

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
- Clustered API replicas persist active tunnel identity, endpoint, allowed ports, operator association, readiness, and transport timestamps in PostgreSQL with optimistic generation checks. Session rows contain no signed tunnel token; tokens remain short-lived derived values. Existing WireGuard private key lease protection and Backup encryption requirements still apply.
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
- Operators provide site, device OS, discovery scope, stored credential, remote connection settings, and schedule.
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
- Keep K3s API TCP `6443` protected by the Borealis firewall rule unless a later multi-node design explicitly opens a narrow node-to-node path.
- Keep Longhorn limited to the Engine cluster storage plane. Do not expose Longhorn UI or manager endpoints publicly, and do not treat Longhorn snapshots as a replacement for Borealis backup/restore.
- Keep `borealis-operator` ClusterIP-only and verify its ServiceAccount cannot read Secrets, list Nodes, or patch unknown workloads after lifecycle verbs are added.
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

    - Stable release bootstrap: `Install-Engine.sh`.
    - Exact release sync boundary: `Engine.sh` functions `resolve_repo_ref` and `sync_repo`.
    - Release asset builder: `Tests/tools/build_engine_release_assets.py`.
    - Draft release packaging workflow: `.github/workflows/publish-engine-release-assets.yml`.
    - Bootstrap regression tests: `Tests/Unit_Tests/test_engine_release_bootstrap.py`.
    - Engine auth bootstrap: `Data/Engine/Containers/api-backend/cmd/api-backend/bootstrap_*.go`.
    - Operator auth and passkeys: `Data/Engine/Containers/api-backend/cmd/api-backend/auth_*.go`.
    - Directory providers: `Data/Engine/Containers/api-backend/cmd/api-backend/directory_*.go`.
    - Device enrollment and approvals: `Data/Engine/Containers/api-backend/cmd/api-backend/agent_enrollment.go` and `Data/Engine/Containers/api-backend/cmd/api-backend/admin_approvals.go`.
    - Device containment routes: `Data/Engine/Containers/api-backend/cmd/api-backend/device_security.go`.
    - Engine tunnel runtime: `Data/Engine/Containers/api-backend/cmd/api-backend/vpn_tunnel.go`.
    - Agent VPN routes: `Data/Engine/Containers/api-backend/cmd/api-backend/agent_vpn_runtime.go`.
    - Remote-operation session authorization: `Data/Engine/Containers/api-backend/cmd/api-backend/remote_ops_sessions.go`.
    - Worker-backed command execution gate: `Data/Engine/Containers/api-backend/cmd/api-backend/device_processes.go`.
    - Scheduled target filtering: `Data/Engine/Containers/api-backend/cmd/api-backend/scheduled_jobs_rerun.go`.
    - Site-worker socket assignment gate: `Data/Engine/Containers/site-worker/data/services/job_scheduler/worker_socket.py`.
    - Agent WireGuard route validation: `Data/Agent/internal/roles/wireguard_tunnel/wireguard_tunnel.go`.
    - Agent auth client and token lifecycle: `Data/Agent/internal/auth`.
    - Agent token and key storage: `Data/Agent/internal/config`.
    - WireGuard control socket: `Data/Engine/Containers/api-backend/internal/wireguardcontrol/server.go`.
    - WireGuard tunnel pod boundary: `Engine.sh` K3s `wireguard-tunnel` manifest and `Data/Engine/Containers/wireguard-tunnel/Dockerfile`.
    - Engine deployment identity, ownership repair, profile caps, and Compose env rendering: `Engine.sh`.
    - K3s baseline reconcile, bridge workload manifests, API/scheduler runtime-env Secret rendering, config hashes, API firewall unit, namespace labels, and node annotations: `Engine.sh`.
    - API background-loop guard for bridge, validator, and traffic-owner modes: `Data/Engine/Containers/api-backend/cmd/api-backend/main.go`.
    - K3s operator bridge process, HMAC command API, restricted lifecycle verbs, admin status route, and client healthcheck: `Data/Engine/Containers/api-backend/cmd/api-backend/borealis_operator.go`.
    - K3s operator image: `Data/Engine/Containers/borealis-operator/Dockerfile`.
    - Static container hardening and service mount contracts: `Data/Engine/Containers/compose.yaml` and `Data/Engine/Containers/compose.env.example`.
    - Container policy validation: `Data/Engine/Containers/check-compose-policy.py` and `.github/workflows/engine-container-policy.yml`.
    - API input validation helpers and shared `validation_failed` response shaping: `Data/Engine/Containers/api-backend/cmd/api-backend/input_validation.go`.
    - API input validation tests: `Data/Engine/Containers/api-backend/cmd/api-backend/input_validation_test.go`.
    - WebUI field-class validation and `/api/*` fetch guard: `Data/Engine/Containers/webui-frontend/data/web-interface/src/app/utils/inputValidation.js` and `Data/Engine/Containers/webui-frontend/data/web-interface/src/app/runtime/bootstrapClientRuntime.js`.
    - Scheduler-to-operator lifecycle calls and route ownership: `Data/Engine/Containers/api-backend/cmd/api-backend/scheduler_manager.go`.
    - K3s site-worker pod template, fixed hostPath allowlist, HMAC lifecycle verbs, and host-loopback bridge annotations: `Data/Engine/Containers/api-backend/cmd/api-backend/borealis_operator.go`.
    - Site-worker bind-host split for K3s host-loopback pods: `Data/Engine/Containers/site-worker/data/services/job_scheduler/worker.py`.
    - Scheduler image and entrypoint routing: `Data/Engine/Containers/job-scheduler/Dockerfile`, `Data/Engine/Containers/job-scheduler/entrypoint.sh`, and `Data/Engine/Containers/job-scheduler/healthcheck.sh`.
    - Remote Desktop guacd container runtime: `Data/Engine/Containers/remote-desktop-guacd/Dockerfile`, `Data/Engine/Containers/remote-desktop-guacd/entrypoint.sh`, and `Data/Engine/Containers/remote-desktop-guacd/healthcheck.sh`.
    - WebUI K3s bind-host and production route support: `Engine.sh`, `Data/Engine/Containers/traefik-edge/entrypoint.sh`, `Data/Engine/Containers/webui-frontend/entrypoint.sh`, `Data/Engine/Containers/webui-frontend/static-server.js`, and `Data/Engine/Containers/webui-frontend/healthcheck.js`.

    ### Input validation runtime behavior

    - Shared Go validators reject malformed public API path, query, and body fields before handlers mutate state, enqueue work, or call remote-operation helpers.
    - Shared WebUI validators classify text inputs as plain single-line, multiline, identifier/ref, slug, host/IP/CIDR, URL, remote path, registry path/value name, regex, secret, or code/content.
    - Bounded JSON decode paths protect public APIs from oversized objects and unexpected field types. Endpoint-specific handlers keep compatible legacy errors where existing callers depend on them.
    - Agent enrollment `agent_pubkey` and `client_nonce` use the `base64` field class, capped at 4096 encoded bytes each. Go preserves encoded text and validates UTF-8, controls, length and the existing base64 decoder (including legacy whitespace and URL alphabet); enrollment retains decoded key/nonce checks and proof-of-possession validation. These are Agent protocol fields with no WebUI text entry. Generic HTML event-attribute detection must not inspect binary encodings. `TestAgentEnrollmentRequestHandlerCreatesPendingResponse` uses a fixed valid Ed25519 SPKI containing an attribute-like base64 suffix; `TestEnrollmentBase64InputPreservesEncodingAndRejectsMalformedValues` covers preservation and malformed, oversized, control-bearing and ordinary markup rejection.
    - Passkey/WebAuthn payloads are validated as structured credential ceremony JSON, not generic text, so browser-supplied authenticator material is preserved while malformed envelopes are rejected.
    - Remote operations preserve valid operator intent for absolute paths, command arguments, scripts, registry edits, LDAP syntax, and regex filters while enforcing context-specific parser and length limits.

    ### Key material locations

    - Embedded edge ACME state: `Engine/Services/traefik-edge/state/acme.json`.
    - Stage 1 K3s config hash: `Engine/Deploy/k3s-baseline.sha256`.
    - Stage 7 API traffic-owner hash: `Engine/Deploy/k3s-api-backend.sha256`.
    - Stage 8 scheduler hash: `Engine/Deploy/k3s-job-scheduler.sha256`.
    - Operator manifest input hash: `Engine/Deploy/borealis-operator.sha256`.
    - WebUI bridge workload manifest input hash: `Engine/Deploy/k3s-webui-frontend.sha256`.
    - Guacd bridge workload manifest input hash: `Engine/Deploy/k3s-remote-desktop-guacd.sha256`.
    - Aggregate bridge workload manifest input hash: `Engine/Deploy/k3s-bridge-workloads.sha256`.
    - Stage 1 K3s kubeconfig: `/etc/rancher/k3s/k3s.yaml`.
    - Operator HMAC secret: `BOREALIS_OPERATOR_SECRET` in `Engine/Deploy/runtime.env`, `Engine/Deploy/compose.env`, and K3s Secret `borealis-operator-auth`.
    - Embedded Traefik runtime config: `Engine/Services/traefik-edge/config/traefik.yml` and `Engine/Services/traefik-edge/config/dynamic/core.yml`.
    - Operator session secret: `Engine/Services/api-backend/secrets/engine_secret.txt`.
    - Script signing keys: `Engine/Services/api-backend/secrets/Certificates/Code-Signing/borealis-script-ed25519.key` and `.pub`.
    - Agent identity keys, tokens, GUID, agent ID, enrollment code, and signing trust: protected `agent.json` beside installed `Agent.exe`.

    ### Shared Engine identity qualification boundary

    - Issue [#519](https://github.com/bunny-lab-io/Borealis/issues/519) tracks an unresolved clustering defect. Host-local `engine_secret.txt`, Agent JWT Ed25519 private key and script-signing Ed25519 keypair currently initialize independently. Existing Aegis mTLS fanout transfers only the unlocked Aegis data key; it does not synchronize these identity files. Shared Aegis unlock therefore does not establish shared authentication identity.
    - `auth.go` loads the operator session secret before opening the store. `newGoAegisService` also uses that secret for passkey credential lookup HMAC. `agent_tokens.go` and `agent_enrollment.go` load or generate the two Agent signing identities. `server_backup.go` includes fixed identity files in encrypted Engine configuration backups. An identity repair must preserve all these consumers and backup/export semantics together.
    - Fresh-node admission qualification must check one operator token against every active replica and compare script-signing public fingerprints. Check candidate identity before promotion and repeat after replacement, rolling update and HMR restoration. Database readiness, Aegis unlock and unauthenticated HTTP health cannot substitute for these checks. Never exercise non-idempotent Agent work while signing trust remains inconsistent.
    - Preserve existing Engine identity as an explicitly chosen authority; never let a newly generated joining-node key win by startup order. Keep identity material out of runtime-env Kubernetes Secrets and ordinary operation/event payloads. Issue [#379](https://github.com/bunny-lab-io/Borealis/issues/379) owns broader storage authority separation; [#444](https://github.com/bunny-lab-io/Borealis/issues/444) owns the separate code-signing database-storage investigation. Neither issue authorizes an implicit storage migration.
    - Legacy recovery requires its own reviewed immutable implementation and operator-approved identity migration, including fixed file allowlist, cluster/node identity binding, private rollback journal, unchanged original identity, controller-owned target quiescence and consumer refresh. Do not copy an entire secrets directory, inject `SECRET_KEY`, rotate the original Engine identity or treat direct-replica diagnostic access as public availability qualification. No such legacy identity recovery is implemented by the configuration-only helper from #517.

    ### Engine container hardening implementation

    Borealis treats Docker as an Engine host trust boundary, not as a replacement for host security. Container hardening reduces blast radius when a service process misbehaves, but the Engine host and Docker daemon remain privileged control planes.

    #### Runtime identity and ownership

    - `Engine.sh` creates or repairs a `borealis-engine` system group and user during deploy. Defaults are UID/GID `64646`, shell `nologin` when available, no interactive login path, and no normal home directory. On shadow-utils hosts, deploy passes explicit `SYS_UID_MAX` and `SYS_GID_MAX` overrides while creating the account so the fixed high Borealis runtime ID does not trigger distro default system-ID range warnings.
    - Deploy fails instead of silently reusing a mismatched UID/GID when the configured `borealis-engine` name or numeric ID is already bound to another account.
    - `Engine.sh` writes `BOREALIS_ENGINE_RUNTIME_OWNER_UID`, `BOREALIS_ENGINE_RUNTIME_OWNER_GID`, `BOREALIS_ENGINE_RUNTIME_USER`, and `BOREALIS_ENGINE_RUNTIME_GROUP` into `Engine/Deploy/compose.env`.
    - `Engine.sh` still detects the host Docker socket group for legacy metadata, but no long-running Stage 11 Engine service receives `/var/run/docker.sock`.
    - Runtime service directories under `Engine/Services/` are chowned to the runtime owner during deploy. Secret paths keep stricter permissions.
    - Traefik ACME storage remains `0600` but is owned by the Borealis runtime user so authenticated Backup/Restore export can snapshot it without granting group/world read access. The Traefik edge process runs as root with `DAC_OVERRIDE` and remains able to read and renew the file.
    - `Engine/Deploy/runtime.env` and `Engine/Deploy/compose.env` are `0640 root:borealis-engine` because deploy renders K3s runtime Secrets from them and some pods consume projected values. `webui-frontend.env` remains `0600`.
    - API secrets are `0750` directories with files stripped of group/other access. WireGuard config and secret files are `0640` so the API backend can write them and the K3s tunnel pod can read them through the Borealis runtime group.
    - K3s PostgreSQL state lives on the Longhorn PVC `postgres-data-postgres-db-0`. `Engine.sh` resolves `BOREALIS_POSTGRES_RUNTIME_UID` from existing PostgreSQL state when present or defaults to the upstream image UID.

    #### Static runtime hardening

    - `compose.yaml` intentionally contains `services: {}` after Stage 11. Static runtime hardening now applies through Engine-authored K3s manifests.
    - Hardened K3s workloads declare CPU and memory limits from profile-managed runtime settings with operator override support.
    - Most static workloads run as `${BOREALIS_ENGINE_RUNTIME_OWNER_UID}:${BOREALIS_ENGINE_RUNTIME_OWNER_GID}`.
    - Writable paths are explicit hostPath, PVC, or memory-backed `emptyDir` mounts. A read-only root filesystem forces cache, log, run, and state writes into reviewed paths.
    - `api-backend` runs non-root and does not mount the Docker socket. Its service root is a scratch `emptyDir`; only API cache, config, logs, and secrets are hostPath-mounted, alongside specific Traefik and WireGuard paths required for edge settings and tunnel reconciliation.
    - K3s `job-scheduler` runs non-root and does not mount the Docker socket or a ServiceAccount token. It mounts API cache/logs read-write, API config/secrets read-only, and Traefik dynamic config read-write.
    - `site-worker-orchestrator` is retired after Stage 11. Deploy still removes stale Compose-era containers with that name, but the Go runtime source, Docker lifecycle fallback, Unix socket, and K3s scheduler mount are removed.
    - `webui-frontend` runs non-root. Runtime source mounts are read-only inside the container; Vite cache and resolved config temp writes use tmpfs, including `node_modules/.vite-temp` in dev mode.
    - K3s `remote-desktop-guacd` runs non-root, exposes guacd only through its ClusterIP Service on port `4822`, mounts only read-only host timezone data, writes only transient in-container file logs under tmpfs, and does not mount the Docker socket.
    - K3s `api-backend`, `job-scheduler`, `webui-frontend`, `remote-desktop-guacd`, Traefik, WireGuard, and site-worker pods mirror the non-root where possible, read-only-root, dropped-capability, tmpfs-style `/tmp`, read-only host timezone data, and CPU/memory cap posture where Kubernetes supports it. Dev WebUI pods also use memory-backed `node_modules/.vite-temp` scratch for Vite config bundles. Kubernetes does not enforce the Compose per-container PID cap here; `Engine.sh` records the desired cap as a pod annotation until a later cutover decides the runtime-level PID policy.
    - K3s site-worker CPU/RAM visibility uses namespace-scoped `metrics.k8s.io` podmetrics read by `borealis-operator`; K3s `api-backend` receives normalized metrics through the operator API and never receives kubeconfig or Kubernetes API credentials.
    - K3s `postgres-db` runs as the PostgreSQL runtime UID with Borealis runtime group compatibility, explicit PVC and scratch mounts, ClusterIP-only exposure, no ServiceAccount token, dropped capabilities, read-only root filesystem, and `RuntimeDefault` seccomp. PostgreSQL logging uses K3s pod stdout/stderr instead of a host log bind mount.
    - `traefik-edge` is an explicit root exception for host-network low-port binding and strict ACME renewal. It drops default capabilities and adds only `NET_BIND_SERVICE` and `DAC_OVERRIDE`.
    - K3s `wireguard-tunnel` is an explicit root exception for WireGuard interface setup. It gets `/dev/net/tun`, `NET_ADMIN`, and `NET_RAW`; it does not run with full privileged mode.

    #### Docker socket boundary

    - No long-running Stage 11 Engine service mounts the Docker socket.
    - K3s site-worker status and CPU/RAM metrics come from `borealis-operator` over the internal HMAC API. API and UI reads do not mount the Docker socket for migrated K3s workers.
    - K3s `job-scheduler` talks to `borealis-operator` for K3s lifecycle and uses the WireGuard control socket for tunnel reconcile work. It does not talk to Docker or mount kubeconfig.
    - Server Info does not expose K3s `job-scheduler` self-restart because the current scheduler must complete the queue item before rollout starts.

    #### Dynamic site-worker launch policy

    Stage 5 K3s bridge mode routes site-worker lifecycle through `borealis-operator`. Callers do not supply arbitrary Docker flags, Kubernetes pod specs, or raw manifests in this path.

    - Container and K3s pod names must use the `site-worker-` prefix. Docker-backed workers keep `site-worker-<worker_guid>` names until Docker-backed mode is retired. K3s bridge workers use deterministic `site-worker-<sanitized-site-name>` pod names when the scheduler can resolve the site name; their worker GUID is deterministic per site so Agent Socket.IO route URLs stay stable across deploys and pod replacement.
    - Site create and rename requests reject names that would produce an empty K3s worker slug, duplicate another site's normalized slug, or exceed the K3s object-name budget.
    - Images must match `BOREALIS_SITE_WORKER_IMAGE` or `BOREALIS_SITE_WORKER_IMAGE_ALLOWLIST`.
    - Workers use pod networking behind per-worker ClusterIP Services while keeping stable route URLs through scheduler-managed Traefik dynamic routes.
    - Workers run as the Borealis runtime UID/GID from `compose.env`.
    - Workers use `no-new-privileges`, `--cap-drop ALL`, `--read-only`, tmpfs `/tmp`, `--memory`, `--cpus`, and `--pids-limit`.
    - Worker resource caps come from `BOREALIS_SITE_WORKER_MEMORY_LIMIT`, `BOREALIS_SITE_WORKER_CPU_LIMIT`, and `BOREALIS_SITE_WORKER_PIDS_LIMIT`.
    - Workers receive labels for role, site ID, worker GUID, remote operation port, remote desktop port, selected image, and lifecycle owner.
    - Workers get `BOREALIS_SITE_WORKER_ROUTE_FILE_WRITES=0`, so legacy worker route helpers keep database state only and cannot write Traefik route files.
    - Workers mount API logs/site-worker logs read-write, API cache read-write, API config read-only, and API secrets read-only.
    - K3s bridge workers receive the same runtime path contract through a fixed operator-authored pod template. Runtime env is projected through the `borealis-site-worker-runtime-env` K3s Secret so the operator does not need Kubernetes Secret read permission. They also receive fixed read-only host timezone data mounts so worker-local logs and time calculations match the Engine host timezone.
    - K3s site workers, Stage 7 API, and Stage 8 scheduler now use pod networking and Service DNS. Host-network exceptions remain limited to `traefik-edge` for HTTP/HTTPS/health ports and `wireguard-tunnel` for UDP tunnel ownership.
    - K3s bridge worker pods use `restartPolicy: OnFailure` so transient worker crashes restart without granting the pod Kubernetes API access. Clean idle TTL exits still complete, and scheduler reconcile retires terminal pods before replacement.
    - Workers do not mount `/var/run/docker.sock`.
    - Workers do not mount Traefik config.
    - Workers do not receive privileged mode, added capabilities, devices, namespace overrides, command overrides, or caller-provided environment overrides through the orchestrator API.
    - Workers have an idle TTL of 300 seconds by default and are reconciled by `job-scheduler`.

    #### Site-worker route ownership

    - `job-scheduler` is the only writer for dynamic site-worker Traefik route files.
    - Route files use `Engine/Services/traefik-edge/config/dynamic/site-worker-<worker_guid>.yml`.
    - Route metadata records the current lifecycle owner, `borealis-operator`.
    - When a worker is retired or reconciled away, `job-scheduler` removes the route file and asks the operator to stop/remove the workload.
    - Scheduler reconciliation prunes terminal route rows and orphaned `site-worker-*.yml` files that no longer have active database routes.
    - Dynamic workers keep host-service runtime behavior, remote operations, Ansible execution, file-transfer helpers, and database state updates, but do not write their own Traefik routes.

    #### Service-action path

    - Server Info service actions create work items. K3s `job-scheduler` sends operator-safe K3s workload actions to `borealis-operator` and sends WireGuard reconcile to the mounted control socket.
    - `traefik-edge reload` is constrained by `resolveOverviewServiceAction`, then handled as `RestartKnownWorkload(service_key=traefik-edge)` through the operator API. Unsupported service/action/mode combinations are rejected before dispatch.
    - No service-action path launches Docker helper containers after Stage 11.

    #### Bind-mount strategy

    - Borealis uses host bind mounts for runtime ownership clarity and backup/restore visibility. Named Docker volumes are not treated as a security boundary.
    - Security comes from service-specific mount targets, read-only flags, non-root users, dropped capabilities, read-only root filesystems, and single-writer ownership.
    - `api-backend` does not mount the entire `Engine/Services` tree or its whole service root from hostPath. It receives an `emptyDir` service root, exact API cache/config/logs/secrets hostPath mounts, and the Traefik/WireGuard paths it must manage.
    - K3s `api-backend` receives the fixed API subpath, Traefik, and WireGuard hostPath allowlist plus a generated runtime-env Secret. Its shadow DB validator Job receives a scratch API service root, read-only API secrets, and a generated runtime-env Secret with `BOREALIS_DATABASE_URL` pointed at K3s PostgreSQL. The PostgreSQL schema initializer receives a scratch API service root only. None of these paths receive arbitrary hostPath, kubeconfig, a ServiceAccount token, or Docker socket access.
    - K3s `job-scheduler` does not mount the whole API runtime or WireGuard runtime. It receives the exact API cache/log/config/secrets paths it needs plus the Traefik dynamic directory it owns for worker routes.
    - Retired `site-worker-orchestrator` runtime source is removed; no scheduler path launches Docker helper containers or mounts Traefik config through that helper.
    - `remote-desktop-guacd` has no service data host bind mounts and does not receive Engine secrets, Docker socket, Traefik config, API runtime paths, or host log directories. Its only host bind exception is fixed read-only timezone data.
    - WebUI runtime source is mounted read-only into the WebUI container so source edits happen from the host runtime tree, not from inside the container.
    - K3s WebUI dev bridge hostPath use is restricted to the same fixed read-only WebUI source paths as Compose. This is an Engine-authored manifest exception, not a runtime operator API capability.

    #### Resource profile behavior

    - `Engine.sh` derives profile-managed caps from the selected Engine deployment profile and writes them to `compose.env`.
    - Caps are per-container limits, not reservations.
    - Site-worker memory, CPU, and PID caps apply per active worker. Host sizing must account for expected active worker count.
    - PostgreSQL memory caps derive from the deployment profile rather than sample idle usage.
    - WebUI limits account for production static serving versus dev Vite behavior.
    - Operators can override individual caps through environment variables before redeploy.

    #### Policy validation

    - `Data/Engine/Containers/check-compose-policy.py` renders Compose with `docker compose config --format json` and validates that `compose.yaml` remains retired with no services.
    - The policy check fails if any service returns to Compose ownership after Stage 11.
    - Static runtime hardening now lives in Engine-authored K3s manifests and focused runtime tests instead of Compose service policy checks.
    - `.github/workflows/engine-container-policy.yml` runs the policy check and Compose validation in CI.

    ### WireGuard runtime behavior

    - `wireGuardRuntime.validatePeerPolicyLocked` enforces one IPv4 `/32` per agent peer, requires peer IPs inside `BOREALIS_WIREGUARD_PEER_NETWORK`, rejects Engine `/32` reuse, rejects duplicate peer public keys, and validates desired reconcile state before mutating the listener.
    - `parseWireGuardRuntimePrefixes` rejects unsafe overlay configuration before runtime starts: Engine address must be private IPv4 `/32`, peer network must be private IPv4 `/16` through `/30`, and the Engine address must sit inside that peer network.
    - `wireGuardRuntime.ensureLinuxFirewallLocked` always creates deterministic `BOREALIS-WG-INPUT` and `BOREALIS-WG-FWD` chains. No environment flag disables those firewall chains.
    - Engine-side WireGuard firewall chains drop invalid packets, accept only established/related return traffic, drop new agent-originated host ingress over the tunnel, drop agent-to-agent forwarding, and drop agent-originated forwarding toward other networks.
    - Go `internal/wireguardcontrol` rejects arbitrary privileged commands. It only accepts expected WireGuard listener, peer, interface, route, and Borealis firewall command shapes under resolved service-local config and secret paths; symlink escapes are rejected.
    - K3s `traefik-edge` is a root pod exception for host-network low-port binding and strict ACME renewal. It uses UID `0` with the Borealis runtime group and only `NET_BIND_SERVICE` plus `DAC_OVERRIDE`; it does not run with privileged mode.
    - K3s `wireguard-tunnel` is a root pod exception for WireGuard interface setup. It uses UID `0` with the Borealis runtime group, host networking, `/dev/net/tun`, `NET_ADMIN`, `NET_RAW`, and `no-new-privileges`; it does not run with full privileged mode.
    - WireGuard key and listener config files are `0640 borealis-engine:borealis-engine` so the API backend can write them and the root-but-capability-dropped tunnel pod can read them through its runtime group.
    - `Engine/Deploy/runtime.env` and `Engine/Deploy/compose.env` are `0640 root:borealis-engine` because deploy renders K3s runtime Secrets from them and some pods consume projected values. They are not world-readable.
    - `deviceAllowsRemoteAccess` blocks non-active device status for `/api/agent/vpn/ensure`, `/api/agent/vpn/ready`, `/api/agent/vnc/ensure`, `/api/tunnel/connect`, `/api/tunnel/status`, `/api/tunnel/active`, `/api/remote-ops/session`, worker-backed process/quick-run/maintenance dispatch, and scheduled target materialization.
    - Site-worker socket authentication rejects quarantined, revoked, and decommissioned devices before registering host-service sockets or file-transfer agent sessions.
    - Site workers run behind per-worker ClusterIP Services. K3s-backed workers use fixed `borealis-operator` pod/service templates, bind remote-operation ports on pod networking, and are routed by Traefik through scheduler-managed dynamic files. They run as the `borealis-engine` runtime owner with `no-new-privileges`, dropped capabilities, read-only root filesystem, memory-backed `/tmp`, memory/CPU caps, read-only API config/secrets, no Traefik config mount, and no `/var/run/docker.sock` mount.

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

    - Engine focused Go tests: `cd Data/Engine/Containers/api-backend && /opt/Borealis/Dependencies/Go/go1.25.12/bin/go test ./cmd/api-backend`.
    - Agent focused Go tests: `cd Data/Agent && /opt/Borealis/Dependencies/Go/go1.22.12/bin/go test ./internal/roles/wireguard_tunnel`.
    - Control socket tests: `cd Data/Engine/Containers/api-backend && /opt/Borealis/Dependencies/Go/go1.25.12/bin/go test ./internal/wireguardcontrol`.
    - Engine remote-access domain wrapper: `BOREALIS_ENGINE_TEST_PYTHON=/opt/Borealis/.cache/codex-engine-tests/bin/python3 ./Engine_Unit_Tests.sh --domain remote-access`.
    - Runtime network validation still requires a disposable deployed Engine with at least two enrolled agents to prove packet-level agent-to-agent, agent-to-internal, and quarantine/revocation denial.

    ### Where to update docs when security changes

    - Update this page for security posture, trust model, containment, token, WireGuard, bootstrap, auth, and code-signing changes.
    - Update [Engine Runtime](../Reference/Core%20Runtimes/engine-runtime.md) when container, service, runtime path, or deployment boundary changes.
    - Update [Docker Stack Breakdown](../Reference/Core%20Runtimes/Stack_Breakdown.md) when K3s workloads, retired Compose state, capabilities, mounts, sockets, or host networking assumptions change.
    - Update [API Reference](../Reference/Data%20and%20Schema/api-reference.md) if security-related endpoints are added or changed.
    - Update [SBOM](SBOM.md) if security work adds, removes, vendors, or downloads third-party software.
