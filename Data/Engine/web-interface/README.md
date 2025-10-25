# Engine Web Interface Staging

The Engine server reuses the existing Vite single-page application that still lives under
`Data/Server/WebUI`.  At runtime the launch scripts copy those assets into this directory so the
Engine can stage its own copy without disturbing the legacy server.

The repository intentionally ignores the staged files to avoid duplicating tens of thousands of
lines (and large binary assets) in source control.  If you need to refresh the Engine copy, run one
of the launch scripts (`Borealis.ps1` or `Borealis.sh`) or copy the assets manually from
`Data/Server/WebUI` into this folder.
