# Competitive Feature Gap Analysis

## Purpose
Turn the provided RMM competitor CSV into a Borealis roadmap analysis that is grounded in current Borealis repo truth, not aspirational parity.

## Scope and Rules
- Competitor source: user-provided CSV matrix covering 18 RMM vendors, marked `Last Updated = 2025/10/23`.
- Borealis source of truth: `Docs/`, `README.md`, and targeted repo searches under `Data/Engine/` and `Data/Agent/`.
- `Shipped` means clearly implemented, documented, and operator-facing today.
- `Partial but still a gap` means Borealis has adjacent primitives, hidden plumbing, or legacy code, but not a clean supported feature on the main path.
- `Absent` means no shipped/operator-facing implementation was found.
- Ignored rows: company metadata, staffing, pricing, trials, implementation costs, and other non-product comparisons.
- Lens: roadmap priorities for the current Borealis single-node MSP/production target, not a pure procurement scorecard.

## Quick Conclusion
Borealis already has real strengths in automation, remote access, technician tooling, product security, directory-backed authentication, site-scoped RBAC, and Windows software management. The biggest roadmap gaps are the features MSPs use every day to replace incumbent RMM stacks:

1. Patch management
2. Integrations and ecosystem depth
3. Technician background tooling
4. Platform breadth
5. Reporting, branding, and MSP packaging
6. Enterprise assurance/compliance extras

## Competitor Pressure Snapshot
The matrix suggests these gaps are not edge-case asks. They show up repeatedly across the 18-vendor comparison set.

| Category | Representative competitor rows | Matrix pressure |
| --- | --- | --- |
| Patch management | `Windows`, `Windows Build/Feature Updates`, `3rd Party Applications`, `WUA monitor` | Windows patching is `18/18`; feature updates are `13/18`; third-party patching is `16/18`; WUA-style monitoring is `9/18`. |
| Technician tooling | `Processes`, `Event Viewer`, `File Manager`, `Uninstall Apps`, `Screenshot/View`, `Chat`, `Startup Management` | Processes are `16/18`; Event Viewer `15/18`; File Manager `14/18`; Uninstall `14/18`; Screenshot `12/18`; Chat `12/18`; Startup `11/18`. |
| Platform/admin breadth | `macOS`, `Linux`, `iOS`, `Android`, `Mobile App`, `SNMP is Agent` | macOS support is effectively universal in the matrix; mobile admin apps show up in `12/18`; iOS and Android endpoint support each show up in `7/18`; SNMP/network-device coverage shows up in `7/18` plus partial entries. |
| MSP ecosystem | `Built Into THEIR BRAND of PSA`, `ConnectWise Manage`, `Datto Autotask`, `HaloPSA/ITSM`, `IT Glue`, `Bitdefender`, `Veeam` | Built-in PSA appears in `11/18`; ConnectWise Manage `13/18`; Autotask `14/18`; Halo `9/18` plus one partial; IT Glue `10/18`; Bitdefender `15/18`; Veeam `10/18`. |
| Reporting and packaging | `Monitoring reports daily`, `Executive Summary reports`, `Can you brand reports?`, `Client/End User White Label Helpdesk` | Daily reporting is `17/18`; executive summaries `16/18`; branded reports `17/18`; white-label helpdesk `11/18`. |
| Security extras | `SAML/SSO`, `IP Allow list for management`, `Log Access/SIEM Integration`, `Bug Bounty` | SAML/SSO appears in `8/18`; management IP allowlisting `11/18`; SIEM/log export `4/18`; bug bounty `9/18`. |

## Borealis Classification by Domain

