package clusterbootstrap

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

func sourceConfig(repository string) string {
	return "[core]\n\trepositoryformatversion = 0\n\tfilemode = true\n\tbare = false\n" +
		"\tlogAllRefUpdates = false\n[remote \"origin\"]\n" +
		"\turl = https://github.com/" + repository + ".git\n" +
		"\tfetch = +refs/heads/*:refs/remotes/origin/*\n"
}

func verifySource(ctx context.Context, root string, i identity, files map[string]sourceFile, gitBin string) error {
	invalid := errors.New("node bootstrap source Git identity or tracked content mismatch")
	// The archive allowlist has already excluded hooks, alternates, replacement
	// refs, linked-worktree state, attributes/info, grafts and external config.
	// Check config bytes BEFORE invoking Git; never trust supplied executable
	// filters/credential helpers/fsmonitor or inherited GIT_* environment.
	for name, expected := range map[string]string{
		"config": sourceConfig(i.Repository), "HEAD": i.SourceSHA + "\n", "shallow": i.SourceSHA + "\n",
	} {
		raw, err := os.ReadFile(filepath.Join(root, ".git", name))
		if err != nil || string(raw) != expected {
			return invalid
		}
	}
	if gitBin == "" {
		gitBin = "git"
	}
	if _, err := sourceGit(ctx, gitBin, root, "fsck", "--strict", "--no-reflogs"); err != nil {
		return invalid
	}
	for _, proof := range []struct {
		args []string
		want string
	}{
		{[]string{"rev-parse", "HEAD^{commit}"}, i.SourceSHA},
		{[]string{"rev-parse", "refs/tags/" + i.Release + "^{commit}"}, i.SourceSHA},
		{[]string{"rev-parse", "HEAD^{tree}"}, i.SourceTree},
		{[]string{"rev-list", "--count", "HEAD"}, "1"},
		{[]string{"for-each-ref", "--format=%(refname)"}, "refs/tags/" + i.Release},
	} {
		value, err := sourceGit(ctx, gitBin, root, proof.args...)
		if err != nil || strings.TrimSpace(string(value)) != proof.want {
			return invalid
		}
	}
	raw, err := sourceGit(ctx, gitBin, root, "ls-tree", "-rz", "--full-tree", i.SourceSHA)
	if err != nil {
		return invalid
	}
	for _, entry := range bytes.Split(raw, []byte{0}) {
		if len(entry) == 0 {
			continue
		}
		metadata, name, ok := bytes.Cut(entry, []byte{'\t'})
		parts := strings.Fields(string(metadata))
		file, found := files[string(name)]
		if !ok || !found || len(parts) != 3 || parts[1] != "blob" || parts[2] != file.sha ||
			!(parts[0] == "100644" && file.mode == 0o644 || parts[0] == "100755" && file.mode == 0o755) {
			return invalid
		}
		delete(files, string(name))
	}
	if len(files) != 0 {
		return invalid
	}
	return nil
}

type boundedOutput struct {
	bytes.Buffer
	limit int
}

func (b *boundedOutput) Write(p []byte) (int, error) {
	if len(p) > b.limit-b.Len() {
		return 0, errors.New("node bootstrap Git output limit exceeded")
	}
	return b.Buffer.Write(p)
}

func sourceGit(ctx context.Context, binary, root string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	fixed := []string{"--no-replace-objects", "-C", root, "-c", "safe.directory=" + root,
		"-c", "core.hooksPath=/dev/null", "-c", "core.fsmonitor=false", "-c", "protocol.allow=never"}
	cmd := exec.CommandContext(ctx, binary, append(fixed, args...)...)
	for _, item := range os.Environ() {
		if !strings.HasPrefix(item, "GIT_") {
			cmd.Env = append(cmd.Env, item)
		}
	}
	cmd.Env = append(cmd.Env, "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_TERMINAL_PROMPT=0", "GIT_NO_REPLACE_OBJECTS=1")
	cmd.WaitDelay = time.Second
	stdout, stderr := &boundedOutput{limit: 32 << 20}, &boundedOutput{limit: 1 << 20}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	if cmd.Run() != nil {
		return nil, errors.New("node bootstrap Git verification failed; diagnostics withheld")
	}
	return stdout.Bytes(), nil
}
