package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

const bootstrapRuntimeAccountName = "borealis-engine"

var errBootstrapRuntimeAccount = errors.New("fresh runtime account custody unavailable or changed")

type bootstrapRuntimeAccount struct {
	User, Group bool
	Shell       string
}

func proveBootstrapRuntimeAccount(ctx context.Context) error {
	state, err := observeBootstrapRuntimeAccount(ctx)
	if err != nil || !state.User || !state.Group {
		return errBootstrapRuntimeAccount
	}
	return nil
}

// Fixed creation is called only inside prepare_runtime_identity's supervised
// durable intent. It never deletes/repairs accounts, sets a password, changes an
// existing account or starts a service. Interrupted partial creation stays
// uncertain until an exact subsequent proof can settle the complete effect.
func prepareBootstrapRuntimeIdentity(ctx context.Context, lock *os.File, check func(context.Context) error) error {
	if os.Geteuid() != 0 {
		return errBootstrapRuntimeAccount
	}
	return prepareBootstrapRuntimeIdentityWith(ctx, lock, check, observeBootstrapRuntimeAccount, createBootstrapRuntimeAccount)
}

func prepareBootstrapRuntimeIdentityWith(ctx context.Context, lock *os.File, check func(context.Context) error,
	observe func(context.Context) (bootstrapRuntimeAccount, error), create func(context.Context, *os.File, bool) error) error {
	if lock == nil || check == nil || observe == nil || create == nil || ctx.Err() != nil {
		return errBootstrapRuntimeAccount
	}
	boundary := func() error {
		if ctx.Err() != nil || check(ctx) != nil || ctx.Err() != nil {
			return errBootstrapRuntimeAccount
		}
		return nil
	}
	if boundary() != nil {
		return errBootstrapRuntimeAccount
	}
	state, err := observe(ctx)
	if err != nil || boundary() != nil {
		return errBootstrapRuntimeAccount
	}
	if !state.Group {
		if create(ctx, lock, false) != nil || boundary() != nil {
			return errBootstrapRuntimeAccount
		}
		current, err := observe(ctx)
		if err != nil || !current.Group || current.User != state.User || current.Shell != state.Shell || boundary() != nil {
			return errBootstrapRuntimeAccount
		}
		state = current
	}
	if !state.User {
		if create(ctx, lock, true) != nil || boundary() != nil {
			return errBootstrapRuntimeAccount
		}
		current, err := observe(ctx)
		if err != nil || !current.User || !current.Group || current.Shell != "/usr/sbin/nologin" || boundary() != nil {
			return errBootstrapRuntimeAccount
		}
		state = current
	}
	current, err := observe(ctx)
	if err != nil || current != state || !current.User || !current.Group || boundary() != nil {
		return errBootstrapRuntimeAccount
	}
	return nil
}

func createBootstrapRuntimeAccount(parent context.Context, lock *os.File, user bool) error {
	if lock == nil {
		return errBootstrapRuntimeAccount
	}
	ctx, cancel := context.WithTimeout(parent, 5*time.Second)
	defer cancel()
	program := "/usr/sbin/groupadd"
	args := []string{"--system", "--key", "SYS_GID_MAX=65534", "--gid", "64646", bootstrapRuntimeAccountName}
	if user {
		program = "/usr/sbin/useradd"
		args = []string{"--system", "--key", "SYS_UID_MAX=65534", "--uid", "64646", "--gid", bootstrapRuntimeAccountName, "--no-user-group", "--home-dir", "/nonexistent", "--no-create-home", "--no-log-init", "--shell", "/usr/sbin/nologin", bootstrapRuntimeAccountName}
	}
	command := exec.CommandContext(ctx, program, args...)
	command.Env = []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "LC_ALL=C"}
	command.ExtraFiles = []*os.File{lock}
	command.Stdin, command.Stdout, command.Stderr = nil, io.Discard, io.Discard
	command.WaitDelay = 250 * time.Millisecond
	if command.Run() != nil || ctx.Err() != nil {
		return errBootstrapRuntimeAccount
	}
	return nil
}

