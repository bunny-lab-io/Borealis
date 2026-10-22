//go:build linux

package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
)

const identityRuntimeRoot = "/opt/Borealis/Engine/Services/api-backend/secrets"

var errIdentityRuntimeProof = errors.New("joined identity host runtime proof unavailable or changed")

type identityRuntimeMount struct {
	Device uint64
	Inode  uint64
	Mount  uint64
	Mode   uint32
	UID    uint32
	GID    uint32
}

type identityRuntimeProjection struct {
	ManagerSHA256   string
	ManifestSHA256  string
	WorkloadsSHA256 string
	Mount           identityRuntimeMount
}

type identityRuntimeReader struct {
	manager     func(context.Context) (string, error)
	manifest    func() ([]byte, error)
	deployments func(context.Context, string) ([]byte, error)
	mount       func(string, int, int) (identityRuntimeMount, error)
	quiesced    func(context.Context, string, identityRuntimeMount) error
}

// observeIdentityHostRuntime is a fixed read-only prerequisite for joined-node
// identity recovery. The active supervised bootstrap request binds this helper
// binary to one immutable release. Its published image manifest and zero-scaled
// consumer Deployments prove any later restart uses that same guard-capable
// source. Procfs then rejects every remaining fixed-path container mount and
// known host consumer while the identity directory device/inode/mount stays
// stable. Kubernetes Pod absence and original-controller/Node authority remain
// separate mandatory evidence. This function exposes no session action.
func observeIdentityHostRuntime(ctx context.Context, repoRoot string, request clusterbootstrap.SessionRequest, uid, gid int) error {
	reader := identityRuntimeReader{
		manager: hashIdentityRuntimeManager,
		manifest: func() ([]byte, error) {
			return readIdentityRuntimeFile(filepath.Join(repoRoot, "Engine", "Deploy", "image-manifest.json"), 64<<10)
		},
		deployments: readIdentityRuntimeDeployments,
		mount:       captureIdentityRuntimeMount,
		quiesced: func(ctx context.Context, root string, mount identityRuntimeMount) error {
			return identityRuntimeProcessesAbsent(ctx, "/proc", root, mount)
		},
	}
	return reader.check(ctx, request, identityRuntimeRoot, uid, gid)
}

func (r identityRuntimeReader) check(ctx context.Context, request clusterbootstrap.SessionRequest, root string, uid, gid int) error {
	fail := errIdentityRuntimeProof
	if request.Validate() != nil || r.manager == nil || r.manifest == nil || r.deployments == nil || r.mount == nil || r.quiesced == nil ||
		ctx.Err() != nil || uid < 0 || gid < 0 || root == "" || !filepath.IsAbs(root) || filepath.Clean(root) != root {
		return fail
	}
	var previous identityRuntimeProjection
	for pass := 0; pass < 2; pass++ {
		manager, err := r.manager(ctx)
		if err != nil || manager != request.ManagerSHA256 {
			return fail
		}
		manifestRaw, err := r.manifest()
		if err != nil {
			return fail
		}
		images, manifestSHA, err := identityRuntimeImages(manifestRaw, request)
		if err != nil {
			return fail
		}
		deploymentsRaw, err := r.deployments(ctx, request.Binding.Hostname)
		if err != nil {
			return fail
		}
		workloadsSHA, err := identityRuntimeWorkloads(deploymentsRaw, request, root, images)
		if err != nil {
			return fail
		}
		mount, err := r.mount(root, uid, gid)
		if err != nil || r.quiesced(ctx, root, mount) != nil || ctx.Err() != nil {
			return fail
		}
		current := identityRuntimeProjection{manager, manifestSHA, workloadsSHA, mount}
		if pass > 0 && current != previous {
			return fail
		}
		previous = current
	}
	return nil
}

