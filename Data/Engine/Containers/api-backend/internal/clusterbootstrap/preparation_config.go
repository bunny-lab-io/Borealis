package clusterbootstrap

import (
	"bytes"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"net/netip"
	"net/url"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

const MaxPreparationConfigBytes = 64 << 10

var ErrPreparationConfig = errors.New("node preparation configuration invalid or changed; private data withheld")

// PreparationExpected comes from current controller/worker authority and the
// observed complete cohort. In particular, CIDRs must be observed on the source,
// not inferred from installer defaults or a joining host's environment.
// This is public binding metadata, not preparation permission.
type PreparationExpected struct {
	Source          Expected       `json:"source"`
	Target          SessionBinding `json:"target"`
	CohortSHA256    string         `json:"cohort_sha256"`
	TargetBootID    string         `json:"target_boot_id"`
	KubeSystemUID   string         `json:"kube_system_uid"`
	ControlPlaneVIP string         `json:"control_plane_vip"`
	EdgeVIP         string         `json:"edge_vip"`
	ManagementCIDR  string         `json:"management_cidr"`
	K3sVersion      string         `json:"k3s_version"`
	PodCIDR         string         `json:"pod_cidr"`
	ServiceCIDR     string         `json:"service_cidr"`
	PeerAddresses   []string       `json:"peer_addresses"`
}

func (e PreparationExpected) Validate() error {
	if e.Source.Validate() != nil || e.Target.Validate() != nil || !digestPattern.MatchString(e.CohortSHA256) || !nonzeroPreparationUUID(e.TargetBootID) ||
		!nonzeroPreparationUUID(e.KubeSystemUID) || len(e.K3sVersion) > 32 || !preparationK3s.MatchString(e.K3sVersion) || len(e.PeerAddresses) != 3 {
		return ErrPreparationConfig
	}
	management, ok := preparationPrefix(e.ManagementCIDR)
	pods, podsOK := preparationPrefix(e.PodCIDR)
	services, servicesOK := preparationPrefix(e.ServiceCIDR)
	if !ok || !podsOK || !servicesOK || management.Overlaps(pods) || management.Overlaps(services) || pods.Overlaps(services) {
		return ErrPreparationConfig
	}
	seen, own := map[string]bool{}, false
	for _, address := range e.PeerAddresses {
		if !preparationHost(management, address) || seen[address] || address == e.ControlPlaneVIP || address == e.EdgeVIP {
			return ErrPreparationConfig
		}
		seen[address], own = true, own || address == e.Target.Address
	}
	if !own || !preparationHost(management, e.ControlPlaneVIP) || !preparationHost(management, e.EdgeVIP) || !sort.StringsAreSorted(e.PeerAddresses) {
		return ErrPreparationConfig
	}
	return nil
}

var (
	preparationK3s      = regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+\+k3s[1-9][0-9]*$`)
	preparationDBName   = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,62}$`)
	preparationInteger  = regexp.MustCompile(`^(0|[1-9][0-9]{0,8})$`)
	preparationQuantity = regexp.MustCompile(`^[1-9][0-9]{0,8}(?:[kKmMgGtT](?:[iI]?[bB])?|[kKmMgGtT]i)?$`)
	preparationNumber   = regexp.MustCompile(`^(?:0|[1-9][0-9]{0,5})(?:\.[0-9]{1,6})?$`)
)

func nonzeroPreparationUUID(s string) bool {
	return sessionUUID.MatchString(s) && s != "00000000-0000-0000-0000-000000000000"
}

func preparationPrefix(value string) (netip.Prefix, bool) {
	p, err := netip.ParsePrefix(value)
	if err != nil || !p.Addr().Is4() || p != p.Masked() || p.String() != value || p.Bits() > 30 {
		return p, false
	}
	for _, block := range []string{"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16"} {
		private := netip.MustParsePrefix(block)
		if p.Bits() >= private.Bits() && private.Contains(p.Addr()) {
			return p, true
		}
	}
	return p, false
}

func preparationHost(p netip.Prefix, value string) bool {
	a, err := netip.ParseAddr(value)
	if err != nil || !a.Is4() || a.String() != value || !p.Contains(a) || a == p.Addr() {
		return false
	}
	// The address after a usable host must remain inside the subnet.
	return p.Contains(a.Next())
}

type preparationConfigWire struct {
	Version  int                 `json:"version"`
	Expected PreparationExpected `json:"expected"`
	Runtime  map[string]string   `json:"runtime"`
}

// PreparationConfiguration is immutable private input. Explicit Export and
// Environment return sensitive bytes; ordinary JSON/formatting cannot leak them.
// It neither installs configuration nor starts any standalone/bootstrap service.
type PreparationConfiguration struct {
	wire preparationConfigWire
	raw  []byte
}

