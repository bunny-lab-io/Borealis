# Borealis Codex Engagement Index

Use this file as the entrypoint for Codex instructions. The full knowledgebase now lives under `Docs/` and includes both human-facing guidance and **Codex Agent** sections with deep, agent-ready details.

## Where to Read
- Start here: `Docs/index.md` (table of contents and documentation rules).
- Agent runtime: `Docs/agent-runtime.md` (runtime paths, logging, security, roles, platform parity, Ansible status).
- Engine runtime: `Docs/engine-runtime.md` (architecture, logging, security/API parity, platform parity, migration notes).
- UI and notifications: `Docs/ui-and-notifications.md` (MagicUI styling, AG Grid rules, toast notifications, UI handoffs).
- VPN and remote access: `Docs/vpn-and-remote-access.md` (WireGuard tunnels, remote shell, VNC, troubleshooting context).
- Security and trust: `Docs/security-and-trust.md` (enrollment, tokens, code signing, sequence diagrams).
- Technical debt: `Docs/technical-debt.md` (patches, workarounds, dev/prod mismatches).

Precedence: follow domain docs first; where overlap exists, the domain page wins. The Codex Agent sections inside each page are the authoritative agent guidance.

## UI / AG Grid
- MagicUI styling language and AG Grid rules are consolidated in `Docs/ui-and-notifications.md`.
- Visual example: `Data/Engine/web-interface/src/Admin/Page_Template.jsx` (reference only - no business logic). Use it to mirror layout, spacing, and selection column behavior.

## Technical Debt Logging
- If you add a patchy workaround, non-standard build step, or dev/prod behavior divergence, log it in `Docs/technical-debt.md` using the template there.

## SBOM Maintenance
- Keep `SBOM.md` in the repo root updated whenever Borealis adds, removes, vendors, or downloads third-party software for the Engine or Agent.
- Record each dependency with its software name, license identifier or license name, and a hyperlink to the governing license text.
- Keep the inventory split into Engine and Agent sections so licensing reviews remain runtime-specific.
- When scanning for new software, check bootstrap/runtime scripts as well as manifests under `Data/Engine/` and `Data/Agent/`.
