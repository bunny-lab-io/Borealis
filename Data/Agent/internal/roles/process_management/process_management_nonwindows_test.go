//go:build !windows

package processmanagement

import "testing"

func TestParseSSNetworkTotals(t *testing.T) {
	output := `ESTAB 0 0 10.0.0.5:44000 10.0.0.10:443 users:(("curl",pid=1234,fd=3))
	 cubic wscale:7,7 rto:204 rtt:2.1/0.4 bytes_sent:1200 bytes_acked:1100 bytes_received:900 segs_out:10 segs_in:8
ESTAB 0 0 10.0.0.5:44002 10.0.0.11:443 users:(("curl",pid=1234,fd=4))
	 cubic wscale:7,7 rto:204 rtt:2.1/0.4 bytes_acked:400 bytes_received:600
ESTAB 0 0 10.0.0.5:44004 10.0.0.12:443 users:(("agent",pid=5678,fd=8))
	 cubic wscale:7,7 rto:204 rtt:2.1/0.4 bytes_sent:50 bytes_received:70`

	totals := parseSSNetworkTotals(output)
	if totals[1234] != 3000 {
		t.Fatalf("pid 1234 total = %d, want 3000", totals[1234])
	}
	if totals[5678] != 120 {
		t.Fatalf("pid 5678 total = %d, want 120", totals[5678])
	}
}
