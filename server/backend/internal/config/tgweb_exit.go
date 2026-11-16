package config

// The official MTProxy of the Telegram WEB proxy on an exit (phase T, variant «MTProxy on the
// exits»: Telegram is unreachable from the Hub). The Hub's forwarder (cmd/tgfwd) reaches it
// through the Hub→exit tunnel at <exit's own address>:TGMTProxyPort — the hysteria ACL, the
// ShadowTLS route and the stls user's kernel guard allow exactly that (ExitHysteriaServerYAML &
// co. take the port). Nothing else may reach it: ufw never opens it and its own nft table drops
// both its ports for anything not arriving on lo (a connection of the exit to its own address
// does). Its own names everywhere: "mtproxy" on an exit is mtg, the classic proxy.

import (
	"fmt"
	"regexp"
)

const (
	TGExitMTProxyUser      = "tgmtproxy"
	TGExitMTProxyDir       = "/opt/heyearth/tg-mtproxy"
	TGExitMTProxyBinary    = TGExitMTProxyDir + "/objs/bin/mtproto-proxy"
	TGExitMTProxyEtc       = "/etc/heyearth/tg-mtproxy"
	TGExitMTProxyEnvPath   = TGExitMTProxyEtc + "/env"
	TGExitMTProxyNftPath   = TGExitMTProxyEtc + "/firewall.nft"
	TGExitMTProxyStatsPort = 3399
	TGExitMTProxyNftTable  = "heyearth_tg_mtproxy"

	TGExitMTProxyUnit     = "heyearth-tg-mtproxy"
	TGExitFirewallUnit    = "heyearth-tg-mtproxy-firewall"
	TGExitRefreshUnit     = "heyearth-tg-mtproxy-refresh"
	TGExitRefreshScript   = "/usr/local/sbin/heyearth-tg-mtproxy-refresh"
	TGExitMTProxyUnitPath = "/etc/systemd/system/" + TGExitMTProxyUnit + ".service"
	TGExitFirewallPath    = "/etc/systemd/system/" + TGExitFirewallUnit + ".service"
	TGExitRefreshSvcPath  = "/etc/systemd/system/" + TGExitRefreshUnit + ".service"
	TGExitRefreshTmrPath  = "/etc/systemd/system/" + TGExitRefreshUnit + ".timer"
)

var reNATArgs = regexp.MustCompile(`^--nat-info [0-9]{1,3}(?:\.[0-9]{1,3}){3}:[0-9]{1,3}(?:\.[0-9]{1,3}){3}$`)

// TGExitMTProxyEnv is its environment file: the secret, one worker, 4096 connections, natArgs ""
// or --nat-info <local>:<public> behind 1:1 NAT (TGMTProxyNATScript).
func TGExitMTProxyEnv(secret, natArgs string) ([]byte, error) {
	if err := checkTGWebSecret(secret); err != nil {
		return nil, err
	}
	if natArgs != "" && !reNATArgs.MatchString(natArgs) {
		return nil, fmt.Errorf("MTPROXY_NAT_ARGS %q не вида --nat-info <локальный IPv4>:<публичный IPv4>", natArgs)
	}
	return []byte("MTPROXY_SECRET=" + secret + "\nMTPROXY_WORKERS=1\nMTPROXY_MAX_CONNECTIONS=4096\nMTPROXY_NAT_ARGS=" + natArgs + "\n"), nil
}

// TGExitMTProxyNft drops both MTProxy ports for anything not arriving on lo (mtproto-proxy has no
// bind option: it listens on 0.0.0.0).
var TGExitMTProxyNft = fmt.Sprintf(`table inet %[1]s
delete table inet %[1]s
table inet %[1]s {
	chain input {
		type filter hook input priority -10; policy accept;
		iifname != "lo" tcp dport { %[2]d, %[3]d } drop
	}
}
`, TGExitMTProxyNftTable, TGMTProxyPort, TGExitMTProxyStatsPort)

