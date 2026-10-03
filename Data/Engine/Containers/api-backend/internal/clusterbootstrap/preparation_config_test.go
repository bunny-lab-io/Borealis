package clusterbootstrap

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net/url"
	"strings"
	"testing"
	"time"
)

func preparationFixture() (PreparationExpected, map[string]string) {
	r := sessionFixture()
	e := PreparationExpected{Source: Expected{r.Repository, r.Release, r.SourceSHA, true}, Target: r.Binding,
		CohortSHA256: strings.Repeat("d", 64),
		TargetBootID: "55555555-5555-4555-8555-555555555555", KubeSystemUID: "66666666-6666-4666-8666-666666666666",
		ControlPlaneVIP: "192.168.3.248", EdgeVIP: "192.168.3.248", ManagementCIDR: "192.168.3.0/24", K3sVersion: "v1.36.3+k3s1",
		PodCIDR: "10.42.0.0/16", ServiceCIDR: "10.43.0.0/16", PeerAddresses: []string{"192.168.3.250", "192.168.3.251", "192.168.3.252"}}
	password := "  private-test ' \" $() `printf sentinel` \\ = & ; <>  "
	u := &url.URL{Scheme: "postgresql", User: url.UserPassword("borealis", password), Host: "borealis-postgres-rw.borealis.svc:5432", Path: "/borealis", RawQuery: "sslmode=disable"}
	runtime := map[string]string{"BOREALIS_PUBLIC_HOSTNAME": "engine.example.test", "BOREALIS_ENGINE_NETWORK_MODE": "public", "BOREALIS_AGENT_ENGINE_CA_PEM_B64": "",
		"BOREALIS_CLUSTER_SIZING_RANK": "1", "BOREALIS_CLUSTER_SIZING_MEMORY_MIB": "16384", "POSTGRES_DB": "borealis", "POSTGRES_USER": "borealis", "POSTGRES_PASSWORD": password,
		"BOREALIS_DATABASE_URL": u.String(), "BOREALIS_OPERATOR_SECRET": "  operator-test $() ' ; = ", "BOREALIS_POSTGRES_DB_MEMORY_LIMIT": "4096m",
		"BOREALIS_API_BACKEND_CPU_LIMIT": "1.50", "BOREALIS_POSTGRES_AUTOVACUUM_NAPTIME": "30s"}
	return e, runtime
}

func TestPreparationConfigurationPreservesPrivateSyntaxAndBindings(t *testing.T) {
	e, runtime := preparationFixture()
	c, err := NewPreparationConfiguration(e, runtime)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := c.Export()
	d, _ := c.Digest()
	if d != digest(raw) {
		t.Fatal("configuration digest differs from exact private bytes")
	}
	copy, err := ImportPreparationConfiguration(raw, e)
	if err != nil {
		t.Fatal(err)
	}
	env, err := copy.Environment()
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"POSTGRES_PASSWORD", "BOREALIS_DATABASE_URL", "BOREALIS_OPERATOR_SECRET", "BOREALIS_POSTGRES_DB_MEMORY_LIMIT", "BOREALIS_CLUSTER_SIZING_MEMORY_MIB"} {
		if !bytes.Contains(env, []byte(key+"="+runtime[key]+"\n")) {
			t.Fatalf("literal value changed for %s", key)
		}
	}
	if !bytes.Contains(env, []byte("BOREALIS_K3S_PEER_CIDRS=192.168.3.250/32,192.168.3.251/32,192.168.3.252/32\n")) || !bytes.Contains(env, []byte("BOREALIS_K3S_INSTALL_VERSION=v1.36.3+k3s1\n")) {
		t.Fatal("complete pinned peer/K3s contract missing")
	}
	for _, key := range []string{"SECRET_KEY=", "K3S_TOKEN=", "BOREALIS_K3S_POSTGRES_ENABLED=", "BOREALIS_ENGINE_SECRET_PATH=", "BOREALIS_ENGINE_RUNTIME_OWNER_UID="} {
		if bytes.Contains(env, []byte(key)) {
			t.Fatal("unapproved runtime field exported")
		}
	}
	// Callers cannot mutate accepted state through original input or export.
	runtime["POSTGRES_PASSWORD"] = "changed"
	e.PeerAddresses[0] = "192.168.3.249"
	raw[0] = '!'
	after, _ := c.Digest()
	if after != d {
		t.Fatal("caller mutated accepted configuration")
	}
	for _, value := range []any{c, *c} {
		if _, err := json.Marshal(value); !errors.Is(err, ErrPreparationConfig) {
			t.Fatal("generic JSON accepted secret-bearing configuration")
		}
		for _, format := range []string{"%v", "%+v", "%#v", "%s"} {
			if strings.Contains(fmt.Sprintf(format, value), "private-test") || strings.Contains(fmt.Sprintf(format, value), "operator-test") {
				t.Fatal("ordinary formatting leaked private values")
			}
		}
	}
}

