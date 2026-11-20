package main

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log"
	"net"
	"slices"
	"strconv"
	"time"

	"github.com/heyearth/backend/internal/biz"
)

// ── Entries monitor (phase 3, spec 2026-10-12 section 4) ──
// Once a minute every entry (Hub = 0, each online relay) is checked from outside — TLS with the
// Reality SNI to <entry>:443 through a foreign exit, and for relays the Bamboo VN probe through
// their hop ports. While the main entry is blocked (3 consecutive REAL failures) and a reserve is
// healthy, the main subscription lines move to the reserve (entry_failover_*); they return after
// 2 successes and 30 min of stability. A probe that could not run (no exit, broken SOCKS, the
// exit's control failing before or right after the check, a DB read error) is a skip: never a
// failure, never a recovery (Review Focus 4).

const (
	entryCheckInterval  = time.Minute
	entryFailThreshold  = 3
	entryReturnStable   = 30 * time.Minute
	entryDBTimeout      = 5 * time.Second
	entryBlindAfter     = time.Hour
	entryControlAddr    = "1.1.1.1:443"
	entryNoReserveAlert = "entry-failover-no-reserve"
	entryBlindAlert     = "entries-probe-blind"
)

var errEntryProbeTool = errors.New("сбой инструмента проверки")

func entryFailoverAlertKey(target int) string  { return "entry-failover-" + strconv.Itoa(target) }
func relayAlertKey(id int, what string) string { return fmt.Sprintf("relay-%d-%s", id, what) }

type entryVerdict int

const (
	entrySkip entryVerdict = iota
	entryFail
	entryOK
)

// decideEntry: a failed check counts only if the exit still passes its own control afterwards.
func decideEntry(checkErr error, control func() error) entryVerdict {
	switch {
	case checkErr == nil:
		return entryOK
	case errors.Is(checkErr, errEntryProbeTool), control() != nil:
		return entrySkip
	default:
		return entryFail
	}
}

// entryTrack: hysteresis of one check of one entry. status is what was published (persisted,
// survives restarts): unhealthy after entryFailThreshold failures, healthy after probeResolveOKs
// successes; verdicts more than loopProbeStaleAfter apart restart both streaks.
type entryTrack struct {
	streak  loopProbeStreak
	status  string
	okSince time.Time // start of the current run of successes; zero while failing
	dirty   bool      // status changed, not saved yet
}

func (t *entryTrack) record(now time.Time, ok bool) (changed bool) {
	if !t.streak.last.IsZero() && now.Sub(t.streak.last) > loopProbeStaleAfter {
		t.okSince = time.Time{}
	}
	t.streak.record(now, ok)
	switch {
	case !ok:
		t.okSince = time.Time{}
	case t.okSince.IsZero():
		t.okSince = now
	}
	prev := t.status
	if t.streak.fails >= entryFailThreshold {
		t.status = biz.EntryUnhealthy
	} else if t.streak.recovered() {
		t.status = biz.EntryHealthy
	}
	return t.status != prev
}

type failoverEvent string

const (
	foNone      failoverEvent = ""
	foActivate  failoverEvent = "activate"
	foRetarget  failoverEvent = "retarget"
	foReturn    failoverEvent = "return"
	foAbandon   failoverEvent = "abandon"
	foDisabled  failoverEvent = "disabled"
	foNoReserve failoverEvent = "no-reserve"
)

type failoverInput struct {
	now     time.Time
	enabled bool
	cur     biz.EntryFailover
	hub     entryTrack // the main entry's TCP track
	healthy []int      // relays with a healthy TCP track, ascending
	relays  int        // online relays
}

