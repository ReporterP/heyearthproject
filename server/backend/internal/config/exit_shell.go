package config

import (
	"fmt"
	"path"
	"strconv"
	"strings"

	"github.com/heyearth/backend/internal/shellquote"
)

// Shell primitives of the provisioning recipes (internal/provision): every op script starts with
// the line "# hp:<op> [args]" — a bash comment by which the core's fake host in tests recognises
// the operation. The recipes run them as root through Remote.Run (bash, script on stdin/in a
// temp file), so every argument that is not a code constant is single-quoted (shQuote).

// shQuote single-quotes s for a POSIX shell (the shared shellquote.Quote).
func shQuote(s string) string { return shellquote.Quote(s) }

// opLine renders the marker line. It is a comment, so line breaks in the arguments are flattened:
// they would end the comment and run the rest as a command.
func opLine(op string, args ...string) string {
	l := "# hp:" + strings.Join(append([]string{op}, args...), " ")
	return strings.NewReplacer("\n", " ", "\r", " ").Replace(l) + "\n"
}

// BinaryPendingDir holds one file per unit whose binary an installer replaced in a run that has
// not passed restart_checked yet; the file names the binary. UnitRollbackScript puts <binary>.bak
// back only then: a .bak alone is never restored (exit 2 keeps hysteria 2.6.1 as
// /usr/local/bin/hysteria.bak next to 2.13.0 — a failed config change must not downgrade it).
// Same idea as /usr/local/bin/hysteria.pending of deploy --setup-hysteria2-hub.
const BinaryPendingDir = "/opt/heyearth/state/binary-pending"

// BinaryPendingMarker is the pending-binary file of a unit (see BinaryPendingDir).
func BinaryPendingMarker(unit string) string { return BinaryPendingDir + "/" + unit }

// RestartCheckFunc defines restart_checked UNIT: restart, then two samples of NRestarts 3 s and
// 6 s later — healthy only when the unit is active both times and NRestarts did not move (a crash
// loop under Restart=always looks "active" at any single instant; the hy_healthy check of
// deploy --setup-hysteria2-hub). A failing `systemctl restart` is not fatal by itself: the check
// decides and prints why. On failure the unit's journal tail goes to stderr and it returns 1.
// RESTART_CHECK_SLEEP / RESTART_CHECK_SLEEP2 override the waits (tests).
const RestartCheckFunc = `restart_checked() {
  systemctl restart "$1" || echo "[unit] systemctl restart $1 завершился с ошибкой" >&2
  sleep "${RESTART_CHECK_SLEEP:-3}"
  if ! systemctl is-active --quiet "$1"; then
    echo "[unit] $1 не запустился" >&2
    journalctl -u "$1" -n 20 --no-pager -o cat >&2 2>/dev/null || true
    return 1
  fi
  rc_n1=$(systemctl show -p NRestarts --value "$1") || rc_n1=x
  sleep "${RESTART_CHECK_SLEEP2:-6}"
  rc_n2=$(systemctl show -p NRestarts --value "$1") || rc_n2=y
  if ! systemctl is-active --quiet "$1" || [ "$rc_n1" != "$rc_n2" ]; then
    echo "[unit] $1 перезапускается сам (NRestarts $rc_n1 -> $rc_n2)" >&2
    journalctl -u "$1" -n 20 --no-pager -o cat >&2 2>/dev/null || true
    return 1
  fi
  echo "[unit] $1 работает стабильно (NRestarts=$rc_n2)"
}
`

// UnitConvergeScript runs extra (installers, config tests; they may set the markers), enables the
// unit and restarts it only when its restart marker exists or it is not running. The markers are
// removed only after restart_checked passed; a failure anywhere leaves them for the next run and
// for UnitRollbackScript.
func UnitConvergeScript(unit, extra string) string {
	return opLine("converge", unit) + "set -e\n" + RestartCheckFunc +
		"CV_UNIT=" + shQuote(unit) + "\nCV_MARK=" + shQuote(RestartMarker(unit)) + "\nCV_BIN=" + shQuote(BinaryPendingMarker(unit)) + "\n" +
		"mkdir -p " + RestartMarkerDir + " " + BinaryPendingDir + "\nsystemctl daemon-reload\n" + extra +
		`systemctl enable --quiet "$CV_UNIT"
if [ -f "$CV_MARK" ] || ! systemctl is-active --quiet "$CV_UNIT"; then
  restart_checked "$CV_UNIT"
  rm -f "$CV_MARK" "$CV_BIN"
else
  echo "[unit] $CV_UNIT: без изменений и работает — без перезапуска"
fi
`
}

