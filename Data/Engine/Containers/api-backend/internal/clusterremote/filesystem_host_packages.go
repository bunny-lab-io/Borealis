package clusterremote

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"encoding/json"
)

// Only compiled, release-bound package metadata enters this script. There is
// no request field for directory inventories and no second evidence receipt.
// Native fixtures use this same composition and the actual compiled list.
func persistentFilesystemLibraries(version string) (string, error) {
	suite, err := clusterbootstrap.HostPackageSuiteForVersion(version)
	if err != nil {
		return "", ErrFilesystem
	}
	plan, _, err := clusterbootstrap.HostPackagePlanForSuite(suite)
	if err != nil {
		return "", ErrFilesystem
	}
	raw, err := json.Marshal(plan.DataDirectories)
	if err != nil {
		return "", ErrFilesystem
	}
	copyPaths, err := json.Marshal(packageCopyPaths)
	if err != nil {
		return "", err
	}
	return persistentNetworkLibraryScript + activeNetworkLibraryScript + systemdObservationLibraryScript + filesystemLibraryScript +
		"\nPFS_PACKAGE_DIRECTORIES = " + string(raw) + "\nPFS_COPY_PATHS = " + string(copyPaths) + "\n" + filesystemHostPackageLibraryScript + packageProfileLibraryScript + persistentFilesystemLibraryScript, nil
}

func buildPersistentFilesystemScript(packageCopies bool, version string) (string, error) {
	library, err := persistentFilesystemLibraries(version)
	if err != nil {
		return "", err
	}
	if packageCopies {
		library += "\nPFS_OBSERVE_COPIES = True\n"
	}
	return library + persistentFilesystemMainScript, nil
}

// Mutable databases replaced during the observed Noble package transaction.
// The primary dpkg status database must exist on a supported target.
var packageCopyPaths = [...]string{
	"var/lib/dpkg/status",
	"var/lib/apt/extended_states",
	"var/cache/debconf/templates.dat",
	"var/lib/ucf/hashfile",
}

const filesystemHostPackageLibraryScript = `
PFS_OBSERVE_COPIES = False

def pfs_package_copies(root):
    # O_PATH inspects metadata without reading private databases or opening devices.
    device, _, _, _, _, mount = fs_node(root)
    states = {}
    for path in PFS_COPY_PATHS:
        parent, name = path.rsplit("/", 1)
        try:
            fd = directory(root, parent)
            try:
                leaf = os.open(name, os.O_PATH | os.O_NOFOLLOW | os.O_CLOEXEC, dir_fd=fd)
            finally:
                os.close(fd)
        except FileNotFoundError:
            if path == PFS_COPY_PATHS[0]:
                raise
            states[path] = None
            continue
        try:
            info = os.fstat(leaf)
            if (not stat.S_ISREG(info.st_mode) or info.st_uid != EXPECTED_UID or info.st_mode & 0o022
                    or info.st_nlink != 1 or info.st_dev != device or fs_mnt_id(leaf) != mount):
                raise ValueError()
            size = fs_integer(info.st_size, 1 if path == PFS_COPY_PATHS[0] else 0)
            states[path] = (size, info.st_ino, info.st_mode, info.st_uid, info.st_gid, info.st_mtime_ns, info.st_ctime_ns)
        finally:
            os.close(leaf)
    return states

def pfs_package_directories(root, paths):
    # Prove only roots actually charged by the fixed package projection. Raw
    # /lib and /sbin are normalized in reviewed metadata; pfs_files separately
    # proves the live aliases. No other symlink may redirect a package ancestor.
    selected = set(paths) & {"/etc", "/usr", "/var"}
    if not selected:
        return []
    initial = fs_node(root)
    checked(os.fstat(root), True)
    states = {}
    for destination in PFS_PACKAGE_DIRECTORIES:
        if "/"+destination.split("/")[1] not in selected:
            continue
        fd, prefix, missing = os.dup(root), "", False
        try:
            for part in destination.split("/")[1:]:
                prefix += "/"+part
                state = None
                if not missing:
                    try:
                        child = os.open(part, FS_OPEN, dir_fd=fd)
                    except FileNotFoundError:
                        missing = True
                    else:
                        os.close(fd)
                        fd = child
                        checked(os.fstat(fd), True)
                        state = fs_node(fd)
                        if state[0] != initial[0] or state[5] != initial[5]:
                            raise ValueError()
                if prefix in states and states[prefix] != state:
                    raise ValueError()
                states[prefix] = state
        finally:
            os.close(fd)
    if fs_node(root) != initial:
        raise ValueError()
    return sorted(states.items())
`
