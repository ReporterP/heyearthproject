package admin

import (
	"net/http"
	"strings"
	"time"

	"github.com/heyearth/backend/internal/biz"
	"github.com/heyearth/backend/internal/db"
)

// «Платежи → Не прошло продление» (C10, the dunning page's place after the wallet): paid
// subscriptions with auto-renew on that the wallet cannot renew — already ended in the last 7
// days, or ending within 3 days. The owner sees who to remind; the client sees the same
// «пополните кошелёк» in the cabinet and the app.
const failingRenewalsQuery = `
	SELECT u.id, u.user_id, COALESCE(p.name, ''), us.valid_until, COALESCE(p.price_rub, 0)::bigint * 100,
	       COALESCE(w.balance_kop, 0), us.valid_until < NOW()
	  FROM user_subscriptions us
	  JOIN users u ON u.id = us.user_id
	  JOIN plans p ON p.id = us.plan_id
	  LEFT JOIN user_wallets w ON w.user_id = u.id
	 WHERE us.auto_renew AND COALESCE(p.price_rub, 0) > 0 AND NOT COALESCE(u.is_blocked, FALSE)
	   AND us.valid_until BETWEEN NOW() - INTERVAL '7 days' AND NOW() + INTERVAL '3 days'
	   AND COALESCE(w.balance_kop, 0) < COALESCE(p.price_rub, 0)::bigint * 100
	   AND NOT EXISTS (SELECT 1 FROM user_subscriptions n WHERE n.user_id = us.user_id AND n.id <> us.id AND n.valid_until > us.valid_until)
	 ORDER BY us.valid_until`

type failingRenewal struct {
	UserDBID     int       `json:"user_db_id"`
	UserID       string    `json:"user_id"`
	Plan         string    `json:"plan"`
	ValidUntil   time.Time `json:"valid_until"`
	PriceKop     int64     `json:"price_kop"`
	BalanceKop   int64     `json:"balance_kop"`
	ShortfallKop int64     `json:"shortfall_kop"`
	Ended        bool      `json:"ended"`
}

// FailingRenewalsHandler — GET /api/admin/renewals/failing.
func FailingRenewalsHandler(w http.ResponseWriter, r *http.Request) {
	rows, err := db.DB.QueryContext(r.Context(), failingRenewalsQuery)
	if err != nil {
		writeAdminError(w, http.StatusInternalServerError, "Не удалось загрузить продления")
		return
	}
	defer rows.Close()
	items := []failingRenewal{}
	for rows.Next() {
		var f failingRenewal
		if err := rows.Scan(&f.UserDBID, &f.UserID, &f.Plan, &f.ValidUntil, &f.PriceKop, &f.BalanceKop, &f.Ended); err != nil {
			writeAdminError(w, http.StatusInternalServerError, "Не удалось прочитать продления")
			return
		}
		f.ShortfallKop = f.PriceKop - f.BalanceKop
		items = append(items, f)
	}
	if err := rows.Err(); err != nil {
		writeAdminError(w, http.StatusInternalServerError, "Не удалось загрузить продления")
		return
	}
	writeAdminJSON(w, http.StatusOK, map[string]any{"items": items, "total": len(items)})
}

// FeaturesHandler — GET /api/admin/features: the switches the menu follows (any admin).
func FeaturesHandler(w http.ResponseWriter, r *http.Request) {
	writeAdminJSON(w, http.StatusOK, map[string]bool{
		"b2b": strings.EqualFold(strings.TrimSpace(biz.GetSettingOrDefault("b2b_enabled", "false")), "true"),
	})
}
