package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/heyearth/backend/internal/biz"
)

var autoT0 = time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)

func healthyOther() []pathHealth { return []pathHealth{{ID: 6, Healthy: true, Known: true}} }

func TestA1Decide(t *testing.T) {
	in := a1Input{EntryID: 5, Paths: healthyOther(), Shares: map[string]share{
		biz.EntrySampleTCP443: {OK: 70, Total: 100, Checks: 10},
	}}
	if d := a1Decide(in, autoT0); !d.Remove || !strings.Contains(d.Reason, "tcp443") {
		t.Fatalf("tcp443 70 %% over 10 checks → out: %+v", d)
	}
	in.Shares[biz.EntrySampleTCP443] = share{OK: 35, Total: 50, Checks: 5}
	if d := a1Decide(in, autoT0); d.Remove {
		t.Fatalf("the same share over 5 checks proves nothing: %+v", d)
	}
	in.Shares = map[string]share{biz.EntrySampleUDP: {OK: 94, Total: 100, Checks: 10}}
	if d := a1Decide(in, autoT0); d.Remove || d.Reason != "" {
		t.Fatalf("udp 94 %% is fine: %+v", d)
	}
	in.Shares, in.DownRuns = nil, 3
	if d := a1Decide(in, autoT0); !d.Remove || !strings.Contains(d.Reason, "3 проверки") {
		t.Fatalf("3 unreachable checks in a row → out: %+v", d)
	}
	in.DownRuns = 2
	if d := a1Decide(in, autoT0); d.Remove {
		t.Fatalf("2 is not 3: %+v", d)
	}
}

// Review Focus 5: never the last healthy path, also when the others are stale or unknown.
func TestA1NeverRemovesLastHealthyPath(t *testing.T) {
	bad := map[string]share{biz.EntrySampleTCP2443: {OK: 10, Total: 100, Checks: 12}}
	for name, paths := range map[string][]pathHealth{
		"no other path":           nil,
		"the other is unhealthy":  {{ID: 6, Healthy: false, Known: true}},
		"the other is unknown":    {{ID: 6, Healthy: true, Known: false}},
		"only the Hub, not known": {{ID: 0, Healthy: true, Known: false}},
	} {
		d := a1Decide(a1Input{EntryID: 5, Shares: bad, Paths: paths}, autoT0)
		if d.Remove || !strings.Contains(d.Blocked, "последний здоровый путь") || d.Reason == "" {
			t.Errorf("%s: %+v", name, d)
		}
	}
}

func TestA1Limits(t *testing.T) {
	in := a1Input{EntryID: 5, Paths: healthyOther(), DownRuns: 3}
	in.LastAuto = []time.Time{autoT0.Add(-40 * time.Minute)}
	if d := a1Decide(in, autoT0); d.Remove || !strings.Contains(d.Blocked, "раза в час") {
		t.Fatalf("an auto removal 40 min ago → not again: %+v", d)
	}
	in.LastAuto = []time.Time{autoT0.Add(-3 * time.Hour), autoT0.Add(-30 * time.Hour)}
	if d := a1Decide(in, autoT0); d.Remove || d.Blocked != "дальше вручную" {
		t.Fatalf("two auto removals without a human → only the alert: %+v", d)
	}
	in.LastAuto = []time.Time{autoT0.Add(-3 * time.Hour), autoT0.Add(-10 * 24 * time.Hour)}
	if d := a1Decide(in, autoT0); !d.Remove {
		t.Fatalf("an old one does not count: %+v", d)
	}
}

func TestA12Decide(t *testing.T) {
	if r, a := a12Decide(3, true, nil, autoT0); !r || a {
		t.Fatalf("3 misses, tunnel alive → restart: %v %v", r, a)
	}
	if r, a := a12Decide(2, true, nil, autoT0); r || a {
		t.Fatalf("2 misses → nothing yet: %v %v", r, a)
	}
	if r, a := a12Decide(5, false, nil, autoT0); r || a {
		t.Fatalf("a dead tunnel explains the silence → nothing: %v %v", r, a)
	}
	three := []time.Time{autoT0.Add(-10 * time.Minute), autoT0.Add(-20 * time.Minute), autoT0.Add(-30 * time.Minute)}
	if r, a := a12Decide(3, true, three, autoT0); r || !a {
		t.Fatalf("3 restarts this hour → the alert instead: %v %v", r, a)
	}
	old := []time.Time{autoT0.Add(-61 * time.Minute), autoT0.Add(-70 * time.Minute), autoT0.Add(-80 * time.Minute)}
	if r, _ := a12Decide(3, true, old, autoT0); !r {
		t.Fatal("restarts older than an hour do not count")
	}
}

