# Borealis Engine Migration Tracker

## Current Focus
- **Stage:** Stage 1 — Establish the Engine skeleton and bootstrapper
- **Active Task:** Stage 1 tasks completed; awaiting direction to proceed to Stage 2.

## Task Ledger
- [x] **Stage 1 — Establish the Engine skeleton and bootstrapper**
  - [x] Add Data/Engine/__init__.py plus service subpackages with placeholder modules and docstrings.
  - [x] Scaffold Data/Engine/server.py with the create_app(config) factory and stub service registration hooks.
  - [x] Return a shared context object containing handles such as the database path, logger, and scheduler.
  - [x] Update project tooling so the Engine runtime can be launched alongside the legacy path.
- [ ] **Stage 2 — Port configuration and dependency loading into the Engine factory**
  - [ ] Extract configuration loading logic from Data/Server/server.py into Data/Engine/config.py helpers.
  - [ ] Verify context parity between Engine and legacy startup.
  - [ ] Initialize logging to Logs/Server/server.log when Engine mode is active.
  - [ ] Document Engine launch paths and configuration requirements in module docstrings.
- [ ] **Stage 3 — Introduce API blueprints and service adapters**
  - [ ] Create domain-focused API blueprints and register_api entry point.
  - [ ] Mirror route behaviour from the legacy server via service adapters.
  - [ ] Add configuration toggles for enabling API groups incrementally.
- [ ] **Stage 4 — Build unit and smoke tests for Engine APIs**
  - [ ] Add pytest modules under Data/Engine/Unit_Tests exercising API blueprints.
  - [ ] Provide fixtures that mirror the legacy SQLite schema and seed data.
  - [ ] Assert HTTP status codes, payloads, and side effects for parity.
  - [ ] Integrate Engine API tests into CI/local workflows.
- [ ] **Stage 5 — Bridge the legacy server to Engine APIs**
  - [ ] Delegate API blueprint registration to the Engine factory from the legacy server.
  - [ ] Replace legacy API routes with Engine-provided blueprints gated by a flag.
  - [ ] Emit transitional logging when Engine handles requests.
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