func TestPreparationConfigurationRejectsAmbiguousJSONAndRebinding(t *testing.T) {
	e, runtime := preparationFixture()
	c, _ := NewPreparationConfiguration(e, runtime)
	raw, _ := c.Export()
	for name, mutate := range map[string]func([]byte) []byte{
		"duplicate":         func(b []byte) []byte { return append([]byte(`{"version":1,`), b[1:]...) },
		"escaped duplicate": func(b []byte) []byte { return append([]byte(`{"\u0076ersion":1,`), b[1:]...) },
		"case alias":        func(b []byte) []byte { return bytes.Replace(b, []byte(`"version"`), []byte(`"Version"`), 1) },
		"unknown":           func(b []byte) []byte { return append([]byte(`{"extra":0,`), b[1:]...) },
		"nested duplicate": func(b []byte) []byte {
			return bytes.Replace(b, []byte(`"generation":7`), []byte(`"generation":7,"generation":7`), 1)
		},
		"null": func(b []byte) []byte {
			return bytes.Replace(b, []byte(`"BOREALIS_AGENT_ENGINE_CA_PEM_B64":""`), []byte(`"BOREALIS_AGENT_ENGINE_CA_PEM_B64":null`), 1)
		},
		"trailing":     func(b []byte) []byte { return append(b, []byte(`{}`)...) },
		"oversize":     func(b []byte) []byte { return append(b, bytes.Repeat([]byte(" "), MaxPreparationConfigBytes)...) },
		"invalid utf8": func(b []byte) []byte { return append(b, 0xff) },
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := ImportPreparationConfiguration(mutate(bytes.Clone(raw)), e); !errors.Is(err, ErrPreparationConfig) {
				t.Fatal("ambiguous private protocol accepted")
			}
		})
	}
	for name, mutate := range map[string]func(*PreparationExpected){
		"claim generation": func(e *PreparationExpected) { e.Target.Generation++ }, "claim holder": func(e *PreparationExpected) { e.Target.HolderID = e.Target.OperationID },
		"attempt": func(e *PreparationExpected) { e.Target.OperationAttempt++ }, "operation": func(e *PreparationExpected) { e.Target.OperationID = e.Target.HolderID },
		"host": func(e *PreparationExpected) { e.Target.Hostname = "other-host" }, "machine": func(e *PreparationExpected) { e.Target.MachineID = strings.Repeat("b", 32) },
		"boot": func(e *PreparationExpected) { e.TargetBootID = e.KubeSystemUID }, "source": func(e *PreparationExpected) { e.Source.SourceSHA = strings.Repeat("b", 40) },
		"Kubernetes": func(e *PreparationExpected) { e.KubeSystemUID = e.TargetBootID }, "SSH pin": func(e *PreparationExpected) {
			e.Target.HostKeyFingerprint = "SHA256:" + base64.RawStdEncoding.EncodeToString(bytes.Repeat([]byte{1}, 32))
		},
		"K3s": func(e *PreparationExpected) { e.K3sVersion = "v1.36.4+k3s1" }, "pod network": func(e *PreparationExpected) { e.PodCIDR = "10.44.0.0/16" },
	} {
		t.Run(name, func(t *testing.T) {
			changed, _ := preparationFixture()
			mutate(&changed)
			if _, err := ImportPreparationConfiguration(raw, changed); err == nil {
				t.Fatal("changed authority accepted")
			}
		})
	}
}