### Remote Access and Technician Tooling
| CSV row or capability cluster | Borealis status | Evidence | Notes |
| --- | --- | --- | --- |
| Remote shell / technician command line | Shipped | `Docs/vpn-and-remote-access.md`, `Docs/Reference/Core Runtimes/agent-runtime.md`, `Docs/Reference/Data and Schema/api-reference.md` | Borealis ships WireGuard-backed remote shell plus SYSTEM/current-user script execution. |
| Remote desktop | Shipped | `Docs/vpn-and-remote-access.md`, `Docs/architecture-overview.md` | Same-origin Apache Guacamole VNC is a real product surface. |
| PowerShell / script execution | Shipped | `Docs/assemblies.md`, `Docs/Reference/Core Runtimes/agent-runtime.md`, `README.md` | Borealis supports quick jobs, scheduled jobs, and signed PowerShell/Batch/Bash execution. |
| Service inventory and service control | Shipped | `Docs/device-management.md`, `Docs/Reference/Data and Schema/api-reference.md` | Device APIs expose cached services plus start/stop/restart actions. |
| Installed software inventory, uninstall, and software override governance | Shipped | `Docs/device-management.md`, `Docs/Reference/Data and Schema/api-reference.md`, `Docs/Software Management/adding-software-to-icon-overrides.md`, `Docs/Software Management/adding-software-to-uninstall-overrides.md`, `Docs/Software Management/adding-software-to-uninstall-blocklist.md` | Borealis now has a first-class Installed Software surface with row-level uninstall, global icon overrides, global uninstall overrides, uninstall block/unblock, on-demand `Query Software Changes`, and uninstall progress/history in Activity History. |
| Processes | Shipped | `Docs/device-management.md`, `Docs/Reference/Core Runtimes/agent-runtime.md`, `Docs/Reference/Data and Schema/api-reference.md`, `Data/Engine/Containers/api-backend/data/services/API/devices/processes.py`, `Data/Agent/Roles/role_system_process_management.py`, `Data/Engine/Containers/webui-frontend/data/web-interface/src/Devices/Tabs/Process_Management.jsx` | Borealis now has a Device Summary `Processes` tab with live process snapshots, CPU/memory/disk/network metadata, owner and command-line columns, parent/child grouping, system-process filtering, terminated-process visibility, copy actions, and operator-triggered `End Task`. |
| Screenshot / quick visual capture | Deferred | `Docs/Reference/Core Runtimes/agent-runtime.md` | Legacy node screenshot support is retired from the Go Agent migration scope; revisit as a new product feature if needed. |
| Macro / UI automation | Deferred | `Docs/Reference/Core Runtimes/agent-runtime.md` | Legacy macro automation is retired from the Go Agent migration scope; revisit as a new product feature if needed. |
| Event Viewer | Absent | `Docs/device-management.md`, `Docs/Reference/Data and Schema/api-reference.md` | No event-log/Event Viewer APIs or documented UI surface were found. |
| File Manager / file transfer | Shipped | `Docs/device-management.md`, `Docs/Reference/Data and Schema/api-reference.md`, `Docs/ui-and-notifications.md` | Borealis now ships a first-class File Management tab with lazy remote browse, file and folder upload, file and folder download, copy/cut/paste, duplicate handling, cancelable transfers, and lightweight inline text editing. |
| Local user and group management | Absent | `Docs/device-management.md`, `Docs/Reference/Data and Schema/api-reference.md` | No dedicated device account-management feature was found. |
| Startup management | Absent | `Docs/device-management.md`, `Docs/Reference/Data and Schema/api-reference.md` | No startup-item management APIs or UI were found. |
| Technician/end-user chat | Absent | `Docs/ui-and-notifications.md`, `Docs/Reference/Data and Schema/api-reference.md` | Borealis has operator toast notifications, not remote chat. |

### Automation and Policy Management
| CSV row or capability cluster | Borealis status | Evidence | Notes |
| --- | --- | --- | --- |
| Run scripts / script editor / quick jobs | Shipped | `Docs/assemblies.md`, `Docs/scheduled-jobs.md`, `README.md` | Borealis is strong here. |
| Workflow editor and execution | Shipped | `Docs/flow-editor-and-nodes.md`, `Docs/assemblies.md` | This is a differentiator, not a gap. |
| Watchdogs, preview, incident queue, remediation | Shipped | `Docs/watchdogs.md`, `Docs/device-alerts.md` | Strong monitoring/remediation story. |
| Scheduled jobs with targeting and history | Shipped | `Docs/scheduled-jobs.md` | Also a differentiator. |
| Device filters and saved views | Shipped | `Docs/device-management.md` | Borealis already has robust targeting primitives. |
| System/company/endpoint policy layer | Partial but still a gap | `Docs/device-management.md`, `Docs/scheduled-jobs.md`, `Docs/watchdogs.md` | Borealis has sites, filters, jobs, and watchdog scopes, but not a named RMM policy-management layer for baseline configuration, patching, or software policy. |
| Auto-assign policies by search/filter | Partial but still a gap | `Docs/device-management.md`, `Docs/scheduled-jobs.md` | Filter-based targeting exists, but not policy auto-assignment as a first-class feature. |
| Local/domain account automation | Partial but still a gap | `Docs/Reference/Data and Schema/api-reference.md`, `Docs/scheduled-jobs.md` | Scripts and Ansible can do this indirectly, but Borealis does not productize it as account automation. |
| Software install/deploy policy | Absent | `Docs/device-management.md`, `Docs/Reference/Data and Schema/api-reference.md`, `Docs/assemblies.md` | Borealis can inventory, uninstall, and govern uninstall behavior, but it does not yet ship a first-class software deployment/catalog/policy surface for installing or enforcing software at scale. |

