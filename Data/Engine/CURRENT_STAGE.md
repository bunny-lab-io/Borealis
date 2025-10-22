# Borealis Engine Migration Progress

[COMPLETED] 1. Stabilize Engine foundation (baseline already in repo)
- 1.1 Confirm `Data/Engine/bootstrapper.py` launches the placeholder Flask app without side effects.
- 1.2 Document environment variables/settings expected by the Engine to keep parity with legacy defaults.
- 1.3 Verify Engine logging produces `Logs/Server/engine.log` entries alongside the legacy server.

[COMPLETED] 2. Introduce configuration & dependency wiring
- 2.1 Create `config/environment.py` loaders mirroring legacy defaults (TLS paths, feature flags).
- 2.2 Add settings dataclasses for Flask, Socket.IO, and DB paths; inject them via `server.py`.
- 2.3 Commit once the Engine can start with equivalent config but no real routes.

[COMPLETED] 3. Copy Flask application scaffolding
- 3.1 Port proxy/CORS/static setup from `Data/Server/server.py` into Engine `server.py` using dependency injection.
- 3.2 Stub out blueprint/Socket.IO registration hooks that mirror names from legacy code (no logic yet).
- 3.3 Smoke-test app startup via `python Data/Engine/bootstrapper.py` (or Flask CLI) to ensure no regressions.

[COMPLETED] 4. Establish SQLite infrastructure
- 4.1 Copy `_db_conn` logic into `repositories/sqlite/connection.py`, parameterized by database path (`<root>/database.db`).
- 4.2 Port migration helpers into `repositories/sqlite/migrations.py`; expose an `apply_all()` callable.
- 4.3 Wire migrations to run during Engine bootstrap (behind a flag) and confirm tables initialize in a sandbox DB.
- 4.4 Commit once DB connection + migrations succeed independently of legacy server.

[COMPLETED] 5. Extract authentication/enrollment domain surface
- 5.1 Define immutable dataclasses in `domain/device_auth.py`, `domain/device_enrollment.py` for tokens, GUIDs, approvals.
- 5.2 Map legacy error codes/enums into domain exceptions or enums in the same modules.
- 5.3 Commit after unit tests (or doctests) validate dataclass invariants.

[COMPLETED] 6. Port authentication services
- 6.1 Copy `DeviceAuthManager` logic into `services/auth/device_auth_service.py`, refactoring to use new repositories and domain types.
- 6.2 Create `builders/device_auth.py` to assemble `DeviceAuthContext` from headers/DPoP proof.
- 6.3 Mirror refresh token issuance into `services/auth/token_service.py`; use `builders/device_enrollment.py` for payload assembly.
- 6.4 Commit once services pass targeted unit tests and integrate with placeholder repositories.

[COMPLETED] 7. Implement SQLite repositories
- 7.1 Introduce `repositories/sqlite/device_repository.py`, `token_repository.py`, `enrollment_repository.py` using copied SQL.
- 7.2 Write integration tests exercising CRUD against a temporary SQLite file.
- 7.3 Commit when repositories provide the required ports used by services.

[COMPLETED] 8. Recreate HTTP interfaces
- 8.1 Port health/enrollment/token blueprints into `interfaces/http/<feature>/routes.py`, calling Engine services only.
- 8.2 Ensure request validation occurs via builders; response schemas stay aligned with legacy JSON.
- 8.3 Register blueprints through Engine `server.py`; confirm endpoints respond via manual or automated tests.
- 8.4 Commit after each major blueprint migration for clear milestones.

[COMPLETED] 9. Rebuild WebSocket interfaces
- 9.1 Establish feature-scoped modules (e.g., `interfaces/ws/agents/events.py`) and copy event handlers.
- 9.2 Replace global state with repository/service calls where feasible; otherwise encapsulate in Engine-managed caches.
- 9.3 Validate namespace registration with Socket.IO test clients before committing.

[COMPLETED] 10. Scheduler & job management
- 10.1 Port scheduler core into `services/jobs/scheduler_service.py`; wrap job state persistence via new repositories.
- 10.2 Implement `builders/job_fabricator.py` for manifest assembly; ensure immutability and validation.
- 10.3 Expose HTTP orchestration via `interfaces/http/job_management.py` and WS notifications via dedicated modules.
- 10.4 Commit after scheduler can run a no-op job loop independently.

[COMPLETED] 11. GitHub integration
- 11.1 Copy GitHub helper logic into `integrations/github/artifact_provider.py` with proper configuration injection.
- 11.2 Provide repository/service hooks for fetching artifacts or repo heads; add resilience logging.
- 11.3 Commit after integration tests (or mocked unit tests) confirm API workflows.

[IN PROGRESS] 12. Final parity verification
- 12.1 Follow the staging playbook in `Data/Engine/STAGING_GUIDE.md` to stand up the Engine end-to-end and exercise enrollment,
  token refresh, agent connections, GitHub integration, and scheduler flows.
- 12.2 Record any divergences in the staging guide’s table and address them with follow-up commits before cut-over.
- 12.3 Once parity is confirmed, coordinate entrypoint switching (point deployment at `Data/Engine/bootstrapper.py`) and plan
  the legacy server deprecation.
- Supporting documentation and unit tests live in `Data/Engine/README.md`, `Data/Engine/STAGING_GUIDE.md`, and
  `Data/Engine/tests/` to guide the remaining staging work.
