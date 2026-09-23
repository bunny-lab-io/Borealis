package clusterremote

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

var ErrFilesystem = errors.New("SSH filesystem evidence unavailable; host diagnostics withheld")

const filesystemIntegerLimit = uint64(1<<53 - 1)

// Paths must be explicitly selected by the caller. Observation does not choose
// installation or Longhorn paths, create directories, or reserve capacity.
type FilesystemRequest struct {
	MachineID string
	BootID    string
	Paths     []string
}

func validFilesystemPath(value string) bool {
	if len(value) < 1 || len(value) > 1024 || value[0] != '/' || path.Clean(value) != value {
		return false
	}
	for _, b := range []byte(value) {
		if b < 32 || b > 126 {
			return false
		}
	}
	parts := strings.Split(value, "/")
	if len(parts) > 65 {
		return false
	}
	for _, part := range parts {
		if len(part) > 255 {
			return false
		}
	}
	return true
}

func (r FilesystemRequest) Validate() error {
	if !machineIDPattern.MatchString(r.MachineID) || r.MachineID == strings.Repeat("0", 32) || !validPublicUUID(r.BootID) ||
		len(r.Paths) < 1 || len(r.Paths) > 8 || !slices.IsSorted(r.Paths) {
		return ErrFilesystem
	}
	for i, value := range r.Paths {
		if !validFilesystemPath(value) || i > 0 && value == r.Paths[i-1] {
			return ErrFilesystem
		}
	}
	return nil
}

type FilesystemPath struct {
	Path       string `json:"path"`
	Ancestor   string `json:"ancestor"`
	Inode      uint64 `json:"inode"`
	MountID    uint64 `json:"mount_id"`
	MountRoot  string `json:"mount_root"`
	MountPoint string `json:"mount_point"`
	Filesystem string `json:"filesystem"`
}

// One budget per filesystem, even when several selected paths/bind mounts use
// it. AvailableBytes is unprivileged f_bavail * f_frsize, never root reserves.
type FilesystemCapacity struct {
	ID             string `json:"id"`
	Device         string `json:"device"`
	Type           string `json:"type"`
	TotalBytes     uint64 `json:"total_bytes"`
	AvailableBytes uint64 `json:"available_bytes"`
}

type FilesystemEvidence struct {
	MountNamespace uint64               `json:"mount_namespace"`
	Receipt        string               `json:"receipt"`
	Paths          []FilesystemPath     `json:"paths"`
	Filesystems    []FilesystemCapacity `json:"filesystems"`
}

func (v FilesystemEvidence) Clone() FilesystemEvidence {
	v.Paths = slices.Clone(v.Paths)
	v.Filesystems = slices.Clone(v.Filesystems)
	return v
}

var filesystemIDPattern = regexp.MustCompile(`^[0-9a-f]{16}$`)
var filesystemReceiptPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

func filesystemContains(parent, child string) bool {
	return parent == "/" || child == parent || strings.HasPrefix(child, parent+"/")
}

func (v FilesystemEvidence) validate(paths []string) error {
	if v.MountNamespace == 0 || v.MountNamespace > filesystemIntegerLimit || !filesystemReceiptPattern.MatchString(v.Receipt) ||
		len(v.Paths) != len(paths) || len(v.Filesystems) < 1 || len(v.Filesystems) > len(paths) {
		return ErrFilesystem
	}
	seen, devices, used := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for i, fs := range v.Filesystems {
		if !filesystemIDPattern.MatchString(fs.ID) || fs.ID == strings.Repeat("0", 16) || seen[fs.ID] || devices[fs.Device] ||
			(i > 0 && fs.ID <= v.Filesystems[i-1].ID) || (fs.Type != "ext4" && fs.Type != "xfs") ||
			fs.TotalBytes == 0 || fs.TotalBytes > filesystemIntegerLimit || fs.AvailableBytes > fs.TotalBytes {
			return ErrFilesystem
		}
		parts := strings.Split(fs.Device, ":")
		if len(parts) != 2 {
			return ErrFilesystem
		}
		for _, part := range parts {
			n, err := strconv.ParseUint(part, 10, 32)
			if err != nil || strconv.FormatUint(n, 10) != part {
				return ErrFilesystem
			}
		}
		seen[fs.ID], devices[fs.Device] = true, true
	}
	mounts := map[uint64]FilesystemPath{}
	for i, item := range v.Paths {
		if item.Path != paths[i] || !validFilesystemPath(item.Ancestor) || !filesystemContains(item.Ancestor, item.Path) ||
			!validFilesystemPath(item.MountRoot) || !validFilesystemPath(item.MountPoint) || !filesystemContains(item.MountPoint, item.Ancestor) ||
			item.Inode == 0 || item.Inode > filesystemIntegerLimit || item.MountID == 0 || item.MountID > filesystemIntegerLimit || !seen[item.Filesystem] {
			return ErrFilesystem
		}
		if prior, ok := mounts[item.MountID]; ok && (prior.MountRoot != item.MountRoot || prior.MountPoint != item.MountPoint || prior.Filesystem != item.Filesystem) {
			return ErrFilesystem
		}
		mounts[item.MountID], used[item.Filesystem] = item, true
	}
	if len(used) != len(seen) {
		return ErrFilesystem
	}
	return nil
}