func TestA14Decide(t *testing.T) {
	const limit = 4 << 30
	if to, alert := a14Decide(86, 3<<30, limit); to != limit/2 || alert {
		t.Fatalf("disk 86 %%, journal 3 GB → vacuum to 2 GB: %d %v", to, alert)
	}
	if to, alert := a14Decide(85, 3<<30, limit); to != 0 || alert {
		t.Fatalf("85 %% is not above 85: %d %v", to, alert)
	}
	if to, alert := a14Decide(92, 1<<30, limit); to != 0 || !alert {
		t.Fatalf("nothing to clean in the journal → the disk alert: %d %v", to, alert)
	}
}

func TestSnapshotDue(t *testing.T) {
	if !snapshotDue("k", AlertCritical, time.Time{}, autoT0) {
		t.Fatal("a critical alert without a snapshot → due")
	}
	if snapshotDue("k", AlertWarn, time.Time{}, autoT0) {
		t.Fatal("warn alerts get none")
	}
	if snapshotDue("k", AlertCritical, autoT0.Add(-9*time.Minute), autoT0) {
		t.Fatal("not more often than every 10 minutes")
	}
	if !snapshotDue("k", AlertCritical, autoT0.Add(-10*time.Minute), autoT0) {
		t.Fatal("10 minutes later → due again")
	}
}

func TestAutoRulesCatalogue(t *testing.T) {
	if len(autoRules) != 16 {
		t.Fatalf("A1…A16: %d rules", len(autoRules))
	}
	for i, r := range autoRules {
		if r.ID != "A"+strconv.Itoa(i+1) || r.Title == "" || r.When == "" || r.Does == "" {
			t.Errorf("rule %d: %+v", i, r)
		}
	}
	for _, id := range []string{"A1", "A2", "A12", "A14", "A16"} {
		if r := autoRuleByID(id); r.Builtin {
			t.Errorf("%s is a rule of this engine", id)
		}
	}
	for _, id := range []string{"A3", "A6", "A7"} {
		if r := autoRuleByID(id); !r.Builtin || r.Setting == "" {
			t.Errorf("%s is built in elsewhere and switched by its setting: %+v", id, r)
		}
	}
}

// The automation never returns an entry or an exit to service, never switches a flag on, never
// restarts xray or Bamboo, never reboots: checked on the source, like TestAlertInfoCoversAllKeys.
func TestAutomationNeverDoes(t *testing.T) {
	files, _ := filepath.Glob("automation*.go")
	if len(files) == 0 {
		t.Fatal("no automation sources")
	}
	forbiddenCalls := map[string]bool{"setFlagState": true, "applyFlagState": true, "restartXray": true, "restartHysteria2Server": true,
		"recordXrayRestart": true, "setExitOutOfService": true}
	fset := token.NewFileSet()
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, w := range []string{"KindReboot", "systemctl reboot", "EntryRotationTest"} {
			if strings.Contains(string(src), w) {
				t.Errorf("%s: %q", f, w)
			}
		}
		file, err := parser.ParseFile(fset, f, src, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			name := ""
			switch fn := call.Fun.(type) {
			case *ast.Ident:
				name = fn.Name
			case *ast.SelectorExpr:
				name = fn.Sel.Name
			}
			if forbiddenCalls[name] {
				t.Errorf("%s: calls %s", f, name)
			}
			if name == "SetEntryRotation" {
				if len(call.Args) < 4 {
					t.Errorf("%s: SetEntryRotation args", f)
					return true
				}
				to, ok := call.Args[3].(*ast.SelectorExpr)
				if !ok || to.Sel.Name != "EntryRotationOff" {
					t.Errorf("%s: SetEntryRotation may only move an entry to «off»", f)
				}
			}
			return true
		})
	}
}
