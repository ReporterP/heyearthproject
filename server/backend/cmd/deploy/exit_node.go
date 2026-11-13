package main

import (
	"context"
	"crypto"
	"crypto/x509"
	"database/sql"
	"errors"
	"fmt"
	"net"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/heyearth/backend/internal/biz"
	"github.com/heyearth/backend/internal/config"
	sshcrypto "github.com/heyearth/backend/internal/crypto"
	"github.com/heyearth/backend/internal/provision"
	"github.com/lib/pq"
	"golang.org/x/crypto/ssh"
)

// exitCLIOptions are the --setup-exit-node flags.
type exitCLIOptions struct {
	name, country, plans, password string
	sshPort                        int   // 0: the stored port (22 for a new exit)
	mtproxy                        *bool // nil: the node's MTProxy switch stays as it is (on for a new exit)
}

// exitNodeCredentials: the exit's own root password (flag or EXIT_NODE_SSH_PASSWORD — the Hub's
// deploy password is never used) and the owner-confirmed host key fingerprint
// (EXIT_NODE_HOST_KEY_FINGERPRINT). Both may be empty here: an exit already in the database
// brings its stored password and confirmed key (chooseExitHostKey, setupExitNodeDeploy decide).
func exitNodeCredentials(env envMap, flagPassword string) (password, fingerprint string, err error) {
	password = flagPassword
	if password == "" {
		password = env.get("EXIT_NODE_SSH_PASSWORD", "")
	}
	fingerprint = strings.TrimSpace(env.get("EXIT_NODE_HOST_KEY_FINGERPRINT", ""))
	if fingerprint != "" && !fingerprintRe.MatchString(fingerprint) {
		return "", "", fmt.Errorf("EXIT_NODE_HOST_KEY_FINGERPRINT %q должен выглядеть как SHA256:<43 символа base64> (ssh-keygen -lf …)", fingerprint)
	}
	return password, fingerprint, nil
}

// exitRowState is the exit's servers row as --setup-exit-node finds it (Found=false: none yet).
type exitRowState struct {
	Found, Confirmed, HasPassword bool
	ID, SSHPort                   int
	FP, Role, Lifecycle           string
}

const exitRowStateQuery = `SELECT id, role, lifecycle_state, COALESCE(ssh_port, 22), COALESCE(ssh_host_key_fp, ''),
       ssh_host_key_confirmed_at IS NOT NULL, ssh_password_enc
  FROM servers WHERE ip_address = $1`

// loadExitRow reads the row of ip; HasPassword: a stored password that decrypts with the Hub's key.
func loadExitRow(ctx context.Context, db *sql.DB, ip string, decrypt func([]byte) (string, error)) (exitRowState, error) {
	var r exitRowState
	var enc []byte
	err := db.QueryRowContext(ctx, exitRowStateQuery, ip).Scan(&r.ID, &r.Role, &r.Lifecycle, &r.SSHPort, &r.FP, &r.Confirmed, &enc)
	if errors.Is(err, sql.ErrNoRows) {
		return exitRowState{}, nil
	}
	if err != nil {
		return r, err
	}
	r.Found = true
	if len(enc) > 0 {
		_, derr := decrypt(enc)
		r.HasPassword = derr == nil
	}
	if r.Role != "exit" {
		return r, fmt.Errorf("сервер %s в базе — «%s», а не выход за рубежом", ip, r.Role)
	}
	return r, nil
}

func findHostKey(p provision.ProbeResult, fp string) (provision.HostKey, bool) {
	for _, k := range p.Keys {
		if k.Fingerprint == fp {
			return k, true
		}
	}
	return provision.HostKey{}, false
}

func presentedKeys(p provision.ProbeResult) string {
	var s []string
	for _, k := range p.Keys {
		s = append(s, k.Type+" "+k.Fingerprint)
	}
	if len(s) == 0 {
		return "ни одного ключа"
	}
	return strings.Join(s, ", ")
}

