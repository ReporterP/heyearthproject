package main

import (
	"encoding/base64"
	"errors"
	"os"
	"strings"
	"testing"
	"time"
)

const sppDomain = "hiearth.ru"

func sppSSLConf() string {
	return buildNginxConfSSL(sppDomain, "/etc/letsencrypt/live/"+sppDomain, true)
}

// legacySSLConf is the http config as the Hub may have it: no PROXY listener.
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
	if strings.Contains(off, "proxy_protocol") || !strings.Contains(off, "upstream nginx_https {\n    server 127.0.0.1:8443;\n}") ||
		!strings.Contains(off, "upstream xray_vless {\n    server 127.0.0.1:10443;\n}") {
		t.Errorf("off: no PROXY, 8443 + 10443:\n%s", off)
	}
	if !strings.Contains(on, "upstream nginx_https {\n    server 127.0.0.1:8446;\n}") || !strings.Contains(on, "upstream xray_vless {\n    server 127.0.0.1:10444;\n}") ||
		strings.Contains(on, ":8443") || strings.Contains(on, ":10443") {
		t.Errorf("on: 8446 + 10444:\n%s", on)
	}
	want := "    ssl_preread on;\n    " + streamProxyProtocolLine + "\n    " + nginxStreamTimeoutComment + "\n    proxy_timeout 3600s;\n    proxy_pass $backend;\n"
	if !strings.Contains(on, want) {
		t.Errorf("on: proxy_protocol must sit in the router block before the timeout comment:\n%s", on)
	}
	for c, m := range map[string]string{off: "off", on: "on"} {
		if got, err := detectStreamMode(c); err != nil || got != m {
			t.Errorf("generator %s detected as %q, %v", m, got, err)
		}
	}
}

func TestBuildNginxConfSSLProxyListener(t *testing.T) {
	c := sppSSLConf()
	if n := strings.Count(c, nginxProxyProtocolListen); n != 3 {
		t.Errorf("every HTTPS server block (site, admin, site.) needs the PROXY listener once, got %d", n)
	}
	if strings.Count(c, "    listen 8443 ssl http2;\n") != 3 {
		t.Error("the plain 8443 listeners must stay (local direct checks such as the cert monitor need them)")
	}
	if strings.Count(c, "proxy_set_header X-Real-IP $remote_addr;") != 2 {
		t.Error("X-Real-IP must keep coming from $remote_addr (realip makes it the client address)")
	}
	if noXray := buildNginxConfSSL(sppDomain, "/etc/letsencrypt/live/x", false); strings.Contains(noXray, "8446") {
		t.Error("without the stream nginx listens on 443 itself: no PROXY listener")
	}
}

// A router with an unrelated upstream that also points at 8443 (not named by the $backend map):
// it is neither counted nor rewritten.
const sppRouterWithExtra = `map $ssl_preread_server_name $backend {
    hiearth.ru     nginx_https;
    default        xray_vless;
}
upstream nginx_https {
    server 127.0.0.1:8443;
}
upstream xray_vless {
    server 127.0.0.1:10443;
}
upstream other_tls {
    server 127.0.0.1:8443;
}
server {
    listen 443;
    ssl_preread on;
    proxy_pass $backend;
}
server {
    listen 9443;
    proxy_pass other_tls;
}
`