### Patch Management
| CSV row or capability cluster | Borealis status | Evidence | Notes |
| --- | --- | --- | --- |
| Windows patch management | Absent | `Docs/Reference/Data and Schema/api-reference.md`, `Docs/device-management.md`, `Docs/Reference/Core Runtimes/agent-runtime.md` | No patch-management endpoints, UI, or agent role were found. |
| Windows build / feature updates | Absent | `Docs/Reference/Data and Schema/api-reference.md`, `Docs/device-management.md` | No productized feature-update lane was found. |
| Third-party application patching | Absent | `Docs/Reference/Data and Schema/api-reference.md`, `Docs/assemblies.md`, `Docs/integrations.md` | Borealis can script and automate, but not through a supported third-party patch catalog or policy system. |
| macOS patching | Absent | `Docs/Reference/Data and Schema/api-reference.md`, `README.md` | No macOS patch product surface was found. |
| Linux patching | Absent | `Docs/Reference/Data and Schema/api-reference.md`, `Docs/Reference/Core Runtimes/engine-runtime.md` | Engine-side automation exists, but there is no Borealis patching product for Linux endpoints. |
| WUA monitoring / remediation | Absent | `Docs/Reference/Data and Schema/api-reference.md`, `Docs/device-management.md` | No Windows Update monitoring/remediation surface was found. |

### Software Management Delta Since The Initial Matrix Pass
- Borealis now has a real Windows software-management surface instead of just passive inventory.
- Operators can uninstall supported software directly from the Installed Software tab, track uninstall work in Activity History, and request an immediate software refresh with `Query Software Changes`.
- Operators can also self-govern global icon overrides, uninstall overrides, uninstall blocks, and uninstall unblocks directly from the WebUI, with hotloaded JSON-backed rule stores that survive Engine restaging.
- These additions reduce the old “uninstall apps” gap materially, but they do not yet close the larger patching/deployment/compliance gaps that mainstream RMM platforms bundle under software management.

### File Management Delta Since The Initial Matrix Pass
- Borealis now has a real remote file-management surface instead of requiring operators to fall back to remote shell or third-party tooling.
- Operators can browse drives/directories lazily, upload files or whole folders, download files/folders, cancel active transfers, handle duplicate upload conflicts, and perform create-folder, rename, move, delete, copy, cut, and paste actions from the File Management tab.
- Lightweight inline text editing closes another frequent technician workflow gap by allowing extension-aware edits without a download-edit-reupload loop.
- This closes the old `File Manager` gap materially, though it does not eliminate the remaining technician-tooling gaps around event logs, startup management, and local account tooling.

### Process Management Delta Since The Initial Matrix Pass
- Borealis now has a real process-management surface instead of only cached process snapshots for watchdog evaluation.
- Operators can inspect live process rows from the Device Summary `Processes` tab, including owner, CPU, memory, disk, network, command line, and parent/child relationships.
- Operators can toggle low-signal system processes, keep recently terminated processes visible, copy executable paths or command lines, and send `End Task` through the live `process_management` agent role.
- This closes the old `Processes` gap materially, though event-log tooling, startup tooling, local account tooling, screenshot capture, chat, and broader technician background tools remain gaps.

