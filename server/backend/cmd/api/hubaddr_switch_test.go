package main

import (
	"context"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/heyearth/backend/internal/provision"
)

// switchRig is a wizard in «dns_ready» with a fake clock, a node runner whose jobs can stay
// running, a Hub that runs the revert script and keeps the timer state, and one event log of the
// Hub's scripts, setting writes and node jobs in order.
type switchRig struct {
	*testWizard
	id     int64
	runner *fakeRunner
	mu     sync.Mutex
	clock  time.Time
	events []string
	hold   map[int]int // server id → polls a job stays running
}

func newSwitchRig(t *testing.T) *switchRig {
	t.Helper()
	w := newTestWizard(t)
	r := &switchRig{testWizard: w, clock: time.Date(2026, 11, 21, 3, 0, 0, 0, time.UTC), hold: map[int]int{}}
	r.runner = withRunner(w)
	w.e.env.Now = func() time.Time { r.mu.Lock(); defer r.mu.Unlock(); return r.clock }
	w.e.env.Sleep = func(_ context.Context, d time.Duration) error {
		r.mu.Lock()
		r.clock = r.clock.Add(d)
		r.mu.Unlock()
		return nil
	}
	w.e.env.ExitTunnels = func(context.Context) ([]hubAddrProbe, error) {
		return []hubAddrProbe{{Name: "Выход 2", OK: true}}, nil
	}
	w.e.env.Bamboo = func(int, string) error { return nil }
	set := w.e.env.SetSetting
	w.e.env.SetSetting = func(k, v string) error { r.event("set:" + k + "=" + v); return set(k, v) }
	w.hub.onScript = func(s string) {
		for _, k := range []string{"systemd-run", "DISARMED", "ip route replace", "TUNNELS_RESTARTED", "COUNTER_OK", "NETPLAN_OK", "COUNTER_DROPPED"} {
			if strings.Contains(s, k) {
				r.event("hub:" + k)
			}
		}
		if strings.HasPrefix(s, "/bin/sh "+hubAddrStateDir) { // the revert script, run by hand
			w.hub.revertNow(w)
		}
	}
	r.runner.onStart = func(id int) { r.event("job:" + strconv.Itoa(id)) }
	r.runner.hold = r.hold
	w.hub.now = w.e.env.Now
	r.id = wizardAt(t, w, hubAddrDNSReady)
	r.mu.Lock()
	r.events = nil // what step 1 did while the rig set the state up is not the switch's
	r.mu.Unlock()
	return r
}

func (r *switchRig) event(e string) {
	r.mu.Lock()
	r.events = append(r.events, e)
	r.mu.Unlock()
}

func (r *switchRig) indexOf(t *testing.T, e string) int {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	for i, x := range r.events {
		if strings.HasPrefix(x, e) {
			return i
		}
	}
	t.Fatalf("event %q missing: %v", e, r.events)
	return -1
}

func (r *switchRig) count(e string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, x := range r.events {
		if strings.HasPrefix(x, e) {
			n++
		}
	}
	return n
}

func (r *switchRig) switchNow(t *testing.T) hubAddrChange {
	t.Helper()
	if err := r.e.RunStep(context.Background(), r.id, hubStepSwitch); err != nil {
		t.Fatal(err)
	}
	return r.change(t, r.id)
}

func TestSwitchOrder(t *testing.T) {
	r := newSwitchRig(t)
	c := r.switchNow(t)
	if c.State != hubAddrSwitching || c.Steps[hubStepSwitch].Status != hubStepOK || c.ConfirmDeadline == nil {
		t.Fatalf("state %s step %+v deadline %v", c.State, c.Steps[hubStepSwitch], c.ConfirmDeadline)
	}
	order := []string{"hub:systemd-run", "hub:ip route replace", "hub:TUNNELS_RESTARTED", "set:main_server_host=" + tNew,
		"set:config_version", "set:" + hubAddrTransitionKey, "hub:COUNTER_OK", "job:5"}
	last := -1
	for _, e := range order {
		i := r.indexOf(t, e)
		if i <= last {
			t.Fatalf("%q out of order: %v", e, r.events)
		}
		last = i
	}
	if r.count("job:2") != 0 {
		t.Fatal("exits are not reprovisioned at the switch")
	}
	if r.count("hub:NETPLAN_OK") != 0 {
		t.Fatal("netplan is written at confirmation, not at the switch")
	}
	if r.hub.src != tNew {
		t.Fatalf("route source %s", r.hub.src)
	}
	if _, ok := r.hub.put[hubAddrStateDir+"/1-revert.sh"]; !ok {
		t.Fatal("the revert script is installed before the change")
	}
}

func TestSwitchRearmsWhileJobsRun(t *testing.T) {
	r := newSwitchRig(t)
	r.e.env.ListNodes = func(context.Context) ([]hubAddrNode, error) {
		var ns []hubAddrNode
		for _, id := range []int{5, 6, 7} {
			ns = append(ns, hubAddrNode{ID: id, Role: "relay", Name: "Вход " + strconv.Itoa(id), IP: "203.0.113." + strconv.Itoa(50+id), Online: true, SSHPort: 22, HostKeyConfirmed: true})
		}
		return ns, nil
	}
	r.hold[5], r.hold[6], r.hold[7] = 200, 200, 200 // 3 × 200 polls × 3 s = 30 min of entry jobs
	r.switchNow(t)
	arms := r.count("hub:systemd-run")
	if arms < 10 {
		t.Fatalf("re-armed %d times while the entry job ran", arms)
	}
	r.hub.mu.Lock()
	defer r.hub.mu.Unlock()
	var maxEnd time.Time
	for _, a := range r.hub.armedUntil {
		if a.After(maxEnd) {
			maxEnd = a
		}
	}
	armedAt := r.hub.armedUntil[0].Add(-600 * time.Second)
	if maxEnd.After(armedAt.Add(40*time.Minute + 600*time.Second)) {
		t.Fatalf("re-arming went past 40 minutes (+ the final 10 for the owner): armed %v, last end %v", armedAt, maxEnd)
	}
}

