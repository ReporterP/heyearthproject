package main

import (
	"os"
	"strings"
	"testing"
)

const sppDomain = "hiearth.ru"

func sppSSLConf() string {
	return buildNginxConfSSL(sppDomain, "/opt/heyearth/mainserver", "/etc/letsencrypt/live/"+sppDomain, true)
}

// legacySSLConf is the http config as the Hub has it before this change: no PROXY listener.
func legacySSLConf(t *testing.T) string {
	t.Helper()
	c := strings.ReplaceAll(sppSSLConf(), nginxProxyProtocolListen, "")
	if strings.Contains(c, "8446") {
		t.Fatal("cannot derive the legacy http config")
	}
	return c
}

func TestBuildNginxStreamConfProxyProtocol(t *testing.T) {
	off, on := buildNginxStreamConf(sppDomain, false), buildNginxStreamConf(sppDomain, true)
	if strings.Contains(off, "proxy_protocol") || !strings.Contains(off, "    server 127.0.0.1:8443;\n") {
		t.Errorf("off: no PROXY, domains to 8443:\n%s", off)
	}
	if !strings.Contains(on, "    server 127.0.0.1:"+nginxProxyProtocolPort+";\n") || strings.Contains(on, "127.0.0.1:8443") {
		t.Errorf("on: domains must go to the PROXY listener %s:\n%s", nginxProxyProtocolPort, on)
	}
	want := "    ssl_preread on;\n    " + streamProxyProtocolLine + "\n    " + nginxStreamTimeoutComment + "\n    proxy_timeout 3600s;\n    proxy_pass $backend;\n"
	if !strings.Contains(on, want) {
		t.Errorf("on: proxy_protocol must sit in the router block before the timeout comment:\n%s", on)
	}
	// Reality stays on 10443 either way; the difference is the header only.
	for _, c := range []string{off, on} {
		if !strings.Contains(c, "server 127.0.0.1:10443;") || !strings.Contains(c, "default        xray_vless;") {
			t.Errorf("Reality upstream changed:\n%s", c)
		}
	}
}

func TestBuildNginxConfSSLProxyListener(t *testing.T) {
	c := sppSSLConf()
	if n := strings.Count(c, nginxProxyProtocolListen); n != 3 {
		t.Errorf("every HTTPS server block (site, admin, site.) needs the PROXY listener once, got %d", n)
	}
	for _, w := range []string{"listen 127.0.0.1:8446 ssl http2 proxy_protocol;", "set_real_ip_from 127.0.0.1;", "real_ip_header proxy_protocol;"} {
		if !strings.Contains(c, w) {
			t.Errorf("lacks %q", w)
		}
	}
	// the plain 8443 listeners stay (local direct checks such as the cert monitor need them)
	if strings.Count(c, "    listen 8443 ssl http2;\n") != 3 {
		t.Error("the plain 8443 listeners must stay")
	}
	// headers towards the API are unchanged: realip turns $remote_addr into the client address
	if strings.Count(c, "proxy_set_header X-Real-IP $remote_addr;") != 2 {
		t.Error("X-Real-IP must keep coming from $remote_addr")
	}
	noXray := buildNginxConfSSL(sppDomain, "/opt", "/etc/letsencrypt/live/x", false)
	if strings.Contains(noXray, "8446") || strings.Contains(noXray, "real_ip_header") {
		t.Error("without the stream (no xray) nginx listens on 443 itself: no PROXY listener")
	}
}

// runSppAwk applies streamProxyAwk to content for mode and returns the result.
func runSppAwk(t *testing.T, content, mode string) string {
	t.Helper()
	out, exit := runBash(t, `T=$(mktemp -d); trap 'rm -rf "$T"' EXIT
cat > "$T/f" <<'SPP_FILE_EOF'
`+content+`SPP_FILE_EOF
awk -v mode=`+mode+` -v port=`+nginxProxyProtocolPort+` `+shQuote(renderStreamProxyAwk())+` "$T/f" "$T/f"
`)
	if exit != 0 {
		t.Fatalf("awk exit %d:\n%s", exit, out)
	}
	return out
}

