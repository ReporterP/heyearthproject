package main

import (
	"encoding/base64"
	"fmt"
	"regexp"
	"strings"
	"testing"
)

// ───────────────────────── nginx stream proxy_timeout ─────────────────────────

// oldNginxStreamConf is what buildNginxStreamConf produced before the proxy_timeout change —
// i.e. what is on the Hub today.
func oldNginxStreamConf(t *testing.T, domain string) string {
	t.Helper()
	cur := buildNginxStreamConf(domain)
	block := "    " + nginxStreamTimeoutComment + "\n    proxy_timeout " + nginxStreamTimeoutValue + ";\n"
	old := strings.Replace(cur, block, "", 1)
	if old == cur || strings.Contains(old, "proxy_timeout") {
		t.Fatalf("cannot derive the old config from the generator output:\n%s", cur)
	}
	return old
}

func TestNginxStreamGeneratorHasTimeout(t *testing.T) {
	conf := buildNginxStreamConf("hiearth.ru")
	want := "    ssl_preread on;\n    " + nginxStreamTimeoutComment + "\n    proxy_timeout 3600s;\n    proxy_pass $backend;\n"
	if !strings.Contains(conf, want) {
		t.Fatalf("the SNI router server block must carry proxy_timeout between ssl_preread and proxy_pass:\n%s", conf)
	}
	if nginxStreamTimeoutValue != "3600s" {
		t.Fatalf("proxy_timeout must equal xray connIdle (3600s), got %s", nginxStreamTimeoutValue)
	}
}

const nginxHarnessStubs = `
nginx() { echo "nginx $*" >> "$T/calls"; if [ "$NGINX_T_FAIL" = 1 ]; then echo "nginx: [emerg] stub config error" >&2; return 1; fi; }
systemctl() { echo "systemctl $*" >> "$T/calls"; }
`

// nginxHarness: setup creates files under $T/nginx; the patch script then runs in a subshell
// against NGINX_DIR=$T/nginx with backups in $T/bk.
func nginxHarness(tFail bool, setup string, show ...string) string {
	f := "0"
	if tFail {
		f = "1"
	}
	var sb strings.Builder
	sb.WriteString(assignments(map[string]string{"NGINX_T_FAIL": f}))
	sb.WriteString(`
T=$(mktemp -d); trap 'rm -rf "$T"' EXIT
mkdir -p "$T/nginx"
export NGINX_DIR="$T/nginx" NGINX_BACKUP_DIR="$T/bk"
` + nginxHarnessStubs + setup + `
cat > "$T/patch.sh" <<'PATCH_SCRIPT_EOF'
` + renderNginxStreamPatchScript() + `PATCH_SCRIPT_EOF
( . "$T/patch.sh" )
rc=$?
echo "STATE exit=$rc"
echo "STATE calls=$( [ -f "$T/calls" ] && tr '\n' ';' < "$T/calls" )"
echo "STATE tree=$(cd "$T/nginx" && find . -type f | sort | tr '\n' ' ')"
echo "STATE backups=$(ls "$T/bk" 2>/dev/null | wc -l | tr -d ' ')"
`)
	for _, f := range show {
		fmt.Fprintf(&sb, "echo '--- %[1]s'; cat \"$T/nginx/%[1]s\"; echo '--- end'\n", f)
	}
	return sb.String()
}

func shown(out, file string) string {
	_, rest, ok := strings.Cut(out, "--- "+file+"\n")
	if !ok {
		return ""
	}
	body, _, _ := strings.Cut(rest, "--- end\n")
	return body
}

func writeConf(path, content string) string {
	return fmt.Sprintf("mkdir -p \"$T/nginx/$(dirname %[1]s)\"\ncat > \"$T/nginx/%[1]s\" <<'CONF_EOF'\n%[2]sCONF_EOF\n", path, content)
}