// decideFailover is the auto-swap state machine (pure). A pinned failover is the owner's
// standing choice («Все через резервный вход»): never undone here — a dead target only sends the
// subscription back to the Hub lines (planEntries), and only the owner unpins.
func decideFailover(in failoverInput) (biz.EntryFailover, failoverEvent) {
	cur := in.cur
	if cur.Active && cur.Pinned {
		return cur, foNone
	}
	if cur.Active {
		switch {
		case !in.enabled:
			return biz.EntryFailover{}, foDisabled
		case in.hub.streak.recovered() && !in.hub.okSince.IsZero() && in.now.Sub(in.hub.okSince) >= entryReturnStable:
			return biz.EntryFailover{}, foReturn
		case !slices.Contains(in.healthy, cur.Target):
			if len(in.healthy) == 0 {
				return biz.EntryFailover{}, foAbandon
			}
			cur.Target = in.healthy[0]
			return cur, foRetarget
		}
		return cur, foNone
	}
	if !in.enabled || in.hub.streak.fails < entryFailThreshold {
		return cur, foNone
	}
	if len(in.healthy) == 0 {
		if in.relays > 0 {
			return cur, foNoReserve
		}
		return cur, foNone
	}
	return biz.EntryFailover{Active: true, Target: in.healthy[0], Since: in.now}, foActivate
}

type entryConfig struct {
	enabled  bool
	sni, psk string
	simUntil time.Time
}

// entryMonitor: every side effect is a field (tests drive it). Used by one goroutine only.
type entryMonitor struct {
	now          func() time.Time
	list         func(ctx context.Context) ([]biz.Entry, error)
	readFailover func(ctx context.Context) (biz.EntryFailover, error)
	saveFailover func(ctx context.Context, st biz.EntryFailover) error
	saveCheck    func(ctx context.Context, id int, kind string, c biz.EntryCheck) error
	bump         func(ctx context.Context) error
	setting      func(key string) (string, error) // "" for absent; error = failed read
	exits        func() ([]int, error)
	control      func(socksPort int) error
	tlsCheck     func(socksPort int, host string, port int, sni string) error
	udpCheck     func(socksPort int, host string, psk []byte) error
	udpControl   func(socksPort int) error
	raise        func(key string, level AlertLevel, title, details string)
	resolve      func(key, message string)
	logf         func(format string, args ...any)

	tcp, udp    map[int]*entryTrack
	rot         map[int]string // the rotation of each relay at the last run (its alerts close once on 'off')
	blindSince  time.Time
	blindRaised bool // the blind alert is up: a verdict resolves it (no resolve noise every minute)
	skipLog     logGate
}

var entryMon = &entryMonitor{
	now: time.Now, list: biz.ListMonitoredEntries, readFailover: biz.GetEntryFailover, saveFailover: biz.SetEntryFailover,
	saveCheck: biz.SaveEntryCheck, bump: biz.BumpSubscriptionVersionContext,
	setting:    func(key string) (string, error) { return settingOrEmpty(biz.GetSetting, key) },
	exits:      func() ([]int, error) { return liveExitSocksPorts(bambooMaxExits) },
	control:    exitTCPControl,
	tlsCheck:   entryTLSCheck,
	udpCheck:   func(port int, host string, psk []byte) error { _, _, err := probeBamboo(port, host, psk); return err },
	udpControl: func(port int) error { return defaultBambooProber.control(port) },
	raise:      RaiseAlert, resolve: ResolveAlert, logf: log.Printf,
}

func runEntryMonitor() { entryMon.run() }

// verifyRelayPath is provision.RelayPathVerifier (the last step of the relay recipe).
func verifyRelayPath(_ context.Context, relayIP string) error { return entryMon.verifyRelay(relayIP) }

func exitTCPControl(socksPort int) error {
	d, err := exitSocksDialer(socksPort)
	if err != nil {
		return err
	}
	return socksDialCheck(d, entryControlAddr)
}