func TestStreamProxyAwk(t *testing.T) {
	if !haveBash() {
		t.Skip("no working bash")
	}
	off, on := buildNginxStreamConf(sppDomain, false), buildNginxStreamConf(sppDomain, true)
	t.Run("router: off -> on gives the generator's on form, on is a fixed point, on -> off gives the off form", func(t *testing.T) {
		if got := runSppAwk(t, off, "on"); got != on {
			t.Fatalf("off->on:\n--- got\n%s\n--- want\n%s", got, on)
		}
		if got := runSppAwk(t, on, "on"); got != on {
			t.Errorf("on->on changed the file:\n%s", got)
		}
		if got := runSppAwk(t, on, "off"); got != off {
			t.Errorf("on->off:\n--- got\n%s\n--- want\n%s", got, off)
		}
		if got := runSppAwk(t, off, "off"); got != off {
			t.Errorf("off->off changed the file:\n%s", got)
		}
	})
	t.Run("a hand-written router without the timeout comment gets proxy_protocol before proxy_pass", func(t *testing.T) {
		hand := "upstream https_up {\n    server 127.0.0.1:8443;\n}\nserver {\n    listen 443;\n    ssl_preread on;\n    proxy_pass $backend;\n}\n"
		want := "upstream https_up {\n    server 127.0.0.1:8446;\n}\nserver {\n    listen 443;\n    ssl_preread on;\n    " + streamProxyProtocolLine + "\n    proxy_pass $backend;\n}\n"
		if got := runSppAwk(t, hand, "on"); got != want {
			t.Errorf("got\n%s\nwant\n%s", got, want)
		}
	})
	t.Run("http: the legacy config gets the PROXY listener and realip in each HTTPS block, once; the generator's config is already as wanted", func(t *testing.T) {
		legacy := legacySSLConf(t)
		got := runSppAwk(t, legacy, "on")
		if n := strings.Count(got, "listen 127.0.0.1:8446 ssl http2 proxy_protocol;"); n != 3 {
			t.Errorf("%d PROXY listeners, want 3:\n%s", n, got)
		}
		if strings.Count(got, "set_real_ip_from 127.0.0.1;") != 3 || strings.Count(got, "real_ip_header proxy_protocol;") != 3 {
			t.Errorf("realip must be in each of the 3 HTTPS blocks:\n%s", got)
		}
		if runSppAwk(t, got, "on") != got {
			t.Error("the patched http config is not a fixed point")
		}
		if gen := sppSSLConf(); runSppAwk(t, gen, "on") != gen {
			t.Error("the generator's http config must already be as wanted")
		}
		if runSppAwk(t, got, "off") != got {
			t.Error("off leaves the (unused) PROXY listener in place")
		}
		// the port-80 blocks are untouched
		if strings.Count(got, "listen 80") != strings.Count(legacy, "listen 80") {
			t.Error("port-80 blocks changed")
		}
	})
}

// ───────────────────────── the switch, against a simulated Hub ─────────────────────────