func (PreparationConfiguration) String() string               { return "node preparation configuration [redacted]" }
func (PreparationConfiguration) GoString() string             { return "node preparation configuration [redacted]" }
func (PreparationConfiguration) MarshalJSON() ([]byte, error) { return nil, ErrPreparationConfig }

// NewPreparationConfiguration accepts only the selected shared settings below.
// Do not pass an entire runtime Secret: host paths, download overrides, identity
// material, login credentials and local process controls are not transferable.
func NewPreparationConfiguration(expected PreparationExpected, runtime map[string]string) (*PreparationConfiguration, error) {
	if expected.Validate() != nil || validatePreparationRuntime(runtime) != nil {
		return nil, ErrPreparationConfig
	}
	expected.PeerAddresses = slices.Clone(expected.PeerAddresses)
	copyRuntime := make(map[string]string, len(runtime))
	for key, value := range runtime {
		copyRuntime[key] = value
	}
	wire := preparationConfigWire{Version: 1, Expected: expected, Runtime: copyRuntime}
	raw, err := json.Marshal(wire)
	if err != nil || len(raw) > MaxPreparationConfigBytes {
		return nil, ErrPreparationConfig
	}
	return &PreparationConfiguration{wire: wire, raw: raw}, nil
}

// Import requires canonical private JSON produced by Export. Re-encoding rejects
// duplicates, case aliases, missing/null fields, unknown fields and alternate
// encodings before accepting any bytes. Expected is independently retained.
func ImportPreparationConfiguration(raw []byte, expected PreparationExpected) (*PreparationConfiguration, error) {
	if len(raw) == 0 || len(raw) > MaxPreparationConfigBytes || !utf8.Valid(raw) {
		return nil, ErrPreparationConfig
	}
	var wire preparationConfigWire
	if json.Unmarshal(raw, &wire) != nil || wire.Version != 1 || !equalPreparationExpected(wire.Expected, expected) {
		return nil, ErrPreparationConfig
	}
	config, err := NewPreparationConfiguration(wire.Expected, wire.Runtime)
	if err != nil || !bytes.Equal(config.raw, raw) {
		return nil, ErrPreparationConfig
	}
	return config, nil
}

func equalPreparationExpected(a, b PreparationExpected) bool {
	left, _ := json.Marshal(a)
	right, _ := json.Marshal(b)
	return bytes.Equal(left, right)
}

func (c *PreparationConfiguration) Export() ([]byte, error) {
	if c == nil || len(c.raw) == 0 {
		return nil, ErrPreparationConfig
	}
	return bytes.Clone(c.raw), nil
}

// Digest is a private equality guard, never public proof or source authority.
func (c *PreparationConfiguration) Digest() (string, error) {
	if c == nil || len(c.raw) == 0 {
		return "", ErrPreparationConfig
	}
	h := sha256.Sum256(c.raw)
	return hex.EncodeToString(h[:]), nil
}

// Environment renders literal key=value lines for fixed pre-join consumers.
// Never source/eval these bytes, place them in argv or run standalone deploy.
// The future journal-backed writer must install both private environment files
// and preserve the same configuration before any candidate consumers start.
func (c *PreparationConfiguration) Environment() ([]byte, error) {
	if c == nil || len(c.raw) == 0 {
		return nil, ErrPreparationConfig
	}
	values := make(map[string]string, len(c.wire.Runtime)+6)
	for key, value := range c.wire.Runtime {
		values[key] = value
	}
	e := c.wire.Expected
	peers := make([]string, len(e.PeerAddresses))
	for i, address := range e.PeerAddresses {
		peers[i] = address + "/32"
	}
	values["BOREALIS_K3S_PEER_CIDRS"] = strings.Join(peers, ",")
	values["BOREALIS_K3S_INSTALL_VERSION"] = e.K3sVersion
	values["BOREALIS_K3S_CLUSTER_CIDR"] = e.PodCIDR
	values["BOREALIS_K3S_SERVICE_CIDR"] = e.ServiceCIDR
	values["BOREALIS_CLUSTER_NODE_NAME"] = e.Target.Hostname
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var output strings.Builder
	for _, key := range keys {
		output.WriteString(key + "=" + values[key] + "\n")
	}
	return []byte(output.String()), nil
}

// PreparationRuntimeKeys is an explicit projection, not prefix acceptance.
// Optional profile overrides retain source values; absent values continue to
// derive from the mandatory inherited rank/reference-memory pair.
func PreparationRuntimeKeys() []string {
	keys := []string{"BOREALIS_PUBLIC_HOSTNAME", "BOREALIS_ENGINE_NETWORK_MODE", "BOREALIS_AGENT_ENGINE_CA_PEM_B64",
		"BOREALIS_CLUSTER_SIZING_RANK", "BOREALIS_CLUSTER_SIZING_MEMORY_MIB", "POSTGRES_DB", "POSTGRES_USER", "POSTGRES_PASSWORD",
		"BOREALIS_DATABASE_URL", "BOREALIS_OPERATOR_SECRET"}
	for key := range preparationTuning {
		keys = append(keys, key)
	}
	for _, service := range preparationServices {
		for _, suffix := range []string{"MEMORY_LIMIT", "CPU_LIMIT", "PIDS_LIMIT"} {
			keys = append(keys, "BOREALIS_"+service+"_"+suffix)
		}
	}
	sort.Strings(keys)
	return keys
}

