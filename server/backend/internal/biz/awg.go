package biz

import (
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net"

	"github.com/heyearth/backend/internal/db"
	"golang.org/x/crypto/curve25519"
)

// AWGCPSParams — Custom Protocol Settings per user. Each peer gets unique
// values so DPI can't fingerprint "all AmneziaWG users share signature X".
//
// Generation ranges (see generateCPSParams):
//   Jc: 3..10           — number of junk packets sent BEFORE handshake
//   Jmin: 50..500       — junk packet min size in bytes
//   Jmax: 500..1500     — junk packet max size in bytes (must be > Jmin)
//   S1: 15..150         — pad bytes prepended to init packet
//   S2: 15..150         — pad bytes appended to init packet
//                         (S1+S2 < 1280 to stay below QUIC-class MTU)
//   H1..H4: random uint32 — magic header values overriding WG defaults
type AWGCPSParams struct {
	Jc   int    `json:"jc"`
	Jmin int    `json:"jmin"`
	Jmax int    `json:"jmax"`
	S1   int    `json:"s1"`
	S2   int    `json:"s2"`
	H1   uint32 `json:"h1"`
	H2   uint32 `json:"h2"`
	H3   uint32 `json:"h3"`
	H4   uint32 `json:"h4"`
}

// AWGConfig — fully-populated user AmneziaWG config ready for serialization
// into a WG INI block by cmd/api/client_api.go::buildAmneziaWGURI.
type AWGConfig struct {
	UserPrivKey  string       // base64, 32 bytes
	UserPubKey   string       // base64, 32 bytes
	UserPSK      string       // base64, 32 bytes (Q on init packet)
	ServerPubKey string       // base64, 32 bytes
	CPSParams    AWGCPSParams // per-user random
	IPAddress    string       // "10.66.66.42" — assigned slot in /24
	Endpoint     string       // "<main_host>:51820" — Hub UDP listener
}

// generateKeyPair returns base64-encoded curve25519 keypair.
// Follows the WireGuard scheme: 32 random bytes → clamp lower 3 bits +
// top bit per RFC 7748 → curve25519.X25519 on the base point.
func generateKeyPair() (privBase64, pubBase64 string, err error) {
	priv := make([]byte, curve25519.ScalarSize) // 32
	if _, err := rand.Read(priv); err != nil {
		return "", "", fmt.Errorf("rand.Read priv: %w", err)
	}
	// RFC 7748 clamping for X25519:
	priv[0] &= 248
	priv[31] &= 127
	priv[31] |= 64

	pub, err := curve25519.X25519(priv, curve25519.Basepoint)
	if err != nil {
		return "", "", fmt.Errorf("curve25519.X25519: %w", err)
	}
	return base64.StdEncoding.EncodeToString(priv),
		base64.StdEncoding.EncodeToString(pub), nil
}

// generatePresharedKey — 32 random bytes, base64-encoded.
func generatePresharedKey() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("rand.Read psk: %w", err)
	}
	return base64.StdEncoding.EncodeToString(buf), nil
}

// randomIntInRange returns a uniformly random int in [min, max] inclusive.
func randomIntInRange(min, max int) (int, error) {
	if max < min {
		return 0, fmt.Errorf("invalid range [%d, %d]", min, max)
	}
	span := big.NewInt(int64(max - min + 1))
	n, err := rand.Int(rand.Reader, span)
	if err != nil {
		return 0, err
	}
	return min + int(n.Int64()), nil
}

// randomUint32 — 4 random bytes interpreted as big-endian uint32.
func randomUint32() (uint32, error) {
	var buf [4]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return 0, err
	}
	return binary.BigEndian.Uint32(buf[:]), nil
}

// generateCPSParams produces a fresh random CPS bundle satisfying constraints.
func generateCPSParams() (AWGCPSParams, error) {
	var p AWGCPSParams
	var err error

	if p.Jc, err = randomIntInRange(3, 10); err != nil {
		return p, err
	}
	if p.Jmin, err = randomIntInRange(50, 500); err != nil {
		return p, err
	}
	jmaxFloor := p.Jmin + 1
	if jmaxFloor > 1500 {
		jmaxFloor = 1500
	}
	if p.Jmax, err = randomIntInRange(jmaxFloor, 1500); err != nil {
		return p, err
	}
	if p.S1, err = randomIntInRange(15, 150); err != nil {
		return p, err
	}
	s2Ceiling := 150
	if maxByConstraint := 1279 - p.S1; maxByConstraint < s2Ceiling {
		s2Ceiling = maxByConstraint
	}
	if p.S2, err = randomIntInRange(15, s2Ceiling); err != nil {
		return p, err
	}
	if p.H1, err = randomUint32(); err != nil {
		return p, err
	}
	if p.H2, err = randomUint32(); err != nil {
		return p, err
	}
	if p.H3, err = randomUint32(); err != nil {
		return p, err
	}
	if p.H4, err = randomUint32(); err != nil {
		return p, err
	}
	return p, nil
}

// awgPoolBase: per-user IPv4 pool 10.66.66.2-254.
const (
	awgPoolPrefix    = "10.66.66."
	awgPoolFirstHost = 2
	awgPoolLastHost  = 254
)

// ErrAWGPoolExhausted — returned from allocateUserIP when 253 slots used.
var ErrAWGPoolExhausted = errors.New("amneziawg pool 10.66.66.0/24 exhausted")