// Account observation never reads shadow/gshadow or exports host account rows.
// Only local files/systemd NSS is supported. Reserve the actual named runtime
// account before assigning secrets to its numeric UID; an unallocated UID could
// otherwise be taken by systemd DynamicUser between observation and key writes.
func observeBootstrapRuntimeAccount(ctx context.Context) (bootstrapRuntimeAccount, error) {
	invalid := bootstrapRuntimeAccount{}
	if ctx.Err() != nil || actionRuntimeID != clusterbootstrap.PreparationRuntimeID {
		return invalid, errBootstrapRuntimeAccount
	}
	nss, err := readBootstrapPublicFile("/etc/nsswitch.conf", 64<<10)
	if err != nil || validateBootstrapAccountNSS(nss) != nil {
		return invalid, errBootstrapRuntimeAccount
	}
	passwd, err := bootstrapAccountGetent(ctx, "passwd")
	if err != nil {
		return invalid, errBootstrapRuntimeAccount
	}
	defer clear(passwd)
	groups, err := bootstrapAccountGetent(ctx, "group")
	if err != nil {
		return invalid, errBootstrapRuntimeAccount
	}
	defer clear(groups)
	state, err := parseBootstrapRuntimeAccount(passwd, groups)
	if err != nil || bootstrapRuntimeUIDAbsent(ctx, "/proc", actionRuntimeID) != nil {
		return invalid, errBootstrapRuntimeAccount
	}
	after, err := readBootstrapPublicFile("/etc/nsswitch.conf", 64<<10)
	if err != nil || !bytes.Equal(nss, after) || ctx.Err() != nil {
		return invalid, errBootstrapRuntimeAccount
	}
	return state, nil
}

func validateBootstrapAccountNSS(raw []byte) error {
	if len(raw) == 0 || len(raw) > 64<<10 || !utf8.Valid(raw) || bytes.ContainsRune(raw, 0) {
		return errBootstrapRuntimeAccount
	}
	seen := map[string]bool{}
	for _, line := range strings.Split(string(raw), "\n") {
		line, _, _ = strings.Cut(line, "#")
		key, value, ok := strings.Cut(line, ":")
		key = strings.TrimSpace(key)
		if key != "passwd" && key != "group" {
			continue
		}
		if !ok || seen[key] {
			return errBootstrapRuntimeAccount
		}
		seen[key] = true
		fields := strings.Fields(value)
		if len(fields) == 0 || fields[0] != "files" || len(fields) > 2 || len(fields) == 2 && fields[1] != "systemd" {
			return errBootstrapRuntimeAccount
		}
	}
	if len(seen) != 2 {
		return errBootstrapRuntimeAccount
	}
	return nil
}

func bootstrapAccountGetent(parent context.Context, database string) ([]byte, error) {
	if database != "passwd" && database != "group" {
		return nil, errBootstrapRuntimeAccount
	}
	ctx, cancel := context.WithTimeout(parent, 2*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "/usr/bin/getent", database)
	command.Env = []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "LC_ALL=C"}
	command.WaitDelay = 250 * time.Millisecond
	output := &identityConsumerOutput{}
	command.Stdout, command.Stderr = output, io.Discard
	if command.Run() != nil || ctx.Err() != nil {
		return nil, errBootstrapRuntimeAccount
	}
	return output.buffer.Bytes(), nil
}