// UnitRollbackScript undoes this run for a unit: every changed file (written by PutFile in this
// run) gets its .bak back (copied, so a repeated rollback gives the same result) or, when it had
// no previous version, is removed; a binary replaced and still pending (BinaryPendingMarker) gets
// its .bak back. Then: something restored → restart_checked (the previous version must run);
// only new files → the fresh install is disabled and stopped; nothing changed in this run (e.g.
// resumed after an SSH drop) → the unit is left exactly as it is, restart marker included.
func UnitRollbackScript(unit string, changed []string) string {
	var b strings.Builder
	b.WriteString(opLine("rollback", unit) + "set -e\n" + RestartCheckFunc)
	b.WriteString("RB_UNIT=" + shQuote(unit) + "\nRB_MARK=" + shQuote(RestartMarker(unit)) + "\nRB_BIN=" + shQuote(BinaryPendingMarker(unit)) +
		"\nrestored=0\nremoved=0\n")
	b.WriteString(`rb_restore() {
  if [ -f "$1.bak" ]; then
    cp -p "$1.bak" "$1.rollback.tmp"
    mv -f "$1.rollback.tmp" "$1"
    restored=1
    echo "[unit] $RB_UNIT: возвращён прежний $1"
  else
    rm -f "$1"
    removed=1
  fi
}
`)
	for _, p := range changed {
		b.WriteString("rb_restore " + shQuote(p) + "\n")
	}
	b.WriteString(`if [ -f "$RB_BIN" ]; then
  rb_b=$(head -n 1 "$RB_BIN")
  if [ -n "$rb_b" ] && [ -f "$rb_b.bak" ]; then
    cp -p "$rb_b.bak" "$rb_b.rollback.tmp"
    mv -f "$rb_b.rollback.tmp" "$rb_b"
    restored=1
    echo "[unit] $RB_UNIT: возвращён прежний бинарник $rb_b"
  fi
  rm -f "$RB_BIN"
fi
systemctl daemon-reload
if [ "$restored" = 1 ]; then
  restart_checked "$RB_UNIT"
  rm -f "$RB_MARK"
  echo "[unit] $RB_UNIT: возвращена прежняя версия"
elif [ "$removed" = 1 ]; then
  systemctl disable --now "$RB_UNIT" >/dev/null 2>&1 || true
  rm -f "$RB_MARK"
  echo "[unit] $RB_UNIT: новая установка снята"
else
  echo "[unit] $RB_UNIT: в этом запуске файлы не менялись — возвращать нечего"
fi
`)
	return b.String()
}

// UnitStateScript prints ACTIVE=0|1 (is-active) and MARKER=0|1 (restart pending).
func UnitStateScript(unit string) string {
	return opLine("unit-state", unit) + "US_UNIT=" + shQuote(unit) + "\nUS_MARK=" + shQuote(RestartMarker(unit)) + "\n" +
		`if systemctl is-active --quiet "$US_UNIT"; then echo ACTIVE=1; else echo ACTIVE=0; fi
if [ -f "$US_MARK" ]; then echo MARKER=1; else echo MARKER=0; fi
`
}

// MarkRestartScript sets the unit's restart marker; the core runs it BEFORE writing a differing
// file, so an SSH drop between the write and the restart cannot lose the restart.
func MarkRestartScript(unit string) string {
	return opLine("mark", unit) + "set -e\nmkdir -p " + RestartMarkerDir + "\ntouch " + shQuote(RestartMarker(unit)) + "\n"
}

// SumsScript prints "<sha256>  <path>" for every path that is a regular file, the path exactly
// as given (hashed from stdin, so sha256sum never escapes or prefixes it).
func SumsScript(paths ...string) string {
	if len(paths) == 0 {
		return opLine("sums") + "true\n"
	}
	q := make([]string, len(paths))
	for i, p := range paths {
		q[i] = shQuote(p)
	}
	return opLine("sums", paths...) + "for p in " + strings.Join(q, " ") + "; do\n" +
		"  if [ -f \"$p\" ] && s=$(sha256sum < \"$p\"); then printf '%s  %s\\n' \"${s%% *}\" \"$p\"; fi\ndone\ntrue\n"
}