func TestDetectStreamMode(t *testing.T) {
	off, on := buildNginxStreamConf(sppDomain, false), buildNginxStreamConf(sppDomain, true)
	bad := map[string]string{
		"PROXY on but the old upstreams":    strings.Replace(off, "    ssl_preread on;\n", "    ssl_preread on;\n    proxy_protocol on;\n", 1),
		"new upstreams but no PROXY":        strings.Replace(on, "    "+streamProxyProtocolLine+"\n", "", 1),
		"only the https upstream switched":  strings.Replace(on, "127.0.0.1:10444", "127.0.0.1:10443", 1),
		"no router":                         strings.Replace(off, "proxy_pass $backend;", "proxy_pass xray_vless;", 1),
		"no $backend map":                   strings.Replace(off, "map $ssl_preread_server_name $backend {", "map $ssl_preread_server_name $other {", 1),
		"one-line upstream (not parseable)": strings.Replace(off, "upstream xray_vless {\n    server 127.0.0.1:10443;\n}", "upstream xray_vless { server 127.0.0.1:10443; }", 1),
		"empty":                             "",
	}
	for name, c := range bad {
		if m, err := detectStreamMode(c); err == nil {
			t.Errorf("%s: detected %q, want an error (fail closed)", name, m)
		}
	}
	if m, err := detectStreamMode(sppRouterWithExtra); err != nil || m != "off" {
		t.Errorf("an unrelated upstream must not matter: %q, %v", m, err)
	}
	// doNginx: no router file yet = off; a broken one = error
	if m, err := liveStreamMode("", false); err != nil || m != "off" {
		t.Errorf("absent router file: %q %v", m, err)
	}
	if _, err := liveStreamMode("garbage {", true); err == nil {
		t.Error("an undeterminable router file must stop doNginx")
	}
}

func TestTransformStream(t *testing.T) {
	off, on := buildNginxStreamConf(sppDomain, false), buildNginxStreamConf(sppDomain, true)
	for _, c := range []struct{ in, mode, want string }{{off, "on", on}, {on, "off", off}, {on, "on", on}, {off, "off", off}} {
		got, err := transformStream(c.in, c.mode)
		if err != nil || got != c.want {
			t.Errorf("-> %s: err %v\n--- got\n%s\n--- want\n%s", c.mode, err, got, c.want)
		}
	}
	got, err := transformStream(sppRouterWithExtra, "on")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "upstream other_tls {\n    server 127.0.0.1:8443;\n}") {
		t.Errorf("an upstream the router does not use must not be rewritten:\n%s", got)
	}
	if !strings.Contains(got, "upstream nginx_https {\n    server 127.0.0.1:8446;\n}") || !strings.Contains(got, "upstream xray_vless {\n    server 127.0.0.1:10444;\n}") ||
		!strings.Contains(got, "    ssl_preread on;\n    "+streamProxyProtocolLine+"\n    proxy_pass $backend;\n") {
		t.Errorf("hand-written router:\n%s", got)
	}
	if strings.Count(got, "proxy_protocol on;") != 1 {
		t.Error("proxy_protocol must go into the router block only")
	}
	oneLine := strings.Replace(off, "upstream xray_vless {\n    server 127.0.0.1:10443;\n}", "upstream xray_vless { server 127.0.0.1:10443; }", 1)
	if _, err := transformStream(oneLine, "on"); err == nil {
		t.Error("a layout that cannot be switched safely must be refused")
	}
	// the --setup-ops patch keeps the on form as it is (tested in ops_hygiene_test); the
	// generator's on form is a fixed point of the switch too
	if got, _ := transformStream(on, "on"); got != on {
		t.Error("on is not a fixed point")
	}
}

func TestAddHTTPProxyListener(t *testing.T) {
	legacy := legacySSLConf(t)
	got := addHTTPProxyListener(legacy)
	if n := strings.Count(got, "listen 127.0.0.1:8446 ssl http2 proxy_protocol;"); n != 3 {
		t.Errorf("%d PROXY listeners, want 3:\n%s", n, got)
	}
	if strings.Count(got, "set_real_ip_from 127.0.0.1;") != 3 || strings.Count(got, "real_ip_header proxy_protocol;") != 3 {
		t.Errorf("realip in each HTTPS block:\n%s", got)
	}
	if addHTTPProxyListener(got) != got {
		t.Error("not idempotent")
	}
	if gen := sppSSLConf(); addHTTPProxyListener(gen) != gen {
		t.Error("the generator's http config must already be as wanted")
	}
	if strings.Count(got, "listen 80") != strings.Count(legacy, "listen 80") {
		t.Error("port-80 blocks changed")
	}
	// a hand-made vhost on 127.0.0.1:8443 without http2 gets the listener without http2
	manual := "server {\n    listen 127.0.0.1:8443 ssl;\n    server_name cdn.hiearth.ru;\n}\nserver {\n    listen [::]:8443 ssl;\n    server_name v6only.example;\n}\n"
	m := addHTTPProxyListener(manual)
	if !strings.Contains(m, "    listen 127.0.0.1:8443 ssl;\n    listen 127.0.0.1:8446 ssl proxy_protocol;\n    set_real_ip_from 127.0.0.1;\n") ||
		strings.Count(m, "8446") != 1 {
		t.Errorf("manual vhost:\n%s", m)
	}
}

