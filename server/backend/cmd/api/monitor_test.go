package main

import "testing"

func TestParseCheckHostTCPResult(t *testing.T) {
	cases := []struct{ name, body, verdict string }{
		{"one node ok", `{"ru1.node.check-host.net":[{"time":0.05,"address":"138.16.181.225"}],"ru2.node.check-host.net":[{"error":"Connection timed out"}],"ru3.node.check-host.net":[{"error":"Connection timed out"}]}`, "reachable"},
		{"all failed", `{"ru1.node.check-host.net":[{"error":"Connection timed out"}],"ru2.node.check-host.net":[{"error":"Connection refused"}]}`, "unreachable"},
		{"pending", `{"ru1.node.check-host.net":[{"error":"Connection timed out"}],"ru2.node.check-host.net":null}`, "unknown"},
		{"empty", `{}`, "unknown"},
	}
	for _, tc := range cases {
		tally, err := parseCheckHostTCPResult([]byte(tc.body))
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if got := tally.verdict(); got != tc.verdict {
			t.Fatalf("%s: verdict %s (%+v), want %s", tc.name, got, tally, tc.verdict)
		}
	}
	if _, err := parseCheckHostTCPResult([]byte(`<html>rate limited</html>`)); err == nil {
		t.Fatal("non-JSON body must be an error (not counted as a failure)")
	}
}
