package main

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/heyearth/backend/internal/biz"
)

var errBlocked = errors.New("read tcp: i/o timeout")

const (
	tHub = "198.51.100.1"
	tR5  = "203.0.113.50"
	tR7  = "203.0.113.70"
)

type entryEnv struct {
	now        time.Time
	entries    []biz.Entry
	fo         biz.EntryFailover
	saveErr    error
	settings   map[string]string
	exits      []int
	exitsErr   error
	controlErr error
	controlSeq []error // consumed first, one per control call
	tcp, udp   map[string]error
	events     []string
	bumps      int
	m          *entryMonitor
}

func newEntryEnv() *entryEnv {
	e := &entryEnv{now: time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC), settings: map[string]string{},
		exits: []int{1090}, tcp: map[string]error{}, udp: map[string]error{},
		entries: []biz.Entry{
			{ID: 0, Name: "Hub", Kind: biz.EntryKindHub, Address: tHub, Lifecycle: "online"},
			{ID: 5, Name: "Резерв", Kind: biz.EntryKindRelay, Address: tR5, Lifecycle: "online",
				Health: biz.EntryHealth{External: biz.EntryHealthy, UDP: biz.EntryHealthy}},
		}}
	e.m = &entryMonitor{
		now:          func() time.Time { return e.now },
		list:         func(context.Context) ([]biz.Entry, error) { return e.entries, nil },
		readFailover: func(context.Context) (biz.EntryFailover, error) { return e.fo, nil },
		saveFailover: func(_ context.Context, st biz.EntryFailover) error {
			if e.saveErr != nil {
				return e.saveErr
			}
			e.fo = st
			return nil
		},
		saveCheck: func(context.Context, int, string, biz.EntryCheck) error { return nil },
		bump:      func(context.Context) error { e.bumps++; return nil },
		setting:   func(k string) (string, error) { return e.settings[k], nil },
		exits:     func() ([]int, error) { return e.exits, e.exitsErr },
		control: func(int) error {
			if len(e.controlSeq) > 0 {
				err := e.controlSeq[0]
				e.controlSeq = e.controlSeq[1:]
				return err
			}
			return e.controlErr
		},
		tlsCheck:   func(_ int, host string, _ int, _ string) error { return e.tcp[host] },
		udpCheck:   func(_ int, host string, _ []byte) error { return e.udp[host] },
		udpControl: func(int) error { return nil },
		raise:      func(k string, l AlertLevel, _, _ string) { e.events = append(e.events, "raise "+k+" "+string(l)) },
		resolve:    func(k, _ string) { e.events = append(e.events, "resolve "+k) },
		logf:       func(string, ...any) {},
	}
	return e
}

func (e *entryEnv) run()               { e.now = e.now.Add(time.Minute); e.m.run() }
func (e *entryEnv) step(hubErr error)  { e.tcp[tHub] = hubErr; e.run() }
func (e *entryEnv) has(ev string) bool { return slices.Contains(e.events, ev) }

func TestFailoverActivatesOnThreeRealFailures(t *testing.T) {
	e := newEntryEnv()
	e.step(errBlocked)
	e.step(errBlocked)
	if e.fo.Active {
		t.Fatal("2 failures switched")
	}
	e.step(errBlocked)
	if !e.fo.Active || e.fo.Target != 5 || !e.fo.Since.Equal(e.now) || !e.has("raise entry-failover-5 critical") || e.bumps != 1 {
		t.Fatalf("fo %+v, events %v, bumps %d", e.fo, e.events, e.bumps)
	}
}

// Review Focus 4: a flapping probe never switches.
func TestFailoverFlappingProbeNeverSwaps(t *testing.T) {
	e := newEntryEnv()
	for i := 0; i < 20; i++ {
		e.step(errBlocked)
		e.step(errBlocked)
		e.step(nil)
	}
	if e.fo.Active || e.has("raise entry-failover-5 critical") {
		t.Fatalf("flapping switched: %+v %v", e.fo, e.events)
	}
}