func entryTLSCheck(socksPort int, host string, port int, sni string) error {
	d, err := exitSocksDialer(socksPort)
	if err != nil {
		return fmt.Errorf("%w: %v", errEntryProbeTool, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	conn, err := d.DialContext(ctx, "tcp", net.JoinHostPort(host, strconv.Itoa(port)))
	if err != nil {
		return err
	}
	defer conn.Close()
	// Reality answers with the dest site's certificate: the path is tested, not the PKI.
	return tls.Client(conn, &tls.Config{ServerName: sni, InsecureSkipVerify: true}).HandshakeContext(ctx)
}

func (m *entryMonitor) readConfig() (entryConfig, error) {
	v := map[string]string{}
	for _, k := range []string{"entry_failover_enabled", "reality_sni_domain", "hysteria2_obfs_password", "entry_probe_simulate_hub_fail_until"} {
		s, err := m.setting(k)
		if err != nil {
			return entryConfig{}, fmt.Errorf("настройка %s не прочитана: %w", k, err)
		}
		v[k] = s
	}
	c := entryConfig{enabled: v["entry_failover_enabled"] != "false", sni: v["reality_sni_domain"], psk: v["hysteria2_obfs_password"]}
	if c.sni == "" {
		c.sni = biz.DefaultRealitySNI
	}
	c.simUntil, _ = time.Parse(time.RFC3339, v["entry_probe_simulate_hub_fail_until"])
	return c, nil
}

// pickExit: the first live exit whose own TCP path works (the "before" control).
func (m *entryMonitor) pickExit() (int, error) {
	ports, err := m.exits()
	if err != nil {
		return 0, err
	}
	for _, p := range ports {
		if m.control(p) == nil {
			return p, nil
		}
	}
	if len(ports) == 0 {
		return 0, errNoLiveExit
	}
	return 0, fmt.Errorf("ни один выход за рубежом не прошёл контроль %s (порты %v)", entryControlAddr, ports)
}

func (m *entryMonitor) track(tracks *map[int]*entryTrack, id int, persisted string) *entryTrack {
	if *tracks == nil {
		*tracks = map[int]*entryTrack{}
	}
	t := (*tracks)[id]
	if t == nil {
		if persisted == "" {
			persisted = biz.EntryUnknown
		}
		t = &entryTrack{status: persisted} // a restart keeps the published state
		(*tracks)[id] = t
	}
	return t
}

func (m *entryMonitor) run() {
	now := m.now()
	if m.blindSince.IsZero() {
		m.blindSince = now
	}
	ctx, cancel := context.WithTimeout(context.Background(), entryDBTimeout)
	entries, err := m.list(ctx)
	var cur biz.EntryFailover
	if err == nil {
		cur, err = m.readFailover(ctx)
	}
	cancel()
	if err != nil {
		m.skip(now, "входы или состояние автоподмены не прочитаны: "+err.Error())
		return
	}
	cfg, err := m.readConfig()
	if err != nil {
		m.skip(now, err.Error())
		return
	}
	port, err := m.pickExit()
	if err != nil {
		m.skip(now, err.Error())
		return
	}
	live := map[int]bool{}
	for _, e := range entries {
		live[e.ID] = true
		m.noteRotation(e)
		m.checkTCP(now, port, e, cfg)
		if e.Kind == biz.EntryKindRelay && cfg.psk != "" {
			m.checkUDP(now, port, e, cfg.psk)
		}
	}
	for id := range m.tcp {
		if !live[id] {
			delete(m.tcp, id)
			delete(m.udp, id)
		}
	}
	for id := range m.rot {
		if !live[id] {
			delete(m.rot, id)
		}
	}
	m.failover(now, cur, cfg.enabled, entries)
}

// inRotation: the entry is published to every user ('all'; the Hub and entries of tests carry
// no rotation). Only these can be the failover target.
func inRotation(e biz.Entry) bool { return e.Rotation == "" || e.Rotation == biz.EntryRotationAll }

// quietEntry: an entry out of rotation is still checked (its health and stability stay visible
// on «Входы»), but raises no alerts — nobody's subscription has it.
func quietEntry(e biz.Entry) bool { return e.Rotation == biz.EntryRotationOff }

// relayAlertKinds are every alert the monitor raises for a relay.
var relayAlertKinds = []string{"unreachable-external", "bamboo", "unstable"}

// noteRotation closes a relay's alerts once when it leaves the rotation (also after a restart:
// Resolve of an inactive key does nothing).
func (m *entryMonitor) noteRotation(e biz.Entry) {
	if e.Kind != biz.EntryKindRelay {
		return
	}
	if m.rot == nil {
		m.rot = map[int]string{}
	}
	prev, seen := m.rot[e.ID]
	m.rot[e.ID] = e.Rotation
	if quietEntry(e) && (!seen || prev != biz.EntryRotationOff) {
		for _, k := range relayAlertKinds {
			m.resolve(relayAlertKey(e.ID, k), fmt.Sprintf("Вход «%s» (%s) выведен из ротации — тревоги по нему не приходят, пока он не вернётся.", e.Name, e.Address))
		}
	}
}

// relayXHTTPTries: a relay's :2443 (Дуб) is its DNAT path into the GRE tunnel — the one TCP path
// that has no backup when the tunnel is down (443/80 fall back to the Hub's public address in the
// relay's nginx). One retry on a new connection: one lost flow is not a dead path.
const relayXHTTPTries = 2

// relayTLSCheck: :443, then for a relay :2443 too — an entry whose tunnel is down answers on 443
// through the backup but not on 2443, and must leave the subscription all the same.
func (m *entryMonitor) relayTLSCheck(port int, e biz.Entry, sni string) error {
	if err := m.tlsCheck(port, e.Address, 443, sni); err != nil || e.Kind != biz.EntryKindRelay {
		return err
	}
	var err error
	for range relayXHTTPTries {
		if err = m.tlsCheck(port, e.Address, 2443, sni); err == nil {
			return nil
		}
	}
	return fmt.Errorf("TLS :2443 (Дуб, через туннель до Hub): %w", err)
}

func (m *entryMonitor) checkTCP(now time.Time, port int, e biz.Entry, cfg entryConfig) {
	err := m.relayTLSCheck(port, e, cfg.sni)
	v := decideEntry(err, func() error { return m.control(port) })
	if e.ID == 0 && now.Before(cfg.simUntil) {
		v, err = entryFail, fmt.Errorf("учебная авария до %s (админка → Входы)", cfg.simUntil.In(mskZone).Format("15:04"))
	}
	if v == entrySkip {
		m.skip(now, fmt.Sprintf("вход %s не проверен через выход :%d: %v", e.Address, port, err))
		return
	}
	m.verdict(now)
	t := m.track(&m.tcp, e.ID, e.Health.External)
	m.publish(e, "ext", t, t.record(now, v == entryOK), now, err)
	if e.Kind != biz.EntryKindRelay || quietEntry(e) {
		return // the Hub's own alerts come from the loop probe (entry-unreachable-external); an entry out of rotation raises none
	}
	key := relayAlertKey(e.ID, "unreachable-external")
	switch {
	case t.streak.fails >= entryFailThreshold:
		m.raise(key, AlertWarn, "Резервный вход недоступен снаружи", fmt.Sprintf(
			"Резервный вход «%s» (%s): %d проверок подряд через выход за рубежом :%d не прошли, строки «· резерв» этого входа убраны из подписки.\nПоследняя ошибка: %v\nНа ретрансляторе: systemctl status heyearth-tunnel heyearth-relay; ip -d link show he-hub; nft list table ip heyearth_relay. На Hub: systemctl status heyearth-tunnel-r%d.",
			e.Name, e.Address, t.streak.fails, port, err, e.ID))
	case t.streak.recovered():
		m.resolve(key, fmt.Sprintf("Резервный вход «%s» (%s) снова доступен снаружи.", e.Name, e.Address))
	}
}

func (m *entryMonitor) checkUDP(now time.Time, port int, e biz.Entry, psk string) {
	err := m.udpCheck(port, e.Address, []byte(psk))
	var cerr error
	if bambooNeedsControl(err) {
		cerr = m.udpControl(port)
	}
	verdict := decideBamboo(err, cerr)
	if verdict == bambooSkip {
		m.skip(now, fmt.Sprintf("Бамбук через вход %s не проверен: %v (контроль UDP: %v)", e.Address, err, cerr))
		return
	}
	t := m.track(&m.udp, e.ID, e.Health.UDP)
	m.publish(e, "udp", t, t.record(now, verdict == bambooOK), now, err)
	if quietEntry(e) {
		return
	}
	key := relayAlertKey(e.ID, "bamboo")
	switch {
	case t.streak.fails >= entryFailThreshold:
		m.raise(key, AlertWarn, "Бамбук через резервный вход не отвечает", fmt.Sprintf(
			"Резервный вход «%s» (%s): %d проверок подряд Бамбук (UDP %d-%d) не ответил, строки «🎋 … · резерв» убраны из подписки.\nПоследняя ошибка: %v",
			e.Name, e.Address, t.streak.fails, bambooHopPortMin, bambooHopPortMax, err))
	case t.streak.recovered():
		m.resolve(key, fmt.Sprintf("Бамбук через резервный вход «%s» снова отвечает.", e.Name))
	}
}

// publish persists the published status; a changed reserve bumps subscription_version (apps
// refetch their lines). A failed write is retried by the next verdict.
func (m *entryMonitor) publish(e biz.Entry, kind string, t *entryTrack, changed bool, now time.Time, cause error) {
	t.dirty = t.dirty || changed
	detail := ""
	if cause != nil {
		detail = cause.Error()
	}
	ctx, cancel := context.WithTimeout(context.Background(), entryDBTimeout)
	defer cancel()
	if err := m.saveCheck(ctx, e.ID, kind, biz.EntryCheck{Status: t.status, At: now, Detail: detail}); err != nil {
		m.logf("[entries] вход %d (%s): состояние не сохранено, повтор при следующей проверке: %v", e.ID, kind, err)
		return
	}
	if t.dirty && e.Kind == biz.EntryKindRelay && !quietEntry(e) { // nobody's subscription has an entry out of rotation
		if err := m.bump(ctx); err != nil {
			m.logf("[entries] subscription_version не увеличена: %v", err)
		}
	}
	t.dirty = false
}

func (m *entryMonitor) failover(now time.Time, cur biz.EntryFailover, enabled bool, entries []biz.Entry) {
	hub := m.track(&m.tcp, 0, biz.EntryUnknown)
	in := failoverInput{now: now, enabled: enabled, cur: cur, hub: *hub}
	names := map[int]string{}
	for _, e := range entries {
		if e.Kind != biz.EntryKindRelay || !inRotation(e) {
			continue // a canary or an entry out of rotation never carries everyone's main lines
		}
		in.relays++
		names[e.ID] = fmt.Sprintf("«%s» (%s)", e.Name, e.Address)
		if t := m.tcp[e.ID]; t != nil && t.status == biz.EntryHealthy {
			in.healthy = append(in.healthy, e.ID)
		}
	}
	slices.Sort(in.healthy)
	name := func(id int) string {
		if n, ok := names[id]; ok {
			return n
		}
		return fmt.Sprintf("вход %d", id)
	}
	next, ev := decideFailover(in)
	switch ev {
	case foNone:
		if hub.status == biz.EntryHealthy {
			m.resolve(entryNoReserveAlert, "Основной вход снова доступен снаружи.")
		}
		return
	case foNoReserve:
		m.raise(entryNoReserveAlert, AlertWarn, "Основной вход недоступен, резерва нет", fmt.Sprintf(
			"Основной вход %d проверок подряд недоступен снаружи, но ни один резервный вход не здоров — автоподмена невозможна. Админка → Входы.", hub.streak.fails))
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), entryDBTimeout)
	defer cancel()
	if err := m.saveFailover(ctx, next); err != nil {
		m.logf("[entries] автоподмена (%s) не сохранена, повтор через минуту: %v", ev, err)
		return // no alert for a state nobody sees: the next run decides again
	}
	if err := m.bump(ctx); err != nil {
		m.logf("[entries] subscription_version не увеличена: %v", err)
	}
	m.logf("[entries] автоподмена: %s; было %+v, стало %+v", ev, cur, next)
	switch ev {
	case foActivate:
		m.resolve(entryNoReserveAlert, "Включена автоподмена на резервный вход.")
		m.raise(entryFailoverAlertKey(next.Target), AlertCritical, "Основной вход недоступен — включена автоподмена", fmt.Sprintf(
			"Основной вход %d проверок подряд недоступен снаружи. Основные строки подписки теперь ведут на резервный вход %s, подписка обновляется раз в час. Возврат — после 30 мин стабильной работы основного входа. Выключить: админка → Входы → «Автоподмена».",
			hub.streak.fails, name(next.Target)))
	case foRetarget:
		m.resolve(entryFailoverAlertKey(cur.Target), fmt.Sprintf("Резервный вход %s перестал отвечать — автоподмена переключена на %s.", name(cur.Target), name(next.Target)))
		m.raise(entryFailoverAlertKey(next.Target), AlertCritical, "Автоподмена переключена на другой резервный вход",
			fmt.Sprintf("Основные строки подписки теперь ведут на %s.", name(next.Target)))
	case foReturn:
		m.resolve(entryFailoverAlertKey(cur.Target), fmt.Sprintf("Основной вход стабильно доступен %v — основные строки подписки вернулись на него.",
			now.Sub(hub.okSince).Round(time.Minute)))
	case foAbandon:
		m.resolve(entryFailoverAlertKey(cur.Target), fmt.Sprintf("Резервный вход %s тоже перестал отвечать — основные строки вернулись на основной вход.", name(cur.Target)))
		m.raise(entryNoReserveAlert, AlertCritical, "Основной и резервные входы недоступны",
			"Основной вход недоступен снаружи, здоровых резервных входов нет. Пользователи не подключатся, пока Hub или вход не заработает.")
	case foDisabled:
		m.resolve(entryFailoverAlertKey(cur.Target), "Автоподмена выключена в админке — основные строки вернулись на основной вход.")
	}
}

// verdict: the probe really tested an entry; skip: it could not — after entryBlindAfter of only
// skips the owner is told that the entries are not checked (and no failover can happen).
func (m *entryMonitor) verdict(now time.Time) {
	m.blindSince = now
	if m.blindRaised {
		m.blindRaised = false
		m.resolve(entryBlindAlert, "Проверка входов снова даёт результат.")
	}
}

func (m *entryMonitor) skip(now time.Time, reason string) {
	if m.skipLog.allow(now, time.Hour) {
		m.logf("[entries] проверка пропущена: %s (не чаще раза в час)", reason)
	}
	if blind := now.Sub(m.blindSince); blind > entryBlindAfter {
		m.blindRaised = true
		m.raise(entryBlindAlert, AlertWarn, "Входы не проверяются", fmt.Sprintf(
			"Уже %v проверка входов снаружи не даёт результата: блокировка основного входа не будет замечена, автоподмена не сработает.\nПоследняя причина: %s",
			blind.Round(time.Minute), reason))
	}
}

// verifyRelay: the provisioning check — TLS to :443 and :2443, then Bamboo through a hop port
// (when deployed). No usable exit = error: the job fails and can be resumed later.
func (m *entryMonitor) verifyRelay(relayIP string) error {
	cfg, err := m.readConfig()
	if err != nil {
		return err
	}
	port, err := m.pickExit()
	if err != nil {
		return fmt.Errorf("проверка невозможна: %w", err)
	}
	for _, p := range []int{443, 2443} {
		if err := m.tlsCheck(port, relayIP, p, cfg.sni); err != nil {
			return fmt.Errorf("TLS к %s:%d (SNI %s) через выход :%d: %w", relayIP, p, cfg.sni, port, err)
		}
	}
	if cfg.psk == "" {
		return nil // Bamboo is not deployed on the Hub
	}
	uerr := m.udpCheck(port, relayIP, []byte(cfg.psk))
	var cerr error
	if bambooNeedsControl(uerr) {
		cerr = m.udpControl(port)
	}
	switch decideBamboo(uerr, cerr) {
	case bambooFail:
		return fmt.Errorf("Бамбук через %s не ответил: %w", relayIP, uerr)
	case bambooSkip:
		m.logf("[relay-verify] %s: UDP не проверен (выход не передаёт UDP): %v / %v", relayIP, uerr, cerr)
	}
	return nil
}
