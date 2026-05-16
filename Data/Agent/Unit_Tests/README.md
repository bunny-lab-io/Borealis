# Agent Unit Test Entry Points

This folder owns Borealis Agent test lane launchers.

- `Agent_Unit_Tests.sh` runs Go Agent tests and Windows/Linux build checks from POSIX shells.
- `Agent_Unit_Tests.ps1` runs the same lane from Windows PowerShell.
- Go unit tests remain package-local as `*_test.go` files under `Data/Agent/cmd` and `Data/Agent/internal` so they can exercise package internals without artificial exported APIs.