// Review Focus 4: the probe's own failure — no exit, unreadable exit list, the exit's control
// failing, the exit dying in the middle of a check, a broken SOCKS — is not a block: not counted,
// and a short real streak survives it.
func TestFailoverProbeToolFailureIsNotABlock(t *testing.T) {
	tool := fmt.Errorf("%w: bad port", errEntryProbeTool)
	breakers := map[string]func(e *entryEnv){
		"нет выхода":                  func(e *entryEnv) { e.exits = nil },
		"список выходов не прочитан":  func(e *entryEnv) { e.exitsErr = errors.New("db down") },
		"контроль выхода не проходит": func(e *entryEnv) { e.controlErr = errors.New("socks: connection refused") },
		"выход умер посреди проверки": func(e *entryEnv) { e.controlSeq = []error{nil, errors.New("refused"), errors.New("refused")} },
		"SOCKS не создаётся":          func(e *entryEnv) { e.tcp[tHub], e.tcp[tR5] = tool, tool },
	}
	for name, breakProbe := range breakers {
		t.Run(name, func(t *testing.T) {
			e := newEntryEnv()
			e.step(errBlocked)
			e.step(errBlocked)
			e.events = nil
			for i := 0; i < 5; i++ {
				e.tcp[tHub], e.tcp[tR5] = errBlocked, errBlocked // both really fail…
				breakProbe(e)                                    // …but the probe cannot prove it
				e.run()
			}
			if e.fo.Active || len(e.events) > 0 || e.m.tcp[5].status != biz.EntryHealthy {
				t.Fatalf("tool failure acted: fo %+v, events %v, reserve %s", e.fo, e.events, e.m.tcp[5].status)
			}
			e.exits, e.exitsErr, e.controlErr, e.controlSeq, e.tcp[tR5] = []int{1090}, nil, nil, nil, nil
			e.step(errBlocked) // 2 old + 1 new within 10 min = 3 in a row
			if !e.fo.Active {
				t.Fatal("skips broke a real streak shorter than the stale window")
			}
		})
	}
	// Blind for an hour: the streak starts over and the blind alert is raised, then resolved.
	e := newEntryEnv()
	e.step(errBlocked)
	e.step(errBlocked)
	e.exits = nil
	for i := 0; i < 61; i++ {
		e.run()
	}
	e.exits = []int{1090}
	e.step(errBlocked)
	if e.fo.Active || !e.has("raise entries-probe-blind warn") || !e.has("resolve entries-probe-blind") {
		t.Fatalf("blind hour: fo %+v, events %v", e.fo, e.events)
	}
}

func TestFailoverNeedsHealthyReserve(t *testing.T) {
	e := newEntryEnv()
	e.entries[1].Health.External = biz.EntryUnhealthy // persisted state seeds the track
	e.tcp[tR5] = errBlocked
	for i := 0; i < 5; i++ {
		e.step(errBlocked)
	}
	if e.fo.Active || !e.has("raise entry-failover-no-reserve warn") {
		t.Fatalf("fo %+v, events %v", e.fo, e.events)
	}
	none := newEntryEnv() // no relays at all: no "no reserve" noise (the loop probe alerts already)
	none.entries = none.entries[:1]
	for i := 0; i < 5; i++ {
		none.step(errBlocked)
	}
	if none.fo.Active || len(none.events) != 0 {
		t.Fatalf("no relays: %+v %v", none.fo, none.events)
	}
}

func TestFailoverReturnNeedsThirtyStableMinutes(t *testing.T) {
	e := newEntryEnv()
	for i := 0; i < 3; i++ {
		e.step(errBlocked)
	}
	for i := 0; i < 29; i++ {
		if e.step(nil); !e.fo.Active {
			t.Fatalf("returned after %d stable minutes", i+1)
		}
	}
	e.step(errBlocked) // one failure restarts the 30 minutes
	for i := 0; i < 30; i++ {
		if e.step(nil); !e.fo.Active {
			t.Fatalf("returned %d min after the last failure", i+1)
		}
	}
	e.step(nil)
	if e.fo.Active || !e.has("resolve entry-failover-5") {
		t.Fatalf("30 stable minutes must return: %+v %v", e.fo, e.events)
	}
}

// A pinned failover (the owner sent everyone through 5) survives everything the monitor sees:
// a healthy Hub for an hour, the auto-swap switched off, a dead target.
func TestFailoverPinnedStays(t *testing.T) {
	e := newEntryEnv()
	pin := biz.EntryFailover{Active: true, Target: 5, Since: e.now, Pinned: true}
	e.fo = pin
	e.settings["entry_failover_enabled"] = "false"
	for i := 0; i < 60; i++ {
		e.step(nil)
	}
	e.tcp[tR5] = errBlocked
	for i := 0; i < 5; i++ {
		e.step(nil)
	}
	if e.fo != pin { // bumps are fine: a dead target changes the lines (back to the Hub)
		t.Fatalf("pinned failover changed: %+v", e.fo)
	}
	for _, ev := range e.events {
		if strings.HasPrefix(ev, "raise entry-failover") || ev == "resolve entry-failover-5" {
			t.Errorf("failover event on a pinned state: %s", ev)
		}
	}
}