### Directory Services Delta Since The Initial Matrix Pass
- Borealis now has an operator-facing Directory Services surface for LDAP/LDAPS-backed authentication.
- Operators can configure directory providers, validate connections before enablement, trust LDAPS server certificates, and use host overrides when the Engine cannot resolve domain-controller FQDNs directly.
- Borealis can map Active Directory groups to Borealis Admin and User roles, then assign specific user-group mappings to specific Borealis sites.
- This materially narrows the old enterprise identity gap because directory-backed operators no longer require local Borealis passwords and can inherit access from AD group membership.
- The remaining identity/security gap is now more specific: Borealis still lacks SAML/OIDC SSO, management IP allowlisting, SIEM export, customer lockbox, and formal assurance artifacts.

### Platform Coverage
| CSV row or capability cluster | Borealis status | Evidence | Notes |
| --- | --- | --- | --- |
| Windows endpoint agent | Shipped | `Docs/Engine Deployment/index.md`, `Docs/Reference/Core Runtimes/agent-runtime.md`, `README.md` | Windows is the reference platform. |
| Linux endpoint agent | Partial but still a gap | `Docs/Engine Deployment/index.md`, `Docs/Reference/Core Runtimes/engine-runtime.md`, `Docs/Reference/Core Runtimes/agent-runtime.md`, `README.md` | Linux agents are script-staged, load roles, and support WireGuard VPN, remote Bash/script execution, file/folder interaction, and Engine-side Ansible reachability. Linux still lacks tray/helper UI and remote desktop, while service control, process management, and software management need validation. |
| macOS endpoint support | Absent | `README.md`, `Docs/Engine Deployment/index.md`, `Docs/Reference/Core Runtimes/agent-runtime.md` | macOS appears in UI filters and OS naming logic, but there is no documented macOS agent/runtime path. |
| iOS / Android / MDM | Absent | `Docs/index.md`, `Docs/Reference/Data and Schema/api-reference.md`, `README.md` | No mobile-device-management feature set was found. |
| Technician mobile app | Absent | `Docs/ui-and-notifications.md`, `README.md` | Borealis documents a web SPA only; no iOS/Android admin app is documented. |
| SNMP / network-device monitoring | Absent | `Docs/device-management.md`, `Docs/Reference/Data and Schema/api-reference.md` | No SNMP or probe-based network monitoring feature was found. |

### Integrations and Ecosystem
| CSV row or capability cluster | Borealis status | Evidence | Notes |
| --- | --- | --- | --- |
| Borealis REST/API surface | Shipped | `Docs/Reference/Data and Schema/api-reference.md` | Borealis has a real API. |
| GitHub integration | Shipped | `Docs/integrations.md` | Useful, but narrow. |
| PSA platform built-in or owned | Absent | `Docs/integrations.md`, `Docs/Reference/Data and Schema/api-reference.md` | No PSA/helpdesk platform was found. |
| PSA integrations: ConnectWise, Autotask, Halo, Salesforce, rev.io, etc. | Absent | `Docs/integrations.md`, `Docs/Reference/Data and Schema/api-reference.md` | The docs currently describe GitHub repo-hash integration only. |
| Backup integrations: Acronis, Veeam, MSP360, Cove, etc. | Absent | `Docs/integrations.md`, `Docs/Reference/Data and Schema/api-reference.md` | No backup connector story is documented. |
| Security integrations: Bitdefender, SentinelOne, Huntress, Webroot, etc. | Absent | `Docs/integrations.md`, `Docs/Reference/Data and Schema/api-reference.md` | No AV/EDR integration layer is documented. |
| Documentation and MSP stack integrations: IT Glue, Hudu, Passportal, ScalePad | Absent | `Docs/integrations.md`, `Docs/Reference/Data and Schema/api-reference.md` | No documentation or lifecycle integration layer is documented. |
| Workflow ecosystem: Zapier, Rewst, CloudRadial, Tier2Tickets | Absent | `Docs/integrations.md`, `Docs/Reference/Data and Schema/api-reference.md` | No integration framework for this ecosystem is documented. |

