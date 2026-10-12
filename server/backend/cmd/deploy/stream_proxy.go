package main

// deploy --stream-proxy-protocol on|off — real client IPs behind the 443 SNI router.
//
// nginx's stream server on :443 (ssl_preread) hands every connection to the http nginx on
// 127.0.0.1:8443 (our domains) or to xray Reality on 127.0.0.1:10443 (everything else). Without
// PROXY protocol both only ever see 127.0.0.1, so the API saw every HTTPS client as 127.0.0.1:
// YooKassa webhooks were refused (403), per-IP rate limits were global, audit IPs were useless.
//
// Mode "on":
//   - stream: `proxy_protocol on;` in the SNI-router server block; our domains go to the PROXY
//     listener 127.0.0.1:8446 instead of 8443;
//   - http nginx: every HTTPS server block also listens on 127.0.0.1:8446 with proxy_protocol and
//     takes the client address from it (set_real_ip_from 127.0.0.1; real_ip_header
//     proxy_protocol). $remote_addr is then the real client, so the existing
//     `X-Real-IP $remote_addr` towards the API carries it (the API trusts X-Real-IP from nginx,
//     internal/middleware ExtractIP). The plain 8443 listener stays as it is: local direct
//     checks keep working without a PROXY header (cmd/api monitor reads the site certificate on
//     127.0.0.1:8443). The PROXY listener is always present (generator and patch) and simply
//     unused while the mode is off;
//   - xray: the Reality inbound gets streamSettings.sockopt.acceptProxyProtocol (cmd/api
//     xray_sync.go, app_settings.stream_proxy_protocol). xray then REQUIRES the header, so nginx
//     and xray must switch together. vless-xhttp (:2443) faces clients directly and the CDN
//     inbound sits behind nginx http — neither is behind the stream.
//
// The switch runs detached on the Hub (systemd-run) so an SSH drop cannot leave it half done:
//
//	on:  setting true -> restart heyearth-api (its start-up sync rewrites config.json and
//	     restarts xray) -> wait until config.json has acceptProxyProtocol and a NEW xray process
//	     listens -> patch nginx -> nginx -t -> reload -> verify
//	off: patch nginx -> nginx -t -> reload -> setting false -> restart heyearth-api -> wait until
//	     acceptProxyProtocol is gone and a new xray listens -> verify
//
// Any failure rolls both sides back to the previous mode, in that mode's own safe order. Verify:
// https://<domain>/api/v1/health through 127.0.0.1:443 answers 200; a TLS handshake with the
// Reality SNI through 127.0.0.1:443 succeeds (xray hands an unauthenticated client to its dest);
// with "on", a request to the Hub's own public address shows up in nginx's access log with that
// address, not 127.0.0.1. Reality is unreachable for new connections for a few seconds between
// the xray restart and the nginx reload; the xray restart drops existing Reality sessions.

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
)

const (
	// nginxProxyProtocolPort: the http nginx listener (127.0.0.1 only) that expects a PROXY
	// header — where the 443 stream sends our domains while the mode is on.
	nginxProxyProtocolPort = "8446"
	// nginxProxyProtocolListen goes into every HTTPS server block next to the 8443 listeners.
	nginxProxyProtocolListen = "    # PROXY-protocol listener (127.0.0.1 only): target of the 443 stream while deploy --stream-proxy-protocol is on\n" +
		"    listen 127.0.0.1:" + nginxProxyProtocolPort + " ssl http2 proxy_protocol;\n" +
		"    set_real_ip_from 127.0.0.1;\n" +
		"    real_ip_header proxy_protocol;\n"
	// streamProxyProtocolLine is the directive in the SNI-router server block while the mode is on.
	streamProxyProtocolLine = "proxy_protocol on;  # deploy --stream-proxy-protocol: real client IP to 127.0.0.1:" + nginxProxyProtocolPort + " and xray 10443"
	// streamProxyLiveCheck prints "on" when xray's live config accepts PROXY on the Reality
	// inbound (the only inbound that can, see cmd/api xray_sync.go), else "off".
	streamProxyLiveCheck = `if grep -Eq '"acceptProxyProtocol"[[:space:]]*:[[:space:]]*true' /opt/heyearth/xray/config.json 2>/dev/null; then echo on; else echo off; fi`

	streamProxyUnit       = "heyearth-stream-proxy"
	streamProxyScriptPath = "/opt/heyearth/ops/stream-proxy-switch.sh"
	streamProxyLog        = "/var/log/heyearth-stream-proxy.log"
)

