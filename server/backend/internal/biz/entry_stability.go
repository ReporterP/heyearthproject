package biz

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/heyearth/backend/internal/db"
)

// The stability of the entries (migration 000098): per minute and kind, how many of the flows the
// entries monitor opened through an exit succeeded.
const (
	EntrySampleTCP443  = "tcp443"  // TLS with the Reality SNI to the entry's :443 (nginx stream, retries)
	EntrySampleTCP2443 = "tcp2443" // TLS to :2443 (DNAT: a lost 5-tuple is a dead flow)
	EntrySampleUDP     = "udp"     // Bamboo VN probes through random hop ports
)

// EntrySample: OK of Total flows of one kind in one check.
type EntrySample struct {
	Kind      string
	OK, Total int
}

// StabilityWindow: the flows of one kind in a time window — OK of Total, over Checks checks.
type StabilityWindow struct {
	OK     int `json:"ok"`
	Total  int `json:"total"`
	Checks int `json:"checks"`
}

// EntryStability: the windows by kind over the last hour and the last 24 hours.
type EntryStability struct {
	H1  map[string]StabilityWindow `json:"h1"`
	H24 map[string]StabilityWindow `json:"h24"`
}

var entrySampleKinds = []string{EntrySampleTCP443, EntrySampleTCP2443, EntrySampleUDP}

// SaveEntrySamples stores one check's samples of entry id (0 = the Hub) in one statement.
func SaveEntrySamples(ctx context.Context, id int, at time.Time, samples []EntrySample) error {
	if len(samples) == 0 {
		return nil
	}
	var q strings.Builder
	q.WriteString(`INSERT INTO entry_probe_samples (entry_id, at, kind, ok, total) VALUES `)
	args := []any{id, at}
	for i, s := range samples {
		if !slices.Contains(entrySampleKinds, s.Kind) || s.Total <= 0 || s.OK < 0 || s.OK > s.Total {
			return fmt.Errorf("entry sample: %+v", s)
		}
		if i > 0 {
			q.WriteString(", ")
		}
		n := len(args)
		fmt.Fprintf(&q, "($1, $2, $%d, $%d, $%d)", n+1, n+2, n+3)
		args = append(args, s.Kind, s.OK, s.Total)
	}
	if _, err := db.DB.ExecContext(ctx, q.String(), args...); err != nil {
		return fmt.Errorf("save entry %d samples: %w", id, err)
	}
	return nil
}

const entryStabilityQuery = `SELECT entry_id, kind,
       COALESCE(SUM(ok) FILTER (WHERE at > NOW() - interval '1 hour'), 0),
       COALESCE(SUM(total) FILTER (WHERE at > NOW() - interval '1 hour'), 0),
       COUNT(*) FILTER (WHERE at > NOW() - interval '1 hour'),
       SUM(ok), SUM(total), COUNT(*)
  FROM entry_probe_samples
 WHERE at > NOW() - interval '24 hours'
 GROUP BY entry_id, kind`

// EntryStabilityAll: every entry's windows (an entry without samples is absent).
func EntryStabilityAll(ctx context.Context) (map[int]EntryStability, error) {
	rows, err := db.DB.QueryContext(ctx, entryStabilityQuery)
	if err != nil {
		return nil, fmt.Errorf("entry stability: %w", err)
	}
	defer rows.Close()
	out := map[int]EntryStability{}
	for rows.Next() {
		var id int
		var kind string
		var h1, h24 StabilityWindow
		if err := rows.Scan(&id, &kind, &h1.OK, &h1.Total, &h1.Checks, &h24.OK, &h24.Total, &h24.Checks); err != nil {
			return nil, fmt.Errorf("entry stability: %w", err)
		}
		st, ok := out[id]
		if !ok {
			st = EntryStability{H1: map[string]StabilityWindow{}, H24: map[string]StabilityWindow{}}
		}
		if h1.Checks > 0 {
			st.H1[kind] = h1
		}
		st.H24[kind] = h24
		out[id] = st
	}
	return out, rows.Err()
}

// PurgeEntrySamples removes samples older than keep (the retention loop: 3 days).
func PurgeEntrySamples(ctx context.Context, keep time.Duration) (int64, error) {
	res, err := db.DB.ExecContext(ctx, `DELETE FROM entry_probe_samples WHERE at < NOW() - make_interval(secs => $1)`, keep.Seconds())
	if err != nil {
		return 0, fmt.Errorf("purge entry samples: %w", err)
	}
	return res.RowsAffected()
}
