package main

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/heyearth/backend/internal/biz"
)

func TestTunnelCheckResult(t *testing.T) {
	now := time.Date(2026, 10, 13, 9, 0, 0, 0, time.UTC)
	node := biz.ExitNodeTunnel{ID: 2, TunnelProtocol: "hysteria2", LifecycleState: "online"}
	ok := func(ms int) pathProbe { return pathProbe{present: true, ok: true, rttMs: ms} }
	fail := pathProbe{present: true, err: "tcp-reality exit #2 via 127.0.0.1:12002: EOF"}
	cases := []struct {
		name    string
		node    biz.ExitNodeTunnel
		p       exitPathProbe
		hy, vr  string
		summary string
	}{
		{"both", node, exitPathProbe{ok(143), ok(90)}, "ok", "ok", "Оба пути работают"},
		{"UDP blocked", node, exitPathProbe{fail, ok(90)}, "fail", "ok", "по запасному TCP-пути"},
		{"no fallback", node, exitPathProbe{ok(143), fail}, "ok", "fail", "запасной TCP-путь не работает"},
		{"no VLESS credentials", node, exitPathProbe{ok(143), pathProbe{}}, "ok", "absent", "Запасного TCP-пути нет"},
		{"dead", node, exitPathProbe{fail, fail}, "fail", "fail", "Ни один путь не работает"},
	}
	for _, tc := range cases {
		r := tunnelCheckResult(tc.node, tc.p, now)
		if r.ServerID != 2 || r.CheckedAt != "2026-10-13T09:00:00Z" || len(r.Paths) != 2 ||
			r.Paths[0].Status != tc.hy || r.Paths[1].Status != tc.vr || !strings.Contains(r.Summary, tc.summary) {
			t.Errorf("%s: %+v", tc.name, r)
		}
	}
	off := node
	off.LifecycleState = "offline"
	if r := tunnelCheckResult(off, exitPathProbe{fail, fail}, now); !strings.Contains(r.Paths[1].Error, "не в работе") {
		t.Errorf("an offline exit has no probe inbound in xray: the TCP error must say so: %+v", r.Paths[1])
	}
}

// An exit outside the routing carries no user traffic: the summary must not claim where "its
// traffic" goes, and a dead Hysteria path may simply be a stopped client.
func TestTunnelCheckResultNotOnline(t *testing.T) {
	now := time.Date(2026, 10, 13, 9, 0, 0, 0, time.UTC)
	fail := pathProbe{present: true, err: "hysteria2 exit #3 via 127.0.0.1:1091: connect: connection refused"}
	ok := pathProbe{present: true, ok: true, rttMs: 80}
	for _, p := range []exitPathProbe{{fail, fail}, {fail, ok}, {ok, fail}, {ok, ok}} {
		r := tunnelCheckResult(biz.ExitNodeTunnel{ID: 3, TunnelProtocol: "hysteria2", LifecycleState: "pending"}, p, now)
		if !strings.Contains(r.Summary, "не в работе (pending)") || strings.Contains(r.Summary, "трафик exit") ||
			strings.Contains(r.Summary, "через другие exit") {
			t.Errorf("%+v: summary %q", p, r.Summary)
		}
		if p.hy.ok != (r.Paths[0].Status == "ok") || (!p.hy.ok && !strings.Contains(r.Paths[0].Error, "не в работе")) {
			t.Errorf("%+v: hy %+v", p, r.Paths[0])
		}
	}
}

// An absent path says why, with the generator's own reason (no values, only field names).
func TestTunnelCheckResultAbsentReasons(t *testing.T) {
	now := time.Date(2026, 10, 13, 9, 0, 0, 0, time.UTC)
	r := tunnelCheckResult(biz.ExitNodeTunnel{ID: 4, TunnelProtocol: "hysteria2", LifecycleState: "online"},
		exitPathProbe{hy: pathProbe{present: true, ok: true, rttMs: 70}}, now)
	if r.Paths[1].Status != "absent" || !strings.Contains(r.Paths[1].Error, "нет данных Reality") {
		t.Errorf("vr: %+v", r.Paths[1])
	}
	r = tunnelCheckResult(biz.ExitNodeTunnel{ID: 4, TunnelProtocol: "vless", LifecycleState: "online"},
		exitPathProbe{vr: pathProbe{present: true, ok: true, rttMs: 70}}, now)
	if r.Paths[0].Status != "absent" || !strings.Contains(r.Paths[0].Error, `"vless"`) ||
		!strings.Contains(r.Summary, "пути Hysteria у этого exit нет") {
		t.Errorf("hy: %+v, summary %q", r.Paths[0], r.Summary)
	}
	r = tunnelCheckResult(biz.ExitNodeTunnel{ID: 600, TunnelProtocol: "hysteria2", LifecycleState: "online"}, exitPathProbe{}, now)
	if !strings.Contains(r.Paths[0].Error, "id вне 1..511") || !strings.Contains(r.Paths[1].Error, "id вне 1..511") {
		t.Errorf("id 600: %+v", r.Paths)
	}
}