// CatScript prints the file, nothing when it is absent.
func CatScript(p string) string {
	return opLine("cat", p) + "cat " + shQuote(p) + " 2>/dev/null || true\n"
}

// ExitFirewallScript never switches a firewall on. If ufw is active it allows the SSH port, the
// tunnels (hysteria UDP 50443 + the hop range, Reality TCP 10443), the MTProxy port and
// node-agent :50052 from the Hub's addresses only (ufw skips existing rules itself). Else, if
// iptables filters INPUT (DROP/REJECT policy or rule), the same rules are inserted once (-C
// first); IPv6 Hub addresses are left to ufw — exits run with IPv6 disabled (ExitSysctlConf).
// A native nftables input chain with a drop policy is not ours to edit: only a warning. Invalid
// ports (sshPort, mtprotoPort 0 = no MTProxy) and non-IP Hub addresses are left out.
func ExitFirewallScript(sshPort int, hubIPs []string, mtprotoPort int) string {
	tcp := validPorts([]int{sshPort, ExitRealityPort, mtprotoPort})
	udp := []string{strconv.Itoa(ExitHysteriaPort), strings.ReplaceAll(ExitHopRange, "-", ":")}
	var hubs []string
	seen := map[string]bool{}
	for _, ip := range hubIPs {
		if c := canonicalIP(ip); c != "" && !seen[c] {
			seen[c] = true
			hubs = append(hubs, c)
		}
	}
	ports := make([]string, 0, len(tcp))
	for _, p := range tcp {
		ports = append(ports, "TCP "+strconv.Itoa(p))
	}
	need := strings.Join(ports, ", ") + ", UDP " + strconv.Itoa(ExitHysteriaPort) + ", UDP " + ExitHopRange + ", TCP " + strconv.Itoa(NodeAgentPort) + " с адресов Hub"

	var b strings.Builder
	b.WriteString(opLine("firewall") + "set -e\nif command -v ufw >/dev/null 2>&1 && ufw status 2>/dev/null | grep -q '^Status: active'; then\n" +
		"  echo \"[fw] ufw включён — добавляю разрешения\"\n")
	for _, p := range tcp {
		fmt.Fprintf(&b, "  ufw allow %d/tcp >/dev/null\n", p)
	}
	for _, p := range udp {
		fmt.Fprintf(&b, "  ufw allow %s/udp >/dev/null\n", p)
	}
	for _, h := range hubs {
		fmt.Fprintf(&b, "  ufw allow from %s to any port %d proto tcp >/dev/null\n", h, NodeAgentPort)
	}
	b.WriteString("elif iptables -S INPUT 2>/dev/null | grep -qE '^-P INPUT (DROP|REJECT)|^-A INPUT .*-j (DROP|REJECT)'; then\n" +
		"  echo \"[fw] iptables фильтрует входящие — добавляю разрешения\"\n" +
		"  ipt_allow() { iptables -C INPUT \"$@\" -j ACCEPT 2>/dev/null || iptables -I INPUT \"$@\" -j ACCEPT; }\n")
	for _, p := range tcp {
		fmt.Fprintf(&b, "  ipt_allow -p tcp --dport %d\n", p)
	}
	for _, p := range udp {
		fmt.Fprintf(&b, "  ipt_allow -p udp --dport %s\n", p)
	}
	for _, h := range hubs {
		if strings.Contains(h, ":") {
			fmt.Fprintf(&b, "  echo \"[fw] IPv6-адрес Hub %s пропущен: IPv6 на exit выключен\"\n", h)
			continue
		}
		fmt.Fprintf(&b, "  ipt_allow -p tcp -s %s --dport %d\n", h, NodeAgentPort)
	}
	b.WriteString("  if command -v netfilter-persistent >/dev/null 2>&1; then\n" +
		"    netfilter-persistent save >/dev/null 2>&1 || echo \"[fw] ВНИМАНИЕ: netfilter-persistent save не удался — правила пропадут после перезагрузки\" >&2\n" +
		"  else\n    echo \"[fw] ВНИМАНИЕ: netfilter-persistent не установлен — эти правила iptables пропадут после перезагрузки\" >&2\n  fi\n" +
		"elif command -v nft >/dev/null 2>&1 && nft list ruleset 2>/dev/null | grep -qE 'hook input .*policy (drop|reject)'; then\n" +
		"  echo \"[fw] ВНИМАНИЕ: в nftables есть собственный фильтр входящих (policy drop) — его не меняю; откройте вручную: " + need + "\" >&2\n" +
		"else\n  echo \"[fw] фаервол выключен — не включаю и ничего не меняю\"\nfi\n")
	return b.String()
}