// The stubs simulate the Hub: the site (curl) only answers when the live nginx routes our domains
// consistently (PROXY on: upstream 8446 and the http PROXY listener; off: upstream 8443), and the
// Reality handshake (openssl) only works when the live nginx and xray agree on PROXY. "restart
// heyearth-api" plays the API's start-up sync: it rewrites xray.json from the setting and starts
// a new xray process (pid + 1) — unless API_FAIL=1. The RUNNING xray's PROXY state
// ($T/xray.running) is what Reality answers with; with API_SLOW_XRAY=1 the new config is on disk
// at once but the new process only appears at the second MainPID query (the real API writes
// config.json first and restarts xray afterwards).
const sppHarnessStubs = `
mkdir -p "$T/bin" "$T/etc/nginx/stream.d" "$T/etc/nginx/conf.d" "$T/bk"
R="$T/etc/nginx/stream.d/heyearth.conf"; H="$T/etc/nginx/conf.d/heyearth.conf"
printf '%s\n%s\n' "$R" "$H" > "$T/nginx.files"
cat > "$T/bin/nginx" <<'STUB'
#!/usr/bin/env bash
case "$1" in
  -T) for f in $(cat "$T/nginx.files"); do echo "# configuration file $f:"; cat "$f"; echo; done ;;
  -t) echo "nginx -t" >> "$T/calls"; if [ "${NGINX_t_FAIL:-0}" = 1 ]; then echo "nginx: [emerg] stub" >&2; exit 1; fi ;;
esac
STUB
cat > "$T/bin/systemctl" <<'STUB'
#!/usr/bin/env bash
R="$T/etc/nginx/stream.d/heyearth.conf"; H="$T/etc/nginx/conf.d/heyearth.conf"
xray_json() {
  so=""; if [ "$1" = true ]; then so=',
        "sockopt": {
          "acceptProxyProtocol": true
        }'; fi
  printf '{\n  "inbounds": [\n    {\n      "streamSettings": {\n        "realitySettings": {\n          "serverNames": [\n            "yandex.ru",\n            "www.yandex.ru"\n          ]\n        }%s\n      }\n    }\n  ]\n}\n' "$so" > "$T/xray.json"
}
case "$1 $2" in
  "reload nginx")
    echo "reload nginx" >> "$T/calls"
    pp=0; if grep -Eq '^[[:space:]]*proxy_protocol[[:space:]]+on' "$R"; then pp=1; fi
    up=$(grep -Eo 'server 127\.0\.0\.1:(8443|8446)' "$R" | head -n 1 | cut -d: -f2)
    hl=0; if grep -q 'listen 127.0.0.1:8446 ssl.*proxy_protocol' "$H"; then hl=1; fi
    echo "$pp $up $hl" > "$T/live" ;;
  "restart heyearth-api")
    echo "restart heyearth-api" >> "$T/calls"
    if [ "${API_FAIL:-0}" = 1 ]; then exit 0; fi
    xray_json "$(cat "$T/setting")"
    if [ "${API_SLOW_XRAY:-0}" = 1 ]; then echo 0 > "$T/xray.pending"; exit 0; fi
    echo $(( $(cat "$T/xray.pid") + 1 )) > "$T/xray.pid"; { grep -c '"acceptProxyProtocol": true' "$T/xray.json" > "$T/xray.running" || true; } ;;
  "init true"|"init false") xray_json "$2"; { grep -c '"acceptProxyProtocol": true' "$T/xray.json" > "$T/xray.running" || true; } ;;
  "show -p")
    if [ -f "$T/xray.pending" ]; then
      n=$(( $(cat "$T/xray.pending") + 1 )); echo "$n" > "$T/xray.pending"
      if [ "$n" -ge 2 ]; then rm -f "$T/xray.pending"; echo $(( $(cat "$T/xray.pid") + 1 )) > "$T/xray.pid"; { grep -c '"acceptProxyProtocol": true' "$T/xray.json" > "$T/xray.running" || true; }; fi
    fi
    cat "$T/xray.pid" ;;
  "is-active --quiet") exit 0 ;;
esac
STUB
cat > "$T/bin/runuser" <<'STUB'
#!/usr/bin/env bash
if [ "${PSQL_FAIL:-0}" = 1 ]; then exit 1; fi
sql="${@: -1}"
case "$sql" in
  *"'stream_proxy_protocol', 'true'"*) echo true > "$T/setting"; echo "setting true" >> "$T/calls" ;;
  *"'stream_proxy_protocol', 'false'"*) echo false > "$T/setting"; echo "setting false" >> "$T/calls" ;;
  *) echo "STUB-BAD-SQL $sql" >&2; exit 99 ;;
esac
STUB
cat > "$T/bin/curl" <<'STUB'
#!/usr/bin/env bash
ua=""; res=""
while [ $# -gt 0 ]; do case "$1" in -A) ua="$2"; shift 2 ;; --resolve) res="$2"; shift 2 ;; *) shift ;; esac; done
addr=${res##*:}
echo "curl $addr" >> "$T/calls"
read -r pp up hl < "$T/live"
ok=0
if [ "$pp" = 1 ] && [ "$up" = 8446 ] && [ "$hl" = 1 ]; then ok=1; fi
if [ "$pp" = 0 ] && [ "$up" = 8443 ]; then ok=1; fi
if [ "${CURL_FAIL:-0}" = 1 ]; then ok=0; fi
if [ "$ok" = 1 ]; then
  if [ -n "$ua" ]; then ip=127.0.0.1; if [ "$pp" = 1 ]; then ip="$addr"; fi; echo "$ip - - [x] \"GET /api/v1/health HTTP/2.0\" 200 2 \"-\" \"$ua\"" >> "$T/access.log"; fi
  printf 200
else
  printf 502
fi
STUB
cat > "$T/bin/openssl" <<'STUB'
#!/usr/bin/env bash
echo "openssl" >> "$T/calls"
read -r pp up hl < "$T/live"
[ "$pp" = "$(cat "$T/xray.running")" ]
STUB
printf '#!/bin/sh\necho "LISTEN 0 4096 127.0.0.1:10443 0.0.0.0:*"\n' > "$T/bin/ss"
printf '#!/bin/sh\necho "2: eth0    inet 138.16.181.225/24 brd 138.16.181.255 scope global eth0"\n' > "$T/bin/ip"
printf '#!/bin/sh\nexit 0\n' > "$T/bin/sleep"
chmod +x "$T/bin/"*
export PATH="$T/bin:$PATH"
export SPP_LOG="$T/switch.log" SPP_XRAY_CONF="$T/xray.json" SPP_BACKUP_DIR="$T/bk" SPP_WAIT_XRAY=3 SPP_ACCESS_LOG="$T/access.log"
: > "$T/calls"; : > "$T/access.log"; echo 100 > "$T/xray.pid"
`

