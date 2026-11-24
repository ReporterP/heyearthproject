package main

// The Hub's console actions and the shared K0 ones (plan docs/plan/stability-operations-2.md, C3):
//
//	hub.status           K0 servers.read    the services' state, nothing changes
//	probes.run           K0 settings.write  a synthetic run (the run is the check after)
//	backup.run           K0 settings.write  heyearth-backup.service; after: a fresh backup file
//	hub.reload-nginx     K0 settings.write  before: nginx -t; reload; after: the site answers 200
//	hub.restart-api      K1 settings.write  a restart 2 s later; the next process closes it by its start run
//	hub.restart-tunnels  K2 ops.disrupt     an exit's tunnel clients; after: the tunnel probe
//	hub.restart-bamboo   K2 ops.disrupt     hysteria2-server; after: active + a synthetic run
//	hub.restart-xray     K3 ops.disrupt     word XRAY; cause «вручную: <who>»; after: a synthetic run
//	user.disconnect-now  K3 ops.disrupt     word XRAY; only when a blocked user is online; after: they are not back

import (
	"context"
	"crypto/tls"
	"database/sql"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/heyearth/backend/internal/biz"
	"github.com/heyearth/backend/internal/db"
)

type hubDeps struct {
	exec            func(ctx context.Context, name string, args ...string) (string, error)
	restartXray     func() error
	xrayRestarted   func(cause string) // the K3 restart line and the storm counter
	restartBamboo   func() error
	bambooRestarted func(by string)
	synth           func(ctx context.Context) (state, summary string, err error)
	online          func(now time.Time) onlineCounts
	exit            func(ctx context.Context, id int) (biz.ExitNodeTunnel, error)
	probeExit       func(node biz.ExitNodeTunnel) exitPathProbe
	hasHy, hasVR    func(node biz.ExitNodeTunnel) bool
	newestBackup    func() (time.Time, error)
	siteOK          func(ctx context.Context) error
	startRunAfter   func(ctx context.Context, t time.Time) (state, summary string, found bool, err error)
	pending         func() []string // blocked users seen online, waiting for a restart
	resetRevoked    func()
	seenSince       func(user string, t time.Time) (seen, fresh bool)
	sleep           func(time.Duration)
}

const (
	hubResumeWait      = 6 * time.Minute
	hubResumeEvery     = 15 * time.Second
	hubBackupFreshness = 30 * time.Minute
	hubOnlineWait      = 3 * time.Minute
)

func synthCheck(ctx context.Context, d hubDeps) opsCheck {
	state, summary, err := d.synth(ctx)
	if err != nil {
		return opsCheck{Name: "прогон синтетики", Detail: err.Error()}
	}
	return opsCheck{Name: "прогон синтетики", OK: state == "pass", Detail: state + ": " + summary}
}

func affectedBy(d hubDeps, pick func(onlineCounts) (int, bool), note string) func(context.Context, string) (int, int, string, error) {
	return func(context.Context, string) (int, int, string, error) {
		n, stale := pick(d.online(time.Now()))
		if stale {
			return -1, 0, note, nil
		}
		return n, 0, note, nil
	}
}

func onXray(c onlineCounts) (int, bool)   { return c.Xray, c.XrayStale }
func onBamboo(c onlineCounts) (int, bool) { return c.Bamboo, c.BambooStale }

func targetExit(ctx context.Context, d hubDeps, target string) (biz.ExitNodeTunnel, error) {
	id, err := strconv.Atoi(target)
	if err != nil {
		return biz.ExitNodeTunnel{}, err
	}
	return d.exit(ctx, id)
}

