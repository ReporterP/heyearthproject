// Package biz — Бамбук (Hysteria2 на Hub): через какой exit он выходит.
//
// hysteria2-server on the Hub sends Bamboo users' traffic into the SOCKS of ONE exit:
// hysteria2-client-<id> on 127.0.0.1:(1088+id), the same client that serves the exit-<id>.hy path
// in xray. There is no "dedicated" client any more (s6yc): its :1091 was exit #3's own port and
// its separate auth password was never set.
//
// Choice (7yrs): a smoothed RTT of each exit's Hysteria path (EWMA, α = 0.3, fed by every
// successful monitor probe through UpdateExitRTT) with hysteresis — an optimisation switch at
// most once per 10 minutes and only to an exit at least 20 % faster. When the current exit loses
// its Hysteria path the switch is immediate (that is a failover, not an optimisation). With no
// live exit the choice stays: server.yaml is not rewritten and hysteria2-server is not restarted
// (traffic cannot pass anyway, and a restart drops every Bamboo session).
package biz

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/heyearth/backend/internal/db"
)

const (
	ExpressEWMAAlpha  = 0.3
	ExpressMinHold    = 10 * time.Minute
	ExpressMinGainPct = 20
	// ExpressFallbackPort is used while no exit was ever chosen (exit #2's port, as before).
	ExpressFallbackPort = 1090
	expressUnmeasured   = 9999.0 // an exit without any RTT sorts last
)

// ExpressCandidate is a healthy exit whose Hysteria path is alive.
type ExpressCandidate struct {
	ID    int
	RTTms int // servers.last_rtt_ms (last Hysteria RTT); 0 = unknown
}

// ExpressChoice is the exit Bamboo leaves through.
type ExpressChoice struct {
	ExitID    int       // 0: no exit chosen yet
	SocksPort int       // 1088+ExitID; ExpressFallbackPort while ExitID is 0
	RTTms     int       // smoothed RTT of the chosen exit, 0 = unknown
	Since     time.Time // when this exit was chosen
	Switched  bool      // this Choose changed the exit
	Reason    string    // why it changed (for the log)
}

func expressSocksPort(id int) int {
	if id <= 0 {
		return ExpressFallbackPort
	}
	return 1088 + id
}

// ExpressSelector keeps the smoothed RTTs and the current choice.
type ExpressSelector struct {
	mu      sync.Mutex
	ewma    map[int]float64
	current int
	since   time.Time
}

// NewExpressSelector starts from the exit chosen at since (0 = none yet; a zero since means the
// hold is already over).
func NewExpressSelector(current int, since time.Time) *ExpressSelector {
	return &ExpressSelector{ewma: map[int]float64{}, current: current, since: since}
}

// Observe folds one successful RTT measurement of exit id into its EWMA.
func (s *ExpressSelector) Observe(id, rttMs int) {
	if id <= 0 || rttMs <= 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	prev, ok := s.ewma[id]
	if !ok || prev >= expressUnmeasured {
		s.ewma[id] = float64(rttMs)
		return
	}
	s.ewma[id] = ExpressEWMAAlpha*float64(rttMs) + (1-ExpressEWMAAlpha)*prev
}

// Current is the choice without re-deciding.
func (s *ExpressSelector) Current() ExpressChoice {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.choiceLocked(false, "")
}

func (s *ExpressSelector) choiceLocked(switched bool, reason string) ExpressChoice {
	rtt := 0
	if v, ok := s.ewma[s.current]; ok && v < expressUnmeasured {
		rtt = int(v + 0.5)
	}
	return ExpressChoice{ExitID: s.current, SocksPort: expressSocksPort(s.current), RTTms: rtt,
		Since: s.since, Switched: switched, Reason: reason}
}

// Choose decides the exit for now among cands (exits whose Hysteria path is alive).
func (s *ExpressSelector) Choose(now time.Time, cands []ExpressCandidate) ExpressChoice {
	s.mu.Lock()
	defer s.mu.Unlock()
	alive := map[int]bool{}
	best := 0
	for _, c := range cands {
		if c.ID <= 0 {
			continue
		}
		alive[c.ID] = true
		if _, ok := s.ewma[c.ID]; !ok {
			if c.RTTms > 0 {
				s.ewma[c.ID] = float64(c.RTTms)
			} else {
				s.ewma[c.ID] = expressUnmeasured
			}
		}
		if best == 0 || s.ewma[c.ID] < s.ewma[best] || (s.ewma[c.ID] == s.ewma[best] && c.ID < best) {
			best = c.ID
		}
	}
	var reason string
	switch {
	case best == 0:
		return s.choiceLocked(false, "нет exit с живым путём Hysteria — выбор не меняется")
	case s.current == 0:
		reason = "первый выбор"
	case !alive[s.current]:
		reason = fmt.Sprintf("у exit #%d нет живого пути Hysteria", s.current)
	case best == s.current, now.Sub(s.since) < ExpressMinHold:
		return s.choiceLocked(false, "")
	case s.ewma[best]*100 > s.ewma[s.current]*(100-ExpressMinGainPct):
		return s.choiceLocked(false, "")
	default:
		reason = fmt.Sprintf("exit #%d быстрее на %d%%", best, int(100*(1-s.ewma[best]/s.ewma[s.current])+0.5))
	}
	s.current, s.since = best, now
	return s.choiceLocked(true, reason)
}