type filesystemObservation struct {
	Version   int                `json:"version"`
	MachineID string             `json:"machine_id"`
	BootID    string             `json:"boot_id"`
	Evidence  FilesystemEvidence `json:"evidence"`
}

// No imported/serialized object can construct native acquisition provenance.
type TargetFilesystem struct {
	wire              filesystemObservation
	target            Target
	key               HostKey
	started, finished time.Time
}

func (v TargetFilesystem) Evidence(notBefore time.Time, target Target, key HostKey, request FilesystemRequest) (FilesystemEvidence, error) {
	if notBefore.IsZero() || v.started.Before(notBefore) || v.finished.Before(v.started) || v.finished.After(time.Now()) ||
		v.finished.Sub(v.started) > 25*time.Second || request.Validate() != nil || target.Validate() != nil || key.Validate() != nil ||
		v.target != target || v.key.Algorithm != key.Algorithm || v.key.Fingerprint != key.Fingerprint || !bytes.Equal(v.key.PublicKey, key.PublicKey) ||
		v.wire.Version != 1 || v.wire.MachineID != request.MachineID || v.wire.BootID != request.BootID || v.wire.Evidence.validate(request.Paths) != nil {
		return FilesystemEvidence{}, ErrFilesystem
	}
	return v.wire.Evidence.Clone(), nil
}

func filesystemCommand(request FilesystemRequest) (string, error) {
	if request.Validate() != nil {
		return "", ErrFilesystem
	}
	// Only public selected paths cross the fixed command argument boundary.
	// Expected machine/boot identity stays local and is compared independently.
	raw, err := json.Marshal(request.Paths)
	if err != nil || len(raw) > 16<<10 {
		return "", ErrFilesystem
	}
	return buildPrivilegedInspectionCommand("/usr/sbin:/usr/bin:/sbin:/bin", "exec /usr/bin/python3 -I -B -c "+shellConstant(filesystemScript)+" "+shellConstant(base64.StdEncoding.EncodeToString(raw))+" 2>/dev/null"), nil
}

func (client *Client) InspectFilesystem(parent context.Context, sudoPassword []byte, expected Target, approved HostKey, request FilesystemRequest, check func(context.Context) error) (TargetFilesystem, error) {
	started := time.Now()
	request.Paths = slices.Clone(request.Paths)
	approved.PublicKey = bytes.Clone(approved.PublicKey)
	command, err := filesystemCommand(request)
	if err != nil || client == nil || client.ssh == nil || expected.Validate() != nil || approved.Validate() != nil || check == nil || parent.Err() != nil ||
		client.target != expected || client.approved.Algorithm != approved.Algorithm || client.approved.Fingerprint != approved.Fingerprint || !bytes.Equal(client.approved.PublicKey, approved.PublicKey) {
		return TargetFilesystem{}, ErrFilesystem
	}
	ctx, cancel := context.WithTimeout(parent, 25*time.Second)
	defer cancel()
	boundary := func() bool {
		bound, stop := context.WithTimeout(ctx, time.Second)
		defer stop()
		if bound.Err() != nil || check(bound) != nil || bound.Err() != nil {
			cancel()
			return false
		}
		return true
	}
	if !boundary() {
		return TargetFilesystem{}, ErrFilesystem
	}
	stop, done := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(250 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ctx.Done():
				return
			case <-ticker.C:
				if !boundary() {
					return
				}
			}
		}
	}()
	raw, err := client.inspectPrivilegedOutput(ctx, sudoPassword, command)
	close(stop)
	<-done
	if err != nil || !boundary() || len(raw) == 0 || len(raw) > MaxOutputBytes {
		return TargetFilesystem{}, ErrFilesystem
	}
	var wire filesystemObservation
	if json.Unmarshal(raw, &wire) != nil {
		return TargetFilesystem{}, ErrFilesystem
	}
	canonical, err := json.Marshal(wire)
	v := TargetFilesystem{wire: wire, target: expected, key: approved, started: started, finished: time.Now()}
	if err != nil || !bytes.Equal(canonical, bytes.TrimSpace(raw)) || ctx.Err() != nil {
		return TargetFilesystem{}, ErrFilesystem
	}
	if _, err := v.Evidence(started, expected, approved, request); err != nil {
		return TargetFilesystem{}, ErrFilesystem
	}
	return v, nil
}