// streamProxyAwk rewrites one nginx config file for mode (awk -v mode=on|off -v port=8446,
// the file given twice: pass 1 collects, pass 2 prints). In files holding the SNI router
// (`proxy_pass $backend;`): the stream upstream `server 127.0.0.1:8443` <-> 127.0.0.1:<port>,
// and `proxy_protocol on;` added (on, before the router's proxy_timeout comment / proxy_timeout /
// proxy_pass, i.e. where the generator and the --setup-ops patch put it) or removed (off). In
// http server blocks with an IPv4/wildcard `listen ... 8443 ... ssl` (on only): the PROXY
// listener and the realip directives are added after that listen line unless present. A file
// already in the wanted state comes out unchanged.
const streamProxyAwk = `FNR == NR {
  code = $0; sub(/#.*/, "", code)
  if (code ~ /^[[:space:]]*proxy_pass[[:space:]]+\$backend;/) isRouter = 1
  if (!inb && code ~ /^[[:space:]]*server[[:space:]]*\{/) { inb = 1; bid++; depth = 0 }
  if (inb) {
    blk[FNR] = bid
    n = gsub(/\{/, "{", code); m = gsub(/\}/, "}", code); depth += n - m
    if ($0 ~ /^[[:space:]]*proxy_pass[[:space:]]+\$backend;/) router[bid] = 1
    if ($0 ~ /^[[:space:]]*proxy_protocol[[:space:]]+on[[:space:]]*;/) hasPP[bid] = 1
    if ($0 ~ /^[[:space:]]*listen[[:space:]]+(0\.0\.0\.0:|\*:|127\.0\.0\.1:)?8443[[:space:];]/ && $0 ~ /[[:space:]]ssl[[:space:];]/) https[bid] = 1
    if ($0 ~ ("^[[:space:]]*listen[[:space:]]+127\\.0\\.0\\.1:" port "[[:space:];]")) hasL[bid] = 1
    if ($0 ~ /^[[:space:]]*set_real_ip_from[[:space:]]+127\.0\.0\.1[[:space:]]*;/) hasSRF[bid] = 1
    if ($0 ~ /^[[:space:]]*real_ip_header[[:space:]]+proxy_protocol[[:space:]]*;/) hasRIH[bid] = 1
    if (depth <= 0) inb = 0
  }
  next
}
{
  b = blk[FNR]; line = $0
  if (isRouter && line ~ /^[[:space:]]*server[[:space:]]+127\.0\.0\.1:[0-9]+[[:space:];]/) {
    if (mode == "on") sub(/127\.0\.0\.1:8443/, "127.0.0.1:" port, line)
    else sub("127\\.0\\.0\\.1:" port, "127.0.0.1:8443", line)
  }
  if (b != "" && router[b]) {
    if (line ~ /^[[:space:]]*proxy_protocol[[:space:]]+on[[:space:]]*;/ && mode == "off") next
    if (mode == "on" && !hasPP[b] && !ppDone[b] && (index(line, "__TIMEOUT_COMMENT__") > 0 || line ~ /^[[:space:]]*proxy_timeout[[:space:]]/ || line ~ /^[[:space:]]*proxy_pass[[:space:]]+\$backend;/)) {
      match(line, /^[[:space:]]*/); print substr(line, 1, RLENGTH) "__PP_LINE__"; ppDone[b] = 1
    }
  }
  print line
  if (mode == "on" && b != "" && https[b] && !insDone[b] && line ~ /^[[:space:]]*listen[[:space:]]+(0\.0\.0\.0:|\*:|127\.0\.0\.1:)?8443[[:space:];]/ && line ~ /[[:space:]]ssl[[:space:];]/) {
    match(line, /^[[:space:]]*/); ind = substr(line, 1, RLENGTH)
    h2 = ""; if (line ~ /[[:space:]]http2[[:space:];]/) h2 = " http2"
    if (!hasL[b]) print ind "listen 127.0.0.1:" port " ssl" h2 " proxy_protocol;"
    if (!hasSRF[b]) print ind "set_real_ip_from 127.0.0.1;"
    if (!hasRIH[b]) print ind "real_ip_header proxy_protocol;"
    insDone[b] = 1
  }
}`

func renderStreamProxyAwk() string {
	return strings.NewReplacer("__TIMEOUT_COMMENT__", nginxStreamTimeoutComment, "__PP_LINE__", streamProxyProtocolLine).Replace(streamProxyAwk)
}

