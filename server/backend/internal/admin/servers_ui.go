package admin

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/heyearth/backend/internal/db"
	"github.com/lib/pq"
)

// The «Серверы» page (cleanup plan B1/B5): what it needs besides the node list — a server's or
// the Hub's load charts, the numbers only the owner knows (the hoster's traffic quota, the
// price, the port speed, the cores) and the Hub's own row.

// loadFreshFor: a sample older than this is history, not the server's state (the collector
// takes one a minute).
const loadFreshFor = 5 * time.Minute

// nodeLoadView is a server's latest load sample as the list shows it.
type nodeLoadView struct {
	CPUPct       *float64   `json:"cpu_pct"`
	MemPct       *float64   `json:"mem_pct"`
	NetRxMbps    *float64   `json:"net_rx_mbps"`
	NetTxMbps    *float64   `json:"net_tx_mbps"`
	Connections  *int       `json:"connections"`
	ConntrackPct *float64   `json:"conntrack_pct"`
	Cores        int        `json:"cores"`
	At           *time.Time `json:"at"`
	Stale        bool       `json:"stale"`           // no sample for loadFreshFor
	Error        string     `json:"error,omitempty"` // why the collector got nothing (newer than the sample)
}

// parseLoadMetrics reads servers.load_metrics as the node load collector writes it
// (node_load.go recordNodeSample / noteNodeLoadError). Values of the old gRPC collector carry
// no synced_at of the SSH collector and come out stale.
func parseLoadMetrics(raw string, now time.Time) *nodeLoadView {
	var m struct {
		CPU     *float64 `json:"cpu_usage"`
		Mem     *float64 `json:"memory_usage"`
		Conns   *float64 `json:"active_connections"`
		Cores   float64  `json:"cores"`
		CT      float64  `json:"conntrack"`
		CTMax   float64  `json:"conntrack_max"`
		Rx      *float64 `json:"net_rx_mbps"`
		Tx      *float64 `json:"net_tx_mbps"`
		Synced  string   `json:"synced_at"`
		Source  string   `json:"source"`
		Err     string   `json:"load_error"`
		ErrAtTx string   `json:"load_error_at"`
	}
	if json.Unmarshal([]byte(raw), &m) != nil {
		return &nodeLoadView{Stale: true}
	}
	v := &nodeLoadView{Stale: true, Cores: int(m.Cores)}
	at, err := time.Parse(time.RFC3339, m.Synced)
	if m.Source == "ssh" && err == nil {
		v.At = &at
		v.Stale = now.Sub(at) > loadFreshFor
		v.CPUPct, v.MemPct, v.NetRxMbps, v.NetTxMbps = m.CPU, m.Mem, m.Rx, m.Tx
		if m.Conns != nil {
			c := int(*m.Conns)
			v.Connections = &c
		}
		if m.CTMax > 0 {
			p := round1(100 * m.CT / m.CTMax)
			v.ConntrackPct = &p
		}
	}
	if m.Err != "" {
		errAt, perr := time.Parse(time.RFC3339, m.ErrAtTx)
		if v.At == nil || (perr == nil && errAt.After(*v.At)) {
			v.Error = m.Err
		}
	}
	return v
}

// nodeOwnerView: what the owner enters in the card («Настройки»); null = not entered.
type nodeOwnerView struct {
	TrafficQuotaTB  *float64 `json:"traffic_quota_tb"`
	MonthlyPriceRub *int     `json:"monthly_price_rub"`
	PortMbps        *int     `json:"port_mbps"`
	CPUCores        *int     `json:"cpu_cores"`
}

func ownerView(quota sql.NullFloat64, price, port, cores sql.NullInt64) nodeOwnerView {
	var o nodeOwnerView
	if quota.Valid {
		q := round1(quota.Float64)
		o.TrafficQuotaTB = &q
	}
	for _, f := range []struct {
		v   sql.NullInt64
		dst **int
	}{{price, &o.MonthlyPriceRub}, {port, &o.PortMbps}, {cores, &o.CPUCores}} {
		if f.v.Valid {
			n := int(f.v.Int64)
			*f.dst = &n
		}
	}
	return o
}

