package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"slices"
	"strings"
	"testing"
	"time"
)

func bootstrapContainerdCheckFixture() (string, []clusterbootstrap.ContainerdImageTarget) {
	digest := "sha256:" + strings.Repeat("a", 64)
	return "REF TYPE DIGEST STATUS SIZE UNPACKED\nexample.test/image:v1 application/vnd.oci.image.manifest.v1+json " + digest + " complete (3/3) 1.0 KiB/1.0 KiB true\n",
		[]clusterbootstrap.ContainerdImageTarget{{Reference: "example.test/image:v1", Digest: digest}}
}

func TestBootstrapContainerdCheck(t *testing.T) {
	for _, mode := range []string{"valid", "command failed", "incomplete", "wrong digest", "not unpacked", "overflow", "ignored overflow", "claim lost", "pre-idle failed", "post-idle failed", "canceled"} {
		t.Run(mode, func(t *testing.T) {
			host, identity := bootstrapHostCommandLocks(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			raw, targets := bootstrapContainerdCheckFixture()
			calls, idles, aborts := 0, 0, 0
			lost := false
			check := func(context.Context) error {
				if lost {
					return errBootstrapSource
				}
				return nil
			}
			idle := func(ctx context.Context) error {
				idles++
				if ctx.Err() != nil {
					t.Fatal("cleanup canceled")
				}
				if mode == "pre-idle failed" && idles == 1 || mode == "post-idle failed" && idles == 2 {
					return errBootstrapSource
				}
				return nil
			}
			run := func(ctx context.Context, spec bootstrapHostCommandSpec, h, i, e, s *os.File, out io.Writer) error {
				calls++
				want := bootstrapContainerdCheckPlan()
				if spec.program != "/proc/self/fd/5" || spec.timeout != 2*time.Minute || !slices.Equal(spec.args, want.args) || h != host || i != identity || e != host || s != identity {
					t.Fatal("fixed command changed")
				}
				deadline, ok := ctx.Deadline()
				if !ok || time.Until(deadline) > 2*time.Minute {
					t.Fatal("command unbounded")
				}
				switch mode {
				case "command failed":
					return errBootstrapSource
				case "incomplete":
					raw = strings.Replace(raw, "complete (3/3)", "incomplete (2/3)", 1)
				case "wrong digest":
					raw = strings.Replace(raw, strings.Repeat("a", 64), strings.Repeat("b", 64), 1)
				case "not unpacked":
					raw = strings.Replace(raw, " true", " false", 1)
				case "overflow":
					_, err := io.Copy(out, strings.NewReader(strings.Repeat("x", (256<<10)+1)))
					return err
				case "ignored overflow":
					_, _ = io.WriteString(out, strings.Repeat("x", (256<<10)+1))
					return nil
				case "claim lost":
					lost = true
				case "canceled":
					cancel()
				}
				_, err := io.WriteString(out, raw)
				return err
			}
			got, err := runBootstrapContainerdCheckWith(ctx, targets, host, identity, host, identity, check, idle, run, func() { aborts++ })
			if (err == nil) != (mode == "valid") || err != nil && got != nil {
				t.Fatalf("check outcome: %v %v", got, err)
			}
			if mode == "valid" && !slices.Equal(got, targets) {
				t.Fatal("unexpected target projection")
			}
			wantCalls, wantIdles := 1, 2
			if mode == "pre-idle failed" {
				wantCalls, wantIdles = 0, 1
			}
			wantAborts := 0
			if mode == "post-idle failed" {
				wantAborts = 1
			}
			if calls != wantCalls || idles != wantIdles || aborts != wantAborts {
				t.Fatal("cleanup/replay boundary", calls, idles, aborts)
			}
		})
	}
}

// Only a Go test executable is reexecuted. No K3s binary, daemon or service.
func TestBootstrapContainerdChild(t *testing.T) {
	if !slices.Contains(os.Args, "borealis-containerd-fixture") {
		return
	}
	if os.Args[0] != "ctr" || os.Getenv("LC_ALL") != "C" || os.Getenv("CONTAINERD_ADDRESS") != "" || os.Getenv("CONTAINERD_NAMESPACE") != "" || os.Getenv("HOME") != "" {
		t.Fatal("argv/environment drift")
	}
	TestBootstrapHostCommandChild(t)
	executable := os.NewFile(5, "held-executable")
	socket := os.NewFile(6, "held-socket")
	defer executable.Close()
	defer socket.Close()
	a, err := executable.Stat()
	b, other := os.Stat("/proc/self/exe")
	if err != nil || other != nil || !os.SameFile(a, b) {
		t.Fatal("wrong executable descriptor")
	}
	info, err := socket.Stat()
	if err != nil || info.Mode()&os.ModeSocket == 0 {
		t.Fatal("wrong socket descriptor")
	}
	conn, err := net.DialTimeout("unix", "/proc/self/fd/6", time.Second)
	if err != nil {
		t.Fatal("held endpoint unavailable", err)
	}
	conn.Close()
	raw, _ := bootstrapContainerdCheckFixture()
	fmt.Print(raw)
	os.Exit(0)
}

func TestBootstrapContainerdRealChild(t *testing.T) {
	for _, cancelChild := range []bool{false, true} {
		host, identity := bootstrapHostCommandLocks(t)
		root, _ := bootstrapContainerdSocketFixture(t)
		path, err := captureSourceSocketPath(context.Background(), root, bootstrapContainerdSocket)
		if err != nil {
			t.Fatal(err)
		}
		defer path.close()
		socket := os.NewFile(uintptr(path.fds[len(path.fds)-1]), "fixture-socket")
		// Keep the sole raw descriptor owner in path; File must stay live through use.
		executable, err := os.Open("/proc/self/exe")
		if err != nil {
			t.Fatal(err)
		}
		defer executable.Close()
		spec := bootstrapContainerdCheckPlan()
		spec.args = []string{"-test.run=^TestBootstrapContainerdChild$", "--", "borealis-containerd-fixture", "borealis-host-command-fixture"}
		limit := 5 * time.Second
		if cancelChild {
			limit = 100 * time.Millisecond
			spec.args = append(spec.args, "wait-for-cancellation")
		}
		ctx, cancel := context.WithTimeout(context.Background(), limit)
		var out bootstrapContainerdOutput
		err = executeBootstrapContainerdCheck(ctx, spec, host, identity, executable, socket, &out)
		cancel()
		if (err == nil) == cancelChild {
			t.Fatal("child outcome", err, out.buffer.String())
		}
		if !cancelChild {
			_, targets := bootstrapContainerdCheckFixture()
			if _, err := clusterbootstrap.ParseContainerdImportCheck(out.buffer.Bytes(), targets); err != nil {
				t.Fatal("child output", err, out.buffer.String())
			}
		}
		for _, f := range []*os.File{host, identity, executable, socket} {
			if _, err := f.Stat(); err != nil {
				t.Fatal("borrowed descriptor closed", err)
			}
		}
		// Transfer closing responsibility to File before its finalizer can run.
		path.fds = path.fds[:len(path.fds)-1]
		socket.Close()
	}
}
