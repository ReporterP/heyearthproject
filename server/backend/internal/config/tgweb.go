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
	TGWebNftPath      = TGWebDir + "/firewall.nft"
	TGWebSiteDir      = "/srv/tproxy-site"

	// TGWebListen / TGWebAdminListen: the relay's public (behind nginx) and admin listeners.
	TGWebListen      = "127.0.0.1:8080"
	TGWebAdminListen = "127.0.0.1:8081"
	// TGMTProxyPort is the official MTProxy's client port. Not upstream's 2398: that is inside
	// 2088..2599, the Hub's ShadowTLS clients' range (2088+id, exit_stls.go) which the egress guard
	// opens to xray on loopback because nothing else listens there. TGMTProxyStatsPort: its stats.
	// mtproto-proxy has no bind option: both listen on 0.0.0.0 and TGWebNft drops them off lo.
	TGMTProxyPort      = 3398
	TGMTProxyStatsPort = 8888

	TGMTProxyDir     = "/opt/MTProxy"
	TGMTProxyBinary  = TGMTProxyDir + "/objs/bin/mtproto-proxy"
	TGMTProxyEtc     = "/etc/mtproxy"
	TGMTProxyEnvPath = TGMTProxyEtc + "/mtproxy.env"
	TGRefreshScript  = "/usr/local/sbin/refresh-mtproxy-config"

	TGWebUnit        = "tproxy-server"
	TGMTProxyUnit    = "mtproxy"
	TGFirewallUnit   = "tproxy-firewall"
	TGRefreshUnit    = "refresh-mtproxy-config"
	TGWebUnitPath    = "/etc/systemd/system/" + TGWebUnit + ".service"
	TGMTProxyUnitPth = "/etc/systemd/system/" + TGMTProxyUnit + ".service"
	TGFirewallPath   = "/etc/systemd/system/" + TGFirewallUnit + ".service"
	TGRefreshSvcPath = "/etc/systemd/system/" + TGRefreshUnit + ".service"
	TGRefreshTmrPath = "/etc/systemd/system/" + TGRefreshUnit + ".timer"

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

// TGMTProxyEnv is /etc/mtproxy/mtproxy.env (the official MTProxy's secret, one worker, 4096
// connections; natArgs "" or "--nat-info <local>:<public>" behind 1:1 NAT — see
// TGMTProxyNATScript).
func TGMTProxyEnv(secret, natArgs string) ([]byte, error) {
	if err := checkTGWebSecret(secret); err != nil {
		return nil, err
	}
	if natArgs != "" && !reNATArgs.MatchString(natArgs) {
		return nil, fmt.Errorf("MTPROXY_NAT_ARGS %q не вида --nat-info <локальный IPv4>:<публичный IPv4>", natArgs)
	}
	return []byte("MTPROXY_SECRET=" + secret + "\nMTPROXY_WORKERS=1\nMTPROXY_MAX_CONNECTIONS=4096\nMTPROXY_NAT_ARGS=" + natArgs + "\n"), nil
}

var reNATArgs = regexp.MustCompile(`^--nat-info [0-9]{1,3}(?:\.[0-9]{1,3}){3}:[0-9]{1,3}(?:\.[0-9]{1,3}){3}$`)

// TGWebTokenKey checks the relay's persistent token-signing key (32 random bytes, stored hex in
// app_settings tg_webproxy_token_key so the database backup carries it) and returns its bytes.
func TGWebTokenKey(hexKey string) ([]byte, error) {
	b, err := hex.DecodeString(strings.TrimSpace(hexKey))
	if err != nil || len(b) != 32 {
		return nil, fmt.Errorf("ключ подписи токенов WEB-прокси не из 32 байт")
	}
	return b, nil
}

// TGWebNft is upstream's second boundary for the MTProxy ports (mtproto-proxy binds 0.0.0.0).
var TGWebNft = fmt.Sprintf(`table inet tproxy_backend {
	chain local_backend {
		type filter hook input priority -10; policy accept;
		iifname != "lo" tcp dport { %d, %d } drop
	}
}
`, TGMTProxyPort, TGMTProxyStatsPort)

