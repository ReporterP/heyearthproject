package main

import (
	"fmt"
	"strings"
	"testing"
)

// ───────────────────────── nginx stream timeouts + limits ─────────────────────────

// oldNginxStreamConf is what buildNginxStreamConf produced before the timeout / keepalive change
// — i.e. what is on the Hub today.
func oldNginxStreamConf(t *testing.T, domain string) string {
	t.Helper()
	cur := buildNginxStreamConf(domain)
	block := "    " + nginxStreamTimeoutComment + "\n    proxy_timeout " + nginxStreamTimeoutValue + ";\n"
	old := strings.ReplaceAll(strings.Replace(cur, block, "", 1), " so_keepalive="+nginxClientKeepalive+";", ";")
	if old == cur || strings.Contains(old, "proxy_timeout") || strings.Contains(old, "keepalive") {
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
	// client-facing keepalive on both listeners; proxy_socket_keepalive would only cover the loopback upstream
	for _, l := range []string{"    listen 443 so_keepalive=60s:10s:3;\n", "    listen [::]:443 so_keepalive=60s:10s:3;\n"} {
		if !strings.Contains(conf, l) {
			t.Errorf("the generator lacks %q:\n%s", l, conf)
		}
	}
	if strings.Contains(conf, "proxy_socket_keepalive") {
		t.Errorf("proxy_socket_keepalive must be gone from the generator:\n%s", conf)
	}
	if nginxClientKeepalive != "60s:10s:3" {
		t.Errorf("keepalive %s", nginxClientKeepalive)
	}
	if nginxStreamTimeoutValue != "3600s" {
		t.Fatalf("proxy_timeout must equal xray connIdle (3600s), got %s", nginxStreamTimeoutValue)
	}
}

// Ubuntu's stock nginx.conf plus the stream include doNginx appends, and what the patch makes of it.
const (
	stockNginxConf = "user www-data;\nworker_processes auto;\npid /run/nginx.pid;\nerror_log /var/log/nginx/error.log;\ninclude /etc/nginx/modules-enabled/*.conf;\n\n" +
		"events {\n\tworker_connections 768;\n\t# multi_accept on;\n}\n\nhttp {\n\tsendfile on;\n\tinclude /etc/nginx/conf.d/*.conf;\n}\nstream { include /etc/nginx/stream.d/*.conf; }\n"
	patchedNginxConf = "user www-data;\nworker_processes auto;\npid /run/nginx.pid;\nerror_log /var/log/nginx/error.log;\ninclude /etc/nginx/modules-enabled/*.conf;\n\n" +
		"worker_rlimit_nofile 65535;\nevents {\n\tworker_connections 8192;\n\t# multi_accept on;\n}\n\nhttp {\n\tsendfile on;\n\tinclude /etc/nginx/conf.d/*.conf;\n}\nstream { include /etc/nginx/stream.d/*.conf; }\n"
)

// nginx -T is emulated: it dumps, under "# configuration file <path>:" headers, exactly the
// files listed in $T/included (the files nginx reads) — never stale copies lying around.
const nginxHarnessStubs = `
nginx() {
  case "$1" in
    -T) if [ "$NGINX_T_BROKEN" = 1 ]; then echo "nginx: [emerg] stub broken config" >&2; return 1; fi
        echo "nginx: configuration file test is successful" >&2
        for f in $(cat "$T/included"); do echo "# configuration file $f:"; cat "$f"; done ;;
    -t) echo "nginx -t" >> "$T/calls"; if [ "$NGINX_T_FAIL" = 1 ]; then echo "nginx: [emerg] stub config error" >&2; return 1; fi ;;
    *) echo "STUB-BAD-NGINX $*" >&2; return 99 ;;
  esac
}
systemctl() { echo "systemctl $*" >> "$T/calls"; }
sleep() { :; }
`

type nginxFile struct {
	path, content string
	included      bool // read by nginx (listed in nginx -T)
}

// nginxHarness lays the files out under $T/nginx, runs the patch script in a subshell against
// the stubs, then prints the state and the files named in show (paths relative to $T/nginx).
func nginxHarness(tFail, tBroken bool, files []nginxFile, show ...string) string {
	b := func(v bool) string {
		if v {
			return "1"
		}
		return "0"
	}
	var sb strings.Builder
	sb.WriteString(assignments(map[string]string{"NGINX_T_FAIL": b(tFail), "NGINX_T_BROKEN": b(tBroken)}))
	sb.WriteString(`
T=$(mktemp -d); trap 'rm -rf "$T"' EXIT
mkdir -p "$T/nginx"; : > "$T/included"; : > "$T/calls"
export NGINX_BACKUP_DIR="$T/bk"
` + nginxHarnessStubs)
	for _, f := range files {
		fmt.Fprintf(&sb, "mkdir -p \"$T/nginx/$(dirname %[1]s)\"\ncat > \"$T/nginx/%[1]s\" <<'CONF_EOF'\n%[2]sCONF_EOF\n", f.path, f.content)
		if f.included {
			fmt.Fprintf(&sb, "echo \"$T/nginx/%s\" >> \"$T/included\"\n", f.path)
		}
	}
	sb.WriteString("cp -r \"$T/nginx\" \"$T/nginx.orig\"\n")
	sb.WriteString(`
cat > "$T/patch.sh" <<'PATCH_SCRIPT_EOF'
` + renderNginxStreamPatchScript() + `PATCH_SCRIPT_EOF
( . "$T/patch.sh" )
rc=$?
echo "STATE exit=$rc"
echo "STATE calls=$(tr '\n' ';' < "$T/calls")"
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

func TestNginxStreamPatchScript(t *testing.T) {
	if !haveBash() {
		t.Skip("no working bash")
	}
	old := oldNginxStreamConf(t, "hiearth.ru")
	cur := buildNginxStreamConf("hiearth.ru")
	run := func(t *testing.T, tFail, tBroken bool, files []nginxFile, show ...string) string {
		t.Helper()
		out, _ := runBash(t, nginxHarness(tFail, tBroken, files, show...))
		return out
	}
	wantLines := func(t *testing.T, out string, lines ...string) {
		t.Helper()
		for _, l := range lines {
			if !hasLine(out, l) {
				t.Errorf("missing line %q in:\n%s", l, out)
			}
		}
	}
	const (
		router = "stream.d/heyearth.conf"
		bakNot = "stream.d/heyearth.conf.bak"
		unused = "unused/old.conf"
	)

	t.Run("the live files of the old generator become what the new generator and the stock nginx.conf patch give; stale and unread copies stay untouched", func(t *testing.T) {
		t.Parallel()
		out := run(t, false, false, []nginxFile{
			{"nginx.conf", stockNginxConf, true}, {router, old, true},
			{bakNot, old, false}, {unused, old, false},
		}, router, "nginx.conf", bakNot, unused)
		if got := shown(out, router); got != cur {
			t.Fatalf("router file differs from buildNginxStreamConf:\n--- got\n%s\n--- want\n%s\n--- output\n%s", got, cur, out)
		}
		if got := shown(out, "nginx.conf"); got != patchedNginxConf {
			t.Fatalf("nginx.conf:\n--- got\n%s\n--- want\n%s", got, patchedNginxConf)
		}
		if shown(out, bakNot) != old || shown(out, unused) != old {
			t.Errorf("files nginx does not read (stale .bak, unused copy) must not be touched:\n%s", out)
		}
		wantLines(t, out, "STATE exit=0", "STATE calls=nginx -t;systemctl reload nginx;", "STATE backups=2",
			"STATE tree=./nginx.conf ./stream.d/heyearth.conf ./stream.d/heyearth.conf.bak ./unused/old.conf ")
		for _, s := range []string{"proxy_timeout 3600s;", "listen 443 so_keepalive=60s:10s:3;", "listen [::]:443 so_keepalive=60s:10s:3;", "worker_rlimit_nofile 65535;", "worker_connections 8192;",
			"nginx: proxy_timeout 3600s, so_keepalive=60s:10s:3 on the SNI router listeners, worker_rlimit_nofile 65535, worker_connections 8192 are in the effective config"} {
			if !strings.Contains(out, s) {
				t.Errorf("output lacks %q:\n%s", s, out)
			}
		}
	})

	t.Run("a second run changes nothing and does not reload nginx", func(t *testing.T) {
		t.Parallel()
		out := run(t, false, false, []nginxFile{{"nginx.conf", patchedNginxConf, true}, {router, cur, true}}, router, "nginx.conf")
		if shown(out, router) != cur || shown(out, "nginx.conf") != patchedNginxConf {
			t.Fatalf("files changed:\n%s", out)
		}
		wantLines(t, out, "STATE exit=0", "STATE calls=", "STATE backups=0")
		if strings.Count(out, "already as wanted") != 2 {
			t.Errorf("both files must report 'already as wanted':\n%s", out)
		}
	})

	t.Run("nginx -t failing restores every patched file byte for byte and does not reload", func(t *testing.T) {
		t.Parallel()
		out := run(t, true, false, []nginxFile{{"nginx.conf", stockNginxConf, true}, {router, old, true}}, router, "nginx.conf")
		if shown(out, router) != old || shown(out, "nginx.conf") != stockNginxConf {
			t.Fatalf("originals not restored:\n%s", out)
		}
		wantLines(t, out, "STATE exit=1", "STATE calls=nginx -t;")
		if !strings.Contains(out, "originals restored") || strings.Contains(out, "reload nginx") {
			t.Errorf("wrong failure handling:\n%s", out)
		}
	})

	t.Run("values that differ are replaced, not duplicated; stale directives after proxy_pass are folded into the canonical block", func(t *testing.T) {
		t.Parallel()
		// a different so_keepalive is replaced, a missing one appended before the ';' (a trailing
		// comment survives), the stale proxy_socket_keepalive of an earlier version is dropped
		hand := "server {\n    listen 443 so_keepalive=on;\n    listen [::]:443 ipv6only=on;\n    listen 8444; # extra\n    ssl_preread on;\n    proxy_pass $backend;\n    proxy_timeout 10m;\n    proxy_socket_keepalive off;\n}\n"
		wantRouter := "server {\n    listen 443 so_keepalive=60s:10s:3;\n    listen [::]:443 ipv6only=on so_keepalive=60s:10s:3;\n    listen 8444 so_keepalive=60s:10s:3; # extra\n    ssl_preread on;\n    " + nginxStreamTimeoutComment + "\n    proxy_timeout 3600s;\n    proxy_pass $backend;\n}\n"
		conf := strings.Replace(strings.Replace(stockNginxConf, "worker_connections 768;", "worker_connections 100;", 1),
			"include /etc/nginx/modules-enabled/*.conf;\n", "include /etc/nginx/modules-enabled/*.conf;\nworker_rlimit_nofile 1024;\n", 1)
		out := run(t, false, false, []nginxFile{{"nginx.conf", conf, true}, {"stream.d/hand.conf", hand, true}}, "stream.d/hand.conf", "nginx.conf")
		if got := shown(out, "stream.d/hand.conf"); got != wantRouter {
			t.Fatalf("router:\n--- got\n%s\n--- want\n%s\n%s", got, wantRouter, out)
		}
		got := shown(out, "nginx.conf")
		if strings.Count(got, "worker_rlimit_nofile") != 1 || !strings.Contains(got, "worker_rlimit_nofile 65535;\n") ||
			strings.Count(got, "worker_connections") != 1 || !strings.Contains(got, "\tworker_connections 8192;\n") {
			t.Fatalf("nginx.conf:\n%s", got)
		}
		wantLines(t, out, "STATE exit=0", "STATE calls=nginx -t;systemctl reload nginx;")
	})

	t.Run("block-aware: every SNI-router server block is patched, other server blocks keep their own proxy_timeout", func(t *testing.T) {
		t.Parallel()
		other := "server {\n    listen 9000;\n    proxy_timeout 5s;\n    proxy_pass 127.0.0.1:9;\n}\n"
		two := "server {\n\tlisten 443;\n\tssl_preread on;\n\tproxy_pass $backend;\n}\n" + other +
			"server {\n    listen 8444;\n    ssl_preread on;\n    proxy_pass $backend;\n}\n"
		wantTwo := "server {\n\tlisten 443 so_keepalive=60s:10s:3;\n\tssl_preread on;\n\t" + nginxStreamTimeoutComment + "\n\tproxy_timeout 3600s;\n\tproxy_pass $backend;\n}\n" + other +
			"server {\n    listen 8444 so_keepalive=60s:10s:3;\n    ssl_preread on;\n    " + nginxStreamTimeoutComment + "\n    proxy_timeout 3600s;\n    proxy_pass $backend;\n}\n"
		out := run(t, false, false, []nginxFile{{"nginx.conf", stockNginxConf, true}, {"stream.d/two.conf", two, true}}, "stream.d/two.conf")
		got := shown(out, "stream.d/two.conf")
		if n := strings.Count(got, "proxy_timeout 3600s;"); n != 2 {
			t.Fatalf("want 2 patched router blocks, got %d:\n%s", n, got)
		}
		if !strings.Contains(got, other) {
			t.Errorf("a server block that is not the SNI router must be left alone:\n%s", got)
		}
		if got != wantTwo {
			t.Errorf("indentation / listen lines:\n--- got\n%s\n--- want\n%s", got, wantTwo)
		}
		if strings.Contains(got, "listen 9000 so_keepalive") {
			t.Errorf("a listen outside the SNI router must not get so_keepalive:\n%s", got)
		}
	})

	t.Run("an events block without worker_connections gets one, a missing worker_rlimit_nofile goes into the main context", func(t *testing.T) {
		t.Parallel()
		bare := "worker_processes auto;\nevents {\n\t# multi_accept on;\n}\nstream { include /etc/nginx/stream.d/*.conf; }\n"
		want := "worker_processes auto;\nworker_rlimit_nofile 65535;\nevents {\n    worker_connections 8192;\n\t# multi_accept on;\n}\nstream { include /etc/nginx/stream.d/*.conf; }\n"
		out := run(t, false, false, []nginxFile{{"nginx.conf", bare, true}, {router, old, true}}, "nginx.conf")
		if got := shown(out, "nginx.conf"); got != want {
			t.Fatalf("nginx.conf:\n--- got\n%s\n--- want\n%s", got, want)
		}
	})

	t.Run("the router inline in nginx.conf: one file patched twice, the backup keeps the true original", func(t *testing.T) {
		t.Parallel()
		inline := "events {\n\tworker_connections 768;\n}\nstream {\n\tserver {\n\t\tlisten 443;\n\t\tssl_preread on;\n\t\tproxy_pass $backend;\n\t}\n}\n"
		want := "worker_rlimit_nofile 65535;\nevents {\n\tworker_connections 8192;\n}\nstream {\n\tserver {\n\t\tlisten 443 so_keepalive=60s:10s:3;\n\t\tssl_preread on;\n\t\t" +
			nginxStreamTimeoutComment + "\n\t\tproxy_timeout 3600s;\n\t\tproxy_pass $backend;\n\t}\n}\n"
		out, _ := runBash(t, nginxHarness(false, false, []nginxFile{{"nginx.conf", inline, true}}, "nginx.conf")+`echo "STATE backup_is_original=$(cmp -s "$T"/bk/* "$T/nginx.orig/nginx.conf" && echo 1 || echo 0)"`+"\n")
		if got := shown(out, "nginx.conf"); got != want {
			t.Fatalf("nginx.conf:\n--- got\n%s\n--- want\n%s\n%s", got, want, out)
		}
		wantLines(t, out, "STATE exit=0", "STATE backups=1", "STATE backup_is_original=1")
	})

	t.Run("a config that nginx -T cannot dump aborts before anything is touched", func(t *testing.T) {
		t.Parallel()
		out := run(t, false, true, []nginxFile{{"nginx.conf", stockNginxConf, true}, {router, old, true}}, router, "nginx.conf")
		wantLines(t, out, "STATE exit=1", "STATE calls=", "STATE backups=0")
		if shown(out, router) != old || shown(out, "nginx.conf") != stockNginxConf || !strings.Contains(out, "nginx -T does not work") {
			t.Errorf("output:\n%s", out)
		}
	})

	t.Run("no SNI router among the files nginx reads is a note, not a failure, and nginx.conf is left alone", func(t *testing.T) {
		t.Parallel()
		out := run(t, false, false, []nginxFile{{"nginx.conf", stockNginxConf, true}, {unused, old, false}}, "nginx.conf")
		wantLines(t, out, "STATE exit=0", "STATE calls=", "STATE backups=0")
		if shown(out, "nginx.conf") != stockNginxConf || !strings.Contains(out, "nothing to patch") {
			t.Errorf("output:\n%s", out)
		}
	})

	t.Run("if the effective config still lacks a directive after the reload the run FAILS", func(t *testing.T) {
		t.Parallel()
		// 'server' and '{' on different lines: the block is not recognised, nothing can be inserted
		odd := "server\n{\n    listen 443;\n    proxy_pass $backend;\n}\n"
		out := run(t, false, false, []nginxFile{{"nginx.conf", stockNginxConf, true}, {"stream.d/odd.conf", odd, true}}, "stream.d/odd.conf")
		wantLines(t, out, "STATE exit=1")
		if !strings.Contains(out, "FAILED: the effective nginx config lacks 'proxy_timeout") ||
			!strings.Contains(out, "FAILED: the SNI router has listeners without so_keepalive=60s:10s:3:") || !strings.Contains(out, "odd.conf:     listen 443;") {
			t.Errorf("output:\n%s", out)
		}
		if strings.Contains(out, "are in the effective config") {
			t.Errorf("must not report success:\n%s", out)
		}
	})
}

// ───────────────────────── journald + fail2ban ─────────────────────────

func fail2banHarness(vars map[string]string, sshClient string) string {
	base := map[string]string{"NEVER_IGNORE": "", "DB_IPS": "", "DB_FAIL": "0"}
	return assignments(mergeVars(base, vars)) + `
export SSH_CLIENT=` + shQuote(sshClient) + `
hostname() { echo "STUB-HOSTNAME-CALLED" >&2; echo "138.16.181.225 10.0.0.2"; }
runuser() { if [ "$DB_FAIL" = 1 ]; then return 1; fi; printf '%s\n' "$DB_IPS"; }
SSHD_PORT=22
` + fail2banIgnoreScript + fail2banJailFunc + `echo "STATE ignore=$IGNORE"
render_jail
`
}

func TestFail2banIgnore(t *testing.T) {
	if !haveBash() {
		t.Skip("no working bash")
	}
	const exit1, exit2 = "89.124.89.126", "5.6.7.8"
	cases := []struct {
		name      string
		vars      map[string]string
		sshClient string
		want      string // exact ignore list
		wantOut   []string
	}{
		{
			name:      "loopback + the operator's current address — and nothing of the Hub's own addresses",
			vars:      map[string]string{"NEVER_IGNORE": exit1, "DB_IPS": exit1 + "\n" + exit2},
			sshClient: "203.0.113.9 51234 22",
			want:      "127.0.0.1/8 ::1 203.0.113.9",
			wantOut:   []string{"fail2ban: ignoring your current address 203.0.113.9"},
		},
		{
			name:      "the backup target is NEVER ignored, even when the operator comes through it",
			vars:      map[string]string{"NEVER_IGNORE": exit1, "DB_IPS": exit2},
			sshClient: exit1 + " 51234 22",
			want:      "127.0.0.1/8 ::1",
			wantOut:   []string{"NOT whitelisting " + exit1 + " — it is an exit node"},
		},
		{
			name:      "an exit node known only from the servers table is not ignored either",
			vars:      map[string]string{"NEVER_IGNORE": exit1, "DB_IPS": exit1 + "\n" + exit2 + "\n"},
			sshClient: exit2 + " 40000 22",
			want:      "127.0.0.1/8 ::1",
			wantOut:   []string{"NOT whitelisting " + exit2},
		},
		{
			name:      "the servers table unreadable: the known target is still excluded, other addresses are whitelisted",
			vars:      map[string]string{"NEVER_IGNORE": exit1, "DB_FAIL": "1"},
			sshClient: "198.51.100.4 1 22",
			want:      "127.0.0.1/8 ::1 198.51.100.4",
		},
		{
			name:      "an IPv6 operator address is whitelisted",
			sshClient: "2001:db8::77 5555 22",
			want:      "127.0.0.1/8 ::1 2001:db8::77",
		},
		{
			name:      "garbage in SSH_CLIENT is never copied into the config",
			sshClient: "$(reboot) 1 2",
			want:      "127.0.0.1/8 ::1",
		},
		{
			name:      "256 in an octet is not an address",
			sshClient: "1.2.3.256 1 22",
			want:      "127.0.0.1/8 ::1",
		},
		{
			name:      "no SSH_CLIENT (not an SSH session): loopback only",
			sshClient: "",
			want:      "127.0.0.1/8 ::1",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			out, exit := runBash(t, fail2banHarness(c.vars, c.sshClient))
			if exit != 0 {
				t.Fatalf("exit %d:\n%s", exit, out)
			}
			if !hasLine(out, "STATE ignore="+c.want) {
				t.Errorf("want ignore list %q:\n%s", c.want, out)
			}
			// the Hub's own addresses (hostname -I) are not consulted at all, and ignoreself is off
			if strings.Contains(out, "STUB-HOSTNAME-CALLED") || strings.Contains(out, "138.16.181.225") || strings.Contains(out, "10.0.0.2") {
				t.Errorf("the Hub's own addresses must not be whitelisted:\n%s", out)
			}
			for _, l := range []string{"ignoreip = " + c.want, "ignoreself = false", "[sshd]", "enabled = true", "backend = systemd", "port = 22", "maxretry = 5", "findtime = 10m", "bantime = 1h"} {
				if !hasLine(out, l) {
					t.Errorf("jail lacks %q:\n%s", l, out)
				}
			}
			for _, s := range c.wantOut {
				if !strings.Contains(out, s) {
					t.Errorf("output lacks %q:\n%s", s, out)
				}
			}
			jail := out[strings.Index(out, "# Installed by"):]
			for _, bad := range []string{exit1, exit2} {
				if strings.Contains(jail, bad) {
					t.Errorf("exit address %s must not be in the jail:\n%s", bad, jail)
				}
			}
		})
	}
}

func TestHostHygieneScript(t *testing.T) {
	s := renderHostHygieneScript(2222, "89.124.89.126")
	if got := payloadFrom(t, s, "/etc/systemd/journald.conf.d/heyearth.conf"); !strings.Contains(got, "[Journal]\nSystemMaxUse=1G\n") {
		t.Errorf("journald drop-in: %q", got)
	}
	for _, want := range []string{
		"SSHD_PORT=2222\n", "NEVER_IGNORE='89.124.89.126'\n", "put_file /etc/fail2ban/jail.d/heyearth-sshd.conf 644 \"$(render_jail | base64 -w0)\"",
		`if ! command -v fail2ban-client`, "apt-get install -y -qq fail2ban python3-systemd", // installed only when missing; failure aborts (set -e)
		`journalctl --vacuum-size=1G`, `if [ "$PUT_CHANGED" = 1 ]; then systemctl restart systemd-journald; fi`,
		`[ "$PUT_CHANGED" = 1 ] || ! systemctl is-active --quiet fail2ban`, "fail2ban-client status sshd", "jail sshd is not running",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("script lacks %q", want)
		}
	}
	// the Hub's own addresses are not whitelisted: no hostname -I, and fail2ban's ignoreself is off
	if strings.Contains(s, "hostname -I") || !strings.Contains(s, "ignoreself = false") {
		t.Errorf("the jail must set ignoreself = false and not whitelist hostname -I")
	}
	// the exit address only ever appears as the "never ignore" input, not in a literal ignoreip
	if strings.Contains(s, "ignoreip = 127.0.0.1/8 ::1 89.124.89.126") {
		t.Errorf("the backup target must not be hard-coded into ignoreip")
	}
	// restarts of journald / fail2ban are conditional: a re-run must not restart anything unchanged
	if strings.Contains(s, "\nsystemctl restart fail2ban") || strings.Contains(s, "\nsystemctl restart systemd-journald") {
		t.Errorf("unconditional restart found")
	}
}
