package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"strconv"
	"strings"
	"time"

	"github.com/heyearth/backend/internal/hubnet"
)

// Step 5 «Переключить» (spec step 5): the only risky change. The revert script is installed and
// its timer armed BEFORE the route source moves; the timer is re-armed while the entries are
// re-pointed (never shortened, never past 40 minutes), then the owner has 10 minutes to confirm
// from the admin. No confirmation → the timer puts the Hub back and the API re-points the entries.

const (
	hubArmFirst      = 600 * time.Second
	hubArmStep       = 5 * time.Minute
	hubRearmEvery    = 2 * time.Minute
	hubArmCap        = 40 * time.Minute
	hubConfirmWindow = 600 * time.Second
	hubRevertRetry   = 5 * time.Minute
	hubProbeBudget   = 8 * time.Minute // the checks after the entries: tunnels up to 2 min, probes
	hubStepRevert    = "revert"        // record of the API-side revert
)

var errHubTimerFired = errors.New("таймер отката сработал — смена откатывается")

func hubRevertUnit(id int64) string       { return "heyearth-hubaddr-revert-" + strconv.FormatInt(id, 10) }
func hubRevertScriptPath(id int64) string { return fmt.Sprintf("%s/%d-revert.sh", hubAddrStateDir, id) }
func hubRevertMarker(id int64) string     { return fmt.Sprintf("%s/%d-reverted", hubAddrStateDir, id) }

type hubAddrSwitchChecks struct {
	ArmedAt  time.Time             `json:"armed_at"`
	Route    hubnet.DefaultRoute   `json:"route"` // before the switch
	OldSrc   string                `json:"old_src"`
	Outbound []hubnet.TargetResult `json:"outbound"`
	Relays   []hubAddrNodeResult   `json:"relays"`
	Probes   []hubAddrProbe        `json:"probes"`
	Green    bool                  `json:"green"`
}

// revertTimer keeps the armed timer's end: re-arms extend it, never shorten it, never past cap.
type revertTimer struct {
	e       *hubAddrEngine
	id      int64
	armedAt time.Time
	end     time.Time
	lastArm time.Time
}

func (t *revertTimer) arm(ctx context.Context, end time.Time) error {
	now := t.e.env.Now()
	secs := int(end.Sub(now).Round(time.Second).Seconds())
	if secs < 1 {
		secs = 1
	}
	out, err := t.e.env.Hub.Run(ctx, hubnet.ArmTimerScript(hubRevertUnit(t.id), hubRevertScriptPath(t.id), secs, hubRevertMarker(t.id)))
	if strings.Contains(out, "REVERT_RUNNING") || strings.Contains(out, "REVERT_DONE") {
		return errHubTimerFired
	}
	if err != nil {
		return fmt.Errorf("таймер отката не взведён: %v: %s", err, strings.TrimSpace(out))
	}
	t.end, t.lastArm = end, now
	return nil
}

// extend pushes the timer's end to at least end (no 40-minute cap: the bounded probe phase).
func (t *revertTimer) extend(ctx context.Context, end time.Time) error {
	if !end.After(t.end) {
		return nil
	}
	return t.arm(ctx, end)
}

// tick runs while entry jobs are polled: every 2 minutes the timer is pushed to now+5 min (capped
// at armedAt+40 min); past the cap the timer has fired.
func (t *revertTimer) tick(ctx context.Context) error {
	now := t.e.env.Now()
	limit := t.armedAt.Add(hubArmCap)
	if !now.Before(t.end) && !now.Before(limit) {
		return errHubTimerFired
	}
	if now.Sub(t.lastArm) < hubRearmEvery {
		return nil
	}
	end := now.Add(hubArmStep)
	if end.After(limit) {
		end = limit
	}
	if !end.After(t.end) {
		return nil
	}
	return t.arm(ctx, end)
}

func (e *hubAddrEngine) revertRequested(id int64) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.reverting[id]
}