func buildHubActions(d hubDeps) []opsAction {
	var discMu sync.Mutex
	var discUsers []string
	var discAt time.Time

	return []opsAction{
		{ID: "hub.status", Title: "Состояние хаба", Impact: biz.ImpactK0, Perm: "servers.read",
			IfFails: "ничего не меняется",
			Run: func(ctx context.Context, _, _ string, progress func(string)) error {
				units := []string{"xray", "hysteria2-server", "nginx", "heyearth-api", "postgresql"}
				out, _ := d.exec(ctx, "systemctl", append([]string{"is-active"}, units...)...)
				states := strings.Fields(out)
				for i, u := range units {
					st := "?"
					if i < len(states) {
						st = states[i]
					}
					progress(u + ": " + st)
				}
				return nil
			}},
		{ID: "probes.run", Title: "Прогон синтетики", Impact: biz.ImpactK0, Perm: "settings.write",
			PostNames: []string{"прогон синтетики"}, IfFails: "ничего не меняется: прогон показывает, какие строки подписки не работают",
			Run: func(_ context.Context, _, _ string, progress func(string)) error {
				progress("внеочередной прогон через выход")
				return nil
			},
			Post: func(ctx context.Context, _ string) ([]opsCheck, error) { return []opsCheck{synthCheck(ctx, d)}, nil }},
		{ID: "backup.run", Title: "Сделать бэкап", Impact: biz.ImpactK0, Perm: "settings.write",
			PostNames: []string{"свежий файл бэкапа"}, IfFails: "бэкапа нет: смотрите journalctl -u heyearth-backup",
			Run: func(ctx context.Context, _, _ string, progress func(string)) error {
				progress("systemctl start heyearth-backup.service")
				if out, err := d.exec(ctx, "systemctl", "start", "heyearth-backup.service"); err != nil {
					return fmt.Errorf("%v: %s", err, lastLine(out))
				}
				return nil
			},
			Post: func(context.Context, string) ([]opsCheck, error) {
				t, err := d.newestBackup()
				if err != nil {
					return nil, err
				}
				age := time.Since(t)
				return []opsCheck{{Name: "свежий файл бэкапа", OK: !t.IsZero() && age <= hubBackupFreshness,
					Detail: "последний " + shortAge(age) + " назад"}}, nil
			}},
		{ID: "hub.reload-nginx", Title: "Перечитать nginx", Impact: biz.ImpactK0, Perm: "settings.write",
			PostNames: []string{"сайт отвечает 200"}, IfFails: "сайт не отвечает: проверьте последний изменённый конфиг nginx",
			Pre: func(ctx context.Context, _ string) ([]opsCheck, error) {
				out, err := d.exec(ctx, "nginx", "-t")
				if err != nil {
					return []opsCheck{{Name: "nginx -t", Detail: lastLine(out)}}, nil
				}
				return []opsCheck{{Name: "nginx -t", OK: true}}, nil
			},
			Run: func(ctx context.Context, _, _ string, progress func(string)) error {
				progress("systemctl reload nginx")
				if out, err := d.exec(ctx, "systemctl", "reload", "nginx"); err != nil {
					return fmt.Errorf("%v: %s", err, lastLine(out))
				}
				return nil
			},
			Post: func(ctx context.Context, _ string) ([]opsCheck, error) {
				if err := d.siteOK(ctx); err != nil {
					return []opsCheck{{Name: "сайт отвечает 200", Detail: err.Error()}}, nil
				}
				return []opsCheck{{Name: "сайт отвечает 200", OK: true}}, nil
			}},
		{ID: "hub.restart-api", Title: "Перезапустить API", Impact: biz.ImpactK1, Perm: "settings.write", MinGap: 5 * time.Minute,
			HandOver: true, PostNames: []string{"прогон синтетики «start» нового процесса"},
			IfFails: "API запустилось, но подписка не проходит: откатите сборку (docs/runbooks/deploy-and-verify.md)",
			Run: func(ctx context.Context, _, _ string, progress func(string)) error {
				progress("перезапуск через 2 с; действие закроет следующий процесс")
				d.sleep(2 * time.Second)
				if out, err := d.exec(ctx, "systemctl", "restart", "--no-block", "heyearth-api"); err != nil {
					return fmt.Errorf("%v: %s", err, lastLine(out))
				}
				return errOpsHandOver
			},
			Resume: func(ctx context.Context, r opsRun) ([]opsCheck, error) {
				since := time.Now()
				if r.StartedAt != nil {
					since = *r.StartedAt
				}
				for waited := time.Duration(0); waited <= hubResumeWait; waited += hubResumeEvery {
					state, summary, found, err := d.startRunAfter(ctx, since)
					if err == nil && found {
						return []opsCheck{{Name: "прогон синтетики «start»", OK: state == "pass", Detail: state + ": " + summary}}, nil
					}
					d.sleep(hubResumeEvery)
				}
				return []opsCheck{{Name: "прогон синтетики «start»", Detail: "прогона не было за 6 минут"}}, nil
			}},
		{ID: "hub.restart-tunnels", Title: "Перезапустить туннели к выходу", Impact: biz.ImpactK2, Perm: opsPermDisrupt,
			Target: "exit", MinGap: 10 * time.Minute, PostNames: []string{"проба туннеля"},
			IfFails: "туннель не поднялся: выход недоступен с хаба — смотрите карточку выхода",
			Pre: func(ctx context.Context, target string) ([]opsCheck, error) {
				if _, err := targetExit(ctx, d, target); err != nil {
					return []opsCheck{{Name: "выход есть", Detail: err.Error()}}, nil
				}
				return []opsCheck{{Name: "выход есть", OK: true}}, nil
			},
			Affected: func(ctx context.Context, target string) (int, int, string, error) {
				c := d.online(time.Now())
				if c.XrayStale {
					return -1, 0, "", nil
				}
				return c.Xray, 0, fmt.Sprintf("до %d: точное число по выходу неизвестно", c.Xray), nil
			},
			Run: func(ctx context.Context, target, _ string, progress func(string)) error {
				node, err := targetExit(ctx, d, target)
				if err != nil {
					return err
				}
				var units []string
				if d.hasHy(node) {
					units = append(units, "hysteria2-client-"+target)
				}
				if d.hasVR(node) {
					units = append(units, "heyearth-stls-client-"+target)
				}
				if len(units) == 0 {
					return errors.New("у выхода нет туннелей")
				}
				progress("systemctl restart " + strings.Join(units, " "))
				if out, err := d.exec(ctx, "systemctl", append([]string{"restart"}, units...)...); err != nil {
					return fmt.Errorf("%v: %s", err, lastLine(out))
				}
				return nil
			},
			Post: func(ctx context.Context, target string) ([]opsCheck, error) {
				node, err := targetExit(ctx, d, target)
				if err != nil {
					return nil, err
				}
				p := d.probeExit(node)
				var out []opsCheck
				for _, path := range []struct {
					name string
					pp   pathProbe
				}{{"туннель Hysteria", p.hy}, {"туннель TCP", p.vr}} {
					if path.pp.present {
						out = append(out, opsCheck{Name: path.name, OK: path.pp.ok, Detail: path.pp.err})
					}
				}
				return out, nil
			}},
		{ID: "hub.restart-bamboo", Title: "Перезапустить сервер Бамбука", Impact: biz.ImpactK2, Perm: opsPermDisrupt,
			MinGap: 10 * time.Minute, PostNames: []string{"hysteria2-server активен", "прогон синтетики"},
			IfFails:  "Бамбук не работает: смотрите journalctl -u hysteria2-server",
			Affected: affectedBy(d, onBamboo, "в сети на Бамбуке"),
			Run: func(_ context.Context, _, by string, progress func(string)) error {
				progress("systemctl restart hysteria2-server")
				if err := d.restartBamboo(); err != nil {
					return err
				}
				d.bambooRestarted(by)
				return nil
			},
			Post: func(ctx context.Context, _ string) ([]opsCheck, error) {
				out, err := d.exec(ctx, "systemctl", "is-active", "hysteria2-server")
				active := opsCheck{Name: "hysteria2-server активен", OK: err == nil, Detail: strings.TrimSpace(out)}
				return []opsCheck{active, synthCheck(ctx, d)}, nil
			}},
		{ID: "hub.restart-xray", Title: "Перезапустить xray", Impact: biz.ImpactK3, Perm: opsPermDisrupt, Confirm: "XRAY",
			MinGap: 30 * time.Minute, PostNames: []string{"прогон синтетики"},
			IfFails:  "xray запущен, но подписка не проходит: смотрите journalctl -u xray и последний конфиг",
			Affected: affectedBy(d, onXray, "в сети через xray"),
			Run: func(_ context.Context, _, by string, progress func(string)) error {
				progress("systemctl restart xray")
				if err := d.restartXray(); err != nil {
					return err
				}
				d.xrayRestarted("вручную: " + by)
				return nil
			},
			Post: func(ctx context.Context, _ string) ([]opsCheck, error) { return []opsCheck{synthCheck(ctx, d)}, nil }},
		{ID: "user.disconnect-now", Title: "Отключить заблокированных сейчас", Impact: biz.ImpactK3, Perm: opsPermDisrupt,
			Confirm: "XRAY", MinGap: 30 * time.Minute, PostNames: []string{"отключённые не вернулись", "прогон синтетики"},
			IfFails: "кто-то из отключённых снова в сети: проверьте, что его доступ закрыт (тариф, блокировка)",
			Pre: func(context.Context, string) ([]opsCheck, error) {
				n := len(d.pending())
				return []opsCheck{{Name: "заблокированные в сети", OK: n > 0, Detail: fmt.Sprintf("%d", n)}}, nil
			},
			Affected: affectedBy(d, onXray, "перезапуск xray рвёт всех, кто в сети"),
			Run: func(_ context.Context, _, by string, progress func(string)) error {
				users := d.pending()
				progress(fmt.Sprintf("отключить %d; systemctl restart xray", len(users)))
				if err := d.restartXray(); err != nil {
					return err
				}
				d.xrayRestarted("отключить заблокированных сейчас: " + by)
				d.resetRevoked()
				discMu.Lock()
				discUsers, discAt = users, time.Now()
				discMu.Unlock()
				return nil
			},
			Post: func(ctx context.Context, _ string) ([]opsCheck, error) {
				discMu.Lock()
				users, at := discUsers, discAt
				discMu.Unlock()
				back := 0
				fresh := false
				for waited := time.Duration(0); waited <= hubOnlineWait && !fresh; waited += hubResumeEvery {
					back, fresh = 0, true
					for _, u := range users {
						seen, f := d.seenSince(u, at)
						fresh = fresh && f
						if seen {
							back++
						}
					}
					if !fresh {
						d.sleep(hubResumeEvery)
					}
				}
				c := opsCheck{Name: "отключённые не вернулись", OK: fresh && back == 0, Detail: fmt.Sprintf("вернулись %d из %d", back, len(users))}
				if !fresh {
					c.Detail = "нет свежей статистики xray"
				}
				return []opsCheck{c, synthCheck(ctx, d)}, nil
			}},
	}
}

