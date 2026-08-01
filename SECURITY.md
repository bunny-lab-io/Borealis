# Security Policy

## Supported Versions

Borealis uses calendar-based versioning in one of the following formats:

```text
YYYY.MM.REVISION
YYYY.MM.REVISION.HOTFIX
```

For example, version `2026.07.5` represents:

* `2026` — Release year
* `07` — Release month
* `5` — Fifth published update during July 2026

A hotfix release adds a fourth version component. For example, version `2026.07.5.1` represents:

* `2026` — Release year
* `07` — Release month
* `5` — Fifth published update during July 2026
* `1` — First hotfix published for release `2026.07.5`

The revision number identifies the order of normal releases within a calendar month. The hotfix number identifies the order of corrective releases based on a specific normal release.

These numbers do not independently indicate whether a release contains major, minor, or patch-level changes.

Borealis follows a rolling support model. Security fixes are developed for the latest published release, including any hotfixes published for that release.

Long-term support branches are not currently maintained, and security fixes are not routinely backported to earlier releases.

| Version                             | Supported                  |
| ----------------------------------- | -------------------------- |
| Latest published release or hotfix  | :white_check_mark:         |
| `main` branch and unreleased builds | :warning: Development only |
| Earlier published releases          | :x:                        |
| Forks or locally modified builds    | :x:                        |

For example, if `2026.07.5.1` is the latest published version, then `2026.07.5` and all earlier releases are no longer considered supported.

Users should upgrade to the latest available Borealis release before reporting a vulnerability unless:

* The vulnerability prevents the upgrade.
* The vulnerability affects the upgrade process.
* The vulnerability is no longer reproducible after upgrading.
* Testing the latest release would create an unreasonable risk to a production environment.

Reports involving an earlier release may still be useful. Maintainers may ask the reporter to verify whether the issue remains reproducible in the latest published release.

## Security Releases and Hotfixes

Accepted vulnerabilities may be corrected through either a normal Borealis release or a hotfix release.

A normal release increments the revision number for the current calendar month:

```text
2026.07.5 -> 2026.07.6
```

When a new calendar month begins, the monthly revision sequence may restart:

```text
2026.07.5 -> 2026.08.1
```

A narrowly scoped correction to an existing release may be published as a hotfix:

```text
2026.07.5 -> 2026.07.5.1
```

Additional hotfixes based on the same normal release increment the hotfix number:

```text
2026.07.5.1 -> 2026.07.5.2
```

A later normal release supersedes hotfixes associated with the previous revision:

```text
2026.07.5.2 -> 2026.07.6
```

Hotfixes are intended for focused corrective changes such as:

* Security corrections.
* Critical defect remediation.
* Deployment-blocking fixes.
* Data-integrity corrections.
* Other urgent changes that should not wait for the next normal release.

The version number identifies when and in what sequence a release was published. It does not independently communicate vulnerability severity, compatibility impact, or the scope of changes included in the release.

## Reporting a Vulnerability

Do not report suspected security vulnerabilities through a public GitHub issue, discussion, pull request, commit comment, or other public channel.

Use GitHub's private vulnerability reporting feature:

1. Open the repository's [Security page](https://github.com/bunny-lab-io/Borealis/security).
2. Select **Report a vulnerability**.
3. Complete the private security advisory form.
4. Include enough information for the issue to be reproduced and evaluated.

If private vulnerability reporting is unavailable, open a public issue requesting a private security contact method.

Do not include vulnerability details, proof-of-concept material, credentials, tokens, logs, screenshots, or other sensitive information in the public issue.

## Information to Include

A useful vulnerability report should include:

* The complete affected Borealis version, such as `2026.07.5` or `2026.07.5.1`.
* The affected Git commit when testing an unreleased build.
* The affected component, service, API, Agent role, or deployment mode.
* A clear description of the suspected vulnerability.
* The potential confidentiality, integrity, or availability impact.
* Any permissions, access, configuration, or prerequisites required to reproduce it.
* Clear and repeatable reproduction steps.
* A minimal proof of concept when appropriate.
* Relevant requests, responses, logs, or screenshots.
* Whether the issue affects a default deployment or requires a non-default configuration.
* Whether the issue was reproduced against the latest published release.
* Any known mitigations or suggested corrections.

Remove or redact secrets and personal information before submitting evidence.

Do not include:

* Live passwords or recovery codes.
* Access or refresh tokens.
* Private keys or certificate private-key material.
* Aegis Cipher values or derived key material.
* WireGuard private keys.
* Code-signing private keys.
* Reusable machine credentials.
* Personally identifiable information.
* Customer or production data.
* Information obtained from systems you were not authorized to test.