func (e *hubAddrEngine) relaysToRepoint(ctx context.Context, c hubAddrChange) ([]hubAddrNode, error) {
	nodes, err := e.onlineNodes(ctx)
	if err != nil {
		return nil, err
	}
	excluded := prevNodesChecks(c.Steps[hubStepNodes]).Excluded
	var out []hubAddrNode
	for _, n := range nodes {
		if n.Role == "relay" && !containsInt(excluded, n.ID) {
			out = append(out, n)
		}
	}
	return out, nil
}

func (e *hubAddrEngine) stepSwitch(ctx context.Context, c hubAddrChange, lg *stepLog) (any, error) {
	cur, nw := mustAddr(c.OldIP), mustAddr(c.NewIP)
	var ch hubAddrSwitchChecks
	nodes, err := e.onlineNodes(ctx)
	if err != nil {
		return ch, err
	}
	ch.Outbound = e.outbound(ctx, cur, nw, e.outboundTargets(ctx, nodes))
	for _, t := range ch.Outbound {
		if t.Verdict == hubnet.VerdictFail {
			return ch, fmt.Errorf("с нового адреса перестал доходить %s (%d из %d) — переключение не начато", t.Target, t.New, t.Tries)
		}
	}
	st, err := e.readHub(ctx)
	if err != nil {
		return ch, err
	}
	ch.Route = st.route
	ch.OldSrc = c.OldIP
	if st.route.Src.IsValid() {
		ch.OldSrc = st.route.Src.String()
	}
	relays, err := e.relaysToRepoint(ctx, c)
	if err != nil {
		return ch, err
	}
	if _, doc, err := e.readNetplan(ctx, c.Iface); err != nil {
		return ch, err
	} else if _, _, err := hubnet.NetplanSetDefaultFrom(doc, c.Iface, nw); err != nil {
		return ch, fmt.Errorf("netplan: %v — источник маршрута не закрепить, переключение не начато", err)
	}

	// 1. Revert first: installed and armed before anything changes.
	script := hubnet.RevertScript(hubnet.RevertParams{ChangeID: c.ID, Route: st.route, OldSrc: mustAddr(ch.OldSrc),
		OldMain: c.OldIP, Marker: hubRevertMarker(c.ID)})
	if _, err := e.env.Hub.PutFile(ctx, hubRevertScriptPath(c.ID), []byte(script), 0o700); err != nil {
		return ch, fmt.Errorf("скрипт отката не записан: %w", err)
	}
	now := e.env.Now()
	if err := e.env.Repo.SetState(ctx, c.ID, hubAddrDNSReady, hubAddrSwitching, func(x *hubAddrChange) {
		t := now.UTC()
		x.SwitchedAt, x.ConfirmDeadline = &t, nil
	}); err != nil {
		return ch, err
	}
	timer := &revertTimer{e: e, id: c.ID, armedAt: now}
	if err := timer.arm(ctx, now.Add(hubArmFirst)); err != nil {
		_ = e.env.Repo.SetState(ctx, c.ID, hubAddrSwitching, hubAddrReverting, func(x *hubAddrChange) { x.RevertReason = err.Error() })
		e.retryRevertRelays(ctx, c, nil)
		return ch, err
	}
	ch.ArmedAt = now.UTC()
	lg.printf("таймер отката взведён на %s", hubArmFirst)

	fail := func(err error) (any, error) {
		lg.printf("переключение прервано: %v — откатываю", err)
		e.revertFromStep(ctx, c, err.Error(), lg)
		return ch, err
	}
	// 2. Runtime route source, tunnels from it.
	if out, err := e.env.Hub.Run(ctx, hubnet.RouteReplaceScript(st.route, nw)); err != nil {
		return fail(fmt.Errorf("маршрут: %v: %s", err, strings.TrimSpace(out)))
	}
	if _, err := e.env.Hub.Run(ctx, hubnet.RestartTunnelsScript()); err != nil {
		lg.printf("туннели не перезапустились: %v", err)
	}
	// 3. Settings: users and entries move, the transition starts.
	if err := e.env.SetSetting("main_server_host", nw.String()); err != nil {
		return fail(fmt.Errorf("main_server_host: %w", err))
	}
	e.bumpConfigVersion(lg)
	if err := e.env.SetSetting(hubAddrTransitionKey, now.UTC().Format(time.RFC3339)); err != nil {
		lg.printf("флаг переходного периода не записан: %v", err)
	}
	if e.env.Sync != nil {
		e.env.Sync()
	}
	if out, err := e.env.Hub.Run(ctx, hubnet.CounterTableScript(cur)); err != nil {
		lg.printf("счётчик старого адреса не создан: %v: %s", err, strings.TrimSpace(out))
	}
	lg.printf("хаб отвечает с %s, подписки и входы переводятся", nw)

	// 4. Entries, with the timer pushed while their jobs run.
	allOK := true
	for _, n := range relays {
		if e.revertRequested(c.ID) {
			return ch, errors.New("владелец запросил откат")
		}
		r := hubAddrNodeResult{ServerID: n.ID, Name: n.Name, IP: n.IP, Role: n.Role}
		job, err := e.reprovisionNodeWith(ctx, n.ID, func() error {
			if e.revertRequested(c.ID) {
				return errors.New("владелец запросил откат")
			}
			return timer.tick(ctx)
		})
		r.JobID = job
		if errors.Is(err, errHubTimerFired) {
			ch.Relays = append(ch.Relays, r)
			return ch, err
		}
		if err != nil {
			r.Status, r.Error, allOK = hubNodeFailed, err.Error(), false
			lg.printf("%s: %v", n.Name, err)
		} else {
			r.Status = hubNodeOK
			lg.printf("%s: переведён на %s", n.Name, nw)
		}
		ch.Relays = append(ch.Relays, r)
	}

	// 5. Checks from outside, under a timer that outlasts them.
	if err := timer.extend(ctx, e.env.Now().Add(hubProbeBudget)); err != nil {
		return ch, err
	}
	ch.Probes = e.switchProbes(ctx, nw)
	ch.Green = allOK
	for _, p := range ch.Probes {
		if !p.OK {
			ch.Green = false
		}
	}
	// 6. The owner's window.
	deadline := e.env.Now().Add(hubConfirmWindow)
	if err := timer.arm(ctx, deadline); err != nil {
		return ch, err
	}
	if err := e.env.Repo.SetState(ctx, c.ID, hubAddrSwitching, hubAddrSwitching, func(x *hubAddrChange) {
		d := deadline.UTC()
		x.ConfirmDeadline = &d
	}); err != nil {
		return ch, err
	}
	if !ch.Green {
		var bad []string
		for _, p := range ch.Probes {
			if !p.OK {
				bad = append(bad, p.Name+": "+p.Error)
			}
		}
		for _, r := range ch.Relays {
			if r.Status != hubNodeOK {
				bad = append(bad, r.Name+": "+r.Error)
			}
		}
		return ch, fmt.Errorf("после переключения не всё зелёное (подтвердите вручную или верните как было): %s", strings.Join(bad, "; "))
	}
	lg.printf("всё зелёное — подтвердите в течение %s", hubConfirmWindow)
	return ch, nil
}

