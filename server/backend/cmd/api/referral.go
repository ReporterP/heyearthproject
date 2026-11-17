package main

import (
	"encoding/json"
	"log"
	"net/http"
	"time"

	"github.com/heyearth/backend/internal/biz"
	"github.com/heyearth/backend/internal/db"
	"github.com/heyearth/backend/internal/middleware"
)

// «Приведи друга» API (cleanup plan, phase R).

// GET /api/v1/referral — the cabinet card: link, counters, rules, the «Код друга» field.
func handleReferral(w http.ResponseWriter, r *http.Request) {
	_, userDBID, ok := requireUser(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	st := biz.LoadReferralSettings(ctx, db.DB)
	out := map[string]any{"enabled": st.Enabled, "referrer_kop": st.ReferrerKop, "friend_kop": st.FriendKop,
		"min_purchase_kop": st.MinPurchaseKop}
	if !st.Enabled {
		writeJSON(w, http.StatusOK, out)
		return
	}
	code, err := biz.EnsureReferralCode(ctx, userDBID)
	if err != nil {
		log.Printf("[referral] code for %d: %v", userDBID, err)
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "Временно недоступно, попробуйте через минуту"})
		return
	}
	stats, err := biz.LoadReferralStats(ctx, userDBID)
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "Временно недоступно, попробуйте через минуту"})
		return
	}
	var bound bool
	var createdAt time.Time
	_ = db.DB.QueryRowContext(ctx, `SELECT referred_by_user_id IS NOT NULL, COALESCE(created_at, NOW()) FROM users WHERE id = $1`, userDBID).Scan(&bound, &createdAt)
	out["code"] = code
	out["link"] = publicBaseURL() + "/r/" + code
	out["stats"] = stats
	out["bound"] = bound
	out["can_bind"] = !bound && time.Since(createdAt) <= 7*24*time.Hour
	writeJSON(w, http.StatusOK, out)
}

// POST /api/v1/referral/bind {code} — the «Код друга» field.
func handleReferralBind(w http.ResponseWriter, r *http.Request) {
	_, userDBID, ok := requireUser(w, r)
	if !ok {
		return
	}
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	var body struct {
		Code string `json:"code"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	if err := biz.BindReferral(r.Context(), userDBID, body.Code, middleware.ExtractIP(r), true); err != nil {
		if biz.IsReferralError(err) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		log.Printf("[referral] bind %d: %v", userDBID, err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Не получилось — попробуйте через минуту"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"message": "Код друга принят: бонус придёт после первой оплаты подписки"})
}

// GET /api/v1/referral/terms — public: the landing's «Приведи друга — по N ₽ каждому».
func handleReferralTerms(w http.ResponseWriter, r *http.Request) {
	setCORS(w, r)
	st := biz.LoadReferralSettings(r.Context(), db.DB)
	writeJSON(w, http.StatusOK, map[string]any{"enabled": st.Enabled, "referrer_kop": st.ReferrerKop, "friend_kop": st.FriendKop,
		"min_purchase_kop": st.MinPurchaseKop})
}

// bindReferralOnRegister attaches a new user to the code their link carried; a bad or
// unknown code never fails the registration.
func bindReferralOnRegister(r *http.Request, userDBID int, code string) {
	if code == "" {
		return
	}
	if err := biz.BindReferral(r.Context(), userDBID, code, middleware.ExtractIP(r), false); err != nil {
		log.Printf("[referral] registration %d with code %q: %v", userDBID, code, err)
	}
}
