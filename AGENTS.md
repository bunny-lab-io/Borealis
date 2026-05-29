Rules of Engagement with Developer:
Respond like smart caveman. Cut all filler, keep technical substance.
- Drop articles (a, an, the), filler (just, really, basically, actually).
- Drop pleasantries (sure, certainly, happy to).
- No hedging. Fragments fine. Short synonyms.
- Technical terms stay exact. Code blocks unchanged.
- Pattern: [thing] [action] [reason]. [next step].

Use this file as entrypoint for Codex instructions. Full knowledgebase lives under `Docs/`, with navigation and documentation rules in `Docs/index.md`.

## Where to Read
- Start at `Docs/index.md`.
- Use index table of contents to find domain documentation, testing guidance, runtime docs, API docs, and operation runbooks.
- Follow domain docs found through index. Where docs overlap, domain page wins. `Detailed Codex Breakdown` admonitions inside each page are authoritative agent guidance.

## Documentation Authoring Style
- Write operator-facing docs like `Docs/Engine/deploying-the-engine.md`: short opening explanation, clear requirements, normal path first, then first-run checks or verification.
- Do not add a visible `Purpose` section. Put the plain-language purpose directly under the page title so the page starts quickly.
- Keep visible sections friendly and task-focused. Explain what an operator should do, what they should expect, and what can go wrong.
- Keep implementation detail out of the operator path. API endpoints, related documentation, source paths, database tables, implementation notes, debug flow, and Codex-only reasoning belong inside a final `??? example "Detailed Codex Breakdown"` section.
- Structure `Detailed Codex Breakdown` sections consistently:
  - `### API endpoints` when endpoint details matter.
  - `### Related documentation` for cross-links and reading order.
  - `### Source map`, `### Runtime behavior`, `### Debug flow`, or similarly precise headings for dense implementation detail.
- Use contextual admonitions for optional or advanced material:
  - `!!! tip` for beginner guidance or recommended choices.
  - `!!! warning` for destructive, risky, or environment-sensitive actions.
  - `!!! info` for short operational context that helps without requiring code knowledge.
  - `??? note` for optional advanced commands, alternate install paths, and deeper configuration.
  - `??? example "Detailed Codex Breakdown"` for Codex-only details and hidden reference material.
- Use tabs for profile choices and sizing tables when options are mutually exclusive. Use collapsed notes for branch installs, dev paths, alternate commands, and advanced recovery.
- Add short comments inside code blocks when commands need context. Prefer one complete, copyable command path before listing variants.
- Keep screenshots on `Docs/screenshots.md` by default. Landing pages may carry one high-signal screenshot; topic pages should stay screenshot-free unless the operator intentionally adds one.
- Do not manually maintain page navigation in `zensical.toml`. Zensical auto-discovers Markdown folders and pages from `Docs/`; place pages in the right folder and use `index.md` for folder landing pages.

## Interacting with the Codebase
- When making changes to the codebase, do not attempt to build code via npm or vite from staging source under `Data/Agent`, `Data/Engine`, or `Data/Engine/Containers/*/data`; changes of that nature need to take place in runtime folders, and it is best to defer to the operator / developer to re-deploy the agent or engine to detect errors with page formatting, etc.

## Unit Testing
- For codebase changes, use `Docs/index.md` to find unit testing guidance before choosing validation.
- Use `Engine_Unit_Tests.sh`, `Data/Agent/Unit_Tests/Agent_Unit_Tests.sh`, and `Data/Agent/Unit_Tests/Agent_Unit_Tests.ps1` as the unit test entrypoints.
- Use documented domain flags while iterating, then run full affected Engine or Agent lane before handoff when practical.

## Database Work
- For any code change, migration, troubleshooting step, or implementation that reads from, writes to, or otherwise interacts with PostgreSQL, use `Docs/index.md` to find database reference first.
- Follow database connection-lifecycle guidance: do minimum SQL work needed, release connection immediately, and perform payload shaping, crypto, target expansion, and integration lookups only after DB connection has returned to pool.

## UI / AG Grid
- Use `Docs/index.md` to find UI, MagicUI, AG Grid, toast notification, and route migration guidance.
- Visual example: `Data/Engine/Containers/webui-frontend/data/web-interface/src/DevTools/Page_Style_Template.jsx` (reference only - no business logic). Use it to mirror layout, spacing, and selection column behavior.

## Technical Debt Logging
- If you add a patchy workaround, non-standard build step, or dev/prod behavior divergence, create or update a GitHub issue with the `Technical Debt` label.

## SBOM Maintenance
- Keep `Docs/Reference/SBOM.md` updated whenever Borealis adds, removes, vendors, or downloads third-party software for the Engine or Agent.
- Record each dependency with its software name, license identifier or license name, and a hyperlink to the governing license text.
- Keep the inventory split into Engine and Agent sections so licensing reviews remain runtime-specific.
- When scanning for new software, check bootstrap/runtime scripts as well as manifests under `Data/Engine/` and `Data/Agent/`.