// validateOwner: every field is optional (null clears it), entered numbers must be sane.
func validateOwner(o nodeOwnerView) error {
	if o.TrafficQuotaTB != nil && (math.IsNaN(*o.TrafficQuotaTB) || *o.TrafficQuotaTB < 0 || *o.TrafficQuotaTB > 10000) {
		return fmt.Errorf("лимит трафика: от 0 до 10 000 ТБ (0 — безлимит)")
	}
	if o.MonthlyPriceRub != nil && (*o.MonthlyPriceRub < 0 || *o.MonthlyPriceRub > 1000000) {
		return fmt.Errorf("цена: от 0 до 1 000 000 ₽ в месяц")
	}
	if o.PortMbps != nil && (*o.PortMbps < 1 || *o.PortMbps > 100000) {
		return fmt.Errorf("порт: от 1 до 100 000 Мбит/с")
	}
	if o.CPUCores != nil && (*o.CPUCores < 1 || *o.CPUCores > 256) {
		return fmt.Errorf("ядра: от 1 до 256")
	}
	return nil
}

func decodeOwner(w http.ResponseWriter, r *http.Request) (nodeOwnerView, bool) {
	var o nodeOwnerView
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&o); err != nil {
		writeAdminError(w, http.StatusBadRequest, "Неверное тело запроса")
		return o, false
	}
	if err := validateOwner(o); err != nil {
		writeAdminError(w, http.StatusBadRequest, err.Error())
		return o, false
	}
	return o, true
}

// serverIDBefore: the {id} of /api/admin/servers/{id}/<suffix>; "hub" = the Hub (0).
func serverIDBefore(path, suffix string) (int, bool) {
	seg := strings.TrimSuffix(strings.TrimPrefix(path, "/api/admin/servers/"), suffix)
	if seg == "hub" {
		return 0, true
	}
	id, err := strconv.Atoi(seg)
	return id, err == nil && id > 0
}

// ServerOwnerHandler — PUT /api/admin/servers/{id}/capacity {traffic_quota_tb, monthly_price_rub,
// port_mbps, cpu_cores}: the owner's numbers «Мощность» counts with; {id} "hub" = the Hub.
func ServerOwnerHandler(w http.ResponseWriter, r *http.Request) {
	id, ok := serverIDBefore(r.URL.Path, "/capacity")
	if !ok {
		writeAdminError(w, http.StatusBadRequest, "Неверный id сервера")
		return
	}
	o, ok := decodeOwner(w, r)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	if id == 0 {
		if err := saveHubOwner(ctx, o); err != nil {
			writeAdminError(w, http.StatusInternalServerError, "Не удалось сохранить")
			return
		}
	} else {
		res, err := db.DB.ExecContext(ctx, `
			UPDATE servers SET traffic_quota_tb = $2, monthly_price_rub = $3, port_mbps = $4, cpu_cores = $5, updated_at = NOW()
			WHERE id = $1`, id, o.TrafficQuotaTB, o.MonthlyPriceRub, o.PortMbps, o.CPUCores)
		if err != nil {
			writeAdminError(w, http.StatusInternalServerError, "Не удалось сохранить")
			return
		}
		if n, _ := res.RowsAffected(); n == 0 {
			writeAdminError(w, http.StatusNotFound, "Сервер не найден")
			return
		}
	}
	target := strconv.Itoa(id)
	if id == 0 {
		target = "hub"
	}
	WriteAuditLog(r, "server.capacity", "server", target, o)
	writeAdminJSON(w, http.StatusOK, o)
}

// Hub's numbers live in app_settings (it has no servers row); "" = not entered.
var hubOwnerKeys = [4]string{"hub_traffic_quota_tb", "hub_monthly_price_rub", "hub_port_mbps", "hub_cpu_cores"}

