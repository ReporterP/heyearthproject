package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/heyearth/backend/internal/admin"
)

func TestStartRefusesSecondChange(t *testing.T) {
	w := newTestWizard(t)
	ctx := context.Background()
	if _, err := w.e.Start(ctx, tNew, "owner"); err != nil {
		t.Fatal(err)
	}
	if _, err := w.e.Start(ctx, "198.51.100.21", "owner"); !errors.Is(err, errHubAddrActive) {
		t.Fatalf("second change: want errHubAddrActive, got %v", err)
	}
}

func TestStartValidatesTheAddress(t *testing.T) {
	w := newTestWizard(t)
	for _, bad := range []string{tRelay, tCur, tGWCar, "10.1.2.3"} {
		if _, err := w.e.Start(context.Background(), bad, "owner"); err == nil {
			t.Errorf("Start(%s): want a refusal", bad)
		}
	}
}

func TestRunStepRefusedWhileRunning(t *testing.T) {
	w := newTestWizard(t)
	var queued []func()
	w.e.env.Go = func(f func()) { queued = append(queued, f) }
	id, err := w.e.Start(context.Background(), tNew, "owner")
	if err != nil {
		t.Fatal(err)
	}
	if err := w.e.RunStep(context.Background(), id, hubStepAdd); !errors.Is(err, errHubAddrBusy) {
		t.Fatalf("a step while another runs: want errHubAddrBusy, got %v", err)
	}
	for _, f := range queued {
		f()
	}
	if err := w.e.RunStep(context.Background(), id, hubStepAdd); err != nil {
		t.Fatalf("after it finished: %v", err)
	}
	if err := w.e.RunStep(context.Background(), id, hubStepSwitch); err == nil {
		t.Fatal("switch from «added» must be refused")
	}
}

func TestAddStepAppliesAndVerifies(t *testing.T) {
	w := newTestWizard(t)
	id, err := w.e.Start(context.Background(), tNew, "owner")
	if err != nil {
		t.Fatal(err)
	}
	c := w.change(t, id)
	if c.State != hubAddrAdded || c.Steps[hubStepAdd].Status != hubStepOK {
		t.Fatalf("state %s, step %+v", c.State, c.Steps[hubStepAdd])
	}
	if !w.hub.ran("ip addr add 198.51.100.20/32 dev ens3") || !w.hub.hasAddr(tNew) {
		t.Fatal("the address is added at runtime")
	}
	if !strings.Contains(w.hub.files[tNP], "        - 198.51.100.20/32\n") || !w.hub.ran(tNP+".bak-hubaddr-1") {
		t.Fatalf("netplan persisted with a backup:\n%s", w.hub.files[tNP])
	}
	if w.hub.ran("netplan apply") {
		t.Fatal("never netplan apply")
	}
	if w.reset != 1 {
		t.Fatalf("hubIPs reset %d times, want 1", w.reset)
	}
	var ch hubAddrAddChecks
	checksOf(t, c.Steps[hubStepAdd], &ch)
	if !ch.AddedRuntime || ch.NetplanPath != tNP || !ch.NetplanChanged {
		t.Fatalf("checks %+v", ch)
	}
	// a re-run changes nothing and keeps «we added it» (cancel relies on it)
	if err := w.e.RunStep(context.Background(), id, hubStepAdd); err != nil {
		t.Fatal(err)
	}
	checksOf(t, w.change(t, id).Steps[hubStepAdd], &ch)
	if !ch.AddedRuntime {
		t.Fatal("a re-run forgot that the wizard added the address")
	}
}

func TestAddStepRollbackOnVerifyFail(t *testing.T) {
	w := newTestWizard(t)
	w.hub.hideAdd = true
	id, err := w.e.Start(context.Background(), tNew, "owner")
	if err != nil {
		t.Fatal(err)
	}
	c := w.change(t, id)
	if c.State != hubAddrDraft || c.Steps[hubStepAdd].Status != hubStepFailed {
		t.Fatalf("state %s, step %+v", c.State, c.Steps[hubStepAdd])
	}
	if strings.Contains(w.hub.files[tNP], tNew) {
		t.Fatal("netplan restored after a failed verify")
	}
	if !w.hub.ran("ip addr del") {
		t.Fatal("the runtime address is removed again")
	}
}