### Reporting, Branding, and MSP Packaging
| CSV row or capability cluster | Borealis status | Evidence | Notes |
| --- | --- | --- | --- |
| Device activity and run history | Shipped | `Docs/assemblies.md`, `Docs/scheduled-jobs.md`, `Docs/device-management.md` | Borealis persists activity history and job/run history. |
| Alerts and operational status surfaces | Shipped | `Docs/device-alerts.md`, `Docs/logging-and-operations.md` | Good operational visibility, but not formal reporting. |
| Monitoring reports (daily/weekly/monthly) | Absent | `Docs/Reference/Data and Schema/api-reference.md`, `README.md` | No scheduled reporting/report-export feature was found. |
| Executive summary reports | Absent | `Docs/Reference/Data and Schema/api-reference.md`, `README.md` | No executive summary/reporting feature was found. |
| Script-output reporting | Partial but still a gap | `Docs/assemblies.md`, `README.md`, `Data/Engine/Containers/webui-frontend/data/web-interface/src/nodes/Reporting/Node_Export_to_CSV.jsx` | Borealis stores outputs and has workflow export primitives, but not a report product around script-returned values. |
| Branded reports / header / domain | Absent | `Docs/ui-and-notifications.md`, `Docs/Reference/Data and Schema/api-reference.md` | Branding assets exist for Borealis itself, but no operator-facing white-label or custom-domain feature was documented. |
| White-label helpdesk / client-facing portal | Absent | `Docs/integrations.md`, `Docs/Reference/Data and Schema/api-reference.md` | No helpdesk/portal product surface was found. |

### Security Controls vs Vendor-Assurance/Compliance
| CSV row or capability cluster | Borealis status | Evidence | Notes |
| --- | --- | --- | --- |
| MFA | Shipped | `Docs/security-and-trust.md`, `Docs/Reference/Data and Schema/api-reference.md` | Borealis requires MFA by default. |
| Passkeys / modern auth | Shipped | `Docs/security-and-trust.md`, `Docs/Reference/Data and Schema/api-reference.md` | Strong modern operator auth story. |
| LDAP/LDAPS directory authentication | Shipped | `Data/Engine/Containers/api-backend/data/services/API/access_management/directory_services.py`, `Data/Engine/Containers/webui-frontend/data/web-interface/src/Access_Management/Directory_Services.jsx`, `Data/Engine/Containers/api-backend/data/database.py` | Borealis now supports directory credential providers with LDAPS certificate trust, provider testing, AD group role mapping, and site-scoped operator assignment by directory group. |
| Script/code signing | Shipped | `Docs/security-and-trust.md`, `Docs/assemblies.md` | Strong differentiator versus much of the field. |
| Aegis secret protection | Shipped | `Docs/security-and-trust.md`, `Docs/Reference/Core Runtimes/engine-runtime.md`, `README.md` | Strong differentiator. |
| Site-scoped RBAC | Shipped | `Docs/device-management.md`, `README.md`, `Data/Engine/Containers/api-backend/data/services/API/access_management/directory_services.py` | Strong multi-operator control model now extends to directory-backed user groups. |
| Customer lockbox | Absent | `Docs/security-and-trust.md`, `Docs/logging-and-operations.md` | No tenancy-support lockbox pattern is documented. |
| SAML / SSO | Absent | `Docs/security-and-trust.md`, `Docs/Reference/Data and Schema/api-reference.md` | LDAPS closes directory-backed authentication, but no SAML/OIDC web SSO flow or endpoints are documented. |
| Management IP allowlisting | Absent | `Docs/security-and-trust.md`, `Docs/vpn-and-remote-access.md` | Borealis documents WireGuard transport port allowlists, not browser/API management IP allowlists. |
| Log export / SIEM integration | Partial but still a gap | `Docs/logging-and-operations.md`, `Docs/Reference/Data and Schema/api-reference.md` | Borealis exposes log APIs and retention management, but no SIEM export/integration is documented. |
| Vendor assurance programs: VDP, bug bounty, SOC2/ISO, FedRAMP | Absent | `Docs/security-and-trust.md`, `README.md` | Product security is strong, but repo/docs do not show formal assurance-program artifacts. |

## Top Roadmap Gaps

### 1. Patch Management
- Why it ranks first:
  - It is the clearest table-stakes gap in the matrix.
  - The competitor set treats Windows patching as baseline and frequently includes feature updates and third-party patching.
  - Borealis already has the execution substrate needed to implement it: agent execution, scheduling, watchdogs, filters, RBAC, and Ansible.
- Current Borealis position:
  - Borealis can automate patching through scripts or Ansible in an ad hoc way.
  - Borealis now has a meaningful Installed Software control surface for inventory, uninstall, override governance, and immediate software refresh.
  - Borealis still does not ship patch inventory, approval workflows, maintenance windows, deployment policy, reboot orchestration, compliance reporting, or WUA-style status tracking.
