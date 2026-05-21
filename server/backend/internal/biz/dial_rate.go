// Package biz — dial-rate helpers.
//
// Dial-rate observations are written by the traffic-stats collector every 60 sec.
// We estimate per-user dial count from uplink-bytes delta (≈50 KB per dial).
// This is rough but adequate for spotting abuse — abusers stand out by an
// order of magnitude vs median legitimate users.
//
// Observation only — no automatic blocking. After collecting enough data,
// admin can decide policy (manual quota, automated cap, education, etc.).
package biz

import (
	"strconv"

	"github.com/heyearth/backend/internal/db"
)

// DialRateSample is one observation window for a single user.
type DialRateSample struct {
	UserUUID       string
	WindowStartISO string // RFC3339
	Dials          int    // estimated dials in the window
	UpBytes        int64
	DownBytes      int64
}

// PersistDialRateSamples writes a batch of samples atomically.
// thresholdPerMin is captured at call time so samples reflect the threshold
// that was active when they were observed.
func PersistDialRateSamples(samples []DialRateSample, thresholdPerMin int) error {
	if len(samples) == 0 {
		return nil
	}
	tx, err := db.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stmt, err := tx.Prepare(`
		INSERT INTO dial_rate_log
		    (user_uuid, window_start, dials_in_window, bytes_uplink, bytes_downlink, threshold_exceeded)
		VALUES ($1, $2::timestamptz, $3, $4, $5, $6)
		ON CONFLICT (user_uuid, window_start) DO NOTHING
	`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, s := range samples {
		exceeded := s.Dials > thresholdPerMin
		if _, err := stmt.Exec(s.UserUUID, s.WindowStartISO, s.Dials, s.UpBytes, s.DownBytes, exceeded); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// GetDialRateThreshold returns the per-minute dial threshold from settings.
// Defaults to 100 if missing or malformed.
func GetDialRateThreshold() int {
	v := GetSettingOrDefault("dial_rate_threshold_per_min", "100")
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return 100
	}
	return n
}

// CleanupDialRateLog deletes samples older than 24 hours.
// Called once per hour from the traffic stats collector.
func CleanupDialRateLog() (int64, error) {
	res, err := db.DB.Exec(`DELETE FROM dial_rate_log WHERE window_start < NOW() - INTERVAL '24 hours'`)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