// sppHarness starts the simulated Hub in state "on" or "off" (nginx files, live nginx, setting,
// xray) and runs the switch for mode.
func sppHarness(t *testing.T, start, mode string, vars map[string]string) string {
	t.Helper()
	script, err := renderStreamProxySwitchScript(mode, sppDomain, "yandex.ru")
	if err != nil {
		t.Fatal(err)
	}
	router, http, setting, live := buildNginxStreamConf(sppDomain, false), legacySSLConf(t), "false", "0 8443 0"
	if start == "on" {
		router, http, setting, live = buildNginxStreamConf(sppDomain, true), sppSSLConf(), "true", "1 8446 1"
	}
	base := map[string]string{"API_FAIL": "0", "API_SLOW_XRAY": "0", "NGINX_t_FAIL": "0", "CURL_FAIL": "0", "PSQL_FAIL": "0"}
	return assignments(mergeVars(base, vars)) + `export API_FAIL API_SLOW_XRAY NGINX_t_FAIL CURL_FAIL PSQL_FAIL
T=$(mktemp -d); export T; trap 'rm -rf "$T"' EXIT
` + sppHarnessStubs + `
cat > "$R" <<'SPP_R_EOF'
` + router + `SPP_R_EOF
cat > "$H" <<'SPP_H_EOF'
` + http + `SPP_H_EOF
cp "$R" "$T/r.orig"; cp "$H" "$T/h.orig"
echo ` + setting + ` > "$T/setting"; echo "` + live + `" > "$T/live"; systemctl init ` + setting + `
cat > "$T/switch.sh" <<'SPP_SWITCH_EOF'
` + script + `SPP_SWITCH_EOF
bash "$T/switch.sh"; echo "STATE rc=$?"
cat "$T/switch.log"
echo "STATE result=$(grep -o 'RESULT: .*' "$T/switch.log" | tail -n 1)"
echo "STATE calls=$(tr '\n' ';' < "$T/calls")"
echo "STATE setting=$(cat "$T/setting") xray_pp=$(grep -c '"acceptProxyProtocol": true' "$T/xray.json") live=$(cat "$T/live")"
echo "STATE r_orig=$(cmp -s "$R" "$T/r.orig" && echo same || echo changed) h_orig=$(cmp -s "$H" "$T/h.orig" && echo same || echo changed)"
echo "STATE probe_ip=$(grep -o '^[0-9.]* - - .*heyearth-spp-' "$T/access.log" | tail -n 1 | cut -d' ' -f1)"
echo "ROUTER-BEGIN"; cat "$R"; echo "ROUTER-END"
`
}