// chooseExitHostKey: the key --setup-exit-node pins — server/.env's fingerprint, else the one
// confirmed in the database; always one the exit presents right now. A different key confirmed in
// the database must be presented too (the same server with another key type: exit 2 has ed25519
// and ECDSA) — otherwise the server was reinstalled or is not the one the owner confirmed.
func chooseExitHostKey(row exitRowState, envFP string, probe provision.ProbeResult) (provision.HostKey, error) {
	want := envFP
	if want == "" {
		switch {
		case row.Found && row.FP != "" && row.Confirmed:
			want = row.FP
		case row.Found && row.FP != "":
			return provision.HostKey{}, fmt.Errorf("ключ хоста %s этой ноды в базе не подтверждён владельцем — подтвердите его в карточке ноды "+
				"(«Получить отпечаток») или задайте EXIT_NODE_HOST_KEY_FINGERPRINT в server/.env", row.FP)
		default:
			return provision.HostKey{}, errors.New("EXIT_NODE_HOST_KEY_FINGERPRINT не задан, а подтверждённого ключа этой ноды в базе нет: " +
				"на exit (консоль хостера) выполните ssh-keygen -lf /etc/ssh/ssh_host_ed25519_key.pub -E sha256 и запишите SHA256:… в server/.env")
		}
	}
	key, ok := findHostKey(probe, want)
	if !ok {
		return provision.HostKey{}, fmt.Errorf("exit сейчас предъявляет другие ключи (%s), а ожидается %s — сверьте с панелью хостера", presentedKeys(probe), want)
	}
	if row.Found && row.Confirmed && row.FP != "" && row.FP != want {
		if _, ok := findHostKey(probe, row.FP); !ok {
			return provision.HostKey{}, fmt.Errorf("в базе подтверждён ключ %s, а exit его больше не предъявляет (%s) — сервер переустановлен или это не он; "+
				"подтвердите новый ключ в карточке ноды", row.FP, presentedKeys(probe))
		}
	}
	return key, nil
}

// exitRow is what --setup-exit-node writes about the exit.
type exitRow struct {
	IP, Password, Name, Country string
	Key                         provision.HostKey
	SSHPort                     int // 0: keep the stored port (22 for a new exit)
}

// The rows are the admin wizard's (CreateNodeHandler): a new exit is 'pending' with the host key
// this run confirmed and only an encrypted password; the provisioning job takes it through
// provisioning → online (or failed). An update re-confirms the key (the confirmation time moves
// only when the key changes) and stores a new encrypted password only when one was given —
// emptying the legacy plaintext column then (NOT NULL: emptied, never NULLed).
const (
	insertExitRowQuery = `INSERT INTO servers (name, role, is_exit_node, ip_address, ssh_port, ssh_user, ssh_password_enc, country_code,
       lifecycle_state, ssh_host_key, ssh_host_key_fp, ssh_host_key_confirmed_at, updated_at)
VALUES ($1, 'exit', TRUE, $2, $3, 'root', $4, $5, 'pending', $6, $7, NOW(), NOW()) RETURNING id`
	updateExitRowQuery = `UPDATE servers SET ssh_port = COALESCE($2::int, ssh_port), ssh_host_key = $3, ssh_host_key_fp = $4,
       ssh_host_key_confirmed_at = CASE WHEN ssh_host_key_fp IS NOT DISTINCT FROM $4 THEN COALESCE(ssh_host_key_confirmed_at, NOW()) ELSE NOW() END,
       ssh_password_enc = COALESCE($5::bytea, ssh_password_enc),
       ssh_password = CASE WHEN $5::bytea IS NULL THEN ssh_password ELSE '' END,
       updated_at = NOW()
 WHERE id = $1`
	exitMTProxySwitchQuery = `UPDATE servers SET mtproto_status = CASE WHEN $2::bool
         THEN CASE WHEN mtproto_status = 'disabled' THEN 'unknown' ELSE mtproto_status END ELSE 'disabled' END,
       updated_at = NOW() WHERE id = $1`
)

var countryRe = regexp.MustCompile(`^[A-Z]{2}$`)