func (e *hubAddrEngine) switchProbes(ctx context.Context, nw netip.Addr) []hubAddrProbe {
	var ps []hubAddrProbe
	add := func(name string, err error) {
		ps = append(ps, hubAddrProbe{Name: name, OK: err == nil, Error: errTextOrEmpty(err)})
	}
	if socks, err := e.env.PickExit(); err != nil {
		add("выход для проверки", err)
	} else {
		add("Берёза на "+nw.String()+":443", e.env.RealityTLS(socks, nw.String(), 443))
		add("Дуб на "+nw.String()+":2443", e.env.RealityTLS(socks, nw.String(), 2443))
		if e.env.Bamboo != nil {
			add("Бамбук на "+nw.String(), e.env.Bamboo(socks, nw.String()))
		}
	}
	if e.env.ExitTunnels != nil {
		var tun []hubAddrProbe
		var err error
		for i := 0; i < 12; i++ { // tunnels come up from the new source within ~2 minutes
			if tun, err = e.env.ExitTunnels(ctx); err == nil && allProbesOK(tun) {
				break
			}
			if e.env.Sleep(ctx, 10*time.Second) != nil {
				break
			}
		}
		if err != nil {
			add("туннели к выходам", err)
		}
		ps = append(ps, tun...)
	}
	return ps
}