// allocateUserIP picks lowest-numbered unused slot inside transaction.
func allocateUserIP(tx *sql.Tx) (string, error) {
	rows, err := tx.Query(`
		SELECT awg_ip_address FROM users
		WHERE awg_ip_address IS NOT NULL
	`)
	if err != nil {
		return "", fmt.Errorf("scan used ips: %w", err)
	}
	defer rows.Close()

	used := make(map[string]struct{})
	for rows.Next() {
		var ip string
		if err := rows.Scan(&ip); err != nil {
			return "", err
		}
		used[ip] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return "", err
	}

	for octet := awgPoolFirstHost; octet <= awgPoolLastHost; octet++ {
		candidate := fmt.Sprintf("%s%d", awgPoolPrefix, octet)
		if _, taken := used[candidate]; !taken {
			if net.ParseIP(candidate).To4() == nil {
				continue
			}
			return candidate, nil
		}
	}
	return "", ErrAWGPoolExhausted
}

// GetServerAWGKeys returns Hub's WG identity. Lazy-init in app_settings.
func GetServerAWGKeys() (privKey, pubKey string, err error) {
	privKey = GetSettingOrDefault("awg_server_private_key", "")
	pubKey = GetSettingOrDefault("awg_server_public_key", "")
	if privKey != "" && pubKey != "" {
		return privKey, pubKey, nil
	}

	privKey, pubKey, err = generateKeyPair()
	if err != nil {
		return "", "", fmt.Errorf("generate server keypair: %w", err)
	}
	if err := SetSetting("awg_server_private_key", privKey); err != nil {
		return "", "", fmt.Errorf("persist server priv: %w", err)
	}
	if err := SetSetting("awg_server_public_key", pubKey); err != nil {
		return "", "", fmt.Errorf("persist server pub: %w", err)
	}
	return privKey, pubKey, nil
}

// GetUserAWGConfig returns user's AWG config, lazy-init on first call.
// Transactional — all 5 awg_* columns written atomically.
func GetUserAWGConfig(userID string) (*AWGConfig, error) {
	user, err := GetOrCreateUser(userID)
	if err != nil {
		return nil, fmt.Errorf("resolve user: %w", err)
	}

	_, serverPub, err := GetServerAWGKeys()
	if err != nil {
		return nil, err
	}

	tx, err := db.DB.Begin()
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	var (
		privKey, pubKey, psk, ip sql.NullString
		cpsJSON                  sql.NullString
	)
	err = tx.QueryRow(`
		SELECT COALESCE(awg_private_key, ''),
		       COALESCE(awg_public_key, ''),
		       COALESCE(awg_preshared_key, ''),
		       COALESCE(awg_ip_address, ''),
		       COALESCE(awg_cps_params::text, '')
		FROM users WHERE id = $1
		FOR UPDATE
	`, user.ID).Scan(&privKey, &pubKey, &psk, &ip, &cpsJSON)
	if err != nil {
		return nil, fmt.Errorf("load awg state: %w", err)
	}

	if privKey.Valid && privKey.String != "" {
		var cps AWGCPSParams
		if cpsJSON.Valid && cpsJSON.String != "" {
			if err := json.Unmarshal([]byte(cpsJSON.String), &cps); err != nil {
				return nil, fmt.Errorf("decode cached cps: %w", err)
			}
		}
		if err := tx.Commit(); err != nil {
			return nil, fmt.Errorf("commit (read-only): %w", err)
		}
		return &AWGConfig{
			UserPrivKey:  privKey.String,
			UserPubKey:   pubKey.String,
			UserPSK:      psk.String,
			ServerPubKey: serverPub,
			CPSParams:    cps,
			IPAddress:    ip.String,
			Endpoint:     awgEndpoint(),
		}, nil
	}

	newPriv, newPub, err := generateKeyPair()
	if err != nil {
		return nil, err
	}
	newPSK, err := generatePresharedKey()
	if err != nil {
		return nil, err
	}
	newCPS, err := generateCPSParams()
	if err != nil {
		return nil, err
	}
	cpsBytes, err := json.Marshal(newCPS)
	if err != nil {
		return nil, fmt.Errorf("marshal cps: %w", err)
	}
	newIP, err := allocateUserIP(tx)
	if err != nil {
		return nil, err
	}

	if _, err := tx.Exec(`
		UPDATE users
		   SET awg_private_key   = $1,
		       awg_public_key    = $2,
		       awg_preshared_key = $3,
		       awg_cps_params    = $4::jsonb,
		       awg_ip_address    = $5
		 WHERE id = $6
	`, newPriv, newPub, newPSK, string(cpsBytes), newIP, user.ID); err != nil {
		return nil, fmt.Errorf("persist awg state: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit: %w", err)
	}

	return &AWGConfig{
		UserPrivKey:  newPriv,
		UserPubKey:   newPub,
		UserPSK:      newPSK,
		ServerPubKey: serverPub,
		CPSParams:    newCPS,
		IPAddress:    newIP,
		Endpoint:     awgEndpoint(),
	}, nil
}

// awgEndpoint returns "<main_host>:51820" for client URI building.
func awgEndpoint() string {
	host := GetSettingOrDefault("main_server_host", "")
	return host + ":51820"
}
