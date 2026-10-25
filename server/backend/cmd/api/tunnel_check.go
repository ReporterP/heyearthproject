package main

import (
	"context"
	"fmt"
	"time"

	"github.com/heyearth/backend/internal/admin"
	"github.com/heyearth/backend/internal/biz"
)

// checkExitTunnel is admin.TunnelCheckFn: the tunnel monitor's own probes (exitPathProber) on
// demand — generate_204 through the Hub's local SOCKS of each path, Hysteria via
// hysteria2-client-N (127.0.0.1:1088+N), TCP via xray's probe inbound (127.0.0.1:12000+N).
// Read-only: one SELECT of the exit; no status, alert, path view, monitor memory or config is
// touched, nothing on the node or the Hub is restarted.
func checkExitTunnel(ctx context.Context, serverID int) (admin.TunnelCheckResult, error) {
	node, err := biz.GetExitNodeTunnel(ctx, serverID)
	if err != nil {
		return admin.TunnelCheckResult{}, fmt.Errorf("чтение exit-ноды #%d: %w", serverID, err)
	}
	return tunnelCheckResult(node, exitPathProber(node), time.Now()), nil
}

// tunnelCheckResult words one probe of both paths for the owner. No secret of the row
// (tunnel_uuid, the Hysteria password) can reach it: only the probe errors and field names.
func tunnelCheckResult(node biz.ExitNodeTunnel, p exitPathProbe, now time.Time) admin.TunnelCheckResult {
	hy := tunnelPathCheck("hy", "Hysteria (UDP)", p.hy, exitHyAbsentReason(node))
	vr := tunnelPathCheck("vr", "TCP (VLESS Reality)", p.vr, exitVRAbsentReason(node))
	summary := tunnelCheckSummary(hy.Status, vr.Status)
	if node.LifecycleState != "online" {
		// Out of the routing: xray has no probe inbound for it (buildExitConfig) and its
		// hysteria2-client may be stopped, and no user traffic goes through it.
		state := node.LifecycleState
		if state == "" {
			state = "состояние неизвестно"
		}
		notInWork := "exit не в работе (" + state + "): "
		if hy.Status == "fail" {
			hy.Error = notInWork + "клиент Hysteria на Hub может быть не запущен. " + hy.Error
		}
		if vr.Status == "fail" {
			vr.Error = notInWork + "проверочного входа в xray нет. " + vr.Error
		}
		summary = "Exit не в работе (" + state + ") и в маршрутизацию не входит. Hysteria — " +
			tunnelPathWord(hy.Status) + ", TCP — " + tunnelPathWord(vr.Status) + "."
	}
	return admin.TunnelCheckResult{
		ServerID: node.ID, CheckedAt: now.UTC().Format(time.RFC3339),
		Summary: summary, Paths: []admin.TunnelPathCheck{hy, vr},
	}
}

func tunnelPathCheck(key, name string, pp pathProbe, absentWhy string) admin.TunnelPathCheck {
	c := admin.TunnelPathCheck{Path: key, Name: name}
	switch {
	case !pp.present:
		c.Status, c.Error = "absent", absentWhy
	case pp.ok:
		c.Status, c.RTTms = "ok", pp.rttMs
	default:
		c.Status, c.Error = "fail", pp.err
	}
	return c
}

// exitHyAbsentReason: why exitHasHyPath says no.
func exitHyAbsentReason(n biz.ExitNodeTunnel) string {
	if !exitIDValid(n.ID) {
		return fmt.Sprintf("пути Hysteria нет: id вне 1..%d", exitMaxID)
	}
	return fmt.Sprintf("пути Hysteria нет: протокол туннеля %q, не hysteria2", n.TunnelProtocol)
}

// exitVRAbsentReason: why exitHasVRPath says no, in the generator's words (exitVRAbsentWhy).
func exitVRAbsentReason(n biz.ExitNodeTunnel) string {
	return "TCP-пути нет: " + exitVRAbsentWhy(n)
}

func tunnelPathWord(status string) string {
	switch status {
	case "ok":
		return "работает"
	case "absent":
		return "нет"
	}
	return "не работает"
}

func tunnelCheckSummary(hy, vr string) string {
	switch {
	case hy == "ok" && vr == "ok":
		return "Оба пути работают."
	case hy == "ok" && vr == "absent":
		return "Hysteria работает. Запасного TCP-пути нет: при блокировке UDP exit отключится."
	case hy == "ok":
		return "Hysteria работает, запасной TCP-путь не работает: при блокировке UDP exit отключится."
	case vr == "ok" && hy == "absent":
		return "Работает TCP-путь (пути Hysteria у этого exit нет)."
	case vr == "ok":
		return "Hysteria не проходит — трафик exit идёт по запасному TCP-пути, Бамбук через этот exit не работает."
	case hy == "absent" && vr == "absent":
		return "У exit нет ни одного пути: нужен протокол hysteria2 или учётка VLESS Reality."
	default:
		return "Ни один путь не работает: exit недоступен, его трафик идёт через другие exit."
	}
}