var preparationServices = []string{"API_BACKEND", "JOB_SCHEDULER", "SITE_WORKER", "WEBUI_FRONTEND", "TRAEFIK_EDGE", "POSTGRES_DB", "REMOTE_DESKTOP_GUACD", "WIREGUARD_TUNNEL"}
var preparationTuning = map[string]string{
	"BOREALIS_DB_POOL_SIZE": "integer", "BOREALIS_DB_MAX_OVERFLOW": "integer", "BOREALIS_DB_CONNECT_TIMEOUT": "integer", "BOREALIS_DB_IDLE_IN_TXN_TIMEOUT_MS": "integer",
	"BOREALIS_POSTGRES_MAX_CONNECTIONS": "integer", "BOREALIS_POSTGRES_SHARED_BUFFERS": "quantity", "BOREALIS_POSTGRES_EFFECTIVE_CACHE_SIZE": "quantity",
	"BOREALIS_POSTGRES_WORK_MEM": "quantity", "BOREALIS_POSTGRES_MAINTENANCE_WORK_MEM": "quantity", "BOREALIS_POSTGRES_MAX_WORKER_PROCESSES": "integer",
	"BOREALIS_POSTGRES_MAX_PARALLEL_WORKERS": "integer", "BOREALIS_POSTGRES_MAX_PARALLEL_WORKERS_PER_GATHER": "integer", "BOREALIS_POSTGRES_AUTOVACUUM_MAX_WORKERS": "integer",
	"BOREALIS_POSTGRES_AUTOVACUUM_VACUUM_COST_LIMIT": "integer", "BOREALIS_POSTGRES_AUTOVACUUM_NAPTIME": "duration", "BOREALIS_POSTGRES_AUTOVACUUM_VACUUM_SCALE_FACTOR": "number",
	"BOREALIS_POSTGRES_AUTOVACUUM_ANALYZE_SCALE_FACTOR": "number", "BOREALIS_POSTGRES_MAX_WAL_SIZE": "quantity", "BOREALIS_POSTGRES_MIN_WAL_SIZE": "quantity",
	"BOREALIS_POSTGRES_EFFECTIVE_IO_CONCURRENCY": "integer", "BOREALIS_POSTGRES_WAL_COMPRESSION": "compression", "BOREALIS_POSTGRES_CHECKPOINT_TIMEOUT": "duration",
	"BOREALIS_POSTGRES_CHECKPOINT_COMPLETION_TARGET": "number", "BOREALIS_POSTGRES_RANDOM_PAGE_COST": "number", "BOREALIS_SITE_WORKER_SCHEDULED_CONCURRENCY": "integer",
	"BOREALIS_SITE_WORKER_ANSIBLE_CONCURRENCY": "integer",
}

