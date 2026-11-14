package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestClusterSSHStoredJSONScalarCompatibility(t *testing.T) {
	for _, tc := range []struct {
		name, raw, canonical string
		want                 bool
	}{
		{"string", `"node-1"`, `"node-1"`, true},
		{"number", `12345`, `12345`, true},
		{"negative zero", `-0`, `-0`, true},
		{"exponent", `1e+3`, `1e+3`, true},
		{"true", `true`, `true`, true},
		{"false", `false`, `false`, true},
		{"null", `null`, `null`, true},
		{"outer whitespace", " \t12345\r\n", `12345`, true},
		{"escaped string", `"\u0061"`, `"a"`, true},
		{"surrogate replacement", `"\ud800"`, `"\ufffd"`, true},
		{"identical invalid UTF8", "\"\xff\"", "\"\xff\"", true},
		{"different invalid UTF8", "\"\xff\"", "\"\xfe\"", true},
		{"invalid UTF8 replacement", "\"\xff\"", `"\ufffd"`, true},
		{"different string", `"node-1"`, `"node-2"`, false},
		{"number spelling", `1.0`, `1`, false},
		{"zero spelling", `-0`, `0`, false},
		{"exponent spelling", `1e1`, `10`, false},
		{"scalar type", `"1"`, `1`, false},
		{"empty", ``, ``, false},
		{"whitespace only", " \t\r\n", " \t\r\n", false},
		{"unknown literal", `undefined`, `undefined`, false},
		{"invalid number", `01`, `01`, false},
		{"invalid exponent", `1e`, `1e`, false},
		{"invalid escape", `"\q"`, `"\q"`, false},
		{"unclosed string", `"node`, `"node`, false},
		{"trailing scalar", `null true`, `null true`, false},
		{"trailing junk", `1 garbage`, `1 garbage`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := sameClusterSSHStoredJSON([]byte(tc.raw), []byte(tc.canonical), 0); got != tc.want {
				t.Fatalf("scalar acceptance = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestClusterSSHStoredJSONContainerStrictness(t *testing.T) {
	for _, tc := range []struct {
		name, raw, canonical string
		want                 bool
	}{
		{"reordered object", `{"b":[true,null],"a":1}`, `{"a":1,"b":[true,null]}`, true},
		{"identical duplicate", `{"a":1,"a":1}`, `{"a":1,"a":1}`, false},
		{"nested duplicate", `[{"a":1,"a":1}]`, `[{"a":1,"a":1}]`, false},
		{"escaped duplicate", `{"a":1,"\u0061":1}`, `{"a":1}`, false},
		{"invalid UTF8 key replacement", "{\"\xff\":1}", `{"\ufffd":1}`, true},
		{"invalid UTF8 key collision", "{\"\xff\":1,\"\xfe\":1}", `{"\ufffd":1}`, false},
		{"canonical overwritten depth", `{"a":1}`, `{"a":` + strings.Repeat(`[`, 33) + `0` + strings.Repeat(`]`, 33) + `,"a":1}`, true},
		{"case alias", `{"A":1}`, `{"a":1}`, false},
		{"unknown key", `{"a":1,"b":2}`, `{"a":1}`, false},
		{"missing key", `{}`, `{"a":1}`, false},
		{"array order", `[1,2]`, `[2,1]`, false},
		{"array length", `[1]`, `[1,2]`, false},
		{"depth boundary", strings.Repeat(`[`, 32) + `1` + strings.Repeat(`]`, 32), strings.Repeat(`[`, 32) + `1` + strings.Repeat(`]`, 32), true},
		{"depth exceeded", strings.Repeat(`[`, 33) + `1` + strings.Repeat(`]`, 33), strings.Repeat(`[`, 33) + `1` + strings.Repeat(`]`, 33), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := sameClusterSSHStoredJSON([]byte(tc.raw), []byte(tc.canonical), 0); got != tc.want {
				t.Fatalf("container acceptance = %v, want %v", got, tc.want)
			}
		})
	}
}

func BenchmarkClusterSSHStoredJSONScalars(b *testing.B) {
	for _, tc := range []struct{ name, value string }{
		{"string", `"source-member-1"`},
		{"number", `12345`},
		{"boolean", `true`},
		{"null", `null`},
	} {
		b.Run(tc.name, func(b *testing.B) {
			raw, canonical := []byte(tc.value), []byte(tc.value)
			b.ReportAllocs()
			b.SetBytes(int64(len(raw)))
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if !sameClusterSSHStoredJSON(raw, canonical, 0) {
					b.Fatal("identical scalar rejected")
				}
			}
		})
	}
}

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