func TestConfirmRequiresGreenOrForce(t *testing.T) {
	r := newSwitchRig(t)
	r.e.env.Bamboo = func(int, string) error { return errBambooTest }
	c := r.switchNow(t)
	if c.State != hubAddrSwitching || c.Steps[hubStepSwitch].Status != hubStepFailed {
		t.Fatalf("a red check leaves the change switching with the step failed: %s %+v", c.State, c.Steps[hubStepSwitch])
	}
	if err := r.e.Confirm(context.Background(), r.id, false); err == nil {
		t.Fatal("confirm without force on red checks must be refused")
	}
	if err := r.e.Confirm(context.Background(), r.id, true); err != nil {
		t.Fatalf("forced confirm: %v", err)
	}
	if c := r.change(t, r.id); c.State != hubAddrTransition {
		t.Fatalf("state %s", c.State)
	}
}

var errBambooTest = errString("Бамбук не ответил")

type errString string

func (e errString) Error() string { return string(e) }

func TestConfirmDisarmsAndPersistsNetplan(t *testing.T) {
	r := newSwitchRig(t)
	r.switchNow(t)
	if err := r.e.Confirm(context.Background(), r.id, false); err != nil {
		t.Fatal(err)
	}
	c := r.change(t, r.id)
	if c.State != hubAddrTransition || c.ConfirmedAt == nil {
		t.Fatalf("state %s", c.State)
	}
	if r.hub.timerArmed {
		t.Fatal("the timer is disarmed")
	}
	if !strings.Contains(r.hub.files[tNP], "from: "+tNew) {
		t.Fatalf("netplan from: written:\n%s", r.hub.files[tNP])
	}
	if r.set[hubAddrTransitionKey] == "" {
		t.Fatal("the transition stays on")
	}
}

func TestWatcherRevertsOnMarker(t *testing.T) {
	r := newSwitchRig(t)
	r.switchNow(t)
	r.hub.revertNow(r.testWizard) // the timer fired: route and setting back, marker touched
	r.e.watchOnce(context.Background())
	c := r.change(t, r.id)
	if c.State != hubAddrReverted || !strings.Contains(c.RevertReason, "таймер") {
		t.Fatalf("state %s reason %q", c.State, c.RevertReason)
	}
	if r.count("job:5") != 2 {
		t.Fatalf("the entry is re-pointed back: %v", r.events)
	}
	if r.set[hubAddrTransitionKey] != "" {
		t.Fatal("the transition is cleared")
	}
}

func TestWatcherRevertsOnSourceMismatchAfterReboot(t *testing.T) {
	r := newSwitchRig(t)
	r.switchNow(t)
	r.hub.mu.Lock()
	r.hub.src, r.hub.timerArmed = tCur, false // a reboot: netplan's source, transient timer gone
	r.hub.mu.Unlock()
	r.e.watchOnce(context.Background())
	c := r.change(t, r.id)
	if c.State != hubAddrReverted {
		t.Fatalf("state %s", c.State)
	}
	if r.set["main_server_host"] != tCur {
		t.Fatalf("main_server_host back: %q", r.set["main_server_host"])
	}
}

func TestRevertRetriesFailedEntries(t *testing.T) {
	r := newSwitchRig(t)
	alerts := 0
	r.e.env.Alert = func(string, string) { alerts++ }
	r.switchNow(t)
	r.runner.status[5] = provision.JobFailed
	if err := r.e.Revert(context.Background(), r.id, "владелец"); err != nil {
		t.Fatal(err)
	}
	if c := r.change(t, r.id); c.State != hubAddrReverting {
		t.Fatalf("an entry failed to re-point: state %s", c.State)
	}
	r.e.watchOnce(context.Background()) // too early: no retry
	jobs := r.count("job:5")
	r.mu.Lock()
	r.clock = r.clock.Add(6 * time.Minute)
	r.mu.Unlock()
	r.runner.status[5] = provision.JobSucceeded
	r.e.watchOnce(context.Background())
	if r.count("job:5") != jobs+1 {
		t.Fatalf("one retry after 5 minutes: %v", r.events)
	}
	if c := r.change(t, r.id); c.State != hubAddrReverted {
		t.Fatalf("state %s", c.State)
	}
	if alerts != 1 {
		t.Fatalf("alerts %d, want 1", alerts)
	}
}

func TestRevertClearsTransition(t *testing.T) {
	r := newSwitchRig(t)
	r.switchNow(t)
	r.e.Confirm(context.Background(), r.id, false)
	if err := r.e.Revert(context.Background(), r.id, "новый адрес заблокировали"); err != nil {
		t.Fatal(err)
	}
	c := r.change(t, r.id)
	if c.State != hubAddrReverted || r.set[hubAddrTransitionKey] != "" || r.set["main_server_host"] != tCur || r.hub.src != tCur {
		t.Fatalf("state %s transition %q main %q src %s", c.State, r.set[hubAddrTransitionKey], r.set["main_server_host"], r.hub.src)
	}
	if !strings.Contains(r.hub.files[tNP], "from: "+tCur) {
		t.Fatalf("netplan from: back to the old source:\n%s", r.hub.files[tNP])
	}
}
