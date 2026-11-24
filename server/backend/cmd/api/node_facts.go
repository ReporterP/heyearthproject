package main

// Node facts (plan docs/plan/stability-operations-2.md, C5; rule A11): a read-only facts job on
// every working exit and entry daily at 06:00 MSK and after each successful verify; the Hub reads
// the same script locally. node_facts keeps the last answer per node (server_id 0 — the Hub) and
// when a node first asked for a reboot. Alerts: node-reboot-pending:<id> past 14 days (the Hub has
// its own, phase 1: rebootPendingKey), node-security-updates:<id> — both without repeats.

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"os/exec"
	"strconv"
	"sync"
	"time"

	"github.com/heyearth/backend/internal/biz"
	"github.com/heyearth/backend/internal/db"
	"github.com/heyearth/backend/internal/provision"
)

type nodeFacts struct {
	ServerID            int       `json:"server_id"`
	CollectedAt         time.Time `json:"collected_at"`
	Kernel              string    `json:"kernel"`
	BootAt              time.Time `json:"boot_at"`
	RebootRequired      bool      `json:"reboot_required"`
	RebootRequiredSince time.Time `json:"reboot_required_since"`
	SecurityUpdates     int       `json:"security_updates"`
	SSHPassword         bool      `json:"ssh_password"`
	DiskPct             int       `json:"disk_pct"`
}

// mergeFacts: a repeated sighting of «reboot required» keeps its first time.
func mergeFacts(prev *nodeFacts, id int, f provision.NodeFacts, at time.Time) nodeFacts {
	n := nodeFacts{ServerID: id, CollectedAt: at, Kernel: f.Kernel, BootAt: f.BootAt, RebootRequired: f.RebootRequired,
		SecurityUpdates: f.SecurityUpdates, SSHPassword: f.SSHPassword, DiskPct: f.DiskPct}
	if f.RebootRequired {
		n.RebootRequiredSince = at
		if prev != nil && prev.RebootRequired && !prev.RebootRequiredSince.IsZero() {
			n.RebootRequiredSince = prev.RebootRequiredSince
		}
	}
	return n
}

func factsNodeKey(id int) string {
	if id == 0 {
		return "hub"
	}
	return strconv.Itoa(id)
}

type factsAlerts struct {
	raise   func(key string, level AlertLevel, title, details string)
	resolve func(key, msg string)
}

func (j factsAlerts) check(list []nodeFacts, now time.Time) {
	for _, f := range list {
		node := factsNodeKey(f.ServerID)
		if f.ServerID != 0 { // the Hub's reboot is its board cell's alert (rebootPendingKey)
			key := "node-reboot-pending:" + node
			if _, detail, alert := rebootPendingVerdict(f.RebootRequiredSince, now); alert {
				j.raise(key, AlertWarn, "Узел "+node+" давно ждёт перезагрузки",
					"Обновления ядра установлены, но не действуют: "+detail+". Перезагрузка — «Операции → Работы», в окно 04:00–05:30 МСК.")
			} else {
				j.resolve(key, "Узел "+node+" перезагружен")
			}
		}
		key := "node-security-updates:" + node
		if f.SecurityUpdates > 0 {
			j.raise(key, AlertWarn, "Узел "+node+": обновления безопасности не поставлены",
				fmt.Sprintf("Ждут установки: %d. Ставит их ночной таймер безопасности; если число не уходит — смотрите «Работы».", f.SecurityUpdates))
		} else {
			j.resolve(key, "Узел "+node+": обновления безопасности поставлены")
		}
	}
}

// ── store ──

