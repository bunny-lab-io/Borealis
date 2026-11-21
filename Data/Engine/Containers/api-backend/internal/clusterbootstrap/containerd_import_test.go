package clusterbootstrap

import (
	"reflect"
	"strings"
	"testing"
)

func TestContainerdImportCheck(t *testing.T) {
	digest := "sha256:" + strings.Repeat("a", 64)
	ref := "docker.io/borealis-engine/api-backend:release-" + strings.Repeat("b", 40)
	target := ContainerdImageTarget{ref, digest}
	header := "REF TYPE DIGEST STATUS SIZE UNPACKED\n"
	row := ref + " " + ociManifestType + " " + digest + " complete (4/4) 10.5 MiB/10.5 MiB true\n"
	for _, mode := range []string{"complete", "extra image", "missing", "digest", "not unpacked", "incomplete", "unavailable", "unequal count", "zero count", "alias count", "missing count", "duplicate", "missing newline", "wrong header", "foreign media", "control", "oversize", "duplicate expected", "invalid expected", "truncated row", "empty expected"} {
		t.Run(mode, func(t *testing.T) {
			raw := header + row
			expected := []ContainerdImageTarget{target}
			switch mode {
			case "extra image":
				raw += "docker.io/library/busybox:latest " + ociManifestType + " " + digest + " incomplete (2/3) 1.0 MiB/2.0 MiB false\n"
			case "missing":
				raw = header
			case "digest":
				raw = strings.Replace(raw, digest, "sha256:"+strings.Repeat("c", 64), 1)
			case "not unpacked":
				raw = strings.Replace(raw, "true", "false", 1)
			case "incomplete":
				raw = strings.Replace(raw, "complete", "incomplete", 1)
			case "unavailable":
				raw = strings.Replace(raw, "complete (4/4)", "unavailable (4/?)", 1)
			case "unequal count":
				raw = strings.Replace(raw, "(4/4)", "(3/4)", 1)
			case "zero count":
				raw = strings.Replace(raw, "(4/4)", "(0/0)", 1)
			case "alias count":
				raw = strings.Replace(raw, "(4/4)", "(04/04)", 1)
			case "missing count":
				raw = strings.Replace(raw, " (4/4)", "", 1)
			case "duplicate":
				raw += row
			case "missing newline":
				raw = strings.TrimSuffix(raw, "\n")
			case "wrong header":
				raw = strings.Replace(raw, "UNPACKED", "READY", 1)
			case "foreign media":
				raw = strings.Replace(raw, ociManifestType, "application/unknown", 1)
			case "control":
				raw = strings.Replace(raw, "10.5", "10.5\x1b", 1)
			case "oversize":
				raw += strings.Repeat(" ", 256<<10) + "\n"
			case "duplicate expected":
				expected = append(expected, target)
			case "invalid expected":
				expected[0].Digest = "tag"
			case "truncated row":
				raw = header + ref + "\n"
			case "empty expected":
				expected = nil
			}
			got, err := ParseContainerdImportCheck([]byte(raw), expected)
			valid := mode == "complete" || mode == "extra image"
			if (err == nil) != valid || valid && !reflect.DeepEqual(got, []ContainerdImageTarget{target}) || !valid && got != nil {
				t.Fatal("wrong import evidence", got, err)
			}
		})
	}
}
