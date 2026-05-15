//go:build !windows

package vnc

func collectDisplayTopology() []map[string]any {
	return []map[string]any{}
}