// ensureExitRow writes the exit's row (cur: as loadExitRow found it) and returns its id.
func ensureExitRow(ctx context.Context, db *sql.DB, r exitRow, cur exitRowState) (int, error) {
	var enc []byte
	if r.Password != "" {
		var err error
		if enc, err = sshcrypto.Encrypt(r.Password); err != nil || len(enc) == 0 {
			return 0, fmt.Errorf("пароль exit не шифруется ключом Hub: %v", err)
		}
	}
	if !cur.Found {
		if enc == nil {
			return 0, errors.New("новая exit-нода: нужен её пароль root (--exit-node-password или EXIT_NODE_SSH_PASSWORD) — пароль Hub не используется")
		}
		country := strings.ToUpper(strings.TrimSpace(r.Country))
		if !countryRe.MatchString(country) {
			return 0, errors.New("новая exit-нода: укажите --exit-node-country (две латинские буквы, например DE)")
		}
		name := strings.TrimSpace(r.Name)
		if name == "" {
			name = "Выход " + country
		}
		port := r.SSHPort
		if port == 0 {
			port = 22
		}
		var id int
		err := db.QueryRowContext(ctx, insertExitRowQuery, name, r.IP, port, enc, country, r.Key.Key, r.Key.Fingerprint).Scan(&id)
		var pe *pq.Error
		if errors.As(err, &pe) && pe.Code == "23505" {
			return 0, fmt.Errorf("запись с адресом %s появилась во время запуска (админка?) — повторите команду", r.IP)
		}
		return id, err
	}
	var port, encArg any
	if r.SSHPort > 0 {
		port = r.SSHPort
	}
	if enc != nil {
		encArg = enc
	}
	_, err := db.ExecContext(ctx, updateExitRowQuery, cur.ID, port, r.Key.Key, r.Key.Fingerprint, encArg)
	return cur.ID, err
}

// parsePlanSpec reads --exit-node-plans: all-paid | none | keep | 1,2,3.
func parsePlanSpec(spec string) (kind string, ids []int, err error) {
	switch spec = strings.TrimSpace(spec); spec {
	case "all-paid", "none", "keep":
		return spec, nil, nil
	}
	for _, p := range strings.Split(spec, ",") {
		id, err := strconv.Atoi(strings.TrimSpace(p))
		if err != nil || id < 1 {
			return "", nil, fmt.Errorf("--exit-node-plans %q: all-paid | none | keep | 1,2,3", spec)
		}
		ids = append(ids, id)
	}
	return "ids", ids, nil
}