func TestFailoverRetargetsAndAbandons(t *testing.T) {
	e := newEntryEnv()
	e.entries = append(e.entries, biz.Entry{ID: 7, Name: "Резерв 2", Kind: biz.EntryKindRelay, Address: tR7, Lifecycle: "online",
		Health: biz.EntryHealth{External: biz.EntryHealthy}})
	for i := 0; i < 3; i++ {
		e.step(errBlocked)
	}
	since := e.fo.Since
	e.tcp[tR5] = errBlocked
	for i := 0; i < 3; i++ {
		e.step(errBlocked)
	}
	if e.fo.Target != 7 || !e.fo.Since.Equal(since) || !e.has("resolve entry-failover-5") || !e.has("raise entry-failover-7 critical") {
		t.Fatalf("retarget: %+v %v", e.fo, e.events)
	}
	e.tcp[tR7] = errBlocked
	for i := 0; i < 3; i++ {
		e.step(errBlocked)
	}
	if e.fo.Active || !e.has("resolve entry-failover-7") || !e.has("raise entry-failover-no-reserve critical") {
		t.Fatalf("abandon: %+v %v", e.fo, e.events)
	}
}

func TestFailoverSwitchOffAndUnsavedState(t *testing.T) {
	e := newEntryEnv()
	e.saveErr = errors.New("db down")
	for i := 0; i < 3; i++ {
		e.step(errBlocked)
	}
	if e.fo.Active || e.has("raise entry-failover-5 critical") || e.bumps != 0 {
		t.Fatal("alert or bump before the state was saved")
	}
	e.saveErr = nil
	if e.step(errBlocked); !e.fo.Active {
		t.Fatal("not retried a minute later")
	}
	e.settings["entry_failover_enabled"] = "false"
	if e.step(errBlocked); e.fo.Active || !e.has("resolve entry-failover-5") {
		t.Fatal("switch-off did not return the lines")
	}
	for i := 0; i < 3; i++ {
		e.step(errBlocked)
	}
	if e.fo.Active {
		t.Fatal("activated while switched off")
	}
}

func TestFailoverDrillExpires(t *testing.T) {
	e := newEntryEnv()
	e.settings["entry_probe_simulate_hub_fail_until"] = e.now.Add(3*time.Minute + 30*time.Second).Format(time.RFC3339)
	for i := 0; i < 3; i++ {
		e.step(nil) // the Hub answers, the drill says it does not
	}
	if !e.fo.Active {
		t.Fatal("the drill did not switch")
	}
	for i := 0; i < 31; i++ {
		e.step(nil)
	}
	if e.fo.Active {
		t.Fatal("the drill did not end")
	}
}

func TestEntryMonitorRestartAndReserveChanges(t *testing.T) {
	e := newEntryEnv() // restarted API during a failover: state kept, no bump for an unchanged reserve
	e.fo = biz.EntryFailover{Active: true, Target: 5, Since: e.now}
	e.step(nil)
	e.step(nil)
	if !e.fo.Active || e.bumps != 0 {
		t.Fatalf("restart: %+v bumps %d", e.fo, e.bumps)
	}
	e = newEntryEnv() // a reserve going down and up: warn alert, a bump on each change
	e.tcp[tR5] = errBlocked
	for i := 0; i < 3; i++ {
		e.step(nil)
	}
	if !e.has("raise relay-5-unreachable-external warn") || e.bumps != 1 {
		t.Fatalf("down: %v bumps %d", e.events, e.bumps)
	}
	e.tcp[tR5] = nil
	e.step(nil)
	e.step(nil)
	if !e.has("resolve relay-5-unreachable-external") || e.bumps != 2 {
		t.Fatalf("up: %v bumps %d", e.events, e.bumps)
	}
}