// streamProxySwitchScript runs on the Hub as root, detached (systemd-run), output appended to
// the log. The last line is "RESULT: <on|off|rolled-back-to-off|rolled-back-to-on|FAILED>".
// SPP_* variables are test hooks, unset in production.
const streamProxySwitchScript = `#!/usr/bin/env bash
# Rendered by deploy --stream-proxy-protocol (cmd/deploy/stream_proxy.go). Runs detached.
set -u
MODE=__MODE__
DOMAIN=__DOMAIN__
PP_PORT=__PP_PORT__
LOG="${SPP_LOG:-__LOG__}"
XRAY_CONF="${SPP_XRAY_CONF:-/opt/heyearth/xray/config.json}"
BK="${SPP_BACKUP_DIR:-/var/backups/heyearth-nginx}"
WAIT_XRAY="${SPP_WAIT_XRAY:-120}"
ACCESS_LOG="${SPP_ACCESS_LOG:-/var/log/nginx/access.log}"
exec >>"$LOG" 2>&1
log() { echo "$(date -Is) [stream-proxy] $*"; }
SPP_AWK='__AWK__'
STAMP=$(date +%Y%m%d-%H%M%S)
CHANGED=""

xray_pp() { grep -Eq '"acceptProxyProtocol"[[:space:]]*:[[:space:]]*true' "$XRAY_CONF" 2>/dev/null; }
xray_pid() { systemctl show -p MainPID --value xray 2>/dev/null || echo 0; }
xray_ready() { systemctl is-active --quiet xray && ss -ltn 2>/dev/null | grep -q '127\.0\.0\.1:10443 '; }
reality_sni() {
  s=$(awk '/"serverNames"/ { getline; gsub(/[[:space:]",]/, ""); print; exit }' "$XRAY_CONF" 2>/dev/null)
  echo "${s:-__DEFAULT_SNI__}"
}
set_setting() {
  if runuser -u postgres -- psql -d heyearth -v ON_ERROR_STOP=1 -qc "INSERT INTO app_settings (key, value, updated_at) VALUES ('stream_proxy_protocol', '$1', CURRENT_TIMESTAMP) ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = CURRENT_TIMESTAMP"; then
    log "app_settings.stream_proxy_protocol = $1"
  else
    log "ERROR: cannot write app_settings.stream_proxy_protocol = $1"; return 1
  fi
}
# sync_xray WANT(1|0): restart heyearth-api (its start-up sync rewrites config.json and restarts
# xray on this structural change) and wait until config.json matches WANT and a NEW xray process
# listens on 127.0.0.1:10443. Nothing to do when xray already matches.
sync_xray() {
  want=$1
  have=0; if xray_pp; then have=1; fi
  if [ "$have" = "$want" ] && xray_ready; then log "xray already has acceptProxyProtocol=$want"; return 0; fi
  old=$(xray_pid)
  systemctl restart heyearth-api || { log "ERROR: systemctl restart heyearth-api failed"; return 1; }
  log "heyearth-api restarted, waiting up to ${WAIT_XRAY}s for xray (acceptProxyProtocol=$want, xray pid was $old)"
  i=0
  while [ "$i" -lt "$WAIT_XRAY" ]; do
    have=0; if xray_pp; then have=1; fi
    pid=$(xray_pid)
    if [ "$have" = "$want" ] && [ -n "$pid" ] && [ "$pid" != 0 ] && [ "$pid" != "$old" ] && xray_ready; then
      log "xray restarted with acceptProxyProtocol=$want (pid $pid, ${i}s)"; return 0
    fi
    sleep 1; i=$((i + 1))
  done
  log "ERROR: xray did not come up with acceptProxyProtocol=$want within ${WAIT_XRAY}s — see journalctl -u heyearth-api | grep xray-sync"
  return 1
}
nginx_files() {
  nginx -T 2>/dev/null | awk '/^# configuration file /{f=$4; sub(/:$/,"",f); next}
    /^[[:space:]]*proxy_pass[[:space:]]+\$backend;/ || /^[[:space:]]*listen[[:space:]]+[^;]*8443[[:space:];]/ {print f}' | sort -u
}
nginx_restore() {
  [ -n "$CHANGED" ] || return 0
  for f in $CHANGED; do cat "$BK/$(printf '%s' "$f" | tr '/' '_').$STAMP.spp" > "$f"; done
  CHANGED=""
  if nginx -t && systemctl reload nginx; then log "nginx config of this run restored and reloaded"; else log "ERROR: nginx restore: nginx -t / reload failed"; return 1; fi
}
# nginx_apply MODE: patch the files nginx reads, nginx -t, reload. A failing nginx -t / reload
# restores this run's originals.
nginx_apply() {
  files=$(nginx_files)
  [ -n "$files" ] || { log "ERROR: nginx -T shows no SNI router / 8443 listener"; return 1; }
  mkdir -p "$BK"; chmod 700 "$BK"
  for f in $files; do
    tmp=$(mktemp "$BK/spp.XXXXXX") || return 1
    if ! awk -v mode="$1" -v port="$PP_PORT" "$SPP_AWK" "$f" "$f" > "$tmp" || [ ! -s "$tmp" ]; then rm -f "$tmp"; log "ERROR: patch of $f failed"; nginx_restore; return 1; fi
    if cmp -s "$tmp" "$f"; then rm -f "$tmp"; continue; fi
    b="$BK/$(printf '%s' "$f" | tr '/' '_').$STAMP.spp"
    [ -f "$b" ] || cp -p "$f" "$b"
    case " $CHANGED " in *" $f "*) ;; *) CHANGED="$CHANGED $f" ;; esac
    cat "$tmp" > "$f"; rm -f "$tmp"
    log "nginx: $f patched for PROXY $1 (original: $b)"
  done
  if ! nginx -t; then log "ERROR: nginx -t rejected the PROXY $1 config"; nginx_restore; return 1; fi
  if ! systemctl reload nginx; then log "ERROR: systemctl reload nginx failed"; nginx_restore; return 1; fi
  sleep 1
  log "nginx reloaded with PROXY $1"
}
# verify WANT(1|0): the site through the 443 stream, Reality through the 443 stream, and (on)
# the real client address in nginx's access log.
verify() {
  code=$(curl -sk -o /dev/null -w '%{http_code}' --max-time 10 --resolve "$DOMAIN:443:127.0.0.1" "https://$DOMAIN/api/v1/health" || true)
  [ "$code" = 200 ] || { log "ERROR: https://$DOMAIN/api/v1/health via 127.0.0.1:443 -> HTTP ${code:-none}"; return 1; }
  sni=$(reality_sni)
  if ! echo | timeout 15 openssl s_client -connect 127.0.0.1:443 -servername "$sni" >/dev/null 2>&1; then
    log "ERROR: TLS handshake with the Reality SNI $sni via 127.0.0.1:443 failed"; return 1
  fi
  log "verify: site 200 and Reality handshake ($sni) through :443"
  [ "$1" = 1 ] || return 0
  pub=$(ip -4 -o addr show scope global 2>/dev/null | awk '{ sub(/\/.*/, "", $4); print $4; exit }')
  [ -n "$pub" ] || { log "ERROR: no global IPv4 address to probe the real client IP with"; return 1; }
  ua="heyearth-spp-$(date +%s)-$$"
  code=$(curl -sk -o /dev/null -w '%{http_code}' --max-time 10 -A "$ua" --resolve "$DOMAIN:443:$pub" "https://$DOMAIN/api/v1/health" || true)
  [ "$code" = 200 ] || { log "ERROR: https://$DOMAIN/api/v1/health via $pub:443 -> HTTP ${code:-none}"; return 1; }
  sleep 1
  seen=$(grep -F "$ua" "$ACCESS_LOG" 2>/dev/null | tail -n 1 | awk '{ print $1 }')
  if [ -z "$seen" ] || [ "$seen" = 127.0.0.1 ]; then log "ERROR: nginx logged the probe from ${seen:-nothing}, expected $pub — the real client IP does not arrive"; return 1; fi
  log "verify: nginx sees the real client address ($seen)"
}
rollback_to_off() {
  log "ROLLBACK to off: nginx first, then xray"
  ok=0
  nginx_restore || ok=1
  set_setting false || ok=1
  sync_xray 0 || ok=1
  return "$ok"
}
rollback_to_on() {
  log "ROLLBACK to on: xray first, then nginx"
  ok=0
  set_setting true || ok=1
  sync_xray 1 || ok=1
  nginx_restore || ok=1
  return "$ok"
}
finish() { log "RESULT: $1"; exit "$2"; }

log "start: --stream-proxy-protocol $MODE (domain $DOMAIN, xray acceptProxyProtocol now: $(xray_pp && echo true || echo false))"
for c in nginx curl openssl runuser awk systemctl ss; do
  command -v "$c" >/dev/null 2>&1 || { log "ERROR: $c not installed — nothing changed"; finish FAILED 1; }
done
nginx -T >/dev/null 2>&1 || { log "ERROR: nginx -T fails (current config invalid?) — nothing changed"; finish FAILED 1; }
[ -n "$(nginx_files)" ] || { log "ERROR: no SNI router (proxy_pass \$backend) / 8443 listener in the nginx config — nothing changed"; finish FAILED 1; }
case "$MODE" in
  on)
    set_setting true || { set_setting false; finish FAILED 1; }
    sync_xray 1 || { rollback_to_off && finish rolled-back-to-off 1; finish FAILED 2; }
    nginx_apply on || { rollback_to_off && finish rolled-back-to-off 1; finish FAILED 2; }
    verify 1 || { rollback_to_off && finish rolled-back-to-off 1; finish FAILED 2; }
    finish on 0 ;;
  off)
    nginx_apply off || finish rolled-back-to-on 1
    set_setting false || { rollback_to_on && finish rolled-back-to-on 1; finish FAILED 2; }
    sync_xray 0 || { rollback_to_on && finish rolled-back-to-on 1; finish FAILED 2; }
    verify 0 || { rollback_to_on && finish rolled-back-to-on 1; finish FAILED 2; }
    finish off 0 ;;
  *) log "usage: MODE on|off"; finish FAILED 1 ;;
esac
`

var streamProxyDomainRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9.-]*[a-z0-9])?$`)

// renderStreamProxySwitchScript renders the switch for mode ("on"/"off") and the site domain.
func renderStreamProxySwitchScript(mode, domain, defaultSNI string) (string, error) {
	if mode != "on" && mode != "off" {
		return "", fmt.Errorf("--stream-proxy-protocol %q: ожидается on или off", mode)
	}
	if !streamProxyDomainRe.MatchString(domain) || !streamProxyDomainRe.MatchString(defaultSNI) {
		return "", fmt.Errorf("недопустимый домен %q / SNI %q", domain, defaultSNI)
	}
	return strings.NewReplacer("__MODE__", mode, "__DOMAIN__", domain, "__PP_PORT__", nginxProxyProtocolPort,
		"__LOG__", streamProxyLog, "__AWK__", renderStreamProxyAwk(), "__DEFAULT_SNI__", defaultSNI).Replace(streamProxySwitchScript), nil
}

// streamProxyStartScript installs the rendered switch and starts it as a transient unit
// (survives the SSH session); prints the log's line count before the start ("START <n>").
func streamProxyStartScript(payloadB64 string) string {
	return `set -eu
if systemctl is-active --quiet ` + streamProxyUnit + `; then echo "[stream-proxy] ERROR: a switch is already running (systemctl status ` + streamProxyUnit + `)"; exit 1; fi
mkdir -p "$(dirname ` + streamProxyScriptPath + `)"
umask 077
printf '%s' '` + payloadB64 + `' | base64 -d > ` + streamProxyScriptPath + `.tmp
chmod 700 ` + streamProxyScriptPath + `.tmp && mv -f ` + streamProxyScriptPath + `.tmp ` + streamProxyScriptPath + `
touch ` + streamProxyLog + ` && chmod 600 ` + streamProxyLog + `
echo "START $(wc -l < ` + streamProxyLog + `)"
systemd-run --quiet --collect --unit=` + streamProxyUnit + ` /bin/bash ` + streamProxyScriptPath + `
`
}

var streamProxyResultRe = regexp.MustCompile(`\[stream-proxy\] RESULT: (\S+)`)

// streamProxyResult returns the RESULT of a finished switch found in log lines ("" if none yet).
func streamProxyResult(lines string) string {
	m := streamProxyResultRe.FindAllStringSubmatch(lines, -1)
	if len(m) == 0 {
		return ""
	}
	return m[len(m)-1][1]
}

// switchStreamProxyProtocol — deploy --stream-proxy-protocol on|off.
func switchStreamProxyProtocol(client *ssh.Client, cfg *deployConfig, mode, defaultSNI string) {
	script, err := renderStreamProxySwitchScript(mode, cfg.siteDomain, defaultSNI)
	if err != nil {
		log.Fatalf("[stream-proxy] %v", err)
	}
	fmt.Printf("[stream-proxy] Переключаю PROXY protocol на 443 stream: %s\n", mode)
	fmt.Println("[stream-proxy] Ожидаемый простой: Reality (Берёза/Дуб) — новые подключения не проходят ~2–5 с между перезапуском xray и reload nginx,")
	fmt.Println("[stream-proxy] активные Reality-сессии рвутся при перезапуске xray; heyearth-api перезапускается (~2–5 с). Сайт/админка/XHTTP/Бамбук/AWG — без простоя.")
	fmt.Println("[stream-proxy] Переключение идёт на Hub отдельным юнитом (" + streamProxyUnit + "), обрыв SSH его не прерывает; лог: " + streamProxyLog)

	out, err := runSSHBash(client, "stream proxy: start", streamProxyStartScript(base64.StdEncoding.EncodeToString([]byte(script))), true)
	if err != nil {
		log.Fatalf("[stream-proxy] не удалось запустить переключение: %v", err)
	}
	start := 0
	for _, l := range strings.Split(out, "\n") {
		if n, ok := strings.CutPrefix(strings.TrimSpace(l), "START "); ok {
			start, _ = strconv.Atoi(n)
		}
	}

	result := pollStreamProxyLog(client, cfg, start)
	switch result {
	case "on", "off":
		fmt.Printf("[stream-proxy] Готово: PROXY protocol %s\n", result)
	case "":
		log.Fatalf("[stream-proxy] нет результата за отведённое время. Переключение могло продолжаться на Hub: go run ./cmd/deploy --ssh 'tail -n 40 %s; systemctl status %s'", streamProxyLog, streamProxyUnit)
	default:
		log.Fatalf("[stream-proxy] ОШИБКА: %s (подробности выше и в %s на Hub)", result, streamProxyLog)
	}
	if mode == "on" {
		checkRealClientIPFromOutside(client, cfg)
	}
}

// pollStreamProxyLog prints the switch's new log lines until its RESULT line, re-dialling the
// Hub when the SSH connection drops (the switch itself keeps running there).
func pollStreamProxyLog(client *ssh.Client, cfg *deployConfig, start int) string {
	seen := start
	deadline := time.Now().Add(6 * time.Minute)
	for time.Now().Before(deadline) {
		time.Sleep(2 * time.Second)
		out, err := runSSHQuiet(client, fmt.Sprintf("tail -n +%d %s", seen+1, streamProxyLog), "")
		if err != nil {
			fmt.Printf("[stream-proxy] SSH: %v — переподключаюсь (переключение на Hub продолжается)\n", err)
			if c, derr := sshDial(fmt.Sprintf("%s:%d", cfg.host, cfg.deployPort), cfg.deployUser, cfg.deployPassword, cfg.hostKeyFingerprint); derr == nil {
				client = c
			}
			continue
		}
		if out == "" {
			continue
		}
		fmt.Print(out)
		seen += strings.Count(out, "\n")
		if r := streamProxyResult(out); r != "" {
			return r
		}
	}
	return ""
}

// checkRealClientIPFromOutside requests the health endpoint from this machine (outside the Hub)
// with a unique User-Agent and looks the request up in nginx's access log on the Hub: its client
// address must not be 127.0.0.1.
func checkRealClientIPFromOutside(client *ssh.Client, cfg *deployConfig) {
	nonce := make([]byte, 6)
	_, _ = rand.Read(nonce)
	ua := "heyearth-spp-ext-" + hex.EncodeToString(nonce)
	req, err := http.NewRequest(http.MethodGet, "https://"+cfg.siteDomain+"/api/v1/health", nil)
	if err != nil {
		fmt.Printf("[stream-proxy] WARN: внешняя проверка: %v\n", err)
		return
	}
	req.Header.Set("User-Agent", ua)
	resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		fmt.Printf("[stream-proxy] WARN: внешняя проверка https://%s/api/v1/health не прошла: %v\n", cfg.siteDomain, err)
		return
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	time.Sleep(time.Second)
	out, _ := runSSHQuiet(client, "grep -F '"+ua+"' /var/log/nginx/access.log | tail -n 1 | awk '{print $1}'", "")
	ip := strings.TrimSpace(out)
	switch {
	case ip == "":
		fmt.Printf("[stream-proxy] WARN: запрос с этой машины (HTTP %d) не найден в /var/log/nginx/access.log\n", resp.StatusCode)
	case ip == "127.0.0.1":
		fmt.Printf("[stream-proxy] ОШИБКА: nginx видит запрос с этой машины как 127.0.0.1 — реальный IP не доходит. Откат: go run ./cmd/deploy --stream-proxy-protocol off\n")
	default:
		fmt.Printf("[stream-proxy] Внешняя проверка: nginx (и API через X-Real-IP) видит клиента как %s (HTTP %d)\n", ip, resp.StatusCode)
	}
}
