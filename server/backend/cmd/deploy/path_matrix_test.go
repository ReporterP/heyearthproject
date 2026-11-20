package main

import (
	"strings"
	"testing"
)

// The verdict needs every group, raw 5-tuple groups at >= pathMatrixRawPct and the user-visible
// ones at >= pathMatrixUserPct (the 20.11 entry 6 numbers fail, a healthy entry passes).
func TestPathMatrixVerdict(t *testing.T) {
	healthy := `== hub → entry 5
RAW hub→entry:443 50/50 ++++
RAW hub→entry:2443 49/50 +++-
== entry 5 → hub
RAW entry→hub:8447 50/50 ++
RAW entry→hub:443 48/50 ++
RAW entry→hub:2443 50/50 ++
USER via-exit:443 50/50
USER via-exit:2443 50/50
HEYEARTH_JOB_DONE
`
	if pass, failed := pathMatrixVerdict(healthy); !pass {
		t.Fatalf("a healthy entry failed: %v", failed)
	}
	entry6 := strings.NewReplacer("50/50 ++++", "18/50 -+-", "RAW entry→hub:8447 50/50", "RAW entry→hub:8447 25/50").Replace(healthy)
	pass, failed := pathMatrixVerdict(entry6)
	if pass || len(failed) != 2 || !strings.Contains(strings.Join(failed, ";"), "hub→entry:443 18/50") {
		t.Fatalf("the 20.11 entry 6 path passed or failed wrongly: %v %v", pass, failed)
	}
	userDip := strings.Replace(healthy, "USER via-exit:2443 50/50", "USER via-exit:2443 49/50", 1)
	if pass, _ := pathMatrixVerdict(userDip); pass {
		t.Fatal("98 % through an exit is below the user floor of 99 %")
	}
	if pass, _ := pathMatrixVerdict("RAW hub→entry:443 50/50\nHEYEARTH_JOB_DONE\n"); pass {
		t.Fatal("a run with missing groups (SSH to the entry failed) passed")
	}
	if pass, failed := pathMatrixVerdict(healthy + "RAW entry→hub:ssh 0/1 SSH до входа не удался\n"); pass {
		t.Fatalf("an SSH failure must fail the verdict: %v", failed)
	}
}

// An entry on a GRE tunnel: the raw groups between the tunnel addresses count, the lossy public
// path (nginx's backup only) is shown as BACKUP and judged by nothing; a missing tunnel group
// still fails through EXPECT.
func TestPathMatrixVerdictTunnel(t *testing.T) {
	onTunnel := `EXPECT 6
BACKUP hub→entry:443 21/50 -+-
BACKUP hub→entry:2443 20/50 -+-
RAW tunnel-hub→entry:443 50/50 ++
BACKUP entry→hub:8447 42/50 ++
BACKUP entry→hub:443 37/50 ++
BACKUP entry→hub:2443 37/50 ++
RAW tunnel-entry→hub:8447 50/50 ++
RAW tunnel-entry→hub:443 50/50 ++
RAW tunnel-entry→hub:2443 50/50 ++
USER via-exit:443 50/50
USER via-exit:2443 50/50
`
	if pass, failed := pathMatrixVerdict(onTunnel); !pass {
		t.Fatalf("an entry whose tunnel works failed on its backup path: %v", failed)
	}
	noTunnelGroup := strings.Replace(onTunnel, "RAW tunnel-entry→hub:2443 50/50 ++\n", "", 1)
	if pass, failed := pathMatrixVerdict(noTunnelGroup); pass || !strings.Contains(strings.Join(failed, ";"), "групп 5 из 6") {
		t.Fatalf("a missing tunnel group passed: %v", failed)
	}
	deadTunnel := strings.Replace(onTunnel, "RAW tunnel-hub→entry:443 50/50", "RAW tunnel-hub→entry:443 0/50", 1)
	if pass, _ := pathMatrixVerdict(deadTunnel); pass {
		t.Fatal("a dead tunnel passed")
	}
}

// The Hub-side job parses as bash and carries the entry part and the probe intact.
func TestPathMatrixRunScript(t *testing.T) {
	s := pathMatrixRun(6, "198.51.100.42", "203.0.113.10", "yandex.ru", 1090, "10.77.0.25", "10.77.0.26")
	for _, must := range []string{"nodessh 6", "PATH_MATRIX_ENTRY_DONE", "--socks5-hostname \"127.0.0.1:$SOCKS\"", "SOCKS=1090", "USER via-exit:$port"} {
		if !strings.Contains(s, must) {
			t.Errorf("job lacks %q", must)
		}
	}
	if !haveBash() {
		t.Skip("no bash")
	}
	if out, rc := runBashStdin(t, s, "-n"); rc != 0 {
		t.Fatalf("job does not parse: %s", out)
	}
	if out, rc := runBashStdin(t, hubJobLaunchScript("path-matrix", 6, s), "-n"); rc != 0 {
		t.Fatalf("launcher does not parse: %s", out)
	}
}