// TGWebRelayUnitText is upstream deploy/tproxy-server.service (hardening kept as is).
const TGWebRelayUnitText = `[Unit]
Description=Telegram WEB proxy relay (tproxy-server, heyearth phase T)
After=network-online.target mtproxy.service tproxy-firewall.service
Wants=network-online.target mtproxy.service
Requires=tproxy-firewall.service

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

// TGMTProxyUnitText is upstream deploy/mtproxy.service with our client port.
var TGMTProxyUnitText = fmt.Sprintf(`[Unit]
Description=Official Telegram MTProxy backend of the WEB proxy (heyearth phase T)
After=network-online.target tproxy-firewall.service
Wants=network-online.target
Requires=tproxy-firewall.service

[Service]
Type=simple
User=mtproxy
Group=mtproxy
EnvironmentFile=/etc/mtproxy/mtproxy.env
Environment=MTPROXY_WORKERS=1
Environment=MTPROXY_MAX_CONNECTIONS=4096
# Empty on a directly addressed host; --nat-info <local>:<public> behind 1:1 NAT (mtproxy.env).
# Unquoted so an empty value expands to no argument.
Environment=MTPROXY_NAT_ARGS=
WorkingDirectory=/opt/MTProxy
ExecStart=/opt/MTProxy/objs/bin/mtproto-proxy -u mtproxy -p %[2]d -H %[1]d -S ${MTPROXY_SECRET} $MTPROXY_NAT_ARGS --aes-pwd /etc/mtproxy/proxy-secret /etc/mtproxy/proxy-multi.conf -M ${MTPROXY_WORKERS} -C ${MTPROXY_MAX_CONNECTIONS}
Restart=on-failure
RestartSec=3s
LimitNOFILE=1048576
NoNewPrivileges=true
PrivateDevices=true
PrivateTmp=true
ProtectHome=true
ProtectProc=invisible
ProtectSystem=strict
ProcSubset=pid
ReadOnlyPaths=/etc/mtproxy
RestrictAddressFamilies=AF_INET AF_INET6
RestrictNamespaces=true
RestrictRealtime=true
LockPersonality=true

[Install]
WantedBy=multi-user.target
`, TGMTProxyPort, TGMTProxyStatsPort)

// TGFirewallUnitText is upstream deploy/tproxy-firewall.service.
const TGFirewallUnitText = `[Unit]
Description=Drop the WEB proxy's MTProxy ports off loopback (heyearth phase T)
After=nftables.service
PartOf=nftables.service
Before=mtproxy.service tproxy-server.service

[Service]
Type=oneshot
RemainAfterExit=true
ExecStart=-/usr/sbin/nft delete table inet tproxy_backend
ExecStart=/usr/sbin/nft -f /etc/tproxy-server/firewall.nft
ExecReload=-/usr/sbin/nft delete table inet tproxy_backend
ExecReload=/usr/sbin/nft -f /etc/tproxy-server/firewall.nft
ExecStop=-/usr/sbin/nft delete table inet tproxy_backend

[Install]
WantedBy=multi-user.target
`

// TGRefreshServiceText / TGRefreshTimerText / TGRefreshScriptText: upstream's daily refresh of
// proxy-multi.conf (MTProxy restarts only when the routing data changed).
const TGRefreshServiceText = `[Unit]
Description=Refresh the official MTProxy routing configuration (heyearth phase T)
After=network-online.target

[Service]
Type=oneshot
ExecStart=/usr/local/sbin/refresh-mtproxy-config
ProtectProc=invisible
ProcSubset=pid
NoNewPrivileges=true
PrivateTmp=true
ProtectHome=true
ProtectSystem=strict
ReadWritePaths=/etc/mtproxy
`

const TGRefreshTimerText = `[Unit]
Description=Daily official MTProxy routing refresh (heyearth phase T)

[Timer]
OnBootSec=10m
OnUnitActiveSec=1d
RandomizedDelaySec=1h
Persistent=true

[Install]
WantedBy=timers.target
`

const TGRefreshScriptText = `#!/usr/bin/env bash
set -euo pipefail

