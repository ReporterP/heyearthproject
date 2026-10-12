package main

// deploy --stream-proxy-protocol on|off — real client IPs behind the 443 SNI router.
//
// nginx's stream server on :443 (ssl_preread) hands every connection to the http nginx (our
// domains) or to xray Reality (everything else). Without PROXY protocol both only see 127.0.0.1:
// the API saw every HTTPS client as 127.0.0.1 (YooKassa webhooks refused, global per-IP rate
// limits, useless audit IPs).
//
// Both backends always have two loopback entry points, so switching is ONLY the stream's choice
// of upstream — no app setting, no API or xray restart, no window, and sessions on the other
// path survive:
//
//	off: upstream nginx_https -> 127.0.0.1:8443, xray_vless -> 127.0.0.1:10443, no proxy_protocol
//	on:  upstream nginx_https -> 127.0.0.1:8446, xray_vless -> 127.0.0.1:10444, proxy_protocol on;
//
//   - http nginx: every HTTPS server block listens on 8443 (plain) and on 127.0.0.1:8446 with
//     proxy_protocol + set_real_ip_from 127.0.0.1 + real_ip_header proxy_protocol, so
//     $remote_addr — sent to the API as X-Real-IP — is the real client on 8446. The generator
//     always writes both; switching on also adds the 8446 listener to any HTTPS block on 8443
//     that lacks it (hand-made vhosts). Plain 8443 stays for local direct checks (the API's cert
//     monitor reads 127.0.0.1:8443);
//   - xray: vless-reality (10443) and its mirror vless-reality-pp (10444,
//     acceptProxyProtocol) always both exist (cmd/api xray_sync.go realityInbound).
//
// An old deploy that rewrites the stream without the mode lands on "off", which keeps working.
// doNginx (--main/--site/--nginx/--setup-xray) preserves the live mode and refuses to touch
// nginx when it cannot determine it; --setup-ops' stream patch keeps the directive and ports.
//
// The switch: Go reads the live files (`nginx -T` only to find them, then their exact bytes),
// determines the current mode (fail closed), checks the Reality mirror exists in xray's live
// config, computes the target files, and renders a script that runs detached on the Hub
// (systemd-run; an SSH drop does not interrupt it): files unchanged since they were read
// (sha256), API answering, the CURRENT mode working through :443 (site health + a TLS handshake
// with the Reality SNI) -> write -> nginx -t -> reload -> API answering -> the target mode
// working through :443 (+ with "on" the real client address in nginx's access log) -> done;
// any failure restores the previous files and reloads. No awk: the config transformation is
// Go (unit-tested), the script only moves files and checks.

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
)

const (
	// nginxProxyProtocolPort: the http nginx listener (127.0.0.1 only) that expects a PROXY header.
	nginxProxyProtocolPort = "8446"
	// The xray Reality inbound and its PROXY-protocol mirror (cmd/api xray_sync.go).
	xrayRealityPort   = "10443"
	xrayRealityPPPort = "10444"
	xrayRealityTag    = "vless-reality"
	xrayRealityPPTag  = "vless-reality-pp"
	// nginxProxyProtocolListen goes into every HTTPS server block next to the 8443 listeners.
	nginxProxyProtocolListen = "    # PROXY-protocol listener (127.0.0.1 only): target of the 443 stream while deploy --stream-proxy-protocol is on\n" +
		"    listen 127.0.0.1:" + nginxProxyProtocolPort + " ssl http2 proxy_protocol;\n" +
		"    set_real_ip_from 127.0.0.1;\n" +
		"    real_ip_header proxy_protocol;\n"
	// streamProxyProtocolLine is the directive in the SNI-router server block while the mode is on.
	streamProxyProtocolLine = "proxy_protocol on;  # deploy --stream-proxy-protocol: real client IP to 127.0.0.1:" + nginxProxyProtocolPort + " / xray " + xrayRealityPPPort

	streamRouterFile      = "/etc/nginx/stream.d/heyearth.conf"
	streamProxyUnit       = "heyearth-stream-proxy"
	streamProxyScriptPath = "/opt/heyearth/ops/stream-proxy-switch.sh"
	streamProxyLog        = "/var/log/heyearth-stream-proxy.log"
)

// streamPorts maps the router's backend ports between the modes.
var streamPorts = map[string][2]string{ // off port -> {off, on}
	"https": {"8443", nginxProxyProtocolPort},
	"xray":  {xrayRealityPort, xrayRealityPPPort},
}

// ───────────────────────── nginx config scanning (line based, fail closed) ─────────────────────────

// nginxStmt is one line of a config with the block it sits in.
type nginxStmt struct {
	code  string // the line without its comment, trimmed
	block int    // index into blocks of the innermost block the line is in (-1 = top level)
}