func TestNginxStreamPatchScript(t *testing.T) {
	if !haveBash() {
		t.Skip("no working bash")
	}
	old := oldNginxStreamConf(t, "hiearth.ru")
	cur := buildNginxStreamConf("hiearth.ru")

	t.Run("the live file of the old generator becomes byte-identical to the new generator output", func(t *testing.T) {
		t.Parallel()
		out, _ := runBash(t, nginxHarness(false, writeConf("stream.d/heyearth.conf", old), "stream.d/heyearth.conf"))
		if got := shown(out, "stream.d/heyearth.conf"); got != cur {
			t.Fatalf("patched file differs from buildNginxStreamConf:\n--- got\n%s\n--- want\n%s\n--- full output\n%s", got, cur, out)
		}
		for _, l := range []string{"STATE exit=0", "STATE calls=nginx -t;systemctl reload nginx;", "STATE tree=./stream.d/heyearth.conf ", "STATE backups=1"} {
			if !hasLine(out, l) {
				t.Errorf("missing %q:\n%s", l, out)
			}
		}
		if !strings.Contains(out, "proxy_timeout 3600s;") || !strings.Contains(out, "proxy_timeout added to") {
			t.Errorf("the grep of the result / the change notice is missing:\n%s", out)
		}
	})

	t.Run("a second run changes nothing and does not reload nginx", func(t *testing.T) {
		t.Parallel()
		out, _ := runBash(t, nginxHarness(false, writeConf("stream.d/heyearth.conf", cur), "stream.d/heyearth.conf"))
		if got := shown(out, "stream.d/heyearth.conf"); got != cur {
			t.Fatalf("file changed:\n%s", got)
		}
		for _, l := range []string{"STATE exit=0", "STATE calls=", "STATE backups=0"} {
			if !hasLine(out, l) {
				t.Errorf("missing %q:\n%s", l, out)
			}
		}
		if !strings.Contains(out, "proxy_timeout already set in") {
			t.Errorf("no 'already set' notice:\n%s", out)
		}
	})

	t.Run("nginx -t failing restores the original and does not reload", func(t *testing.T) {
		t.Parallel()
		out, _ := runBash(t, nginxHarness(true, writeConf("stream.d/heyearth.conf", old), "stream.d/heyearth.conf"))
		if got := shown(out, "stream.d/heyearth.conf"); got != old {
			t.Fatalf("original not restored:\n%s", got)
		}
		for _, l := range []string{"STATE exit=1", "STATE calls=nginx -t;"} {
			if !hasLine(out, l) {
				t.Errorf("missing %q:\n%s", l, out)
			}
		}
		if !strings.Contains(out, "originals restored") || strings.Contains(out, "reload nginx") {
			t.Errorf("wrong failure handling:\n%s", out)
		}
	})

	t.Run("finds stream-enabled/heyearth (no .conf), skips backups and unrelated proxy_pass, keeps copies out of /etc/nginx", func(t *testing.T) {
		t.Parallel()
		decoy := "server {\n    location / {\n        proxy_pass http://127.0.0.1:8000;\n    }\n}\n"
		setup := writeConf("stream-enabled/heyearth", old) +
			writeConf("stream.d/heyearth.conf.bak", old) +
			writeConf("conf.d/site.conf", decoy)
		out, _ := runBash(t, nginxHarness(false, setup, "stream-enabled/heyearth", "stream.d/heyearth.conf.bak", "conf.d/site.conf"))
		if got := shown(out, "stream-enabled/heyearth"); got != cur {
			t.Fatalf("stream-enabled/heyearth not patched:\n%s", got)
		}
		if got := shown(out, "stream.d/heyearth.conf.bak"); got != old {
			t.Errorf("a .bak file must not be touched:\n%s", got)
		}
		if got := shown(out, "conf.d/site.conf"); got != decoy {
			t.Errorf("a config without 'proxy_pass $backend;' must not be touched:\n%s", got)
		}
		if !hasLine(out, "STATE tree=./conf.d/site.conf ./stream-enabled/heyearth ./stream.d/heyearth.conf.bak ") {
			t.Errorf("the original copy must not land inside the nginx directory (a stream-enabled/* glob would load it twice):\n%s", out)
		}
		if !hasLine(out, "STATE backups=1") || !hasLine(out, "STATE exit=0") {
			t.Errorf("state:\n%s", out)
		}
	})

	t.Run("every SNI-router server block is patched, indentation kept", func(t *testing.T) {
		t.Parallel()
		two := "server {\n\tlisten 443;\n\tssl_preread on;\n\tproxy_pass $backend;\n}\nserver {\n    listen 8444;\n    ssl_preread on;\n    proxy_pass $backend;\n}\n"
		out, _ := runBash(t, nginxHarness(false, writeConf("stream.d/two.conf", two), "stream.d/two.conf"))
		got := shown(out, "stream.d/two.conf")
		if n := strings.Count(got, "proxy_timeout 3600s;"); n != 2 {
			t.Fatalf("want 2 proxy_timeout lines, got %d:\n%s", n, got)
		}
		if !strings.Contains(got, "\t"+nginxStreamTimeoutComment+"\n\tproxy_timeout 3600s;\n\tproxy_pass $backend;") ||
			!strings.Contains(got, "    "+nginxStreamTimeoutComment+"\n    proxy_timeout 3600s;\n    proxy_pass $backend;") {
			t.Fatalf("indentation not kept:\n%s", got)
		}
	})

	t.Run("a different proxy_timeout set by hand is left alone", func(t *testing.T) {
		t.Parallel()
		hand := strings.Replace(old, "    proxy_pass $backend;", "    proxy_timeout 10m;\n    proxy_pass $backend;", 1)
		out, _ := runBash(t, nginxHarness(false, writeConf("stream.d/heyearth.conf", hand), "stream.d/heyearth.conf"))
		if got := shown(out, "stream.d/heyearth.conf"); got != hand {
			t.Fatalf("file changed:\n%s", got)
		}
		if !hasLine(out, "STATE calls=") {
			t.Errorf("must not test/reload nginx:\n%s", out)
		}
	})

	t.Run("no stream config at all is a note, not a failure", func(t *testing.T) {
		t.Parallel()
		out, _ := runBash(t, nginxHarness(false, writeConf("conf.d/site.conf", "server { listen 80; }\n")))
		if !hasLine(out, "STATE exit=0") || !hasLine(out, "STATE calls=") || !strings.Contains(out, "nothing to patch") {
			t.Fatalf("output:\n%s", out)
		}
	})
}

