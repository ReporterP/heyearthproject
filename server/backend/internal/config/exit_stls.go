package config

import (
	"crypto/hkdf"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// The Hub→exit backup channel (B8 of the cleanup plan 2026-11-13). Chosen by a three-day
// measurement Hub → exit 2 (13–16.11.2026, 2 026 small probes and 410 downloads of 20 MB per
// channel, the live Hysteria path as the control): ShadowTLS v3 answered 100 % of both, 20 MB in
// 0.80 s median / 1.3 s p95 (evenings 0.85 / 1.5 s); an SSH tunnel lost two 20 MB transfers and
// had an 8 s p95 (evenings 2.8 s median). The VLESS Reality path it replaces (:10443) had been
// frozen by the TSPU since 12.11 (Beads cudk.89).
//
//	exit: sing-box ExitSTLSUnit — ShadowTLS v3 on TCP ExitSTLSPort (the handshake relayed to
//	      ExitSTLSSNI, strict mode) → in-process detour to Shadowsocks 2022 on
//	      127.0.0.1:exitSTLSSSPort → out, under the kernel egress guard (exit_guard.go);
//	Hub:  sing-box HubSTLSUnit(id) — SOCKS 127.0.0.1:HubSTLSSocksPort(id) → Shadowsocks 2022
//	      (UDP over TCP) → ShadowTLS → <exit>:443. The Hub's xray reaches it through the
//	      outbound exit-<id>.vr and the monitor's probe pair probe-vr-<id> (cmd/api xray_exits.go;
//	      the "vr" tags are historical names of the TCP path).
//
// No secrets of its own: both ends derive the ShadowTLS password and the Shadowsocks key from the
// exit's tunnel UUID — the keys file on the exit (ExitKeysPath), servers.tunnel_uuid in the DB —
// with HKDF-SHA256 under distinct labels (STLSSecrets). Nothing new is stored or synchronised,
// and a new tunnel UUID changes both.
const (
	SingBoxVersion = "1.14.4"
	// SingBoxArchiveSHA256 is the sha256 of sing-box-<version>-linux-amd64.tar.gz of the GitHub
	// release (checked 2026-11-16 on the Hub; the binary inside: 1264361b…aa3a, the one the B8
	// experiment ran for three days). Update both together.
	SingBoxArchiveSHA256 = "699a1b510aa4396b2176d32d1181920399ebf1e267835da6f3b598d0f7c24968"
	SingBoxDir           = "/opt/heyearth/sing-box"
	SingBoxBinary        = SingBoxDir + "/sing-box"

	ExitSTLSPort = 443
	// ExitSTLSSNI is the site the exit's ShadowTLS relays the TLS handshake to and the Hub names
	// in its ClientHello: TLS 1.3 (ShadowTLS v3 needs it), reachable from RU and from the exits,
	// a foreign SNI for a foreign address (Per-country SNI). The B8 experiment ran on it.
	ExitSTLSSNI      = "www.twitch.tv"
	ExitSTLSUnit     = "heyearth-stls"
	ExitSTLSConfig   = SingBoxDir + "/server.json"
	ExitSTLSUnitPath = "/etc/systemd/system/" + ExitSTLSUnit + ".service"

	// exitSTLSSSPort is the loopback Shadowsocks behind ShadowTLS (never reachable from outside).
	exitSTLSSSPort  = 8389
	stlsSSMethod    = "2022-blake3-aes-128-gcm"
	stlsFingerprint = "chrome"

	// HubSTLSDir holds the Hub's client configs (HubSTLSConfigPath, 0600 — they carry the secrets).
	HubSTLSDir = "/opt/heyearth/stls"
	// hubSTLSSocksBase: the Hub client of exit id listens on 2088+id, ids 1..511 → 2088..2599,
	// outside the hysteria clients' 1088..1599 and the probe inbounds 12000+id. The Hub's egress
	// guard lets xray reach exactly this range (cmd/deploy/ops_egress_guard.go egressSTLSPorts).
	hubSTLSSocksBase = 2088

	// The exit's xray of the Reality path, retired by the recipe's retire_xray step.
	ExitXrayDir      = "/opt/heyearth/xray"
	ExitXrayUnitPath = "/etc/systemd/system/xray.service"
)

// SingBoxDownloadURL is the linux-amd64 release archive of SingBoxVersion (exits and the Hub are
// x86_64: the preflight refuses anything else).
func SingBoxDownloadURL() string {
	return "https://github.com/SagerNet/sing-box/releases/download/v" + SingBoxVersion +
		"/sing-box-" + SingBoxVersion + "-linux-amd64.tar.gz"
}

// HubSTLSUnit is the Hub's ShadowTLS client unit of exit id (HubSTLSClientUnit).
func HubSTLSUnit(id int) string { return "heyearth-stls-client-" + strconv.Itoa(id) }

// HubSTLSConfigPath is that client's config (HubSTLSClientJSON).
func HubSTLSConfigPath(id int) string { return HubSTLSDir + "/client-" + strconv.Itoa(id) + ".json" }

// HubSTLSSocksPort is the loopback SOCKS of the Hub's ShadowTLS client of exit id.
func HubSTLSSocksPort(id int) int { return hubSTLSSocksBase + id }

// STLSSecrets derives the ShadowTLS v3 password (32 hex characters) and the Shadowsocks
// 2022-blake3-aes-128-gcm key (16 bytes, base64) of an exit from its tunnel UUID (canonical or 32
// bare hex digits, any case — both spell the same id).
func STLSSecrets(uuid string) (password, ssKey string, err error) {
	id := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(uuid), "-", ""))
	if b, err := hex.DecodeString(id); err != nil || len(b) != 16 {
		return "", "", errors.New("tunnel UUID missing or not a UUID")
	}
	pw, err := hkdf.Key(sha256.New, []byte(id), nil, "heyearth exit backup: shadowtls password v1", 16)
	if err != nil {
		return "", "", err
	}
	key, err := hkdf.Key(sha256.New, []byte(id), nil, "heyearth exit backup: ss2022 key v1", 16)
	if err != nil {
		return "", "", err
	}
	return hex.EncodeToString(pw), base64.StdEncoding.EncodeToString(key), nil
}

