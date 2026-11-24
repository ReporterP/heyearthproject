package main

// Node actions of the console (plan docs/plan/stability-operations-2.md, C5):
//
//	node.reboot  K2 ops.disrupt  an exit or an entry; «в окно» by default. A silence on the node's
//	             alerts for 30 min; an entry leaves the rotation first (biz.SetEntryRotation —
//	             it comes back by hand, through the test, as «Входы» requires) and never when it is
//	             the last healthy path; the reboot job; verify retried until the node is back
//	             (10 min); the run ends with verify's result.
//	hub.reboot   K3 ops.disrupt, word REBOOT; the next API start closes it by its start run.

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/heyearth/backend/internal/biz"
	"github.com/heyearth/backend/internal/db"
	"github.com/heyearth/backend/internal/provision"
)

const (
	nodeEntryHealthFresh = 10 * time.Minute
	nodeBackWait         = 10 * time.Minute
	nodeBackEvery        = time.Minute
	nodeRebootSilence    = 30 * time.Minute
)

// otherHealthyPaths counts the entries in rotation «all» with a fresh healthy external check,
// except one. The Hub does not count (mobile networks cut it); unknown or stale is not healthy.
func otherHealthyPaths(entries []biz.Entry, except int, now time.Time) int {
	n := 0
	for _, e := range entries {
		if e.ID == except || e.Kind != biz.EntryKindRelay || e.Lifecycle != "online" || e.Rotation != biz.EntryRotationAll {
			continue
		}
		if e.Health.External == biz.EntryHealthy && e.Health.ExternalAt != nil && now.Sub(*e.Health.ExternalAt) <= nodeEntryHealthFresh {
			n++
		}
	}
	return n
}

func nodeSilencePatterns(n provision.Node) []string {
	id := strconv.Itoa(n.ID)
	if n.Role == "relay" {
		return []string{"relay-" + id + "-*"}
	}
	return []string{"exit-down-" + id, "exit-path-down-" + id + "-*"}
}

type nodeOpsDeps struct {
	node      func(ctx context.Context, id int) (provision.Node, error)
	entries   func(ctx context.Context) ([]biz.Entry, error)
	runJob    func(ctx context.Context, id int, kind string) (status string, err error) // start and wait
	rotateOff func(ctx context.Context, id int, reason, by string) error
	bump      func(ctx context.Context) error
	silence   func(ctx context.Context, pattern, reason, by string, d time.Duration) error
	online    func(now time.Time) onlineCounts
	sleep     func(time.Duration)
	now       func() time.Time
}

func buildNodeActions(d nodeOpsDeps) []opsAction {
	target := func(ctx context.Context, s string) (provision.Node, error) {
		id, err := strconv.Atoi(s)
		if err != nil {
			return provision.Node{}, err
		}
		return d.node(ctx, id)
	}
	return []opsAction{{
		ID: "node.reboot", Title: "Перезагрузить узел", Impact: biz.ImpactK2, Perm: opsPermDisrupt, Target: "node",
		MinGap: 30 * time.Minute, PostNames: []string{"verify после возврата"},
		IfFails: "узел не вернулся или verify не прошёл: карточка узла; вход остаётся вне ротации",
		Pre: func(ctx context.Context, s string) ([]opsCheck, error) {
			n, err := target(ctx, s)
			if err != nil {
				return []opsCheck{{Name: "узел работает", Detail: err.Error()}}, nil
			}
			checks := []opsCheck{{Name: "узел работает", OK: n.Lifecycle == "online" && n.HostKeyConfirmed && (n.Role == "exit" || n.Role == "relay"),
				Detail: n.Role + ", " + n.Lifecycle}}
			if n.Role == "relay" {
				entries, err := d.entries(ctx)
				if err != nil {
					return nil, err
				}
				others := otherHealthyPaths(entries, n.ID, d.now())
				checks = append(checks, opsCheck{Name: "не последний здоровый путь", OK: others > 0,
					Detail: fmt.Sprintf("других здоровых входов в ротации: %d", others)})
			}
			return checks, nil
		},
		Affected: func(ctx context.Context, s string) (int, int, string, error) {
			return -1, 0, "пользователи этого узла: точное число неизвестно", nil
		},
		Run: func(ctx context.Context, s, by string, progress func(string)) error {
			n, err := target(ctx, s)
			if err != nil {
				return err
			}
			for _, p := range nodeSilencePatterns(n) {
				if err := d.silence(ctx, p, "перезагрузка узла с пульта", by, nodeRebootSilence); err != nil {
					return fmt.Errorf("тишина %s: %w", p, err)
				}
			}
			if n.Role == "relay" {
				if err := d.rotateOff(ctx, n.ID, "перезагрузка с пульта: "+by, by); err != nil {
					return fmt.Errorf("вход не выведен из ротации: %w", err)
				}
				_ = d.bump(ctx)
				progress("вход выведен из ротации; вернуть — «Входы», через тест")
			}
			st, err := d.runJob(ctx, n.ID, provision.KindReboot)
			if err != nil || st != provision.JobSucceeded {
				return fmt.Errorf("перезагрузка не назначена: %s %v", st, err)
			}
			progress("перезагрузка назначена; жду возврата до 10 минут")
			for waited := time.Duration(0); waited < nodeBackWait; waited += nodeBackEvery {
				d.sleep(nodeBackEvery)
				if st, err := d.runJob(ctx, n.ID, provision.KindVerify); err == nil && st == provision.JobSucceeded {
					progress("узел вернулся, verify прошёл")
					return nil
				}
			}
			return errors.New("узел не вернулся за 10 минут (или verify не проходит)")
		},
	}}
}

