// Package clusterbootstrap verifies immutable node bootstrap assets. It does
// not authorize publication, SSH credentials, membership or host mutations.
package clusterbootstrap

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"regexp"
	"strings"
	"unicode/utf8"
)

const (
	BundleName       = "borealis-node-bootstrap-linux-amd64.tar.gz"
	ManifestName     = "borealis-node-bootstrap-linux-amd64.json"
	ManagerPath      = "bin/borealis-node-manager"
	MaxManifestBytes = 16 << 10
	MaxBundleBytes   = 256 << 20
	MaxExpandedBytes = 1 << 30
	MaxEntries       = 20000
)

var (
	repositoryPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,99}/[A-Za-z0-9][A-Za-z0-9_.-]{0,99}$`)
	releasePattern    = regexp.MustCompile(`^[0-9]{4}\.[0-9]{1,2}\.[0-9]+(?:\.[0-9]+)?(?:-rc\.[1-9][0-9]*)?$`)
	shaPattern        = regexp.MustCompile(`^[0-9a-f]{40}$`)
	digestPattern     = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

// Expected comes from an independently authorized operation and freshly
// resolved immutable GitHub tag, never from the downloaded manifest itself.
type Expected struct {
	Repository         string
	Release            string
	SourceSHA          string
	AllowQualification bool
}

func (e Expected) Validate() error {
	if !repositoryPattern.MatchString(e.Repository) || len(e.Release) > 64 || !releasePattern.MatchString(e.Release) ||
		!shaPattern.MatchString(e.SourceSHA) || e.SourceSHA == strings.Repeat("0", 40) ||
		(strings.Contains(e.Release, "-rc.") && !e.AllowQualification) {
		return errors.New("node bootstrap expected release identity invalid")
	}
	return nil
}

type fileIdentity struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

type identity struct {
	SchemaVersion int          `json:"schema_version"`
	Repository    string       `json:"repository"`
	Release       string       `json:"release"`
	SourceSHA     string       `json:"source_sha"`
	SourceTree    string       `json:"source_tree"`
	Platform      string       `json:"platform"`
	GoVersion     string       `json:"go_version"`
	NodeManager   fileIdentity `json:"node_manager"`
}

type assetIdentity struct {
	Name   string `json:"name"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
	URL    string `json:"url"`
}

// Manifest can only be initialized through ParseManifest. It contains public
// release metadata; private fields prevent callers from skipping validation.
type Manifest struct {
	identity identity
	asset    assetIdentity
}

func (m *Manifest) BundleSize() int64    { return m.asset.Size }
func (m *Manifest) BundleSHA256() string { return m.asset.SHA256 }
func (m *Manifest) SourceSHA() string    { return m.identity.SourceSHA }

// AssetURL is canonical and never provides a download authorization. HTTP
// callers must use the independently validated GitHub release asset ID.
func AssetURL(e Expected, name string) string {
	return "https://github.com/" + e.Repository + "/releases/download/" + e.Release + "/" + name
}

var identityFields = []string{"schema_version", "repository", "release", "source_sha", "source_tree", "platform", "go_version", "node_manager"}

func ParseManifest(raw []byte, expected Expected) (*Manifest, error) {
	if err := expected.Validate(); err != nil {
		return nil, err
	}
	fields, err := exactObject(raw, append(append([]string{}, identityFields...), "asset"))
	if err != nil {
		return nil, err
	}
	if _, err := exactObject(fields["asset"], []string{"name", "size", "sha256", "url"}); err != nil {
		return nil, err
	}
	// Decode identity separately so inner and outer payloads use the same rules.
	delete(fields, "asset")
	inner, _ := json.Marshal(fields)
	i, err := parseIdentity(inner, expected)
	if err != nil {
		return nil, err
	}
	var outer struct {
		Asset assetIdentity `json:"asset"`
	}
	if json.Unmarshal(raw, &outer) != nil || outer.Asset.Name != BundleName ||
		outer.Asset.Size < 1 || outer.Asset.Size > MaxBundleBytes || !digestPattern.MatchString(outer.Asset.SHA256) ||
		outer.Asset.URL != AssetURL(expected, BundleName) {
		return nil, errors.New("node bootstrap archive identity invalid")
	}
	return &Manifest{identity: i, asset: outer.Asset}, nil
}

func parseIdentity(raw []byte, expected Expected) (identity, error) {
	var i identity
	fields, err := exactObject(raw, identityFields)
	if err != nil {
		return i, err
	}
	if _, err = exactObject(fields["node_manager"], []string{"path", "size", "sha256"}); err != nil {
		return i, err
	}
	if json.Unmarshal(raw, &i) != nil || i.SchemaVersion != 1 || i.Repository != expected.Repository ||
		i.Release != expected.Release || i.SourceSHA != expected.SourceSHA || !shaPattern.MatchString(i.SourceTree) ||
		i.SourceTree == strings.Repeat("0", 40) || i.Platform != "linux-amd64" || i.GoVersion != "go1.25.12" ||
		i.NodeManager.Path != ManagerPath || i.NodeManager.Size < 1 || i.NodeManager.Size > MaxBundleBytes || !digestPattern.MatchString(i.NodeManager.SHA256) {
		return identity{}, errors.New("node bootstrap source or binary identity invalid")
	}
	return i, nil
}

// Exact key matching rejects duplicates (including escaped aliases), unknown
// keys, missing/null values, case aliases, invalid UTF-8 and trailing JSON.
func exactObject(raw []byte, names []string) (map[string]json.RawMessage, error) {
	invalid := errors.New("node bootstrap manifest object invalid")
	if len(raw) == 0 || len(raw) > MaxManifestBytes || !utf8.Valid(raw) {
		return nil, invalid
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	if token, err := d.Token(); err != nil || token != json.Delim('{') {
		return nil, invalid
	}
	allowed := make(map[string]bool, len(names))
	for _, name := range names {
		allowed[name] = true
	}
	fields := make(map[string]json.RawMessage, len(names))
	for d.More() {
		token, err := d.Token()
		name, ok := token.(string)
		if err != nil || !ok || !allowed[name] || fields[name] != nil {
			return nil, invalid
		}
		var value json.RawMessage
		if d.Decode(&value) != nil || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return nil, invalid
		}
		fields[name] = value
	}
	if token, err := d.Token(); err != nil || token != json.Delim('}') || len(fields) != len(names) || d.Decode(&struct{}{}) != io.EOF {
		return nil, invalid
	}
	return fields, nil
}
