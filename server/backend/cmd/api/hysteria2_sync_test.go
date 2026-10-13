package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/heyearth/backend/internal/biz"
)

// hysteria2-server restarts only when server.yaml's bytes change (SyncHysteria2Config compares
// them first): two hours of RTT jitter and a flapping second exit must not change them; a real
// failover changes them once, onto the new exit's own port.
func TestBambooRestartsOnlyWhenTheExitChanges(t *testing.T) {
	t0 := time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC)
	sel := biz.NewExpressSelector(2, t0)
	render := func(c biz.ExpressChoice) string {
		return renderHysteria2Config(100, 100, "obfs", "stats", c.SocksPort, []string{"138.16.181.225"})
	}
	last := render(sel.Current())
	if !strings.Contains(last, "addr: 127.0.0.1:1090") {
		t.Fatalf("exit #2 must keep Bamboo on 127.0.0.1:1090 (no restart at deploy):\n%s", last)
	}
	restarts := 0
	for i := 1; i <= 240; i++ {
		rtt2, rtt3 := 100+i%15, 95+(i*7)%15
		sel.Observe(2, rtt2)
		sel.Observe(3, rtt3)
		cs := []biz.ExpressCandidate{{ID: 2, RTTms: rtt2}, {ID: 3, RTTms: rtt3}}
		if i%3 == 0 {
			cs = cs[:1] // exit 3 flaps out every third tick
		}
		if y := render(sel.Choose(t0.Add(time.Duration(i)*30*time.Second), cs)); y != last {
			restarts++
			last = y
		}
	}
	if restarts != 0 {
		t.Fatalf("hysteria2-server restarted %d times without a real exit change", restarts)
	}
	y := render(sel.Choose(t0.Add(3*time.Hour), []biz.ExpressCandidate{{ID: 3, RTTms: 100}}))
	if y == last || !strings.Contains(y, "addr: 127.0.0.1:1091") {
		t.Fatalf("failover must move via_exit to exit #3's own 127.0.0.1:1091:\n%s", y)
	}
}

// A switch is committed (memory + app_settings) only once server.yaml carries it: a failed
// validation or restart keeps the old exit current, and the next sync proposes the switch again.
func TestBambooChoiceCommittedOnlyWhenServerYAMLCarriesIt(t *testing.T) {
	t0 := time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC)
	sel := biz.NewExpressSelector(2, t0)
	render := func(port int) string {
		return renderHysteria2Config(100, 100, "obfs", "stats", port, []string{"138.16.181.225"})
	}
	var installed []string
	installErr := errors.New("validation failed (keeping existing config)")
	install := func(yaml string) (bool, error) {
		if installErr != nil {
			return false, installErr
		}
		installed = append(installed, yaml)
		return true, nil
	}
	commits := 0
	commit := func(c biz.ExpressChoice) error { commits++; sel.Commit(c); return nil }
	exit3 := []biz.ExpressCandidate{{ID: 3, RTTms: 100}} // exit 2 lost its Hysteria path

	c := sel.Decide(t0.Add(time.Minute), exit3)
	if !c.Switched || c.ExitID != 3 {
		t.Fatalf("want a proposed failover to #3: %+v", c)
	}
	if _, err := applyBambooChoice(c, render, install, commit); !errors.Is(err, installErr) {
		t.Fatalf("install error not returned: %v", err)
	}
	if cur := sel.Current(); cur.ExitID != 2 || cur.SocksPort != 1090 || commits != 0 {
		t.Fatalf("a failed install committed the switch: %+v, commits %d", cur, commits)
	}

	installErr = nil
	c = sel.Decide(t0.Add(6*time.Minute), exit3)
	if !c.Switched || c.ExitID != 3 {
		t.Fatalf("the next sync must propose the switch again: %+v", c)
	}
	changed, err := applyBambooChoice(c, render, install, commit)
	if err != nil || !changed || len(installed) != 1 || !strings.Contains(installed[0], "addr: 127.0.0.1:1091") {
		t.Fatalf("want server.yaml on 1091 installed: changed %v, err %v, %d installs", changed, err, len(installed))
	}
	if cur := sel.Current(); cur.ExitID != 3 || !cur.Since.Equal(t0.Add(6*time.Minute)) || commits != 1 {
		t.Fatalf("a successful install must commit the switch once: %+v, commits %d", cur, commits)
	}

	// Unchanged bytes (the file already carries the exit, e.g. a restart that failed after the
	// rename): the choice is committed as well.
	sel = biz.NewExpressSelector(2, t0)
	c = sel.Decide(t0.Add(time.Minute), exit3)
	unchanged := func(string) (bool, error) { return false, nil }
	if changed, err := applyBambooChoice(c, render, unchanged, commit); err != nil || changed || sel.Current().ExitID != 3 {
		t.Fatalf("unchanged bytes must commit: changed %v, err %v, %+v", changed, err, sel.Current())
	}
}