func TestStreamProxySwitch(t *testing.T) {
	if !haveBash() {
		t.Skip("no working bash")
	}
	run := func(t *testing.T, start, mode string, vars map[string]string) string {
		t.Helper()
		out, exit := runBash(t, sppHarness(t, start, mode, vars))
		if exit != 0 {
			t.Fatalf("harness exit %d:\n%s", exit, out)
		}
		if strings.Contains(out, "STUB-BAD") {
			t.Errorf("bad stub call:\n%s", out)
		}
		return out
	}
	want := func(t *testing.T, out string, lines ...string) {
		t.Helper()
		for _, l := range lines {
			if !hasLine(out, l) {
				t.Errorf("missing %q:\n%s", l, out)
			}
		}
	}
	router := func(out string) string {
		_, r, _ := strings.Cut(out, "ROUTER-BEGIN\n")
		r, _, _ = strings.Cut(r, "ROUTER-END\n")
		return r
	}
	t.Run("on: setting, API sync and a new xray FIRST, then nginx, then verify incl. the real client IP", func(t *testing.T) {
		t.Parallel()
		out := run(t, "off", "on", nil)
		want(t, out, "STATE rc=0", "STATE result=RESULT: on",
			"STATE calls=setting true;restart heyearth-api;nginx -t;reload nginx;curl 127.0.0.1;openssl;curl 138.16.181.225;",
			"STATE setting=true xray_pp=1 live=1 8446 1", "STATE r_orig=changed h_orig=changed", "STATE probe_ip=138.16.181.225")
		if r := router(out); r != buildNginxStreamConf(sppDomain, true) {
			t.Errorf("router after on:\n%s", r)
		}
	})
	t.Run("on, the API writes config.json before xray restarts: nginx waits for the NEW xray process", func(t *testing.T) {
		t.Parallel()
		out := run(t, "off", "on", map[string]string{"API_SLOW_XRAY": "1"})
		want(t, out, "STATE rc=0", "STATE result=RESULT: on",
			"STATE calls=setting true;restart heyearth-api;nginx -t;reload nginx;curl 127.0.0.1;openssl;curl 138.16.181.225;",
			"STATE setting=true xray_pp=1 live=1 8446 1")
	})
	t.Run("off: nginx FIRST, then the setting and the API sync, then verify", func(t *testing.T) {
		t.Parallel()
		out := run(t, "on", "off", nil)
		want(t, out, "STATE rc=0", "STATE result=RESULT: off",
			"STATE calls=nginx -t;reload nginx;setting false;restart heyearth-api;curl 127.0.0.1;openssl;",
			"STATE setting=false xray_pp=0 live=0 8443 1", "STATE r_orig=changed h_orig=same")
		if r := router(out); r != buildNginxStreamConf(sppDomain, false) {
			t.Errorf("router after off:\n%s", r)
		}
	})
	t.Run("on when already on: no API restart, nothing patched, verified", func(t *testing.T) {
		t.Parallel()
		out := run(t, "on", "on", nil)
		want(t, out, "STATE rc=0", "STATE result=RESULT: on", "STATE r_orig=same h_orig=same",
			"STATE calls=setting true;nginx -t;reload nginx;curl 127.0.0.1;openssl;curl 138.16.181.225;")
	})
	t.Run("on, the API never brings xray to PROXY: nginx untouched, setting back to false", func(t *testing.T) {
		t.Parallel()
		out := run(t, "off", "on", map[string]string{"API_FAIL": "1"})
		want(t, out, "STATE rc=1", "STATE result=RESULT: rolled-back-to-off",
			"STATE calls=setting true;restart heyearth-api;setting false;",
			"STATE setting=false xray_pp=0 live=0 8443 0", "STATE r_orig=same h_orig=same")
	})
	t.Run("on, nginx -t rejects the patch: originals restored, xray back to no PROXY", func(t *testing.T) {
		t.Parallel()
		out := run(t, "off", "on", map[string]string{"NGINX_t_FAIL": "1"})
		want(t, out, "STATE rc=1", "STATE result=RESULT: rolled-back-to-off",
			"STATE setting=false xray_pp=0 live=0 8443 0", "STATE r_orig=same h_orig=same")
		if strings.Contains(out, "reload nginx;") {
			t.Errorf("nginx must not be reloaded with a config nginx -t rejected:\n%s", out)
		}
	})
	t.Run("on, verification fails: nginx restored and reloaded BEFORE xray goes back", func(t *testing.T) {
		t.Parallel()
		out := run(t, "off", "on", map[string]string{"CURL_FAIL": "1"})
		want(t, out, "STATE rc=1", "STATE result=RESULT: rolled-back-to-off",
			"STATE calls=setting true;restart heyearth-api;nginx -t;reload nginx;curl 127.0.0.1;nginx -t;reload nginx;setting false;restart heyearth-api;",
			"STATE setting=false xray_pp=0 live=0 8443 0", "STATE r_orig=same h_orig=same")
	})
	t.Run("off, the API never drops PROXY from xray: xray stays, nginx goes back to PROXY", func(t *testing.T) {
		t.Parallel()
		out := run(t, "on", "off", map[string]string{"API_FAIL": "1"})
		want(t, out, "STATE rc=1", "STATE result=RESULT: rolled-back-to-on",
			"STATE calls=nginx -t;reload nginx;setting false;restart heyearth-api;setting true;nginx -t;reload nginx;",
			"STATE setting=true xray_pp=1 live=1 8446 1", "STATE r_orig=same h_orig=same")
	})
	t.Run("the setting cannot be written: nothing else is touched", func(t *testing.T) {
		t.Parallel()
		out := run(t, "off", "on", map[string]string{"PSQL_FAIL": "1"})
		want(t, out, "STATE rc=1", "STATE result=RESULT: FAILED", "STATE calls=", "STATE r_orig=same h_orig=same")
	})
}