type nginxBlock struct {
	kind string // first word of the header: server, upstream, map, location, ...
	head string // the whole header before "{"
}

// scanNginx assigns every line to its innermost block. A header line ("upstream x {") belongs
// to the block it opens. Lines that open and close a block on the same line ("location / { ... }")
// stay in the outer block — callers that need their directives fail closed.
func scanNginx(content string) ([]nginxStmt, []nginxBlock) {
	lines := strings.Split(content, "\n")
	stmts := make([]nginxStmt, len(lines))
	var blocks []nginxBlock
	var stack []int
	for i, l := range lines {
		code := l
		if j := strings.IndexByte(code, '#'); j >= 0 {
			code = code[:j]
		}
		code = strings.TrimSpace(code)
		top := -1
		if len(stack) > 0 {
			top = stack[len(stack)-1]
		}
		stmts[i] = nginxStmt{code: code, block: top}
		head := ""
		for _, ch := range code {
			switch ch {
			case '{':
				h := strings.TrimSpace(head)
				kind := h
				if f := strings.Fields(h); len(f) > 0 {
					kind = f[0]
				}
				blocks = append(blocks, nginxBlock{kind: kind, head: h})
				stack = append(stack, len(blocks)-1)
				if strings.Count(code, "{") == 1 && strings.HasSuffix(code, "{") {
					stmts[i].block = len(blocks) - 1
				}
				head = ""
			case '}':
				if len(stack) > 0 {
					stack = stack[:len(stack)-1]
				}
				head = ""
			case ';':
				head = ""
			default:
				head += string(ch)
			}
		}
	}
	return stmts, blocks
}

var (
	reProxyPassBackend = regexp.MustCompile(`^proxy_pass\s+\$backend;$`)
	reProxyProtocolOn  = regexp.MustCompile(`^proxy_protocol\s+on;$`)
	reUpstreamServer   = regexp.MustCompile(`^server\s+127\.0\.0\.1:(\d+)(\s[^;]*)?;$`)
	reMapEntry         = regexp.MustCompile(`^(\S+)\s+(\S+);$`)
	reHTTPS8443Listen  = regexp.MustCompile(`^listen\s+((0\.0\.0\.0|\*|127\.0\.0\.1):)?8443(\s[^;]*)?;$`)
	rePPListen         = regexp.MustCompile(`^listen\s+127\.0\.0\.1:` + nginxProxyProtocolPort + `\s[^;]*proxy_protocol[^;]*;$`)
	reRealIPFrom       = regexp.MustCompile(`^set_real_ip_from\s+127\.0\.0\.1;$`)
	reRealIPHeader     = regexp.MustCompile(`^real_ip_header\s+proxy_protocol;$`)
)

// streamRouter describes the SNI router of one stream config file.
type streamRouter struct {
	routerBlocks map[int]bool     // server blocks with proxy_pass $backend
	upstreams    map[string]int   // name -> block index of the upstreams the router's map names
	ports        map[string][]int // upstream name -> its 127.0.0.1 server ports
	pp           []bool           // proxy_protocol on, per router block
}

