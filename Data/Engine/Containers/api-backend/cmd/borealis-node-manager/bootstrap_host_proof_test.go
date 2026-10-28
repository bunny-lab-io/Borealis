package main

import (
	"context"
	"errors"
	"testing"
)

func TestBootstrapPreparedHostComposition(t *testing.T) {
	for _, mode := range []string{"valid", "repository", "files", "dependencies", "kernel", "firewall", "late files", "late repository", "authority", "cancel", "nil observer", "invalid expected"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			calls := map[string]int{}
			observer := func(name string) func(context.Context) error {
				return func(context.Context) error {
					calls[name]++
					if name == mode || mode == "late "+name && calls[name] == 3 {
						return errors.New("private proof details")
					}
					if mode == "cancel" {
						cancel()
					}
					return nil
				}
			}
			o := bootstrapPreparedHostObservers{observer("repository"), observer("files"), observer("dependencies"), observer("kernel"), observer("firewall")}
			expected := bootstrapHostFilesFixture()
			if mode == "nil observer" {
				o.kernel = nil
			}
			if mode == "invalid expected" {
				expected.Target.Generation = 0
			}
			checks := 0
			check := func(context.Context) error {
				checks++
				if mode == "authority" && checks == 22 {
					return errors.New("private authority details")
				}
				return nil
			}
			err := proveBootstrapPreparedHostWith(ctx, expected, check, o)
			if (err == nil) != (mode == "valid") || err != nil && err != errBootstrapSource {
				t.Fatal("partial host proof succeeded or leaked diagnostics", err)
			}
			if mode == "valid" && (calls["repository"] != 3 || calls["files"] != 3 || calls["dependencies"] != 2 || calls["kernel"] != 2 || calls["firewall"] != 2 || checks != 22) {
				t.Fatal("full host proof not repeated/bracketed", calls, checks)
			}
			if (mode == "nil observer" || mode == "invalid expected") && len(calls) != 0 {
				t.Fatal("invalid input reached native observer")
			}
		})
	}
}