func allProbesOK(ps []hubAddrProbe) bool {
	for _, p := range ps {
		if !p.OK {
			return false
		}
	}
	return true
}

func (e *hubAddrEngine) bumpConfigVersion(lg *stepLog) {
	v, err := e.env.Setting("config_version", "1")
	if err != nil {
		lg.printf("config_version не прочитан: %v", err)
		return
	}
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil {
		return
	}
	if err := e.env.SetSetting("config_version", strconv.Itoa(n+1)); err != nil {
		lg.printf("config_version не записан: %v", err)
	}
}

func (e *hubAddrEngine) markerExists(ctx context.Context, id int64) bool {
	out, _ := e.env.Hub.Run(ctx, "test -e "+hubRevertMarker(id)+" && echo YES || echo NO\n")
	return strings.TrimSpace(out) == "YES"
}

func (e *hubAddrEngine) revertServiceActive(ctx context.Context, id int64) bool {
	out, _ := e.env.Hub.Run(ctx, "systemctl is-active "+hubRevertUnit(id)+".service\n")
	st := strings.TrimSpace(out)
	return st == "active" || st == "activating"
}

// Confirm ends the switch: the timer is disarmed and netplan gets `from: <new>`. Refused while
// the step runs, after the timer fired, when the route source is not the new address, or on red
// checks without force.
func (e *hubAddrEngine) Confirm(ctx context.Context, id int64, force bool) error {
	c, err := e.env.Repo.Get(ctx, id)
	if err != nil {
		return err
	}
	if c.State != hubAddrSwitching {
		return fmt.Errorf("подтверждать нечего: смена в состоянии «%s»", c.State)
	}
	e.mu.Lock()
	running := e.busy[id] != "" || e.reverting[id]
	e.mu.Unlock()
	if running {
		return errHubAddrBusy
	}
	if e.markerExists(ctx, id) || e.revertServiceActive(ctx, id) {
		return errHubTimerFired
	}
	st, err := e.readHub(ctx)
	if err != nil {
		return err
	}
	if st.route.Src != mustAddr(c.NewIP) {
		return fmt.Errorf("хаб отвечает не с %s (источник %s) — подтверждать нельзя", c.NewIP, st.route.Src)
	}
	var ch hubAddrSwitchChecks
	_ = json.Unmarshal(c.Steps[hubStepSwitch].Checks, &ch)
	if !ch.Green && !force {
		return errors.New("проверки после переключения не зелёные — подтвердите с отметкой «несмотря на» или верните как было")
	}
	// netplan first: if it cannot be written, the timer stays armed and the change reverts by itself.
	if err := e.setNetplanFrom(ctx, c, mustAddr(c.NewIP)); err != nil {
		return fmt.Errorf("netplan не записан, таймер отката не снят: %w", err)
	}
	if out, err := e.env.Hub.Run(ctx, hubnet.DisarmTimerScript(hubRevertUnit(id))); err != nil {
		return fmt.Errorf("netplan записан, но таймер не снят — подтвердите ещё раз: %v: %s", err, out)
	}
	now := e.env.Now().UTC()
	return e.env.Repo.SetState(ctx, id, hubAddrSwitching, hubAddrTransition, func(x *hubAddrChange) { x.ConfirmedAt = &now })
}

func (e *hubAddrEngine) setNetplanFrom(ctx context.Context, c hubAddrChange, src netip.Addr) error {
	path, doc, err := e.readNetplan(ctx, c.Iface)
	if err != nil {
		return err
	}
	edited, changed, err := hubnet.NetplanSetDefaultFrom(doc, c.Iface, src)
	if err != nil || !changed {
		return err
	}
	out, err := e.env.Hub.Run(ctx, hubnet.NetplanWriteScript(path, edited, hubNetplanBackup(path, c.ID)))
	if err != nil {
		return fmt.Errorf("%v: %s", err, strings.TrimSpace(out))
	}
	return nil
}