// TGExitMTProxyUnitText is upstream deploy/mtproxy.service with our names and ports.
var TGExitMTProxyUnitText = fmt.Sprintf(`[Unit]
Description=Official Telegram MTProxy of the WEB proxy (heyearth phase T)
After=network-online.target %[4]s.service
Wants=network-online.target
Requires=%[4]s.service

[Service]
Type=simple
User=%[3]s
Group=%[3]s
EnvironmentFile=%[5]s
Environment=MTPROXY_WORKERS=1
Environment=MTPROXY_MAX_CONNECTIONS=4096
# Empty on a directly addressed host; --nat-info <local>:<public> behind 1:1 NAT.
# Unquoted so an empty value expands to no argument.
Environment=MTPROXY_NAT_ARGS=
WorkingDirectory=%[6]s
ExecStart=%[7]s -u %[3]s -p %[2]d -H %[1]d -S ${MTPROXY_SECRET} $MTPROXY_NAT_ARGS --aes-pwd %[8]s/proxy-secret %[8]s/proxy-multi.conf -M ${MTPROXY_WORKERS} -C ${MTPROXY_MAX_CONNECTIONS}
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
ReadOnlyPaths=%[8]s
RestrictAddressFamilies=AF_INET AF_INET6
RestrictNamespaces=true
RestrictRealtime=true
LockPersonality=true

[Install]
WantedBy=multi-user.target
`, TGMTProxyPort, TGExitMTProxyStatsPort, TGExitMTProxyUser, TGExitFirewallUnit, TGExitMTProxyEnvPath,
	TGExitMTProxyDir, TGExitMTProxyBinary, TGExitMTProxyEtc)

// TGExitFirewallUnitText is upstream deploy/tproxy-firewall.service with our table.
var TGExitFirewallUnitText = fmt.Sprintf(`[Unit]
Description=Drop the WEB proxy MTProxy's ports off loopback (heyearth phase T)
After=nftables.service
PartOf=nftables.service
Before=%[2]s.service

[Service]
Type=oneshot
RemainAfterExit=true
ExecStart=/usr/sbin/nft -f %[3]s
ExecReload=/usr/sbin/nft -f %[3]s
ExecStop=-/usr/sbin/nft delete table inet %[1]s

[Install]
WantedBy=multi-user.target
`, TGExitMTProxyNftTable, TGExitMTProxyUnit, TGExitMTProxyNftPath)

// TGExitRefreshServiceText / TimerText / ScriptText: upstream's daily refresh of
// proxy-multi.conf (MTProxy restarts only when the routing data changed).
var TGExitRefreshServiceText = fmt.Sprintf(`[Unit]
Description=Refresh the WEB proxy MTProxy's routing configuration (heyearth phase T)
After=network-online.target

[Service]
Type=oneshot
ExecStart=%[1]s
ProtectProc=invisible
ProcSubset=pid
NoNewPrivileges=true
PrivateTmp=true
ProtectHome=true
ProtectSystem=strict
ReadWritePaths=%[2]s
`, TGExitRefreshScript, TGExitMTProxyEtc)

const TGExitRefreshTimerText = `[Unit]
Description=Daily WEB proxy MTProxy routing refresh (heyearth phase T)

[Timer]
OnBootSec=10m
OnUnitActiveSec=1d
RandomizedDelaySec=1h
Persistent=true

[Install]
WantedBy=timers.target
`

var TGExitRefreshScriptText = fmt.Sprintf(`#!/usr/bin/env bash
set -euo pipefail

destination=%[1]s/proxy-multi.conf
temporary="$(mktemp %[1]s/proxy-multi.conf.XXXXXX)"
trap 'rm -f "$temporary"' EXIT

curl --fail --silent --show-error --location --proto '=https' --proto-redir '=https' --tlsv1.2 \
	--output "$temporary" https://core.telegram.org/getProxyConfig
test "$(wc -c < "$temporary")" -ge 100
grep -q '^default ' "$temporary"
grep -q '^proxy_for ' "$temporary"
chown root:%[2]s "$temporary"
chmod 0640 "$temporary"
if [[ -e "$destination" ]] && cmp -s "$temporary" "$destination"; then
	rm -f "$temporary"
	trap - EXIT
	exit 0
fi
mv -f "$temporary" "$destination"
trap - EXIT
systemctl try-restart %[3]s.service
`, TGExitMTProxyEtc, TGExitMTProxyUser, TGExitMTProxyUnit)

