# Migration Prompt
You are working in the Borealis Automation Platform repo (root: <ProjectRoot>). The legacy runtime lives under Data/Server/server.py. Your objective is to introduce a new Engine runtime under Data/Engine that will progressively take over responsibilities (API first, then WebUI, then WebSocket). Execute the migration in the stages seen below (be sure to not overstep stages, we only want to work on one stage at a time, until I give approval to move onto the next stage):

Everytime you do work, you indicate the current stage you are on by writing to the file in <ProjectRoot>/Data/Engine/CODE_MIGRATION_TRACKER.md, inside of this file, you will keep an up-to-date ledger of the overall task list seen below, as well as the current stage you are on, and what task within that stage you are working on.  You will keep this file up-to-date at all times whenever you make progress, and you will reference this file whenever making changes in case you forget where you were last at in the codebase migration work.  You will never make modifications to the "# Migration Prompt" section, only the "# Borealis Engine Migration Tracker" section.

Lastly, everytime that you complete a stage, you will create a pull request named "Stage <number> - <Stage Description> Implemented" I will merge your pull request associated with that stage into the "main" branch of the codebase, then I will create a new gpt-5-codex conversation to keep teh conversation fresh and relevant, instructing the agent to work from the next stage in-line, and I expect the Codex agent to read the aforementioned <ProjectRoot>/Data/Engine/CODE_MIGRATION_TRACKER.md to understand what it has already done thus far, and what it needs to work on next.  Every time that I start the new conversation, I will instruct gpt-5-codex to read <ProjectRoot>/Data/Engine/CODE_MIGRATION_TRACKER.md to understand it's tasks to determine what to do.


# Borealis Engine Migration Tracker
## Task Ledger
- [x] **Stage 1 — Establish the Engine skeleton and bootstrapper**
  - [x] Add Data/Engine/__init__.py plus service subpackages with placeholder modules and docstrings.
  - [x] Scaffold Data/Engine/server.py with the create_app(config) factory and stub service registration hooks.
  - [x] Return a shared context object containing handles such as the database path, logger, and scheduler.
  - [x] Update project tooling so the Engine runtime can be launched alongside the legacy path.
- [x] **Stage 2 — Port configuration and dependency loading into the Engine factory**
  - [x] Extract configuration loading logic from Data/Server/server.py into Data/Engine/config.py helpers.
  - [x] Verify context parity between Engine and legacy startup.
  - [x] Initialize logging to Logs/Server/server.log when Engine mode is active.
  - [x] Document Engine launch paths and configuration requirements in module docstrings.
- [x] **Stage 3 — Introduce API blueprints and service adapters**
  - [x] Create domain-focused API blueprints and register_api entry point.
  - [x] Mirror route behaviour from the legacy server via service adapters.
  - [x] Add configuration toggles for enabling API groups incrementally.
- [x] **Stage 4 — Build unit and smoke tests for Engine APIs**
  - [x] Add pytest modules under Data/Engine/Unit_Tests exercising API blueprints.
  - [x] Provide fixtures that mirror the legacy SQLite schema and seed data.
  - [x] Assert HTTP status codes, payloads, and side effects for parity.
  - [x] Integrate Engine API tests into CI/local workflows.
- [x] **Stage 5 — Bridge the legacy server to Engine APIs**
  - [x] Delegate API blueprint registration to the Engine factory from the legacy server.
  - [x] Replace legacy API routes with Engine-provided blueprints gated by a flag.
  - [x] Emit transitional logging when Engine handles requests.
- [ ] **Stage 6 — Plan WebUI migration**
  - [ ] Move static/template handling into Data/Engine/services/WebUI.
  - [ ] Preserve TLS-aware URL generation and caching.
  - [ ] Add migration switch in the legacy server for WebUI delegation.
  - [ ] Extend tests to cover critical WebUI routes.
- [ ] **Stage 7 — Plan WebSocket migration**
  - [ ] Extract Socket.IO handlers into Data/Engine/services/WebSocket.
  - [ ] Provide register_realtime hook for the Engine factory.
  - [ ] Add integration tests or smoke checks for key events.
  - [ ] Update legacy server to consume Engine WebSocket registration.

## Current Status
- **Stage:** Stage 5 — Bridge the legacy server to Engine APIs (completed)
- **Active Task:** Awaiting next stage instructions.