// Revert («Вернуть как было», switching or transition): the revert script runs now (unless the
// timer already ran it), netplan goes back to the old source, then the API side (entries) in the
// background — none of it dies with the admin's HTTP request.
func (e *hubAddrEngine) Revert(ctx context.Context, id int64, reason string) error {
	ctx = context.WithoutCancel(ctx)
	c, err := e.env.Repo.Get(ctx, id)
	if err != nil {
		return err
	}
	if c.State != hubAddrSwitching && c.State != hubAddrTransition {
		return fmt.Errorf("возвращать нечего: смена в состоянии «%s»", c.State)
	}
	e.mu.Lock()
	if e.reverting[id] {
		e.mu.Unlock()
		return errHubAddrBusy
	}
	e.reverting[id] = true
	e.mu.Unlock()
	done := func() { e.mu.Lock(); delete(e.reverting, id); e.mu.Unlock() }
	if !e.markerExists(ctx, id) && !e.revertServiceActive(ctx, id) {
		if out, err := e.env.Hub.Run(ctx, "/bin/sh "+hubRevertScriptPath(id)+"\n"); err != nil {
			done()
			return fmt.Errorf("скрипт отката: %v: %s", err, strings.TrimSpace(out))
		}
	}
	var ch hubAddrSwitchChecks
	_ = json.Unmarshal(c.Steps[hubStepSwitch].Checks, &ch)
	if old := mustAddr(ch.OldSrc); old.IsValid() {
		if err := e.setNetplanFrom(ctx, c, old); err != nil {
			e.env.Alert("hub-address-netplan", "netplan не возвращён на старый источник: "+err.Error())
		}
	}
	e.env.Go(func() {
		defer done()
		bctx, cancel := context.WithTimeout(context.Background(), 2*time.Hour)
		defer cancel()
		for i := 0; i < 1200; i++ { // the switch step stops at its next poll
			e.mu.Lock()
			running := e.busy[id] != ""
			e.mu.Unlock()
			if !running || e.env.Sleep(bctx, time.Second) != nil {
				break
			}
		}
		cur, err := e.env.Repo.Get(bctx, id)
		if err == nil {
			err = e.apiRevert(bctx, cur, reason)
		}
		if err != nil {
			e.env.Alert("hub-address-revert", "откат смены адреса хаба: "+err.Error())
		}
	})
	return nil
}

// revertFromStep: the switch step failed after the timer was armed — the script runs now.
func (e *hubAddrEngine) revertFromStep(ctx context.Context, c hubAddrChange, reason string, lg *stepLog) {
	if !e.markerExists(ctx, c.ID) {
		if out, err := e.env.Hub.Run(ctx, "/bin/sh "+hubRevertScriptPath(c.ID)+"\n"); err != nil {
			lg.printf("скрипт отката: %v: %s", err, strings.TrimSpace(out))
		}
	}
	if cur, err := e.env.Repo.Get(ctx, c.ID); err == nil {
		if err := e.apiRevert(ctx, cur, reason); err != nil {
			lg.printf("откат: %v", err)
		}
	}
}

type hubAddrRevertChecks struct {
	Relays  []hubAddrNodeResult `json:"relays"`
	LastTry time.Time           `json:"last_try"`
	Alerted bool                `json:"alerted"`
}

// apiRevert is the API side of a revert: the record goes to «reverting», the Hub side is made
// sure of, then the entries are re-pointed (retried by the watcher until all are back).
func (e *hubAddrEngine) apiRevert(ctx context.Context, c hubAddrChange, reason string) error {
	if c.State == hubAddrSwitching || c.State == hubAddrTransition {
		if err := e.env.Repo.SetState(ctx, c.ID, c.State, hubAddrReverting, func(x *hubAddrChange) { x.RevertReason = reason }); err != nil {
			return err
		}
	}
	var lg stepLog
	if err := e.ensureHubReverted(ctx, c, &lg); err != nil {
		return err
	}
	return e.retryRevertRelays(ctx, c, &lg)
}

