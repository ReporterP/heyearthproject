package biz

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/heyearth/backend/internal/db"
	"github.com/lib/pq"
)

// GetUserHysteria2Password returns the per-user Hysteria2 password.
// Generates one (32 random bytes hex-encoded) on first call.
// Mirrors the GetUserVlessUUID pattern from internal/biz/user.go.
func GetUserHysteria2Password(userID string) (string, error) {
	user, err := GetOrCreateUser(userID)
	if err != nil {
		return "", err
	}

	var pw string
	if err := db.DB.QueryRow(
		`SELECT COALESCE(hysteria2_password, '') FROM users WHERE id = $1`,
		user.ID,
	).Scan(&pw); err != nil {
		return "", fmt.Errorf("load hysteria2_password: %w", err)
	}
	if pw != "" {
		return pw, nil
	}

	pw, err = generateHysteria2Password()
	if err != nil {
		return "", err
	}
	if _, err := db.DB.Exec(
		`UPDATE users SET hysteria2_password = $1 WHERE id = $2`, pw, user.ID,
	); err != nil {
		return "", fmt.Errorf("persist hysteria2_password: %w", err)
	}
	return pw, nil
}

// GetHysteria2ObfsPassword returns the shared Salamander obfuscation password,
// "" when it cannot be read or created (see Hysteria2ObfsPassword; callers
// treat "" as unavailable).
func GetHysteria2ObfsPassword() string {
	pw, err := Hysteria2ObfsPassword()
	if err != nil {
		return ""
	}
	return pw
}

// Hysteria2ObfsPassword returns the shared Salamander obfuscation password.
// Generated lazily on first call and persisted in app_settings — but only when
// the key is really absent (sql.ErrNoRows or blank): a failed read is returned
// as an error. Generating on a read failure (the former GetSettingOrDefault
// path) could overwrite the live password with a new one, breaking every
// Hysteria2 client until the server config and the cached profiles caught up.
func Hysteria2ObfsPassword() (string, error) {
	pw, err := GetSetting("hysteria2_obfs_password")
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("read hysteria2_obfs_password: %w", err)
	}
	if pw = strings.TrimSpace(pw); pw != "" {
		return pw, nil
	}
	if pw, err = generateHysteria2Password(); err != nil {
		return "", err
	}
	if err := SetSetting("hysteria2_obfs_password", pw); err != nil {
		return "", fmt.Errorf("persist hysteria2_obfs_password: %w", err)
	}
	return pw, nil
}

func generateHysteria2Password() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("rand.Read: %w", err)
	}
	return hex.EncodeToString(buf), nil
}

// Hysteria2AuthResult is the outcome of an auth-backend check.
// Fields mirror what Hysteria2 returns to its server (id is shown in logs/metrics).
type Hysteria2AuthResult struct {
	OK     bool
	UserID string
	PlanID int
	// Per-plan bandwidth limits sent back to Hysteria2 via the response so the
	// server enforces Brutal targets without restart.
	UpMbps   int
	DownMbps int
}

// ValidateHysteria2Auth checks an incoming Hysteria2 password against active premium
// subscriptions. Returns ok=true with the user's plan limits when authorized.
//
// Authorization rules:
//   - Password matches a user with a non-blocked account.
//   - User has an active (valid_until > NOW) subscription whose plan contains
//     'hysteria2' in allowed_protocols AND has express_enabled = TRUE.
//   - Traffic limit (if any) is not exceeded.
//
// The cycle quota here (IsCycleExceeded) is enforced independently of the
// app_settings flag quota_cutoff_enabled, which only arms the xray_sync cutoff
// for Reality/XHTTP. This is pre-existing behaviour and intentionally unchanged:
// gating it would silently let over-quota Express users back in. The subscription
// that decides the quota is the same effective one as in the xray cutoff
// (effectiveSubOrderBy).
func ValidateHysteria2Auth(password string) Hysteria2AuthResult {
	password = strings.TrimSpace(password)
	if password == "" || len(password) != 64 {
		return Hysteria2AuthResult{}
	}

	var userPK int
	var userID string
	if err := db.DB.QueryRow(
		`SELECT id, user_id FROM users
		 WHERE hysteria2_password = $1 AND is_blocked = FALSE
		 LIMIT 1`,
		password,
	).Scan(&userPK, &userID); err != nil {
		return Hysteria2AuthResult{}
	}

	var planID int
	var allowed []string
	var enabled bool
	var up, down int
	err := db.DB.QueryRow(hysteria2AuthPlanQuery, userPK).Scan(&planID, pq.Array(&allowed), &enabled, &up, &down)
	if err != nil {
		return Hysteria2AuthResult{}
	}

	if !enabled {
		return Hysteria2AuthResult{}
	}
	if !containsProto(allowed, "hysteria2") {
		return Hysteria2AuthResult{}
	}

	// Cycle-quota gate. Express traffic flows into traffic_stats via the
	// hysteria2-stats collector; IsCycleExceeded reuses the same machinery
	// that Xray-side cycle limits use, so over-quota Express users are
	// rejected automatically within ~60s of crossing their limit.
	if exceeded, _ := IsCycleExceeded(userPK, userID); exceeded {
		return Hysteria2AuthResult{OK: false}
	}

	return Hysteria2AuthResult{
		OK:       true,
		UserID:   userID,
		PlanID:   planID,
		UpMbps:   up,
		DownMbps: down,
	}
}

