package main

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

const restoreRunbook = "../../../../docs/runbooks/restore-hub-backup.md"

func runbookSection(t *testing.T, doc, heading string) string {
	t.Helper()
	i := strings.Index(doc, "\n"+heading+"\n")
	if i < 0 {
		t.Fatalf("runbook has no section %q", heading)
	}
	rest := doc[i+len(heading)+2:]
	if j := strings.Index(rest, "\n## "); j >= 0 {
		rest = rest[:j]
	}
	return rest
}

// The restore procedure produces a working Hub only in this order: DNS before the certificate
// (--main), the DB and the state before the services that read them, every --setup-* that --main
// does not run, the exit tunnel, ops, then an external Bamboo check.
func TestRestoreRunbookSteps(t *testing.T) {
	b, err := os.ReadFile(restoreRunbook)
	if err != nil {
		t.Fatal(err)
	}
	doc := string(b)
	restore := runbookSection(t, doc, "## Полное восстановление на новом Hub")
	steps := []string{
		"dig +short hiearth.ru",
		"go run ./cmd/deploy --main",
		"pg_restore --clean",
		"tar -C / -xzf /root/restore/files.tar.gz",
		"go run ./cmd/deploy --setup-xray",
		"go run ./cmd/deploy --setup-hysteria2-hub",
		"go run ./cmd/deploy --setup-awg",
		"go run ./cmd/deploy --setup-exit-node 89.124.89.126",
		"go run ./cmd/deploy --setup-ops",
		"Бамбук — только снаружи",
	}
	last := -1
	for _, s := range steps {
		i := strings.Index(restore, s)
		if i < 0 {
			t.Errorf("restore procedure lacks %q", s)
			continue
		}
		if i < last {
			t.Errorf("%q is out of order", s)
		}
		last = i
	}
	// the restored api.env must not bring the old DB password back over the fresh one
	if !strings.Contains(restore, "NEW_DB=$(grep '^DATABASE_URL=' /opt/heyearth/mainserver/api.env)") {
		t.Error("restore procedure does not keep the fresh DATABASE_URL across the files restore")
	}
	for _, h := range []string{"## Что не входит в бэкап", "## Пароль PostgreSQL"} {
		runbookSection(t, doc, h)
	}
	if pw := runbookSection(t, doc, "## Пароль PostgreSQL"); !strings.Contains(pw, "openssl rand -hex 24") || !strings.Contains(pw, "ALTER USER postgres PASSWORD") {
		t.Error("the PostgreSQL section must document the rotation")
	}

	// every deploy flag the runbook names exists
	src, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range regexp.MustCompile(`--(setup-[a-z0-9-]+|main|update-main|status|force)\b`).FindAllStringSubmatch(doc, -1) {
		if !strings.Contains(string(src), `flag.Bool("`+m[1]+`"`) && !strings.Contains(string(src), `flag.String("`+m[1]+`"`) {
			t.Errorf("runbook names --%s, deploy has no such flag", m[1])
		}
	}

	// what the runbook says is archived is what the backup script archives
	for _, p := range []string{"hysteria2-client-*.service", "hysteria2-server.service.d"} {
		if !strings.Contains(doc, p) || !strings.Contains(backupScript, p) {
			t.Errorf("%s: runbook and backup script must both list it", p)
		}
	}
}

// Guard operations in the runbook: re-apply with the script (systemctl restart would run
// ExecStop and drop the chain and the 10085 rule for a moment), the phase-B API rollback
// procedure, the previous-guard rollback.
func TestRunbookGuardProcedures(t *testing.T) {
	b, err := os.ReadFile(restoreRunbook)
	if err != nil {
		t.Fatal(err)
	}
	doc := string(b)
	if strings.Contains(strings.ReplaceAll(doc, "**не** `systemctl restart heyearth-egress-guard`", ""), "systemctl restart heyearth-egress-guard") {
		t.Error("the runbook advises systemctl restart heyearth-egress-guard; use heyearth-egress-guard.sh start")
	}
	sec := runbookSection(t, doc, "### Откат API ниже 4ab930e, когда гард в фазе B")
	set, start := strings.Index(sec, "HY_AUTH_PORTS=8000,8001"), strings.Index(sec, "/opt/heyearth/bin/heyearth-egress-guard.sh start")
	if set < 0 || start < 0 || set > start || strings.Index(sec, "откатить API") < start {
		t.Error("phase-B API rollback: switch the guard to phase A (env + script start) BEFORE rolling the API back")
	}
	if egressHyAuthPortsA != "8000,8001" {
		t.Errorf("the runbook's HY_AUTH_PORTS value must be the phase-A ports %q", egressHyAuthPortsA)
	}
	for _, w := range []string{egressGuardPrevDir, "rolled back to the PREVIOUS guard", "Шаги 5–9 идут без kernel egress guard"} {
		if !strings.Contains(doc, w) {
			t.Errorf("runbook lacks %q", w)
		}
	}
}