func parseBootstrapRuntimeAccount(passwd, groups []byte) (bootstrapRuntimeAccount, error) {
	state := bootstrapRuntimeAccount{}
	invalid := func() (bootstrapRuntimeAccount, error) { return bootstrapRuntimeAccount{}, errBootstrapRuntimeAccount }
	for _, raw := range [][]byte{passwd, groups} {
		if len(raw) == 0 || len(raw) > identityConsumerObservationLimit || !utf8.Valid(raw) || bytes.ContainsRune(raw, 0) || !bytes.HasSuffix(raw, []byte{'\n'}) {
			return invalid()
		}
	}
	id := strconv.Itoa(actionRuntimeID)
	for _, line := range strings.Split(strings.TrimSuffix(string(passwd), "\n"), "\n") {
		fields := strings.Split(line, ":")
		if len(fields) != 7 || len(line) > 4096 {
			return invalid()
		}
		for _, field := range fields[2:4] {
			value, err := strconv.ParseUint(field, 10, 32)
			if err != nil || strconv.FormatUint(value, 10) != field {
				return invalid()
			}
		}
		if fields[0] != bootstrapRuntimeAccountName && fields[2] != id {
			if fields[3] == id {
				return invalid()
			}
			continue
		}
		if state.User || fields[0] != bootstrapRuntimeAccountName || fields[2] != id || fields[3] != id || fields[5] != "/nonexistent" {
			return invalid()
		}
		switch fields[6] {
		case "/usr/sbin/nologin", "/sbin/nologin", "/bin/false":
		default:
			return invalid()
		}
		state.User, state.Shell = true, fields[6]
	}
	for _, line := range strings.Split(strings.TrimSuffix(string(groups), "\n"), "\n") {
		fields := strings.Split(line, ":")
		if len(fields) != 4 || len(line) > 4096 {
			return invalid()
		}
		value, err := strconv.ParseUint(fields[2], 10, 32)
		if err != nil || strconv.FormatUint(value, 10) != fields[2] {
			return invalid()
		}
		if fields[0] != bootstrapRuntimeAccountName && fields[2] != id {
			for _, member := range strings.Split(fields[3], ",") {
				if member == bootstrapRuntimeAccountName {
					return invalid()
				}
			}
			continue
		}
		if state.Group || fields[0] != bootstrapRuntimeAccountName || fields[2] != id || fields[3] != "" {
			return invalid()
		}
		state.Group = true
	}
	return state, nil
}

// Probe all four UID credentials, not just effective UID. Unknown procfs reads
// fail closed; a process that disappeared before opening its status is skipped.
// Caller repeats observation around directory/identity effects under supervision.
func bootstrapRuntimeUIDAbsent(ctx context.Context, path string, uid int) error {
	if uid <= 0 || ctx.Err() != nil {
		return errBootstrapRuntimeAccount
	}
	root, err := os.OpenRoot(path)
	if err != nil {
		return errBootstrapRuntimeAccount
	}
	defer root.Close()
	directory, err := root.Open(".")
	if err != nil {
		return errBootstrapRuntimeAccount
	}
	defer directory.Close()
	entries, err := directory.ReadDir(32769)
	if err != nil && err != io.EOF || len(entries) > 32768 {
		return errBootstrapRuntimeAccount
	}
	for _, entry := range entries {
		if ctx.Err() != nil {
			return errBootstrapRuntimeAccount
		}
		pid, err := strconv.ParseUint(entry.Name(), 10, 32)
		if err != nil || pid == 0 || strconv.FormatUint(pid, 10) != entry.Name() {
			continue
		}
		file, err := root.OpenFile(entry.Name()+"/status", os.O_RDONLY, 0)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return errBootstrapRuntimeAccount
		}
		raw, readErr := io.ReadAll(io.LimitReader(file, 64<<10+1))
		closeErr := file.Close()
		if readErr != nil || closeErr != nil || len(raw) > 64<<10 {
			return errBootstrapRuntimeAccount
		}
		seen := false
		for _, line := range strings.Split(string(raw), "\n") {
			if !strings.HasPrefix(line, "Uid:") {
				continue
			}
			fields := strings.Fields(strings.TrimPrefix(line, "Uid:"))
			if seen || len(fields) != 4 {
				return errBootstrapRuntimeAccount
			}
			seen = true
			for _, field := range fields {
				value, err := strconv.ParseUint(field, 10, 32)
				if err != nil || strconv.FormatUint(value, 10) != field || value == uint64(uid) {
					return errBootstrapRuntimeAccount
				}
			}
		}
		if !seen {
			return errBootstrapRuntimeAccount
		}
	}
	if ctx.Err() != nil {
		return errBootstrapRuntimeAccount
	}
	return nil
}