// ── the live dependencies ──

func liveExec(ctx context.Context, name string, args ...string) (string, error) {
	out, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
	return string(out), err
}

// liveActionSynth makes one synthetic run for an action, waiting while another run is busy.
func liveActionSynth(ctx context.Context) (string, string, error) {
	deadline := time.Now().Add(3 * time.Minute)
	for {
		id, state, err := runSynthetic(ctx, "action")
		if errors.Is(err, errSynthBusy) && time.Now().Before(deadline) {
			select {
			case <-ctx.Done():
				return "", "", ctx.Err()
			case <-time.After(10 * time.Second):
			}
			continue
		}
		if err != nil {
			return "", "", err
		}
		return state, fmt.Sprintf("прогон %d", id), nil
	}
}

func liveSiteOK(ctx context.Context) error {
	site := monitorSiteHost()
	tr := &http.Transport{
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, network, "127.0.0.1:8443")
		},
		TLSClientConfig: &tls.Config{ServerName: site},
	}
	defer tr.CloseIdleConnections()
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://"+site+"/api/v1/health", nil)
	if err != nil {
		return err
	}
	resp, err := (&http.Client{Transport: tr}).Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("ответ %d", resp.StatusCode)
	}
	return nil
}

func liveStartRunAfter(ctx context.Context, t time.Time) (string, string, bool, error) {
	var state, summary string
	err := db.DB.QueryRowContext(ctx, `
		SELECT state, summary FROM probe_runs
		 WHERE trigger = 'start' AND vantage = 'outside' AND at >= $1 ORDER BY id LIMIT 1`, t).Scan(&state, &summary)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", false, nil
	}
	return state, summary, err == nil, err
}