destination=/etc/mtproxy/proxy-multi.conf
temporary="$(mktemp /etc/mtproxy/proxy-multi.conf.XXXXXX)"
trap 'rm -f "$temporary"' EXIT

curl --fail --silent --show-error --location --proto '=https' --proto-redir '=https' --tlsv1.2 \
	--output "$temporary" https://core.telegram.org/getProxyConfig
test "$(wc -c < "$temporary")" -ge 100
grep -q '^default ' "$temporary"
grep -q '^proxy_for ' "$temporary"
chown root:mtproxy "$temporary"
chmod 0640 "$temporary"
if [[ -e "$destination" ]] && cmp -s "$temporary" "$destination"; then
	rm -f "$temporary"
	trap - EXIT
	exit 0
fi
mv -f "$temporary" "$destination"
trap - EXIT
systemctl try-restart mtproxy.service
`

// TGWebMTProxyBuildScript is upstream deploy/install-mtproxy.sh: the pinned, checksum-verified
// MTProxy source built unchanged by the unprivileged mtproxy user (skipped when /opt/MTProxy is
// already that commit), then proxy-secret and proxy-multi.conf over HTTPS. Idempotent.
var TGWebMTProxyBuildScript = fmt.Sprintf(`set -euo pipefail
if [[ "$(uname -m)" != "x86_64" ]]; then
	echo "the stock official MTProxy build requires an x86_64 server" >&2
	exit 1
fi
# On the live Hub: never upgrade what is installed, never let needrestart restart services (nginx,
# the API running this very script), never start a freshly installed nftables.service (its default
# /etc/nftables.conf begins with flush ruleset: the Hub's iptables-nft rules would go).
export DEBIAN_FRONTEND=noninteractive NEEDRESTART_MODE=l NEEDRESTART_SUSPEND=1
need=
for p in ca-certificates curl build-essential iproute2 libssl-dev util-linux zlib1g-dev; do
	dpkg-query -W -f='${Status}' "$p" 2>/dev/null | grep -q 'install ok installed' || need="$need $p"
done
command -v nft >/dev/null 2>&1 || need="$need nftables"
if [[ -n "$need" ]]; then
	printf '#!/bin/sh\nexit 101\n' > /usr/sbin/policy-rc.d; chmod 755 /usr/sbin/policy-rc.d
	trap 'rm -f /usr/sbin/policy-rc.d' EXIT
	apt-get update -qq
	apt-get install -y -qq --no-install-recommends --no-upgrade $need
	rm -f /usr/sbin/policy-rc.d
	trap - EXIT
fi
if ! id mtproxy >/dev/null 2>&1; then
	useradd --system --home /nonexistent --shell /usr/sbin/nologin mtproxy
fi

source_directory=%[1]s
mtproxy_commit=%[2]s
mtproxy_checksum=%[3]s
if [[ ! -x "$source_directory/objs/bin/mtproto-proxy" ]] ||
	[[ ! -f "$source_directory/.tproxy-commit" ]] ||
	! grep -Fxq "$mtproxy_commit" "$source_directory/.tproxy-commit"; then
	temporary="$(mktemp -d /tmp/mtproxy-build.XXXXXX)"
	chmod 0711 "$temporary"
	trap 'rm -rf "$temporary"' EXIT
	archive="$temporary/MTProxy.tar.gz"
	build_directory="$temporary/MTProxy"
	curl --fail --silent --show-error --location --proto '=https' --proto-redir '=https' --tlsv1.2 \
		--output "$archive" \
		"https://github.com/TelegramMessenger/MTProxy/archive/${mtproxy_commit}.tar.gz"
	test "$(sha256sum "$archive" | awk '{print $1}')" = "$mtproxy_checksum"
	install -d -o mtproxy -g mtproxy -m 0755 "$build_directory"
	tar -C "$build_directory" --strip-components=1 -xzf "$archive"
	chown -R mtproxy:mtproxy "$build_directory"
	runuser -u mtproxy -- nice -n 19 ionice -c 3 make -C "$build_directory" -j2 >/dev/null
	test -x "$build_directory/objs/bin/mtproto-proxy"
	printf '%%s\n' "$mtproxy_commit" > "$build_directory/.tproxy-commit"
	chown -R root:root "$build_directory"
	if [[ -e "$source_directory" ]]; then
		mv "$source_directory" "$source_directory.before-tproxy.$(date +%%Y%%m%%d%%H%%M%%S)"
	fi
	mv "$build_directory" "$source_directory"
	trap - EXIT
	rm -rf "$temporary" "$source_directory".before-tproxy.*
	# A new binary runs only after a restart: tg_services restarts every marked unit.
	mkdir -p %[5]s && touch %[5]s/mtproxy
	echo "MTProxy собран из $mtproxy_commit"
