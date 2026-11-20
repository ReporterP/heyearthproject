package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/heyearth/backend/internal/hubnet"
)

// Steps 6–7 and cancel (spec «6. Переходный период», «7. Вывести старый адрес», «Cancel»).

const (
	hubStepTransition = "transition" // the old-address counter samples
	hubStepFinish     = "finish"

	hubFinishHours  = 72
	hubFinishRatio  = 0.90
	hubFinishPerMin = 1.0
	hubSampleEvery  = 5 * time.Minute
	hubSampleKeep   = 40
)

type hubCounterSample struct {
	At      time.Time `json:"at"`
	Packets uint64    `json:"packets"`
}

type hubAddrTransitionRecord struct {
	Samples []hubCounterSample `json:"samples"`
	Error   string             `json:"error,omitempty"`
}

type hubAddrTransitionView struct {
	Since     time.Time `json:"since"`
	Hours     float64   `json:"hours"`
	N         int       `json:"n"`
	M         int       `json:"m"`
	OldPerMin float64   `json:"old_per_min"`
	Measured  bool      `json:"measured"` // the counter has an hour (or at least a minute) of samples
	Ready     bool      `json:"ready"`
	Blockers  []string  `json:"blockers"`
}

type hubAddrFinishChecks struct {
	Override bool     `json:"override"`
	Blockers []string `json:"blockers"`
	Removed  bool     `json:"removed"`
	Notes    []string `json:"notes"`
	By       string   `json:"by"`
}

func (e *hubAddrEngine) transitionRecord(c hubAddrChange) hubAddrTransitionRecord {
	var r hubAddrTransitionRecord
	if s, ok := c.Steps[hubStepTransition]; ok && len(s.Checks) > 0 {
		_ = json.Unmarshal(s.Checks, &r)
	}
	return r
}

// sampleCounter (the watcher, every 5 minutes in «transition») reads the old-address counter.
func (e *hubAddrEngine) sampleCounter(ctx context.Context, c hubAddrChange) {
	rec := e.transitionRecord(c)
	now := e.env.Now().UTC()
	if n := len(rec.Samples); n > 0 && now.Sub(rec.Samples[n-1].At) < hubSampleEvery {
		return
	}
	out, err := e.env.Hub.Run(ctx, hubnet.CounterReadScript())
	if err == nil {
		var p uint64
		if p, err = hubnet.ParseCounter(out); err == nil {
			rec.Samples = append(rec.Samples, hubCounterSample{At: now, Packets: p})
			if len(rec.Samples) > hubSampleKeep {
				rec.Samples = rec.Samples[len(rec.Samples)-hubSampleKeep:]
			}
			rec.Error = ""
		}
	}
	if err != nil {
		rec.Error = err.Error()
	}
	b, _ := json.Marshal(rec)
	_ = e.env.Repo.SaveStep(ctx, c.ID, hubStepTransition, hubAddrStep{Status: hubStepRunning, FinishedAt: &now, Checks: b})
}

// oldRate: new connections per minute to the old address between the latest sample and the
// oldest one at most an hour before it.
func oldRate(ss []hubCounterSample) (float64, bool) {
	if len(ss) < 2 {
		return 0, false
	}
	last := ss[len(ss)-1]
	first := last
	for i := len(ss) - 2; i >= 0; i-- {
		if last.At.Sub(ss[i].At) > time.Hour {
			break
		}
		first = ss[i]
	}
	mins := last.At.Sub(first.At).Minutes()
	if mins < 1 {
		return 0, false
	}
	d := float64(0)
	if last.Packets > first.Packets {
		d = float64(last.Packets - first.Packets)
	}
	return d / mins, true
}