// parseStreamRouter finds the router (server blocks with `proxy_pass $backend;`), the upstreams
// named by the `map ... $backend` block and their loopback server ports.
func parseStreamRouter(content string) (streamRouter, []nginxStmt, error) {
	stmts, blocks := scanNginx(content)
	r := streamRouter{routerBlocks: map[int]bool{}, upstreams: map[string]int{}, ports: map[string][]int{}}
	names := map[string]bool{}
	for _, s := range stmts {
		if s.block < 0 {
			continue
		}
		b := blocks[s.block]
		switch {
		case b.kind == "server" && reProxyPassBackend.MatchString(s.code):
			r.routerBlocks[s.block] = true
		case b.kind == "map" && isBackendMap(b.head):
			if m := reMapEntry.FindStringSubmatch(s.code); m != nil {
				names[m[2]] = true
			}
		}
	}
	if len(r.routerBlocks) == 0 {
		return r, stmts, errors.New("no SNI router (server block with `proxy_pass $backend;`)")
	}
	if len(names) == 0 {
		return r, stmts, errors.New("no `map ... $backend` block naming the router's upstreams")
	}
	for i, b := range blocks {
		if f := strings.Fields(b.head); b.kind == "upstream" && len(f) == 2 && names[f[1]] {
			r.upstreams[f[1]] = i
		}
	}
	pp := map[int]bool{}
	for _, s := range stmts {
		if s.block < 0 {
			continue
		}
		if r.routerBlocks[s.block] && reProxyProtocolOn.MatchString(s.code) {
			pp[s.block] = true
		}
		for name, bi := range r.upstreams {
			if s.block == bi {
				if m := reUpstreamServer.FindStringSubmatch(s.code); m != nil {
					p, _ := strconv.Atoi(m[1])
					r.ports[name] = append(r.ports[name], p)
				}
			}
		}
	}
	ids := make([]int, 0, len(r.routerBlocks))
	for id := range r.routerBlocks {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	for _, id := range ids {
		r.pp = append(r.pp, pp[id])
	}
	return r, stmts, nil
}

// isBackendMap: a `map <source> $backend` header (the router's SNI -> upstream map).
func isBackendMap(head string) bool {
	f := strings.Fields(head)
	return len(f) == 3 && f[0] == "map" && f[2] == "$backend"
}

// detectStreamMode returns "on" or "off" for a router config, or an error when it is neither
// (fail closed: a half-switched or unknown layout is never guessed).
func detectStreamMode(content string) (string, error) {
	r, _, err := parseStreamRouter(content)
	if err != nil {
		return "", err
	}
	ppAll, ppAny := true, false
	for _, v := range r.pp {
		ppAll, ppAny = ppAll && v, ppAny || v
	}
	if ppAny && !ppAll {
		return "", errors.New("proxy_protocol is on in some router blocks only")
	}
	seen := map[string]bool{}
	for _, ports := range r.ports {
		for _, p := range ports {
			seen[strconv.Itoa(p)] = true
		}
	}
	on := seen[nginxProxyProtocolPort] && seen[xrayRealityPPPort] && !seen["8443"] && !seen[xrayRealityPort]
	off := seen["8443"] && seen[xrayRealityPort] && !seen[nginxProxyProtocolPort] && !seen[xrayRealityPPPort]
	switch {
	case ppAll && on:
		return "on", nil
	case !ppAny && off:
		return "off", nil
	}
	return "", fmt.Errorf("inconsistent router: proxy_protocol=%v, router upstream ports %v (want on: 8446+10444 with proxy_protocol, off: 8443+10443 without)", ppAll, keys(seen))
}

func keys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// transformStream rewrites a router config for mode: the router's upstreams (only those the
// `$backend` map names) switch 8443<->8446 and 10443<->10444, and `proxy_protocol on;` is added
// to (on: before the timeout comment / proxy_timeout / proxy_pass, where the generator and the
// --setup-ops patch keep it) or removed from (off) the router blocks. The result must detect as
// mode, else an error (nothing is written).
func transformStream(content, mode string) (string, error) {
	if mode != "on" && mode != "off" {
		return "", fmt.Errorf("mode %q", mode)
	}
	r, stmts, err := parseStreamRouter(content)
	if err != nil {
		return "", err
	}
	from, to := 0, 1
	if mode == "off" {
		from, to = 1, 0
	}
	upBlocks := map[int]bool{}
	for _, bi := range r.upstreams {
		upBlocks[bi] = true
	}
	lines := strings.Split(content, "\n")
	var out []string
	ppDone := map[int]bool{}
	for i, l := range lines {
		s := stmts[i]
		if s.block >= 0 && upBlocks[s.block] && reUpstreamServer.MatchString(s.code) {
			for _, pair := range streamPorts {
				l = strings.Replace(l, "127.0.0.1:"+pair[from]+";", "127.0.0.1:"+pair[to]+";", 1)
				l = strings.Replace(l, "127.0.0.1:"+pair[from]+" ", "127.0.0.1:"+pair[to]+" ", 1)
			}
		}
		if s.block >= 0 && r.routerBlocks[s.block] {
			if reProxyProtocolOn.MatchString(s.code) {
				if mode == "off" {
					continue
				}
				ppDone[s.block] = true
			}
			if mode == "on" && !ppDone[s.block] && !blockHasPP(stmts, s.block) &&
				(strings.Contains(l, nginxStreamTimeoutComment) || strings.HasPrefix(s.code, "proxy_timeout ") || reProxyPassBackend.MatchString(s.code)) {
				out = append(out, leadingSpace(l)+streamProxyProtocolLine)
				ppDone[s.block] = true
			}
		}
		out = append(out, l)
	}
	res := strings.Join(out, "\n")
	if got, err := detectStreamMode(res); err != nil || got != mode {
		return "", fmt.Errorf("the router cannot be switched to %s automatically (result: %q, %v) — edit it by hand", mode, got, err)
	}
	return res, nil
}

func blockHasPP(stmts []nginxStmt, block int) bool {
	for _, s := range stmts {
		if s.block == block && reProxyProtocolOn.MatchString(s.code) {
			return true
		}
	}
	return false
}

func leadingSpace(l string) string { return l[:len(l)-len(strings.TrimLeft(l, " \t"))] }

// addHTTPProxyListener adds, to every http server block with a plain IPv4/wildcard listen on
// 8443 with ssl, the PROXY listener on 127.0.0.1:8446 (with http2 if the 8443 listen has it) and
// the realip directives, after that listen line, where missing. Additive and harmless while the
// stream does not use 8446.
func addHTTPProxyListener(content string) string {
	stmts, blocks := scanNginx(content)
	has := map[int][3]bool{}
	for _, s := range stmts {
		if s.block < 0 || blocks[s.block].kind != "server" {
			continue
		}
		h := has[s.block]
		h[0] = h[0] || rePPListen.MatchString(s.code)
		h[1] = h[1] || reRealIPFrom.MatchString(s.code)
		h[2] = h[2] || reRealIPHeader.MatchString(s.code)
		has[s.block] = h
	}
	lines := strings.Split(content, "\n")
	var out []string
	done := map[int]bool{}
	for i, l := range lines {
		out = append(out, l)
		s := stmts[i]
		if s.block < 0 || blocks[s.block].kind != "server" || done[s.block] {
			continue
		}
		m := reHTTPS8443Listen.FindStringSubmatch(s.code)
		if m == nil || !strings.Contains(" "+m[3]+" ", " ssl ") {
			continue
		}
		done[s.block] = true
		ind, h := leadingSpace(l), has[s.block]
		if !h[0] {
			h2 := ""
			if strings.Contains(" "+m[3]+" ", " http2 ") {
				h2 = " http2"
			}
			out = append(out, ind+"listen 127.0.0.1:"+nginxProxyProtocolPort+" ssl"+h2+" proxy_protocol;")
		}
		if !h[1] {
			out = append(out, ind+"set_real_ip_from 127.0.0.1;")
		}
		if !h[2] {
			out = append(out, ind+"real_ip_header proxy_protocol;")
		}
	}
	return strings.Join(out, "\n")
}

// ───────────────────────── the live state ─────────────────────────

var reConfFile = regexp.MustCompile(`(?m)^# configuration file (/[A-Za-z0-9._/-]+):$`)

// nginxDumpFiles splits `nginx -T` output into path -> dumped content (for discovery only: the
// exact bytes are read separately).
func nginxDumpFiles(dump string) map[string]string {
	files := map[string]string{}
	idx := reConfFile.FindAllStringSubmatchIndex(dump, -1)
	for i, m := range idx {
		end := len(dump)
		if i+1 < len(idx) {
			end = idx[i+1][0]
		}
		files[dump[m[2]:m[3]]] = dump[m[1]:end]
	}
	return files
}

// streamSwitchFiles classifies the files nginx reads: router files (`proxy_pass $backend;`) and
// http files with an HTTPS listen on 8443.
func streamSwitchFiles(dump string) (routers, https []string) {
	for path, c := range nginxDumpFiles(dump) {
		stmts, _ := scanNginx(c)
		isR, isH := false, false
		for _, s := range stmts {
			isR = isR || reProxyPassBackend.MatchString(s.code)
			if m := reHTTPS8443Listen.FindStringSubmatch(s.code); m != nil && strings.Contains(" "+m[3]+" ", " ssl ") {
				isH = true
			}
		}
		if isR {
			routers = append(routers, path)
		} else if isH {
			https = append(https, path)
		}
	}
	sort.Strings(routers)
	sort.Strings(https)
	return routers, https
}

// realityInbounds checks xray's live config: the SNI of vless-reality and whether the PROXY
// mirror vless-reality-pp (127.0.0.1:10444, acceptProxyProtocol) exists.
func realityInbounds(configJSON []byte) (sni string, hasMirror bool, err error) {
	var cfg struct {
		Inbounds []struct {
			Tag            string `json:"tag"`
			Listen         string `json:"listen"`
			Port           int    `json:"port"`
			StreamSettings struct {
				RealitySettings struct {
					ServerNames []string `json:"serverNames"`
				} `json:"realitySettings"`
				Sockopt struct {
					AcceptProxyProtocol bool `json:"acceptProxyProtocol"`
				} `json:"sockopt"`
			} `json:"streamSettings"`
		} `json:"inbounds"`
	}
	if err := json.Unmarshal(configJSON, &cfg); err != nil {
		return "", false, fmt.Errorf("xray config: %w", err)
	}
	for _, in := range cfg.Inbounds {
		switch {
		case in.Tag == xrayRealityTag && len(in.StreamSettings.RealitySettings.ServerNames) > 0:
			sni = in.StreamSettings.RealitySettings.ServerNames[0]
		case in.Tag == xrayRealityPPTag && in.Listen == "127.0.0.1" && strconv.Itoa(in.Port) == xrayRealityPPPort && in.StreamSettings.Sockopt.AcceptProxyProtocol:
			hasMirror = true
		}
	}
	if sni == "" {
		return "", hasMirror, errors.New("xray config has no vless-reality inbound with serverNames")
	}
	return sni, hasMirror, nil
}

// liveStreamMode is doNginx's view: the mode of the router file it rewrites ("off" while it does
// not exist yet), or an error — then doNginx must not touch nginx.
func liveStreamMode(content string, exists bool) (string, error) {
	if !exists {
		return "off", nil
	}
	return detectStreamMode(content)
}

// streamRouterReadCmd prints the router file base64-encoded, or ABSENT.
const streamRouterReadCmd = "if [ -f " + streamRouterFile + " ]; then base64 -w0 " + streamRouterFile + "; else echo ABSENT; fi"

// streamModeForDeploy turns doNginx's read of the router file (streamRouterReadCmd output) into
// the mode to keep (true = PROXY on). Any error means: do not touch nginx.
func streamModeForDeploy(out string, readErr error) (bool, error) {
	if readErr != nil {
		return false, fmt.Errorf("не удалось прочитать %s: %w", streamRouterFile, readErr)
	}
	out = strings.TrimSpace(out)
	content, exists := "", out != "ABSENT"
	if exists {
		b, err := base64.StdEncoding.DecodeString(out)
		if err != nil {
			return false, fmt.Errorf("%s не прочитан: %w", streamRouterFile, err)
		}
		content = string(b)
	}
	mode, err := liveStreamMode(content, exists)
	if err != nil {
		return false, fmt.Errorf("режим PROXY protocol в %s не определяется: %w", streamRouterFile, err)
	}
	return mode == "on", nil
}

// readRemoteFiles returns the exact bytes of paths on the Hub.
func readRemoteFiles(run func(string) (string, error), paths []string) (map[string]string, error) {
	out := map[string]string{}
	for _, p := range paths {
		if !reConfFile.MatchString("# configuration file " + p + ":") {
			return nil, fmt.Errorf("unexpected path %q", p)
		}
		b64, err := run("base64 -w0 " + p)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", p, err)
		}
		b, err := base64.StdEncoding.DecodeString(strings.TrimSpace(b64))
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", p, err)
		}
		out[p] = string(b)
	}
	return out, nil
}

