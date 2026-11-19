package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"context"
	"errors"
	"strings"
	"testing"
)

func TestBootstrapTargetPackagesBeforeSourceMutation(t *testing.T) {
	for _, mode := range []string{"fresh", "missing fresh approval", "legacy recovery", "post-install authority", "inventory drift", "selection drift", "plan drift", "malformed inventory", "reader failure", "missing reader", "cancelled", "cancel during read"} {
		t.Run(mode, func(t *testing.T) {
			expected := bootstrapHostFilesFixture()
			approved, err := clusterbootstrap.SelectHostPackages([]byte(bootstrapHostPackageBefore))
			if err != nil {
				t.Fatal(err)
			}
			expected.TargetPackages = &approved
			action := "stage_source"
			switch mode {
			case "missing fresh approval", "legacy recovery":
				expected.TargetPackages = nil
			case "selection drift":
				approved.Missing = approved.Missing[1:]
			case "plan drift":
				approved.PlanSHA256 = strings.Repeat("a", 64)
			}
			if mode == "legacy recovery" {
				action = "reconcile_source"
			} else if mode == "post-install authority" {
				action = "prepare_prejoin_host"
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if mode == "cancelled" {
				cancel()
			}
			reads := 0
			read := func(_ context.Context, probe string) ([]byte, error) {
				reads++
				if probe != "packages" {
					t.Fatal("unexpected probe")
				}
				switch mode {
				case "inventory drift":
					return []byte(strings.Replace(bootstrapHostPackageBefore, "2.8.3", "2.8.4", 1)), nil
				case "post-install authority", "malformed inventory":
					return []byte("changed after owned install"), nil
				case "reader failure":
					return nil, errors.New("private read failure")
				case "cancel during read":
					cancel()
				}
				return []byte(bootstrapHostPackageBefore), nil
			}
			if mode == "missing reader" {
				read = nil
			}
			err = verifyBootstrapTargetPackages(ctx, expected, action, read)
			valid := mode == "fresh" || mode == "legacy recovery" || mode == "post-install authority"
			if (err == nil) != valid || err != nil && err != errBootstrapSource {
				t.Fatal("unsafe fresh source or broken recovery boundary", err)
			}
			if (mode == "legacy recovery" || mode == "post-install authority" || mode == "missing fresh approval" || mode == "plan drift" || mode == "cancelled") && reads != 0 {
				t.Fatal("invalid or post-effect source check entered inventory reader")
			}
		})
	}
}