// Exit Xray: pinned like hysteria (HysteriaVersion/HysteriaSHA256). XrayExitSHA256 is the
// "SHA2-256" line of Xray-linux-64.zip.dgst of the release, re-checked against the downloaded
// asset (2026-10-15). Update both together.
const (
	XrayExitVersion = "v26.3.27"
	XrayExitSHA256  = "23cd9af937744d97776ee35ecad4972cf4b2109d1e0fe6be9930467608f7c8ae"
)

// XrayExitDownloadURL is the linux-64 release archive of XrayExitVersion.
func XrayExitDownloadURL() string {
	return "https://github.com/XTLS/Xray-core/releases/download/" + XrayExitVersion + "/Xray-linux-64.zip"
}

// XrayExitInstallScript installs the pinned xray on an exit (see xrayExitInstallScript).
func XrayExitInstallScript() string { return xrayExitInstallScript(XrayExitSHA256) }

// xrayExitInstallScript renders the installer for the expected archive sha256 (tests pass the
// hash of their stub download). It acts when the binary is not XrayExitVersion or geoip.dat /
// geosite.dat are missing (the exit config's guard rules use geoip:private; earlier installs
// unzipped the whole archive, the provisioning core must not depend on that): download to a temp
// dir in /opt/heyearth/xray (same filesystem: atomic mv; stale dirs of a dropped run are
// removed), sha256 before unzip and any execution, version check of the new binary, data files
// installed from the same archive, the previous binary kept as xray.bak (temp + mv). Sets
// XR_REPLACED=1 when the binary was replaced, XR_DATA=1 when the data files were (re)installed.
// On failure "[xray] ERROR" goes to stderr, nothing is changed and it returns non-zero: run
// under set -e. Does not restart anything.
func xrayExitInstallScript(sha string) string {
	return `XR_WANT="` + strings.TrimPrefix(XrayExitVersion, "v") + `"
XR_SHA256="` + sha + `"
XR_DIR=` + path.Dir(ExitXrayBinary) + `
XR_REPLACED=0
XR_DATA=0
XR_CUR=$("$XR_DIR/xray" version 2>/dev/null | awk 'NR==1{print $2}') || XR_CUR=""
xr_fetch() {
  curl -fsSL --connect-timeout 15 --max-time 300 --retry 3 --retry-delay 2 -o "$XR_TMP/xray.zip" "` + XrayExitDownloadURL() + `" \
    && echo "$XR_SHA256  $XR_TMP/xray.zip" | sha256sum -c --quiet \
    && unzip -o -q "$XR_TMP/xray.zip" xray geoip.dat geosite.dat -d "$XR_TMP" \
    && [ -s "$XR_TMP/geoip.dat" ] && [ -s "$XR_TMP/geosite.dat" ] \
    && chmod 755 "$XR_TMP/xray" && chmod 644 "$XR_TMP/geoip.dat" "$XR_TMP/geosite.dat" \
    && [ "$("$XR_TMP/xray" version 2>/dev/null | awk 'NR==1{print $2}')" = "$XR_WANT" ]
}
if [ "$XR_CUR" != "$XR_WANT" ] || [ ! -s "$XR_DIR/geoip.dat" ] || [ ! -s "$XR_DIR/geosite.dat" ]; then
  mkdir -p "$XR_DIR"
  rm -rf "$XR_DIR"/.dl.*
  XR_TMP=$(mktemp -d "$XR_DIR/.dl.XXXXXX")
  if xr_fetch; then
    mv -f "$XR_TMP/geoip.dat" "$XR_DIR/geoip.dat"
    mv -f "$XR_TMP/geosite.dat" "$XR_DIR/geosite.dat"
    XR_DATA=1
    if [ "$XR_CUR" != "$XR_WANT" ]; then
      if [ -f "$XR_DIR/xray" ]; then
        cp -p "$XR_DIR/xray" "$XR_DIR/xray.bak.tmp"
        mv -f "$XR_DIR/xray.bak.tmp" "$XR_DIR/xray.bak"
      fi
      mv -f "$XR_TMP/xray" "$XR_DIR/xray"
      XR_REPLACED=1
      echo "[xray] installed $XR_WANT (previous: ${XR_CUR:-none})"
    else
      echo "[xray] $XR_WANT: geoip.dat/geosite.dat restored from the release archive"
    fi
    rm -rf "$XR_TMP"
  else
    rm -rf "$XR_TMP"
    echo "[xray] ERROR: download/sha256/verify of $XR_WANT failed, binary unchanged" >&2
    false
  fi
else
  echo "[xray] $XR_WANT already installed"
fi
`
}