func saveHubOwner(ctx context.Context, o nodeOwnerView) error {
	vals := [4]string{}
	if o.TrafficQuotaTB != nil {
		vals[0] = strconv.FormatFloat(*o.TrafficQuotaTB, 'f', -1, 64)
	}
	for i, p := range []*int{o.MonthlyPriceRub, o.PortMbps, o.CPUCores} {
		if p != nil {
			vals[i+1] = strconv.Itoa(*p)
		}
	}
	_, err := db.DB.ExecContext(ctx, `
		INSERT INTO app_settings (key, value, updated_at) VALUES ($1, $2, NOW()), ($3, $4, NOW()), ($5, $6, NOW()), ($7, $8, NOW())
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = NOW()`,
		hubOwnerKeys[0], vals[0], hubOwnerKeys[1], vals[1], hubOwnerKeys[2], vals[2], hubOwnerKeys[3], vals[3])
	return err
}

func loadHubOwner(ctx context.Context) (nodeOwnerView, error) {
	rows, err := db.DB.QueryContext(ctx, `SELECT key, value FROM app_settings WHERE key = ANY($1)`, pq.Array(hubOwnerKeys[:]))
	if err != nil {
		return nodeOwnerView{}, err
	}
	defer rows.Close()
	var o nodeOwnerView
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return o, err
		}
		v = strings.TrimSpace(v)
		if k == hubOwnerKeys[0] {
			if f, err := strconv.ParseFloat(v, 64); err == nil {
				o.TrafficQuotaTB = &f
			}
			continue
		}
		n, err := strconv.Atoi(v)
		if err != nil {
			continue
		}
		switch k {
		case hubOwnerKeys[1]:
			o.MonthlyPriceRub = &n
		case hubOwnerKeys[2]:
			o.PortMbps = &n
		case hubOwnerKeys[3]:
			o.CPUCores = &n
		}
	}
	return o, rows.Err()
}

// HubHandler — GET /api/admin/hub: the Hub's row on «Серверы» (latest sample, the owner's
// numbers, this month's traffic).
func HubHandler(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	owner, err := loadHubOwner(ctx)
	if err != nil {
		writeAdminError(w, http.StatusInternalServerError, "Не удалось загрузить Hub")
		return
	}
	load := &nodeLoadView{Stale: true}
	var at time.Time
	var cpu, mem float64
	var rx, tx sql.NullFloat64
	var cores, ct, ctMax, tcp int
	err = db.DB.QueryRowContext(ctx, `
		SELECT at, cpu_pct, mem_pct, net_rx_mbps, net_tx_mbps, cores, conntrack, conntrack_max, tcp
		FROM server_metric_samples WHERE server_id IS NULL ORDER BY at DESC LIMIT 1`).
		Scan(&at, &cpu, &mem, &rx, &tx, &cores, &ct, &ctMax, &tcp)
	switch {
	case err == sql.ErrNoRows:
	case err != nil:
		writeAdminError(w, http.StatusInternalServerError, "Не удалось загрузить нагрузку Hub")
		return
	default:
		c, m := round1(cpu), round1(mem)
		load = &nodeLoadView{CPUPct: &c, MemPct: &m, Connections: &tcp, Cores: cores, At: &at, Stale: time.Since(at) > loadFreshFor}
		if rx.Valid && tx.Valid {
			a, b := round1(rx.Float64), round1(tx.Float64)
			load.NetRxMbps, load.NetTxMbps = &a, &b
		}
		if ctMax > 0 {
			p := round1(100 * float64(ct) / float64(ctMax))
			load.ConntrackPct = &p
		}
	}
	var monthTB float64
	_ = db.DB.QueryRowContext(ctx, `SELECT COALESCE(SUM(rx_bytes + tx_bytes), 0)::float8 / 1e12 FROM server_metric_samples
		WHERE server_id IS NULL AND at >= date_trunc('month', NOW())`).Scan(&monthTB)
	writeAdminJSON(w, http.StatusOK, map[string]any{"name": "Hub", "load": load, "owner": owner, "month_tb": round2(monthTB)})
}