// The GRE tunnel of a relay down: :443 still answers (nginx falls back to the Hub's public
// address) but :2443 does not — three checks and the relay is unhealthy, out of the subscription.
// One lost :2443 flow is retried on a new connection and is no failure.
func TestEntryMonitorRelay2443(t *testing.T) {
	e := newEntryEnv()
	var tries []int
	dead := map[int]bool{2443: true}
	e.m.tlsCheck = func(_ int, host string, port int, _ string) error {
		if host == tR5 {
			tries = append(tries, port)
			if dead[port] {
				return errBlocked
			}
		}
		return e.tcp[host]
	}
	for i := 0; i < 3; i++ {
		e.step(nil)
	}
	if e.m.tcp[5].status != biz.EntryUnhealthy || !e.has("raise relay-5-unreachable-external warn") || e.bumps != 1 {
		t.Fatalf("tunnel down, 443 alive: status %s events %v bumps %d", e.m.tcp[5].status, e.events, e.bumps)
	}
	if want := []int{443, 2443, 2443}; !slices.Equal(tries[:3], want) {
		t.Fatalf("tries %v, want %v per check", tries, want)
	}
	// One unlucky flow: the retry passes, the relay stays healthy.
	e = newEntryEnv()
	n := 0
	e.m.tlsCheck = func(_ int, host string, port int, _ string) error {
		if host == tR5 && port == 2443 {
			if n++; n%2 == 1 {
				return errBlocked
			}
		}
		return e.tcp[host]
	}
	for i := 0; i < 3; i++ {
		e.step(nil)
	}
	if e.m.tcp[5].status == biz.EntryUnhealthy || e.has("raise relay-5-unreachable-external warn") {
		t.Fatalf("one lost flow per check made the relay unhealthy: %v", e.events)
	}
	// The Hub itself is checked on :443 only.
	e = newEntryEnv()
	var hubPorts []int
	e.m.tlsCheck = func(_ int, host string, port int, _ string) error {
		if host == tHub {
			hubPorts = append(hubPorts, port)
		}
		return nil
	}
	e.step(nil)
	if !slices.Equal(hubPorts, []int{443}) {
		t.Fatalf("Hub checked on %v", hubPorts)
	}
}

func TestEntryMonitorBambooThroughRelay(t *testing.T) {
	e := newEntryEnv()
	e.settings["hysteria2_obfs_password"] = "psk"
	e.udp[tR5] = fmt.Errorf("%w от %s:41000 за 5s", errBambooNoReply, tR5)
	for i := 0; i < 3; i++ {
		e.step(nil)
	}
	if !e.has("raise relay-5-bamboo warn") || e.m.udp[5].status != biz.EntryUnhealthy {
		t.Fatalf("silent Bamboo through the relay: %v", e.events)
	}
	lost := newEntryEnv() // the exit lost UDP (control fails): a skip, not a Bamboo failure
	lost.settings["hysteria2_obfs_password"] = "psk"
	lost.udp[tR5] = fmt.Errorf("%w от %s", errBambooNoReply, tR5)
	lost.m.udpControl = func(int) error { return errors.New("DNS через exit: нет ответа") }
	for i := 0; i < 3; i++ {
		lost.step(nil)
	}
	if lost.has("raise relay-5-bamboo warn") || lost.m.udp[5] != nil {
		t.Fatalf("exit without UDP counted as a Bamboo failure: %v", lost.events)
	}
}

func TestVerifyRelayPath(t *testing.T) {
	e := newEntryEnv()
	if err := e.m.verifyRelay(tR5); err != nil {
		t.Fatalf("healthy relay: %v", err)
	}
	e.exits = nil
	if err := e.m.verifyRelay(tR5); err == nil || !strings.Contains(err.Error(), "проверка невозможна") {
		t.Fatalf("no exit: %v", err)
	}
	e.exits = []int{1090}
	e.m.tlsCheck = func(_ int, _ string, port int, _ string) error {
		if port == 2443 {
			return errBlocked
		}
		return nil
	}
	if err := e.m.verifyRelay(tR5); err == nil || !strings.Contains(err.Error(), ":2443") {
		t.Fatalf("XHTTP port not checked: %v", err)
	}
	e.m.tlsCheck = func(int, string, int, string) error { return nil }
	e.settings["hysteria2_obfs_password"] = "psk"
	e.udp[tR5] = fmt.Errorf("%w от %s", errBambooNoReply, tR5)
	if err := e.m.verifyRelay(tR5); err == nil {
		t.Fatal("silent Bamboo through the relay accepted")
	}
	e.m.udpControl = func(int) error { return errors.New("exit lost UDP") }
	if err := e.m.verifyRelay(tR5); err != nil {
		t.Fatalf("UDP that cannot be checked is not the relay's fault: %v", err)
	}
}
