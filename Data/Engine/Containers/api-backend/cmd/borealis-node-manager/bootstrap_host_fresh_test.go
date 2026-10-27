package main

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

func TestBootstrapFreshHostPreflight(t *testing.T) {
	for _, mode := range []string{"valid", "existing runtime", "generated file", "pending file", "unit exists", "late authority", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			root, read := bootstrapDockerFixture(t)
			expected := bootstrapHostFilesFixture()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			lost := false
			units := 0
			check := func(context.Context) error {
				if lost {
					return errBootstrapSource
				}
				return nil
			}
			switch mode {
			case "existing runtime":
				bootstrapDockerWrite(t, root, "usr/bin/docker", "foreign")
			case "generated file":
				bootstrapDockerWrite(t, root, "usr/local/lib/borealis/k3s-api-firewall.sh", "foreign")
			case "pending file":
				files, _ := bootstrapPrejoinHostFiles(expected)
				file := files[3]
				bootstrapDockerWrite(t, root, "etc/systemd/system/"+file.pending, "partial")
			}
			got, err := observeBootstrapFreshHost(ctx, root, expected, check, read, func(_ context.Context, unit string) error {
				units++
				if unit != "borealis-k3s-api-firewall.service" {
					t.Fatal("wrong unit")
				}
				if mode == "unit exists" {
					return errors.New("unit present")
				}
				if mode == "late authority" {
					lost = true
				}
				if mode == "cancel" {
					cancel()
				}
				return nil
			})
			if (err == nil) != (mode == "valid") {
				t.Fatal("incorrect fresh-host decision", err)
			}
			if mode == "valid" && (got.suite != "noble" || len(got.probes) == 0 || units != 1) {
				t.Fatal("fresh observation missing")
			}
			if err != nil && !reflect.DeepEqual(got, bootstrapDockerHost{}) {
				t.Fatal("failed preflight returned usable observation")
			}
		})
	}
}