fi
find "$source_directory" -type d -exec chmod 0755 {} +
chmod 0755 "$source_directory/objs/bin/mtproto-proxy"

install -d -o root -g mtproxy -m 0750 %[4]s
if [[ ! -s %[4]s/proxy-secret ]] || [[ ! -s %[4]s/proxy-multi.conf ]]; then
	secret_temp="$(mktemp %[4]s/proxy-secret.XXXXXX)"
	config_temp="$(mktemp %[4]s/proxy-multi.conf.XXXXXX)"
	trap 'rm -f "$secret_temp" "$config_temp"' EXIT
	curl --fail --silent --show-error --location --proto '=https' --proto-redir '=https' --tlsv1.2 \
		--output "$secret_temp" https://core.telegram.org/getProxySecret
	curl --fail --silent --show-error --location --proto '=https' --proto-redir '=https' --tlsv1.2 \
		--output "$config_temp" https://core.telegram.org/getProxyConfig
	test "$(wc -c < "$secret_temp")" -eq 128
	test "$(wc -c < "$config_temp")" -ge 100
	grep -q '^default ' "$config_temp"
	grep -q '^proxy_for ' "$config_temp"
	chown root:mtproxy "$secret_temp" "$config_temp"
	chmod 0640 "$secret_temp" "$config_temp"
	mv -f "$secret_temp" %[4]s/proxy-secret
	mv -f "$config_temp" %[4]s/proxy-multi.conf
	trap - EXIT
	echo "proxy-secret и proxy-multi.conf получены с core.telegram.org"
fi
`, TGMTProxyDir, MTProxyCommit, MTProxyArchiveSHA256, TGMTProxyEtc, RestartMarkerDir)

// TGMTProxyNATScript prints NAT=<args> for mtproxy.env: "" when the source address the Hub uses
// towards Telegram's middle-ends is public (configured on an interface: no 1:1 NAT), otherwise
// --nat-info <local>:<public> with the public address measured by an echo service (upstream
// install.sh's logic). MTProxy hashes its own address into the middle-end keys: a wrong one
// leaves every stream silently hanging.
const TGMTProxyNATScript = `set -uo pipefail
local_address="$(ip -4 route get 149.154.175.50 2>/dev/null | sed -n 's/.*[[:space:]]src[[:space:]]\+\([0-9.]\+\).*/\1/p' | head -n 1)"
if [[ -z "$local_address" ]]; then echo "NAT_ERR=нет маршрута к Telegram (ip route get 149.154.175.50)"; exit 0; fi
if ! [[ "$local_address" =~ ^(10\.|127\.|169\.254\.|192\.168\.|172\.(1[6-9]|2[0-9]|3[01])\.|100\.(6[4-9]|[7-9][0-9]|1[01][0-9]|12[0-7])\.) ]]; then
	echo "NAT="
	exit 0
fi
for probe in https://api.ipify.org https://ifconfig.co/ip https://icanhazip.com; do
	candidate="$(curl --fail --silent --location --ipv4 --max-time 10 --proto '=https' --proto-redir '=https' --tlsv1.2 "$probe" 2>/dev/null | tr -d '[:space:]')" || continue
	if [[ "$candidate" =~ ^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
		echo "NAT=--nat-info $local_address:$candidate"
		exit 0
	fi
done
echo "NAT_ERR=адрес $local_address частный, а публичный не определился — MTProxy не сможет работать с Telegram"
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