// The new server.yaml was renamed into place but `systemctl restart hysteria2-server` failed: the
// running server still has the old exit. The next syncs see equal bytes, yet must retry the
// restart and commit the switch only once it succeeds.
func TestBambooRestartRetriedBeforeCommit(t *testing.T) {
	t0 := time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC)
	render := func(port int) string {
		return renderHysteria2Config(100, 100, "obfs", "stats", port, []string{"138.16.181.225"})
	}
	path := filepath.Join(t.TempDir(), "server.yaml")
	if err := os.WriteFile(path, []byte(render(1090)), 0o600); err != nil {
		t.Fatal(err)
	}
	restartErr := errors.New("Job for hysteria2-server.service failed")
	restarts := 0
	in := &hysteria2Installer{path: path, validate: func(string) error { return nil },
		restart: func() error { restarts++; return restartErr }}
	sel := biz.NewExpressSelector(2, t0)
	commit := func(c biz.ExpressChoice) error { sel.Commit(c); return nil }
	exit3 := []biz.ExpressCandidate{{ID: 3, RTTms: 100}}
	sync := func(minute int) (bool, error) {
		return applyBambooChoice(sel.Decide(t0.Add(time.Duration(minute)*time.Minute), exit3), render, in.install, commit)
	}

	if _, err := sync(1); !errors.Is(err, restartErr) {
		t.Fatalf("restart error not returned: %v", err)
	}
	if b, _ := os.ReadFile(path); string(b) != render(1091) {
		t.Fatal("the new server.yaml must be in place after the rename")
	}
	if c := sel.Current(); c.ExitID != 2 || restarts != 1 {
		t.Fatalf("a failed restart committed the switch: %+v, restarts %d", c, restarts)
	}
	if _, err := sync(6); !errors.Is(err, restartErr) || restarts != 2 || sel.Current().ExitID != 2 {
		t.Fatalf("equal bytes after a failed restart: want a retried restart and no commit: err %v, restarts %d, %+v",
			err, restarts, sel.Current())
	}
	restartErr = nil
	if changed, err := sync(11); err != nil || !changed || restarts != 3 || sel.Current().ExitID != 3 {
		t.Fatalf("want the retried restart to succeed and commit: changed %v, err %v, restarts %d, %+v",
			changed, err, restarts, sel.Current())
	}
	if changed, err := sync(16); err != nil || changed || restarts != 3 {
		t.Fatalf("steady state must not restart: changed %v, err %v, restarts %d", changed, err, restarts)
	}
}

// The rollout must not restart hysteria2-server: on the live Hub app_settings holds
// hysteria2_bound_exit_id = 2 (written by the removed shared mode), and the server.yaml the new
// code renders for it must be byte-identical to the one the previous code rendered
// (testdata/hysteria2_server_exit2.golden was generated by the code before s6yc/7yrs).
func TestBambooServerYAMLUnchangedAtRollout(t *testing.T) {
	c := biz.NewExpressSelector(2, time.Time{}).Choose(time.Date(2026, 10, 13, 0, 0, 0, 0, time.UTC),
		[]biz.ExpressCandidate{{ID: 2, RTTms: 60}})
	if c.Switched || c.ExitID != 2 || c.SocksPort != 1090 {
		t.Fatalf("the seeded exit #2 must stay without a switch: %+v", c)
	}
	checkGolden(t, "hysteria2_server_exit2",
		[]byte(renderHysteria2Config(100, 100, "obfs", "stats", c.SocksPort, []string{"138.16.181.225"})))
}
