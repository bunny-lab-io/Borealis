package clusterbootstrap

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

type DevelopmentAsset struct {
	ID     int64  `json:"-"`
	Bytes  int64  `json:"bytes"`
	SHA256 string `json:"sha256"`
	Path   string `json:"-"`
}

type DevelopmentCatalog struct {
	expected Expected
	assets   map[string]DevelopmentAsset
}

func (c *DevelopmentCatalog) Expected() Expected { return c.expected }

func (c *DevelopmentCatalog) Asset(name string) (DevelopmentAsset, bool) {
	asset, ok := c.assets[name]
	return asset, ok
}

func developmentPaths() map[string]string {
	paths := map[string]string{ManifestName: "bootstrap", BundleName: "bootstrap", ImageInventoryName: "image",
		ExternalInventoryName: "external", K3sInventoryName: "k3s", K3sBinaryName: "k3s", K3sInstallerName: "k3s", K3sArchiveName: "k3s",
		HostPackageInventoryName: "host_package", HostPackageArchiveName: "host_package"}
	for _, role := range ImageRoles() {
		paths[ImageAssetName(role)] = "image"
	}
	for _, pin := range ExternalImagePins() {
		paths[ExternalImageAssetName(pin.Reference)] = "external"
	}
	return paths
}

func ParseDevelopmentCatalog(expected Expected, raw []byte) (*DevelopmentCatalog, error) {
	b, err := expected.developmentBinding()
	if err != nil || len(raw) > 64<<10 || developmentHash(raw) != b.Manifest ||
		imageFields(raw, []string{"schema", "channel", "kind", "repository", "source_sha", "source_tree", "platform", "artifacts"}, nil) != nil {
		return nil, ErrPreparationConfig
	}
	var manifest struct {
		Schema     int                        `json:"schema"`
		Channel    string                     `json:"channel"`
		Kind       string                     `json:"kind"`
		Repository string                     `json:"repository"`
		Source     string                     `json:"source_sha"`
		Tree       string                     `json:"source_tree"`
		Platform   string                     `json:"platform"`
		Artifacts  map[string]json.RawMessage `json:"artifacts"`
	}
	paths := developmentPaths()
	if json.Unmarshal(raw, &manifest) != nil || manifest.Schema != 1 || manifest.Channel != b.Channel || manifest.Kind != "candidate" ||
		manifest.Repository != b.Repository || manifest.Source != b.Source || manifest.Tree != b.Tree || manifest.Platform != b.Platform ||
		len(manifest.Artifacts) != len(paths) {
		return nil, ErrPreparationConfig
	}
	c := &DevelopmentCatalog{expected: expected, assets: make(map[string]DevelopmentAsset, len(paths))}
	for i, name := range slices.Sorted(maps.Keys(paths)) {
		family := paths[name]
		path := family + "/" + name
		data := manifest.Artifacts[path]
		var obj DevelopmentAsset
		if imageFields(data, []string{"bytes", "sha256"}, nil) != nil || json.Unmarshal(data, &obj) != nil ||
			obj.Bytes < 1 || obj.Bytes > MaxImageArchiveBytes || !digestPattern.MatchString(obj.SHA256) {
			return nil, ErrPreparationConfig
		}
		if strings.HasSuffix(name, ".json") && obj.SHA256 != b.Inventories[family] {
			return nil, ErrPreparationConfig
		}
		obj.ID, obj.Path = int64(i+1), path
		c.assets[name] = obj
	}
	return c, nil
}

