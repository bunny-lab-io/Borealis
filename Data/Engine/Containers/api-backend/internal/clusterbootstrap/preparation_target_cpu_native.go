package clusterbootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os/exec"
	"time"
)

// ObservePreparationTargetCPU reads the fixed CPU/MemTotal contract twice under
// current preparation authority. It never invokes a package manager, systemd,
// K3s or Engine. Installer custody must independently enforce the pinned plan.
func ObservePreparationTargetCPU(ctx context.Context, k3sVersion string, check func(context.Context) error) (PreparationTargetCPU, error) {
	return observePreparationTargetCPU(ctx, k3sVersion, check, preparationTargetCPUNativeScript)
}

// VerifyPreparationTargetCPU rejects drift from the approved native observation
// before the caller crosses its separately authorized mutation boundary.
func VerifyPreparationTargetCPU(ctx context.Context, expectedCPU PreparationTargetCPU, k3sVersion string, check func(context.Context) error) error {
	return verifyPreparationTargetCPU(ctx, expectedCPU, k3sVersion, check, preparationTargetCPUNativeScript)
}

func verifyPreparationTargetCPU(ctx context.Context, expectedCPU PreparationTargetCPU, k3sVersion string, check func(context.Context) error, script string) error {
	if expectedCPU.Validate(k3sVersion) != nil {
		return ErrPreparationConfig
	}
	observed, err := observePreparationTargetCPU(ctx, k3sVersion, check, script)
	if err != nil {
		return err
	}
	if observed != expectedCPU {
		return ErrPreparationConfig
	}
	return nil
}

func observePreparationTargetCPU(parent context.Context, k3sVersion string, check func(context.Context) error, script string) (PreparationTargetCPU, error) {
	var invalid PreparationTargetCPU
	plan, err := PreparationTargetCPUPlanDigest(k3sVersion)
	if err != nil || parent == nil || check == nil {
		return invalid, ErrPreparationConfig
	}
	ctx, cancel := context.WithTimeout(parent, 10*time.Second)
	defer cancel()
	if ctx.Err() != nil || check(ctx) != nil || ctx.Err() != nil {
		return invalid, ErrSessionAuthority
	}
	request, err := json.Marshal(struct {
		Version    int    `json:"version"`
		PlanSHA256 string `json:"plan_sha256"`
	}{PreparationTargetCPUVersion, plan})
	if err != nil {
		return invalid, ErrPreparationConfig
	}
	command := exec.CommandContext(ctx, "/usr/bin/python3", "-I", "-B", "-c", script)
	command.Env = []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "LC_ALL=C"}
	command.Stdin = bytes.NewReader(request)
	output := &boundedOutput{limit: 32 << 10}
	command.Stdout, command.Stderr = output, io.Discard
	command.WaitDelay = time.Second
	runErr := command.Run()
	if ctx.Err() != nil || check(ctx) != nil || ctx.Err() != nil {
		return invalid, ErrSessionAuthority
	}
	if runErr != nil {
		return invalid, ErrPreparationConfig
	}
	decoder := json.NewDecoder(bytes.NewReader(output.Bytes()))
	decoder.DisallowUnknownFields()
	var observed PreparationTargetCPU
	if decoder.Decode(&observed) != nil || decoder.Decode(new(any)) != io.EOF || observed.Validate(k3sVersion) != nil {
		return invalid, ErrPreparationConfig
	}
	return observed, nil
}

const preparationTargetCPUNativeScript = preparationTargetCPUNativeLibrary + `
if __name__ == "__main__":
    try:
        main()
    except BaseException:
        sys.exit(1)
`