func loadNodeFacts(ctx context.Context) ([]nodeFacts, error) {
	rows, err := db.DB.QueryContext(ctx, `
		SELECT server_id, collected_at, kernel, boot_at, reboot_required, reboot_required_since, security_updates, ssh_password, disk_pct
		  FROM node_facts ORDER BY server_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []nodeFacts
	for rows.Next() {
		var f nodeFacts
		var boot, since sql.NullTime
		if err := rows.Scan(&f.ServerID, &f.CollectedAt, &f.Kernel, &boot, &f.RebootRequired, &since, &f.SecurityUpdates, &f.SSHPassword, &f.DiskPct); err != nil {
			return nil, err
		}
		f.BootAt, f.RebootRequiredSince = boot.Time, since.Time
		out = append(out, f)
	}
	return out, rows.Err()
}

// saveNodeFacts merges with the stored row and writes it.
func saveNodeFacts(ctx context.Context, id int, f provision.NodeFacts) error {
	var prev *nodeFacts
	if list, err := loadNodeFacts(ctx); err == nil {
		for i := range list {
			if list[i].ServerID == id {
				prev = &list[i]
			}
		}
	}
	n := mergeFacts(prev, id, f, time.Now())
	_, err := db.DB.ExecContext(ctx, `
		INSERT INTO node_facts (server_id, collected_at, kernel, boot_at, reboot_required, reboot_required_since, security_updates, ssh_password, disk_pct)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		ON CONFLICT (server_id) DO UPDATE SET collected_at = EXCLUDED.collected_at, kernel = EXCLUDED.kernel, boot_at = EXCLUDED.boot_at,
		       reboot_required = EXCLUDED.reboot_required, reboot_required_since = EXCLUDED.reboot_required_since,
		       security_updates = EXCLUDED.security_updates, ssh_password = EXCLUDED.ssh_password, disk_pct = EXCLUDED.disk_pct`,
		n.ServerID, n.CollectedAt, n.Kernel, nullTime(n.BootAt), n.RebootRequired, nullTime(n.RebootRequiredSince), n.SecurityUpdates, n.SSHPassword, n.DiskPct)
	return err
}

// ── collection ──

func collectHubFacts(ctx context.Context) error {
	out, err := exec.CommandContext(ctx, "bash", "-c", provision.FactsScript).Output()
	if err != nil && len(out) == 0 {
		return err
	}
	f, err := provision.ParseFacts(string(out))
	if err != nil {
		return err
	}
	return saveNodeFacts(ctx, 0, f)
}

// factsCron: the daily run at 06:00 MSK and the follow-up of every successful verify.
type factsCron struct {
	mu        sync.Mutex
	lastDaily string    // the Moscow date of the last daily run
	since     time.Time // verify jobs finished after this are followed by facts
}

var factsLive = &factsCron{}

func (c *factsCron) tick(ctx context.Context, now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	m := biz.MoscowTime(now)
	var ids []int
	if day := m.Format("2006-01-02"); m.Hour() == 6 && c.lastDaily != day {
		c.lastDaily = day
		if err := collectHubFacts(ctx); err != nil {
			log.Printf("[facts] хаб: %v", err)
		}
		rows, err := db.DB.QueryContext(ctx, `SELECT id FROM servers WHERE role IN ('exit', 'relay') AND lifecycle_state = 'online' AND ssh_host_key_fp <> ''`)
		if err == nil {
			for rows.Next() {
				var id int
				if rows.Scan(&id) == nil {
					ids = append(ids, id)
				}
			}
			rows.Close()
		}
	}
	if c.since.IsZero() {
		c.since = now
	}
	rows, err := db.DB.QueryContext(ctx, `SELECT DISTINCT server_id FROM provision_jobs WHERE kind = 'verify' AND status = 'succeeded' AND finished_at > $1`, c.since)
	if err == nil {
		for rows.Next() {
			var id int
			if rows.Scan(&id) == nil {
				ids = append(ids, id)
			}
		}
		rows.Close()
		c.since = now
	}
	seen := map[int]bool{}
	for _, id := range ids {
		if seen[id] || nodeRunner == nil {
			continue
		}
		seen[id] = true
		if _, err := nodeRunner.Start(ctx, id, provision.KindFacts); err != nil {
			log.Printf("[facts] узел %d: %v", id, err) // busy with another job: the next day or verify
		}
	}
	if list, err := loadNodeFacts(ctx); err == nil {
		factsAlerts{raise: RaiseAlertNoRepeat, resolve: ResolveAlert}.check(list, now)
	}
}

func startNodeFacts() {
	go runEvery(3*time.Minute, 5*time.Minute, func() {
		safely("сведения узлов", func() {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
			defer cancel()
			factsLive.tick(ctx, time.Now())
		})
	})
}
