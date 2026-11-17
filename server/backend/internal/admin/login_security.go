package admin

// Admin sign-in hardening (security audit 2026-11-18, migration 000088):
//   - lockout: adminMaxFailures wrong passwords or codes in a row lock the account for
//     adminLockout; the answer during a lock is the same whatever the password, so a lock does
//     not confirm a guess. The counter resets on a successful sign-in.
//   - an unknown username costs the same bcrypt time as a known one (no username probing).
//   - TOTP (RFC 6238: HMAC-SHA1, 30 s, 6 digits, ±1 step), opt-in per admin from «Настройки»;
//     a code is accepted once (totp_last_step). The secret is stored encrypted (internal/crypto).
// Recovery when the phone is lost: docs/runbooks/ddos.md («Админка: 2FA») — SQL on the Hub.

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"database/sql"
	"encoding/base32"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	qrcode "github.com/skip2/go-qrcode"
	"golang.org/x/crypto/bcrypt"

	"github.com/heyearth/backend/internal/crypto"
	"github.com/heyearth/backend/internal/db"
	"github.com/heyearth/backend/internal/middleware"
)

const (
	adminMaxFailures = 5
	adminLockout     = 15 * time.Minute
	totpPeriod       = 30
	totpIssuer       = "HiEarth admin"
)

// dummyAdminHash is compared against when the username does not exist (same cost as a real one).
var dummyAdminHash, _ = bcrypt.GenerateFromPassword([]byte("not-a-real-password-hiearth"), bcrypt.DefaultCost)

type adminLoginState struct {
	failed      int
	lockedUntil sql.NullTime
	totpEnabled bool
	totpSecret  []byte
	totpLast    int64
}

func loadAdminLoginState(id int) (adminLoginState, error) {
	var s adminLoginState
	err := db.DB.QueryRow(`SELECT failed_logins, locked_until, totp_enabled, totp_secret_enc, totp_last_step FROM admin_users WHERE id = $1`, id).
		Scan(&s.failed, &s.lockedUntil, &s.totpEnabled, &s.totpSecret, &s.totpLast)
	return s, err
}

func (s adminLoginState) locked(now time.Time) bool {
	return s.lockedUntil.Valid && s.lockedUntil.Time.After(now)
}

// recordAdminFailure counts one failed attempt and locks the account at adminMaxFailures.
func recordAdminFailure(id int) {
	_, _ = db.DB.Exec(`
		UPDATE admin_users SET
			failed_logins = CASE WHEN failed_logins + 1 >= $2 THEN 0 ELSE failed_logins + 1 END,
			locked_until  = CASE WHEN failed_logins + 1 >= $2 THEN NOW() + make_interval(secs => $3) ELSE locked_until END
		WHERE id = $1`, id, adminMaxFailures, int(adminLockout.Seconds()))
}

// writeLoginAudit records a sign-in event (no session yet, so the admin is named explicitly).
func writeLoginAudit(r *http.Request, adminID int, username, action string, details any) {
	var dj []byte
	if details != nil {
		dj, _ = json.Marshal(details)
	}
	_, _ = auditLogExec(`
		INSERT INTO admin_audit_log (admin_id, admin_username, action, target_type, target_id, details, ip_address)
		VALUES ($1, $2, $3, 'admin_user', $4, $5, $6)`, adminID, username, action, fmt.Sprint(adminID), dj, middleware.ExtractIP(r))
}

func resetAdminFailures(id int) {
	_, _ = db.DB.Exec(`UPDATE admin_users SET failed_logins = 0, locked_until = NULL WHERE id = $1`, id)
}

// ── TOTP ──

func newTOTPSecret() (string, error) {
	b := make([]byte, 20)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b), nil
}

