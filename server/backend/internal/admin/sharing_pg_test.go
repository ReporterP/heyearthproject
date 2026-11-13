package admin

import (
	"encoding/json"
	"strconv"
	"testing"

	"github.com/heyearth/backend/internal/biz"
	"github.com/heyearth/backend/internal/testpg"
)

// C13: the minutes are rolled up into Moscow days; the suspects list and the user card read them.
func TestDialRateSharing(t *testing.T) {
	conn := testpg.Open(t)
	sharer := testpg.NewUser(t, conn, "sharer")
	testpg.NewUser(t, conn, "calm")
	// sharer: 90 minutes above the threshold today; calm: 5 quiet minutes.
	testpg.MustExec(t, conn, `INSERT INTO dial_rate_log (user_uuid, window_start, dials_in_window, bytes_uplink, bytes_downlink, threshold_exceeded)
		SELECT 'sharer', date_trunc('minute', NOW()) - make_interval(mins => g), 300, 15000000, 90000000, TRUE FROM generate_series(1, 90) g`)
	testpg.MustExec(t, conn, `INSERT INTO dial_rate_log (user_uuid, window_start, dials_in_window, bytes_uplink, bytes_downlink, threshold_exceeded)
		SELECT 'calm', date_trunc('minute', NOW()) - make_interval(mins => g), 2, 100000, 900000, FALSE FROM generate_series(1, 5) g`)
	// Two earlier days above an hour make it 3 of 7.
	testpg.MustExec(t, conn, `INSERT INTO dial_rate_daily (user_id, day, peak_dials, minutes_active, minutes_over) VALUES
		('sharer', (NOW() AT TIME ZONE 'Europe/Moscow')::date - 3, 400, 300, 120),
		('sharer', (NOW() AT TIME ZONE 'Europe/Moscow')::date - 5, 400, 300, 75)`)
	if err := biz.RollupDialRate(); err != nil {
		t.Fatal(err)
	}
	if err := biz.RollupDialRate(); err != nil { // idempotent
		t.Fatal(err)
	}
	var over, active int
	_ = conn.QueryRow(`SELECT SUM(minutes_over), SUM(minutes_active) FROM dial_rate_daily WHERE user_id = 'sharer' AND day >= (NOW() AT TIME ZONE 'Europe/Moscow')::date - 1`).Scan(&over, &active)
	if over != 90 || active != 90 {
		t.Errorf("rolled up over %d active %d", over, active)
	}

	rec := call(UserDialRateHandler, "GET", "/api/admin/users/"+strconv.Itoa(sharer)+"/dial-rate", "")
	var card struct {
		Days    []biz.DialDay      `json:"days"`
		Verdict biz.SharingVerdict `json:"verdict"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &card); err != nil || rec.Code != 200 || len(card.Days) != 7 || !card.Verdict.Suspect {
		t.Fatalf("card %d %s", rec.Code, rec.Body)
	}
	rec = call(DialRateSuspectsHandler, "GET", "/api/admin/dial-rate/suspects", "")
	var list struct {
		Suspects []sharingRow `json:"suspects"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil || rec.Code != 200 {
		t.Fatalf("suspects %d %s", rec.Code, rec.Body)
	}
	if len(list.Suspects) != 1 || list.Suspects[0].UserID != "sharer" || list.Suspects[0].UserDBID != sharer || !list.Suspects[0].Verdict.Suspect {
		t.Errorf("suspects %+v", list.Suspects)
	}
}
