package main

import (
	"context"
	"strings"
	"testing"

	"github.com/heyearth/backend/internal/testpg"
)

// A free-days code applied on a plan page may grant another plan: the hint names the plan the
// person gets, not just «N дн. бесплатно».
func TestQuoteFreeTrialHintNamesTheGrantedPlan(t *testing.T) {
	conn := testpg.Open(t)
	uid := testpg.NewUser(t, conn, "quote-trial-user")
	var cheap, granted int
	for _, p := range []struct {
		slug, name string
		price      int
		id         *int
	}{{"cheap", "Дешёвый", 250, &cheap}, {"full", "Всё включено", 300, &granted}} {
		if err := conn.QueryRow(`INSERT INTO plans (slug, name, description, price_rub, duration_days, allowed_protocols, is_active)
			VALUES ($1, $2, '', $3, 30, ARRAY['all'], TRUE) RETURNING id`, p.slug, p.name, p.price).Scan(p.id); err != nil {
			t.Fatal(err)
		}
	}
	testpg.MustExec(t, conn, `INSERT INTO promocodes (code, duration_days, promo_type, max_uses, free_days, plan_id_granted)
		VALUES ('TRIAL14', 14, 'free_trial', 100, 14, $1)`, granted)
	q, err := buildQuote(context.Background(), "quote-trial-user", uid, cheap, "TRIAL14")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(q.PromoMessage, "«Всё включено»") || !strings.Contains(q.PromoMessage, "14") {
		t.Fatalf("hint %q: want the 14 days and the plan «Всё включено»", q.PromoMessage)
	}
}
