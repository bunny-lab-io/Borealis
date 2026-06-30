//go:build !windows

package registrymanagement

import "context"

func registryRoots(_ context.Context) (map[string]any, error) {
	return unsupportedRegistryPlatform()
}

func registryChildren(_ context.Context, _ registryPath) (map[string]any, error) {
	return unsupportedRegistryPlatform()
}

func registryCreateKey(_ context.Context, _ registryPath, _ string) (map[string]any, error) {
	return unsupportedRegistryPlatform()
}

func registryRenameKey(_ context.Context, _ registryPath, _ string) (map[string]any, error) {
	return unsupportedRegistryPlatform()
}

func registryDeleteKey(_ context.Context, _ registryPath, _ bool) (map[string]any, error) {
	return unsupportedRegistryPlatform()
}

func registrySetValue(_ context.Context, _ registryPath, _ registryValueInput, _ bool) (map[string]any, error) {
	return unsupportedRegistryPlatform()
}

func registryDeleteValue(_ context.Context, _ registryPath, _ string) (map[string]any, error) {
	return unsupportedRegistryPlatform()
}
