package clusterbootstrap

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExecutorIdleRequiresSoleProcessAndNoDescendants(t *testing.T) {
	stats := "nr_descendants 0\nnr_dying_descendants 0\n"
	if !executorCgroupIdle([]byte("123\n"), []byte(stats+"future_field 1\n"), 123) {
		t.Fatal("complete idle observation rejected")
	}
	for _, procs := range []string{"", "123", "123\n124\n", "124\n", "123\n123\n", "0123\n", "123\r\n"} {
		if executorCgroupIdle([]byte(procs), []byte(stats), 123) {
			t.Fatal("unknown/other process accepted", procs)
		}
	}
	for _, broken := range []string{"", "nr_descendants 0\n", "nr_dying_descendants 0\n", stats + "nr_descendants 0\n", strings.Replace(stats, " 0", " 1", 1), strings.Replace(stats, "descendants 0", "descendants -1", 1), strings.TrimSuffix(stats, "\n"), strings.Repeat("x", 4097)} {
		if executorCgroupIdle([]byte("123\n"), []byte(broken), 123) {
			t.Fatal("child/unknown cgroup accepted")
		}
	}
}

func TestExecutorIdleDirectoryProof(t *testing.T) {
	for _, mode := range []string{"valid", "missing", "linked directory", "linked procs", "missing stats", "extra process", "child cgroup"} {
		t.Run(mode, func(t *testing.T) {
			path := t.TempDir()
			identity := testMutationExecutor(strings.Repeat("c", 64))
			directory := filepath.Join(path, identity.Unit)
			if mode == "linked directory" {
				directory = filepath.Join(path, "other")
			}
			if err := os.Mkdir(directory, 0700); err != nil {
				t.Fatal(err)
			}
			procs, stats := "123\n", "nr_descendants 0\nnr_dying_descendants 0\n"
			if mode == "extra process" {
				procs += "456\n"
			}
			if mode == "child cgroup" {
				stats = "nr_descendants 1\nnr_dying_descendants 0\n"
			}
			for name, raw := range map[string]string{"cgroup.procs": procs, "cgroup.stat": stats} {
				if mode == "missing stats" && name == "cgroup.stat" || mode == "missing" {
					continue
				}
				if mode == "linked procs" && name == "cgroup.procs" {
					name = "other-procs"
				}
				if err := os.WriteFile(filepath.Join(directory, name), []byte(raw), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "linked procs" {
				if err := os.Symlink("other-procs", filepath.Join(directory, "cgroup.procs")); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "linked directory" {
				if err := os.Symlink("other", filepath.Join(path, identity.Unit)); err != nil {
					t.Fatal(err)
				}
			}
			parent, err := os.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer parent.Close()
			if err := readIdleExecutorDirectory(parent, identity, 123, false); (err == nil) != (mode == "valid") {
				t.Fatal("incorrect idle directory proof", err)
			}
			if readIdleExecutorDirectory(parent, identity, 123, true) == nil {
				t.Fatal("production accepted ordinary filesystem as cgroup2")
			}
		})
	}
}

func TestExecutorIdleStableSupervision(t *testing.T) {
	for _, mode := range []string{"valid", "changed invocation", "reader failure", "descendant", "cancel", "invalid identity", "nil current", "nil idle"} {
		t.Run(mode, func(t *testing.T) {
			identity := testMutationExecutor(strings.Repeat("c", 64))
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			currentCalls, idleCalls := 0, 0
			current := func(context.Context) (ExecutorIdentity, error) {
				currentCalls++
				actual := identity
				if mode == "changed invocation" && currentCalls == 2 {
					actual.InvocationID = strings.Repeat("d", 32)
				}
				if mode == "reader failure" {
					return ExecutorIdentity{}, errors.New("private diagnostic")
				}
				return actual, nil
			}
			idle := func(ExecutorIdentity) error {
				idleCalls++
				if mode == "descendant" {
					return errors.New("private diagnostic")
				}
				if mode == "cancel" {
					cancel()
				}
				return nil
			}
			if mode == "invalid identity" {
				identity.Unit = "foreign.service"
			}
			if mode == "nil current" {
				current = nil
			}
			if mode == "nil idle" {
				idle = nil
			}
			err := observeExecutorIdleWith(ctx, identity, current, idle)
			if (err == nil) != (mode == "valid") || err != nil && err != ErrExecutorContainment {
				t.Fatal("unsafe idle proof or diagnostic leak", err)
			}
			if mode == "valid" && (currentCalls != 2 || idleCalls != 2) {
				t.Fatal("proof not repeated")
			}
		})
	}
}