// pendingEmails: the watched users seen online after their access was removed.
func (w *revokedWatch) pendingEmails() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	var out []string
	for email, e := range w.users {
		if e.Pending {
			out = append(out, email)
		}
	}
	sort.Strings(out)
	return out
}

var liveHubDeps = hubDeps{
	exec:          liveExec,
	restartXray:   restartXray,
	xrayRestarted: recordXrayRestart,
	restartBamboo: restartHysteria2Server,
	bambooRestarted: func(by string) {
		recordOps(biz.OpsEvent{Kind: "restart", Actor: by, Target: "bamboo", Impact: biz.ImpactK2, OK: true,
			Summary: "сервер Бамбука перезапущен: вручную: " + by, Details: map[string]any{"cause": "manual"}})
	},
	synth:     liveActionSynth,
	online:    onlineNow,
	exit:      biz.GetExitNodeTunnel,
	probeExit: func(n biz.ExitNodeTunnel) exitPathProbe { return exitPathProber(n) },
	hasHy:     exitHasHyPath,
	hasVR:     exitHasVRPath,
	newestBackup: func() (time.Time, error) {
		entries, err := os.ReadDir(backupDir)
		if err != nil {
			return time.Time{}, err
		}
		return newestBackupModTime(entries), nil
	},
	siteOK:        liveSiteOK,
	startRunAfter: liveStartRunAfter,
	pending:       func() []string { return revokedUsers.pendingEmails() },
	resetRevoked:  func() { revokedUsers.reset() },
	seenSince:     func(u string, t time.Time) (bool, bool) { return onlineLive.seenSince("xray", u, t) },
	sleep:         time.Sleep,
}

func init() {
	for _, a := range buildHubActions(liveHubDeps) {
		registerAction(a)
	}
}
