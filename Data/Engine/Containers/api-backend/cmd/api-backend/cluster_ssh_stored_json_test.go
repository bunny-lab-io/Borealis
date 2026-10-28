package main

import (
	"bytes"
	"encoding/json"
	"io"
	"math/rand"
	"reflect"
	"strings"
	"testing"
)

// Preserve the original comparison as a differential oracle. Its permissive
// canonical decoder and strict raw object decoder intentionally differ.
func legacyClusterSSHStoredJSON(raw, canonical []byte, depth int) bool {
	if depth > 32 {
		return false
	}
	raw, canonical = bytes.TrimSpace(raw), bytes.TrimSpace(canonical)
	if len(raw) == 0 || len(canonical) == 0 {
		return false
	}
	if canonical[0] == '{' {
		var expected map[string]json.RawMessage
		if json.Unmarshal(canonical, &expected) != nil {
			return false
		}
		allowed := make(map[string]bool, len(expected))
		for key := range expected {
			allowed[key] = true
		}
		actual, err := decodeClusterSSHObject(raw, allowed)
		if err != nil || len(actual) != len(expected) {
			return false
		}
		for key, value := range expected {
			if !legacyClusterSSHStoredJSON(actual[key], value, depth+1) {
				return false
			}
		}
		return true
	}
	if canonical[0] == '[' {
		var actual, expected []json.RawMessage
		if raw[0] != '[' || json.Unmarshal(raw, &actual) != nil || json.Unmarshal(canonical, &expected) != nil || len(actual) != len(expected) {
			return false
		}
		for i := range expected {
			if !legacyClusterSSHStoredJSON(actual[i], expected[i], depth+1) {
				return false
			}
		}
		return true
	}
	decode := func(value []byte) (any, bool) {
		d := json.NewDecoder(bytes.NewReader(value))
		d.UseNumber()
		var out any
		err := d.Decode(&out)
		return out, err == nil && d.Decode(&struct{}{}) == io.EOF
	}
	actual, ok := decode(raw)
	expected, expectedOK := decode(canonical)
	return ok && expectedOK && reflect.DeepEqual(actual, expected)
}

func TestClusterSSHStoredJSONComparisonCompatibility(t *testing.T) {
	corpus := []string{
		``, ` `, `null`, `true`, `false`, `0`, `-0`, `1`, `1.0`, `1e1`, `10`,
		`9007199254740993`, `1e999`, `""`, `"a"`, `"\u0061"`, `"\ud800"`, `"\ufffd"`,
		"\"\xff\"", "\"\xfe\"", `{}`, `[]`, `[null]`, `[1,2]`, `[2,1]`,
		`{"a":1}`, `{"A":1}`, `{"a":1,"b":2}`, `{"b":2,"a":1}`, `{"a":1,"a":1}`,
		`{"a":0,"a":1}`, `{"\u0061":1}`, `{"a":0,"\u0061":1}`, `[{"a":1,"a":1}]`,
		`{"a":[null,{},[],"x",1.00]}`, `01`, `NaN`, `undefined`, `1e`, `"\q"`, `"unclosed`,
		`{"a":1,}`, `[1,]`, `1 2`, `{}null`, `[] garbage`,
		"\u00a0[1]\u00a0", "\u2003{\"a\":1}\u2003",
	}
	for _, depth := range []int{0, 1, 31, 32, 33} {
		for i, raw := range corpus {
			for j, canonical := range corpus {
				got := sameClusterSSHStoredJSON([]byte(raw), []byte(canonical), depth)
				want := legacyClusterSSHStoredJSON([]byte(raw), []byte(canonical), depth)
				if got != want {
					t.Fatalf("cases %d/%d depth %d acceptance = %v, want %v", i, j, depth, got, want)
				}
			}
		}
	}
	for _, count := range []int{31, 32, 33} {
		for _, pair := range [][2]string{{`{"a":`, `}`}, {`[`, `]`}} {
			raw := []byte(strings.Repeat(pair[0], count) + `1` + strings.Repeat(pair[1], count))
			if got, want := sameClusterSSHStoredJSON(raw, raw, 0), legacyClusterSSHStoredJSON(raw, raw, 0); got != want {
				t.Fatalf("nesting %d acceptance = %v, want %v", count, got, want)
			}
		}
	}
	// Seeded mutations cover malformed/trailing tokens and decoded key changes
	// without depending on a long-running fuzz process for normal validation.
	random := rand.New(rand.NewSource(530))
	base := []byte(`{"a":[1,true,null,{"b":"value","c":-0}],"d":{"e":[],"f":{}}}`)
	alphabet := []byte("{}[],:\"\\0129eE+- abcdefnt\n\t")
	for i := 0; i < 1000; i++ {
		raw := append([]byte{}, base...)
		for changes := 1 + random.Intn(4); changes > 0; changes-- {
			raw[random.Intn(len(raw))] = alphabet[random.Intn(len(alphabet))]
		}
		for _, canonical := range [][]byte{base, raw} {
			if got, want := sameClusterSSHStoredJSON(raw, canonical, 0), legacyClusterSSHStoredJSON(raw, canonical, 0); got != want {
				t.Fatalf("mutation %d acceptance = %v, want %v", i, got, want)
			}
		}
	}
}

func BenchmarkClusterSSHStoredJSONStructured(b *testing.B) {
	canonical, err := json.Marshal(sshCNPGOperatorFixture())
	if err != nil {
		b.Fatal(err)
	}
	var formatted bytes.Buffer
	if err := json.Indent(&formatted, canonical, "", "  "); err != nil {
		b.Fatal(err)
	}
	raw := formatted.Bytes()
	for _, tc := range []struct {
		name    string
		compare func([]byte, []byte, int) bool
	}{{"original", legacyClusterSSHStoredJSON}, {"single parse", sameClusterSSHStoredJSON}} {
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(raw)))
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if !tc.compare(raw, canonical, 0) {
					b.Fatal("equivalent structured JSON rejected")
				}
			}
		})
	}
}
