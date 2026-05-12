# Agent.exe Bootstrap

`Agent.exe` is the native Windows bootstrap entrypoint for Borealis Agent setup, repair, update, remote onboarding, and uninstall.
When built with a non-`main` `repo_ref`, update checks are pinned to that GitHub branch head and refuse the Engine update manifest path so feature-branch Agents cannot drift back to a different runtime build.

Target path:

```text
C:\Borealis\Agent.exe
```

Normal arguments:

```text
Agent.exe --server-url https://borealis.example.com --site-enrollment-code E925-...
Agent.exe
Agent.exe -verbose
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

Default console and onboarding stdout show high-level task summaries plus warnings/errors. Detailed command output and trace lines are hidden unless `-verbose` or `--verbose` is passed.

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
