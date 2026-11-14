package clusterbootstrap

import (
	"context"
	"encoding/json"
	"slices"
)

// Reviewed data.tar members, grouped before any target alias resolution. File
// bytes exclude symlinks; link bytes measure their UTF-8 target text separately.
// Entries include directories and links, but exclude the already-existing '/'.
type HostPackagePathContent struct {
	Path      string `json:"path"`
	FileBytes int64  `json:"file_bytes"`
	LinkBytes int64  `json:"link_bytes"`
	Entries   int64  `json:"entries"`
}

// Control tar content is authenticated metadata only. Dpkg can publish or copy
// it while generating other state; these counts are not installed/transient
// capacity and are deliberately excluded from InstalledContentDemand.
type HostPackageControlContent struct {
	FileBytes   int64 `json:"file_bytes"`
	LinkBytes   int64 `json:"link_bytes"`
	Entries     int64 `json:"entries"`
	RootEntries int64 `json:"root_entries"`
}

var hostPackageDataPaths = [...]string{"/etc", "/lib", "/sbin", "/usr", "/var"}

func hostPackageContentFields(raw []byte) error {
	if imageFields(raw, []string{"name", "version", "architecture", "file", "phase", "size", "sha256", "url", "file_bytes", "entries", "data_paths", "data_root_entries", "control"}, nil) != nil {
		return ErrImageArchive
	}
	var fields struct {
		DataPaths []json.RawMessage `json:"data_paths"`
		Control   json.RawMessage   `json:"control"`
	}
	if json.Unmarshal(raw, &fields) != nil || imageFields(fields.Control, []string{"file_bytes", "link_bytes", "entries", "root_entries"}, nil) != nil {
		return ErrImageArchive
	}
	for _, row := range fields.DataPaths {
		if imageFields(row, []string{"path", "file_bytes", "link_bytes", "entries"}, nil) != nil {
			return ErrImageArchive
		}
	}
	return nil
}

func validateHostPackageContent(pin HostPackagePin) error {
	if pin.DataRootEntries != 1 || len(pin.DataPaths) < 1 || len(pin.DataPaths) > len(hostPackageDataPaths) {
		return ErrImageArchive
	}
	var files, links, entries int64
	for i, row := range pin.DataPaths {
		if !slices.Contains(hostPackageDataPaths[:], row.Path) || i > 0 && pin.DataPaths[i-1].Path >= row.Path ||
			row.FileBytes < 0 || row.FileBytes > MaxImageExpandedBytes || row.LinkBytes < 0 || row.LinkBytes > MaxImageExpandedBytes || row.Entries < 1 || row.Entries > MaxImageEntries {
			return ErrImageArchive
		}
		files, links, entries = files+row.FileBytes, links+row.LinkBytes, entries+row.Entries
	}
	c := pin.Control
	if files != pin.FileBytes || entries+pin.DataRootEntries != pin.Entries || files+links > MaxImageExpandedBytes ||
		c.FileBytes < 1 || c.FileBytes > MaxImageExpandedBytes || c.LinkBytes < 0 || c.LinkBytes > MaxImageExpandedBytes ||
		c.FileBytes+c.LinkBytes > MaxImageExpandedBytes || c.RootEntries != 1 || c.Entries < 1 || c.Entries >= MaxImageEntries {
		return ErrImageArchive
	}
	return nil
}

type HostPackageDestinationDemand struct {
	Path           string
	Bytes, Entries uint64
}

type HostPackagePathAlias struct{ Path, Destination string }

// Requirements, never observations. Consumers must prove these exact current
// target aliases, safe descendant ancestry and persistent mount correspondence
// before resolving raw /lib or /sbin demand onto /usr. No silent usrmerge guess.
func HostPackagePathAliases() []HostPackagePathAlias {
	return []HostPackagePathAlias{{"/lib", "/usr/lib"}, {"/sbin", "/usr/sbin"}}
}

// InstalledContentDemand covers all locked data members conservatively, even
// already-installed packages and repeated directory entries. It excludes tar
// root entries, control contents, dpkg/generated/transient and service growth.
// Raw directory rows still require target path/mount/alias evidence; this method
// neither resolves aliases nor supplies complete persistent-storage readiness.
func (s *HostPackageSet) InstalledContentDemand(ctx context.Context, expected Expected, check func(context.Context) error) (demand []HostPackageDestinationDemand, result error) {
	if s == nil {
		return nil, ErrImageArchive
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	defer func() { s.failed = s.failed || result != nil }()
	if err := s.proveDemandOwner(ctx, expected, check); err != nil {
		return nil, err
	}
	rows := map[string]HostPackageDestinationDemand{}
	for _, pin := range s.inventory.wire.Packages {
		for _, part := range pin.DataPaths {
			row := rows[part.Path]
			row.Path = part.Path
			row.Bytes += uint64(part.FileBytes + part.LinkBytes)
			row.Entries += uint64(part.Entries)
			rows[part.Path] = row
		}
	}
	for _, path := range hostPackageDataPaths {
		if row, ok := rows[path]; ok {
			demand = append(demand, row)
		}
	}
	asset := s.inventory.Archive()
	if !stagedImageFileMatches(s.root, asset.Name, s.file, asset.Size) || imageBoundary(ctx, check) != nil {
		return nil, ErrSessionAuthority
	}
	return demand, nil
}
