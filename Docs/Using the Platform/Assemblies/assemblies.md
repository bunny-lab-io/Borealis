# Assemblies

Assemblies are reusable automation records. Borealis uses them for quick jobs, scheduled jobs, workflow nodes, watchdog remediation, and Engine-side playbook execution.

<figure class="bo-screenshot">
  <img src="../../Reference/images/repo_screenshots/Assembly_List.png" alt="Borealis Assembly List" loading="lazy">
  <figcaption>Assembly List is the catalog for script assemblies, playbooks, and workflow-backed automation.</figcaption>
</figure>

## Use Assembly Catalog

Open `Automation > Assemblies`.

The catalog contains:

- Script assemblies.
- Workflow assemblies.
- Ansible playbook assemblies.
- Official Aurora assemblies.
- Local user-created assemblies.

## Understand Domains

- `Aurora` / official assemblies come from the Aurora content repository.
- `User` assemblies are created locally on your Engine.
- Protected domains require admin/dev-mode workflows before direct edits.

## Choose Assembly Type

- Use [Scripts](scripts.md) for PowerShell, Batch, or Bash payloads.
- Use [Workflows](workflows.md) for visual graph automation.
- Use [Ansible Playbooks](ansible-playbooks.md) for Engine-side SSH or WinRM automation.

## Update Official Content

Use official update actions when you want the Engine to sync newer Aurora catalog content. Local user assemblies remain local.

??? example "Detailed Codex Breakdown"

    ### API endpoints

    - `GET /api/assemblies` - list assemblies.
    - `GET /api/assemblies/<assembly_guid>` - assembly details.
    - `POST /api/assemblies` - create assembly.
    - `PUT /api/assemblies/<assembly_guid>` - update assembly.
    - `DELETE /api/assemblies/<assembly_guid>` - delete assembly.
    - `POST /api/assemblies/<assembly_guid>/clone` - clone assembly.
    - `POST /api/assemblies/import` - import assembly JSON.
    - `GET /api/assemblies/<assembly_guid>/export` - export assembly JSON.
    - `POST /api/assemblies/<assembly_guid>/official-update` - update one official assembly.
    - `POST /api/assemblies/official/update-all` - sync official Aurora assemblies.

    ### Related documentation

    - [Scripts](scripts.md)
    - [Workflows](workflows.md)
    - [Ansible Playbooks](ansible-playbooks.md)
    - [Scheduled Jobs](../scheduled-jobs.md)
    - [Watchdogs](../watchdogs.md)
    - [Database Reference](../../Reference/Data%20and%20Schema/db-reference.md)

    ### Source map

    - Assembly APIs: `Data/Engine/Containers/api-backend/cmd/api-backend/assemblies.go` and `assemblies_catalog.go`
    - Quick-run API: `Data/Engine/Containers/api-backend/cmd/api-backend/quick_run.go`
    - Assembly cache: `Data/Engine/Containers/api-backend/data/assembly_management/`
    - UI: `Data/Engine/Containers/webui-frontend/data/web-interface/src/Assemblies/`
    - Aurora cache: `Engine/Services/api-backend/cache/Aurora/`
    - Bundled official seed: `Data/Engine/Containers/api-backend/data/Official_Assemblies/`

    ### Runtime behavior

    - Assembly tables live under PostgreSQL `assemblies.*`.
    - `assembly_type` routes records to script, workflow, or Ansible paths.
    - Official rows track Aurora provenance through source path, version, and content hash.
    - Startup seeds bundled official assemblies and can sync Aurora when available.