// fakeHub answers planStreamSwitch's remote commands from in-memory files.
func fakeHub(files map[string]string, xray string) remoteRun {
	return func(cmd string) (string, error) {
		switch {
		case cmd == "nginx -T 2>/dev/null":
			var sb strings.Builder
			for p, c := range files {
				sb.WriteString("# configuration file " + p + ":\n" + c + "\n")
			}
			return sb.String(), nil
		case strings.HasPrefix(cmd, "base64 -w0 "):
			c, ok := files[strings.TrimPrefix(cmd, "base64 -w0 ")]
			if !ok {
				return "", errors.New("no such file")
			}
			return base64.StdEncoding.EncodeToString([]byte(c)), nil
		case cmd == "cat /opt/heyearth/xray/config.json":
			return xray, nil
		}
		return "", errors.New("unexpected command " + cmd)
	}
}

const sppXrayBoth = `{"inbounds": [
 {"tag": "vless-reality", "listen": "127.0.0.1", "port": 10443, "streamSettings": {"realitySettings": {"serverNames": ["yandex.ru", "www.yandex.ru"]}}},
 {"tag": "vless-reality-pp", "listen": "127.0.0.1", "port": 10444, "streamSettings": {"realitySettings": {"serverNames": ["yandex.ru"]}, "sockopt": {"acceptProxyProtocol": true}}}]}`

func TestPlanStreamSwitch(t *testing.T) {
	r, h := "/etc/nginx/stream.d/heyearth.conf", "/etc/nginx/conf.d/heyearth.conf"
	files := map[string]string{r: buildNginxStreamConf(sppDomain, false), h: legacySSLConf(t), "/etc/nginx/nginx.conf": "events {}\n"}
	prev, sni, plan, err := planStreamSwitch(fakeHub(files, sppXrayBoth), "on")
	if err != nil || prev != "off" || sni != "yandex.ru" || len(plan) != 2 {
		t.Fatalf("prev %q sni %q files %d err %v", prev, sni, len(plan), err)
	}
	for _, f := range plan {
		if f.sha != sha256Hex(files[f.path]) {
			t.Errorf("%s: sha of the live content expected", f.path)
		}
		if f.path == r && f.target != buildNginxStreamConf(sppDomain, true) {
			t.Errorf("router target:\n%s", f.target)
		}
	}
	// already on: nothing to write
	files[r], files[h] = buildNginxStreamConf(sppDomain, true), sppSSLConf()
	if prev, _, plan, err := planStreamSwitch(fakeHub(files, sppXrayBoth), "on"); err != nil || prev != "on" || len(plan) != 0 {
		t.Errorf("already on: prev %q files %d err %v", prev, len(plan), err)
	}
	// off: only the router changes (the PROXY listener stays, unused)
	if _, _, plan, err := planStreamSwitch(fakeHub(files, sppXrayBoth), "off"); err != nil || len(plan) != 1 || plan[0].path != r {
		t.Errorf("off: %v %v", plan, err)
	}
	// xray without the mirror: on is refused, off still possible
	noMirror := `{"inbounds": [{"tag": "vless-reality", "port": 10443, "streamSettings": {"realitySettings": {"serverNames": ["yandex.ru"]}}}]}`
	files[r] = buildNginxStreamConf(sppDomain, false)
	if _, _, _, err := planStreamSwitch(fakeHub(files, noMirror), "on"); err == nil || !strings.Contains(err.Error(), "deploy the API") {
		t.Errorf("on without the PROXY inbound: %v", err)
	}
	// a half-switched router: refused
	files[r] = strings.Replace(buildNginxStreamConf(sppDomain, true), "127.0.0.1:10444", "127.0.0.1:10443", 1)
	if _, _, _, err := planStreamSwitch(fakeHub(files, sppXrayBoth), "off"); err == nil {
		t.Error("an inconsistent router must be refused")
	}
}

