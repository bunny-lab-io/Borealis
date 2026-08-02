package main

import (
	"database/sql"
	"testing"
)

func TestDirectoryTLSConfigFailsClosedWithoutCustomCA(t *testing.T) {
	provider := directoryProviderConfig{
		Row: directoryProviderRow{
			TLSRequired: sql.NullInt64{Int64: 0, Valid: true},
		},
	}
	target := directoryConnectionTarget{
		Scheme:        "ldaps",
		Host:          "ldap.example.test",
		RequestedHost: "ldap.example.test",
		ServerURL:     "ldaps://ldap.example.test:636",
	}

	config, err := directoryTLSConfig(provider, target)
	if err != nil {
		t.Fatal(err)
	}
	if config == nil {
		t.Fatal("expected LDAPS TLS config")
	}
	if config.InsecureSkipVerify {
		t.Fatal("LDAPS config must not disable certificate verification")
	}
	if config.ServerName != "ldap.example.test" {
		t.Fatalf("unexpected server name %q", config.ServerName)
	}
}