// ───────────────────────── the detached switch ─────────────────────────

// streamSwitchFile is one file the switch replaces.
type streamSwitchFile struct{ path, sha, target string }

func sha256Hex(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

// streamProxySwitchScript runs on the Hub as root, detached, output appended to the log; the
// last line is "RESULT: <on|off|rolled-back-to-on|rolled-back-to-off|FAILED>". SPP_* are test
// hooks, unset in production.
const streamProxySwitchScript = `#!/usr/bin/env bash
# Rendered by deploy --stream-proxy-protocol (cmd/deploy/stream_proxy.go). Runs detached.
set -u
MODE=__MODE__
PREV=__PREV__
DOMAIN=__DOMAIN__
SNI=__SNI__
LOG="${SPP_LOG:-__LOG__}"
BK="${SPP_BACKUP_DIR:-/var/backups/heyearth-nginx}"
WAIT_API="${SPP_WAIT_API:-60}"
ACCESS_LOG="${SPP_ACCESS_LOG:-/var/log/nginx/access.log}"
exec >>"$LOG" 2>&1
log() { echo "$(date -Is) [stream-proxy] $*"; }
finish() { log "RESULT: $1"; exit "$2"; }
STAMP=$(date +%Y%m%d-%H%M%S)
FILES=$(cat <<'SPP_FILES'
__FILES__SPP_FILES
)
CHANGED=""

api_ready() {
  i=0
  while [ "$i" -lt "$WAIT_API" ]; do
    if [ "$(curl -s -o /dev/null -w '%{http_code}' --max-time 3 http://127.0.0.1:8000/api/v1/health || true)" = 200 ]; then return 0; fi
    sleep 1; i=$((i + 1))
  done
  log "ERROR: heyearth-api does not answer on 127.0.0.1:8000 within ${WAIT_API}s"; return 1
}
# check_443: our site and Reality both work through the 443 stream (as clients reach them).
check_443() {
  code=$(curl -sk -o /dev/null -w '%{http_code}' --max-time 10 --resolve "$DOMAIN:443:127.0.0.1" "https://$DOMAIN/api/v1/health" || true)
  [ "$code" = 200 ] || { log "ERROR: https://$DOMAIN/api/v1/health via 127.0.0.1:443 -> HTTP ${code:-none}"; return 1; }
  if ! echo | timeout 15 openssl s_client -connect 127.0.0.1:443 -servername "$SNI" >/dev/null 2>&1; then
    log "ERROR: TLS handshake with the Reality SNI $SNI via 127.0.0.1:443 failed"; return 1
  fi
  log "check: site 200 and Reality handshake ($SNI) through :443"
}
# check_real_ip: a request to the Hub's own public address shows that address in nginx's log.
check_real_ip() {
  pub=$(ip -4 -o addr show scope global 2>/dev/null | head -n 1 | sed 's/.* inet \([0-9.]*\)\/.*/\1/')
  case "$pub" in ''|*[!0-9.]*) log "ERROR: no global IPv4 address to probe the real client IP with"; return 1 ;; esac
  ua="heyearth-spp-$(date +%s)-$$"
  code=$(curl -sk -o /dev/null -w '%{http_code}' --max-time 10 -A "$ua" --resolve "$DOMAIN:443:$pub" "https://$DOMAIN/api/v1/health" || true)
  [ "$code" = 200 ] || { log "ERROR: https://$DOMAIN/api/v1/health via $pub:443 -> HTTP ${code:-none}"; return 1; }
  sleep 1
  seen=$(grep -F "$ua" "$ACCESS_LOG" 2>/dev/null | tail -n 1 | cut -d' ' -f1)
  if [ -z "$seen" ] || [ "$seen" = 127.0.0.1 ]; then log "ERROR: nginx logged the probe from ${seen:-nothing}, expected $pub"; return 1; fi
  log "check: nginx sees the real client address ($seen)"
}
verify() { api_ready && check_443 && { [ "$1" = off ] || check_real_ip; }; }
restore() {
  [ -n "$CHANGED" ] || return 0
  for p in $CHANGED; do cat "$BK/$(printf '%s' "$p" | tr '/' '_').$STAMP.spp" > "$p"; done
  CHANGED=""
  if nginx -t && systemctl reload nginx; then log "previous nginx config restored and reloaded"; else log "ERROR: restoring the previous nginx config: nginx -t / reload failed"; return 1; fi
}
rollback() {
  log "ROLLBACK to $PREV"
  restore && sleep 1 && verify "$PREV" && finish "rolled-back-to-$PREV" 1
  finish FAILED 2
}

log "start: --stream-proxy-protocol $MODE (now: $PREV, domain $DOMAIN, Reality SNI $SNI)"
for c in nginx curl openssl systemctl ss sha256sum base64; do
  command -v "$c" >/dev/null 2>&1 || { log "ERROR: $c not installed — nothing changed"; finish FAILED 1; }
done
while read -r p sha _; do
  [ -n "$p" ] || continue
  [ "$(sha256sum "$p" 2>/dev/null | cut -d' ' -f1)" = "$sha" ] || { log "ERROR: $p changed since it was read — nothing changed, run the command again"; finish FAILED 1; }
done <<< "$FILES"
if [ "$MODE" = on ] && ! ss -ltn 2>/dev/null | grep -q '127\.0\.0\.1:__PP_XRAY__ '; then
  log "ERROR: xray does not listen on 127.0.0.1:__PP_XRAY__ (the PROXY Reality inbound) — deploy the API first; nothing changed"; finish FAILED 1
fi
verify_now() { api_ready && check_443; }
verify_now || { log "ERROR: the current mode ($PREV) does not work through :443 — not switching; nothing changed"; finish FAILED 1; }
if [ -z "$(printf '%s' "$FILES" | tr -d '[:space:]')" ]; then
  log "already $MODE: nothing to change"
  verify "$MODE" && finish "$MODE" 0
  finish FAILED 1
fi
mkdir -p "$BK"; chmod 700 "$BK"
while read -r p sha b64; do
  [ -n "$p" ] || continue
  b="$BK/$(printf '%s' "$p" | tr '/' '_').$STAMP.spp"
  cp -p "$p" "$b" || { log "ERROR: backup of $p failed"; restore; finish FAILED 1; }
  CHANGED="$CHANGED $p"
  printf '%s' "$b64" | base64 -d > "$b.new" && cat "$b.new" > "$p" && rm -f "$b.new" || { log "ERROR: writing $p failed"; restore; finish "rolled-back-to-$PREV" 1; }
  log "nginx: $p written for PROXY $MODE (previous: $b)"
done <<< "$FILES"
if ! nginx -t; then
  log "ERROR: nginx -t rejected the PROXY $MODE config"
  for p in $CHANGED; do cat "$BK/$(printf '%s' "$p" | tr '/' '_').$STAMP.spp" > "$p"; done
  log "previous files restored (nginx was not reloaded)"
  finish "rolled-back-to-$PREV" 1
fi
systemctl reload nginx || { log "ERROR: systemctl reload nginx failed"; rollback; }
sleep 1
log "nginx reloaded with PROXY $MODE"
verify "$MODE" || rollback
finish "$MODE" 0
`

var streamProxyDomainRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9.-]*[a-z0-9])?$`)

// renderStreamProxySwitchScript renders the switch. files are the files to replace (none when
// the target mode is already in place: the script then only verifies).
func renderStreamProxySwitchScript(mode, prev, domain, sni string, files []streamSwitchFile) (string, error) {
	for _, m := range []string{mode, prev} {
		if m != "on" && m != "off" {
			return "", fmt.Errorf("--stream-proxy-protocol %q: ожидается on или off", m)
		}
	}
	if !streamProxyDomainRe.MatchString(domain) || !streamProxyDomainRe.MatchString(sni) {
		return "", fmt.Errorf("недопустимый домен %q / SNI %q", domain, sni)
	}
	var sb strings.Builder
	for _, f := range files {
		if !reConfFile.MatchString("# configuration file " + f.path + ":") {
			return "", fmt.Errorf("unexpected path %q", f.path)
		}
		fmt.Fprintf(&sb, "%s %s %s\n", f.path, f.sha, base64.StdEncoding.EncodeToString([]byte(f.target)))
	}
	return strings.NewReplacer("__MODE__", mode, "__PREV__", prev, "__DOMAIN__", domain, "__SNI__", sni,
		"__LOG__", streamProxyLog, "__FILES__", sb.String(), "__PP_XRAY__", xrayRealityPPPort).Replace(streamProxySwitchScript), nil
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

// ───────────────────────── the command ─────────────────────────

// remoteRun runs a command on the Hub and returns its stdout (not printed).
type remoteRun func(cmd string) (string, error)

func sshRun(c *ssh.Client) remoteRun {
	return func(cmd string) (string, error) { return runSSHQuiet(c, cmd, "") }
}

// planStreamSwitch reads the live state and computes the switch: the current mode, the Reality
// SNI and the files to replace. Fails closed on anything unexpected.
func planStreamSwitch(run remoteRun, mode string) (prev, sni string, files []streamSwitchFile, err error) {
	dump, err := run("nginx -T 2>/dev/null")
	if err != nil || !strings.Contains(dump, "# configuration file ") {
		return "", "", nil, fmt.Errorf("nginx -T: %v", err)
	}
	routers, https := streamSwitchFiles(dump)
	if len(routers) == 0 {
		return "", "", nil, errors.New("nginx has no SNI router (proxy_pass $backend)")
	}
	contents, err := readRemoteFiles(run, append(append([]string{}, routers...), https...))
	if err != nil {
		return "", "", nil, err
	}
	for _, p := range routers {
		m, err := detectStreamMode(contents[p])
		if err != nil {
			return "", "", nil, fmt.Errorf("%s: %w", p, err)
		}
		if prev != "" && m != prev {
			return "", "", nil, fmt.Errorf("router files disagree on the mode (%s vs %s)", prev, m)
		}
		prev = m
	}
	xcfg, err := run("cat /opt/heyearth/xray/config.json")
	if err != nil {
		return "", "", nil, fmt.Errorf("xray config: %w", err)
	}
	sni, hasMirror, err := realityInbounds([]byte(xcfg))
	if err != nil {
		return "", "", nil, err
	}
	if mode == "on" && !hasMirror {
		return "", "", nil, errors.New("xray has no PROXY Reality inbound vless-reality-pp on 127.0.0.1:10444 — deploy the API with it first (--update-main)")
	}
	for _, p := range routers {
		t, err := transformStream(contents[p], mode)
		if err != nil {
			return "", "", nil, fmt.Errorf("%s: %w", p, err)
		}
		if t != contents[p] {
			files = append(files, streamSwitchFile{p, sha256Hex(contents[p]), t})
		}
	}
	if mode == "on" {
		for _, p := range https {
			if t := addHTTPProxyListener(contents[p]); t != contents[p] {
				files = append(files, streamSwitchFile{p, sha256Hex(contents[p]), t})
			}
		}
	}
	return prev, sni, files, nil
}

// switchStreamProxyProtocol — deploy --stream-proxy-protocol on|off.
func switchStreamProxyProtocol(client *ssh.Client, cfg *deployConfig, mode string) {
	if mode != "on" && mode != "off" {
		log.Fatalf("[stream-proxy] --stream-proxy-protocol %q: ожидается on или off", mode)
	}
	prev, sni, files, err := planStreamSwitch(sshRun(client), mode)
	if err != nil {
		log.Fatalf("[stream-proxy] ОШИБКА (ничего не менял): %v", err)
	}
	fmt.Printf("[stream-proxy] сейчас: %s, цель: %s; файлов к замене: %d\n", prev, mode, len(files))
	for _, f := range files {
		fmt.Printf("[stream-proxy]   %s\n", f.path)
	}
	fmt.Println("[stream-proxy] Простоя нет: меняется только upstream nginx stream (nginx -t + reload); xray и API не перезапускаются,")
	fmt.Println("[stream-proxy] сессии, открытые по прежнему пути, доживают. Переключение идёт на Hub юнитом " + streamProxyUnit + " (обрыв SSH не мешает), лог: " + streamProxyLog)
	script, err := renderStreamProxySwitchScript(mode, prev, cfg.siteDomain, sni, files)
	if err != nil {
		log.Fatalf("[stream-proxy] %v", err)
	}
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
	redial := func() (remoteRun, error) {
		c, err := sshDial(fmt.Sprintf("%s:%d", cfg.host, cfg.deployPort), cfg.deployUser, cfg.deployPassword, cfg.hostKeyFingerprint)
		if err != nil {
			return nil, err
		}
		return sshRun(c), nil
	}
	result, run := pollStreamSwitch(sshRun(client), redial, start, 3*time.Minute, func() { time.Sleep(2 * time.Second) }, func(s string) { fmt.Print(s) })
	switch result {
	case "on", "off":
		fmt.Printf("[stream-proxy] Готово: PROXY protocol %s\n", result)
	case "":
		log.Fatalf("[stream-proxy] нет результата за отведённое время. Состояние на Hub: go run ./cmd/deploy --ssh 'tail -n 40 %s; systemctl status %s'", streamProxyLog, streamProxyUnit)
	default:
		log.Fatalf("[stream-proxy] ОШИБКА: %s (подробности выше и в %s на Hub). FAILED: сначала повторите противоположную команду (--stream-proxy-protocol %s)", result, streamProxyLog, prev)
	}
	if mode == "on" {
		checkRealClientIPFromOutside(run, cfg.siteDomain)
	}
}

// pollStreamSwitch prints the switch's new log lines until its RESULT line. When the SSH
// connection drops it re-dials (the switch keeps running on the Hub) and returns the runner in
// use at the end, so later checks do not use a dead connection.
func pollStreamSwitch(run remoteRun, redial func() (remoteRun, error), start int, timeout time.Duration, pause func(), print func(string)) (string, remoteRun) {
	seen := start
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		pause()
		out, err := run(fmt.Sprintf("tail -n +%d %s", seen+1, streamProxyLog))
		if err != nil {
			print(fmt.Sprintf("[stream-proxy] SSH: %v — переподключаюсь (переключение на Hub продолжается)\n", err))
			if r, derr := redial(); derr == nil {
				run = r
			}
			continue
		}
		if out == "" {
			continue
		}
		print(out)
		seen += strings.Count(out, "\n")
		if r := streamProxyResult(out); r != "" {
			return r, run
		}
	}
	return "", run
}

// checkRealClientIPFromOutside requests the health endpoint from this machine (outside the Hub)
// with a unique User-Agent and looks the request up in nginx's access log on the Hub: its client
// address must not be 127.0.0.1.
func checkRealClientIPFromOutside(run remoteRun, domain string) {
	nonce := make([]byte, 6)
	_, _ = rand.Read(nonce)
	ua := "heyearth-spp-ext-" + hex.EncodeToString(nonce)
	req, err := http.NewRequest(http.MethodGet, "https://"+domain+"/api/v1/health", nil)
	if err != nil {
		fmt.Printf("[stream-proxy] WARN: внешняя проверка: %v\n", err)
		return
	}
	req.Header.Set("User-Agent", ua)
	resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		fmt.Printf("[stream-proxy] WARN: внешняя проверка https://%s/api/v1/health не прошла: %v\n", domain, err)
		return
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	time.Sleep(time.Second)
	out, err := run("grep -F '" + ua + "' /var/log/nginx/access.log | tail -n 1 | cut -d' ' -f1")
	ip := strings.TrimSpace(out)
	switch {
	case err != nil:
		fmt.Printf("[stream-proxy] WARN: access.log на Hub не прочитан: %v\n", err)
	case ip == "":
		fmt.Printf("[stream-proxy] WARN: запрос с этой машины (HTTP %d) не найден в /var/log/nginx/access.log\n", resp.StatusCode)
	case ip == "127.0.0.1":
		fmt.Printf("[stream-proxy] ОШИБКА: nginx видит запрос с этой машины как 127.0.0.1 — реальный IP не доходит. Откат: go run ./cmd/deploy --stream-proxy-protocol off\n")
	default:
		fmt.Printf("[stream-proxy] Внешняя проверка: nginx (и API через X-Real-IP) видит клиента как %s (HTTP %d)\n", ip, resp.StatusCode)
	}
}
