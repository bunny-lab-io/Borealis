//go:build windows

package config

// Windows uses the existing tested atomic replacement primitive and synced file.
// Directory-entry persistence through sudden power loss is not qualified. Lost
// Agent evidence cannot release the Engine's unresolved operation charge.
func syncWorkerOperationsDirectory(string) error { return nil }
