package clusterremote

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"encoding/json"
)

// Only compiled, release-bound package metadata enters this script. There is
// no request field for directory inventories and no second evidence receipt.
// Native fixtures use this same composition and the actual compiled list.
func persistentFilesystemLibraries() (string, error) {
	directories, err := clusterbootstrap.HostPackageDirectories()
	if err != nil {
		return "", ErrFilesystem
	}
	raw, err := json.Marshal(directories)
	if err != nil {
		return "", ErrFilesystem
	}
	return persistentNetworkLibraryScript + activeNetworkLibraryScript + systemdObservationLibraryScript + filesystemLibraryScript +
		"\nPFS_PACKAGE_DIRECTORIES = " + string(raw) + "\n" + filesystemHostPackageLibraryScript + persistentFilesystemLibraryScript, nil
}

func buildPersistentFilesystemScript() (string, error) {
	library, err := persistentFilesystemLibraries()
	if err != nil {
		return "", err
	}
	return library + persistentFilesystemMainScript, nil
}

const filesystemHostPackageLibraryScript = `
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