// TGExitMTProxyBuildScript is upstream deploy/install-mtproxy.sh with our names: the pinned,
// checksum-verified source built unchanged by the unprivileged user (skipped when already that
// commit), Telegram's proxy-secret and proxy-multi.conf over HTTPS. On a live exit: no package
// upgrades, needrestart restarts nothing, a freshly installed nftables.service never starts (its
// flush ruleset would drop the exit's tables), the build is niced. A rebuild marks the unit for
// restart. Idempotent.
var TGExitMTProxyBuildScript = fmt.Sprintf(`set -euo pipefail
if [[ "$(uname -m)" != "x86_64" ]]; then
	echo "the stock official MTProxy build requires an x86_64 server" >&2
	exit 1
fi
export DEBIAN_FRONTEND=noninteractive NEEDRESTART_MODE=l NEEDRESTART_SUSPEND=1
need=
for p in ca-certificates curl build-essential iproute2 libssl-dev util-linux zlib1g-dev; do
	dpkg-query -W -f='${Status}' "$p" 2>/dev/null | grep -q 'install ok installed' || need="$need $p"
done
command -v nft >/dev/null 2>&1 || need="$need nftables"
if [[ -n "$need" ]]; then
	ours=
	if [[ ! -e /usr/sbin/policy-rc.d ]]; then
		printf '#!/bin/sh\nexit 101\n' > /usr/sbin/policy-rc.d; chmod 755 /usr/sbin/policy-rc.d; ours=1
		trap 'rm -f /usr/sbin/policy-rc.d' EXIT
	fi
	apt-get update -qq
	apt-get install -y -qq --no-install-recommends --no-upgrade $need
	if [[ -n "$ours" ]]; then rm -f /usr/sbin/policy-rc.d; trap - EXIT; fi
fi
if ! id %[6]s >/dev/null 2>&1; then
	useradd --system --home /nonexistent --shell /usr/sbin/nologin %[6]s
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
	install -d -o %[6]s -g %[6]s -m 0755 "$build_directory"
	tar -C "$build_directory" --strip-components=1 -xzf "$archive"
	chown -R %[6]s:%[6]s "$build_directory"
	runuser -u %[6]s -- nice -n 19 ionice -c 3 make -C "$build_directory" -j2 >/dev/null
	test -x "$build_directory/objs/bin/mtproto-proxy"
	printf '%%s\n' "$mtproxy_commit" > "$build_directory/.tproxy-commit"
	chown -R root:root "$build_directory"
	mkdir -p "$(dirname "$source_directory")"
	if [[ -e "$source_directory" ]]; then
		mv "$source_directory" "$source_directory.previous"
	fi
	mv "$build_directory" "$source_directory"
	trap - EXIT
	rm -rf "$temporary" "$source_directory.previous"
	# A new binary runs only after a restart: the converge restarts a marked unit.
	mkdir -p %[5]s && touch %[5]s/%[7]s
	echo "MTProxy собран из $mtproxy_commit"
fi
find "$source_directory" -type d -exec chmod 0755 {} +
chmod 0755 "$source_directory/objs/bin/mtproto-proxy"

install -d -o root -g %[6]s -m 0750 %[4]s
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
	chown root:%[6]s "$secret_temp" "$config_temp"
	chmod 0640 "$secret_temp" "$config_temp"
	mv -f "$secret_temp" %[4]s/proxy-secret
	mv -f "$config_temp" %[4]s/proxy-multi.conf
	trap - EXIT
	echo "proxy-secret и proxy-multi.conf получены с core.telegram.org"
fi
`, TGExitMTProxyDir, MTProxyCommit, MTProxyArchiveSHA256, TGExitMTProxyEtc, RestartMarkerDir, TGExitMTProxyUser, TGExitMTProxyUnit)

// TGMTProxyNATScript prints NAT=<args> for the env file: "" when the source address towards
// Telegram's middle-ends is public (no 1:1 NAT), otherwise --nat-info <local>:<public> with the
// public address measured by an echo service (upstream install.sh's logic). MTProxy hashes its
// own address into the middle-end keys: a wrong one leaves every stream silently hanging.
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