## Response Process

Borealis is maintained on a best-effort basis.

Maintainers aim to:

* Acknowledge a complete vulnerability report within 7 calendar days.
* Provide an initial assessment within 14 calendar days.
* Provide a status update at least every 14 calendar days while an accepted report remains unresolved.

Response and remediation times may vary depending on:

* Severity and potential impact.
* Reproducibility.
* Technical complexity.
* Required architectural changes.
* Availability of a safe mitigation.
* Maintainer availability.
* Whether an upstream dependency must first publish a correction.

After reviewing a report, maintainers will normally:

1. Confirm whether the report can be reproduced.
2. Determine whether the behavior represents a Borealis vulnerability.
3. Identify the affected versions, components, and trust boundaries.
4. Evaluate severity, exploitability, required access, and potential impact.
5. Develop a correction, mitigation, or documentation update.
6. Prepare a normal release or hotfix when appropriate.
7. Coordinate public disclosure after a correction or mitigation is available.

If a report is declined, maintainers will attempt to explain whether it is:

* Expected behavior.
* An unsupported configuration.
* A duplicate of an existing report.
* Not sufficiently reproducible.
* A third-party issue without a demonstrated Borealis impact.
* Outside the documented Borealis security model.
* A general hardening recommendation without an exploitable condition.

## Coordinated Disclosure

Reporters are asked to keep vulnerability details confidential until one of the following occurs:

* A corrected release or hotfix has been published.
* A documented mitigation has been published.
* Maintainers and the reporter agree upon a disclosure date.
* Maintainers determine that the report does not represent a Borealis vulnerability.

Accepted vulnerabilities may be documented through a GitHub Security Advisory.

A CVE may be requested when the severity, affected population, and security impact justify one.

Reporters may request:

* Public individual credit.
* Public organizational credit.
* Anonymous recognition.
* No public recognition.

Reporter information will not be published without permission.

## Security Scope

Security reports are particularly relevant when they affect the following areas.

### Authentication and Operator Access

* Password authentication.
* Multi-factor authentication.
* WebAuthn passkeys.
* Session creation, validation, revocation, or expiration.
* Account recovery and administrative bootstrap processes.
* Role-based access control.
* Site scoping.
* Unauthorized privilege escalation.

### Aegis Cipher and Protected Data

* Aegis Cipher setup, unlock, reset, or key derivation.
* Exposure of protected credentials or authentication material.
* Improper plaintext storage or logging of protected data.
* Backup and restore encryption.
* Unauthorized decryption or reuse of protected secrets.

### Agent Identity and Enrollment

* Agent enrollment or approval bypass.
* Device identity impersonation.
* Ed25519 identity validation.
* Access-token or refresh-token trust.
* Token revocation or version invalidation.
* Device quarantine, revocation, or decommissioning bypass.
* Unauthorized device reassignment or site movement.

### Automation and Script Integrity

* Engine code-signing behavior.
* Agent signature verification.
* Unsigned or tampered script execution.
* Unauthorized assembly, workflow, or job execution.
* Scheduled job or quick-job authorization.
* Watchdog remediation authorization.
* Ansible credential or target isolation.
* Cross-site automation access.

### Remote Operations

* Remote shell authorization.
* Remote desktop authorization.
* File-management operations.
* Process-management operations.
* Service-management operations.
* Software-management operations.
* Unauthorized execution against quarantined, revoked, or out-of-scope devices.

### WireGuard and Network Containment

* WireGuard peer identity.
* Duplicate or overly broad peer routes.
* Agent-to-agent lateral access.
* Agent-initiated access to protected Engine services.
* Route-validation bypass.
* Firewall-rule bypass.
* Unauthorized tunnel use.
* WireGuard reachability being treated as authorization without the required Borealis access checks.

### Engine APIs and Service Boundaries

* REST API authorization.
* WebSocket or Socket.IO authorization.
* Internal service exposure.
* Cross-service authentication.
* Site-worker isolation.
* Borealis operator API authorization.
* Container or Kubernetes workload boundaries.
* Unauthorized access to PostgreSQL or internal control services.
* Secret disclosure through application logs, API responses, or runtime files.

### Containment and Audit

* Quarantine or revocation bypass.
* Audit-log tampering.
* Authentication anomaly logging.
* Enrollment approval logging.
* Script-signature failure handling.
* Unauthorized deletion or alteration of security-relevant activity history.

## Generally Out of Scope

The following findings are generally outside the scope of this policy unless they demonstrate a specific and reproducible Borealis security impact:

