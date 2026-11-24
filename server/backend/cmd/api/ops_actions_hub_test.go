package main

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/heyearth/backend/internal/biz"
)

type fakeOpsHub struct {
	mu        sync.Mutex
	cmds      []string
	failCmd   map[string]error // by the command's first words
	xray      int
	causes    []string
	bamboo    int
	synth     string
	pending   []string
	startRun  string // "" — none yet
	backupAge time.Duration
}

func (f *fakeOpsHub) deps() hubDeps {
	return hubDeps{
		exec: func(_ context.Context, name string, args ...string) (string, error) {
			f.mu.Lock()
			defer f.mu.Unlock()
			cmd := strings.Join(append([]string{name}, args...), " ")
			f.cmds = append(f.cmds, cmd)
			for prefix, err := range f.failCmd {
				if strings.HasPrefix(cmd, prefix) {
					return "nginx: [emerg] unknown directive in /etc/nginx/conf.d/x.conf:3", err
				}
			}
			return "", nil
		},
		restartXray:     func() error { f.xray++; return nil },
		xrayRestarted:   func(cause string) { f.causes = append(f.causes, cause) },
		restartBamboo:   func() error { f.bamboo++; return nil },
		bambooRestarted: func(string) {},
		synth: func(context.Context) (string, string, error) {
			if f.synth == "" {
				return "pass", "17 проверок, все прошли", nil
			}
			return f.synth, "2 проверки не прошли", nil
		},
		online: func(time.Time) onlineCounts { return onlineCounts{Xray: 40, Bamboo: 7, Total: 45} },
		exit: func(_ context.Context, id int) (biz.ExitNodeTunnel, error) {
			if id != 3 {
				return biz.ExitNodeTunnel{}, errors.New("нет такого выхода")
			}
			return biz.ExitNodeTunnel{ID: 3}, nil
		},
		probeExit:    func(biz.ExitNodeTunnel) exitPathProbe { return exitPathProbe{hy: pathProbe{present: true, ok: true}} },
		hasHy:        func(biz.ExitNodeTunnel) bool { return true },
		hasVR:        func(biz.ExitNodeTunnel) bool { return false },
		newestBackup: func() (time.Time, error) { return time.Now().Add(-f.backupAge), nil },
		siteOK:       func(context.Context) error { return nil },
		startRunAfter: func(context.Context, time.Time) (string, string, bool, error) {
			if f.startRun == "" {
				return "", "", false, nil
			}
			return f.startRun, "прогон start", true, nil
		},
		pending:      func() []string { return f.pending },
		resetRevoked: func() {},
		seenSince:    func(string, time.Time) (bool, bool) { return false, true },
		sleep:        func(time.Duration) {},
	}
}

func hubStand(t *testing.T, f *fakeOpsHub) *opsStand {
	t.Helper()
	s := newOpsStand(t, buildHubActions(f.deps())...)
	for _, a := range s.o.actions {
		if disruptive(a.Impact) {
			_ = s.store.setMode(context.Background(), a.ID, opsModeOn, "owner")
		}
	}
	return s
}

func (s *opsStand) runAction(t *testing.T, id, target, typed string) (int, map[string]any) {
	t.Helper()
	tok := s.preview(t, id, target)["confirm_token"].(string)
	return s.do(t, http.MethodPost, "/api/admin/ops/actions/"+id+"/run",
		map[string]string{"target": target, "when": "now", "confirm_token": tok, "typed": typed})
}

func TestHubActionsRegisteredWithTheirClass(t *testing.T) {
	want := []struct {
		id, impact, perm, word, target string
		gap                            time.Duration
	}{
		{"hub.status", "K0", "servers.read", "", "", 0},
		{"probes.run", "K0", "settings.write", "", "", 0},
		{"backup.run", "K0", "settings.write", "", "", 0},
		{"hub.reload-nginx", "K0", "settings.write", "", "", 0},
		{"hub.restart-api", "K1", "settings.write", "", "", 5 * time.Minute},
		{"hub.restart-tunnels", "K2", "ops.disrupt", "", "exit", 10 * time.Minute},
		{"hub.restart-bamboo", "K2", "ops.disrupt", "", "", 10 * time.Minute},
		{"hub.restart-xray", "K3", "ops.disrupt", "XRAY", "", 30 * time.Minute},
		{"user.disconnect-now", "K3", "ops.disrupt", "XRAY", "", 30 * time.Minute},
	}
	for _, w := range want {
		a, ok := opsActions[w.id] // the live registry
		if !ok {
			t.Errorf("%s is not registered", w.id)
			continue
		}
		if a.Impact != w.impact || requiredPerm(a) != w.perm || a.Confirm != w.word || a.Target != w.target || a.MinGap != w.gap {
			t.Errorf("%s: impact %s perm %s word %q target %q gap %v; want %s %s %q %q %v",
				w.id, a.Impact, requiredPerm(a), a.Confirm, a.Target, a.MinGap, w.impact, w.perm, w.word, w.target, w.gap)
		}
		if a.Title == "" || a.Run == nil {
			t.Errorf("%s: a title and a Run", w.id)
		}
	}
	if !opsActions["hub.restart-api"].HandOver || opsActions["hub.restart-api"].Resume == nil {
		t.Error("hub.restart-api is closed by the next process")
	}
}

