package main

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestClusterSSHCredentialSudoFramingPreservesSyntaxAndRejectsLines(t *testing.T) {
	envelope := clusterSSHCredentialEnvelope{Version: 1, Username: "operator", Method: "password", Password: "SSH fixture may contain\nvalid syntax",
		Binding: clusterSSHCredentialBinding{ClusterID: "11111111-1111-4111-8111-111111111111", OperationID: "22222222-2222-4222-8222-222222222222", TargetID: "33333333-3333-4333-8333-333333333333",
			Address: "192.168.3.251", Port: 22, Fingerprint: "SHA256:" + base64.RawStdEncoding.EncodeToString(make([]byte, 32))}}
	for _, password := range []string{"", "  <&>$();' meaningful syntax  ", strings.Repeat("x", 4096)} {
		envelope.SudoPassword = password
		if err := envelope.validate(); err != nil || envelope.SudoPassword != password {
			t.Fatalf("sudo secret syntax changed or rejected: %v", err)
		}
	}
	for _, password := range []string{"line\nnext", "line\rnext", "nul\x00byte", string([]byte{0xff}), strings.Repeat("x", 4097)} {
		envelope.SudoPassword = password
		if envelope.validate() == nil {
			t.Fatal("unsupported sudo framing accepted for encryption")
		}
	}
}
