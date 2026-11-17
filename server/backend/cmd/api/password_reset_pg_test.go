package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/heyearth/backend/internal/biz"
	"github.com/heyearth/backend/internal/pb/heyearth_v1"
	"github.com/heyearth/backend/internal/testpg"
)

// testInitData signs Mini App launch data for the bot token the way Telegram does
// (internal/telegram.ValidateInitData).
func testInitData(token string, tgID int64) string {
	v := url.Values{}
	v.Set("auth_date", strconv.FormatInt(time.Now().Unix(), 10))
	v.Set("query_id", "AAtest")
	v.Set("user", fmt.Sprintf(`{"id":%d,"first_name":"Test","username":"reset_tester"}`, tgID))
	keys := make([]string, 0, len(v))
	for k := range v {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	lines := make([]string, 0, len(keys))
	for _, k := range keys {
		lines = append(lines, k+"="+v.Get(k))
	}
	mac := func(key, msg []byte) []byte { h := hmac.New(sha256.New, key); h.Write(msg); return h.Sum(nil) }
	secret := mac([]byte("WebAppData"), []byte(token))
	v.Set("hash", hex.EncodeToString(mac(secret, []byte(strings.Join(lines, "\n")))))
	return v.Encode()
}

// Obligatory change after a reset by support: signing in with the temporary password — on the
// site, in the Mini App, in the app (gRPC) — works and leaves password_must_change; the
// profile reports it; the new password must differ from the temporary one; the change clears
// the flag and keeps only the caller's session.
func TestTemporaryPasswordMustBeChanged(t *testing.T) {
	conn := testpg.Open(t)
	t.Setenv("JWT_SECRET", "test-secret-must-change")
	tgCfg.forget(tgBotTokenSetting)
	testpg.MustExec(t, conn, `INSERT INTO app_settings (key, value) VALUES ('tg_bot_token', $1)
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value`, fakeBotToken)
	t.Cleanup(func() { tgCfg.forget(tgBotTokenSetting) })
	ctx := context.Background()

	uid := testpg.NewUser(t, conn, "temp-user")
	if err := biz.SetCloudPassword("temp-user", "my-old-pass", "", ""); err != nil {
		t.Fatal(err)
	}
	if err := biz.SetUsername("temp-user", "temp_login"); err != nil {
		t.Fatal(err)
	}
	if p, _ := biz.GetUserProfile("temp-user"); p.MustChangePassword {
		t.Fatal("a fresh password asks to be changed")
	}
	res, err := biz.ResetPasswordByAdmin(ctx, uid)
	if err != nil {
		t.Fatal(err)
	}
	temp := res.TempPassword
	flag := func() bool {
		var v bool
		_ = conn.QueryRow(`SELECT password_must_change FROM users WHERE id = $1`, uid).Scan(&v)
		return v
	}

	post := func(h http.HandlerFunc, path, token string, body any) *httptest.ResponseRecorder {
		b, _ := json.Marshal(body)
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(string(b)))
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		rec := httptest.NewRecorder()
		h(rec, req)
		return rec
	}
	type session struct {
		Token   string          `json:"token"`
		Profile biz.UserProfile `json:"profile"`
	}

	// ── site: an empty password says «password required» (the site's own text), the
	//    temporary one signs in by login name and leaves the flag ──
	if rec := post(handleUserLogin, "/api/v1/auth/login", "", map[string]string{"id_or_username": "temp_login"}); rec.Code != http.StatusUnauthorized ||
		!strings.Contains(rec.Body.String(), "password required") {
		t.Errorf("empty password: %d %s", rec.Code, rec.Body)
	}
	rec := post(handleUserLogin, "/api/v1/auth/login", "", map[string]string{"id_or_username": "temp_login", "password": temp})
	if rec.Code != http.StatusOK {
		t.Fatalf("site login: %d %s", rec.Code, rec.Body)
	}
	var site session
	_ = json.Unmarshal(rec.Body.Bytes(), &site)
	if !site.Profile.MustChangePassword || !flag() {
		t.Errorf("site login: profile must_change=%v, flag=%v", site.Profile.MustChangePassword, flag())
	}

	// ── Mini App: «У меня есть HiEarth ID» with the temporary password ──
	rec = post(handleAuthTelegramLogin, "/api/v1/auth/telegram/login", "", map[string]string{
		"init_data": testInitData(fakeBotToken, 777001), "id_or_username": "temp-user", "password": temp,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("Mini App login: %d %s", rec.Code, rec.Body)
	}
	var tg session
	_ = json.Unmarshal(rec.Body.Bytes(), &tg)
	if tg.Token == "" || !tg.Profile.MustChangePassword || !flag() {
		t.Errorf("Mini App login: token %v, profile must_change=%v, flag=%v", tg.Token != "", tg.Profile.MustChangePassword, flag())
	}

	// ── app (gRPC) ──
	if _, err := (&server{}).Login(ctx, &heyearth_v1.LoginRequest{IdOrUsername: "temp-user", Password: temp}); err != nil {
		t.Fatalf("gRPC login: %v", err)
	}
	if !flag() {
		t.Error("gRPC login cleared the flag")
	}

	// ── the cabinet reads it from the profile ──
	get := httptest.NewRequest(http.MethodGet, "/api/v1/user/profile", nil)
	get.Header.Set("Authorization", "Bearer "+site.Token)
	prof := httptest.NewRecorder()
	handleUserProfile(prof, get)
	if prof.Code != http.StatusOK || !strings.Contains(prof.Body.String(), `"must_change_password":true`) {
		t.Fatalf("profile: %d %s", prof.Code, prof.Body)
	}

	patch := func(token string, body map[string]string) *httptest.ResponseRecorder {
		b, _ := json.Marshal(body)
		req := httptest.NewRequest(http.MethodPatch, "/api/v1/user/profile", strings.NewReader(string(b)))
		req.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()
		handleUserProfile(rec, req)
		return rec
	}
	// The temporary password again as the new one: refused, the flag stays.
	if rec := patch(site.Token, map[string]string{"new_password": temp, "current_password": temp}); rec.Code != http.StatusBadRequest ||
		!strings.Contains(rec.Body.String(), biz.ErrSameAsTemporaryPassword.Error()) || !flag() {
		t.Errorf("same as temporary: %d %s flag=%v", rec.Code, rec.Body, flag())
	}
	if rec := patch(site.Token, map[string]string{"new_password": "brand-new-pass", "current_password": "wrong"}); rec.Code != http.StatusBadRequest || !flag() {
		t.Errorf("wrong temporary: %d %s flag=%v", rec.Code, rec.Body, flag())
	}
	rec = patch(site.Token, map[string]string{"new_password": "brand-new-pass", "current_password": temp})
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"must_change_password":false`) || flag() {
		t.Fatalf("change: %d %s flag=%v", rec.Code, rec.Body, flag())
	}
	// Only the caller's session survives; the new password signs in, the temporary one not.
	// (Counted by token: two sign-ins of one user in the same second get the same JWT today,
	// so the Mini App or app session may literally be the caller's token.)
	var others, mine int
	_ = conn.QueryRow(`SELECT COUNT(*) FILTER (WHERE token_hash <> encode(sha256(convert_to($2, 'UTF8')), 'hex')),
		COUNT(*) FILTER (WHERE token_hash = encode(sha256(convert_to($2, 'UTF8')), 'hex'))
		FROM user_sessions WHERE user_id = $1`, uid, site.Token).Scan(&others, &mine)
	if others != 0 || mine == 0 {
		t.Errorf("after the change: %d other sessions left, caller's kept=%v", others, mine > 0)
	}
	if _, err := biz.LoginByIDOrUsername("temp_login", temp); err == nil {
		t.Error("the temporary password still logs in")
	}
	if u, err := biz.LoginByIDOrUsername("temp_login", "brand-new-pass"); err != nil || u.PasswordMustChange {
		t.Errorf("new password: %v", err)
	}
	// An ordinary later change does not bring the "differ from temporary" rule back.
	if err := biz.SetCloudPassword("temp-user", "brand-new-pass-2", "brand-new-pass", ""); err != nil {
		t.Errorf("ordinary change: %v", err)
	}
}