// ExitXrayPrepareScript is the converge extra of the exit xray (UnitConvergeScript("xray", …)),
// after the recipe wrote config.json, xray.service and the guard files (ExitXrayGuardConfPath,
// ExitXrayGuardUnitPath, ExitXrayResolvConfPath): the system user (ExitXrayUserScript), the
// kernel egress guard loaded and enabled (ExitXrayGuardApplyScript — refused when its files are
// missing, xray never starts unguarded), the pinned binary, then `xray run -test` on the config
// before any restart. A replaced binary or data files set the restart marker; a replaced binary
// also the pending-binary marker (UnitRollbackScript).
func ExitXrayPrepareScript() string { return exitXrayPrepareScript(XrayExitInstallScript()) }

func exitXrayPrepareScript(install string) string {
	return ExitXrayUserScript +
		"for xr_f in " + ExitXrayGuardConfPath + " " + ExitXrayGuardUnitPath + " " + ExitXrayResolvConfPath + "; do\n" +
		"  [ -f \"$xr_f\" ] || { echo \"[xray] ERROR: нет $xr_f — без защиты исходящих xray не запускается\" >&2; exit 1; }\ndone\n" +
		ExitXrayGuardApplyScript + install +
		`if [ "$XR_REPLACED" = 1 ] || [ "$XR_DATA" = 1 ]; then mkdir -p ` + RestartMarkerDir + `; touch ` + RestartMarker("xray") + `; fi
if [ "$XR_REPLACED" = 1 ]; then mkdir -p ` + BinaryPendingDir + `; printf '%s\n' ` + ExitXrayBinary + ` > ` + BinaryPendingMarker("xray") + `; fi
if ! XR_TEST=$(` + ExitXrayBinary + ` run -test -config ` + ExitXrayConfig + ` 2>&1); then
  printf '%s\n' "$XR_TEST" | tail -n 20 >&2
  echo "[xray] ERROR: конфигурация не прошла проверку xray run -test — служба не перезапущена" >&2
  exit 1
fi
echo "[xray] конфигурация прошла проверку (xray run -test)"
`
}

// ExitHysteriaPrepareScript is the converge extra of the exit hysteria (UnitConvergeScript(
// "hysteria2", …)): the pinned hysteria (HysteriaInstallScript; a replaced binary sets the
// restart and pending-binary markers) and the self-signed TLS pair, created only when a half is
// missing (marker first, then key, then certificate — a drop never leaves a mismatched pair).
func ExitHysteriaPrepareScript() string { return exitHysteriaPrepareScript(HysteriaInstallScript()) }

func exitHysteriaPrepareScript(install string) string {
	certDir := path.Dir(ExitHysteriaCert)
	return install + `mkdir -p ` + RestartMarkerDir + ` ` + BinaryPendingDir + `
if [ "$HY_REPLACED" = 1 ]; then
  touch ` + RestartMarker("hysteria2") + `
  printf '%s\n' /usr/local/bin/hysteria > ` + BinaryPendingMarker("hysteria2") + `
fi
mkdir -p ` + certDir + `
if [ ! -s ` + ExitHysteriaCert + ` ] || [ ! -s ` + ExitHysteriaKey + ` ]; then
  rm -rf ` + certDir + `/.hy2.*
  HY_CT=$(mktemp -d ` + certDir + `/.hy2.XXXXXX)
  if ! HY_SSL=$(openssl req -x509 -newkey ec -pkeyopt ec_paramgen_curve:prime256v1 -days 3650 -nodes \
      -keyout "$HY_CT/hy2.key" -out "$HY_CT/hy2.crt" -subj "/CN=www.microsoft.com" 2>&1); then
    printf '%s\n' "$HY_SSL" >&2
    rm -rf "$HY_CT"
    echo "[hysteria] ERROR: не удалось создать TLS-сертификат" >&2
    exit 1
  fi
  chmod 600 "$HY_CT/hy2.key"
  chmod 644 "$HY_CT/hy2.crt"
  touch ` + RestartMarker("hysteria2") + `
  mv -f "$HY_CT/hy2.key" ` + ExitHysteriaKey + `
  mv -f "$HY_CT/hy2.crt" ` + ExitHysteriaCert + `
  rm -rf "$HY_CT"
  echo "[hysteria] создан самоподписанный TLS-сертификат"
fi
`
}