func readIdentityRuntimeFile(path string, limit int64) ([]byte, error) {
	file, err := openBootstrapPublicFile(path, limit)
	if err != nil {
		return nil, errIdentityRuntimeProof
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil || len(raw) < 1 || int64(len(raw)) > limit {
		return nil, errIdentityRuntimeProof
	}
	return raw, nil
}

func hashIdentityRuntimeManager(ctx context.Context) (string, error) {
	path, err := os.Executable()
	if err != nil {
		return "", errIdentityRuntimeProof
	}
	file, err := openBootstrapPublicFile(path, clusterbootstrap.MaxBundleBytes)
	if err != nil {
		return "", errIdentityRuntimeProof
	}
	defer file.Close()
	hash := sha256.New()
	buffer := make([]byte, 64<<10)
	for {
		if ctx.Err() != nil {
			return "", errIdentityRuntimeProof
		}
		n, readErr := file.Read(buffer)
		if n > 0 {
			_, _ = hash.Write(buffer[:n])
		}
		if readErr == io.EOF {
			return hex.EncodeToString(hash.Sum(nil)), nil
		}
		if readErr != nil || n == 0 {
			return "", errIdentityRuntimeProof
		}
	}
}

type identityRuntimeImage struct {
	Image            string `json:"image"`
	ArchiveReference string `json:"archive_reference"`
	Hash             string `json:"hash"`
	Mode             string `json:"mode"`
	ManifestDigest   string `json:"manifest_digest"`
	ConfigDigest     string `json:"config_digest"`
}

func identityRuntimeImages(raw []byte, request clusterbootstrap.SessionRequest) (map[string]string, string, error) {
	fail := func() (map[string]string, string, error) { return nil, "", errIdentityRuntimeProof }
	if len(raw) == 0 || len(raw) > 64<<10 || request.Validate() != nil {
		return fail()
	}
	var manifest struct {
		SchemaVersion int                             `json:"schema_version"`
		Project       string                          `json:"project"`
		Mode          string                          `json:"mode"`
		SourceSHA     string                          `json:"source_sha"`
		Release       string                          `json:"release"`
		Services      map[string]identityRuntimeImage `json:"services"`
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if decoder.Decode(&manifest) != nil || decoder.Decode(&struct{}{}) != io.EOF || manifest.SchemaVersion != 1 ||
		manifest.Project != "borealis-engine" || manifest.Mode != "prod" || manifest.SourceSHA != request.SourceSHA ||
		manifest.Release != request.Release || len(manifest.Services) != 9 {
		return fail()
	}
	images := make(map[string]string, len(manifest.Services))
	for _, role := range clusterbootstrap.ImageRoles() {
		image, ok := manifest.Services[role]
		digest, prefix := strings.CutPrefix(image.ManifestDigest, "sha256:")
		wantArchive := "docker.io/borealis-engine/" + role + ":release-" + request.SourceSHA
		wantImage := "docker.io/borealis-engine/" + role + "@" + image.ManifestDigest
		if !ok || !prefix || len(digest) != 64 || image.Image != wantImage || image.ArchiveReference != wantArchive ||
			image.Hash != digest || image.Mode != "prod" || !strings.HasPrefix(image.ConfigDigest, "sha256:") || len(image.ConfigDigest) != 71 {
			return fail()
		}
		if _, err := hex.DecodeString(digest); err != nil {
			return fail()
		}
		if _, err := hex.DecodeString(strings.TrimPrefix(image.ConfigDigest, "sha256:")); err != nil {
			return fail()
		}
		images[role] = wantImage
	}
	if len(images) != len(manifest.Services) {
		return fail()
	}
	digest := sha256.Sum256(raw)
	return images, hex.EncodeToString(digest[:]), nil
}

func readIdentityRuntimeDeployments(ctx context.Context, node string) ([]byte, error) {
	if !nodePattern.MatchString(node) || ctx.Err() != nil {
		return nil, errIdentityRuntimeProof
	}
	raw, err := run(ctx, "", "k3s", "kubectl", "-n", "borealis", "get", "deployments",
		"-l", "borealis.io/engine-node="+node+",borealis.io/node-workload=true", "-o", "json")
	if err != nil || len(raw) == 0 || len(raw) > 4<<20 {
		return nil, errIdentityRuntimeProof
	}
	return []byte(raw), nil
}

func identityRuntimeWorkloads(raw []byte, request clusterbootstrap.SessionRequest, root string, images map[string]string) (string, error) {
	if len(raw) == 0 || len(raw) > 4<<20 || request.Validate() != nil || len(images) != len(clusterbootstrap.ImageRoles()) {
		return "", errIdentityRuntimeProof
	}
	var list struct {
		Items []struct {
			Metadata struct {
				Labels      map[string]string `json:"labels"`
				Annotations map[string]string `json:"annotations"`
			} `json:"metadata"`
			Spec struct {
				Replicas *int `json:"replicas"`
				Template struct {
					Metadata struct {
						Annotations map[string]string `json:"annotations"`
					} `json:"metadata"`
					Spec struct {
						Containers []struct {
							Name  string `json:"name"`
							Image string `json:"image"`
							Env   []struct {
								Name  string `json:"name"`
								Value string `json:"value"`
							} `json:"env"`
							VolumeMounts []struct {
								Name      string `json:"name"`
								MountPath string `json:"mountPath"`
								ReadOnly  bool   `json:"readOnly"`
							} `json:"volumeMounts"`
						} `json:"containers"`
						Volumes []struct {
							Name     string `json:"name"`
							HostPath *struct {
								Path string `json:"path"`
							} `json:"hostPath"`
						} `json:"volumes"`
					} `json:"spec"`
				} `json:"template"`
			} `json:"spec"`
		} `json:"items"`
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if decoder.Decode(&list) != nil || decoder.Decode(&struct{}{}) != io.EOF || len(list.Items) < 2 || len(list.Items) > 32 {
		return "", errIdentityRuntimeProof
	}
	seen := map[string]bool{}
	projection := make([]string, 0, 4)
	for _, deployment := range list.Items {
		name := deployment.Metadata.Labels["app.kubernetes.io/name"]
		role := strings.TrimSuffix(name, "-candidate")
		identityVolume := ""
		for _, volume := range deployment.Spec.Template.Spec.Volumes {
			if volume.HostPath == nil {
				continue
			}
			path := filepath.Clean(volume.HostPath.Path)
			if path == root || strings.HasPrefix(path, root+string(filepath.Separator)) || strings.HasPrefix(root, path+string(filepath.Separator)) {
				if identityVolume != "" || volume.Name == "" {
					return "", errIdentityRuntimeProof
				}
				identityVolume = volume.Name
			}
		}
		if identityVolume == "" {
			continue
		}
		if role != "api-backend" && role != "job-scheduler" || seen[name] || deployment.Spec.Replicas == nil || *deployment.Spec.Replicas != 0 ||
			deployment.Metadata.Labels["borealis.io/engine-node"] != request.Binding.Hostname ||
			deployment.Metadata.Labels["borealis.io/node-workload"] != "true" ||
			deployment.Metadata.Annotations["borealis.io/revision"] != request.SourceSHA ||
			deployment.Spec.Template.Metadata.Annotations["borealis.io/revision"] != request.SourceSHA {
			return "", errIdentityRuntimeProof
		}
		seen[name] = true
		found := false
		for _, container := range deployment.Spec.Template.Spec.Containers {
			if container.Name != role {
				continue
			}
			if found || container.Image != images[role] {
				return "", errIdentityRuntimeProof
			}
			mounted := false
			for _, mount := range container.VolumeMounts {
				if mount.Name == identityVolume && mount.MountPath == root {
					if role == "job-scheduler" && !mount.ReadOnly {
						return "", errIdentityRuntimeProof
					}
					mounted = true
				}
			}
			if !mounted {
				return "", errIdentityRuntimeProof
			}
			if role == "job-scheduler" {
				worker := ""
				for _, value := range container.Env {
					if value.Name == "BOREALIS_SITE_WORKER_IMAGE" {
						if worker != "" {
							return "", errIdentityRuntimeProof
						}
						worker = value.Value
					}
				}
				if worker != images["site-worker"] {
					return "", errIdentityRuntimeProof
				}
			}
			found = true
		}
		if !found {
			return "", errIdentityRuntimeProof
		}
		projection = append(projection, name+"="+images[role])
	}
	for _, required := range []string{"api-backend", "job-scheduler"} {
		if !seen[required] {
			return "", errIdentityRuntimeProof
		}
	}
	slices.Sort(projection)
	digest := sha256.Sum256([]byte(strings.Join(projection, "\n")))
	return hex.EncodeToString(digest[:]), nil
}

func captureIdentityRuntimeMount(path string, uid, gid int) (identityRuntimeMount, error) {
	invalid := identityRuntimeMount{}
	abs, err := filepath.Abs(path)
	if err != nil || abs != path || filepath.Clean(path) != path {
		return invalid, errIdentityRuntimeProof
	}
	real, err := filepath.EvalSymlinks(path)
	if err != nil || real != path {
		return invalid, errIdentityRuntimeProof
	}
	fd, err := unix.Open(path, unix.O_PATH|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return invalid, errIdentityRuntimeProof
	}
	defer unix.Close(fd)
	var stat unix.Statx_t
	const mask = unix.STATX_TYPE | unix.STATX_MODE | unix.STATX_UID | unix.STATX_GID | unix.STATX_INO | unix.STATX_MNT_ID
	if unix.Statx(fd, "", unix.AT_EMPTY_PATH|unix.AT_STATX_DONT_SYNC, mask, &stat) != nil || stat.Mask&mask != mask ||
		stat.Mode&unix.S_IFMT != unix.S_IFDIR || stat.Mode&0o022 != 0 || stat.Uid != uint32(uid) || stat.Gid != uint32(gid) {
		return invalid, errIdentityRuntimeProof
	}
	device := unix.Mkdev(stat.Dev_major, stat.Dev_minor)
	if device == 0 || stat.Ino == 0 || stat.Mnt_id == 0 {
		return invalid, errIdentityRuntimeProof
	}
	return identityRuntimeMount{device, stat.Ino, stat.Mnt_id, uint32(stat.Mode), stat.Uid, stat.Gid}, nil
}

func identityRuntimeProcessesAbsent(ctx context.Context, proc, root string, identity identityRuntimeMount) error {
	if ctx.Err() != nil || identity.Device == 0 || identity.Inode == 0 || identity.Mount == 0 {
		return errIdentityRuntimeProof
	}
	hostNamespace, err := os.Stat(filepath.Join(proc, "1", "ns", "mnt"))
	if err != nil {
		return errIdentityRuntimeProof
	}
	directory, err := os.Open(proc)
	if err != nil {
		return errIdentityRuntimeProof
	}
	entries, readErr := directory.ReadDir(131073)
	directory.Close()
	if readErr != nil && readErr != io.EOF || len(entries) > 131072 {
		return errIdentityRuntimeProof
	}
	for _, entry := range entries {
		if ctx.Err() != nil {
			return errIdentityRuntimeProof
		}
		pid, err := strconv.ParseUint(entry.Name(), 10, 31)
		if err != nil || pid == 1 || int(pid) == os.Getpid() {
			continue
		}
		base := filepath.Join(proc, entry.Name())
		namespace, err := os.Stat(filepath.Join(base, "ns", "mnt"))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return errIdentityRuntimeProof
		}
		known, knownErr := identityRuntimeKnownHostConsumer(base)
		if errors.Is(knownErr, os.ErrNotExist) {
			continue
		}
		if knownErr != nil || known {
			return errIdentityRuntimeProof
		}
		if os.SameFile(namespace, hostNamespace) {
			continue
		}
		path := filepath.Join(base, "root", strings.TrimPrefix(root, string(filepath.Separator)))
		info, err := os.Stat(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return errIdentityRuntimeProof
		}
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !ok || (uint64(stat.Dev) == identity.Device && stat.Ino == identity.Inode) {
			return errIdentityRuntimeProof
		}
		after, err := os.Stat(filepath.Join(base, "ns", "mnt"))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil || !os.SameFile(namespace, after) {
			return errIdentityRuntimeProof
		}
	}
	return nil
}

func identityRuntimeKnownHostConsumer(process string) (bool, error) {
	executable, err := os.Readlink(filepath.Join(process, "exe"))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return false, err
	}
	if errors.Is(err, os.ErrNotExist) {
		return false, os.ErrNotExist
	}
	if filepath.Base(strings.TrimSuffix(executable, " (deleted)")) == "borealis-api-backend-go" {
		return true, nil
	}
	file, err := os.Open(filepath.Join(process, "cmdline"))
	if err != nil {
		return false, err
	}
	raw, readErr := io.ReadAll(io.LimitReader(file, 16<<10+1))
	closeErr := file.Close()
	if readErr != nil || closeErr != nil || len(raw) > 16<<10 {
		return false, errIdentityRuntimeProof
	}
	arguments := bytes.Split(bytes.TrimSuffix(raw, []byte{0}), []byte{0})
	if len(arguments) == 0 {
		return false, nil
	}
	entrypoint := string(arguments[0])
	if entrypoint == "/usr/local/bin/borealis-api-backend" || entrypoint == "/usr/local/bin/borealis-job-scheduler" ||
		entrypoint == "/usr/local/bin/borealis-site-worker" {
		return true, nil
	}
	for index := 0; index+1 < len(arguments); index++ {
		if string(arguments[index]) == "-m" && string(arguments[index+1]) == "Data.Engine.services.job_scheduler.worker" {
			return true, nil
		}
	}
	return false, nil
}
