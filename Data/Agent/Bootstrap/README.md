# Agent.exe Bootstrap

`Agent.exe` is the native Windows bootstrap entrypoint for Borealis Agent setup, repair, update, remote onboarding, and uninstall.

Target path:

```text
C:\Borealis\Agent.exe
```

Normal arguments:

```text
Agent.exe --server-url https://borealis.example.com --site-enrollment-code E925-...
Agent.exe
Agent.exe -uninstall
```

No subcommands or aliases are supported.

Default mode auto-detects install health:

- no install: install silently
- broken install: repair or redeploy
- healthy install: check for updates
- missing required bootstrap input: prompt only when interactive, fail clearly when remote/noninteractive

Logs:

```text
C:\Borealis\Agent\Logs\bootstrap.log
```

Remote onboarding stages `Agent.exe`, `agent-payload.zip`, `agent-payload-manifest.json`, and `bootstrapper-config.json`, then starts `C:\Borealis\Agent.exe` through a transient remote execution method.

Build from Linux with native Go 1.22+:

```bash
./build-agent.sh
```

The build script uses `go` on `PATH` when available. If Go is missing or too old, it installs official native Go under `Dependencies/Go/`. It does not use Docker, Podman, or containerized compilers.

Committed output:

```text
Data/Agent/Bootstrap/Agent.exe
```
