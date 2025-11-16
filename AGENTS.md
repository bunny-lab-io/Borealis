# Borealis Codex Engagement Index

Use this file as the entrypoint for Codex instructions. Domain-specific guidance lives in `/Docs/Codex` so we can scale without bloating this page.

## Where to Read
- Agent: `Docs/Codex/BOREALIS_AGENT.md` (runtime paths, logging, security, roles, platform parity, Ansible status).
- Engine: `Docs/Codex/BOREALIS_ENGINE.md` (migration tracker, architecture, logging, security/API parity, platform parity, Ansible state).
- Shared: `Docs/Codex/SHARED.md` with UI guidance at `Docs/Codex/USER_INTERFACE.md`.

Precedence: follow domain docs first; fall back to Shared when there is overlap. If domain and Shared disagree, domain wins.

## UI / AG Grid
- MagicUI styling language and AG Grid rules are consolidated in `Docs/Codex/USER_INTERFACE.md`.
- Visual example: `Data/Engine/web-interface/src/Admin/Page_Template.jsx` (reference only—no business logic). Use it to mirror layout, spacing, and selection column behavior.