// ───────────────────────── the switch, against a simulated Hub ─────────────────────────

// The stubs simulate the Hub: on "reload nginx" the live state is taken from the files (PROXY on,
// the https and xray upstream ports, the http PROXY listener). The site (curl) answers when the
// live nginx is consistent (on: 8446 and the listener, off: 8443); Reality (openssl) when PROXY
// matches the xray inbound the stream uses (on: 10444, off: 10443). xray always has both.
const sppHarnessStubs = `
mkdir -p "$T/bin" "$T/etc/nginx/stream.d" "$T/etc/nginx/conf.d" "$T/bk"
R="$T/etc/nginx/stream.d/heyearth.conf"; H="$T/etc/nginx/conf.d/heyearth.conf"
cat > "$T/bin/nginx" <<'STUB'
#!/usr/bin/env bash
case "$1" in
  -t) echo "nginx -t" >> "$T/calls"; if [ "${NGINX_t_FAIL:-0}" = 1 ]; then echo "nginx: [emerg] stub" >&2; exit 1; fi ;;
esac
STUB
cat > "$T/bin/systemctl" <<'STUB'
#!/usr/bin/env bash
R="$T/etc/nginx/stream.d/heyearth.conf"; H="$T/etc/nginx/conf.d/heyearth.conf"
if [ "$1 $2" = "reload nginx" ]; then
  echo "reload nginx" >> "$T/calls"
  if [ "${STALE_RELOAD:-0}" = 1 ]; then exit 0; fi
  pp=0; if grep -Eq '^[[:space:]]*proxy_protocol[[:space:]]+on' "$R"; then pp=1; fi
  hp=$(grep -A1 'upstream nginx_https' "$R" | grep -Eo '127\.0\.0\.1:[0-9]+' | cut -d: -f2)
  xp=$(grep -A1 'upstream xray_vless' "$R" | grep -Eo '127\.0\.0\.1:[0-9]+' | cut -d: -f2)
  hl=0; if grep -q 'listen 127.0.0.1:8446 ssl.*proxy_protocol' "$H"; then hl=1; fi
  echo "$pp $hp $xp $hl" > "$T/live"
fi
exit 0
STUB
cat > "$T/bin/curl" <<'STUB'
#!/usr/bin/env bash
ua=""; res=""; url=""
while [ $# -gt 0 ]; do case "$1" in -A) ua="$2"; shift 2 ;; --resolve) res="$2"; shift 2 ;; -w|-o|--max-time) shift 2 ;; -*) shift ;; *) url="$1"; shift ;; esac; done
case "$url" in
  http://127.0.0.1:8000/*) echo "curl api" >> "$T/calls"; if [ "${API_DOWN:-0}" = 1 ]; then printf 000; exit 7; fi; printf 200; exit 0 ;;
esac
addr=${res##*:}
echo "curl $addr" >> "$T/calls"
if [ "${CHANGE_DURING_PREFLIGHT:-0}" = 1 ] && [ ! -e "$T/changed" ]; then echo "# edited meanwhile" >> "$T/etc/nginx/stream.d/heyearth.conf"; : > "$T/changed"; fi
read -r pp hp xp hl < "$T/live"
ok=0
if [ "$pp" = 1 ] && [ "$hp" = 8446 ] && [ "$hl" = 1 ]; then ok=1; fi
if [ "$pp" = 0 ] && [ "$hp" = 8443 ]; then ok=1; fi
if [ "${CURL_FAIL:-0}" = 1 ]; then ok=0; fi
if [ "${VERIFY_FAIL:-0}" = 1 ] && [ "$pp" = 1 ]; then ok=0; fi
if [ "$ok" = 1 ]; then
  if [ -n "$ua" ]; then ip=127.0.0.1; if [ "$pp" = 1 ]; then ip="$addr"; if [ "${WRONG_IP:-0}" = 1 ]; then ip=10.9.9.9; fi; fi; echo "$ip - - [x] \"GET /api/v1/health HTTP/2.0\" 200 2 \"-\" \"$ua\"" >> "$T/access.log"; fi
  printf 200
else
  printf 502
fi
STUB
cat > "$T/bin/openssl" <<'STUB'
#!/usr/bin/env bash
echo "openssl" >> "$T/calls"
read -r pp hp xp hl < "$T/live"
{ [ "$pp" = 1 ] && [ "$xp" = 10444 ]; } || { [ "$pp" = 0 ] && [ "$xp" = 10443 ]; }
STUB
cat > "$T/bin/ss" <<'STUB'
#!/bin/sh
echo "LISTEN 0 4096 127.0.0.1:10443 0.0.0.0:*"
if [ "${NO_MIRROR:-0}" != 1 ]; then echo "LISTEN 0 4096 127.0.0.1:10444 0.0.0.0:*"; fi
STUB
printf '#!/bin/sh\necho "2: eth0    inet 203.0.113.225/24 brd 203.0.113.255 scope global eth0"\n' > "$T/bin/ip"
printf '#!/bin/sh\nexit 0\n' > "$T/bin/sleep"
chmod +x "$T/bin/"*
export PATH="$T/bin:$PATH"
export SPP_LOG="$T/switch.log" SPP_BACKUP_DIR="$T/bk" SPP_WAIT_API=3 SPP_ACCESS_LOG="$T/access.log"
: > "$T/calls"; : > "$T/access.log"
`

