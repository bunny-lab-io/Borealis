# Agent Service Bootstrapper

`Agent_Service_Bootstrapper.exe` is the native Windows service shim used by remote onboarding.

It owns only remote launch mechanics:

- report real Windows service state to SCM
- acquire the host-wide Borealis onboarding mutex
- clean `C:\Borealis\Temp\Onboarding`
- download `Agent.ps1`
- write onboarding `state.json` and `events.jsonl`
- run `Agent.ps1`
- mirror Agent step markers into `events.jsonl` so Engine polling can show live task progress
- enforce timeout and kill the child process tree

`Agent.ps1` remains the single Windows Agent install/update brain.

Build from Linux only with native Go 1.22+:

```bash
./build-agent-service-bootstrapper.sh
```

The script uses `go` on `PATH` when available. If Go is missing or too old, it installs official native Go under `Dependencies/Go/` and uses that. It does not use Docker, Podman, or any containerized compiler.

`Agent_Service_Bootstrapper.exe` is a committed release artifact. Engine images copy this top-level prebuilt EXE and do not compile Go during image build.

Rebuild only when this bootstrapper source changes or when developer release process intentionally refreshes the EXE. Operators and end users should never need to build it.

```bash
docker build -f Data/Engine/Containers/job-scheduler/Dockerfile .
```

Output:

```text
Data/Agent/Bootstrapper/Agent_Service_Bootstrapper.exe
```
