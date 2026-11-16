package config

// The Telegram WEB proxy on the Hub (phase T, docs/superpowers/plans/2026-11-16-telegram-web-proxy.md):
//
//	Telegram (WEB proxy) --HTTPS 443, SNI <host>--> RU entry (nginx stream, PROXY) or the Hub
//	Hub: nginx stream SNI map -> http nginx 8443 / 8446 (realip), server <host> (TGWebNginxConf)
//	     location / -> 127.0.0.1:8080 tproxy-server (admin 127.0.0.1:8081)
//	                    -> 127.0.0.1:TGMTProxyPort official MTProxy -> Telegram DCs
//
// The relay (telegramdesktop/tproxy-server, TProxyCommit) is built off the Hub and shipped as the
// artifact TGWebArtifact; the official MTProxy (TelegramMessenger/MTProxy, MTProxyCommit) is
// built on the Hub by TGWebMTProxyBuildScript. Units, the nft table and the refresh script are
// upstream's deploy/ files with our paths and ports; our nginx replaces upstream's Caddy.
//
// The client-facing secret is app_settings tg_webproxy_secret (32 hex). It is written only into
// profiles.json (0400) and mtproxy.env (0640 root:mtproxy) — never into a unit, a log line or a
// step output (the recipe registers it as a secret).

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

const (
	// TProxyRepo / TProxyCommit pin the relay source (2026-09-29, "Repair MTProxy permissions on
	// existing installs too"; its go test ./... passes on linux/amd64 — internal/tproxybuild runs it).
	TProxyRepo   = "https://github.com/telegramdesktop/tproxy-server.git"
	TProxyCommit = "c8adb8b7c6b7fc46c12ae3acb68be9070c26a8e8"

	// MTProxyCommit / MTProxyArchiveSHA256: the official MTProxy upstream's deploy/install-mtproxy.sh
	// pins (GitHub archive tarball of the commit).
	MTProxyCommit        = "f36d8af769ffaeac36978d38c2c0f6d1104c2137"
	MTProxyArchiveSHA256 = "919795c416b870670841a21d1930ad97a24c7b84b9eb8c6f9e3de32f2fdf4655"

	// TGWebArtifact: the relay binary next to the API (AppDir()/TGWebArtifact, with a .sha256),
	// put there by quick_deploy / deploy --ensure-hub-provisioning; installed as TGWebBinary.
	TGWebArtifact = "artifacts/tproxy-server"
	TGWebBinary   = "/usr/local/bin/tproxy-server"

	TGWebDir          = "/etc/tproxy-server"
	TGWebConfigPath   = TGWebDir + "/config.json"
	TGWebProfilesPath = TGWebDir + "/profiles.json"
	TGWebTokenKeyPath = TGWebDir + "/token.key"
	TGWebSiteDir      = "/srv/tproxy-site"

	// TGWebListen / TGWebAdminListen: the relay's public (behind nginx) and admin listeners.
	TGWebListen      = "127.0.0.1:8080"
	TGWebAdminListen = "127.0.0.1:8081"
	TGWebUnit        = "tproxy-server"
	TGWebUnitPath    = "/etc/systemd/system/" + TGWebUnit + ".service"

	// TGMTProxyPort is the official MTProxy's client port: on the exits (TGExitMTProxy*), and on
	// the Hub the forwarder's loopback listener the relay's profile points at. Not upstream's 2398:
	// that is inside 2088..2599, the Hub's ShadowTLS clients' range (2088+id, exit_stls.go) which
	// the egress guard opens to xray on loopback because nothing else listens there.
	TGMTProxyPort = 3398

	// The forwarder on the Hub (cmd/tgfwd): 127.0.0.1:TGMTProxyPort → an exit's MTProxy through
	// the Hub→exit tunnel. Telegram is unreachable from the Hub itself (checked 16.11.2026: core,
	// middle-ends, DCs all time out), so MTProxy runs on the exits.
	TGFwdArtifact     = "artifacts/tgfwd"
	TGFwdBinary       = "/usr/local/bin/heyearth-tgfwd"
	TGFwdUnit         = "heyearth-tgfwd"
	TGFwdUnitPath     = "/etc/systemd/system/" + TGFwdUnit + ".service"
	TGFwdDir          = "/etc/heyearth-tgfwd"
	TGFwdBackendsPath = TGFwdDir + "/backends.json"

	// TGWebNginxConfPath: the Hub's http server blocks of the WEB proxy host (port 80 always,
	// 8443 / 8446 once its certificate exists). The API owns this file; deploy --nginx never
	// writes or removes it.
	TGWebNginxConfPath = "/etc/nginx/conf.d/heyearth-tg.conf"
	// TGWebRouterMarker ends the 443 router's $backend map line of the WEB proxy host: the API
	// adds and removes the line; deploy --nginx carries marked lines over when it rewrites the
	// router (RouterManagedLines).
	TGWebRouterMarker = "# heyearth: tg-webproxy"
)