// sppScenario renders the switch for a Hub that starts in mode start, with the router and http
// files at the given paths inside $T.
func sppScenario(t *testing.T, start, mode string, vars map[string]string, pre string) string {
	t.Helper()
	router, http := buildNginxStreamConf(sppDomain, false), legacySSLConf(t)
	live := "0 8443 10443 0"
	if start == "on" {
		router, http, live = buildNginxStreamConf(sppDomain, true), sppSSLConf(), "1 8446 10444 1"
	}
	var files []streamSwitchFile
	if t2, err := transformStream(router, mode); err != nil {
		t.Fatal(err)
	} else if t2 != router {
		files = append(files, streamSwitchFile{"__R__", sha256Hex(router), t2})
	}
	if mode == "on" {
		if t2 := addHTTPProxyListener(http); t2 != http {
			files = append(files, streamSwitchFile{"__H__", sha256Hex(http), t2})
		}
	}
	// render with placeholder paths (validated as absolute paths), then point them into $T
	for i := range files {
		files[i].path = map[string]string{"__R__": "/sppR/heyearth.conf", "__H__": "/sppH/heyearth.conf"}[files[i].path]
	}
	script, err := renderStreamProxySwitchScript(mode, start, sppDomain, "yandex.ru", files)
	if err != nil {
		t.Fatal(err)
	}
	base := map[string]string{"API_DOWN": "0", "NGINX_t_FAIL": "0", "CURL_FAIL": "0", "VERIFY_FAIL": "0", "NO_MIRROR": "0",
		"STALE_RELOAD": "0", "WRONG_IP": "0", "CHANGE_DURING_PREFLIGHT": "0"}
	return assignments(mergeVars(base, vars)) + `export API_DOWN NGINX_t_FAIL CURL_FAIL VERIFY_FAIL NO_MIRROR STALE_RELOAD WRONG_IP CHANGE_DURING_PREFLIGHT
T=$(mktemp -d); export T; trap 'rm -rf "$T"' EXIT
` + sppHarnessStubs + `
cat > "$R" <<'SPP_R_EOF'
` + router + `SPP_R_EOF
cat > "$H" <<'SPP_H_EOF'
` + http + `SPP_H_EOF
cp "$R" "$T/r.orig"; cp "$H" "$T/h.orig"; echo "` + live + `" > "$T/live"
cat > "$T/switch.sh" <<'SPP_SWITCH_EOF'
` + script + `SPP_SWITCH_EOF
sed -i "s#/sppR/heyearth.conf#$R#g; s#/sppH/heyearth.conf#$H#g" "$T/switch.sh"
` + pre + `
bash "$T/switch.sh"; echo "STATE rc=$?"
cat "$T/switch.log"
echo "STATE result=$(grep -o 'RESULT: .*' "$T/switch.log" | tail -n 1)"
echo "STATE calls=$(tr '\n' ';' < "$T/calls")"
echo "STATE live=$(cat "$T/live")"
echo "STATE r_orig=$(cmp -s "$R" "$T/r.orig" && echo same || echo changed) h_orig=$(cmp -s "$H" "$T/h.orig" && echo same || echo changed)"
echo "STATE probe_ip=$(grep -o '^[0-9.]* - - .*heyearth-spp-' "$T/access.log" | tail -n 1 | cut -d' ' -f1)"
echo "ROUTER-BEGIN"; cat "$R"; echo "ROUTER-END"
`
}