func totpCode(secret string, step int64) (string, error) {
	key, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(strings.ToUpper(strings.TrimSpace(secret)))
	if err != nil {
		return "", err
	}
	var msg [8]byte
	binary.BigEndian.PutUint64(msg[:], uint64(step))
	mac := hmac.New(sha1.New, key)
	mac.Write(msg[:])
	sum := mac.Sum(nil)
	off := sum[len(sum)-1] & 0x0f
	v := (binary.BigEndian.Uint32(sum[off:off+4]) & 0x7fffffff) % 1000000
	return fmt.Sprintf("%06d", v), nil
}

// verifyTOTP returns the matching step (now ±1) of code, or 0. Steps at or below last are refused
// (a code works once).
func verifyTOTP(secret, code string, now time.Time, last int64) int64 {
	code = strings.ReplaceAll(strings.TrimSpace(code), " ", "")
	if len(code) != 6 {
		return 0
	}
	cur := now.Unix() / totpPeriod
	for _, st := range []int64{cur - 1, cur, cur + 1} {
		if st <= last {
			continue
		}
		want, err := totpCode(secret, st)
		if err == nil && hmac.Equal([]byte(want), []byte(code)) {
			return st
		}
	}
	return 0
}

func decryptTOTP(enc []byte) (string, error) {
	if len(enc) == 0 {
		return "", fmt.Errorf("no TOTP secret")
	}
	return crypto.Decrypt(enc)
}

// ── /api/admin/me/totp ──

type totpStatusResponse struct {
	Enabled bool `json:"enabled"`
}

// TOTPStatusHandler: GET — whether the signed-in admin has 2FA on.
func TOTPStatusHandler(w http.ResponseWriter, r *http.Request) {
	claims, ok := ClaimsFromContext(r.Context())
	if !ok {
		writeAdminError(w, http.StatusUnauthorized, "Missing admin session")
		return
	}
	s, err := loadAdminLoginState(claims.AdminID)
	if err != nil {
		writeAdminError(w, http.StatusInternalServerError, "Не удалось прочитать настройки входа")
		return
	}
	writeAdminJSON(w, http.StatusOK, totpStatusResponse{Enabled: s.totpEnabled})
}

// TOTPSetupHandler: POST — a new secret for the signed-in admin (pending until confirmed with a
// code). Answers the secret, the otpauth:// link and a QR code of it.
func TOTPSetupHandler(w http.ResponseWriter, r *http.Request) {
	claims, ok := ClaimsFromContext(r.Context())
	if !ok {
		writeAdminError(w, http.StatusUnauthorized, "Missing admin session")
		return
	}
	if crypto.Ready() != nil {
		writeAdminError(w, http.StatusServiceUnavailable, "На сервере нет ключа шифрования (SSH_ENCRYPTION_KEY) — 2FA недоступна")
		return
	}
	secret, err := newTOTPSecret()
	if err != nil {
		writeAdminError(w, http.StatusInternalServerError, "Не удалось создать ключ")
		return
	}
	enc, err := crypto.Encrypt(secret)
	if err != nil {
		writeAdminError(w, http.StatusInternalServerError, "Не удалось зашифровать ключ")
		return
	}
	if _, err := db.DB.Exec(`UPDATE admin_users SET totp_pending_enc = $2 WHERE id = $1`, claims.AdminID, enc); err != nil {
		writeAdminError(w, http.StatusInternalServerError, "Не удалось сохранить ключ")
		return
	}
	label := url.PathEscape(totpIssuer + ":" + claims.Username)
	uri := fmt.Sprintf("otpauth://totp/%s?secret=%s&issuer=%s&period=%d&digits=6&algorithm=SHA1", label, secret, url.QueryEscape(totpIssuer), totpPeriod)
	png, err := qrcode.Encode(uri, qrcode.Medium, 256)
	if err != nil {
		writeAdminError(w, http.StatusInternalServerError, "Не удалось построить QR-код")
		return
	}
	writeAdminJSON(w, http.StatusOK, map[string]string{
		"secret": secret,
		"uri":    uri,
		"qr":     "data:image/png;base64," + base64.StdEncoding.EncodeToString(png),
	})
}