func validatePreparationRuntime(runtime map[string]string) error {
	if len(runtime) < 10 || len(runtime) > 100 {
		return ErrPreparationConfig
	}
	allowed := PreparationRuntimeKeys()
	for key, value := range runtime {
		if !slices.Contains(allowed, key) || len(value) > 24<<10 || !utf8.ValidString(value) || strings.ContainsAny(value, "\x00\r\n") {
			return ErrPreparationConfig
		}
		kind := preparationTuning[key]
		for _, service := range preparationServices {
			switch key {
			case "BOREALIS_" + service + "_MEMORY_LIMIT":
				kind = "quantity"
			case "BOREALIS_" + service + "_CPU_LIMIT":
				kind = "number"
			case "BOREALIS_" + service + "_PIDS_LIMIT":
				kind = "integer"
			}
		}
		if kind != "" && !validPreparationTuning(kind, value) {
			return ErrPreparationConfig
		}
	}
	for _, key := range []string{"BOREALIS_PUBLIC_HOSTNAME", "BOREALIS_ENGINE_NETWORK_MODE", "BOREALIS_AGENT_ENGINE_CA_PEM_B64", "BOREALIS_CLUSTER_SIZING_RANK", "BOREALIS_CLUSTER_SIZING_MEMORY_MIB", "POSTGRES_DB", "POSTGRES_USER", "POSTGRES_PASSWORD", "BOREALIS_DATABASE_URL", "BOREALIS_OPERATOR_SECRET"} {
		if _, ok := runtime[key]; !ok {
			return ErrPreparationConfig
		}
	}
	host := runtime["BOREALIS_PUBLIC_HOSTNAME"]
	if len(host) > 253 || !strings.Contains(host, ".") {
		return ErrPreparationConfig
	}
	if _, err := netip.ParseAddr(host); err == nil {
		return ErrPreparationConfig
	}
	for _, part := range strings.Split(host, ".") {
		if !sessionHostname.MatchString(part) {
			return ErrPreparationConfig
		}
	}
	mode := runtime["BOREALIS_ENGINE_NETWORK_MODE"]
	if mode != "public" && mode != "local" {
		return ErrPreparationConfig
	}
	if !validPreparationCA(runtime["BOREALIS_AGENT_ENGINE_CA_PEM_B64"], mode == "local") {
		return ErrPreparationConfig
	}
	rank := runtime["BOREALIS_CLUSTER_SIZING_RANK"]
	memory := runtime["BOREALIS_CLUSTER_SIZING_MEMORY_MIB"]
	mib, err := strconv.ParseInt(memory, 10, 64)
	if len(rank) != 1 || rank[0] < '0' || rank[0] > '3' || !preparationInteger.MatchString(memory) || err != nil || mib < 1 {
		return ErrPreparationConfig
	}
	minimum := []int64{1, 16384, 32768, 65536}
	if mib < minimum[rank[0]-'0'] {
		return ErrPreparationConfig
	}
	if !preparationDBName.MatchString(runtime["POSTGRES_DB"]) || !preparationDBName.MatchString(runtime["POSTGRES_USER"]) {
		return ErrPreparationConfig
	}
	for _, key := range []string{"POSTGRES_PASSWORD", "BOREALIS_OPERATOR_SECRET"} {
		if len(runtime[key]) < 1 || len(runtime[key]) > 4096 {
			return ErrPreparationConfig
		}
	}
	u, err := url.Parse(runtime["BOREALIS_DATABASE_URL"])
	if err != nil || len(runtime["BOREALIS_DATABASE_URL"]) > 16<<10 || (u.Scheme != "postgres" && u.Scheme != "postgresql") || u.Opaque != "" || u.User == nil || u.Fragment != "" ||
		!slices.Contains([]string{"borealis-postgres-rw", "borealis-postgres-rw.borealis", "borealis-postgres-rw.borealis.svc", "borealis-postgres-rw.borealis.svc.cluster.local"}, u.Hostname()) || (u.Port() != "" && u.Port() != "5432") {
		return ErrPreparationConfig
	}
	password, exists := u.User.Password()
	if !exists || password != runtime["POSTGRES_PASSWORD"] || u.User.Username() != runtime["POSTGRES_USER"] || u.Path != "/"+runtime["POSTGRES_DB"] {
		return ErrPreparationConfig
	}
	query, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		return ErrPreparationConfig
	}
	for key, values := range query {
		if key != "sslmode" || len(values) != 1 || !slices.Contains([]string{"disable", "require", "verify-ca", "verify-full"}, values[0]) {
			return ErrPreparationConfig
		}
	}
	return nil
}

func validPreparationTuning(kind, value string) bool {
	if len(value) > 32 {
		return false
	}
	switch kind {
	case "integer":
		return preparationInteger.MatchString(value)
	case "number":
		return preparationNumber.MatchString(value)
	case "quantity":
		return preparationQuantity.MatchString(value)
	case "duration":
		d, err := time.ParseDuration(value)
		return err == nil && d > 0 && d <= 24*time.Hour
	case "compression":
		return slices.Contains([]string{"on", "off", "pglz", "lz4", "zstd"}, value)
	}
	return false
}

func validPreparationCA(encoded string, required bool) bool {
	if encoded == "" {
		return !required
	}
	raw, err := base64.StdEncoding.Strict().DecodeString(encoded)
	if err != nil || len(raw) > 16<<10 || base64.StdEncoding.EncodeToString(raw) != encoded {
		return false
	}
	count := 0
	for len(bytes.TrimSpace(raw)) > 0 {
		block, rest := pem.Decode(raw)
		if block == nil || block.Type != "CERTIFICATE" || len(block.Headers) != 0 {
			return false
		}
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil || !cert.IsCA || !cert.BasicConstraintsValid || cert.KeyUsage&x509.KeyUsageCertSign == 0 || time.Now().Before(cert.NotBefore) || !time.Now().Before(cert.NotAfter) {
			return false
		}
		// pem.Decode ignores leading text; accept only certificate PEM/whitespace.
		if !bytes.Equal(bytes.TrimSpace(raw[:len(raw)-len(rest)]), bytes.TrimSpace(pem.EncodeToMemory(block))) {
			return false
		}
		raw, count = rest, count+1
		if count > 8 {
			return false
		}
	}
	return count > 0
}