// ensureHubReverted: settings, timer, counter and route source as before the switch (a reboot or
// a half-done revert leaves some of them behind).
func (e *hubAddrEngine) ensureHubReverted(ctx context.Context, c hubAddrChange, lg *stepLog) error {
	if v, err := e.env.Setting("main_server_host", ""); err == nil && strings.TrimSpace(v) != c.OldIP {
		if err := e.env.SetSetting("main_server_host", c.OldIP); err != nil {
			return err
		}
		e.bumpConfigVersion(lg)
	}
	_ = e.env.SetSetting(hubAddrTransitionKey, "")
	_, _ = e.env.Hub.Run(ctx, hubnet.DisarmTimerScript(hubRevertUnit(c.ID)))
	_, _ = e.env.Hub.Run(ctx, hubnet.CounterDropScript())
	var sw hubAddrSwitchChecks
	_ = json.Unmarshal(c.Steps[hubStepSwitch].Checks, &sw)
	if st, err := e.readHub(ctx); err == nil && st.route.Src == mustAddr(c.NewIP) {
		old := mustAddr(sw.OldSrc)
		if !old.IsValid() {
			old = mustAddr(c.OldIP)
		}
		_, _ = e.env.Hub.Run(ctx, hubnet.RouteReplaceScript(st.route, old))
		_, _ = e.env.Hub.Run(ctx, hubnet.RestartTunnelsScript())
	}
	if e.env.Sync != nil {
		e.env.Sync()
	}
	return nil
}

func (e *hubAddrEngine) revertRecord(c hubAddrChange) hubAddrRevertChecks {
	var r hubAddrRevertChecks
	if s, ok := c.Steps[hubStepRevert]; ok && len(s.Checks) > 0 {
		_ = json.Unmarshal(s.Checks, &r)
	}
	return r
}

// retryRevertRelays re-points the entries that are not back yet (all of them the first time).
func (e *hubAddrEngine) retryRevertRelays(ctx context.Context, c hubAddrChange, lg *stepLog) error {
	if lg == nil {
		lg = &stepLog{}
	}
	cur, err := e.env.Repo.Get(ctx, c.ID)
	if err != nil {
		return err
	}
	rec := e.revertRecord(cur)
	done := map[int]bool{}
	for _, r := range rec.Relays {
		if r.Status == hubNodeOK {
			done[r.ServerID] = true
		}
	}
	relays, err := e.relaysToRepoint(ctx, cur)
	if err != nil {
		return err
	}
	var results []hubAddrNodeResult
	allOK := true
	for _, n := range relays {
		r := hubAddrNodeResult{ServerID: n.ID, Name: n.Name, IP: n.IP, Role: n.Role, Status: hubNodeOK}
		if !done[n.ID] {
			job, err := e.reprovisionNodeWith(ctx, n.ID, nil)
			r.JobID = job
			if err != nil {
				r.Status, r.Error, allOK = hubNodeFailed, err.Error(), false
				lg.printf("%s: обратно не переведён: %v", n.Name, err)
			}
		}
		results = append(results, r)
	}
	rec.Relays, rec.LastTry = results, e.env.Now().UTC()
	if !rec.Alerted {
		e.env.Alert("hub-address-revert", fmt.Sprintf("Смена адреса хаба %s → %s откатывается: %s", cur.OldIP, cur.NewIP, cur.RevertReason))
		rec.Alerted = true
	}
	status := hubStepRunning
	if allOK {
		status = hubStepOK
	}
	b, _ := json.Marshal(rec)
	now := e.env.Now().UTC()
	if err := e.env.Repo.SaveStep(ctx, cur.ID, hubStepRevert, hubAddrStep{Status: status, FinishedAt: &now, Log: lg.String(), Checks: b}); err != nil {
		return err
	}
	if !allOK {
		return nil
	}
	return e.env.Repo.SetState(ctx, cur.ID, hubAddrReverting, hubAddrReverted, func(x *hubAddrChange) { x.RevertedAt = &now })
}