func marshalConfig(cfg map[string]any) ([]byte, error) {
	b, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}

// ExitSTLSServerJSON renders ExitSTLSConfig of the exit exitIP (ValidateExitIP): ShadowTLS v3 on
// 0.0.0.0:443 for the Hub's user, strict, detour to the loopback Shadowsocks 2022; out through
// "direct". The route rules mirror the hysteria ACL: a target name is resolved first, then the
// exit's own address is refused except tcp/<MTProxy port> (0 = none), and so are the private,
// loopback, multicast and broadcast ranges. The kernel guard (ExitGuardConf) is the second layer.
func ExitSTLSServerJSON(uuid, exitIP string, mtprotoPort int) ([]byte, error) {
	if err := ValidateExitIP(exitIP); err != nil {
		return nil, err
	}
	pw, key, err := STLSSecrets(uuid)
	if err != nil {
		return nil, err
	}
	ip := canonicalIP(exitIP)
	own := ip + "/32"
	if strings.Contains(ip, ":") {
		own = ip + "/128"
	}
	rules := []any{map[string]any{"action": "resolve", "strategy": "ipv4_only"}}
	if ports := validPorts([]int{mtprotoPort}); len(ports) > 0 {
		rules = append(rules, map[string]any{"ip_cidr": []string{own}, "network": []string{"tcp"}, "port": ports,
			"action": "route", "outbound": "direct"})
	}
	blocked := append(append([]string{own}, exitBlockedRanges...), "224.0.0.0/4", "255.255.255.255/32", "ff00::/8")
	rules = append(rules, map[string]any{"ip_cidr": blocked, "action": "reject"},
		map[string]any{"ip_is_private": true, "action": "reject"})
	dns := make([]any, len(exitSTLSDNSServers))
	for i, s := range exitSTLSDNSServers {
		dns[i] = map[string]any{"type": "udp", "tag": "dns-" + strconv.Itoa(i+1), "server": s}
	}
	return marshalConfig(map[string]any{
		"log": map[string]any{"level": "warn", "timestamp": false},
		"dns": map[string]any{"servers": dns, "strategy": "ipv4_only"},
		"inbounds": []any{
			map[string]any{"type": "shadowtls", "tag": "stls-in", "listen": "0.0.0.0", "listen_port": ExitSTLSPort, "version": 3,
				"users":       []any{map[string]any{"name": "hub", "password": pw}},
				"handshake":   map[string]any{"server": ExitSTLSSNI, "server_port": 443},
				"strict_mode": true, "detour": "ss-in"},
			map[string]any{"type": "shadowsocks", "tag": "ss-in", "listen": "127.0.0.1", "listen_port": exitSTLSSSPort,
				"method": stlsSSMethod, "password": key},
		},
		"outbounds": []any{map[string]any{"type": "direct", "tag": "direct"}},
		"route":     map[string]any{"rules": rules, "final": "direct", "default_domain_resolver": "dns-1"},
	})
}

