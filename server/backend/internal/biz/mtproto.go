// Package biz — MTProto-proxy helpers: balancer (PickMTProtoExit),
// install-state queries (ListExitsNeedingMTProto) and status mutation
// (MarkMTProtoStatus). The installer runs over the node's pinned SSH connection:
// internal/provision InstallMTProxy / InstallMTProxyOnNode.
//
// "Healthy" definition: is_exit_node=TRUE AND lifecycle_state='online' AND
// mtproto_status='healthy'. The Tunnel-health-monitor maintains the status
// via per-server TCP probe on the global mtproto_port.
package biz

import (
	crand "crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"math/rand"
	"strings"

	"github.com/heyearth/backend/internal/db"
)

// MTProtoFrontingDomain returns the configured fake-TLS fronting domain.
//
// The domain is hex-encoded into the mtg secret, and Telegram sends it as the
// TLS SNI of the fake-TLS ClientHello. Clients that sniff TLS (Happ/sing-box/
// Xray with destOverride) REPLACE the connection destination with this domain,
// which drops the proxy IP out of the flow — the DirectIp exception can no
// longer match and the request ends up dialled against the real domain on the
// proxy port. So the domain MUST resolve to the MTProto exit node(s); if it
// does not, the proxy is unreachable for those clients while every server-side
// check (daemon up, port listening, TCP probe) still reports healthy.
func MTProtoFrontingDomain() string {
	return strings.TrimSpace(GetSettingOrDefault("mtproto_fronting_domain", ""))
}

// MTProtoFrontingDomainStrict is MTProtoFrontingDomain with a strict read:
// "" only when the key is absent or blank, an error when the read failed.
func MTProtoFrontingDomainStrict() (string, error) {
	v, err := GetSetting("mtproto_fronting_domain")
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("read mtproto_fronting_domain: %w", err)
	}
	return strings.TrimSpace(v), nil
}

// BuildMTProtoSecret assembles an mtg fake-TLS secret: the "ee" marker, 16
// random bytes, and the hex-encoded fronting domain — the exact layout mtg's
// own `generate-secret --hex <domain>` produces.
func BuildMTProtoSecret(domain string) (string, error) {
	domain = strings.TrimSpace(domain)
	if domain == "" {
		return "", fmt.Errorf("fronting domain is empty")
	}
	buf := make([]byte, 16)
	if _, err := crand.Read(buf); err != nil {
		return "", fmt.Errorf("generate random secret: %w", err)
	}
	return "ee" + hex.EncodeToString(buf) + hex.EncodeToString([]byte(domain)), nil
}

// MTProtoExit — minimal row returned by the picker.
type MTProtoExit struct {
	ID         int
	IPAddress  string
	LoadWeight int
}

// PickMTProtoExit returns a weighted-random healthy exit's host:port to use
// for a Telegram MTProto deeplink. `ok` is false if no healthy exit exists.
//
// Port comes from app_settings.mtproto_port (default 8443) — same on every exit.
func PickMTProtoExit() (host, port string, ok bool) {
	rows, err := db.DB.Query(`
		SELECT id, ip_address, COALESCE(load_weight, 1)
		  FROM servers
		 WHERE is_exit_node = TRUE
		   AND lifecycle_state = 'online'
		   AND mtproto_status = 'healthy'
	`)
	if err != nil {
		log.Printf("[mtproto] PickMTProtoExit query: %v", err)
		return "", "", false
	}
	defer rows.Close()

	var pool []MTProtoExit
	for rows.Next() {
		var ex MTProtoExit
		if err := rows.Scan(&ex.ID, &ex.IPAddress, &ex.LoadWeight); err != nil {
			continue
		}
		w := ex.LoadWeight
		if w < 1 {
			w = 1
		}
		for i := 0; i < w; i++ {
			pool = append(pool, ex)
		}
	}
	if len(pool) == 0 {
		return "", "", false
	}
	chosen := pool[rand.Intn(len(pool))]
	port = GetSettingOrDefault("mtproto_port", "8443")
	return chosen.IPAddress, port, true
}

// ListMTProtoExitIPs returns IP addresses of all exit-nodes currently running
// a healthy mtproto-proxy daemon. Used by the subscription handler to build
// the "DirectIp" rule in the Happ routing profile so Telegram's connections
// to these IPs bypass the VPN tunnel (avoiding the VPN-traffic-loops-to-itself
// problem when the user has both VPN and MTProto-proxy enabled).
func ListMTProtoExitIPs() ([]string, error) {
	rows, err := db.DB.Query(`
		SELECT ip_address
		  FROM servers
		 WHERE is_exit_node = TRUE
		   AND lifecycle_state = 'online'
		   AND mtproto_status = 'healthy'
		 ORDER BY id
	`)
	if err != nil {
		return nil, fmt.Errorf("list mtproto exit ips: %w", err)
	}
	defer rows.Close()
	var ips []string
	for rows.Next() {
		var ip string
		if err := rows.Scan(&ip); err != nil {
			continue
		}
		if ip != "" {
			ips = append(ips, ip)
		}
	}
	return ips, rows.Err()
}

// exitsNeedingMTProtoQuery: online exits without a daemon yet. 'disabled' = the owner switched
// MTProxy off for this node in the wizard.
const exitsNeedingMTProtoQuery = `
	SELECT id, ip_address, COALESCE(load_weight, 1)
	  FROM servers
	 WHERE is_exit_node = TRUE
	   AND lifecycle_state = 'online'
	   AND mtproto_installed_at IS NULL
	   AND mtproto_status NOT IN ('installing', 'disabled')
`

// ListExitsNeedingMTProto returns online exits that have no daemon installed
// yet (mtproto_installed_at IS NULL) and whose MTProxy is not switched off.
// Used by Ghost Reconciler.
func ListExitsNeedingMTProto() ([]MTProtoExit, error) {
	rows, err := db.DB.Query(exitsNeedingMTProtoQuery)
	if err != nil {
		return nil, fmt.Errorf("list exits needing mtproto: %w", err)
	}
	defer rows.Close()

	var out []MTProtoExit
	for rows.Next() {
		var ex MTProtoExit
		if err := rows.Scan(&ex.ID, &ex.IPAddress, &ex.LoadWeight); err != nil {
			continue
		}
		out = append(out, ex)
	}
	return out, rows.Err()
}

// MarkMTProtoStatus sets mtproto_status and updates last_check timestamp.
// mtproto_installed_at is set to NOW() ONLY on first 'healthy' observation —
// preserves retry semantics: if install never succeeded, installed_at stays
// NULL and the reconciler keeps retrying until success.
func MarkMTProtoStatus(serverID int, status string) error {
	_, err := db.DB.Exec(`
		UPDATE servers
		   SET mtproto_status = $1,
		       mtproto_last_check = NOW(),
		       mtproto_installed_at = COALESCE(mtproto_installed_at,
		                                       CASE WHEN $1 = 'healthy' THEN NOW() END),
		       updated_at = NOW()
		 WHERE id = $2
	`, status, serverID)
	return err
}
