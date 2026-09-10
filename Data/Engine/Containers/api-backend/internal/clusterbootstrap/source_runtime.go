package clusterbootstrap

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"maps"
	"unicode/utf8"
)

// PreparationRuntimeSecret is private in-memory source evidence. The full
// Kubernetes Secret never enters an operation payload, Job result or log.
type PreparationRuntimeSecret struct {
	uid, revision string
	digest        [32]byte
	settings      map[string]string
}

func (PreparationRuntimeSecret) String() string               { return "preparation runtime source [redacted]" }
func (PreparationRuntimeSecret) GoString() string             { return "preparation runtime source [redacted]" }
func (PreparationRuntimeSecret) MarshalJSON() ([]byte, error) { return nil, ErrPreparationConfig }

// Settings returns a private copy for NewPreparationConfiguration. It must
// never be sourced, logged, placed in argv or sent through ordinary Job output.
func (s PreparationRuntimeSecret) Settings() map[string]string { return maps.Clone(s.settings) }

func (s PreparationRuntimeSecret) SameObservation(other PreparationRuntimeSecret) bool {
	return s.uid != "" && s.uid == other.uid && s.revision == other.revision && s.digest == other.digest
}

func ParsePreparationRuntimeSecret(raw []byte) (PreparationRuntimeSecret, error) {
	fail := func() (PreparationRuntimeSecret, error) { return PreparationRuntimeSecret{}, ErrPreparationConfig }
	object, err := sourceJSONObject(raw, 2<<20)
	if err != nil {
		return fail()
	}
	var version, kind, secretType string
	if json.Unmarshal(object["apiVersion"], &version) != nil || version != "v1" ||
		json.Unmarshal(object["kind"], &kind) != nil || kind != "Secret" ||
		json.Unmarshal(object["type"], &secretType) != nil || secretType != "Opaque" {
		return fail()
	}
	metadata, err := sourceJSONObject(object["metadata"], 1<<20)
	if err != nil {
		return fail()
	}
	var uid, revision, name, namespace string
	if json.Unmarshal(metadata["uid"], &uid) != nil || !nonzeroPreparationUUID(uid) ||
		json.Unmarshal(metadata["resourceVersion"], &revision) != nil || len(revision) < 1 || len(revision) > 128 ||
		json.Unmarshal(metadata["name"], &name) != nil || name != "borealis-api-backend-runtime-env" ||
		json.Unmarshal(metadata["namespace"], &namespace) != nil || namespace != "borealis" {
		return fail()
	}
	for _, c := range revision {
		if c < 33 || c > 126 {
			return fail()
		}
	}
	if deleted, exists := metadata["deletionTimestamp"]; exists && !bytes.Equal(bytes.TrimSpace(deleted), []byte("null")) {
		return fail()
	}
	if _, exists := object["stringData"]; exists {
		return fail()
	}
	data, err := sourceJSONObject(object["data"], 2<<20)
	if err != nil {
		return fail()
	}
	settings := map[string]string{}
	for _, key := range PreparationRuntimeKeys() {
		value, exists := data[key]
		if !exists {
			continue
		} // Required/optional semantics belong to the configuration validator.
		var encoded string
		if json.Unmarshal(value, &encoded) != nil || bytes.Equal(bytes.TrimSpace(value), []byte("null")) || len(encoded) > MaxPreparationConfigBytes {
			return fail()
		}
		decoded, err := base64.StdEncoding.Strict().DecodeString(encoded)
		if err != nil || base64.StdEncoding.EncodeToString(decoded) != encoded || !utf8.Valid(decoded) {
			return fail()
		}
		settings[key] = string(decoded)
	}
	// Hash canonical complete data, including excluded settings. UID/revision
	// replacement or content drift invalidates the retained observation. Metadata
	// serialization order and managedFields do not select runtime settings.
	canonical, err := json.Marshal(data)
	if err != nil {
		return fail()
	}
	return PreparationRuntimeSecret{uid: uid, revision: revision, digest: sha256.Sum256(canonical), settings: settings}, nil
}