// ExitSTLSUnitFile is ExitSTLSUnitPath: sing-box runs as ExitSTLSUser, the subject of the kernel
// egress guard it Requires= (exit_guard.go). The config is written by root as 0600 (the
// provisioning core compares content and mode, not the owner), so the privileged ("+")
// ExecStartPre hands it to the user on every start. sing-box sees ExitSTLSResolvConf as
// /etc/resolv.conf. Hardening: only CAP_NET_BIND_SERVICE (:443), no new privileges, read-only
// system, no /home, private /tmp; sing-box writes nothing (logs go to the journal).
const ExitSTLSUnitFile = `[Unit]
Description=Backup tunnel from the Hub: ShadowTLS v3 (HeYearth)
After=network.target ` + ExitGuardService + `.service
Requires=` + ExitGuardService + `.service
[Service]
Type=simple
User=` + ExitSTLSUser + `
Group=` + ExitSTLSUser + `
ExecStartPre=+/bin/chown ` + ExitSTLSUser + `:` + ExitSTLSUser + ` ` + ExitSTLSConfig + `
ExecStart=` + SingBoxBinary + ` run -c ` + ExitSTLSConfig + `
BindReadOnlyPaths=` + ExitSTLSResolvConfPath + `:/etc/resolv.conf
AmbientCapabilities=CAP_NET_BIND_SERVICE
CapabilityBoundingSet=CAP_NET_BIND_SERVICE
NoNewPrivileges=yes
ProtectSystem=strict
ProtectHome=yes
PrivateTmp=yes
Restart=always
RestartSec=5
LimitNOFILE=65535
[Install]
WantedBy=multi-user.target
`

// HubSTLSClientJSON renders HubSTLSConfigPath(id): SOCKS on 127.0.0.1:HubSTLSSocksPort(id) (TCP
// and UDP associate) → Shadowsocks 2022 with UDP over TCP (ShadowTLS carries TCP only) → ShadowTLS
// v3 to <exit>:443, SNI ExitSTLSSNI, uTLS chrome.
func HubSTLSClientJSON(id int, exitIP, uuid string) ([]byte, error) {
	if err := ValidateExitIP(exitIP); err != nil {
		return nil, err
	}
	if id < 1 || HubSTLSSocksPort(id) > 65535 {
		return nil, fmt.Errorf("exit id %d: no SOCKS port for its backup client", id)
	}
	pw, key, err := STLSSecrets(uuid)
	if err != nil {
		return nil, err
	}
	return marshalConfig(map[string]any{
		"log":      map[string]any{"level": "warn", "timestamp": false},
		"inbounds": []any{map[string]any{"type": "socks", "tag": "socks-in", "listen": "127.0.0.1", "listen_port": HubSTLSSocksPort(id)}},
		"outbounds": []any{
			map[string]any{"type": "shadowsocks", "tag": "ss", "method": stlsSSMethod, "password": key,
				"udp_over_tcp": map[string]any{"enabled": true, "version": 2}, "detour": "stls"},
			map[string]any{"type": "shadowtls", "tag": "stls", "server": canonicalIP(exitIP), "server_port": ExitSTLSPort, "version": 3,
				"password": pw,
				"tls": map[string]any{"enabled": true, "server_name": ExitSTLSSNI,
					"utls": map[string]any{"enabled": true, "fingerprint": stlsFingerprint}}},
		},
		"route": map[string]any{"final": "ss"},
	})
}

// HubSTLSClientUnit is the Hub's unit of HubSTLSClientJSON. A dynamic user (no account on the Hub,
// no capabilities) reads the root-only config through LoadCredential; the client only ever dials
// its exit, so the Hub's egress guard does not need to know it.
func HubSTLSClientUnit(id int, exitIP string) string {
	return `[Unit]
Description=Backup tunnel client to exit id=` + strconv.Itoa(id) + ` (` + canonicalIP(exitIP) + `): ShadowTLS v3 — HeYearth
After=network.target
[Service]
Type=simple
DynamicUser=yes
LoadCredential=client.json:` + HubSTLSConfigPath(id) + `
ExecStart=` + SingBoxBinary + ` run -c ${CREDENTIALS_DIRECTORY}/client.json
NoNewPrivileges=yes
ProtectSystem=strict
ProtectHome=yes
PrivateTmp=yes
CapabilityBoundingSet=
AmbientCapabilities=
Restart=always
RestartSec=3
LimitNOFILE=65535
[Install]
WantedBy=multi-user.target
`
}

