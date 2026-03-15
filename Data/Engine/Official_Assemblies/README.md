# Official Assemblies Snapshot

This directory is the bundled source-of-truth snapshot for Borealis official
assemblies.

Contents:
- `manifest.json` - catalog metadata plus one entry per official assembly
- `items/<assembly_guid>.json` - exported assembly JSON documents keyed by
  `assembly_guid`

Runtime behavior:
- Engine startup loads this bundled snapshot into PostgreSQL
  `assemblies.official_assemblies`
- On-demand `Update` and `Update All` actions use the active official catalog
  manifest and update assemblies by `assembly_guid`
- The bundled snapshot only manages the official domain; it does not touch
  community or user-created assemblies

Recommended workflow:
1. Keep official assembly authoring in a dedicated repository.
2. Publish a `manifest.json` plus `items/*.json` from that repository.
3. During Borealis release/update, export the currently approved official
   catalog into this directory so fresh Engine installs ship with a useful
   default snapshot.

Generate or refresh this snapshot from PostgreSQL:

```bash
. /opt/Borealis/Engine/database.env
PYTHONPATH=/opt/Borealis /opt/Borealis/Engine/bin/python3 \
  -m Data.Engine.services.assemblies.official_catalog export-bundled \
  --database-url "$BOREALIS_DATABASE_URL" \
  --output-root /opt/Borealis/Data/Engine/Official_Assemblies \
  --repo-url https://example.com
```