* Vulnerabilities that exist only in an unsupported Borealis release.
* Vulnerabilities introduced exclusively by a fork or local source modification.
* Deployment misconfigurations that contradict documented security requirements.
* Missing optional hardening controls without an exploitable condition.
* Automated scanner output without manual validation or demonstrated impact.
* Version disclosure without a demonstrated attack path.
* Missing security headers without a demonstrated exploit.
* Self-XSS requiring a user to intentionally execute code in their own browser console.
* Social engineering or phishing.
* Physical access attacks.
* Credential theft that does not involve a Borealis vulnerability.
* Denial-of-service or stress testing that could disrupt production systems.
* Reports requiring access to systems the reporter was not authorized to test.
* Vulnerabilities in third-party products without a demonstrated Borealis attack path.
* Vulnerabilities that require an already fully compromised Borealis Engine host, unless Borealis violates a documented containment or trust boundary after the compromise.
* General feature requests or recommendations that do not describe a vulnerability.

Third-party dependency vulnerabilities may still be reported when Borealis:

* Uses the vulnerable functionality.
* Exposes the vulnerable component through a Borealis deployment.
* Creates a meaningful attack path that would not otherwise exist.
* Requires a dependency update or Borealis-specific mitigation.

## Testing Guidelines

Security testing must only be performed against systems that you own or have explicit authorization to test.

When investigating a suspected vulnerability:

* Use a dedicated test environment whenever possible.
* Avoid testing against production systems.
* Access only the minimum information required to demonstrate the issue.
* Do not modify or delete data unnecessarily.
* Do not establish persistent access.
* Do not deploy malware, ransomware, cryptominers, or destructive payloads.
* Do not pivot into unrelated systems or networks.
* Do not access another user's information beyond what is required to demonstrate the vulnerability.
* Do not perform denial-of-service, load, or resource-exhaustion testing without prior authorization.
* Stop testing if sensitive data is encountered unexpectedly.
* Securely delete any unintentionally collected sensitive information.
* Provide maintainers a reasonable opportunity to investigate and publish a correction before disclosure.

Testing against public Borealis infrastructure, documentation services, repositories, or systems operated by Bunny Lab does not imply authorization to test those systems.

## Operational Security Responsibilities

Borealis is self-hosted. Operators are responsible for the security of their deployment environment.

This includes protecting:

* The Borealis Engine host.
* Host operating-system access.
* SSH access.
* Kubernetes and container administration.
* Network firewall rules.
* DNS records.
* TLS certificates.
* Reverse-proxy exposure.
* Backup files.
* Administrative accounts.
* Identity-provider or directory-service configuration.
* Account provisioning and deprovisioning.
* Physical and hypervisor access.
* External monitoring and incident-response processes.

The Borealis Engine host is the security root of a deployment. A complete compromise of the Engine host may expose:

* Application data.
* Protected credentials.
* Signing keys.
* WireGuard material.
* Agent trust data.
* Operator authentication material.
* Backup data.
* Remote-management capabilities.

Borealis provides application-level controls intended to reduce risk and enforce trust boundaries. It cannot preserve application secrets against an attacker with unrestricted access to the Engine host, its storage, or its runtime.

## Security Architecture

This policy describes vulnerability reporting, supported versions, and disclosure expectations. It does not replace the Borealis security model, deployment requirements, or operational guidance.

Review the following resources for additional information:

* [Borealis Documentation](https://bunny-lab-io.github.io/Borealis/)
* [Borealis Security Whitepaper](https://bunny-lab-io.github.io/Borealis/Reference/security-whitepaper/)

The Security Whitepaper documents areas including:

* Engine and Agent trust boundaries.
* Agent identity and enrollment.
* Token trust and revocation.
* Aegis Cipher.
* MFA and WebAuthn.
* Role-based access control.
* Site scoping.
* Script signing and verification.
* WireGuard peer isolation.
* Device containment.
* Runtime and container boundaries.
* Kubernetes workload security.
* Audit and recovery expectations.

## Security Advisories

Security advisories and corrected releases will be published through the repository when appropriate.

Users should monitor:

* The repository's [Security page](https://github.com/bunny-lab-io/Borealis/security).
* Published Borealis releases and hotfixes.
* Release notes.
* The Borealis documentation.

Because only the latest published Borealis release or hotfix is supported, users should apply new versions promptly after reviewing their deployment-specific compatibility and backup requirements.

## Bug Bounty

Borealis does not currently operate a paid bug bounty program.

Submitting a vulnerability report does not create an entitlement to:

* Payment.
* Compensation.
* Employment.
* Merchandise.
* Services.
* Any other reward.

Public recognition may be provided for accepted reports when requested by the reporter and approved by the maintainers.
