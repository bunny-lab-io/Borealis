Rules of Engagement with Developer:
Respond like smart caveman. Cut all filler, keep technical substance.
- Drop articles (a, an, the), filler (just, really, basically, actually).
- Drop pleasantries (sure, certainly, happy to).
- No hedging. Fragments fine. Short synonyms.
- Technical terms stay exact. Code blocks unchanged.
- Pattern: [thing] [action] [reason]. [next step].

Use this file as the entrypoint for Codex instructions. The full knowledgebase now lives under `Docs/` and includes both human-facing guidance and **Codex Agent** sections with deep, agent-ready details.

## Where to Read
- Start here: `Docs/index.md` (table of contents and documentation rules).
- Orientation: `Docs/getting-started.md` (bootstrap and launch flow) and `Docs/architecture-overview.md` (system shape and component relationships).
- Agent runtime: `Docs/agent-runtime.md` (runtime paths, logging, security, roles, platform parity, Ansible status).
- Engine runtime: `Docs/engine-runtime.md` (architecture, logging, security/API parity, platform parity, migration notes).
- Database reference: `Docs/db-reference.md` (schema ownership, connection-lifecycle rules, troubleshooting queries, and DB hygiene guardrails for implementation work).
- Automation and execution: `Docs/assemblies.md`, `Docs/flow-editor-and-nodes.md`, `Docs/scheduled-jobs.md`, and `Docs/watchdogs.md`.
- Device operations: `Docs/device-management.md`, `Docs/device-alerts.md`, `Docs/logging-and-operations.md`, and `Docs/vpn-and-remote-access.md`.
- UI and notifications: `Docs/ui-and-notifications.md` (MagicUI styling, AG Grid rules, toast notifications, UI handoffs) and `Docs/migrating-pages-to-react-router.md` (route migration runbook).
- API and integrations: `Docs/api-reference.md` and `Docs/integrations.md`.
- Security and trust: `Docs/security-and-trust.md` (enrollment, tokens, code signing, sequence diagrams).
- Technical debt: `Docs/technical-debt.md` (patches, workarounds, dev/prod mismatches).

Precedence: follow domain docs first; where overlap exists, the domain page wins. The Codex Agent sections inside each page are the authoritative agent guidance.

## Interacting with the Codebase
- When making changes to the codebase, do not attempt to build code via npm or vite from the staging folder located under either `Data/Agent` or `Data/Engine`, changes of that nature need to take place in the runtime folders, and it is best to defer to the operator / developer to re-deploy the agent or engine to detect errors with page formatting, etc.

## Database Work
- For any code change, migration, troubleshooting step, or implementation that reads from, writes to, or otherwise interacts with PostgreSQL, read `Docs/db-reference.md` first.
- Follow the connection-lifecycle guidance in `Docs/db-reference.md`: do the minimum SQL work needed, release the connection immediately, and perform payload shaping, crypto, target expansion, and integration lookups only after the DB connection has been returned to the pool.

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
