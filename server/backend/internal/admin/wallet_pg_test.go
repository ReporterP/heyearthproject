package admin

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/heyearth/backend/internal/biz"
	"github.com/heyearth/backend/internal/testpg"
)

// W6: «Начислить» / «Списать» with a reason; a debit never overdraws; the same request
// twice changes the balance once.
func TestWalletAdjust(t *testing.T) {
	conn := testpg.Open(t)
	uid := testpg.NewUser(t, conn, "card-user")
	call := func(body map[string]any) *httptest.ResponseRecorder {
		b, _ := json.Marshal(body)
		rec := httptest.NewRecorder()
		UserWalletAdjustHandler(rec, httptest.NewRequest(http.MethodPost, "/api/admin/users/"+strconv.Itoa(uid)+"/wallet/adjust", bytes.NewReader(b)))
		return rec
	}
	if rec := call(map[string]any{"direction": "credit", "amount_rub": 100, "reason": "", "request_id": "r0"}); rec.Code != http.StatusBadRequest {
		t.Errorf("no reason: %d", rec.Code)
	}
	for i := 0; i < 2; i++ {
		if rec := call(map[string]any{"direction": "credit", "amount_rub": 100.5, "reason": "компенсация", "request_id": "r1"}); rec.Code != http.StatusOK {
			t.Fatalf("credit: %d %s", rec.Code, rec.Body.String())
		}
	}
	if rec := call(map[string]any{"direction": "debit", "amount_rub": 200, "reason": "ошибка", "request_id": "r2"}); rec.Code != http.StatusBadRequest {
		t.Errorf("overdraft debit: %d", rec.Code)
	}
	if rec := call(map[string]any{"direction": "debit", "amount_rub": 0.5, "reason": "ошибка", "request_id": "r3"}); rec.Code != http.StatusOK {
		t.Errorf("debit: %d %s", rec.Code, rec.Body.String())
	}
	b, _ := biz.WalletBalance(t.Context(), uid)
	if b != 10000 {
		t.Errorf("balance %d, want 10000", b)
	}
	// the reason is internal (audit log); the client sees «… HiEarth» or the text written for them
	txs, _ := biz.ListWalletTransactions(t.Context(), uid, 10)
	if len(txs) != 2 || txs[0].Title != "Списание HiEarth" || txs[1].Title != "Начисление от HiEarth" {
		t.Errorf("ledger %+v", txs)
	}
	if rec := call(map[string]any{"direction": "credit", "amount_rub": 50, "reason": "тикет 42, сбой 18.11", "client_text": "Компенсация за перерыв 18 ноября", "request_id": "r4"}); rec.Code != http.StatusOK {
		t.Fatalf("credit with a client text: %d %s", rec.Code, rec.Body.String())
	}
	txs, _ = biz.ListWalletTransactions(t.Context(), uid, 10)
	if txs[0].Title != "Компенсация за перерыв 18 ноября" {
		t.Errorf("client text: %q", txs[0].Title)
	}
	for _, tx := range txs {
		if strings.Contains(tx.Title, "тикет") || strings.Contains(tx.Title, "ошибка") || strings.Contains(tx.Title, "компенсация") {
			t.Errorf("the internal reason reached the client: %q", tx.Title)
		}
	}
}