// ───────────────────────── journald + fail2ban ─────────────────────────

func payloadOf(t *testing.T, script, path string) string {
	t.Helper()
	i := strings.Index(script, "put_file "+path+" ")
	if i < 0 {
		t.Fatalf("no put_file for %s", path)
	}
	m := regexp.MustCompile(`'([A-Za-z0-9+/=]*)'`).FindStringSubmatch(script[i:])
	b, err := base64.StdEncoding.DecodeString(m[1])
	if err != nil {
		t.Fatalf("payload of %s: %v", path, err)
	}
	return string(b)
}

func TestHostHygieneScript(t *testing.T) {
	s := renderHostHygieneScript(2222, "89.124.89.126")
	if got := payloadOf(t, s, "/etc/systemd/journald.conf.d/heyearth.conf"); !strings.Contains(got, "[Journal]\nSystemMaxUse=1G\n") {
		t.Errorf("journald drop-in: %q", got)
	}
	jail := payloadOf(t, s, "/etc/fail2ban/jail.d/heyearth-sshd.conf")
	for _, want := range []string{"[sshd]\n", "enabled = true\n", "backend = systemd\n", "port = 2222\n", "maxretry = 5\n", "findtime = 10m\n", "bantime = 1h\n", "ignoreip = 127.0.0.1/8 ::1 89.124.89.126\n"} {
		if !strings.Contains(jail, want) {
			t.Errorf("jail lacks %q:\n%s", want, jail)
		}
	}
	if got := renderFail2banJail(22, ""); !strings.Contains(got, "ignoreip = 127.0.0.1/8 ::1\n") || strings.Contains(got, "ignoreip = 127.0.0.1/8 ::1 \n") {
		t.Errorf("jail without a backup target: %q", got)
	}
	for _, want := range []string{
		`if ! command -v fail2ban-client`, "apt-get install -y -qq fail2ban python3-systemd", // installed only when missing; failure aborts (set -e)
		`journalctl --vacuum-size=1G`, `if [ "$PUT_CHANGED" = 1 ]; then systemctl restart systemd-journald; fi`,
		`[ "$PUT_CHANGED" = 1 ] || ! systemctl is-active --quiet fail2ban`, "fail2ban-client status sshd", "jail sshd is not running",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("script lacks %q", want)
		}
	}
	// restarts of journald / fail2ban are conditional: a re-run must not restart anything unchanged
	if strings.Contains(s, "\nsystemctl restart fail2ban") || strings.Contains(s, "\nsystemctl restart systemd-journald") {
		t.Errorf("unconditional restart found")
	}
}
