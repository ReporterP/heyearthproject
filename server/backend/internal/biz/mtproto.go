// Package biz — MTProto-proxy helpers: balancer (PickMTProtoExit),
// install-state queries (ListExitsNeedingMTProto) and status mutation
// (MarkMTProtoStatus). The installer runs over the node's pinned SSH connection:
// internal/provision InstallMTProxy / InstallMTProxyOnNode.
//
// "Healthy" definition: is_exit_node=TRUE AND lifecycle_state='online' AND
// mtproto_status='healthy'. The Tunnel-health-monitor maintains the status
// via a per-server fake-TLS handshake (internal/mtprobe) on the global mtproto_port.
package biz

import (
	"context"
	crand "crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"math/rand"
	"net"
	"regexp"
	"slices"
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
	Domain     string // servers.mtproto_domain ("" = the shared domain); PickMTProtoExit only
}

// PickMTProtoExit returns a weighted-random healthy exit's host:port to use
// for a Telegram MTProto deeplink, and the fake-TLS domain it fronts ("" = the
// shared mtproto_fronting_domain). `ok` is false if no healthy exit exists.
//
// Port comes from app_settings.mtproto_port (default 8443) — same on every exit.
func PickMTProtoExit() (host, port, domain string, ok bool) {
	rows, err := db.DB.Query(`
		SELECT id, ip_address, COALESCE(load_weight, 1), COALESCE(mtproto_domain, '')
		  FROM servers
		 WHERE is_exit_node = TRUE
		   AND lifecycle_state = 'online'
		   AND mtproto_status = 'healthy'
	`)
	if err != nil {
		log.Printf("[mtproto] PickMTProtoExit query: %v", err)
		return "", "", "", false
	}
	defer rows.Close()

	var pool []MTProtoExit
	for rows.Next() {
		var ex MTProtoExit
		if err := rows.Scan(&ex.ID, &ex.IPAddress, &ex.LoadWeight, &ex.Domain); err != nil {
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
		return "", "", "", false
	}
	chosen := pool[rand.Intn(len(pool))]
	port = GetSettingOrDefault("mtproto_port", "8443")
	return chosen.IPAddress, port, chosen.Domain, true
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

// markMTProtoStatusQuery never touches a row whose MTProxy the owner switched off ('disabled'):
// the health monitor reads the row, probes for up to 3 s and only then writes, so a switch-off in
// between would otherwise be turned back into healthy/unhealthy. Only the admin switch
// (internal/admin mtproxyEnableQuery) leaves 'disabled'.
const markMTProtoStatusQuery = `
		UPDATE servers
		   SET mtproto_status = $1,
		       mtproto_last_check = NOW(),
		       mtproto_installed_at = COALESCE(mtproto_installed_at,
		                                       CASE WHEN $1 = 'healthy' THEN NOW() END),
		       updated_at = NOW()
		 WHERE id = $2 AND mtproto_status <> 'disabled'
	`

// MarkMTProtoStatus sets mtproto_status and updates last_check timestamp (the MTProto health
// monitor's write; a 'disabled' row is left alone, see markMTProtoStatusQuery).
// mtproto_installed_at is set to NOW() ONLY on first 'healthy' observation —
// preserves retry semantics: if install never succeeded, installed_at stays
// NULL and the reconciler keeps retrying until success.
func MarkMTProtoStatus(serverID int, status string) error {
	_, err := db.DB.Exec(markMTProtoStatusQuery, status, serverID)
	return err
}

// ── Per-exit fronting domain ──
//
// Every exit fronts its own fake-TLS domain: one that resolves to that exit (a sniffing client
// dials the domain instead of the IP — see MTProtoFrontingDomain). Its secret is the shared key
// of mtproto_secret with that domain (MTProtoSecretFor), so a client gets the secret of the exit
// it is handed. servers.mtproto_domain holds the exit's domain ('' = the shared domain).

// MTProtoSecretFor is the shared mtg fake-TLS secret re-issued for domain: the same "ee" + 16-byte
// key, the domain part replaced. The shared domain gives the shared secret back unchanged.
func MTProtoSecretFor(secret, domain string) (string, error) {
	secret, domain = strings.TrimSpace(secret), strings.ToLower(strings.TrimSpace(domain))
	if len(secret) < 34 || !strings.EqualFold(secret[:2], "ee") {
		return "", errors.New("mtproto_secret не в формате mtg (ee + 32 hex-символа + домен в hex)")
	}
	if _, err := hex.DecodeString(secret[2:34]); err != nil {
		return "", errors.New("mtproto_secret не в формате mtg (ee + 32 hex-символа + домен в hex)")
	}
	if domain == "" {
		return "", errors.New("домен маскировки MTProxy пуст")
	}
	if d, err := hex.DecodeString(secret[34:]); err == nil && strings.EqualFold(string(d), domain) {
		return secret, nil
	}
	return secret[:34] + hex.EncodeToString([]byte(domain)), nil
}

// Resolvers of ExitFrontingDomain (tests swap them).
var (
	mtLookupAddr = net.DefaultResolver.LookupAddr
	mtLookupHost = net.DefaultResolver.LookupHost
)

// mtDomainNameRe: a domain mtg and install-mtproxy.sh take (provision's mtDomainRe, lower case).
var mtDomainNameRe = regexp.MustCompile(`^(?:[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z](?:[a-z0-9-]{0,61}[a-z0-9])?$`)

// ExitFrontingDomain is the fake-TLS domain of the exit at ip: the one stored for it (as is — its
// MTProxy runs with it, and a DNS blip must not move a working exit to another domain; the
// installer checks it still resolves here), else the shared mtproto_fronting_domain, else the
// exit's reverse-DNS name (hosters name their servers, e.g. v1234567.hosted-by-<hoster>.com) —
// those two only while they resolve to ip.
func ExitFrontingDomain(ctx context.Context, ip, stored, shared string) (string, error) {
	if d := strings.ToLower(strings.TrimSpace(stored)); d != "" {
		return d, nil
	}
	want := net.ParseIP(strings.TrimSpace(ip))
	if want == nil {
		return "", fmt.Errorf("адрес выхода %q — не IP", ip)
	}
	resolvesHere := func(name string) bool {
		if len(name) > 253 || !mtDomainNameRe.MatchString(name) {
			return false
		}
		ips, err := mtLookupHost(ctx, name)
		return err == nil && slices.ContainsFunc(ips, func(s string) bool { return want.Equal(net.ParseIP(s)) })
	}
	if d := strings.ToLower(strings.TrimSpace(shared)); resolvesHere(d) {
		return d, nil
	}
	names, err := mtLookupAddr(ctx, want.String())
	for _, n := range names {
		if n = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(n), ".")); resolvesHere(n) {
			return n, nil
		}
	}
	why := "обратной записи нет"
	if err != nil {
		why = "обратная запись не прочитана: " + err.Error()
	} else if len(names) > 0 {
		why = "обратная запись " + strings.Join(names, ", ") + " не указывает обратно на него"
	}
	return "", fmt.Errorf("у выхода %s нет домена для Telegram-прокси: ни %q не указывает на него, %s — попросите хостера задать PTR-запись (имя сервера) и повторите", ip, shared, why)
}