// collectCLIHubIPs is cmd/api's collectHubIPs for the deploy CLI: the Hub's global unicast,
// non-private interface addresses (`ip -o addr show scope global` on the Hub) plus hosts
// (main_server_host / hub_main_server_host: IPs or names resolved with lookup), sorted, unique.
// The same sources as the API, so the exit's guard rules do not differ between the two.
func collectCLIHubIPs(ipAddrOut string, hosts []string, lookup func(string) ([]net.IP, error)) []string {
	set := map[string]bool{}
	add := func(ip net.IP) {
		if ip != nil && ip.IsGlobalUnicast() && !ip.IsPrivate() {
			set[ip.String()] = true
		}
	}
	for _, line := range strings.Split(ipAddrOut, "\n") {
		f := strings.Fields(line)
		for i := 0; i+1 < len(f); i++ {
			if f[i] == "inet" || f[i] == "inet6" {
				if ip, _, err := net.ParseCIDR(f[i+1]); err == nil {
					add(ip)
				}
			}
		}
	}
	for _, h := range hosts {
		h = strings.TrimSpace(h)
		if h == "" {
			continue
		}
		if ip := net.ParseIP(h); ip != nil {
			add(ip)
			continue
		}
		if ips, err := lookup(h); err == nil {
			for _, ip := range ips {
				add(ip)
			}
		}
	}
	out := make([]string, 0, len(set))
	for s := range set {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

func lookupIPs(host string) ([]net.IP, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	addrs, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, err
	}
	ips := make([]net.IP, len(addrs))
	for i, a := range addrs {
		ips[i] = a.IP
	}
	return ips, nil
}

// appSetting reads app_settings (trimmed; "" when absent or unreadable).
func appSetting(ctx context.Context, db *sql.DB, key string) string {
	var v string
	_ = db.QueryRowContext(ctx, `SELECT value FROM app_settings WHERE key = $1`, key).Scan(&v)
	return strings.TrimSpace(v)
}

// cliHubIPs: the Hub's public addresses (collectCLIHubIPs); DEPLOY_HOST only when nothing else is
// known. Without any the exit's agent would be reachable by nobody or everybody: refused.
func cliHubIPs(ctx context.Context, mainClient *ssh.Client, db *sql.DB, deployHost string) ([]string, error) {
	out, err := runSSHQuiet(mainClient, "ip -o addr show scope global", "")
	if err != nil {
		return nil, fmt.Errorf("адреса интерфейсов Hub: %w", err)
	}
	ips := collectCLIHubIPs(out, []string{appSetting(ctx, db, "main_server_host"), appSetting(ctx, db, "hub_main_server_host")}, lookupIPs)
	if len(ips) == 0 {
		ips = collectCLIHubIPs("", []string{deployHost}, lookupIPs)
	}
	if len(ips) == 0 {
		return nil, errors.New("публичные адреса Hub не определены — агент exit остался бы без защиты или без связи с Hub")
	}
	return ips, nil
}

// cliExitOptions is the API's apiExitOptions for the deploy CLI: the same Hub files (read over the
// deploy session) and settings; the CA certificate without its key — node certificates are signed
// on the Hub (CASigner → provision.HubCASigner), the CA key never comes to this machine.
func cliExitOptions(ctx context.Context, mainClient *ssh.Client, hub provision.Remote, db *sql.DB, hubIPs []string, exitIP string, cur exitRowState) (provision.ExitOptions, error) {
	agent, err := runSSHQuiet(mainClient, "cat "+hubNodeAgentArtifact, "")
	if err != nil || len(agent) == 0 {
		return provision.ExitOptions{}, fmt.Errorf("на Hub нет %s — сначала go run ./cmd/deploy --ensure-hub-provisioning (%v)", hubNodeAgentArtifact, err)
	}
	ca, err := hub.Run(ctx, config.CatScript(hubCertsDir+"/ca-cert.pem"))
	if err != nil {
		return provision.ExitOptions{}, err
	}
	script, err := runSSHQuiet(mainClient, "cat "+hubMTProxyScript, "")
	if err != nil || len(script) == 0 {
		return provision.ExitOptions{}, fmt.Errorf("на Hub нет %s — сначала go run ./cmd/deploy --ensure-hub-provisioning (%v)", hubMTProxyScript, err)
	}
	port := config.ParseMTProtoPort(appSetting(ctx, db, "mtproto_port"))
	return provision.ExitOptions{
		HubIPs:      hubIPs,
		MTProtoPort: port,
		HubXray:     "/opt/heyearth/xray/xray",
		NodeAgent:   func() ([]byte, error) { return []byte(agent), nil },
		CA:          func() ([]byte, []byte, error) { return []byte(ca), nil, nil },
		CASigner: func(ctx context.Context, c *x509.Certificate) (crypto.Signer, error) {
			return provision.HubCASigner(ctx, hub, hubCertsDir+"/ca-key.pem", c.PublicKey)
		},
		MTProto: cliMTProtoOptions(ctx, db, exitIP, cur, &provision.MTProtoOptions{Port: port, Secret: appSetting(ctx, db, "mtproto_secret"),
			Domain: appSetting(ctx, db, "mtproto_fronting_domain"), Script: []byte(script)}),
	}, nil
}

// cliMTProtoOptions points o at the exit's own fronting domain and its secret, like the API's
// exitMTProtoDomain (o.Record is set once the row exists: withMTProtoRecord). No domain found:
// o keeps the shared one, and the installer says it does not resolve to the node.
func cliMTProtoOptions(ctx context.Context, db *sql.DB, exitIP string, cur exitRowState, o *provision.MTProtoOptions) *provision.MTProtoOptions {
	stored := ""
	if cur.Found {
		stored, _ = biz.ExitMTProtoDomain(ctx, db, cur.ID)
	}
	domain, err := biz.ExitFrontingDomain(ctx, exitIP, stored, o.Domain)
	if err != nil {
		fmt.Printf("[exit-node] %v\n", err)
		return o
	}
	if secret, err := biz.MTProtoSecretFor(o.Secret, domain); err == nil {
		o.Domain, o.Secret = domain, secret
	}
	return o
}

// withMTProtoRecord lets the install step record the domain the exit's MTProxy fronts. A stored
// domain always wins in biz.ExitFrontingDomain, so the shared fallback is written only over an
// empty record (the same secret).
func withMTProtoRecord(db *sql.DB, id int, o *provision.MTProtoOptions) {
	if o == nil {
		return
	}
	domain := o.Domain
	o.Record = func(ctx context.Context) error { return biz.SetExitMTProtoDomain(ctx, db, id, domain) }
}