func round2(v float64) float64 { return math.Round(v*100) / 100 }

// loadRanges: the card's «Нагрузка» charts — 24 h in 10-minute points, 7 days in hours.
var loadRanges = map[string]struct{ span, step time.Duration }{
	"24h": {24 * time.Hour, 10 * time.Minute},
	"7d":  {7 * 24 * time.Hour, time.Hour},
}

type loadPoint struct {
	At           time.Time `json:"at"`
	CPU          float64   `json:"cpu"`     // average over the point
	CPUMax       float64   `json:"cpu_max"` // its peak minute
	Mem          float64   `json:"mem"`
	RxMbps       *float64  `json:"rx_mbps"` // peak
	TxMbps       *float64  `json:"tx_mbps"`
	Connections  int       `json:"connections"` // peak
	ConntrackPct *float64  `json:"conntrack_pct"`
}

// serverLoadSQL buckets a server's samples; %s is the server condition.
const serverLoadSQL = `
	SELECT to_timestamp(floor(extract(epoch FROM at)::float8 / $1) * $1) b,
	       AVG(cpu_pct)::float8, MAX(cpu_pct)::float8, AVG(mem_pct)::float8,
	       MAX(net_rx_mbps)::float8, MAX(net_tx_mbps)::float8, MAX(tcp),
	       MAX(CASE WHEN conntrack_max > 0 THEN 100.0 * conntrack / conntrack_max END)::float8
	FROM server_metric_samples
	WHERE %s AND at > NOW() - make_interval(secs => $2)
	GROUP BY 1 ORDER BY 1`

// ServerLoadHandler — GET /api/admin/servers/{id|hub}/load?range=24h|7d.
func ServerLoadHandler(w http.ResponseWriter, r *http.Request) {
	id, ok := serverIDBefore(r.URL.Path, "/load")
	if !ok {
		writeAdminError(w, http.StatusBadRequest, "Неверный id сервера")
		return
	}
	rng := r.URL.Query().Get("range")
	if rng == "" {
		rng = "24h"
	}
	spec, ok := loadRanges[rng]
	if !ok {
		writeAdminError(w, http.StatusBadRequest, "range: 24h или 7d")
		return
	}
	cond, args := "server_id IS NULL", []any{spec.step.Seconds(), spec.span.Seconds()}
	if id != 0 {
		cond, args = "server_id = $3", append(args, id)
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	rows, err := db.DB.QueryContext(ctx, fmt.Sprintf(serverLoadSQL, cond), args...)
	if err != nil {
		writeAdminError(w, http.StatusInternalServerError, "Не удалось загрузить нагрузку")
		return
	}
	defer rows.Close()
	points := []loadPoint{}
	for rows.Next() {
		var p loadPoint
		var rx, tx, ct sql.NullFloat64
		if err := rows.Scan(&p.At, &p.CPU, &p.CPUMax, &p.Mem, &rx, &tx, &p.Connections, &ct); err != nil {
			writeAdminError(w, http.StatusInternalServerError, "Не удалось прочитать нагрузку")
			return
		}
		p.CPU, p.CPUMax, p.Mem = round1(p.CPU), round1(p.CPUMax), round1(p.Mem)
		for _, f := range []struct {
			v   sql.NullFloat64
			dst **float64
		}{{rx, &p.RxMbps}, {tx, &p.TxMbps}, {ct, &p.ConntrackPct}} {
			if f.v.Valid {
				x := round1(f.v.Float64)
				*f.dst = &x
			}
		}
		points = append(points, p)
	}
	if err := rows.Err(); err != nil {
		writeAdminError(w, http.StatusInternalServerError, "Не удалось загрузить нагрузку")
		return
	}
	writeAdminJSON(w, http.StatusOK, map[string]any{"range": rng, "step_minutes": int(spec.step.Minutes()), "points": points})
}
