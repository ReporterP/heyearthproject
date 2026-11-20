package main

import (
	"context"
	"strings"
	"testing"
	"time"
)

// transitionRig is a confirmed switch: the change is in «transition».
func transitionRig(t *testing.T) *switchRig {
	t.Helper()
	r := newSwitchRig(t)
	r.switchNow(t)
	if err := r.e.Confirm(context.Background(), r.id, false); err != nil {
		t.Fatal(err)
	}
	r.progress(95, 100)
	return r
}

func (r *switchRig) progress(n, m int) {
	r.e.env.Progress = func(context.Context, time.Time) (int, int, error) { return n, m, nil }
}

func (r *switchRig) after(d time.Duration) {
	r.mu.Lock()
	r.clock = r.clock.Add(d)
	r.mu.Unlock()
}

// sample lets the watcher read the old-address counter once at the current time.
func (r *switchRig) sample(packets uint64) {
	r.hub.mu.Lock()
	r.hub.counter = packets
	r.hub.mu.Unlock()
	r.e.watchOnce(context.Background())
}

func TestFinishGate(t *testing.T) {
	r := transitionRig(t)
	ctx := context.Background()
	r.sample(1000)
	r.after(30 * time.Hour)
	r.sample(1100)
	v, err := r.e.Transition(ctx, r.id)
	if err != nil {
		t.Fatal(err)
	}
	if v.Ready || len(v.Blockers) == 0 || !strings.Contains(strings.Join(v.Blockers, "|"), "72") {
		t.Fatalf("30 h in: not ready, blocker names the 72 h: %+v", v)
	}
	if _, err := r.e.Finish(ctx, r.id, false, "owner"); err == nil {
		t.Fatal("finish before the gate must be refused without override")
	}

	r.after(43 * time.Hour)
	r.sample(1100)
	r.after(time.Hour)
	r.sample(1130) // 30 in an hour: 0.5 per minute
	r.progress(80, 100)
	v, _ = r.e.Transition(ctx, r.id)
	if v.Ready || !strings.Contains(strings.Join(v.Blockers, "|"), "80 из 100") {
		t.Fatalf("80 %% refreshed: not ready: %+v", v)
	}
	r.progress(95, 100)
	v, _ = r.e.Transition(ctx, r.id)
	if !v.Ready || v.N != 95 || v.M != 100 || v.OldPerMin >= 1 {
		t.Fatalf("74 h, 95 %%, 0.5/min: ready: %+v", v)
	}

	r.after(time.Hour)
	r.sample(1130 + 600) // 10 per minute: someone still dials the old address
	v, _ = r.e.Transition(ctx, r.id)
	if v.Ready || !strings.Contains(strings.Join(v.Blockers, "|"), "в минуту") {
		t.Fatalf("10/min on the old address: not ready: %+v", v)
	}
	notes, err := r.e.Finish(ctx, r.id, true, "owner")
	if err != nil {
		t.Fatalf("override: %v", err)
	}
	c := r.change(t, r.id)
	if c.State != hubAddrFinished || r.set[hubAddrTransitionKey] != "" {
		t.Fatalf("state %s transition %q", c.State, r.set[hubAddrTransitionKey])
	}
	var fin hubAddrFinishChecks
	checksOf(t, c.Steps[hubStepFinish], &fin)
	if !fin.Override || len(fin.Blockers) == 0 {
		t.Fatalf("the override and what it overrode are recorded: %+v", fin)
	}
	if !strings.Contains(strings.Join(notes, "|"), "DEPLOY_HOST") {
		t.Fatalf("leftovers name DEPLOY_HOST: %v", notes)
	}
}

func TestFinishRemovesExtraAddress(t *testing.T) {
	r := transitionRig(t)
	ctx := context.Background()
	if _, err := r.e.Finish(ctx, r.id, true, "owner"); err != nil {
		t.Fatal(err)
	}
	if r.hub.hasAddr(tCur) {
		t.Fatal("the old /32 is removed from the interface")
	}
	if strings.Contains(r.hub.files[tNP], "- "+tCur+"/32") {
		t.Fatalf("and from netplan:\n%s", r.hub.files[tNP])
	}
	if r.hub.timerArmed {
		t.Fatal("the removal's timer is disarmed once the Hub still works")
	}
	if r.reset < 2 {
		t.Fatalf("hubIPs reset after the removal (%d)", r.reset)
	}
}

func TestFinishKeepsGatewayCarrier(t *testing.T) {
	r := transitionRig(t)
	r.hub.mu.Lock()
	r.hub.gw = "192.0.2.1"
	for i, a := range r.hub.addrs {
		if strings.HasPrefix(a, tCur+"/") {
			r.hub.addrs[i] = tCur + "/24"
		}
	}
	r.hub.mu.Unlock()
	notes, err := r.e.Finish(context.Background(), r.id, true, "owner")
	if err != nil {
		t.Fatal(err)
	}
	if !r.hub.hasAddr(tCur) || r.hub.ran("ip addr del") {
		t.Fatal("the gateway carrier stays on the interface")
	}
	if !strings.Contains(strings.Join(notes, "|"), "шлюз") {
		t.Fatalf("the owner is told why: %v", notes)
	}
}

func TestCancelRemovesOnlyWhatWeAdded(t *testing.T) {
	ctx := context.Background()
	w := newTestWizard(t)
	id, _ := w.e.Start(ctx, tNew, "owner")
	notes, err := w.e.Cancel(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if w.hub.hasAddr(tNew) || strings.Contains(w.hub.files[tNP], tNew) || w.change(t, id).State != hubAddrCancelled {
		t.Fatalf("the wizard's address and netplan line are gone, state %s", w.change(t, id).State)
	}
	if strings.Contains(strings.Join(notes, "|"), "DNS") {
		t.Fatalf("DNS was never touched: %v", notes)
	}

	w = newTestWizard(t)
	w.hub.addrs = append(w.hub.addrs, tNew+"/32") // VDSina bound it already
	id, _ = w.e.Start(ctx, tNew, "owner")
	w.e.Cancel(ctx, id)
	if !w.hub.hasAddr(tNew) {
		t.Fatal("an address the wizard did not add is left alone")
	}
}

func TestCancelRefusedWhileSwitching(t *testing.T) {
	r := newSwitchRig(t)
	r.switchNow(t)
	if _, err := r.e.Cancel(context.Background(), r.id); err == nil {
		t.Fatal("cancel in «switching» must be refused (revert is the way back)")
	}
}