func TestCheckGate(t *testing.T) {
	ctx := context.Background()

	w := newTestWizard(t)
	w.net = newFakeNet(false)
	w.e.env.Bound = w.net.bound
	id, _ := w.e.Start(ctx, tNew, "owner")
	if err := w.e.RunStep(ctx, id, hubStepCheck); err != nil {
		t.Fatal(err)
	}
	c := w.change(t, id)
	if c.State != hubAddrAdded || c.Steps[hubStepCheck].Status != hubStepFailed {
		t.Fatalf("outbound broken: state %s, step %+v", c.State, c.Steps[hubStepCheck])
	}
	var ch hubAddrCheckResult
	checksOf(t, c.Steps[hubStepCheck], &ch)
	if len(ch.Outbound) != len(allTargets()) || ch.Outbound[0].Verdict != "fail" || len(ch.Blockers) == 0 {
		t.Fatalf("checks %+v", ch)
	}

	w = newTestWizard(t)
	id, _ = w.e.Start(ctx, tNew, "owner")
	w.e.RunStep(ctx, id, hubStepCheck)
	if c := w.change(t, id); c.State != hubAddrAdded {
		t.Fatalf("without the phone check the gate stays closed: %s", c.State)
	}
	if err := w.e.SkipPhone(ctx, id); err != nil {
		t.Fatal(err)
	}
	w.e.RunStep(ctx, id, hubStepCheck)
	c = w.change(t, id)
	if c.State != hubAddrChecked || !c.PhoneSkipped {
		t.Fatalf("all green + phone skipped: state %s skipped=%v step %+v", c.State, c.PhoneSkipped, c.Steps[hubStepCheck])
	}
	if _, ok := w.hub.put["/var/www/heyearth-site/hub-probe.bin"]; !ok || len(w.hub.put["/var/www/heyearth-site/hub-probe.bin"]) != 262144 {
		t.Fatal("the 256 KiB probe object is written to the site root")
	}
	if len(w.relay.got) == 0 || !strings.Contains(w.relay.got[0], "--resolve hiearth.ru:443:198.51.100.20") {
		t.Fatalf("the RU check runs on the entry: %v", w.relay.got)
	}

	// a phone that got through on the new address opens the gate without a skip
	w = newTestWizard(t)
	id, _ = w.e.Start(ctx, tNew, "owner")
	if _, _, err := w.e.StartPhoneCheck(ctx, id, "new"); err != nil {
		t.Fatal(err)
	}
	w.phone = []admin.MobileCheckResult{{IP: "198.51.100.77", OK: true}}
	if _, err := w.e.PhoneCheck(ctx, id); err != nil {
		t.Fatal(err)
	}
	w.e.RunStep(ctx, id, hubStepCheck)
	if c := w.change(t, id); c.State != hubAddrChecked || c.PhoneSkipped {
		t.Fatalf("phone ok on the new address: state %s", c.State)
	}
}

func TestHubIPCacheReset(t *testing.T) {
	var c hubIPCache
	calls := 0
	compute := func() []string { calls++; return []string{tCur} }
	c.get(compute, func(string) {})
	c.get(compute, func(string) {})
	c.reset()
	c.get(compute, func(string) {})
	if calls != 2 {
		t.Fatalf("compute ran %d times, want 2 (cached, then recomputed after reset)", calls)
	}
}

func TestServiceVerdict(t *testing.T) {
	if serviceVerdict(nil) != "ok" || serviceVerdict(errHubCheckSkipped) != "skipped" || serviceVerdict(errors.New("535 auth failed")) != "535 auth failed" {
		t.Fatal("ok / skipped / the error text")
	}
}