// ExitPreflightScript changes nothing; one "PF <check> ok|fail <detail>" line per check (os,
// arch, root, disk, internet — www.gstatic.com and github.com, where Xray and Hysteria come from
// — and the ports 50443/udp, 10443/tcp, 50052/tcp: free or held by our own service), then
// "PF_TIME <unix>" for the clock check on the Hub.
func ExitPreflightScript() string {
	return opLine("preflight") + `. /etc/os-release 2>/dev/null || true
case "${ID:-}" in ubuntu|debian) echo "PF os ok ${PRETTY_NAME:-$ID}" ;; *) echo "PF os fail нужна Ubuntu или Debian, а здесь ${PRETTY_NAME:-неизвестная ОС}" ;; esac
a=$(uname -m); if [ "$a" = x86_64 ]; then echo "PF arch ok x86_64"; else echo "PF arch fail нужна x86_64, а здесь $a"; fi
if [ "$(id -u)" = 0 ]; then echo "PF root ok"; else echo "PF root fail нужен вход под root"; fi
avail=$(df -Pk / | awk 'NR==2{print $4}')
if [ "${avail:-0}" -ge 1048576 ]; then echo "PF disk ok $((avail/1024)) МБ свободно"; else echo "PF disk fail свободно меньше 1 ГБ"; fi
pf_tcp() { timeout 10 bash -c "exec 3<>/dev/tcp/$1/443" 2>/dev/null; }
if ! pf_tcp www.gstatic.com; then echo "PF internet fail нет выхода в интернет (www.gstatic.com:443)"
elif ! pf_tcp github.com; then echo "PF internet fail нет доступа к github.com:443 (оттуда ставятся Xray и Hysteria)"
else echo "PF internet ok"; fi
for spec in udp:` + strconv.Itoa(ExitHysteriaPort) + `:hysteria tcp:` + strconv.Itoa(ExitRealityPort) + `:xray tcp:` + strconv.Itoa(NodeAgentPort) + `:node-agent; do
  proto=${spec%%:*}; rest=${spec#*:}; port=${rest%%:*}; own=${rest#*:}
  if [ "$proto" = udp ]; then line=$(ss -Hulpn "sport = :$port" 2>/dev/null); else line=$(ss -Htlpn "sport = :$port" 2>/dev/null); fi
  if [ -z "$line" ] || printf '%s' "$line" | grep -q "\"$own\""; then echo "PF port_$port ok"
  else echo "PF port_$port fail порт $port занят: $(printf '%s' "$line" | grep -o 'users:(("[^"]*"' | head -1)"; fi
done
echo "PF_TIME $(date +%s)"
`
}

// exitPackages are what the exit recipes need from apt (ExitBaseScript, ExitBaseCheckScript).
const exitPackages = "curl unzip nftables openssl ca-certificates"

// havePkgFunc: have_pkg P — installed (not merely known: "deinstall ok config-files" is missing).
const havePkgFunc = `have_pkg() { [ "$(dpkg-query -W -f='${Status}' "$1" 2>/dev/null)" = "install ok installed" ]; }
`