// ── live ──

func liveRunJob(ctx context.Context, id int, kind string) (string, error) {
	if nodeRunner == nil {
		return "", errors.New("задания недоступны")
	}
	jobID, err := nodeRunner.Start(ctx, id, kind)
	if err != nil {
		return "", err
	}
	for {
		v, err := nodeStore.JobView(ctx, jobID)
		if err == nil && v.Status != provision.JobRunning && v.Status != "queued" {
			return v.Status, nil
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(5 * time.Second):
		}
	}
}

func liveSilence(ctx context.Context, pattern, reason, by string, d time.Duration) error {
	if err := validSilence(pattern, d); err != nil {
		return err
	}
	_, err := db.DB.ExecContext(ctx, `INSERT INTO alert_silences (pattern, reason, created_by, until) VALUES ($1, $2, $3, now() + make_interval(mins => $4))`,
		pattern, reason, by, int(d.Minutes()))
	if err == nil {
		refreshSilences(ctx)
		recordOps(biz.OpsEvent{Kind: "maintenance", Actor: by, Target: "alerts", Impact: biz.ImpactK0, OK: true,
			Summary: fmt.Sprintf("тишина %s на %d мин: %s", pattern, int(d.Minutes()), reason)})
	}
	return err
}

var liveNodeOpsDeps = nodeOpsDeps{
	node:    func(ctx context.Context, id int) (provision.Node, error) { return nodeStore.LoadNode(ctx, id) },
	entries: biz.ListMonitoredEntries,
	runJob:  liveRunJob,
	rotateOff: func(ctx context.Context, id int, reason, by string) error {
		return biz.SetEntryRotation(ctx, id, []string{biz.EntryRotationAll, biz.EntryRotationTest}, biz.EntryRotationOff, reason, by)
	},
	bump:    biz.BumpSubscriptionVersionContext,
	silence: liveSilence,
	online:  onlineNow,
	sleep:   time.Sleep,
	now:     time.Now,
}

func buildHubRebootAction(d hubDeps) opsAction {
	return opsAction{ID: "hub.reboot", Title: "Перезагрузить хаб", Impact: biz.ImpactK3, Perm: opsPermDisrupt, Confirm: "REBOOT",
		MinGap: time.Hour, HandOver: true, PostNames: []string{"прогон синтетики «start» после загрузки"},
		IfFails:  "хаб загрузился, но подписка не проходит: docs/runbooks/hub-down.md",
		Affected: affectedBy(d, func(c onlineCounts) (int, bool) { return c.Total, c.Stale }, "все, кто в сети"),
		Run: func(ctx context.Context, _, _ string, progress func(string)) error {
			progress("перезагрузка хаба через 10 с; действие закроет API после загрузки")
			if out, err := d.exec(ctx, "systemd-run", "--on-active=10", "--unit=heyearth-console-reboot", "systemctl", "reboot"); err != nil {
				return fmt.Errorf("%v: %s", err, lastLine(out))
			}
			return errOpsHandOver
		},
		Resume: opsActions["hub.restart-api"].Resume,
	}
}

func init() {
	for _, a := range buildNodeActions(liveNodeOpsDeps) {
		registerAction(a)
	}
	a := buildHubRebootAction(liveHubDeps)
	if a.Resume == nil { // hub.restart-api registers first (ops_actions_hub.go init order is by file name)
		for _, h := range buildHubActions(liveHubDeps) {
			if h.ID == "hub.restart-api" {
				a.Resume = h.Resume
			}
		}
	}
	registerAction(a)
}
