package clusterbootstrap

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
)

// Development is the compact composition selected by the operator. Each
// independently verified inventory binds the contents of its own payloads.
type developmentBinding struct {
	Schema      int               `json:"schema"`
	Channel     string            `json:"channel"`
	Platform    string            `json:"platform"`
	Repository  string            `json:"repository"`
	Source      string            `json:"source_sha"`
	Tree        string            `json:"source_tree"`
	Manifest    string            `json:"manifest_sha256"`
	Inventories map[string]string `json:"inventories"`
}

func developmentHash(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func (e Expected) developmentBinding() (developmentBinding, error) {
	var b developmentBinding
	raw := []byte(e.Development)
	if !repositoryPattern.MatchString(e.Repository) || !shaPattern.MatchString(e.SourceSHA) || e.SourceSHA == strings.Repeat("0", 40) || len(raw) == 0 || len(raw) > 4096 || e.AllowQualification || e.Release != "dev-"+developmentHash(raw) ||
		imageFields(raw, []string{"schema", "channel", "platform", "repository", "source_sha", "source_tree", "manifest_sha256", "inventories"}, nil) != nil ||
		json.Unmarshal(raw, &b) != nil || b.Schema != 2 || b.Channel != "development" || b.Platform != "linux-amd64" ||
		b.Repository != e.Repository || b.Source != e.SourceSHA || !shaPattern.MatchString(b.Tree) || b.Tree == strings.Repeat("0", 40) ||
		!digestPattern.MatchString(b.Manifest) || (len(b.Inventories) != 5 && len(b.Inventories) != 6) {
		return developmentBinding{}, ErrPreparationConfig
	}
	for _, name := range []string{"bootstrap", "image", "external", "k3s", "host_package"} {
		if !digestPattern.MatchString(b.Inventories[name]) {
			return developmentBinding{}, ErrPreparationConfig
		}
	}
	if len(b.Inventories) == 6 && !digestPattern.MatchString(b.Inventories["host_package_resolute"]) {
		return developmentBinding{}, ErrPreparationConfig
	}
	return b, nil
}

func (e Expected) acceptsInventory(family string, raw []byte) bool {
	if e.Development == "" {
		return e.Validate() == nil
	}
	b, err := e.developmentBinding()
	return err == nil && b.Inventories[family] == developmentHash(raw)
}

func (e Expected) packagingRelease() string {
	if e.Development != "" {
		return "dev-src-" + e.SourceSHA
	}
	return e.Release
}