func TestStreamProxySwitch(t *testing.T) {
	if !haveBash() {
		t.Skip("no working bash")
	}
	run := func(t *testing.T, start, mode string, vars map[string]string, pre string) string {
		t.Helper()
		out, exit := runBash(t, sppScenario(t, start, mode, vars, pre))
		if exit != 0 {
			t.Fatalf("harness exit %d:\n%s", exit, out)
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
	const preflight = "curl api;curl 127.0.0.1;openssl;"
	t.Run("on: the current mode is checked through :443, then one nginx -t + reload, then the target is verified incl. the real client IP", func(t *testing.T) {
		t.Parallel()
		out := run(t, "off", "on", nil, "")
		want(t, out, "STATE rc=0", "STATE result=RESULT: on",
			"STATE calls="+preflight+"nginx -t;reload nginx;curl api;curl 127.0.0.1;openssl;curl 203.0.113.225;",
			"STATE live=1 8446 10444 1", "STATE r_orig=changed h_orig=changed", "STATE probe_ip=203.0.113.225")
		if r := router(out); r != buildNginxStreamConf(sppDomain, true) {
			t.Errorf("router after on:\n%s", r)
		}
	})
	t.Run("off: same single reload; the http PROXY listener stays", func(t *testing.T) {
		t.Parallel()
		out := run(t, "on", "off", nil, "")
		want(t, out, "STATE rc=0", "STATE result=RESULT: off",
			"STATE calls="+preflight+"nginx -t;reload nginx;curl api;curl 127.0.0.1;openssl;curl 203.0.113.225;",
			"STATE live=0 8443 10443 1", "STATE r_orig=changed h_orig=same")
		if r := router(out); r != buildNginxStreamConf(sppDomain, false) {
			t.Errorf("router after off:\n%s", r)
		}
	})
	t.Run("already on: nothing written, no reload, verified", func(t *testing.T) {
		t.Parallel()
		out := run(t, "on", "on", nil, "")
		want(t, out, "STATE rc=0", "STATE result=RESULT: on", "STATE r_orig=same h_orig=same",
			"STATE calls="+preflight+"curl api;curl 127.0.0.1;openssl;curl 203.0.113.225;")
	})
	t.Run("a file changed after it was read: nothing changed", func(t *testing.T) {
		t.Parallel()
		out := run(t, "off", "on", nil, `echo "# edited" >> "$R"; cp "$R" "$T/r.orig"`)
		want(t, out, "STATE rc=1", "STATE result=RESULT: FAILED", "STATE calls=", "STATE r_orig=same h_orig=same")
	})
	t.Run("the current mode does not work through :443: not switching", func(t *testing.T) {
		t.Parallel()
		out := run(t, "off", "on", map[string]string{"CURL_FAIL": "1"}, "")
		want(t, out, "STATE rc=1", "STATE result=RESULT: FAILED", "STATE r_orig=same h_orig=same", "STATE live=0 8443 10443 0")
		if strings.Contains(out, "reload nginx;") {
			t.Error("must not reload")
		}
	})
	t.Run("the API does not answer: not switching", func(t *testing.T) {
		t.Parallel()
		out := run(t, "off", "on", map[string]string{"API_DOWN": "1"}, "")
		want(t, out, "STATE rc=1", "STATE result=RESULT: FAILED", "STATE r_orig=same h_orig=same")
	})
	t.Run("on without the xray PROXY inbound (old API): not switching", func(t *testing.T) {
		t.Parallel()
		out := run(t, "off", "on", map[string]string{"NO_MIRROR": "1"}, "")
		want(t, out, "STATE rc=1", "STATE result=RESULT: FAILED", "STATE calls=", "STATE r_orig=same h_orig=same")
	})
	t.Run("nginx -t rejects the target: files restored, nginx never reloaded", func(t *testing.T) {
		t.Parallel()
		out := run(t, "off", "on", map[string]string{"NGINX_t_FAIL": "1"}, "")
		want(t, out, "STATE rc=1", "STATE result=RESULT: rolled-back-to-off", "STATE r_orig=same h_orig=same", "STATE live=0 8443 10443 0",
			"STATE calls="+preflight+"nginx -t;")
	})
	t.Run("on: nginx logs the probe with another address than the Hub's: not accepted, rolled back", func(t *testing.T) {
		t.Parallel()
		out := run(t, "off", "on", map[string]string{"WRONG_IP": "1"}, "")
		want(t, out, "STATE rc=1", "STATE result=RESULT: rolled-back-to-off", "STATE r_orig=same h_orig=same", "STATE live=0 8443 10443 0")
	})
	t.Run("off whose reload did not take effect (probe still logged with the real address): rolled back to on", func(t *testing.T) {
		t.Parallel()
		out := run(t, "on", "off", map[string]string{"STALE_RELOAD": "1"}, "")
		want(t, out, "STATE rc=1", "STATE result=RESULT: rolled-back-to-on", "STATE r_orig=same h_orig=same")
	})
	t.Run("a file edited during the preflight: re-checked right before writing, nothing written", func(t *testing.T) {
		t.Parallel()
		out := run(t, "off", "on", map[string]string{"CHANGE_DURING_PREFLIGHT": "1"}, "")
		want(t, out, "STATE rc=1", "STATE result=RESULT: rolled-back-to-off", "STATE r_orig=changed h_orig=same", "STATE live=0 8443 10443 0")
		if strings.Contains(out, "nginx -t;") || strings.Contains(router(out), "8446") || !strings.Contains(router(out), "# edited meanwhile") {
			t.Errorf("the edited file must be left as it is, nothing tested or reloaded:\n%s", out)
		}
	})
	t.Run("the target fails verification: previous files restored, reloaded and verified", func(t *testing.T) {
		t.Parallel()
		out := run(t, "off", "on", map[string]string{"VERIFY_FAIL": "1"}, "")
		want(t, out, "STATE rc=1", "STATE result=RESULT: rolled-back-to-off", "STATE r_orig=same h_orig=same", "STATE live=0 8443 10443 0",
			"STATE calls="+preflight+"nginx -t;reload nginx;curl api;curl 127.0.0.1;nginx -t;reload nginx;curl api;curl 127.0.0.1;openssl;curl 203.0.113.225;")
	})
}

func TestRenderStreamProxySwitchScript(t *testing.T) {
	files := []streamSwitchFile{{"/etc/nginx/stream.d/heyearth.conf", sha256Hex("a"), "b"}}
	for _, mode := range []string{"on", "off"} {
		s, err := renderStreamProxySwitchScript(mode, map[string]string{"on": "off", "off": "on"}[mode], sppDomain, "yandex.ru", files)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(s, "__") {
			t.Errorf("%s: placeholder left", mode)
		}
		if strings.Contains(s, "awk") {
			t.Errorf("%s: the switch must not depend on awk (mawk on the Hub)", mode)
		}
		if haveBash() {
			if out, code := runBashStdin(t, s, "-n"); code != 0 {
				t.Errorf("%s: bash -n failed (%d): %s", mode, code, out)
			}
		}
	}
	for _, bad := range []struct{ mode, prev, domain, sni, path string }{
		{"maybe", "off", sppDomain, "yandex.ru", "/a"}, {"on", "x", sppDomain, "yandex.ru", "/a"}, {"on", "off", "hiearth.ru;reboot", "yandex.ru", "/a"},
		{"on", "off", sppDomain, "$(id)", "/a"}, {"on", "off", sppDomain, "yandex.ru", "/a b"}, {"on", "off", sppDomain, "yandex.ru", "relative"},
	} {
		if _, err := renderStreamProxySwitchScript(bad.mode, bad.prev, bad.domain, bad.sni, []streamSwitchFile{{bad.path, "x", "y"}}); err == nil {
			t.Errorf("%+v must be refused", bad)
		}
	}
	if r := streamProxyResult("x\n2026 [stream-proxy] RESULT: rolled-back-to-off\n"); r != "rolled-back-to-off" {
		t.Errorf("result %q", r)
	}
}

// After an SSH drop the poller continues on a NEW connection and hands that one back, so the
// external check after a successful switch does not run on the dead client.
func TestPollStreamSwitchReconnect(t *testing.T) {
	dead := func(string) (string, error) { return "", errors.New("connection lost") }
	fresh := func(cmd string) (string, error) {
		if cmd == "whoami" {
			return "fresh", nil
		}
		return "2026 [stream-proxy] nginx reloaded\n2026 [stream-proxy] RESULT: on\n", nil
	}
	redials := 0
	var printed strings.Builder
	result, run := pollStreamSwitch(dead, func() (remoteRun, error) { redials++; return fresh, nil }, 0, time.Minute, func() {}, func(s string) { printed.WriteString(s) })
	if result != "on" || redials != 1 {
		t.Fatalf("result %q after %d redials", result, redials)
	}
	if who, _ := run("whoami"); who != "fresh" {
		t.Error("the poller must return the reconnected runner")
	}
	if !strings.Contains(printed.String(), "переподключаюсь") {
		t.Error("the reconnect is not reported")
	}
}

// The runbook names what the code uses and the procedures the review asked for.
func TestStreamProxyRunbook(t *testing.T) {
	b, err := os.ReadFile("../../../../docs/runbooks/stream-proxy-protocol.md")
	if err != nil {
		t.Fatal(err)
	}
	doc := string(b)
	for _, w := range []string{"go run ./cmd/deploy --stream-proxy-protocol on", "go run ./cmd/deploy --stream-proxy-protocol off",
		"127.0.0.1:" + nginxProxyProtocolPort, "127.0.0.1:" + xrayRealityPPPort, xrayRealityPPTag, streamProxyUnit, streamProxyLog,
		"152-ФЗ", "logrotate", "противоположную"} {
		if !strings.Contains(doc, w) {
			t.Errorf("stream-proxy-protocol runbook lacks %q", w)
		}
	}
	if strings.Contains(doc, "stream_proxy_protocol") {
		t.Error("the runbook still mentions the removed app setting")
	}
}

// doNginx keeps the live mode and refuses to touch nginx when it cannot determine it.
func TestStreamModeForDeploy(t *testing.T) {
	b64 := func(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }
	if pp, err := streamModeForDeploy("ABSENT\n", nil); err != nil || pp {
		t.Errorf("no router file yet: %v %v", pp, err)
	}
	if pp, err := streamModeForDeploy(b64(buildNginxStreamConf(sppDomain, true)), nil); err != nil || !pp {
		t.Errorf("live on: %v %v", pp, err)
	}
	if pp, err := streamModeForDeploy(b64(buildNginxStreamConf(sppDomain, false)), nil); err != nil || pp {
		t.Errorf("live off: %v %v", pp, err)
	}
	half := strings.Replace(buildNginxStreamConf(sppDomain, true), "127.0.0.1:10444", "127.0.0.1:10443", 1)
	for name, c := range map[string]struct {
		out string
		err error
	}{
		"read failed":       {"", errors.New("connection lost")},
		"not base64":        {"%%%", nil},
		"half switched":     {b64(half), nil},
		"empty router file": {b64(""), nil},
	} {
		if _, err := streamModeForDeploy(c.out, c.err); err == nil {
			t.Errorf("%s: doNginx must not touch nginx", name)
		}
	}
}
