package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestPreflightReleaseRepairsOnlyMissingPinnedAncestry(t *testing.T) {
	for _, mode := range []string{"development", "stable", "qualification", "non-descendant", "complete non-descendant", "already proven", "dirty before fetch", "dirty during repair", "HEAD during repair", "repair fetch failure"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			remote, seed, checkout := filepath.Join(root, "origin.git"), filepath.Join(root, "seed"), filepath.Join(root, "checkout")
			realGit, err := exec.LookPath("git")
			if err != nil {
				t.Fatal(err)
			}
			git := func(dir string, args ...string) string {
				t.Helper()
				command := exec.Command(realGit, args...)
				command.Dir = dir
				out, err := command.CombinedOutput()
				if err != nil {
					t.Fatalf("git %v: %v: %s", args, err, out)
				}
				return strings.TrimSpace(string(out))
			}
			git(root, "init", "--bare", remote)
			git(root, "init", "-b", "main", seed)
			git(seed, "config", "user.email", "borealis-tests@example.invalid")
			git(seed, "config", "user.name", "Borealis Tests")
			commit := func(value string) string {
				t.Helper()
				if err := os.WriteFile(filepath.Join(seed, "baseline.txt"), []byte(value+"\n"), 0o600); err != nil {
					t.Fatal(err)
				}
				git(seed, "add", "baseline.txt")
				git(seed, "commit", "-m", value)
				return git(seed, "rev-parse", "HEAD")
			}
			ancestor := commit("ancestor")
			baseline := commit("baseline")
			git(seed, "branch", "baseline")
			middle := commit("intermediate")
			target := commit("target")
			git(seed, "tag", "2026.11.1", target)
			git(seed, "tag", "2026.11.1-rc.1", target)
			git(seed, "checkout", "-b", "sibling", ancestor)
			sibling := commit("sibling")
			git(seed, "remote", "add", "origin", remote)
			git(seed, "push", "origin", "main", "baseline", "sibling", "refs/tags/2026.11.1", "refs/tags/2026.11.1-rc.1")
			origin := "file://" + remote
			git(root, "clone", "--no-tags", "--depth=1", "--branch", "baseline", origin, checkout)
			git(checkout, "fetch", "--no-tags", "--depth=1", "origin", middle)
			git(checkout, "fetch", "--no-tags", "origin", target)
			shallow, err := os.ReadFile(filepath.Join(checkout, ".git", "shallow"))
			if err != nil || !strings.Contains(string(shallow), baseline) || !strings.Contains(string(shallow), middle) {
				t.Fatal("fixture lacks both real shallow boundaries", err)
			}
			if mode == "non-descendant" || mode == "complete non-descendant" {
				target = sibling
			}
			if mode == "complete non-descendant" {
				git(checkout, "fetch", "--no-tags", "--unshallow", "origin", target)
			}
			if mode == "already proven" {
				git(checkout, "fetch", "--no-tags", "--depth=3", "origin", target)
				git(checkout, "merge-base", "--is-ancestor", baseline, target)
			}
			if mode == "dirty before fetch" {
				if err := os.WriteFile(filepath.Join(checkout, "untracked.txt"), []byte("operator work\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			wrapper := filepath.Join(root, "bin")
			if err := os.Mkdir(wrapper, 0o750); err != nil {
				t.Fatal(err)
			}
			log := filepath.Join(root, "git.log")
			script := `#!/bin/sh
printf '%s\n' "$*" >> "$BOREALIS_TEST_GIT_LOG"
case " $* " in
  *" fetch --no-tags --unshallow origin "*)
    if [ "$BOREALIS_TEST_GIT_MODE" = "repair fetch failure" ]; then exit 1; fi
    "$BOREALIS_TEST_REAL_GIT" "$@" || exit $?
    if [ "$BOREALIS_TEST_GIT_MODE" = "dirty during repair" ]; then
      printf 'operator work\n' > untracked.txt
    elif [ "$BOREALIS_TEST_GIT_MODE" = "HEAD during repair" ]; then
      "$BOREALIS_TEST_REAL_GIT" reset --hard "$BOREALIS_TEST_TARGET" >/dev/null || exit $?
    fi
    exit 0;;
esac
exec "$BOREALIS_TEST_REAL_GIT" "$@"
`
			if err := os.WriteFile(filepath.Join(wrapper, "git"), []byte(script), 0o750); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", wrapper+string(os.PathListSeparator)+os.Getenv("PATH"))
			t.Setenv("BOREALIS_TEST_REAL_GIT", realGit)
			t.Setenv("BOREALIS_TEST_GIT_LOG", log)
			t.Setenv("BOREALIS_TEST_GIT_MODE", mode)
			t.Setenv("BOREALIS_TEST_TARGET", target)
			t.Setenv("BOREALIS_ENGINE_REPOSITORY_URL", origin)
			release := "dev-" + target[:12]
			if mode == "stable" {
				release = "2026.11.1"
			} else if mode == "qualification" {
				release = "2026.11.1-rc.1"
			}
			m := &manager{repoRoot: checkout}
			result, err := m.preflightRelease(context.Background(), release, target)
			success := mode == "development" || mode == "stable" || mode == "qualification" || mode == "already proven"
			if (err == nil) != success {
				t.Fatalf("preflight success=%v, want %v: %v", err == nil, success, err)
			}
			if success && (result["revision"] != target || result["fast_forward"] != true || result["worktree_clean"] != true) {
				t.Fatal("preflight lost pinned result")
			}
			if mode != "HEAD during repair" && git(checkout, "rev-parse", "HEAD") != baseline {
				t.Fatal("preflight moved original HEAD")
			}
			if success && git(checkout, "status", "--porcelain", "--untracked-files=normal") != "" {
				t.Fatal("preflight changed worktree")
			}
			commands, err := os.ReadFile(log)
			if err != nil {
				t.Fatal(err)
			}
			wantRepairs := 1
			if mode == "already proven" || mode == "complete non-descendant" || mode == "dirty before fetch" {
				wantRepairs = 0
			}
			if got := strings.Count(string(commands), " fetch --no-tags --unshallow origin "+target+"\n"); got != wantRepairs {
				t.Fatalf("pinned repair fetches=%d, want %d: %s", got, wantRepairs, commands)
			}
			if strings.Contains(string(commands), " --force") || strings.Contains(string(commands), " reset ") || strings.Contains(string(commands), " checkout ") || strings.Contains(string(commands), " merge ") {
				t.Fatal("preflight invoked worktree mutation")
			}
		})
	}
}