- Immediate product implication:
  - Patching is the most leverage-rich way to turn Borealis from a strong automation/remote-access platform into a real incumbent RMM replacement.

### 2. Integrations and Ecosystem
- Why it ranks second:
  - Integrations are a major switching blocker for MSPs.
  - The matrix shows heavy competitor coverage across PSA, backup, AV/EDR, documentation, and adjacent ops platforms.
- Current Borealis position:
  - `Docs/integrations.md` documents GitHub repo-hash integration only.
  - Borealis currently lacks a connector framework that covers the MSP stack.
- Immediate product implication:
  - Without PSA/security/backup/documentation integrations, Borealis risks being adopted as a sidecar tool instead of the system of record.

### 3. Technician Background Tooling
- Why it ranks third:
  - These are high-frequency operator tools used during every support shift.
  - The matrix shows strong competitor coverage for event logs, file management, processes, uninstall, and related device-admin surfaces.
- Current Borealis position:
  - Borealis already has excellent remote shell, VNC, service control, and process management.
  - Borealis now also has usable software-management, file-management, and process-management surfaces with uninstall actions, override/block governance, remote browse/transfer, inline text editing, live process inspection, `End Task`, and Activity History visibility.
  - It still does not yet productize the rest of the background-control tool belt.
  - Legacy screenshot/macro roles do not close this gap because they are explicitly outside the supported runtime path.
- Immediate product implication:
  - This is the gap most likely to create daily operator friction even if Borealis wins on automation depth.

### 4. Platform Breadth
- Why it ranks fourth:
  - Windows remains the broadest tested endpoint path today, while competitors typically market broader endpoint coverage.
  - Mobile admin apps and some form of network-device/SNMP support are common enough to matter.
- Current Borealis position:
  - Windows is healthy.
  - Linux has a functioning script-staged Agent path with WireGuard, script execution, file/folder interaction, and Engine-side Ansible reachability, but still lacks tray/helper UI and remote desktop, and several management roles need Linux validation.
  - macOS is not productized.
  - iOS/Android/MDM and SNMP/network-device coverage are absent.
- Immediate product implication:
  - The platform can win in Windows-centric environments today, but broader coverage is needed before it can credibly displace cross-platform RMM incumbents.

### 5. Reporting, Branding, and MSP Packaging
- Why it ranks fifth:
  - Reporting and client-facing polish are less foundational than patching/integrations, but they matter for MSP retention and sales.
  - Competitors overwhelmingly advertise monitoring reports, executive summaries, branded reports, and white-label packaging.
- Current Borealis position:
  - Borealis has good operational history and alerting.
  - `README.md` now classifies reporting and client packaging as partial because activity history, run history, alerts, and recaps exist, but scheduled reports and branded client-facing outputs do not.
  - No scheduled reports, executive summaries, report branding, or white-label helpdesk surfaces are documented.
- Immediate product implication:
  - Borealis currently feels more like a strong operator platform than a polished MSP reporting/customer-portal platform.

### 6. Enterprise Assurance / Compliance Extras
- Why it ranks sixth:
  - These matter, but for Borealis's current target market they are usually less immediate than patching, integrations, and technician productivity.
  - LDAPS directory authentication, AD group role mapping, and directory group site assignment now remove a meaningful identity-management gap that would otherwise make this rank higher.
- Current Borealis position:
  - Borealis is strong on actual product security primitives: Aegis, MFA, passkeys, LDAPS directory authentication, AD group role mapping, directory group site assignment, code signing, scoped RBAC, short-lived tokens, and WireGuard.
  - It is still weak on the enterprise assurance/compliance layer that buyers often ask for in vendor reviews: SAML/OIDC SSO, management IP allowlisting, SIEM export, lockbox, VDP/bug bounty/compliance evidence.
- Immediate product implication:
  - This should follow the operational product gaps unless Borealis decides to target more compliance-heavy buyer segments sooner.

## Secondary Gaps
- Built-in PSA/helpdesk ownership is absent.
- Mobile admin app support is absent.
- Customer-facing branding/custom-domain controls are absent.
- Network-device/SNMP coverage is absent.
- Local/domain user-account automation is not productized.
- LDAP/LDAPS directory authentication is now shipped, but SAML/OIDC SSO and broader enterprise assurance controls remain gaps.
- Software deployment/install policy remains absent even though software uninstall and override governance now exist.
- File management and process management are now shipped, but event-log tooling, startup tooling, and local account tooling are still absent.
- Reporting/export exists in pieces, but not as a first-class reporting product.