// ─────────────── The process-wide selector (DB-backed) ───────────────

var (
	expressMu  sync.Mutex
	expressSel *ExpressSelector
)

// expressSelector returns the selector, seeded once from app_settings so an API restart neither
// forgets the current exit nor restarts its 10-minute hold. A failed read is an error (retried by
// the next call), never "no exit": that would move Bamboo and restart hysteria2-server.
func expressSelector(ctx context.Context) (*ExpressSelector, error) {
	expressMu.Lock()
	defer expressMu.Unlock()
	if expressSel != nil {
		return expressSel, nil
	}
	read := func(key string) (string, error) {
		v, err := GetSettingContext(ctx, key)
		if errors.Is(err, sql.ErrNoRows) {
			return "", nil
		}
		return strings.TrimSpace(v), err
	}
	idStr, err := read("hysteria2_bound_exit_id")
	if err != nil {
		return nil, err
	}
	sinceStr, err := read("hysteria2_bound_exit_since")
	if err != nil {
		return nil, err
	}
	id, _ := strconv.Atoi(idStr)
	since, _ := time.Parse(time.RFC3339, sinceStr) // absent (pre-phase-1): zero, the hold is over
	expressSel = NewExpressSelector(id, since)
	return expressSel, nil
}

// ExpressCurrent is the current choice without touching the database (admin status).
func ExpressCurrent() (ExpressChoice, bool) {
	expressMu.Lock()
	s := expressSel
	expressMu.Unlock()
	if s == nil {
		return ExpressChoice{}, false
	}
	return s.Current(), true
}

// expressCandidatesQuery lists the exits Bamboo may use: online, healthy, with a Hysteria path
// and an id whose SOCKS port 1088+id the Hub's egress guard allows (1..511, like exitHasHyPath).
const expressCandidatesQuery = `
	SELECT id, COALESCE(last_rtt_ms, 0)
	  FROM servers
	 WHERE is_exit_node = TRUE
	   AND lifecycle_state = 'online'
	   AND tunnel_status = 'healthy'
	   AND tunnel_protocol = 'hysteria2'
	   AND id BETWEEN 1 AND 511
	 ORDER BY id`

// filterExpressCandidates drops exits whose Hysteria path the monitor saw dead (the exit itself
// may still be healthy through its TCP path, which Bamboo cannot use). An exit the monitor has
// not probed since the API started is kept: its healthy tunnel_status is the last word.
func filterExpressCandidates(cands []ExpressCandidate, paths func(int) (ExitPaths, bool)) []ExpressCandidate {
	out := cands[:0:0]
	for _, c := range cands {
		if p, ok := paths(c.ID); ok && p.Hy != PathHealthy {
			continue
		}
		out = append(out, c)
	}
	return out
}

func listExpressCandidates(ctx context.Context) ([]ExpressCandidate, error) {
	rows, err := db.DB.QueryContext(ctx, expressCandidatesQuery)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var cands []ExpressCandidate
	for rows.Next() {
		var c ExpressCandidate
		if err := rows.Scan(&c.ID, &c.RTTms); err != nil {
			return nil, err
		}
		cands = append(cands, c)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return filterExpressCandidates(cands, GetExitPaths), nil
}

// ExpressExit decides Bamboo's exit now and persists a change. On a database error it returns
// the current choice with the error: the caller must keep server.yaml as it is.
func ExpressExit() (ExpressChoice, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	sel, err := expressSelector(ctx)
	if err != nil {
		return ExpressChoice{SocksPort: ExpressFallbackPort}, fmt.Errorf("read the bound exit: %w", err)
	}
	cands, err := listExpressCandidates(ctx)
	if err != nil {
		return sel.Current(), fmt.Errorf("list exits for Bamboo: %w", err)
	}
	c := sel.Choose(time.Now(), cands)
	if c.Switched {
		for key, val := range map[string]string{
			"hysteria2_bound_exit_id":    strconv.Itoa(c.ExitID),
			"hysteria2_bound_exit_since": c.Since.UTC().Format(time.RFC3339),
		} {
			if err := SetSettingContext(ctx, key, val); err != nil {
				log.Printf("[bamboo] не удалось сохранить %s: %v", key, err)
			}
		}
	}
	return c, nil
}

// UpdateExitRTT writes the per-probe RTT of the exit's Hysteria path into last_rtt_ms /
// last_rtt_at and feeds Bamboo's EWMA. Called by the tunnel monitor on every successful Hysteria
// probe only, so transient failures never poison the selection.
func UpdateExitRTT(serverID int, rttMs int) error {
	if rttMs <= 0 {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if s, err := expressSelector(ctx); err == nil {
		s.Observe(serverID, rttMs)
	}
	_, err := db.DB.ExecContext(ctx, `
		UPDATE servers
		   SET last_rtt_ms = $1,
		       last_rtt_at = CURRENT_TIMESTAMP
		 WHERE id = $2
	`, rttMs, serverID)
	return err
}