func TestRenderStreamProxySwitchScript(t *testing.T) {
	for _, mode := range []string{"on", "off"} {
		s, err := renderStreamProxySwitchScript(mode, sppDomain, "yandex.ru")
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(s, "__") {
			t.Errorf("%s: placeholder left", mode)
		}
		if !strings.Contains(s, "\nMODE="+mode+"\n") || !strings.Contains(s, "\nDOMAIN="+sppDomain+"\n") {
			t.Errorf("%s: mode/domain not rendered", mode)
		}
		if haveBash() {
			if out, code := runBashStdin(t, s, "-n"); code != 0 {
				t.Errorf("%s: bash -n failed (%d): %s", mode, code, out)
			}
		}
		if n := len(shQuote(streamProxyStartScript("x"))) + len(s)*4/3; n > 100_000 {
			t.Errorf("start command too large: %d", n)
		}
	}
	for _, bad := range [][3]string{{"maybe", sppDomain, "yandex.ru"}, {"on", "hiearth.ru;reboot", "yandex.ru"}, {"on", sppDomain, "$(id)"}, {"on", "", "yandex.ru"}} {
		if _, err := renderStreamProxySwitchScript(bad[0], bad[1], bad[2]); err == nil {
			t.Errorf("%v must be refused", bad)
		}
	}
	start := streamProxyStartScript("QUJD")
	for _, w := range []string{"systemctl is-active --quiet " + streamProxyUnit, "systemd-run --quiet --collect --unit=" + streamProxyUnit + " /bin/bash " + streamProxyScriptPath, `echo "START $(wc -l < ` + streamProxyLog + `)"`} {
		if !strings.Contains(start, w) {
			t.Errorf("start script lacks %q", w)
		}
	}
	if r := streamProxyResult("x\n2026 [stream-proxy] RESULT: rolled-back-to-off\n"); r != "rolled-back-to-off" {
		t.Errorf("result %q", r)
	}
	if r := streamProxyResult("2026 [stream-proxy] nginx reloaded\n"); r != "" {
		t.Errorf("no result yet, got %q", r)
	}
}

// The runbook names what the code uses, and the restore runbook turns the mode off around a
// restore (a restored "true" plus a freshly generated stream would break Reality).
func TestStreamProxyRunbook(t *testing.T) {
	b, err := os.ReadFile("../../../../docs/runbooks/stream-proxy-protocol.md")
	if err != nil {
		t.Fatal(err)
	}
	doc := string(b)
	for _, w := range []string{"go run ./cmd/deploy --stream-proxy-protocol on", "go run ./cmd/deploy --stream-proxy-protocol off",
		"127.0.0.1:" + nginxProxyProtocolPort, streamProxyUnit, streamProxyLog, "acceptProxyProtocol", "stream_proxy_protocol", "--update-main"} {
		if !strings.Contains(doc, w) {
			t.Errorf("stream-proxy-protocol runbook lacks %q", w)
		}
	}
	r, err := os.ReadFile(restoreRunbook)
	if err != nil {
		t.Fatal(err)
	}
	restore := string(r)
	off, on := strings.Index(restore, "UPDATE app_settings SET value='false' WHERE key='stream_proxy_protocol'"), strings.Index(restore, "--stream-proxy-protocol on")
	if off < 0 || on < 0 || off > strings.Index(restore, "go run ./cmd/deploy --setup-xray") {
		t.Error("the restore runbook must switch PROXY off right after the DB restore (before --setup-xray) and say how to switch it back on")
	}
}