// hysteria2AuthPlanQuery picks the plan row that authorizes an Express login
// (us.user_id = $1, users.id). Ordered by effectiveSubOrderBy; with the inner
// JOIN plans its first key is constant (plan_id is never NULL here), so compared
// with the former `ORDER BY us.valid_until DESC` only the tie-break on equal
// valid_until (newest row) is new and the same users are authorized. The
// status = 'active' and legacy traffic_used filters are kept on purpose: they
// can skip the effective subscription and fall back to an older plan row, so
// the Express gate may judge a different row than the routing/quota code.
var hysteria2AuthPlanQuery = `
		SELECT p.id, p.allowed_protocols, p.express_enabled,
		       p.express_up_mbps, p.express_down_mbps
		FROM user_subscriptions us
		JOIN plans p ON p.id = us.plan_id
		WHERE us.user_id = $1
		  AND us.valid_until > NOW()
		  AND us.status = 'active'
		  AND (us.traffic_limit IS NULL OR us.traffic_used < us.traffic_limit)
		ORDER BY ` + effectiveSubOrderBy("us") + `
		LIMIT 1
	`

// hysteria2PlanGateQuery is the plan row of CanUseHysteria2 (u.user_id = $1,
// users.user_id). Same ordering note as hysteria2AuthPlanQuery: only the
// tie-break is new; status = 'active' is kept, the traffic_used filter is
// deliberately absent (quota is enforced at auth time).
var hysteria2PlanGateQuery = `
		SELECT p.allowed_protocols, p.express_enabled
		FROM user_subscriptions us
		JOIN users u ON u.id = us.user_id
		JOIN plans p ON p.id = us.plan_id
		WHERE u.user_id = $1 AND us.valid_until > NOW() AND us.status = 'active'
		ORDER BY ` + effectiveSubOrderBy("us") + `
		LIMIT 1
	`

// CanUseHysteria2 — the same plan gate as ValidateHysteria2Auth (an active subscription
// whose plan has express_enabled AND 'hysteria2'/'all' allowed). Builders use it so
// «Бамбук» is never offered to a plan the auth backend would reject. Quota and the
// blocked flag are NOT checked here: they are enforced at auth time.
//
// (false, nil) means "this user has no such plan" (no active subscription included).
// A database failure is returned as an error and must not be read as "no": callers
// answer 503, otherwise a subscription fetched during a DB blip would silently drop
// the Bamboo profiles and clients would overwrite their cached copy.
func CanUseHysteria2(userID string) (bool, error) {
	var allowed []string
	var enabled bool
	err := db.DB.QueryRow(hysteria2PlanGateQuery, userID).Scan(pq.Array(&allowed), &enabled)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("hysteria2 plan gate: %w", err)
	}
	return enabled && containsProto(allowed, "hysteria2"), nil
}

// Hysteria2Status — snapshot of the Express subsystem state used by the admin UI.
type Hysteria2Status struct {
	BinaryInstalled bool   `json:"binary_installed"`
	ConfigPresent   bool   `json:"config_present"`
	ConfigVersion   string `json:"config_version"`
	LastSyncAt      int64  `json:"last_sync_at"` // unix seconds, 0 if never
	LastError       string `json:"last_error,omitempty"`
	MaxUpMbps       int    `json:"max_up_mbps"`
	MaxDownMbps     int    `json:"max_down_mbps"`
	ActivePlans     int    `json:"active_plans"` // count of plans with express_enabled=true
}

// GetHysteria2Status reads the persisted sync snapshot from app_settings and a
// few derived facts (config file presence, binary on PATH). It does not poll
// `systemctl` — that requires elevated permissions and isn't always reliable.
//
// `binary_installed` is best-effort: it relies on the api process and the
// hysteria binary being on the same host (true for Hub deployments).
func GetHysteria2Status() Hysteria2Status {
	_, errCfg := os.Stat("/etc/hysteria/server.yaml")
	_, errBin := exec.LookPath("hysteria")

	lastSyncStr := GetSettingOrDefault("hysteria2_last_sync_at", "0")
	var lastSync int64
	for _, c := range lastSyncStr {
		if c < '0' || c > '9' {
			break
		}
		lastSync = lastSync*10 + int64(c-'0')
	}

	maxUp := 0
	maxDn := 0
	for _, c := range GetSettingOrDefault("hysteria2_max_up_mbps", "0") {
		if c < '0' || c > '9' {
			break
		}
		maxUp = maxUp*10 + int(c-'0')
	}
	for _, c := range GetSettingOrDefault("hysteria2_max_down_mbps", "0") {
		if c < '0' || c > '9' {
			break
		}
		maxDn = maxDn*10 + int(c-'0')
	}

	// Quick count of active Express plans.
	var activePlans int
	_ = db.DB.QueryRow(`SELECT COUNT(*) FROM plans WHERE express_enabled = TRUE AND is_active = TRUE`).Scan(&activePlans)

	return Hysteria2Status{
		BinaryInstalled: errBin == nil,
		ConfigPresent:   errCfg == nil,
		ConfigVersion:   GetSettingOrDefault("hysteria2_config_version", "0"),
		LastSyncAt:      lastSync,
		LastError:       GetSettingOrDefault("hysteria2_last_error", ""),
		MaxUpMbps:       maxUp,
		MaxDownMbps:     maxDn,
		ActivePlans:     activePlans,
	}
}

func containsProto(arr []string, want string) bool {
	for _, v := range arr {
		if v == "all" || strings.EqualFold(v, want) {
			return true
		}
	}
	return false
}
