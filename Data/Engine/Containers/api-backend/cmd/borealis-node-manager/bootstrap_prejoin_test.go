package main

import (
	"context"
	"errors"
	"testing"
)

func TestBootstrapPrejoinStableMountAndConsumerCustody(t *testing.T) {
	for _, mode := range []string{"fresh transition", "existing", "occupied fresh", "missing installed", "anchor drift", "identity drift", "identity removed", "consumer", "account drift", "mid-scan replacement", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			anchor := identityRuntimeMount{Device: 1, Inode: 2, Mount: 3}
			identity := identityRuntimeMount{Device: 1, Inode: 4, Mount: 3}
			create := mode == "fresh transition" || mode == "occupied fresh"
			present := !create || mode == "occupied fresh"
			if mode == "missing installed" {
				present = false
			}
			fail := false
			observations := 0
			o := bootstrapPrejoinObservers{
				boundary: func() error {
					if fail && mode == "account drift" {
						return errors.New("custody drift")
					}
					return nil
				},
				anchor: func() (identityRuntimeMount, error) { return anchor, nil },
				identity: func() (identityRuntimeMount, bool, error) {
					if !present {
						return identityRuntimeMount{}, false, nil
					}
					return identity, present, nil
				},
				processes: func(path string, got identityRuntimeMount) error {
					observations++
					if path != bootstrapPrejoinEngine && path != bootstrapPrejoinSecrets {
						t.Fatal("unfixed path")
					}
					if got.Device == 0 {
						t.Fatal("empty mount proof")
					}
					if fail && mode == "consumer" {
						return errors.New("consumer")
					}
					if fail && mode == "mid-scan replacement" {
						identity.Inode++
					}
					return nil
				},
			}
			check, err := newBootstrapPrejoinQuiescence(ctx, create, o)
			if mode == "occupied fresh" || mode == "missing installed" {
				if err == nil || check != nil {
					t.Fatal("wrong initial lifecycle accepted")
				}
				return
			}
			if err != nil || observations == 0 {
				t.Fatal("initial custody failed", err)
			}
			fail = true
			switch mode {
			case "fresh transition":
				present = true
			case "anchor drift":
				anchor.Inode++
			case "identity drift":
				identity.Inode++
			case "identity removed":
				present = false
			case "cancel":
				cancel()
			}
			err = check()
			if (err == nil) != (mode == "fresh transition" || mode == "existing") {
				t.Fatal("custody drift accepted", err)
			}
			if mode == "fresh transition" {
				identity.Inode++
				if check() == nil {
					t.Fatal("newly captured mount not pinned")
				}
			}
		})
	}
}