func readCandidateFile(root *os.Root, path string, max int64) ([]byte, error) {
	info, err := root.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() < 1 || info.Size() > max {
		return nil, ErrPreparationConfig
	}
	file, err := root.Open(path)
	if err != nil {
		return nil, ErrPreparationConfig
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(opened, info) {
		return nil, ErrPreparationConfig
	}
	data, err := io.ReadAll(io.LimitReader(file, info.Size()+1))
	if err != nil || int64(len(data)) != info.Size() {
		return nil, ErrPreparationConfig
	}
	return data, nil
}

// The root is a privately provisioned read-only mount. The caller supplies an
// independently selected candidate ID and source SHA. Directory names do not
// grant authority to build, stage an operation or modify a node.
func ReadDevelopmentCatalog(parent, id, sha, repo string) (*DevelopmentCatalog, []byte, error) {
	if !filepath.IsAbs(parent) || !strings.HasPrefix(id, "dev-") || !digestPattern.MatchString(strings.TrimPrefix(id, "dev-")) {
		return nil, nil, ErrPreparationConfig
	}
	root, err := os.OpenRoot(filepath.Join(parent, id))
	if err != nil {
		return nil, nil, ErrPreparationConfig
	}
	defer root.Close()
	binding, err := readCandidateFile(root, "development-identity.json", 4096)
	if err != nil {
		return nil, nil, err
	}
	expected := Expected{Repository: repo, Release: id, SourceSHA: sha, Development: string(binding)}
	raw, err := readCandidateFile(root, "development-manifest.json", 64<<10)
	if err != nil {
		return nil, nil, err
	}
	catalog, err := ParseDevelopmentCatalog(expected, raw)
	return catalog, raw, err
}

func (c *DevelopmentCatalog) OpenAsset(parent, name string) (*os.File, error) {
	obj, ok := c.Asset(name)
	if !ok || !filepath.IsAbs(parent) {
		return nil, ErrPreparationConfig
	}
	root, err := os.OpenRoot(filepath.Join(parent, c.expected.Release))
	if err != nil {
		return nil, ErrPreparationConfig
	}
	defer root.Close()
	info, err := root.Lstat(obj.Path)
	if err != nil || !info.Mode().IsRegular() || info.Size() != obj.Bytes {
		return nil, ErrPreparationConfig
	}
	file, err := root.Open(obj.Path)
	if err != nil {
		return nil, ErrPreparationConfig
	}
	opened, err := file.Stat()
	if err != nil || !os.SameFile(info, opened) {
		file.Close()
		return nil, ErrPreparationConfig
	}
	return file, nil
}

// VerifyReadOnly checks all packed bytes and applies the real native inventory
// parsers. Staging and native installation retain their own checks and custody.
func (c *DevelopmentCatalog) VerifyReadOnly(ctx context.Context, parent string) error {
	for name, obj := range c.assets {
		err := func() error {
			file, err := c.OpenAsset(parent, name)
			if err != nil {
				return err
			}
			defer file.Close()
			reader := io.LimitReader(contextReader{ctx, file}, obj.Bytes+1)
			if !strings.HasSuffix(name, ".json") {
				h := sha256.New()
				n, err := io.Copy(h, reader)
				if err != nil || n != obj.Bytes || hex.EncodeToString(h.Sum(nil)) != obj.SHA256 {
					return ErrPreparationConfig
				}
				return nil
			}
			if obj.Bytes > MaxImageInventoryBytes {
				return ErrPreparationConfig
			}
			raw, err := io.ReadAll(reader)
			if err != nil || int64(len(raw)) != obj.Bytes || developmentHash(raw) != obj.SHA256 {
				return ErrPreparationConfig
			}
			switch name {
			case ManifestName:
				var manifest *Manifest
				manifest, err = ParseManifest(raw, c.expected)
				bundle := c.assets[BundleName]
				if err == nil && (bundle.SHA256 != manifest.BundleSHA256() || bundle.Bytes != manifest.BundleSize()) {
					return ErrPreparationConfig
				}
			case ImageInventoryName:
				var inv *ImageInventory
				inv, err = ParseImageInventory(raw, c.expected)
				if err == nil {
					for _, i := range inv.Images() {
						a := c.assets[ImageAssetName(i.Role)]
						if a.SHA256 != i.ArchiveSHA256 || a.Bytes != i.ArchiveBytes {
							return ErrImageArchive
						}
					}
				}
			case ExternalInventoryName:
				var inv *ExternalImageInventory
				inv, err = ParseExternalImageInventory(raw, c.expected)
				if err == nil {
					for _, i := range inv.Images() {
						a := c.assets[ExternalImageAssetName(i.Reference)]
						if a.SHA256 != i.ArchiveSHA256 || a.Bytes != i.ArchiveBytes {
							return ErrImageArchive
						}
					}
				}
			case K3sInventoryName:
				_, err = ParseK3sInventory(raw, c.expected, K3sPins().Version)
				pins := K3sPins()
				for name, pin := range map[string]K3sAssetPin{K3sBinaryName: pins.Binary, K3sInstallerName: pins.Installer, K3sArchiveName: pins.Archive} {
					a := c.assets[name]
					if a.SHA256 != pin.SHA256 || a.Bytes != pin.Size {
						return ErrImageArchive
					}
				}
			case HostPackageInventoryName:
				var inv *HostPackageInventory
				inv, err = ParseHostPackageInventory(raw, c.expected)
				if err == nil {
					a := c.assets[HostPackageArchiveName]
					if a.SHA256 != inv.Archive().SHA256 || a.Bytes != inv.Archive().Size {
						return ErrImageArchive
					}
				}
			default:
				return ErrPreparationConfig
			}
			return err
		}()
		if err != nil {
			return err
		}
	}
	return ctx.Err()
}
