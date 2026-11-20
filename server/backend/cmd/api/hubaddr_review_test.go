package main

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// Regressions from the branch review (2026-11-20).

// Review I1: the revert must not die with the admin's HTTP request.
func TestRevertSurvivesCancelledRequest(t *testing.T) {
	r := newSwitchRig(t)
	r.switchNow(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // the browser gave up
	if err := r.e.Revert(ctx, r.id, "владелец"); err != nil {
		t.Fatalf("revert on a cancelled request: %v", err)
	}
	c := r.change(t, r.id)
	if c.State != hubAddrReverted || r.hub.src != tCur || r.set["main_server_host"] != tCur {
		t.Fatalf("state %s src %s main %q", c.State, r.hub.src, r.set["main_server_host"])
	}
}

// Review I1: in «reverting» the watcher makes sure of the Hub side too, not only the entries.
func TestWatcherRevertingRestoresHubSide(t *testing.T) {
	r := newSwitchRig(t)
	r.switchNow(t)
	r.runner.status[5] = "failed"
	r.e.Revert(context.Background(), r.id, "владелец")
	r.set["main_server_host"] = tNew // something put it back meanwhile
	r.after(6 * time.Minute)
	r.runner.status[5] = "succeeded"
	r.e.watchOnce(context.Background())
	if r.set["main_server_host"] != tCur || r.change(t, r.id).State != hubAddrReverted {
		t.Fatalf("main %q state %s", r.set["main_server_host"], r.change(t, r.id).State)
	}
}

// Review I2: a netplan the wizard could not pin the source in is refused at step 1, not at confirm.
func TestAddRefusesNetplanWithoutDefaultRoute(t *testing.T) {
	w := newTestWizard(t)
	w.hub.files[tNP] = strings.Replace(tNetplan, "      routes:\n        - to: default\n          via: 203.0.113.1\n          from: 192.0.2.10\n", "      gateway4: 203.0.113.1\n", 1)
	before := w.hub.files[tNP]
	id, err := w.e.Start(context.Background(), tNew, "owner")
	if err != nil {
		t.Fatal(err)
	}
	c := w.change(t, id)
	if c.State != hubAddrDraft || c.Steps[hubStepAdd].Status != hubStepFailed || !strings.Contains(c.Steps[hubStepAdd].Log, "маршрут") {
		t.Fatalf("state %s step %+v", c.State, c.Steps[hubStepAdd])
	}
	if w.hub.hasAddr(tNew) || w.hub.files[tNP] != before {
		t.Fatal("nothing may change before the refusal")
	}
}

// Review I2: confirm writes netplan before it disarms; a failed write keeps the timer.
func TestConfirmKeepsTimerWhenNetplanFails(t *testing.T) {
	r := newSwitchRig(t)
	r.switchNow(t)
	r.hub.failWrite = true
	if err := r.e.Confirm(context.Background(), r.id, false); err == nil {
		t.Fatal("confirm with a failing netplan write must fail")
	}
	if !r.hub.timerArmed || r.change(t, r.id).State != hubAddrSwitching {
		t.Fatalf("the timer stays armed (%v), the change stays switching (%s)", r.hub.timerArmed, r.change(t, r.id).State)
	}
}

// Review I3: a netplan cloud-init rewrites at boot is refused unless its network config is off.
func TestAddRefusesCloudInitNetplan(t *testing.T) {
	w := newTestWizard(t)
	w.hub.files = map[string]string{"/etc/netplan/50-cloud-init.yaml": tNetplan}
	id, _ := w.e.Start(context.Background(), tNew, "owner")
	if c := w.change(t, id); c.State != hubAddrDraft || !strings.Contains(c.Steps[hubStepAdd].Log, "cloud-init") {
		t.Fatalf("state %s log %q", c.State, c.Steps[hubStepAdd].Log)
	}
	if w.hub.hasAddr(tNew) {
		t.Fatal("nothing may change before the refusal")
	}
	w = newTestWizard(t)
	w.hub.files = map[string]string{"/etc/netplan/50-cloud-init.yaml": tNetplan}
	w.hub.cloudInitOff = true
	id, _ = w.e.Start(context.Background(), tNew, "owner")
	if c := w.change(t, id); c.State != hubAddrAdded {
		t.Fatalf("cloud-init network config disabled: state %s\n%s", c.State, c.Steps[hubStepAdd].Log)
	}
}

// Review I4: once the revert ran (marker), re-arming stops the step instead of arming again.
func TestRearmStopsWhenTheRevertRan(t *testing.T) {
	r := newSwitchRig(t)
	r.hold[5] = 200
	prev := r.hub.onScript
	arms := 0
	r.hub.onScript = func(s string) {
		if strings.Contains(s, "systemd-run") {
			arms++
			if arms == 2 { // the timer fired and the revert finished just before this re-arm
				r.hub.mu.Lock()
				r.hub.marker, r.hub.src, r.hub.timerArmed = true, tCur, false // it fired, so it is not armed
				r.hub.mu.Unlock()
			}
		}
		prev(s)
	}
	c := r.switchNow(t)
	if !errorsIsFired(c.Steps[hubStepSwitch].Log) {
		t.Fatalf("the step stops with «таймер отката сработал»:\n%s", c.Steps[hubStepSwitch].Log)
	}
	if r.hub.timerArmed {
		t.Fatal("no timer may be armed on a Hub that already reverted")
	}
}

func errorsIsFired(log string) bool { return strings.Contains(log, errHubTimerFired.Error()) }

// Review I5: the timer covers the final probes — no gap between one arm's end and the next arm.
func TestTimerCoversFinalProbes(t *testing.T) {
	r := newSwitchRig(t)
	r.hold[5] = 238 // the entry job ends ~12 min in, just before a re-arm
	r.e.env.ExitTunnels = func(context.Context) ([]hubAddrProbe, error) {
		return []hubAddrProbe{{Name: "Выход 2", OK: false, Error: "unhealthy"}}, nil
	}
	r.e.env.Bamboo = func(int, string) error { r.after(150 * time.Second); return nil }
	r.switchNow(t)
	log := r.hub.armLog
	for i := 1; i < len(log); i++ {
		if log[i].at.After(log[i-1].end) {
			t.Fatalf("arm %d at %v after the previous end %v: the timer would have fired", i, log[i].at, log[i-1].end)
		}
	}
}

// Review (minor → important): confirm is refused while a revert is underway.
func TestConfirmRefusedWhileReverting(t *testing.T) {
	r := newSwitchRig(t)
	r.switchNow(t)
	r.e.mu.Lock()
	r.e.reverting[r.id] = true
	r.e.mu.Unlock()
	if err := r.e.Confirm(context.Background(), r.id, false); !errors.Is(err, errHubAddrBusy) {
		t.Fatalf("owner's revert underway: want errHubAddrBusy, got %v", err)
	}
	r.e.mu.Lock()
	delete(r.e.reverting, r.id)
	r.e.mu.Unlock()
	r.hub.revertActive = true
	if err := r.e.Confirm(context.Background(), r.id, false); !errors.Is(err, errHubTimerFired) {
		t.Fatalf("the timer's revert is running: want errHubTimerFired, got %v", err)
	}
}

// Review (minor → important): after a reboot the old-address counter table is recreated.
func TestCounterRecreatedAfterReboot(t *testing.T) {
	r := transitionRig(t)
	r.hub.counterMissing = true
	r.e.watchOnce(context.Background())
	if r.hub.counterMissing {
		t.Fatal("the watcher recreates the counter table")
	}
	r.after(10 * time.Minute)
	r.sample(5)
	rec := r.e.transitionRecord(r.change(t, r.id))
	if len(rec.Samples) == 0 || rec.Error != "" {
		t.Fatalf("samples resume: %+v", rec)
	}
}
