# Overview

Use this section before changing PostgreSQL access, API contracts, integration behavior, or third-party dependency inventory.

## Guides

- [Database Reference](db-reference.md) - schema ownership, relationships, connection lifecycle, and table-level behavior.
- [API Reference](api-reference.md) - Engine API endpoints grouped by domain.
- [Integrations](integrations.md) - external service behavior and integration defaults.
- [SBOM](../SBOM.md) - Engine and Agent third-party software inventory.

## Change Rules

- Keep DB work short while connection is checked out.
- Shape payloads, decrypt/encrypt values, expand targets, and resolve integrations after returning DB connection to pool.
- Update API and domain docs together when endpoint behavior changes.