// SingBoxInstallScript installs the pinned sing-box for unit (see singBoxInstallScript).
func SingBoxInstallScript(unit string) string {
	return singBoxInstallScript(unit, SingBoxArchiveSHA256)
}

// singBoxInstallScript renders the installer for the expected archive sha256 (tests pass the
// hash of their stub download). It acts when the binary is not SingBoxVersion: download to a temp
// dir in SingBoxDir (same filesystem: atomic mv; stale dirs of a dropped run are removed), sha256
// before unpacking and any execution, version check of the new binary, the previous binary kept as
// sing-box.bak (temp + mv; with no previous binary a leftover .bak is removed, so a rollback
// cannot resurrect it). Before the move the unit's restart marker and pending-binary marker (the
// new binary's sha256, BinaryPendingMarker) are set: an interruption right after it is still
// restarted and, if unhealthy, rolled back. On the Hub the binary is shared by the clients of
// every exit: the others pick a new one up on their next restart. On failure "[sing-box] ERROR"
// goes to stderr, nothing is changed and it returns non-zero: run under set -e. Restarts nothing.
func singBoxInstallScript(unit, sha string) string {
	return `SB_WANT="` + SingBoxVersion + `"
SB_SHA256="` + sha + `"
SB_DIR=` + SingBoxDir + `
SB_CUR=$("$SB_DIR/sing-box" version 2>/dev/null | awk 'NR==1{print $3}') || SB_CUR=""
sb_fetch() {
  curl -fsSL --connect-timeout 15 --max-time 300 --retry 3 --retry-delay 2 -o "$SB_TMP/sb.tgz" "` + SingBoxDownloadURL() + `" \
    && echo "$SB_SHA256  $SB_TMP/sb.tgz" | sha256sum -c --quiet \
    && tar -xzf "$SB_TMP/sb.tgz" -C "$SB_TMP" --strip-components=1 "sing-box-$SB_WANT-linux-amd64/sing-box" \
    && chmod 755 "$SB_TMP/sing-box" \
    && [ "$("$SB_TMP/sing-box" version 2>/dev/null | awk 'NR==1{print $3}')" = "$SB_WANT" ]
}
if [ "$SB_CUR" != "$SB_WANT" ]; then
  mkdir -p "$SB_DIR"
  rm -rf "$SB_DIR"/.dl.*
  SB_TMP=$(mktemp -d "$SB_DIR/.dl.XXXXXX")
  if sb_fetch; then
    mkdir -p ` + RestartMarkerDir + ` ` + BinaryPendingDir + `
    touch ` + RestartMarker(unit) + `
    if [ -f "$SB_DIR/sing-box" ]; then
      cp -p "$SB_DIR/sing-box" "$SB_DIR/sing-box.bak.tmp"
      mv -f "$SB_DIR/sing-box.bak.tmp" "$SB_DIR/sing-box.bak"
    else
      rm -f "$SB_DIR/sing-box.bak"
    fi
    SB_NEW_SHA=$(sha256sum < "$SB_TMP/sing-box" | cut -d' ' -f1)
    printf '%s  %s\n' "$SB_NEW_SHA" "$SB_DIR/sing-box" > ` + BinaryPendingMarker(unit) + `
    mv -f "$SB_TMP/sing-box" "$SB_DIR/sing-box"
    rm -rf "$SB_TMP"
    echo "[sing-box] installed $SB_WANT (previous: ${SB_CUR:-none})"
  else
    rm -rf "$SB_TMP"
    echo "[sing-box] ERROR: download/sha256/verify of $SB_WANT failed, binary unchanged" >&2
    false
  fi
else
  echo "[sing-box] $SB_WANT already installed"
fi
`
}

// singBoxCheck refuses a config sing-box does not accept, before any restart.
func singBoxCheck(cfg string) string {
	return `if ! SB_TEST=$(` + SingBoxBinary + ` check -c ` + shQuote(cfg) + ` 2>&1); then
  printf '%s\n' "$SB_TEST" | tail -n 20 >&2
  echo "[sing-box] ERROR: конфигурация не прошла проверку sing-box check — служба не перезапущена" >&2
  exit 1
fi
echo "[sing-box] конфигурация прошла проверку (sing-box check)"
`
}

