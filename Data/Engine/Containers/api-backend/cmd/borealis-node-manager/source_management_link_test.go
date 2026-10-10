package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

const sourceManagementFixture = `[{"ifindex":2,"ifname":"ens18","flags":["BROADCAST","MULTICAST","UP","LOWER_UP"],"operstate":"UP","link_type":"ether","address":"02:00:00:00:00:01","broadcast":"ff:ff:ff:ff:ff:ff","addr_info":[{"family":"inet","local":"192.168.90.20","prefixlen":24,"scope":"global","valid_life_time":4294967295,"preferred_life_time":4294967295}]}]`

func TestSourceManagementLinkNamespaceAndCancellation(t *testing.T) {
	for _, mode := range []string{"success", "namespace unavailable", "namespace changed", "namespace zero", "namespace too large", "read failure", "cancel", "invalid address"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			calls, reads := 0, 0
			namespace := func() (uint64, error) {
				calls++
				if mode == "namespace unavailable" {
					return 0, errors.New("private namespace error")
				}
				if mode == "namespace zero" {
					return 0, nil
				}
				if mode == "namespace too large" {
					return 1 << 53, nil
				}
				if mode == "namespace changed" && calls > 1 {
					return 1235, nil
				}
				return 1234, nil
			}
			read := func(context.Context) ([]byte, error) {
				reads++
				if mode == "read failure" {
					return nil, errors.New("private command error")
				}
				if mode == "cancel" {
					cancel()
				}
				return []byte(sourceManagementFixture), nil
			}
			address := "192.168.90.20"
			if mode == "invalid address" {
				address = "$(id)"
			}
			got, err := observeSourceManagementLink(ctx, address, namespace, read)
			if mode == "success" {
				if err != nil || !got.MatchesAddress(address) || calls != 2 || reads != 1 {
					t.Fatal("incomplete link scope")
				}
			} else if err != clusterbootstrap.ErrPreparationConfig || got != (clusterbootstrap.ManagementLink{}) {
				t.Fatal("unsafe link scope")
			}
			if mode == "invalid address" && (calls != 0 || reads != 0) {
				t.Fatal("invalid input reached host")
			}
		})
	}
}

func TestSourceManagementLinkCommandBoundsAndJoinedCancellation(t *testing.T) {
	t.Setenv("BOREALIS_LINK_TEST_PRIVATE", "must-not-inherit")
	for _, mode := range []string{"success", "failed exit", "oversize", "empty", "timeout", "cancel", "already cancelled"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			binary, pidPath := filepath.Join(root, "ip-fixture"), filepath.Join(root, "pid")
			script := fmt.Sprintf(`#!/usr/bin/python3 -I
import os, sys, time
assert sys.argv[1:] == ["-j", "-d", "-4", "address", "show"]
assert os.environ.get("PATH") == "/usr/sbin:/usr/bin:/sbin:/bin"
assert os.environ.get("LC_ALL") == "C"
assert "BOREALIS_LINK_TEST_PRIVATE" not in os.environ
assert sys.stdin.buffer.read(1) == b""
with open(%q, "w") as f:
    f.write(str(os.getpid()))
mode = %q
if mode in ("timeout", "cancel"):
    time.sleep(30)
if mode == "oversize":
    sys.stdout.write("s" * 131073)
elif mode == "success" or mode == "failed exit":
    print(%q)
if mode == "failed exit":
    sys.stderr.write("private host diagnostic")
    sys.exit(1)
`, pidPath, mode, sourceManagementFixture)
			if err := os.WriteFile(binary, []byte(script), 0700); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if mode == "already cancelled" {
				cancel()
			}
			type result struct {
				raw []byte
				err error
			}
			done := make(chan result, 1)
			started := time.Now()
			go func() {
				raw, err := sourceLinkCommand(ctx, binary, "-j", "-d", "-4", "address", "show")
				done <- result{raw, err}
			}()
			if mode == "cancel" {
				deadline := time.Now().Add(2 * time.Second)
				for {
					if raw, err := os.ReadFile(pidPath); err == nil && len(raw) > 0 {
						break
					}
					if time.Now().After(deadline) {
						cancel()
						<-done
						t.Fatal("fixture never started")
					}
					time.Sleep(5 * time.Millisecond)
				}
				cancel()
			}
			out := <-done
			if mode == "success" {
				if out.err != nil || strings.TrimSpace(string(out.raw)) != sourceManagementFixture {
					t.Fatalf("fixed command: %v", out.err)
				}
			} else if out.err != clusterbootstrap.ErrPreparationConfig || out.raw != nil {
				t.Fatal("private/partial command output escaped")
			}
			if time.Since(started) > 4*time.Second {
				t.Fatal("command exceeded bound")
			}
			if mode == "timeout" || mode == "cancel" {
				raw, err := os.ReadFile(pidPath)
				pid, parseErr := strconv.Atoi(string(raw))
				if err != nil || parseErr != nil || pid <= 0 || syscall.Kill(pid, 0) != syscall.ESRCH {
					t.Fatal("child not killed and joined")
				}
			}
			if mode == "already cancelled" {
				if _, err := os.Stat(pidPath); !os.IsNotExist(err) {
					t.Fatal("cancelled call started command")
				}
			}
		})
	}
}
