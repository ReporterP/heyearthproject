package admin

import (
	"net/http"
	"time"

	"github.com/heyearth/backend/internal/biz"
	"github.com/heyearth/backend/internal/db"
)

// «Приведи друга» in the admin (cleanup plan R4).

type referralTop struct {
	UserDBID  int    `json:"user_db_id"`
	UserID    string `json:"user_id"`
	Invited   int    `json:"invited"`
	Paid      int    `json:"paid"`
	EarnedKop int64  `json:"earned_kop"`
}

type referralRewardRow struct {
	ID          int64     `json:"id"`
	ReferrerID  int       `json:"referrer_db_id"`
	Referrer    string    `json:"referrer"`
	FriendID    int       `json:"friend_db_id"`
	Friend      string    `json:"friend"`
	Status      string    `json:"status"`
	ReferrerKop int64     `json:"referrer_kop"`
	FriendKop   int64     `json:"friend_kop"`
	CreatedAt   time.Time `json:"created_at"`
}

type referralSuspect struct {
	UserDBID int    `json:"user_db_id"`
	UserID   string `json:"user_id"`
	IP       string `json:"ip"`
	Friends  int    `json:"friends"`
}

// ReferralsOverviewHandler — GET /api/admin/referrals.
func ReferralsOverviewHandler(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	out := map[string]any{"settings": biz.LoadReferralSettings(ctx, db.DB)}

	top := []referralTop{}
	rows, err := db.DB.QueryContext(ctx, `
		SELECT u.id, u.user_id,
		       (SELECT COUNT(*) FROM users f WHERE f.referred_by_user_id = u.id),
		       (SELECT COUNT(*) FROM referral_rewards rr WHERE rr.referrer_id = u.id),
		       (SELECT COALESCE(SUM(referrer_kop), 0) FROM referral_rewards rr WHERE rr.referrer_id = u.id AND rr.status = 'paid')
		FROM users u
		WHERE EXISTS (SELECT 1 FROM users f WHERE f.referred_by_user_id = u.id)
		ORDER BY 4 DESC, 3 DESC LIMIT 20`)
	if err != nil {
		writeAdminError(w, http.StatusInternalServerError, "Failed to read referrals")
		return
	}
	for rows.Next() {
		var t referralTop
		if err := rows.Scan(&t.UserDBID, &t.UserID, &t.Invited, &t.Paid, &t.EarnedKop); err == nil {
			top = append(top, t)
		}
	}
	rows.Close()
	out["top"] = top

	recent := []referralRewardRow{}
	rows, err = db.DB.QueryContext(ctx, `
		SELECT rr.id, rr.referrer_id, ru.user_id, rr.friend_id, fu.user_id, rr.status, rr.referrer_kop, rr.friend_kop, rr.created_at
		FROM referral_rewards rr JOIN users ru ON ru.id = rr.referrer_id JOIN users fu ON fu.id = rr.friend_id
		ORDER BY rr.id DESC LIMIT 50`)
	if err != nil {
		writeAdminError(w, http.StatusInternalServerError, "Failed to read referral rewards")
		return
	}
	for rows.Next() {
		var x referralRewardRow
		if err := rows.Scan(&x.ID, &x.ReferrerID, &x.Referrer, &x.FriendID, &x.Friend, &x.Status, &x.ReferrerKop, &x.FriendKop, &x.CreatedAt); err == nil {
			recent = append(recent, x)
		}
	}
	rows.Close()
	out["recent"] = recent

	// Suspicious: several friends of one inviter came from the same address.
	sus := []referralSuspect{}
	rows, err = db.DB.QueryContext(ctx, `
		SELECT u.id, u.user_id, f.referred_ip, COUNT(*)
		FROM users f JOIN users u ON u.id = f.referred_by_user_id
		WHERE f.referred_ip IS NOT NULL AND f.referred_ip <> ''
		GROUP BY u.id, u.user_id, f.referred_ip
		HAVING COUNT(*) >= 3
		ORDER BY 4 DESC LIMIT 20`)
	if err != nil {
		writeAdminError(w, http.StatusInternalServerError, "Failed to read referral signals")
		return
	}
	for rows.Next() {
		var x referralSuspect
		if err := rows.Scan(&x.UserDBID, &x.UserID, &x.IP, &x.Friends); err == nil {
			sus = append(sus, x)
		}
	}
	rows.Close()
	out["suspicious"] = sus

	var totals struct {
		Rewards   int   `json:"rewards"`
		PaidOut   int64 `json:"paid_out_kop"`
		Attracted int   `json:"attracted"`
	}
	_ = db.DB.QueryRowContext(ctx, `
		SELECT (SELECT COUNT(*) FROM referral_rewards WHERE status = 'paid'),
		       (SELECT COALESCE(SUM(referrer_kop + friend_kop), 0) FROM referral_rewards WHERE status = 'paid'),
		       (SELECT COUNT(*) FROM users WHERE referred_by_user_id IS NOT NULL)`).Scan(&totals.Rewards, &totals.PaidOut, &totals.Attracted)
	out["totals"] = totals
	writeAdminJSON(w, http.StatusOK, out)
}

// UserReferralsHandler — GET /api/admin/users/{id}/referrals: who invited the user and
// whom they invited.
func UserReferralsHandler(w http.ResponseWriter, r *http.Request) {
	id, ok := userIDFromWalletPath(r.URL.Path)
	if !ok {
		writeAdminError(w, http.StatusBadRequest, "Invalid user id")
		return
	}
	ctx := r.Context()
	var by *struct {
		ID     int    `json:"id"`
		UserID string `json:"user_id"`
	}
	var refID int
	var refUser string
	if err := db.DB.QueryRowContext(ctx, `SELECT u.id, u.user_id FROM users f JOIN users u ON u.id = f.referred_by_user_id WHERE f.id = $1`, id).Scan(&refID, &refUser); err == nil {
		by = &struct {
			ID     int    `json:"id"`
			UserID string `json:"user_id"`
		}{refID, refUser}
	}
	type friend struct {
		ID         int        `json:"id"`
		UserID     string     `json:"user_id"`
		ReferredAt *time.Time `json:"referred_at"`
		Reward     string     `json:"reward"` // paid | capped | ""
	}
	friends := []friend{}
	rows, err := db.DB.QueryContext(ctx, `
		SELECT f.id, f.user_id, f.referred_at, COALESCE(rr.status, '')
		FROM users f LEFT JOIN referral_rewards rr ON rr.friend_id = f.id
		WHERE f.referred_by_user_id = $1 ORDER BY f.referred_at DESC NULLS LAST LIMIT 200`, id)
	if err != nil {
		writeAdminError(w, http.StatusInternalServerError, "Failed to read referrals")
		return
	}
	defer rows.Close()
	for rows.Next() {
		var f friend
		if err := rows.Scan(&f.ID, &f.UserID, &f.ReferredAt, &f.Reward); err == nil {
			friends = append(friends, f)
		}
	}
	writeAdminJSON(w, http.StatusOK, map[string]any{"referred_by": by, "friends": friends})
}