// ExitSTLSPrepareScript is the converge extra of the exit's tunnel (UnitConvergeScript(
// ExitSTLSUnit, …)), after the recipe wrote the config, the unit and the guard files
// (ExitGuardConfPath, ExitGuardUnitPath, ExitSTLSResolvConfPath): the system user, the kernel
// egress guard loaded and enabled (refused when its files are missing — sing-box never starts
// unguarded), the pinned binary (it sets the markers itself), then `sing-box check`.
func ExitSTLSPrepareScript() string { return exitSTLSPrepareScript(SingBoxInstallScript(ExitSTLSUnit)) }

func exitSTLSPrepareScript(install string) string {
	return ExitSTLSUserScript +
		"for sb_f in " + ExitGuardConfPath + " " + ExitGuardUnitPath + " " + ExitSTLSResolvConfPath + "; do\n" +
		"  [ -f \"$sb_f\" ] || { echo \"[stls] ERROR: нет $sb_f — без защиты исходящих туннель не запускается\" >&2; exit 1; }\ndone\n" +
		ExitGuardApplyScript + install + singBoxCheck(ExitSTLSConfig)
}

// HubSTLSPrepareScript is the converge extra of the Hub's client of exit id: the pinned binary
// (markers of that client's unit), then `sing-box check` of its config.
func HubSTLSPrepareScript(id int) string {
	return singBoxInstallScript(HubSTLSUnit(id), SingBoxArchiveSHA256) + singBoxCheck(HubSTLSConfigPath(id))
}

// STLSCheckScript prints VERSION=<sing-box version>, LISTEN=0|1 (sing-box on TCP 443), GUARD=0|1
// (the kernel egress guard of the tunnel user is loaded).
var STLSCheckScript = opLine("stls-check") +
	`echo "VERSION=$(` + SingBoxBinary + ` version 2>/dev/null | awk 'NR==1{print $3}')"
if ss -Htlpn 'sport = :` + strconv.Itoa(ExitSTLSPort) + `' 2>/dev/null | grep -q '"sing-box"'; then echo LISTEN=1; else echo LISTEN=0; fi
if nft list table inet ` + ExitGuardTable + ` 2>/dev/null | grep -q 'jump tunnel_egress'; then echo GUARD=1; else echo GUARD=0; fi
`

// ExitRetireXrayScript removes the exit's xray of the old backup path (VLESS Reality :10443):
// the unit stopped, disabled and deleted with its drop-ins, ExitXrayDir (binary, and the config
// that carries the tunnel UUID) deleted, the 10443/tcp allowances of ufw / iptables dropped, the
// system user xray deleted. The kernel guard stays (it now guards sing-box). Idempotent; the
// recipe runs it only after the ShadowTLS path answered end to end.
var ExitRetireXrayScript = opLine("retire-xray") + `set -e
if [ -f ` + ExitXrayUnitPath + ` ] || systemctl is-active --quiet xray 2>/dev/null; then
  systemctl disable --now xray >/dev/null 2>&1 || true
  rm -rf ` + ExitXrayUnitPath + ` ` + ExitXrayUnitPath + `.d
  systemctl daemon-reload
  systemctl reset-failed xray >/dev/null 2>&1 || true
  echo "[stls] xray (VLESS Reality :10443) остановлен и удалён"
fi
rm -rf ` + ExitXrayDir + `
if command -v ufw >/dev/null 2>&1 && LC_ALL=C ufw status 2>/dev/null | grep -q '^Status: active'; then
  ufw delete allow 10443/tcp >/dev/null 2>&1 || true
fi
if command -v iptables >/dev/null 2>&1; then
  rx_ipt=0
  while iptables -C INPUT -p tcp --dport 10443 -j ACCEPT 2>/dev/null; do iptables -D INPUT -p tcp --dport 10443 -j ACCEPT; rx_ipt=1; done
  if [ "$rx_ipt" = 1 ] && command -v netfilter-persistent >/dev/null 2>&1; then netfilter-persistent save >/dev/null 2>&1 || true; fi
fi
if id -u xray >/dev/null 2>&1 && ! pgrep -u xray >/dev/null 2>&1; then userdel xray >/dev/null 2>&1 || true; fi
`

// ExitXrayStateScript prints XRAY=0|1: anything of the retired xray is left (its unit file, a
// running unit or ExitXrayDir).
var ExitXrayStateScript = opLine("xray-state") +
	`if [ -e ` + ExitXrayUnitPath + ` ] || [ -e ` + ExitXrayDir + ` ] || systemctl is-active --quiet xray 2>/dev/null; then echo XRAY=1; else echo XRAY=0; fi
`
