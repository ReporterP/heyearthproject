package admin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/heyearth/backend/internal/biz"
	"github.com/heyearth/backend/internal/db"
)

// Wallet in the admin (cleanup plan W6): the user card shows the balance and the
// ledger; «Начислить» / «Списать» need the wallet.adjust permission, a reason, and land
// in the audit log. Refunds of top-ups go through the payment refund (cmd/api/refunds.go).

const permWalletAdjust = "wallet.adjust"

// userIDFromWalletPath parses /api/admin/users/{id}/wallet[/adjust].
func userIDFromWalletPath(path string) (int, bool) {
	p := strings.Trim(strings.TrimPrefix(path, "/api/admin/users/"), "/")
	idPart, _, _ := strings.Cut(p, "/")
	id, err := strconv.Atoi(idPart)
	return id, err == nil && id > 0
}

// UserWalletHandler — GET /api/admin/users/{id}/wallet.
func UserWalletHandler(w http.ResponseWriter, r *http.Request) {
	id, ok := userIDFromWalletPath(r.URL.Path)
	if !ok {
		writeAdminError(w, http.StatusBadRequest, "Invalid user id")
		return
	}
	balance, err := biz.WalletBalance(r.Context(), id)
	if err != nil {
		writeAdminError(w, http.StatusInternalServerError, "Failed to read the wallet")
		return
	}
	txs, err := biz.ListWalletTransactions(r.Context(), id, 200)
	if err != nil {
		writeAdminError(w, http.StatusInternalServerError, "Failed to read the wallet")
		return
	}
	writeAdminJSON(w, http.StatusOK, map[string]any{"balance_kop": balance, "transactions": txs})
}

// UserWalletAdjustHandler — POST /api/admin/users/{id}/wallet/adjust
// {"direction": "credit"|"debit", "amount_rub": 100.5, "reason": "...", "request_id": "..."}.
func UserWalletAdjustHandler(w http.ResponseWriter, r *http.Request) {
	id, ok := userIDFromWalletPath(r.URL.Path)
	if !ok {
		writeAdminError(w, http.StatusBadRequest, "Invalid user id")
		return
	}
	var body struct {
		Direction string  `json:"direction"`
		AmountRub float64 `json:"amount_rub"`
		Reason    string  `json:"reason"`
		RequestID string  `json:"request_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeAdminError(w, http.StatusBadRequest, "Invalid body")
		return
	}
	reason := strings.TrimSpace(body.Reason)
	if reason == "" || len([]rune(reason)) > 300 {
		writeAdminError(w, http.StatusBadRequest, "Укажите причину (до 300 символов)")
		return
	}
	kop, err := biz.ParseRubToKop(strconv.FormatFloat(body.AmountRub, 'f', -1, 64))
	if err != nil || kop <= 0 || kop > 100_000_00 {
		writeAdminError(w, http.StatusBadRequest, "Сумма — от 0,01 до 100 000 ₽, не больше двух знаков после запятой")
		return
	}
	kind := biz.WalletAdminCredit
	delta := kop
	switch body.Direction {
	case "credit":
	case "debit":
		kind, delta = biz.WalletAdminDebit, -kop
	default:
		writeAdminError(w, http.StatusBadRequest, "direction: credit или debit")
		return
	}
	rid := strings.TrimSpace(body.RequestID)
	if rid == "" || len(rid) > 64 {
		writeAdminError(w, http.StatusBadRequest, "request_id required")
		return
	}
	claims, _ := ClaimsFromContext(r.Context())
	adminID := 0
	if claims != nil {
		adminID = claims.AdminID
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	tx, err := db.DB.BeginTx(ctx, nil)
	if err != nil {
		writeAdminError(w, http.StatusInternalServerError, "Database error")
		return
	}
	defer tx.Rollback() //nolint:errcheck
	var exists bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM users WHERE id = $1)`, id).Scan(&exists); err != nil || !exists {
		writeAdminError(w, http.StatusNotFound, "Пользователь не найден")
		return
	}
	entry, err := biz.WalletApply(ctx, tx, id, delta, kind, fmt.Sprintf("admin:%d:%s", adminID, rid),
		biz.WalletRefs{AdminID: adminID, Comment: reason})
	if errors.Is(err, biz.ErrInsufficientFunds) {
		writeAdminError(w, http.StatusBadRequest, "На кошельке меньше этой суммы — списать нельзя")
		return
	}
	if err != nil {
		writeAdminError(w, http.StatusInternalServerError, "Не удалось изменить баланс: "+err.Error())
		return
	}
	if err := tx.Commit(); err != nil {
		writeAdminError(w, http.StatusInternalServerError, "Database error")
		return
	}
	if !entry.Replayed {
		WriteAuditLog(r, "wallet."+body.Direction, "user", strconv.Itoa(id), map[string]any{
			"amount_kop": kop, "reason": reason, "balance_after_kop": entry.BalanceAfterKop, "wallet_tx_id": entry.TxID,
		})
	}
	writeAdminJSON(w, http.StatusOK, map[string]any{"balance_kop": entry.BalanceAfterKop, "wallet_tx_id": entry.TxID})
}

// walletStats is the dashboard's wallet block: what users hold (liabilities) and this
// month's top-ups and spending, in kopecks.
type walletStats struct {
	LiabilitiesKop int64 `json:"liabilities_kop"`
	TopupsMonthKop int64 `json:"topups_month_kop"`
	SpentMonthKop  int64 `json:"spent_month_kop"`
	WalletsNonZero int   `json:"wallets_non_zero"`
}

func loadWalletStats(ctx context.Context) (walletStats, error) {
	var s walletStats
	err := db.DB.QueryRowContext(ctx, `
		SELECT COALESCE((SELECT SUM(balance_kop) FROM user_wallets), 0),
		       COALESCE((SELECT COUNT(*) FROM user_wallets WHERE balance_kop > 0), 0),
		       COALESCE((SELECT SUM(amount_kop) FROM wallet_transactions WHERE kind = 'topup' AND created_at >= date_trunc('month', NOW())), 0),
		       COALESCE((SELECT -SUM(amount_kop) FROM wallet_transactions WHERE kind IN ('purchase', 'auto_renew') AND created_at >= date_trunc('month', NOW())), 0)`).
		Scan(&s.LiabilitiesKop, &s.WalletsNonZero, &s.TopupsMonthKop, &s.SpentMonthKop)
	return s, err
}

// WalletStatsHandler — GET /api/admin/wallet-stats (dashboard).
func WalletStatsHandler(w http.ResponseWriter, r *http.Request) {
	s, err := loadWalletStats(r.Context())
	if err != nil {
		writeAdminError(w, http.StatusInternalServerError, "Failed to read wallet stats")
		return
	}
	writeAdminJSON(w, http.StatusOK, s)
}