// Every action id the alert catalogue offers is either registered or a known link elsewhere.
func TestAlertCatalogActionsExist(t *testing.T) {
	elsewhere := func(id string) bool {
		return strings.HasPrefix(id, "entry.") || strings.HasPrefix(id, "exit.") || strings.HasPrefix(id, "node.") ||
			strings.HasPrefix(id, "subscription.") || strings.HasPrefix(id, "dns.") || strings.HasPrefix(id, "payments.") ||
			strings.HasPrefix(id, "bot.") || strings.HasPrefix(id, "watchdog.") || strings.HasPrefix(id, "user.") ||
			strings.HasPrefix(id, "settings.") || strings.HasPrefix(id, "servers.")
	}
	for _, e := range alertCatalog {
		for _, id := range e.Info.Actions {
			if _, ok := opsActions[id]; !ok && !elsewhere(id) && !strings.HasPrefix(id, "hub.") && id != "probes.run" && id != "backup.run" {
				t.Errorf("catalogue %s%s offers an unknown action %q", e.Key, e.Prefix, id)
			}
		}
	}
}

func TestReloadNginxPreconditionBlocksReload(t *testing.T) {
	f := &fakeOpsHub{failCmd: map[string]error{"nginx -t": errors.New("exit status 1")}}
	s := hubStand(t, f)
	code, out := s.runAction(t, "hub.reload-nginx", "", "")
	if code != http.StatusUnprocessableEntity {
		t.Fatalf("nginx -t failed → 422: %d %v", code, out)
	}
	for _, c := range f.cmds {
		if strings.Contains(c, "reload") {
			t.Fatalf("reload must not run: %v", f.cmds)
		}
	}
	f.failCmd = nil
	s.now = s.now.Add(time.Minute)
	if code, out := s.runAction(t, "hub.reload-nginx", "", ""); code != http.StatusAccepted {
		t.Fatalf("a good config reloads: %d %v", code, out)
	}
	if last := f.cmds[len(f.cmds)-1]; last != "systemctl reload nginx" {
		t.Fatalf("reload: %v", f.cmds)
	}
}

func TestRestartXrayRecordsTheManualCause(t *testing.T) {
	f := &fakeOpsHub{}
	s := hubStand(t, f)
	code, out := s.runAction(t, "hub.restart-xray", "", "XRAY")
	if code != http.StatusAccepted || f.xray != 1 {
		t.Fatalf("restart: %d %v, xray %d", code, out, f.xray)
	}
	if len(f.causes) != 1 || f.causes[0] != "вручную: owner" {
		t.Fatalf("the restart's cause names who: %v", f.causes)
	}
	if s.feed[0].Impact != "K3" {
		t.Fatalf("the action's feed line carries K3: %+v", s.feed[0])
	}
	r, _ := s.store.get(context.Background(), int64(out["run_id"].(float64)))
	if r.Status != opsOK || len(r.Post) == 0 {
		t.Fatalf("the synthetic run is the check after: %+v", r)
	}
	p := s.preview(t, "hub.restart-xray", "")
	if p["affected_now"].(float64) != 40 {
		t.Fatalf("affected = online on xray: %v", p["affected_now"])
	}
}

func TestRestartXrayFailedSyntheticIsPostFailed(t *testing.T) {
	f := &fakeOpsHub{synth: "fail"}
	s := hubStand(t, f)
	_, out := s.runAction(t, "hub.restart-xray", "", "XRAY")
	r, _ := s.store.get(context.Background(), int64(out["run_id"].(float64)))
	if r.Status != opsPostFailed || len(s.raised) != 1 {
		t.Fatalf("a failing run after the restart → post_failed and an alert: %+v %v", r, s.raised)
	}
}

