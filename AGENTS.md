# Borealis Codex Engagement Index

Use this file as the entrypoint for Codex instructions. The full knowledgebase now lives under `Docs/` and includes both human-facing guidance and **Codex Agent** sections with deep, agent-ready details. There is no separate Codex folder anymore.

## Where to Read
- Start here: `Docs/index.md` (table of contents and documentation rules).
- Agent runtime: `Docs/agent-runtime.md` (runtime paths, logging, security, roles, platform parity, Ansible status).
- Engine runtime: `Docs/engine-runtime.md` (architecture, logging, security/API parity, platform parity, migration notes).
- UI and notifications: `Docs/ui-and-notifications.md` (MagicUI styling, AG Grid rules, toast notifications, UI handoffs).
- VPN and remote access: `Docs/vpn-and-remote-access.md` (WireGuard tunnels, remote shell, RDP, troubleshooting context).
- Security and trust: `Docs/security-and-trust.md` (enrollment, tokens, code signing, sequence diagrams).

Precedence: follow domain docs first; where overlap exists, the domain page wins. The Codex Agent sections inside each page are the authoritative agent guidance.

## UI / AG Grid
- MagicUI styling language and AG Grid rules are consolidated in `Docs/ui-and-notifications.md`.
- Visual example: `Data/Engine/web-interface/src/Admin/Page_Template.jsx` (reference only - no business logic). Use it to mirror layout, spacing, and selection column behavior.
