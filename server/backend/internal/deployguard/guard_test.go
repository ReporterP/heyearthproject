package deployguard

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func clean() Local {
	return Local{SHA: "bbb2222", OnMain: true, Contains: func(string) (bool, error) { return true, nil }}
}

func has(list []string, sub string) bool {
	for _, s := range list {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

func TestGuardRefusals(t *testing.T) {
	l := clean()
	l.Dirty = true
	if v := Check(l, Remote{DeployedSHA: "aaa1111"}); !has(v.Refusals, "рабочее дерево не чистое") {
		t.Errorf("dirty tree: %+v", v)
	}
	l = clean()
	l.OnMain = false
	if v := Check(l, Remote{DeployedSHA: "aaa1111"}); !has(v.Refusals, "коммит не из main") {
		t.Errorf("not on main: %+v", v)
	}
	l = clean()
	l.Contains = func(sha string) (bool, error) { return sha != "abc", nil }
	v := Check(l, Remote{DeployedSHA: "abc"})
	if !has(v.Refusals, "откатит чужую правку") || !has(v.Refusals, "abc") || !has(v.Refusals, "bbb2222") {
		t.Errorf("not a descendant: %+v", v)
	}
	v = Check(clean(), Remote{DeployedSHA: "aaa1111", LockHolder: "alex@pc 2026-11-24T10:00:00Z", LockAge: 5 * time.Minute})
	if !has(v.Refusals, "alex@pc") || !has(v.Refusals, "идёт другая выкатка") {
		t.Errorf("fresh lock: %+v", v)
	}
}

// Review Focus 3: the guard must never lock a deploy out for good.
func TestGuardStaleLockAndUnknownSHA(t *testing.T) {
	v := Check(clean(), Remote{DeployedSHA: "aaa1111", LockHolder: "alex@pc", LockAge: 31 * time.Minute})
	if len(v.Refusals) != 0 || !has(v.Warnings, "замок") || !has(v.Warnings, "alex@pc") {
		t.Errorf("stale lock: %+v, want a warning only", v)
	}
	for _, sha := range []string{"", "dev", "unknown"} {
		v := Check(clean(), Remote{DeployedSHA: sha})
		if len(v.Refusals) != 0 || !has(v.Warnings, "выкаченная сборка неизвестна") {
			t.Errorf("deployed %q: %+v, want a warning only", sha, v)
		}
	}
	l := clean()
	l.Contains = func(string) (bool, error) { return false, errors.New("fatal: bad object aaa1111") }
	v = Check(l, Remote{DeployedSHA: "aaa1111"})
	if len(v.Refusals) != 0 || !has(v.Warnings, "aaa1111") {
		t.Errorf("Contains error: %+v, want a warning only", v)
	}
	// a build from a dirty tree: its commit is checked, and the owner is told
	l = clean()
	var asked string
	l.Contains = func(sha string) (bool, error) { asked = sha; return true, nil }
	v = Check(l, Remote{DeployedSHA: "aaa1111-dirty"})
	if asked != "aaa1111" || len(v.Refusals) != 0 || !has(v.Warnings, "грязного дерева") {
		t.Errorf("dirty deployed build: asked %q, %+v", asked, v)
	}
}

func TestGuardClean(t *testing.T) {
	if v := Check(clean(), Remote{DeployedSHA: "aaa1111"}); len(v.Refusals)+len(v.Warnings) != 0 {
		t.Errorf("clean deploy: %+v", v)
	}
	// the same build again is fine too
	if v := Check(clean(), Remote{DeployedSHA: "bbb2222"}); len(v.Refusals)+len(v.Warnings) != 0 {
		t.Errorf("same build: %+v", v)
	}
}

// The local part is decided before any connection to the Hub.
func TestCheckLocal(t *testing.T) {
	l := clean()
	l.Dirty, l.OnMain = true, false
	v := CheckLocal(l)
	if len(v.Refusals) != 2 || len(v.Warnings) != 0 {
		t.Errorf("CheckLocal = %+v", v)
	}
	if v := CheckLocal(clean()); len(v.Refusals)+len(v.Warnings) != 0 {
		t.Errorf("clean: %+v", v)
	}
}

func TestForce(t *testing.T) {
	v := Verdict{Refusals: []string{"коммит не из main"}, Warnings: []string{"w"}}
	if _, err := v.Force("коротко"); err == nil {
		t.Error("a reason shorter than 10 characters must be refused")
	}
	f, err := v.Force("горит прод, откат вручную")
	if err != nil || len(f.Refusals) != 0 || len(f.Warnings) != 2 || !has(f.Warnings, "коммит не из main") {
		t.Errorf("Force = %+v, %v", f, err)
	}
}

func TestParseRemote(t *testing.T) {
	now := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
	out := "DEPLOYED {\"api\":{\"sha\":\"aaa1111\",\"at\":\"2026-11-24T09:00:00Z\"},\"admin\":{\"sha\":\"ccc3333\",\"at\":\"2026-11-24T09:01:00Z\"}}\n" +
		"LOCK alex@pc 2026-11-24T11:50:00Z\nLOCKAGE 600\nBOOT 2026/11/24 00:09:39 [boot] сборка 48cdd5caec6f (собрана 2026-11-24T00:05:00Z)\n"
	r, doc := ParseRemote("admin", out, now)
	if r.DeployedSHA != "ccc3333" || r.LockHolder != "alex@pc 2026-11-24T11:50:00Z" || r.LockAge != 10*time.Minute {
		t.Errorf("admin: %+v", r)
	}
	if !strings.Contains(string(doc), "aaa1111") {
		t.Errorf("deployed.json not kept: %s", doc)
	}
	// the API without its record yet: the build comes from the journal
	r, _ = ParseRemote("api", "DEPLOYED \nBOOT 2026/11/24 00:09:39 [boot] сборка 48cdd5caec6f (собрана —)\n", now)
	if r.DeployedSHA != "48cdd5caec6f" || r.LockHolder != "" {
		t.Errorf("api from the journal: %+v", r)
	}
	// nothing known at all
	r, _ = ParseRemote("site", "DEPLOYED \n", now)
	if r.DeployedSHA != "" {
		t.Errorf("site: %+v", r)
	}
}

func TestMergeDeployed(t *testing.T) {
	at := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
	b, err := MergeDeployed([]byte(`{"api":{"sha":"aaa1111","at":"x"}}`), "admin", "ccc3333", at)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]struct{ SHA, At string }
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	if m["api"].SHA != "aaa1111" || m["admin"].SHA != "ccc3333" || m["admin"].At != "2026-11-24T12:00:00Z" {
		t.Errorf("merged %s", b)
	}
	// an unreadable file is replaced, not a reason to fail the deploy
	if b, err := MergeDeployed([]byte("garbage"), "api", "ddd4444", at); err != nil || !strings.Contains(string(b), "ddd4444") {
		t.Errorf("garbage: %s %v", b, err)
	}
}

// fakeHub runs the shell commands of the guard against an in-memory Hub.
type fakeHub struct {
	locked  bool
	holder  string
	written map[string][]byte
	cmds    []string
}

func (h *fakeHub) run(cmd string, stdin []byte) (string, error) {
	h.cmds = append(h.cmds, cmd)
	switch {
	case strings.Contains(cmd, "mkdir "+LockDir):
		if h.locked && !strings.Contains(cmd, "rm -rf "+LockDir) {
			return "BUSY\n", nil
		}
		h.locked, h.holder = true, string(stdin)
		return "LOCKED\n", nil
	case strings.HasPrefix(cmd, "rm -rf "+LockDir):
		h.locked = false
		return "", nil
	case strings.Contains(cmd, DeployedFile) && stdin != nil:
		if h.written == nil {
			h.written = map[string][]byte{}
		}
		h.written[DeployedFile] = stdin
		return "WRITTEN\n", nil
	case strings.Contains(cmd, "/internal/ops/event"):
		if h.written == nil {
			h.written = map[string][]byte{}
		}
		h.written["feed"] = stdin
		return "200", nil
	}
	return "", nil
}

func TestLockAndRecord(t *testing.T) {
	h := &fakeHub{}
	if err := Lock(h.run, "alex@pc", false); err != nil {
		t.Fatal(err)
	}
	if err := Lock(h.run, "other@pc", false); err == nil {
		t.Fatal("a second lock must fail while the first is held")
	}
	if err := Lock(h.run, "other@pc", true); err != nil || !strings.Contains(h.holder, "other@pc") {
		t.Fatalf("take over a stale lock: %v, holder %q", err, h.holder)
	}
	if err := Unlock(h.run); err != nil || h.locked {
		t.Fatalf("unlock: %v locked=%v", err, h.locked)
	}
	if err := Record(h.run, []byte(`{}`), "api", "aaa1111", time.Now()); err != nil || !strings.Contains(string(h.written[DeployedFile]), "aaa1111") {
		t.Fatalf("record: %v %q", err, h.written[DeployedFile])
	}
	err := Feed(h.run, Event{Kind: "action", Actor: "deploy", Target: "admin", Impact: "K0", Summary: "выкачена админка aaa1111", OK: true})
	if err != nil || !strings.Contains(string(h.written["feed"]), `"kind":"action"`) {
		t.Fatalf("feed: %v %q", err, h.written["feed"])
	}
	// the key never travels as an argument: it is read on the Hub by the command itself
	for _, c := range h.cmds {
		if strings.Contains(c, "/internal/ops/event") && !strings.Contains(c, IntakeKeyFile) {
			t.Errorf("feed command does not read the key file: %q", c)
		}
	}
}

// fakeRemote answers ReadRemoteCmd with a fixed Hub state and runs the rest on fakeHub.
func fakeRemote(h *fakeHub, state string) Run {
	return func(cmd string, stdin []byte) (string, error) {
		if cmd == ReadRemoteCmd() {
			return state, nil
		}
		return h.run(cmd, stdin)
	}
}

func TestGateRefusedTakesNoLock(t *testing.T) {
	h := &fakeHub{}
	l := clean()
	l.Contains = func(string) (bool, error) { return false, nil }
	var out strings.Builder
	g, err := Open(fakeRemote(h, "DEPLOYED {\"api\":{\"sha\":\"aaa1111\"}}\n"), "api", "alex@pc", l, "", time.Now(), &out)
	if !errors.Is(err, ErrRefused) || g != nil || h.locked {
		t.Fatalf("Open = %v, %v; locked=%v", g, err, h.locked)
	}
	if !strings.Contains(out.String(), "ОТКАЗ") || !strings.Contains(out.String(), "откатит чужую правку") {
		t.Errorf("output %q", out.String())
	}
}

func TestGateHappyPath(t *testing.T) {
	h := &fakeHub{}
	var out strings.Builder
	g, err := Open(fakeRemote(h, "DEPLOYED {\"api\":{\"sha\":\"aaa1111\"}}\n"), "admin", "alex@pc", clean(), "", time.Now(), &out)
	if err != nil || !h.locked {
		t.Fatalf("Open: %v, locked=%v, out %q", err, h.locked, out.String())
	}
	if err := g.Done("bbb2222", time.Now()); err != nil {
		t.Fatal(err)
	}
	doc := string(h.written[DeployedFile])
	if h.locked || !strings.Contains(doc, "bbb2222") || !strings.Contains(doc, "aaa1111") {
		t.Errorf("after Done: locked=%v deployed.json %s", h.locked, doc)
	}
	g.Release() // a second release after Done is harmless
}

func TestGateStaleLockTakenOver(t *testing.T) {
	h := &fakeHub{locked: true, holder: "old@pc"}
	var out strings.Builder
	g, err := Open(fakeRemote(h, "DEPLOYED \nLOCK old@pc\nLOCKAGE 3600\n"), "api", "alex@pc", clean(), "", time.Now(), &out)
	if err != nil || !strings.Contains(h.holder, "alex@pc") {
		t.Fatalf("Open: %v, holder %q", err, h.holder)
	}
	if !strings.Contains(out.String(), "ВНИМАНИЕ") {
		t.Errorf("no warning printed: %q", out.String())
	}
	g.Release()
	if h.locked {
		t.Error("Release left the lock")
	}
}

func TestGateForce(t *testing.T) {
	h := &fakeHub{}
	l := clean()
	l.OnMain = false
	var out strings.Builder
	if _, err := Open(fakeRemote(h, "DEPLOYED \n"), "api", "alex@pc", l, "коротко", time.Now(), &out); err == nil {
		t.Fatal("a short reason must be refused")
	}
	g, err := Open(fakeRemote(h, "DEPLOYED \n"), "api", "alex@pc", l, "срочный откат на прод", time.Now(), &out)
	if err != nil || g == nil || !h.locked {
		t.Fatalf("forced Open: %v", err)
	}
	if !strings.Contains(out.String(), "обойдено") {
		t.Errorf("the bypass is not printed: %q", out.String())
	}
}