// dbExecQueryer is *sql.DB or *sql.Tx.
type dbExecQueryer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// ExitMTProtoDomain reads servers.mtproto_domain of exit id (empty: the shared domain).
func ExitMTProtoDomain(ctx context.Context, q dbExecQueryer, id int) (string, error) {
	var d string
	err := q.QueryRowContext(ctx, `SELECT COALESCE(mtproto_domain, '') FROM servers WHERE id = $1`, id).Scan(&d)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return strings.TrimSpace(d), err
}

// SetExitMTProtoDomain records the domain exit id fronts (before its MTProxy is installed with it:
// the deeplink hands out the secret of that domain).
func SetExitMTProtoDomain(ctx context.Context, q dbExecQueryer, id int, domain string) error {
	_, err := q.ExecContext(ctx, `UPDATE servers SET mtproto_domain = $2 WHERE id = $1 AND mtproto_domain IS DISTINCT FROM $2`, id, domain)
	return err
}

// MTProtoFrontingDomainsStrict: every domain a sniffing client may dial for our MTProxy — the
// shared one and those of the healthy exits — for the direct rules of the Happ routing profile.
// A failed read is an error.
func MTProtoFrontingDomainsStrict() ([]string, error) {
	shared, err := MTProtoFrontingDomainStrict()
	if err != nil {
		return nil, err
	}
	var out []string
	if shared != "" {
		out = append(out, strings.ToLower(shared))
	}
	rows, err := db.DB.Query(`
		SELECT DISTINCT LOWER(mtproto_domain)
		  FROM servers
		 WHERE is_exit_node = TRUE
		   AND lifecycle_state = 'online'
		   AND mtproto_status = 'healthy'
		   AND mtproto_domain <> ''
		 ORDER BY 1`)
	if err != nil {
		return nil, fmt.Errorf("list mtproto exit domains: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var d string
		if err := rows.Scan(&d); err != nil {
			return nil, fmt.Errorf("list mtproto exit domains: %w", err)
		}
		if d != "" && !slices.Contains(out, d) {
			out = append(out, d)
		}
	}
	return out, rows.Err()
}
