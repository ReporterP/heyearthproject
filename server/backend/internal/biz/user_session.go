package biz

import (
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/heyearth/backend/internal/db"
)

const userTokenExpiry = 30 * 24 * time.Hour // 30 days

type userJWTHeader struct {
	Alg string `json:"alg"`
	Typ string `json:"typ"`
}

type userJWTClaims struct {
	UserID   string `json:"user_id"`
	UserDBID int    `json:"user_db_id"`
	Iat      int64  `json:"iat"`
	Exp      int64  `json:"exp"`
}

func userJWTSecret() []byte {
	secret := strings.TrimSpace(os.Getenv("JWT_SECRET"))
	if secret == "" {
		// CRITICAL: never use a default secret in production.
		// During initial development without .env, return a marker that will fail signature checks.
		panic("JWT_SECRET env is required — set it in .env or environment")
	}
	return []byte(secret)
}

// GenerateUserSessionToken creates a JWT token for a user and stores the session.
func GenerateUserSessionToken(userID string, userDBID int) (string, error) {
	now := time.Now()
	claims := userJWTClaims{
		UserID:   userID,
		UserDBID: userDBID,
		Iat:      now.Unix(),
		Exp:      now.Add(userTokenExpiry).Unix(),
	}

	header := userJWTHeader{Alg: "HS256", Typ: "JWT"}
	headerJSON, _ := json.Marshal(header)
	claimsJSON, _ := json.Marshal(claims)

	headerB64 := base64.RawURLEncoding.EncodeToString(headerJSON)
	claimsB64 := base64.RawURLEncoding.EncodeToString(claimsJSON)
	signingInput := headerB64 + "." + claimsB64

	mac := hmac.New(sha256.New, userJWTSecret())
	mac.Write([]byte(signingInput))
	signature := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))

	token := signingInput + "." + signature

	// Store session in DB
	tokenHash := hashToken(token)
	_, err := db.DB.Exec(`
		INSERT INTO user_sessions (user_id, token_hash, expires_at)
		VALUES ($1, $2, $3)
	`, userDBID, tokenHash, now.Add(userTokenExpiry))
	if err != nil {
		return "", fmt.Errorf("store session: %w", err)
	}

	return token, nil
}

// ValidateUserToken validates a JWT token signature, expiry, AND checks
// that the session exists in user_sessions (so tokens can be revoked).
func ValidateUserToken(token string) (*userJWTClaims, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, fmt.Errorf("invalid token format")
	}

	signingInput := parts[0] + "." + parts[1]
	mac := hmac.New(sha256.New, userJWTSecret())
	mac.Write([]byte(signingInput))
	expectedSignature := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))

	if !hmac.Equal([]byte(parts[2]), []byte(expectedSignature)) {
		return nil, fmt.Errorf("invalid signature")
	}

	claimsJSON, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, fmt.Errorf("decode claims: %w", err)
	}

	var claims userJWTClaims
	if err := json.Unmarshal(claimsJSON, &claims); err != nil {
		return nil, fmt.Errorf("parse claims: %w", err)
	}

	if time.Now().Unix() > claims.Exp {
		return nil, fmt.Errorf("token expired")
	}

	// Verify session exists in DB (enables revocation without rotating secret).
	tokenHash := hashToken(token)
	var sessionID int
	err = db.DB.QueryRow(`
		SELECT id FROM user_sessions
		WHERE token_hash = $1 AND user_id = $2 AND expires_at > NOW()
	`, tokenHash, claims.UserDBID).Scan(&sessionID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("session revoked or not found")
	}
	if err != nil {
		// The session could not be checked: not a revoked session. Handlers
		// that must not read a DB outage as "logged out" answer 503 for it.
		return nil, &SessionLookupError{Err: err}
	}

	return &claims, nil
}

// ErrSessionLookup: the token is well-formed and signed, but its session could
// not be checked because the database read failed (errors.Is target of
// SessionLookupError).
var ErrSessionLookup = errors.New("session lookup failed")

// SessionLookupError carries the database error behind ErrSessionLookup. Its
// message stays generic: handlers send err.Error() to clients.
type SessionLookupError struct{ Err error }

func (e *SessionLookupError) Error() string        { return ErrSessionLookup.Error() }
func (e *SessionLookupError) Is(target error) bool { return target == ErrSessionLookup }
func (e *SessionLookupError) Unwrap() error        { return e.Err }

// RevokeUserSession deletes a user session by token (for logout).
func RevokeUserSession(token string) error {
	tokenHash := hashToken(token)
	_, err := db.DB.Exec(`DELETE FROM user_sessions WHERE token_hash = $1`, tokenHash)
	return err
}

// RevokeAllUserSessions deletes all sessions for a user (password change, etc.).
func RevokeAllUserSessions(userDBID int) error {
	_, err := db.DB.Exec(`DELETE FROM user_sessions WHERE user_id = $1`, userDBID)
	return err
}

func hashToken(token string) string {
	h := sha256.Sum256([]byte(token))
	return fmt.Sprintf("%x", h)
}