// ExitBaseScript applies ExitSysctlConf (written by the recipe; unknown keys ignored, BBR checked
// by ExitBaseCheckScript) and installs the missing exitPackages, waiting for the dpkg lock that
// unattended-upgrades holds on a fresh VPS. A failed apt-get update stops the step.
const ExitBaseScript = `# hp:base
set -e
` + havePkgFunc + `modprobe tcp_bbr 2>/dev/null || true
sysctl -e -p ` + ExitSysctlPath + ` >/dev/null
missing=""
for p in ` + exitPackages + `; do have_pkg "$p" || missing="$missing $p"; done
if [ -n "$missing" ]; then
  export DEBIAN_FRONTEND=noninteractive
  echo "[base] ставлю пакеты:$missing"
  apt-get -o DPkg::Lock::Timeout=300 update -qq
  apt-get -o DPkg::Lock::Timeout=300 install -y -qq --no-install-recommends $missing
fi
echo "[base] BBR: $(sysctl -n net.ipv4.tcp_congestion_control), пакеты на месте"
`

// ExitBaseCheckScript prints BBR=0|1 and PKGS=0|1.
const ExitBaseCheckScript = `# hp:base-check
` + havePkgFunc + `if [ "$(sysctl -n net.ipv4.tcp_congestion_control 2>/dev/null)" = bbr ]; then echo BBR=1; else echo BBR=0; fi
ok=1; for p in ` + exitPackages + `; do have_pkg "$p" || ok=0; done; echo PKGS=$ok
`

// hopRuleGrep matches the applied hop rule as nft lists it (only with the local-destination
// guard: an older unguarded table does not count).
const hopRuleGrep = `grep -q 'fib daddr type local .*redirect to :` + "50443" + `'`

// ExitPortHopApplyScript loads ExitPortHopConf and enables its boot unit (both written by the
// recipe), then checks the kernel has the guarded rule. ExitPortHopScript is the CLI variant
// that also writes the files.
const ExitPortHopApplyScript = `# hp:hop-apply
set -e
nft -f ` + ExitPortHopConfPath + `
systemctl daemon-reload
systemctl enable --quiet nftables-hysteria
if ! nft list table inet hysteria_porthopping 2>/dev/null | ` + hopRuleGrep + `; then
  echo "[hop] ERROR: правило прыжков портов не появилось в nftables" >&2
  exit 1
fi
echo "[hop] UDP ` + ExitHopRange + ` -> :50443 (только для адресов самого exit)"
`

// HysteriaCheckScript prints VERSION=<hysteria version>, LISTEN=0|1 (hysteria on UDP 50443),
// HOP=0|1 (guarded hop rule loaded).
const HysteriaCheckScript = `# hp:hy-check
echo "VERSION=$(/usr/local/bin/hysteria version 2>/dev/null | awk '$1=="Version:"{print $2}')"
if ss -Hulpn 'sport = :50443' 2>/dev/null | grep -q '"hysteria"'; then echo LISTEN=1; else echo LISTEN=0; fi
if nft list table inet hysteria_porthopping 2>/dev/null | ` + hopRuleGrep + `; then echo HOP=1; else echo HOP=0; fi
`

// XrayCheckScript prints VERSION=<xray version>, LISTEN=0|1 (xray on TCP 10443), GUARD=0|1 (the
// kernel egress guard table of the xray user is loaded).
const XrayCheckScript = `# hp:xray-check
echo "VERSION=$(` + ExitXrayBinary + ` version 2>/dev/null | awk 'NR==1{print $2}')"
if ss -Htlpn 'sport = :10443' 2>/dev/null | grep -q '"xray"'; then echo LISTEN=1; else echo LISTEN=0; fi
if nft list table inet ` + ExitXrayGuardTable + ` 2>/dev/null | grep -q 'jump xray_egress'; then echo GUARD=1; else echo GUARD=0; fi
`

// HubSSHKeyEnsureScript (run on the Hub): the Hub's own ed25519 key, created once (an empty
// leftover is replaced; a lost .pub is derived from the key, never a new key); prints the public
// line "ssh-ed25519 AAAA… heyearth-hub".
const HubSSHKeyEnsureScript = `# hp:hub-key
set -e
K=` + HubSSHKeyPath + `
umask 077
mkdir -p "$(dirname "$K")"
chmod 700 "$(dirname "$K")"
if [ ! -s "$K" ]; then
  rm -f "$K" "$K.pub"
  ssh-keygen -q -t ed25519 -N '' -C heyearth-hub -f "$K"
fi
chmod 600 "$K"
if [ ! -s "$K.pub" ]; then
  P=$(ssh-keygen -y -f "$K")
  printf '%s\n' "$P" | awk 'NR==1{print $1" "$2" heyearth-hub"}' > "$K.pub.tmp"
  mv -f "$K.pub.tmp" "$K.pub"
fi
cat "$K.pub"
`