// Transition is the page's view of «transition»: hours since the switch, N of M refreshed, the
// old-address rate, and what still blocks step 7.
func (e *hubAddrEngine) Transition(ctx context.Context, id int64) (hubAddrTransitionView, error) {
	c, err := e.env.Repo.Get(ctx, id)
	if err != nil {
		return hubAddrTransitionView{}, err
	}
	if c.State != hubAddrTransition || c.SwitchedAt == nil {
		return hubAddrTransitionView{}, fmt.Errorf("переходного периода нет: смена в состоянии «%s»", c.State)
	}
	v := hubAddrTransitionView{Since: *c.SwitchedAt}
	v.Hours = e.env.Now().Sub(*c.SwitchedAt).Hours()
	if v.Hours < hubFinishHours {
		v.Blockers = append(v.Blockers, fmt.Sprintf("прошло %.0f ч из %d", v.Hours, hubFinishHours))
	}
	if e.env.Progress != nil {
		if v.N, v.M, err = e.env.Progress(ctx, *c.SwitchedAt); err != nil {
			v.Blockers = append(v.Blockers, "счётчик подписок не прочитан: "+err.Error())
		} else if v.M > 0 && float64(v.N) < hubFinishRatio*float64(v.M) {
			v.Blockers = append(v.Blockers, fmt.Sprintf("обновили подписку %d из %d (нужно 90 %%)", v.N, v.M))
		}
	}
	rec := e.transitionRecord(c)
	v.OldPerMin, v.Measured = oldRate(rec.Samples)
	switch {
	case !v.Measured:
		v.Blockers = append(v.Blockers, "счётчик старого адреса ещё не набрал данных")
	case v.OldPerMin >= hubFinishPerMin:
		v.Blockers = append(v.Blockers, fmt.Sprintf("на старый адрес ещё приходит %.1f новых соединений в минуту", v.OldPerMin))
	}
	v.Ready = len(v.Blockers) == 0
	return v, nil
}

func hubReaddUnit(id int64) string       { return fmt.Sprintf("heyearth-hubaddr-readd-%d", id) }
func hubReaddScriptPath(id int64) string { return fmt.Sprintf("%s/%d-readd.sh", hubAddrStateDir, id) }

// Finish ends the transition (step 7). Without override it is refused while Transition has
// blockers; with it, the blockers are recorded. The old address leaves the interface and netplan
// unless it carries the gateway — under a re-add timer that is disarmed once the Hub still
// reaches its database and an exit.
func (e *hubAddrEngine) Finish(ctx context.Context, id int64, override bool, by string) ([]string, error) {
	c, err := e.env.Repo.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if c.State != hubAddrTransition {
		return nil, fmt.Errorf("завершать нечего: смена в состоянии «%s»", c.State)
	}
	v, err := e.Transition(ctx, id)
	if err != nil {
		return nil, err
	}
	if !v.Ready && !override {
		return nil, fmt.Errorf("рано выводить старый адрес: %s", strings.Join(v.Blockers, "; "))
	}
	fin := hubAddrFinishChecks{Override: override && !v.Ready, Blockers: v.Blockers, By: by}
	_ = e.env.SetSetting(hubAddrTransitionKey, "")
	_, _ = e.env.Hub.Run(ctx, hubnet.CounterDropScript())
	fin.Notes = append(fin.Notes, fmt.Sprintf("Поменяйте DEPLOY_HOST в server/.env на %s (и адрес в своих SSH-скриптах).", c.NewIP))

	st, err := e.readHub(ctx)
	if err != nil {
		return nil, err
	}
	old := mustAddr(c.OldIP)
	switch {
	case hubnet.IsGatewayCarrier(st.addrs, st.route, old):
		fin.Notes = append(fin.Notes, fmt.Sprintf("%s оставлен на интерфейсе: через него идёт шлюз, снимать его нельзя.", old))
	case hubnet.HasAddr(st.addrs, old) && st.route.Src != old:
		if err := e.removeOldAddr(ctx, c); err != nil {
			return nil, fmt.Errorf("старый адрес не снят (возвращён): %w", err)
		}
		fin.Removed = true
		fin.Notes = append(fin.Notes,
			fmt.Sprintf("Откажитесь от адреса %s у VDSina.", old),
			fmt.Sprintf("Переустановите выходы и входы, чтобы убрать %s из их разрешений.", old))
	}
	_, _ = e.env.Hub.Run(ctx, fmt.Sprintf("rm -f %s %s %s\n", hubRevertScriptPath(id), hubReaddScriptPath(id), hubProbePath))

	now := e.env.Now().UTC()
	b, _ := json.Marshal(fin)
	if err := e.env.Repo.SaveStep(ctx, id, hubStepFinish, hubAddrStep{Status: hubStepOK, FinishedAt: &now, Checks: b}); err != nil {
		return nil, err
	}
	if err := e.env.Repo.SetState(ctx, id, hubAddrTransition, hubAddrFinished, func(x *hubAddrChange) { x.FinishedAt = &now }); err != nil {
		return nil, err
	}
	return fin.Notes, nil
}

