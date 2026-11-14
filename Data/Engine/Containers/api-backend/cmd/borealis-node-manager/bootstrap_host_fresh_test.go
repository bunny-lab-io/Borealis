package main

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestBootstrapFreshHostPreflight(t *testing.T) {
	for _, mode := range []string{"valid", "base configured", "base unpacked", "existing runtime", "generated file", "pending file", "repository scratch", "unit exists", "late authority", "cancel"} {
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
			if strings.HasPrefix(mode, "base ") {
				original := read
				read = func(ctx context.Context, probe string) ([]byte, error) {
					raw, err := original(ctx, probe)
					if probe == "packages" {
						for _, name := range bootstrapHostPackages[:7] {
							raw = append(raw, []byte(name+"\tamd64\tinstall ok installed\t1.0\n")...)
						}
						if mode == "base unpacked" {
							raw = []byte(strings.Replace(string(raw), "python3\tamd64\tinstall ok installed", "python3\tamd64\tinstall ok unpacked", 1))
						}
					}
					return raw, err
				}
			}
			switch mode {
			case "existing runtime":
				bootstrapDockerWrite(t, root, "usr/bin/docker", "foreign")
			case "generated file":
				bootstrapDockerWrite(t, root, "usr/local/lib/borealis/k3s-api-firewall.sh", "foreign")
			case "repository scratch":
				bootstrapDockerWrite(t, root, "etc/apt/keyrings/.borealis-docker-retained", "partial")
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
			if (err == nil) != (mode == "valid" || mode == "base configured") {
				t.Fatal("incorrect fresh-host decision", err)
			}
			if (mode == "valid" || mode == "base configured") && (got.suite != "noble" || len(got.probes) == 0 || units != 1) {
				t.Fatal("fresh observation missing")
			}
			if err != nil && !reflect.DeepEqual(got, bootstrapDockerHost{}) {
				t.Fatal("failed preflight returned usable observation")
			}
		})
	}
}
