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

// RollupDialRate folds yesterday's and today's minutes into dial_rate_daily (Moscow days,
// idempotent: a re-run rewrites the same rows). Called once per hour before the cleanup.
func RollupDialRate() error {
	_, err := db.DB.Exec(rollupDialRateSQL)
	return err
}

const rollupDialRateSQL = `
	INSERT INTO dial_rate_daily (user_id, day, peak_dials, minutes_active, minutes_over, up_bytes, down_bytes)
	SELECT user_uuid, (window_start AT TIME ZONE 'Europe/Moscow')::date,
	       MAX(dials_in_window), COUNT(*), COUNT(*) FILTER (WHERE threshold_exceeded),
	       COALESCE(SUM(bytes_uplink), 0), COALESCE(SUM(bytes_downlink), 0)
	  FROM dial_rate_log
	 WHERE window_start >= ((NOW() AT TIME ZONE 'Europe/Moscow')::date - 1)::timestamp AT TIME ZONE 'Europe/Moscow'
	 GROUP BY 1, 2
	ON CONFLICT (user_id, day) DO UPDATE SET
	    peak_dials = EXCLUDED.peak_dials, minutes_active = EXCLUDED.minutes_active, minutes_over = EXCLUDED.minutes_over,
	    up_bytes = EXCLUDED.up_bytes, down_bytes = EXCLUDED.down_bytes`

// CleanupDialRateLog keeps the minutes 48 hours and the daily summaries 30 days (C13).
// Called once per hour from the traffic stats collector, after RollupDialRate.
func CleanupDialRateLog() (int64, error) {
	res, err := db.DB.Exec(`DELETE FROM dial_rate_log WHERE window_start < NOW() - INTERVAL '48 hours'`)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	if _, err := db.DB.Exec(`DELETE FROM dial_rate_daily WHERE day < (NOW() AT TIME ZONE 'Europe/Moscow')::date - 30`); err != nil {
		return n, err
	}
	return n, nil
}

// DialDay is one Moscow day of a user's dial-rate summary.
type DialDay struct {
	Day         string `json:"day"` // YYYY-MM-DD
	PeakDials   int    `json:"peak_dials"`
	MinutesOn   int    `json:"minutes_active"`
	MinutesOver int    `json:"minutes_over"`
	UpBytes     int64  `json:"up_bytes"`
	DownBytes   int64  `json:"down_bytes"`
}

// SharingVerdict: does the last week look like one subscription shared by many? The dial
// estimate is outgoing bytes / 50 KB, so a long video call also counts — the verdict says
// «похоже», and nothing is blocked automatically. Suspect: above the per-minute threshold at
// least an hour a day on 3 of the 7 days, or 4 hours on any day.
type SharingVerdict struct {
	Suspect   bool   `json:"suspect"`
	Text      string `json:"text"`
	DaysOver  int    `json:"days_over"`
	MaxMinute int    `json:"max_minutes_over"`
}

const (
	sharingHourDays = 3
	sharingDayMin   = 60
	sharingHeavyMin = 240
)

func JudgeSharing(days []DialDay) SharingVerdict {
	v := SharingVerdict{}
	for _, d := range days {
		if d.MinutesOver >= sharingDayMin {
			v.DaysOver++
		}
		if d.MinutesOver > v.MaxMinute {
			v.MaxMinute = d.MinutesOver
		}
	}
	v.Suspect = v.DaysOver >= sharingHourDays || v.MaxMinute >= sharingHeavyMin
	switch {
	case v.Suspect:
		v.Text = "Похоже на раздачу: подключений выше порога больше часа в сутки " + strconv.Itoa(v.DaysOver) + " из 7 дней (максимум " + strconv.Itoa(v.MaxMinute) + " мин). Проверьте вручную — долгие видеозвонки дают похожую картину."
	case v.MaxMinute > 0:
		v.Text = "Бывает выше порога, но в пределах обычного: до " + strconv.Itoa(v.MaxMinute) + " мин в сутки"
	default:
		v.Text = "В норме"
	}
	return v
}
