package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func BenchmarkClusterSSHStorageObject(b *testing.B) {
	raw, err := json.Marshal(sshCNPGOperatorFixture())
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.SetBytes(int64(len(raw)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, _, err := clusterSSHStorageObject(raw); err != nil {
			b.Fatal(err)
		}
	}
}

func TestClusterSSHStorageJSONReceiptCompatibility(t *testing.T) {
	f := newSSHStorageFixture(t, true)
	raws := [][]byte{}
	for _, object := range f.objects {
		raw, _ := json.Marshal(object)
		raws = append(raws, raw)
	}
	for _, raw := range []string{
		`{}`, `{"a":null,"A":true,"n":-0,"e":1e+3,"f":1.00,"s":"\u0061","empty":[]}`,
		`{"escaped":"\ud800","nested":[{},null,false,[],1.25,-1,"x"]}`,
		`{"a":null,"a":null}`, `{"a":1,"\u0061":2}`, `{"nested":[{"x":1,"x":2}]}`,
		`{"a":1,}`, `{"a":[1,]}`, `{"a":NaN}`, `{"a":01}`, `{"a":1}null`, `{"a":1} garbage`, `null`, `[]`,
		strings.Repeat(`{"a":`, 32) + `1` + strings.Repeat(`}`, 32),
		strings.Repeat(`{"a":`, 33) + `1` + strings.Repeat(`}`, 33),
		`{"a":` + strings.Repeat(`[`, 31) + `[]` + strings.Repeat(`]`, 31) + `}`,
		`{"a":` + strings.Repeat(`[`, 32) + `[]` + strings.Repeat(`]`, 32) + `}`,
	} {
		raws = append(raws, []byte(raw))
	}
	for i, raw := range raws {
		// Retain the original receipt contract as a differential oracle, including
		// exact json.Number spelling, empty containers and decoded duplicate keys.
		var old map[string]any
		d := json.NewDecoder(bytes.NewReader(raw))
		d.UseNumber()
		decoded := d.Decode(&old) == nil && old != nil
		canonical, marshalErr := json.Marshal(old)
		accepted := decoded && marshalErr == nil && sameClusterSSHStoredJSON(raw, canonical, 0)
		got, digest, err := clusterSSHStorageObject(raw)
		if (err == nil) != accepted {
			t.Fatalf("case %d acceptance changed", i)
		}
		if accepted && (!reflect.DeepEqual(got, old) || digest != sha256.Sum256(canonical)) {
			t.Fatalf("case %d receipt changed", i)
		}
	}
}