func TestPreparationConfigurationRejectsUnsafeNetworksAndRuntime(t *testing.T) {
	for name, mutate := range map[string]func(*PreparationExpected, map[string]string){
		"mutable source":               func(e *PreparationExpected, _ map[string]string) { e.Source.Release = "main" },
		"development source":           func(e *PreparationExpected, _ map[string]string) { e.Source.Release = "dev-" + e.Source.SourceSHA[:12] },
		"qualification without opt-in": func(e *PreparationExpected, _ map[string]string) { e.Source.AllowQualification = false },
		"missing source pods":          func(e *PreparationExpected, _ map[string]string) { e.PodCIDR = "" },
		"overlap":                      func(e *PreparationExpected, _ map[string]string) { e.ServiceCIDR = e.PodCIDR },
		"management overlap":           func(e *PreparationExpected, _ map[string]string) { e.PodCIDR = e.ManagementCIDR },
		"partly public prefix":         func(e *PreparationExpected, _ map[string]string) { e.PodCIDR = "172.0.0.0/8" },
		"unmasked prefix":              func(e *PreparationExpected, _ map[string]string) { e.PodCIDR = "10.42.1.0/16" },
		"duplicate peer":               func(e *PreparationExpected, _ map[string]string) { e.PeerAddresses[0] = e.PeerAddresses[1] },
		"incomplete pair":              func(e *PreparationExpected, _ map[string]string) { e.PeerAddresses = e.PeerAddresses[:2] },
		"VIP peer":                     func(e *PreparationExpected, _ map[string]string) { e.ControlPlaneVIP = e.Target.Address },
		"broadcast peer":               func(e *PreparationExpected, _ map[string]string) { e.PeerAddresses[2] = "192.168.3.255" },
		"missing target":               func(e *PreparationExpected, _ map[string]string) { e.Target.Address = "192.168.3.249" },
		"zero boot": func(e *PreparationExpected, _ map[string]string) {
			e.TargetBootID = "00000000-0000-0000-0000-000000000000"
		},
		"environment override": func(_ *PreparationExpected, r map[string]string) { r["LD_PRELOAD"] = "/tmp/x" },
		"download override": func(_ *PreparationExpected, r map[string]string) {
			r["BOREALIS_K3S_INSTALL_SCRIPT_URL"] = "https://example.invalid"
		},
		"identity injection": func(_ *PreparationExpected, r map[string]string) { r["SECRET_KEY"] = "private-test" },
		"host path":          func(_ *PreparationExpected, r map[string]string) { r["BOREALIS_PROJECT_ROOT"] = "/outside" },
		"newline secret": func(_ *PreparationExpected, r map[string]string) {
			r["BOREALIS_OPERATOR_SECRET"] = "private-test\nOTHER=1"
		},
		"NUL secret": func(_ *PreparationExpected, r map[string]string) { r["BOREALIS_OPERATOR_SECRET"] = "private-test\x00" },
		"oversize secret": func(_ *PreparationExpected, r map[string]string) {
			r["BOREALIS_OPERATOR_SECRET"] = strings.Repeat("x", 4097)
		},
		"missing sizing":      func(_ *PreparationExpected, r map[string]string) { delete(r, "BOREALIS_CLUSTER_SIZING_RANK") },
		"partial sizing":      func(_ *PreparationExpected, r map[string]string) { r["BOREALIS_CLUSTER_SIZING_MEMORY_MIB"] = "" },
		"insufficient sizing": func(_ *PreparationExpected, r map[string]string) { r["BOREALIS_CLUSTER_SIZING_MEMORY_MIB"] = "16000" },
		"noncanonical sizing": func(_ *PreparationExpected, r map[string]string) { r["BOREALIS_CLUSTER_SIZING_MEMORY_MIB"] = "016384" },
		"mode":                func(_ *PreparationExpected, r map[string]string) { r["BOREALIS_ENGINE_NETWORK_MODE"] = "dev" },
		"FQDN syntax": func(_ *PreparationExpected, r map[string]string) {
			r["BOREALIS_PUBLIC_HOSTNAME"] = "https://example.test"
		},
		"missing local CA": func(_ *PreparationExpected, r map[string]string) { r["BOREALIS_ENGINE_NETWORK_MODE"] = "local" },
		"CA garbage": func(_ *PreparationExpected, r map[string]string) {
			r["BOREALIS_AGENT_ENGINE_CA_PEM_B64"] = "c2VudGluZWw="
		},
		"standalone database": func(_ *PreparationExpected, r map[string]string) {
			r["BOREALIS_DATABASE_URL"] = strings.Replace(r["BOREALIS_DATABASE_URL"], "borealis-postgres-rw.borealis.svc", "postgres-db.borealis.svc", 1)
		},
		"different database password": func(_ *PreparationExpected, r map[string]string) { r["POSTGRES_PASSWORD"] = "different" },
		"DB endpoint override":        func(_ *PreparationExpected, r map[string]string) { r["BOREALIS_DATABASE_URL"] += "&host=example.test" },
		"DB duplicate option":         func(_ *PreparationExpected, r map[string]string) { r["BOREALIS_DATABASE_URL"] += "&sslmode=require" },
		"DB parse error": func(_ *PreparationExpected, r map[string]string) {
			r["BOREALIS_DATABASE_URL"] = "postgres://%private-test"
		},
		"tuning command": func(_ *PreparationExpected, r map[string]string) {
			r["BOREALIS_POSTGRES_DB_MEMORY_LIMIT"] = "4096m;private-test"
		},
		"unbounded tuning": func(_ *PreparationExpected, r map[string]string) {
			r["BOREALIS_POSTGRES_MAX_CONNECTIONS"] = "999999999999999999999999"
		},
	} {
		t.Run(name, func(t *testing.T) {
			e, r := preparationFixture()
			mutate(&e, r)
			c, err := NewPreparationConfiguration(e, r)
			if !errors.Is(err, ErrPreparationConfig) || c != nil {
				t.Fatal("unsafe pre-join configuration accepted")
			}
			if strings.Contains(err.Error(), "private-test") {
				t.Fatal("private diagnostic leaked")
			}
		})
	}
}