// «Проверить туннель» end to end with a scripted database: exactly one read of the exit, the
// monitor's prober, and nothing written — no SQL write, no alert, no path view, no monitor memory
// (Review Focus 5: no secret of the row in the answer either).
func TestCheckExitTunnelIsReadOnly(t *testing.T) {
	const id = 47
	const uuid, hyPass = "0f9e8d7c-6b5a-4433-8211-00aa11bb22cc", "hy-secret-password"
	row := []driver.Value{int64(id), "Рига", "203.0.113.47", "hysteria2", int64(0), uuid, "pubkey-not-checked-here",
		"ab12", "", "", hyPass, "healthy", int64(3), "online"}
	exitQuery := func(rows ...[]driver.Value) []scriptQuery {
		return []scriptQuery{{name: "exit", match: "FROM servers WHERE id = $1 AND is_exit_node = TRUE",
			cols: []string{"id", "name", "ip", "proto", "port", "uuid", "pbk", "sid", "sni", "ssm", "ssp", "status", "w", "lc"}, rows: rows}}
	}
	ah := withExitAlertHarness(t)
	oldProber := exitPathProber
	t.Cleanup(func() { exitPathProber = oldProber })
	var probed []biz.ExitNodeTunnel
	exitPathProber = func(n biz.ExitNodeTunnel) exitPathProbe {
		probed = append(probed, n)
		return exitPathProbe{
			hy: pathProbe{present: true, err: "hysteria2 exit #47 via 127.0.0.1:1135: timeout after 5s"},
			vr: pathProbe{present: true, ok: true, rttMs: 95},
		}
	}

	s := &scriptDB{queries: exitQuery(row), seen: map[string]bool{}}
	useScriptDB(t, s)
	res, err := checkExitTunnel(context.Background(), id)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if len(probed) != 1 || probed[0].ID != id || probed[0].LifecycleState != "online" || probed[0].TunnelUUID != uuid {
		t.Fatalf("the prober must get the exit as read: %+v", probed)
	}
	if res.ServerID != id || res.Paths[0].Status != "fail" || res.Paths[1].Status != "ok" || res.Paths[1].RTTms != 95 ||
		!strings.Contains(res.Summary, "по запасному TCP-пути") {
		t.Fatalf("result: %+v", res)
	}
	if body, _ := json.Marshal(res); strings.Contains(string(body), uuid) || strings.Contains(string(body), hyPass) {
		t.Fatalf("a secret of the exit row leaked into the answer: %s", body)
	}
	if len(s.writes) != 0 || len(s.unknown) != 0 || !s.seen["exit"] {
		t.Fatalf("one read and nothing else: writes %q, other queries %q", s.writes, s.unknown)
	}
	if len(ah.events) != 0 || len(ah.sent) != 0 {
		t.Fatalf("the check touched alerts: %q", alertLog(ah))
	}
	if _, ok := biz.GetExitPaths(id); ok {
		t.Fatal("the check published a path view: that is the monitor's")
	}
	if _, ok := exitHealthByID.Load(id); ok {
		t.Fatal("the check created monitor memory")
	}

	// Not an exit / unknown id: sql.ErrNoRows (404), nothing probed.
	probed = nil
	useScriptDB(t, &scriptDB{queries: exitQuery(), seen: map[string]bool{}})
	if _, err := checkExitTunnel(context.Background(), id); !errors.Is(err, sql.ErrNoRows) || len(probed) != 0 {
		t.Fatalf("unknown exit: err %v, probed %d", err, len(probed))
	}
	// The read itself fails: an error that is not ErrNoRows (502), nothing probed.
	useScriptDB(t, &scriptDB{queries: exitQuery(row), fail: "exit", seen: map[string]bool{}})
	if _, err := checkExitTunnel(context.Background(), id); err == nil || errors.Is(err, sql.ErrNoRows) || len(probed) != 0 {
		t.Fatalf("db failure: err %v, probed %d", err, len(probed))
	}
}