const preparationTargetCPUNativeLibrary = `import hashlib, json, os, re, stat, sys

ROOT = "/"
EXPECTED_UID = 0
EXPECTED_GID = 0

def require(condition):
    if not condition:
        raise ValueError()

def canonical(value):
    return json.dumps(value, separators=(",", ":"), sort_keys=True, ensure_ascii=True).encode("ascii")

def digest(raw):
    return hashlib.sha256(raw).hexdigest()

def metadata(info):
    value = [info.st_dev, info.st_ino, info.st_mode, info.st_uid, info.st_gid]
    return value if stat.S_ISDIR(info.st_mode) else value + [info.st_nlink]

def safe(info, directory):
    require(info.st_uid == EXPECTED_UID and info.st_gid == EXPECTED_GID and not info.st_mode & 0o022)
    require(not info.st_mode & 0o7000)
    require(stat.S_ISDIR(info.st_mode) if directory else stat.S_ISREG(info.st_mode) and info.st_nlink == 1)

class Snapshot:
    def __init__(self):
        self.root = os.open(ROOT, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW | os.O_CLOEXEC)
        try:
            safe(os.fstat(self.root), True)
            self.parents = {"": metadata(os.fstat(self.root))}
        except BaseException:
            os.close(self.root)
            raise
        self.files = {}
        self.listings = {}
        self.absent = []

    def close(self):
        os.close(self.root)

    def directory(self, path):
        fd = os.dup(self.root)
        current = ""
        try:
            for part in path.split("/") if path else ():
                require(part not in ("", ".", ".."))
                child = os.open(part, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW | os.O_CLOEXEC, dir_fd=fd)
                os.close(fd)
                fd = child
                info = os.fstat(fd)
                safe(info, True)
                current = current + "/" + part if current else part
                identity = metadata(info)
                require(current not in self.parents or self.parents[current] == identity)
                self.parents[current] = identity
            return fd
        except BaseException:
            os.close(fd)
            raise

    def read(self, path, limit, projection=None):
        require(type(limit) is int and 0 < limit <= 1 << 20)
        parent, leaf = path.rsplit("/", 1)
        directory = self.directory(parent)
        try:
            before = os.stat(leaf, dir_fd=directory, follow_symlinks=False)
            safe(before, False)
            require(before.st_size <= limit)
            fd = os.open(leaf, os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK | os.O_CLOEXEC, dir_fd=directory)
            with os.fdopen(fd, "rb") as source:
                require(metadata(os.fstat(source.fileno())) == metadata(before))
                raw = source.read(limit + 1)
                require(len(raw) <= limit and metadata(os.fstat(source.fileno())) == metadata(before))
            require(metadata(os.stat(leaf, dir_fd=directory, follow_symlinks=False)) == metadata(before))
            proof_raw = raw if projection is None else projection(raw)
            require(type(proof_raw) is bytes)
            proof = [metadata(before), digest(proof_raw)]
            require(path not in self.files or self.files[path] == proof)
            self.files[path] = proof
            return raw
        finally:
            os.close(directory)

    def entries(self, path, limit, optional=False):
        require(type(limit) is int and 0 < limit <= 8192)
        try:
            fd = self.directory(path)
        except FileNotFoundError:
            require(optional)
            self.absent.append(path)
            return None
        try:
            names = []
            with os.scandir(fd) as scan:
                for item in scan:
                    require(len(names) < limit and re.fullmatch(r"[-_.A-Za-z0-9]{1,255}", item.name))
                    names.append(item.name)
            names.sort()
            require(path not in self.listings or self.listings[path] == names)
            self.listings[path] = names
            return names
        finally:
            os.close(fd)

` + PreparationTargetCPUObserverLibrary + `

def observe():
    snapshot = Snapshot()
    try:
        semantic = preparation_target_cpu(snapshot.read, snapshot.entries)
        require(metadata(os.stat(ROOT, follow_symlinks=False)) == snapshot.parents[""])
        proof = {"parents": snapshot.parents, "files": snapshot.files,
            "listings": snapshot.listings, "absent": snapshot.absent}
        return semantic, proof
    finally:
        snapshot.close()

def main():
    raw = sys.stdin.buffer.read(1025)
    require(0 < len(raw) <= 1024)
    request = json.loads(raw)
    require(type(request) is dict and set(request) == {"version", "plan_sha256"})
    require(type(request["version"]) is int and request["version"] == 1)
    require(type(request["plan_sha256"]) is str and re.fullmatch(r"[0-9a-f]{64}", request["plan_sha256"]))
    first = observe()
    second = observe()
    require(first == second)
    semantic = first[0]
    result = {"version": 1, "plan_sha256": request["plan_sha256"],
        "online_cpus": semantic["online_cpus"], "allocatable_milli": semantic["allocatable_milli"],
        "memory_kib": semantic["memory_kib"],
        "observation_sha256": digest(b"borealis-preparation-target-cpu-v1\n" + canonical(semantic))}
    sys.stdout.buffer.write(canonical(result) + b"\n")
`
