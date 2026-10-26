package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBootstrapRuntimeAccountCreationIsFixedAndFenced(t *testing.T) {
	for _, mode := range []string{"fresh", "existing", "group only", "user only", "initial fence", "group failure", "user failure", "group drift", "user drift", "final drift", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			lock, err := os.CreateTemp(t.TempDir(), "lock")
			if err != nil {
				t.Fatal(err)
			}
			defer lock.Close()
			state := bootstrapRuntimeAccount{}
			if mode == "existing" {
				state = bootstrapRuntimeAccount{true, true, "/usr/sbin/nologin"}
			}
			if mode == "group only" {
				state.Group = true
			}
			if mode == "user only" {
				state.User = true
				state.Shell = "/usr/sbin/nologin"
			}
			if mode == "cancel" {
				cancel()
			}
			var writes []bool
			reads := 0
			err = prepareBootstrapRuntimeIdentityWith(ctx, lock, func(context.Context) error {
				if mode == "initial fence" {
					return errors.New("private authority")
				}
				return nil
			},
				func(context.Context) (bootstrapRuntimeAccount, error) {
					reads++
					current := state
					if mode == "final drift" && reads == 4 {
						current.Shell = "/bin/false"
					}
					return current, nil
				},
				func(_ context.Context, got *os.File, user bool) error {
					if got != lock {
						t.Fatal("inherited lock changed")
					}
					writes = append(writes, user)
					if user {
						if !state.Group || state.User {
							t.Fatal("user creation order invalid")
						}
						if mode == "user failure" {
							return errors.New("private useradd")
						}
						state.User = true
						state.Shell = "/usr/sbin/nologin"
						if mode == "user drift" {
							state.Group = false
						}
					} else {
						if state.Group {
							t.Fatal("existing group modified")
						}
						if mode == "group failure" {
							return errors.New("private groupadd")
						}
						state.Group = true
						if mode == "group drift" {
							state.User = true
							state.Shell = "/usr/sbin/nologin"
						}
					}
					return nil
				})
			valid := mode == "fresh" || mode == "existing" || mode == "group only" || mode == "user only"
			if (err == nil) != valid {
				t.Fatal("unexpected account creation result", err)
			}
			if (mode == "existing" || mode == "initial fence" || mode == "cancel") && len(writes) != 0 {
				t.Fatal("unexpected account mutation")
			}
			if mode == "fresh" && (len(writes) != 2 || writes[0] || !writes[1]) {
				t.Fatal("group/user order changed")
			}
		})
	}
}

func TestBootstrapRuntimeAccountExactCustody(t *testing.T) {
	passwd := "root:x:0:0:root:/root:/bin/bash\nborealis-engine:x:64646:64646::/nonexistent:/usr/sbin/nologin\n"
	groups := "root:x:0:\nborealis-engine:x:64646:\n"
	for _, mode := range []string{"complete", "absent", "group only", "wrong UID", "wrong GID", "UID collision", "GID collision", "primary group member", "duplicate user", "duplicate group", "login shell", "home", "group member", "extra group", "UID alias", "GID alias", "malformed"} {
		t.Run(mode, func(t *testing.T) {
			p, g := passwd, groups
			switch mode {
			case "absent":
				p = "root:x:0:0:root:/root:/bin/bash\n"
				g = "root:x:0:\n"
			case "group only":
				p = "root:x:0:0:root:/root:/bin/bash\n"
			case "wrong UID":
				p = strings.Replace(p, ":64646:64646:", ":64645:64646:", 1)
			case "wrong GID":
				p = strings.Replace(p, ":64646:64646:", ":64646:64645:", 1)
			case "UID collision":
				p = strings.Replace(p, "borealis-engine:", "foreign:", 1)
			case "GID collision":
				g = strings.Replace(g, "borealis-engine:", "foreign:", 1)
			case "primary group member":
				p += "foreign:x:1001:64646::/nonexistent:/bin/false\n"
			case "duplicate user":
				p += p
			case "duplicate group":
				g += g
			case "login shell":
				p = strings.Replace(p, "/usr/sbin/nologin", "/bin/bash", 1)
			case "home":
				p = strings.Replace(p, "/nonexistent", "/home/foreign", 1)
			case "group member":
				g = strings.Replace(g, "64646:\n", "64646:foreign\n", 1)
			case "extra group":
				g += "docker:x:999:borealis-engine\n"
			case "UID alias":
				p = strings.Replace(p, ":64646:64646:", ":064646:64646:", 1)
			case "GID alias":
				g = strings.Replace(g, ":64646:", ":064646:", 1)
			case "malformed":
				p += "unexpected\n"
			}
			got, err := parseBootstrapRuntimeAccount([]byte(p), []byte(g))
			valid := mode == "complete" || mode == "absent" || mode == "group only"
			if (err == nil) != valid {
				t.Fatal("invalid account evidence", err)
			}
			if valid && (got.User != (mode == "complete") || got.Group != (mode != "absent")) {
				t.Fatal("wrong account projection")
			}
		})
	}
}

func TestBootstrapRuntimeAccountNSS(t *testing.T) {
	for _, text := range []string{"passwd: files systemd\ngroup: files systemd\n", "# local\npasswd: files\ngroup: files # local\nhosts: files dns\n"} {
		if validateBootstrapAccountNSS([]byte(text)) != nil {
			t.Fatal("supported NSS rejected")
		}
	}
	for _, text := range []string{"passwd: files systemd\n", "passwd: sss files\ngroup: files\n", "passwd: files [SUCCESS=return]\ngroup: files\n", "passwd: files\npasswd: files\ngroup: files\n", "passwd: files\ngroup: files ldap\n", "passwd: files\ngroup: files\n\x00"} {
		if validateBootstrapAccountNSS([]byte(text)) == nil {
			t.Fatal("unsupported NSS accepted")
		}
	}
}

func TestBootstrapRuntimeAccountProcesses(t *testing.T) {
	for _, mode := range []string{"absent", "real", "effective", "saved", "filesystem", "missing UID", "duplicate UID", "malformed", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			if err := os.Mkdir(filepath.Join(root, "123"), 0700); err != nil {
				t.Fatal(err)
			}
			ids := []string{"1000", "1000", "1000", "1000"}
			switch mode {
			case "real":
				ids[0] = "64646"
			case "effective":
				ids[1] = "64646"
			case "saved":
				ids[2] = "64646"
			case "filesystem":
				ids[3] = "64646"
			case "malformed":
				ids[3] = "unknown"
			}
			status := "Name:\tfixture\nUid:\t" + strings.Join(ids, "\t") + "\n"
			if mode == "missing UID" {
				status = "Name:\tfixture\n"
			}
			if mode == "duplicate UID" {
				status += "Uid:\t1000 1000 1000 1000\n"
			}
			if err := os.WriteFile(filepath.Join(root, "123", "status"), []byte(status), 0600); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if mode == "cancel" {
				cancel()
			}
			err := bootstrapRuntimeUIDAbsent(ctx, root, actionRuntimeID)
			if (err == nil) != (mode == "absent") {
				t.Fatal("live or unknown UID accepted", err)
			}
		})
	}
}
