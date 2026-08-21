//go:build windows

package main

import "testing"

func TestBorealisOwnedServiceProcessRequiresExactServiceOwnership(t *testing.T) {
	installDir := `C:\Borealis`
	cases := []struct {
		name        string
		serviceName string
		pathName    string
		want        bool
	}{
		{"agent", "BorealisAgent", `"C:\Borealis\Agent.exe" --service`, true},
		{"agent outside install", "BorealisAgent", `"C:\Temp\Agent.exe" --service`, false},
		{"managed UltraVNC", "BorealisAgentUltraVNC", `"C:\Program Files\UltraVNC\winvnc.exe" -service`, true},
		{"unrelated UltraVNC service", "UltraVNC", `"C:\Program Files\UltraVNC\winvnc.exe" -service`, false},
		{"managed WireGuard", "WireGuardTunnel$Borealis", `"C:\Program Files\WireGuard\wireguard.exe" /tunnelservice`, true},
		{"unrelated process", "BorealisAgentUltraVNC", `"C:\Windows\System32\powershell.exe"`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isBorealisOwnedServiceProcess(tc.serviceName, tc.pathName, installDir); got != tc.want {
				t.Fatalf("isBorealisOwnedServiceProcess(%q, %q)=%t want %t", tc.serviceName, tc.pathName, got, tc.want)
			}
		})
	}
}