// TGWebSecretRe: the client-facing secret as the relay and the stock MTProxy take it (16 bytes, hex).
var TGWebSecretRe = regexp.MustCompile(`^[0-9a-f]{32}$`)

// tgHostRe: a lowercase ASCII DNS name with at least one dot (upstream install.sh's rule; IDN
// hosts are entered as A-labels).
var tgHostRe = regexp.MustCompile(`^(?:[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z](?:[a-z0-9-]{0,61}[a-z0-9])?$`)

// ValidTGWebHost reports whether host may be the WEB proxy's public hostname.
func ValidTGWebHost(host string) error {
	if len(host) > 253 || !tgHostRe.MatchString(host) {
		return fmt.Errorf("адрес WEB-прокси %q — не доменное имя (строчные латинские буквы, цифры, дефис и точки; кириллический домен — в виде xn--…)", host)
	}
	return nil
}

func checkTGWebSecret(secret string) error {
	if !TGWebSecretRe.MatchString(secret) {
		return fmt.Errorf("секрет WEB-прокси не в формате: 32 строчных hex-символа")
	}
	return nil
}

// TGWebConfigJSON is /etc/tproxy-server/config.json: one public hostname at the root (no base
// path), the static decoy site, exact static routes, the profiles from systemd's credential.
// Limits and timeouts are the relay's defaults (config.example.json); the server-side long poll
// (25 s) stays well under nginx's proxy_read_timeout (TGWebNginxConf).
func TGWebConfigJSON(host string) ([]byte, error) {
	if err := ValidTGWebHost(host); err != nil {
		return nil, err
	}
	type cfg struct {
		PublicHostname string `json:"public_hostname"`
		BasePath       string `json:"base_path"`
		Listen         string `json:"listen"`
		AdminListen    string `json:"admin_listen"`
		PublicDir      string `json:"public_dir"`
		StaticRoutes   string `json:"static_routes"`
		TokenKeyFile   string `json:"token_key_file"`
		ProfilesFile   string `json:"profiles_file"`
		EnablePprof    bool   `json:"enable_pprof"`
	}
	b, err := json.MarshalIndent(cfg{
		PublicHostname: host, Listen: TGWebListen, AdminListen: TGWebAdminListen,
		PublicDir: TGWebSiteDir, StaticRoutes: "exact", TokenKeyFile: TGWebTokenKeyPath,
		ProfilesFile: "/run/credentials/" + TGWebUnit + ".service/profiles.json",
	}, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}

// TGWebProfilesJSON is profiles.json: one profile, the HTTPS carrier, the local MTProxy.
func TGWebProfilesJSON(secret string) ([]byte, error) {
	if err := checkTGWebSecret(secret); err != nil {
		return nil, err
	}
	type profile struct {
		Name        string `json:"name"`
		Secret      string `json:"secret"`
		Backend     string `json:"backend"`
		CarrierMode string `json:"carrier_mode"`
	}
	b, err := json.MarshalIndent(map[string][]profile{"profiles": {{
		Name: "default", Secret: secret, Backend: fmt.Sprintf("127.0.0.1:%d", TGMTProxyPort), CarrierMode: "https",
	}}}, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}

// TGWebTokenKey checks the relay's persistent token-signing key (32 random bytes, stored hex in
// app_settings tg_webproxy_token_key so the database backup carries it) and returns its bytes.
func TGWebTokenKey(hexKey string) ([]byte, error) {
	b, err := hex.DecodeString(strings.TrimSpace(hexKey))
	if err != nil || len(b) != 32 {
		return nil, fmt.Errorf("ключ подписи токенов WEB-прокси не из 32 байт")
	}
	return b, nil
}

// TGWebRelayUnitText is upstream deploy/tproxy-server.service (hardening kept as is); its
// backend is the forwarder (TGFwdUnit) instead of a local MTProxy and its firewall unit.
const TGWebRelayUnitText = `[Unit]
Description=Telegram WEB proxy relay (tproxy-server, heyearth phase T)
After=network-online.target heyearth-tgfwd.service
Wants=network-online.target heyearth-tgfwd.service

[Service]
Type=simple
User=tproxy
Group=tproxy
LoadCredential=profiles.json:/etc/tproxy-server/profiles.json
ExecStart=/usr/local/bin/tproxy-server -config /etc/tproxy-server/config.json
Restart=on-failure
RestartSec=3s
TimeoutStopSec=20s
LimitNOFILE=1048576
NoNewPrivileges=true
PrivateDevices=true
PrivateTmp=true
ProtectClock=true
ProtectControlGroups=true
ProtectHome=true
ProtectHostname=true
ProtectKernelLogs=true
ProtectKernelModules=true
ProtectKernelTunables=true
ProtectProc=invisible
ProtectSystem=strict
ProcSubset=pid
ReadOnlyPaths=-/srv/tproxy-site
RestrictAddressFamilies=AF_INET AF_INET6
RestrictNamespaces=true
RestrictRealtime=true
RestrictSUIDSGID=true
LockPersonality=true
MemoryDenyWriteExecute=true
CapabilityBoundingSet=
IPAddressDeny=any
IPAddressAllow=localhost
SystemCallArchitectures=native
SystemCallFilter=@system-service
UMask=0077

[Install]
WantedBy=multi-user.target
`

// TGWebNginxConf renders TGWebNginxConfPath for host. Port 80 always: the HTTP→HTTPS redirect of
// the host itself (never to the site — a future neutral domain must not point at it) and the
// place certbot's nginx authenticator answers http-01 (through an entry: its port 80 goes to the
// Hub's). With certDir (the host's own certificate, /etc/letsencrypt/live/<host>) also the HTTPS
// server on 8443 and the PROXY mirror 127.0.0.1:8446, the same listen options as the site's
// blocks (an nginx socket takes one set): everything goes to the relay, which serves the decoy
// site itself (one gateway, no path of its own); no access log (the bridge URI is a credential);
// exactly one X-Forwarded-For address (the relay refuses a list); no buffering either way (long
// polls, 2 MiB uplink batches); read timeout above the relay's 25 s long poll; no site security
// headers (the bridge sets its own CSP); X25519/P-256 only (Android fails on X25519MLKEM768,
// tproxy-server#16 — OpenSSL 3.0 on the Hub has none, pinned anyway).
func TGWebNginxConf(host, certDir string) (string, error) {
	if err := ValidTGWebHost(host); err != nil {
		return "", err
	}
	var b strings.Builder
	fmt.Fprintf(&b, `# Generated by heyearth-api (config.TGWebNginxConf): Telegram WEB proxy %[1]s. Do not edit.
server {
    listen 80;
    listen [::]:80;
    server_name %[1]s;
    access_log off;
    return 301 https://$host$request_uri;
}
`, host)
	if certDir == "" {
		return b.String(), nil
	}
	if strings.ContainsAny(certDir, " ;{}\"'\n") || !strings.HasPrefix(certDir, "/") {
		return "", fmt.Errorf("каталог сертификата %q", certDir)
	}
	fmt.Fprintf(&b, `
map $http_upgrade $heyearth_tg_connection {
    default upgrade;
    ''      "";
}

upstream heyearth_tg_relay {
    server %[3]s;
    keepalive 32;
}

server {
    listen 8443 ssl http2;
    listen [::]:8443 ssl http2;
    # PROXY-protocol listener (127.0.0.1 only): the 443 router in PROXY mode and the RU entries
    listen 127.0.0.1:8446 ssl http2 proxy_protocol;
    set_real_ip_from 127.0.0.1;
    real_ip_header proxy_protocol;
    server_name %[1]s;

    ssl_certificate %[2]s/fullchain.pem;
    ssl_certificate_key %[2]s/privkey.pem;
    include /etc/letsencrypt/options-ssl-nginx.conf;
    ssl_dhparam /etc/letsencrypt/ssl-dhparams.pem;
    ssl_ecdh_curve X25519:prime256v1;
    add_header Strict-Transport-Security "max-age=31536000; includeSubDomains" always;

    # The bridge URI (/?bridge=...) and the carrier's bearer are credentials: no access log, and
    # no error log either (an upstream error line quotes the request).
    access_log off;
    error_log /dev/null crit;
    client_max_body_size 0;

    location / {
        proxy_pass http://heyearth_tg_relay;
        proxy_http_version 1.1;
        proxy_set_header Host $host;
        # Exactly one address: the relay refuses a list of them.
        proxy_set_header X-Forwarded-For $remote_addr;
        proxy_set_header X-Forwarded-Proto https;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection $heyearth_tg_connection;
        proxy_buffering off;
        proxy_request_buffering off;
        proxy_read_timeout 75s;
        proxy_send_timeout 75s;
    }
}
`, host, certDir, TGWebListen)
	return b.String(), nil
}

// ── the 443 router's map line of the WEB proxy host ──

var reRouterMapLine = regexp.MustCompile(`^\s*([A-Za-z0-9.*_-]+)\s+([A-Za-z0-9_]+)\s*;`)

// RouterManagedLines returns the router's lines that end with TGWebRouterMarker (deploy --nginx
// carries them into the router it generates).
func RouterManagedLines(router string) []string {
	var out []string
	for _, l := range strings.Split(router, "\n") {
		if strings.HasSuffix(strings.TrimRight(l, " \t\r"), TGWebRouterMarker) && reRouterMapLine.MatchString(l) {
			out = append(out, strings.TrimRight(l, " \t\r"))
		}
	}
	return out
}

// RouterWithManagedLines returns the router with every marked line removed and lines inserted
// into the `map $ssl_preread_server_name $backend` block, right before its `default` entry (or its
// closing brace). An error when the map is missing or a line names a host the map already routes
// itself (two entries for one name: nginx refuses the config).
func RouterWithManagedLines(router string, lines []string) (string, error) {
	in := strings.Split(router, "\n")
	var kept []string
	for _, l := range in {
		if strings.HasSuffix(strings.TrimRight(l, " \t\r"), TGWebRouterMarker) {
			continue
		}
		kept = append(kept, l)
	}
	inMap, at := false, -1
	existing := map[string]bool{}
	for i, l := range kept {
		code := l
		if j := strings.IndexByte(code, '#'); j >= 0 {
			code = code[:j]
		}
		code = strings.TrimSpace(code)
		switch {
		case reRouterMapHead.MatchString(code):
			inMap = true
		case inMap && strings.HasPrefix(code, "}"):
			if at < 0 {
				at = i
			}
			inMap = false
		case inMap:
			if m := reMapEntry.FindStringSubmatch(code); m != nil {
				if m[1] == "default" && at < 0 {
					at = i
				}
				existing[m[1]] = true
			}
		}
	}
	if at < 0 {
		return "", fmt.Errorf("в 443-маршрутизаторе нет блока map $ssl_preread_server_name $backend")
	}
	for _, l := range lines {
		m := reRouterMapLine.FindStringSubmatch(l)
		if m == nil || !strings.HasSuffix(l, TGWebRouterMarker) {
			return "", fmt.Errorf("строка %q — не строка map с меткой %q", l, TGWebRouterMarker)
		}
		if existing[m[1]] {
			return "", fmt.Errorf("имя %s уже есть в 443-маршрутизаторе без метки WEB-прокси — уберите его оттуда или выберите другой адрес", m[1])
		}
	}
	out := append([]string{}, kept[:at]...)
	out = append(out, lines...)
	out = append(out, kept[at:]...)
	return strings.Join(out, "\n"), nil
}

// TGWebRouterLine is the router's map line of host: to the http nginx's upstream (nginx_https,
// the one buildNginxStreamConf names for the site, whatever its port in the PROXY mode).
func TGWebRouterLine(host string) (string, error) {
	if err := ValidTGWebHost(host); err != nil {
		return "", err
	}
	return fmt.Sprintf("    %-28s nginx_https; %s", host, TGWebRouterMarker), nil
}

// TGWebDNSCheck compares the host's A / AAAA records with the addresses it must point at (the RU
// entries and the Hub): "" when every A record is one of them and there is no AAAA, else what to
// fix, in the admin's words.
func TGWebDNSCheck(host string, a, aaaa, expected []string) string {
	if len(a) == 0 {
		return fmt.Sprintf("У %s нет A-записей: добавьте у регистратора A-записи на адреса входов (те же, что у go.<домен>): %s", host, strings.Join(expected, ", "))
	}
	want := map[string]bool{}
	for _, e := range expected {
		if ip := net.ParseIP(strings.TrimSpace(e)); ip != nil {
			want[ip.String()] = true
		}
	}
	var stray []string
	for _, x := range a {
		if ip := net.ParseIP(strings.TrimSpace(x)); ip == nil || !want[ip.String()] {
			stray = append(stray, x)
		}
	}
	switch {
	case len(stray) > 0:
		return fmt.Sprintf("A-записи %s указывают не на входы и не на Hub: %s — исправьте у регистратора (нужны: %s)", host, strings.Join(stray, ", "), strings.Join(expected, ", "))
	case len(aaaa) > 0:
		return fmt.Sprintf("У %s есть AAAA-запись (%s), а у Hub и входов нет IPv6 — удалите её", host, strings.Join(aaaa, ", "))
	}
	return ""
}

// TGFwdUnitText runs the forwarder (cmd/tgfwd) as a throwaway user that can reach loopback only:
// the relay connects to it, it connects to the Hub's tunnel SOCKS ports.
var TGFwdUnitText = fmt.Sprintf(`[Unit]
Description=Telegram WEB proxy: relay streams to the exits' MTProxy through the tunnel (heyearth phase T)
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
DynamicUser=yes
ExecStart=%[1]s -listen 127.0.0.1:%[2]d -backends %[3]s
Restart=always
RestartSec=2s
LimitNOFILE=1048576
NoNewPrivileges=true
PrivateDevices=true
PrivateTmp=true
ProtectHome=true
ProtectSystem=strict
ProtectProc=invisible
ProcSubset=pid
ProtectKernelTunables=true
ProtectKernelModules=true
ProtectControlGroups=true
RestrictAddressFamilies=AF_INET AF_INET6
RestrictNamespaces=true
LockPersonality=true
MemoryDenyWriteExecute=true
CapabilityBoundingSet=
IPAddressDeny=any
IPAddressAllow=localhost
SystemCallArchitectures=native

[Install]
WantedBy=multi-user.target
`, TGFwdBinary, TGMTProxyPort, TGFwdBackendsPath)

// TGFwdTarget is one exit's MTProxy for the forwarder: its address and the Hub's local SOCKS
// ports that reach it (hysteria 1088+id first, ShadowTLS 2088+id second).
type TGFwdTarget struct {
	Exit  int      `json:"exit"`
	Addr  string   `json:"addr"`
	Socks []string `json:"socks"`
}

// TGFwdBackendsJSON is TGFwdBackendsPath for the exits whose MTProxy runs (by id).
func TGFwdBackendsJSON(exits map[int]string) []byte {
	ids := make([]int, 0, len(exits))
	for id := range exits {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	ts := []TGFwdTarget{}
	for _, id := range ids {
		ip := net.ParseIP(strings.TrimSpace(exits[id]))
		if ip == nil || id < 1 || id > 511 {
			continue
		}
		ts = append(ts, TGFwdTarget{Exit: id, Addr: net.JoinHostPort(ip.String(), strconv.Itoa(TGMTProxyPort)),
			Socks: []string{"127.0.0.1:" + strconv.Itoa(1088+id), "127.0.0.1:" + strconv.Itoa(hubSTLSSocksBase+id)}})
	}
	b, _ := json.MarshalIndent(map[string]any{"targets": ts}, "", "  ")
	return append(b, '\n')
}