func TestRestartAPIClosedByTheNextProcess(t *testing.T) {
	f := &fakeOpsHub{}
	s := hubStand(t, f)
	code, out := s.runAction(t, "hub.restart-api", "", "")
	if code != http.StatusAccepted {
		t.Fatalf("run: %d %v", code, out)
	}
	id := int64(out["run_id"].(float64))
	r, _ := s.store.get(context.Background(), id)
	if r.Status != opsRunning || f.cmds[len(f.cmds)-1] != "systemctl restart --no-block heyearth-api" {
		t.Fatalf("the run waits for the next process: %+v %v", r, f.cmds)
	}
	// The next process: recovery keeps it, the start run closes it.
	s.now = s.now.Add(time.Minute)
	s.o.recover(context.Background())
	if r, _ := s.store.get(context.Background(), id); r.Status != opsRunning {
		t.Fatalf("a fresh hand-over run is not interrupted: %+v", r)
	}
	f.startRun = "pass"
	s.o.resumeHandOvers(context.Background())
	r, _ = s.store.get(context.Background(), id)
	if r.Status != opsOK || len(r.Post) != 1 || !r.Post[0].OK {
		t.Fatalf("the start run of the next process closes it: %+v", r)
	}
	if last := s.feed[len(s.feed)-1]; !last.OK || !strings.Contains(last.Summary, "готово") {
		t.Fatalf("feed: %+v", s.feed)
	}
}

func TestRestartAPIBlindStartRunProvesNothing(t *testing.T) {
	f := &fakeOpsHub{startRun: "blind"}
	s := hubStand(t, f)
	_, out := s.runAction(t, "hub.restart-api", "", "")
	id := int64(out["run_id"].(float64))
	s.o.resumeHandOvers(context.Background())
	if r, _ := s.store.get(context.Background(), id); r.Status != opsOK || len(s.levels) != 1 || s.levels[0] != AlertWarn {
		t.Fatalf("a blind start run proves nothing: done, with a warning (review #7): %+v %v", r, s.levels)
	}
}

func TestRestartTunnelsNeedsAnExit(t *testing.T) {
	f := &fakeOpsHub{}
	s := hubStand(t, f)
	if code, _ := s.runAction(t, "hub.restart-tunnels", "9", ""); code != http.StatusUnprocessableEntity {
		t.Fatalf("an unknown exit fails the check before: %d", code)
	}
	code, out := s.runAction(t, "hub.restart-tunnels", "3", "")
	if code != http.StatusAccepted {
		t.Fatalf("run: %d %v", code, out)
	}
	if last := f.cmds[len(f.cmds)-1]; last != "systemctl restart hysteria2-client-3" {
		t.Fatalf("only the exit's present tunnel units: %v", f.cmds)
	}
	if r, _ := s.store.get(context.Background(), int64(out["run_id"].(float64))); r.Status != opsOK {
		t.Fatalf("the tunnel probe is the check after: %+v", r)
	}
}

func TestDisconnectNowNeedsSomeoneToDisconnect(t *testing.T) {
	f := &fakeOpsHub{}
	s := hubStand(t, f)
	if code, _ := s.runAction(t, "user.disconnect-now", "", "XRAY"); code != http.StatusUnprocessableEntity || f.xray != 0 {
		t.Fatalf("nobody blocked is online → no K3 restart: %d, xray %d", code, f.xray)
	}
	f.pending = []string{"5f0c1a2e-0000-4000-8000-00000000000a"}
	code, out := s.runAction(t, "user.disconnect-now", "", "XRAY")
	if code != http.StatusAccepted || f.xray != 1 || !strings.Contains(f.causes[0], "заблокированных") {
		t.Fatalf("restart with its cause: %d %v %v", code, out, f.causes)
	}
	if r, _ := s.store.get(context.Background(), int64(out["run_id"].(float64))); r.Status != opsOK {
		t.Fatalf("nobody of them is back online: %+v", r)
	}
}

func TestBackupRunChecksAFreshFile(t *testing.T) {
	f := &fakeOpsHub{backupAge: 3 * time.Hour}
	s := hubStand(t, f)
	_, out := s.runAction(t, "backup.run", "", "")
	if r, _ := s.store.get(context.Background(), int64(out["run_id"].(float64))); r.Status != opsPostFailed {
		t.Fatalf("no fresh file after the backup → post_failed: %+v", r)
	}
	if f.cmds[len(f.cmds)-1] != "systemctl start heyearth-backup.service" {
		t.Fatalf("the backup unit: %v", f.cmds)
	}
}