func preparationTestCA(t *testing.T, isCA bool, expired bool) string {
	t.Helper()
	pub, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	cert := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "test CA"}, NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour), BasicConstraintsValid: true, IsCA: isCA, KeyUsage: x509.KeyUsageCertSign}
	if expired {
		cert.NotAfter = now.Add(-time.Minute)
	}
	der, err := x509.CreateCertificate(rand.Reader, cert, cert, pub, key)
	if err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
}

func TestPreparationConfigurationLocalTrustAndProfileInheritance(t *testing.T) {
	for rank, memory := range []string{"4096", "16384", "32768", "65536"} {
		e, r := preparationFixture()
		r["BOREALIS_CLUSTER_SIZING_RANK"] = fmt.Sprint(rank)
		r["BOREALIS_CLUSTER_SIZING_MEMORY_MIB"] = memory
		r["BOREALIS_ENGINE_NETWORK_MODE"] = "local"
		r["BOREALIS_AGENT_ENGINE_CA_PEM_B64"] = preparationTestCA(t, true, false)
		c, err := NewPreparationConfiguration(e, r)
		if err != nil {
			t.Fatal(err)
		}
		env, _ := c.Environment()
		if !bytes.Contains(env, []byte("BOREALIS_CLUSTER_SIZING_MEMORY_MIB="+memory+"\n")) {
			t.Fatal("source tuning was not retained")
		}
	}
	for _, encoded := range []string{preparationTestCA(t, false, false), preparationTestCA(t, true, true), base64.StdEncoding.EncodeToString([]byte("-----BEGIN PRIVATE KEY-----\nprivate-test\n-----END PRIVATE KEY-----\n"))} {
		e, r := preparationFixture()
		r["BOREALIS_AGENT_ENGINE_CA_PEM_B64"] = encoded
		if _, err := NewPreparationConfiguration(e, r); err == nil {
			t.Fatal("invalid CA trust accepted")
		}
	}
}