// AuthorizedKeyScript appends pub (the Hub's "ssh-ed25519 AAAA… heyearth-hub" line) to root's
// authorized_keys once, on a line of its own. Anything else — another key type, options, a line
// break — is refused before the file is touched.
func AuthorizedKeyScript(pub string) string {
	return opLine("authkey") + "set -e\nKEY=" + shQuote(pub) + "\n" +
		`case "$KEY" in *$'\n'*|*$'\r'*) echo "[ssh] ERROR: ключ Hub не одной строкой" >&2; exit 1 ;; "ssh-ed25519 "*) ;; *) echo "[ssh] ERROR: ожидался ключ ssh-ed25519" >&2; exit 1 ;; esac
umask 077
mkdir -p "$HOME/.ssh"
AK="$HOME/.ssh/authorized_keys"
touch "$AK"
if grep -qxF -- "$KEY" "$AK"; then
  echo "[ssh] ключ Hub уже есть"
else
  if [ -s "$AK" ] && [ -n "$(tail -c 1 "$AK")" ]; then echo >> "$AK"; fi
  printf '%s\n' "$KEY" >> "$AK"
  echo "[ssh] ключ Hub добавлен"
fi
chmod 700 "$HOME/.ssh"
chmod 600 "$AK"
`
}

// AuthorizedKeyCheckScript prints PRESENT=0|1.
func AuthorizedKeyCheckScript(pub string) string {
	return opLine("authkey-check") + "KEY=" + shQuote(pub) + "\n" +
		"if grep -qxF -- \"$KEY\" \"$HOME/.ssh/authorized_keys\" 2>/dev/null; then echo PRESENT=1; else echo PRESENT=0; fi\n"
}

// probeCurl is one generate_204 through the loopback SOCKS $1; prints the HTTP code (000 = none).
const probeCurl = `probe_once() { curl -s -o /dev/null -w '%{http_code}' --max-time 15 --socks5-hostname "127.0.0.1:$1" https://www.gstatic.com/generate_204 || true; }
`

// SocksProbeScript (Hub): generate_204 through the local SOCKS port, up to 3 tries; HTTP=<code>.
func SocksProbeScript(port int) string {
	p := strconv.Itoa(port)
	return opLine("socks-probe", p) + probeCurl + "code=000\nfor i in 1 2 3; do\n  code=$(probe_once " + p + ")\n" +
		"  [ \"$code\" = 204 ] && break\n  [ \"$i\" = 3 ] || sleep 3\ndone\necho \"HTTP=$code\"\n"
}

// VRProbeScript (Hub): a short-lived xray (xrayBin) on the ExitRealityProbeConfig at cfgPath,
// SOCKS on port; up to 3 generate_204; HTTP=<code>. A stale probe of a dropped run is killed
// first; on exit the probe xray is stopped and the config (it carries the tunnel UUID) and its
// .bak are removed. On failure xray's own log goes to stderr.
func VRProbeScript(xrayBin, cfgPath string, port int) string {
	p := strconv.Itoa(port)
	return opLine("vr-probe", p) + probeCurl + "VR_BIN=" + shQuote(xrayBin) + "\nVR_CFG=" + shQuote(cfgPath) + "\n" +
		`VR_LOG=$(mktemp)
pkill -f -- "run -config $VR_CFG" 2>/dev/null || true
"$VR_BIN" run -config "$VR_CFG" > "$VR_LOG" 2>&1 &
vr_pid=$!
trap 'kill "$vr_pid" 2>/dev/null; wait "$vr_pid" 2>/dev/null; rm -f "$VR_CFG" "$VR_CFG.bak" "$VR_LOG"' EXIT
trap 'exit 1' HUP INT TERM
for i in $(seq 1 25); do
  ss -Htln 'sport = :` + p + `' 2>/dev/null | grep -q . && break
  kill -0 "$vr_pid" 2>/dev/null || break
  sleep 0.2
done
code=000
for i in 1 2 3; do
  code=$(probe_once ` + p + `)
  [ "$code" = 204 ] && break
  [ "$i" = 3 ] || sleep 2
done
if [ "$code" != 204 ]; then tail -n 15 "$VR_LOG" | sed 's/^/[vr-probe] xray: /' >&2; fi
echo "HTTP=$code"
`
}