## Current Differentiators
Borealis is not starting from zero. Several areas already compare well, and these should remain part of the product thesis:

- Workflows and visual automation:
  - `Docs/flow-editor-and-nodes.md`
  - `Docs/assemblies.md`
- Watchdogs with preview, incident tracking, and remediation:
  - `Docs/watchdogs.md`
  - `Docs/device-alerts.md`
- WireGuard-first remote shell, VNC, and Engine-side Ansible:
  - `Docs/vpn-and-remote-access.md`
  - `Docs/scheduled-jobs.md`
  - `README.md`
- Windows software inventory, uninstall, and override governance:
  - `Docs/device-management.md`
  - `Docs/Reference/Data and Schema/api-reference.md`
  - `Docs/Software Management/adding-software-to-icon-overrides.md`
  - `Docs/Software Management/adding-software-to-uninstall-overrides.md`
  - `Docs/Software Management/adding-software-to-uninstall-blocklist.md`
- Remote file browsing, transfer, and inline text editing:
  - `Docs/device-management.md`
  - `Docs/Reference/Data and Schema/api-reference.md`
  - `Docs/ui-and-notifications.md`
- Live process inspection and termination:
  - `Docs/device-management.md`
  - `Docs/Reference/Core Runtimes/agent-runtime.md`
  - `Data/Engine/Containers/api-backend/data/services/API/devices/processes.py`
  - `Data/Agent/Roles/role_system_process_management.py`
  - `Data/Engine/Containers/webui-frontend/data/web-interface/src/Devices/Tabs/Process_Management.jsx`
- Aegis, MFA, passkeys, short-lived tokens, and code signing:
  - `Docs/security-and-trust.md`
  - `Docs/Reference/Core Runtimes/engine-runtime.md`
- LDAP/LDAPS directory authentication with AD group role and site assignment:
  - `Data/Engine/Containers/api-backend/data/services/API/access_management/directory_services.py`
  - `Data/Engine/Containers/webui-frontend/data/web-interface/src/Access_Management/Directory_Services.jsx`
  - `Data/Engine/Containers/api-backend/data/database.py`
- Site-scoped RBAC and scoped targeting:
  - `Docs/device-management.md`
  - `README.md`

## Public Interface Implications
If Borealis chooses to close the top gaps, the public interface surface will likely need to grow in these directions:

### Patching
- Patch inventory/status APIs per device and per software/update class.
- Patch policy models for approval, deferral, reboot behavior, maintenance windows, and rollout targeting.
- Deployment/run history APIs for patch jobs and compliance views.
- Agent-side patch execution/reporting contracts distinct from generic script execution.

### Integrations
- Connector config/status APIs.
- Credential and secret models for third-party services.
- Sync jobs and error/reporting surfaces.
- Vendor-specific mapping contracts for PSA, documentation, security, backup, and automation ecosystems.

### Technician Tooling
- Device-side read/action APIs for:
  - event logs
  - local user/group management
  - startup/session tooling

### Software Management
- Patch inventory/status APIs per device and per software/update class.
- Software deployment/catalog APIs for install, update, and removal policy at scale.
- Approval, deferral, reboot-behavior, and maintenance-window models for updates.
- Fleet-wide software compliance and reporting views for outdated, blocked, overridden, and missing software.

### Reporting and MSP Packaging
- Report-generation and export APIs.
- Scheduled report delivery surfaces.
- Branding settings and template controls.
- Possible tenant/client-facing data models if white-label/helpdesk features enter scope.

## Final Read
Borealis already looks differentiated in automation, remote access, technician tooling, directory-backed access control, and security. The competitive gap is not that it lacks depth everywhere. The gap is that it still lacks several high-frequency MSP operating-system features that incumbents bundle into the same pane of glass.

If Borealis wants the fastest path toward feature-market fit, the roadmap should prioritize:
- patch management first
- integrations second
- technician background tooling third
- platform breadth fourth

That ordering best complements the strengths Borealis already has instead of trying to replace them.