type totpCodeRequest struct {
	Code     string `json:"code"`
	Password string `json:"password"`
}

// TOTPEnableHandler: POST {code} — confirms the pending secret with a code from the app.
func TOTPEnableHandler(w http.ResponseWriter, r *http.Request) {
	claims, ok := ClaimsFromContext(r.Context())
	if !ok {
		writeAdminError(w, http.StatusUnauthorized, "Missing admin session")
		return
	}
	var req totpCodeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeAdminError(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	var pending []byte
	if err := db.DB.QueryRow(`SELECT totp_pending_enc FROM admin_users WHERE id = $1`, claims.AdminID).Scan(&pending); err != nil || len(pending) == 0 {
		writeAdminError(w, http.StatusBadRequest, "Сначала получите новый ключ")
		return
	}
	secret, err := decryptTOTP(pending)
	if err != nil {
		writeAdminError(w, http.StatusInternalServerError, "Не удалось прочитать ключ")
		return
	}
	step := verifyTOTP(secret, req.Code, time.Now(), 0)
	if step == 0 {
		writeAdminError(w, http.StatusBadRequest, "Код не подошёл — проверьте время на телефоне и введите новый код")
		return
	}
	res, err := db.DB.Exec(`UPDATE admin_users SET totp_secret_enc = totp_pending_enc, totp_pending_enc = NULL, totp_enabled = TRUE, totp_last_step = $2
		WHERE id = $1 AND totp_pending_enc = $3`, claims.AdminID, step, pending)
	if n, _ := res.RowsAffected(); err != nil || n != 1 {
		writeAdminError(w, http.StatusConflict, "Ключ изменился — получите новый и повторите")
		return
	}
	WriteAuditLog(r, "admin.totp_enable", "admin_user", fmt.Sprint(claims.AdminID), nil)
	writeAdminJSON(w, http.StatusOK, totpStatusResponse{Enabled: true})
}

// TOTPDisableHandler: POST {password, code} — turns 2FA off for the signed-in admin.
func TOTPDisableHandler(w http.ResponseWriter, r *http.Request) {
	claims, ok := ClaimsFromContext(r.Context())
	if !ok {
		writeAdminError(w, http.StatusUnauthorized, "Missing admin session")
		return
	}
	var req totpCodeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeAdminError(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	record, err := loadAdminRecordByID(claims.AdminID)
	if err != nil {
		writeAdminError(w, http.StatusInternalServerError, "Failed to load admin")
		return
	}
	s, err := loadAdminLoginState(claims.AdminID)
	if err != nil {
		writeAdminError(w, http.StatusInternalServerError, "Failed to load admin")
		return
	}
	if bcrypt.CompareHashAndPassword([]byte(record.PasswordHash), []byte(req.Password)) != nil {
		recordAdminFailure(claims.AdminID)
		writeAdminError(w, http.StatusForbidden, "Неверный пароль")
		return
	}
	if s.totpEnabled {
		secret, err := decryptTOTP(s.totpSecret)
		if err != nil || verifyTOTP(secret, req.Code, time.Now(), s.totpLast) == 0 {
			recordAdminFailure(claims.AdminID)
			writeAdminError(w, http.StatusForbidden, "Код не подошёл")
			return
		}
	}
	if _, err := db.DB.Exec(`UPDATE admin_users SET totp_enabled = FALSE, totp_secret_enc = NULL, totp_pending_enc = NULL WHERE id = $1`, claims.AdminID); err != nil {
		writeAdminError(w, http.StatusInternalServerError, "Не удалось выключить 2FA")
		return
	}
	WriteAuditLog(r, "admin.totp_disable", "admin_user", fmt.Sprint(claims.AdminID), nil)
	writeAdminJSON(w, http.StatusOK, totpStatusResponse{Enabled: false})
}