func (e *hubAddrEngine) removeOldAddr(ctx context.Context, c hubAddrChange) error {
	old := mustAddr(c.OldIP)
	readd := hubnet.AddrAddScript(c.Iface, old)
	if _, err := e.env.Hub.PutFile(ctx, hubReaddScriptPath(c.ID), []byte(readd), 0o700); err != nil {
		return err
	}
	if out, err := e.env.Hub.Run(ctx, hubnet.ArmTimerScript(hubReaddUnit(c.ID), hubReaddScriptPath(c.ID), int(hubConfirmWindow.Seconds()))); err != nil {
		return fmt.Errorf("таймер возврата адреса: %v: %s", err, out)
	}
	path, doc, err := e.readNetplan(ctx, c.Iface)
	if err != nil {
		return err
	}
	undo := func() {
		_, _ = e.env.Hub.Run(ctx, readd)
		_, _ = e.env.Hub.Run(ctx, hubnet.NetplanWriteScript(path, doc, hubNetplanBackup(path, c.ID)))
		_, _ = e.env.Hub.Run(ctx, hubnet.DisarmTimerScript(hubReaddUnit(c.ID)))
	}
	if out, err := e.env.Hub.Run(ctx, hubnet.AddrDelScript(c.Iface, old)); err != nil {
		undo()
		return fmt.Errorf("ip addr del: %v: %s", err, out)
	}
	if edited, changed, err := hubnet.NetplanRemoveAddress(doc, c.Iface, old.String()); err != nil {
		undo()
		return err
	} else if changed {
		if out, err := e.env.Hub.Run(ctx, hubnet.NetplanWriteScript(path, edited, hubNetplanBackup(path, c.ID))); err != nil {
			undo()
			return fmt.Errorf("netplan: %v: %s", err, out)
		}
	}
	// The Hub still works: the address is gone, the database answers, an exit is reachable.
	st, err := e.readHub(ctx)
	if err == nil && hubnet.HasAddr(st.addrs, old) {
		err = fmt.Errorf("%s всё ещё на интерфейсе", old)
	}
	if err == nil {
		_, err = e.env.Repo.Get(ctx, c.ID)
	}
	if err == nil {
		_, err = e.env.PickExit()
	}
	if err != nil {
		undo()
		return err
	}
	if out, err := e.env.Hub.Run(ctx, hubnet.DisarmTimerScript(hubReaddUnit(c.ID))); err != nil {
		return fmt.Errorf("адрес снят, но таймер возврата не снят: %v: %s", err, out)
	}
	e.env.ResetHubIPs()
	return nil
}

// Cancel abandons a change before the switch: the address the wizard added comes off, netplan
// gets its backup back. DNS records the owner changed are the owner's to put back.
func (e *hubAddrEngine) Cancel(ctx context.Context, id int64) ([]string, error) {
	c, err := e.env.Repo.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if !containsStr([]string{hubAddrDraft, hubAddrAdded, hubAddrChecked, hubAddrNodesReady, hubAddrDNSReady}, c.State) {
		return nil, fmt.Errorf("отменить нельзя в состоянии «%s» — после переключения только «Вернуть как было»", c.State)
	}
	e.mu.Lock()
	running := e.busy[id] != ""
	e.mu.Unlock()
	if running {
		return nil, errHubAddrBusy
	}
	var add hubAddrAddChecks
	if s, ok := c.Steps[hubStepAdd]; ok && len(s.Checks) > 0 {
		_ = json.Unmarshal(s.Checks, &add)
	}
	if add.NetplanChanged && add.NetplanPath != "" {
		if out, err := e.env.Hub.Run(ctx, hubnet.NetplanRestoreScript(add.NetplanPath, add.Backup)); err != nil {
			return nil, fmt.Errorf("netplan не возвращён: %v: %s", err, out)
		}
	}
	if add.AddedRuntime {
		if out, err := e.env.Hub.Run(ctx, hubnet.AddrDelScript(c.Iface, mustAddr(c.NewIP))); err != nil {
			return nil, fmt.Errorf("адрес не снят: %v: %s", err, out)
		}
	}
	e.env.ResetHubIPs()
	var notes []string
	var dns hubAddrDNSChecks
	if s, ok := c.Steps[hubStepDNS]; ok && len(s.Checks) > 0 {
		_ = json.Unmarshal(s.Checks, &dns)
		var names []string
		for n, a := range dns.Before {
			if a == c.OldIP {
				names = append(names, n)
			}
		}
		if len(names) > 0 {
			notes = append(notes, fmt.Sprintf("Если меняли DNS-записи, верните их на %s: %s.", c.OldIP, strings.Join(names, ", ")))
		}
	}
	return notes, e.env.Repo.SetState(ctx, id, c.State, hubAddrCancelled, nil)
}
