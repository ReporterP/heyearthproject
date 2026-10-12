package main

// Hub hygiene steps of `deploy --setup-ops`: journald size cap, fail2ban sshd jail and the nginx
// stream proxy_timeout on the live file. The xray API guard lives in ops_xray_guard.go.

import (
	"fmt"
	"strings"

	"golang.org/x/crypto/ssh"
)

// ───────────────────────── journald + fail2ban ─────────────────────────

const journaldConf = "# Installed by deploy --setup-ops (cmd/deploy/ops_hygiene.go)\n[Journal]\nSystemMaxUse=1G\n"

// renderFail2banJail is the sshd jail: 5 failures in 10 minutes -> 1 hour ban. The backup
// target (the exit the Hub copies backups to, and often the way the operator reaches the Hub)
// is in ignoreip so a typo can never lock the Hub away from it.
func renderFail2banJail(sshPort int, ignoreIP string) string {
	ignore := "127.0.0.1/8 ::1"
	if ignoreIP != "" {
		ignore += " " + ignoreIP
	}
	return fmt.Sprintf("# Installed by deploy --setup-ops (cmd/deploy/ops_hygiene.go)\n[sshd]\nenabled = true\nbackend = systemd\nport = %d\nmaxretry = 5\nfindtime = 10m\nbantime = 1h\nignoreip = %s\n", sshPort, ignore)
}

// renderHostHygieneScript caps journald (restart only when the drop-in changed, vacuum every
// time), installs fail2ban if missing (apt failure aborts: no half-configured jail) and writes
// the sshd jail. The jail must answer `fail2ban-client status sshd` afterwards.
func renderHostHygieneScript(sshPort int, ignoreIP string) string {
	return `set -eu
` + opsPutFileFunc + putFileCalls([]opsFile{{"/etc/systemd/journald.conf.d/heyearth.conf", "644", journaldConf}}) + `if [ "$PUT_CHANGED" = 1 ]; then systemctl restart systemd-journald; fi
journalctl --vacuum-size=1G >/dev/null 2>&1 || true
echo "[ops] journald: $(journalctl --disk-usage)"
if ! command -v fail2ban-client >/dev/null 2>&1; then
  timeout 300 apt-get update -qq || true
  DEBIAN_FRONTEND=noninteractive timeout 600 apt-get install -y -qq fail2ban python3-systemd
fi
PUT_CHANGED=0
` + putFileCalls([]opsFile{{"/etc/fail2ban/jail.d/heyearth-sshd.conf", "644", renderFail2banJail(sshPort, ignoreIP)}}) + `systemctl enable fail2ban >/dev/null 2>&1
if [ "$PUT_CHANGED" = 1 ] || ! systemctl is-active --quiet fail2ban; then systemctl restart fail2ban; fi
ok=0
for i in 1 2 3 4 5 6 7 8 9 10; do
  if fail2ban-client status sshd; then ok=1; break; fi
  sleep 1
done
[ "$ok" = 1 ] || { echo "[ops] FAILED: fail2ban jail sshd is not running"; exit 1; }
`
}

func setupJournaldFail2ban(client *ssh.Client, sshPort int, ignoreIP string) error {
	return runOpsScript(client, "journald + fail2ban (setup-ops)", renderHostHygieneScript(sshPort, ignoreIP))
}

// ───────────────────────── nginx stream proxy_timeout ─────────────────────────

// nginxStreamTimeoutComment and nginxStreamTimeoutValue are the single source for both the
// generator (buildNginxStreamConf, used on reinstall) and the patch of the live file below;
// TestNginxStreamPatchEqualsGenerator keeps the two byte-identical.
const (
	nginxStreamTimeoutComment = "# = xray connIdle 3600s; default 10m cut idle long-lived sessions behind the SNI router."
	nginxStreamTimeoutValue   = "3600s"
)

// nginxStreamPatchScript adds `proxy_timeout 3600s;` above `proxy_pass $backend;` (the SNI
// router) in every nginx config file that has that line and no proxy_timeout yet. The files are
// found by content under /etc/nginx (the layout differs: stream.d/heyearth.conf from this
// generator, stream-enabled/heyearth on hosts set up by hand). Originals are copied to
// /var/backups/heyearth-nginx — never into /etc/nginx, where a copy would be included twice by
// a `stream-enabled/*` glob. nginx -t must pass before the reload; if it does not, every
// patched file is restored and the script fails.
//
// NGINX_DIR / NGINX_BACKUP_DIR are unit-test hooks, unset in production.
const nginxStreamPatchScript = `set -eu
NGINX_DIR="${NGINX_DIR:-/etc/nginx}"
BK="${NGINX_BACKUP_DIR:-/var/backups/heyearth-nginx}"
files=$(grep -rlE --exclude='*.bak' --exclude='*~' --exclude='*.orig' --exclude='*.dpkg-*' '^[[:space:]]*proxy_pass[[:space:]]+\$backend;' "$NGINX_DIR" 2>/dev/null || true)
if [ -z "$files" ]; then echo "[ops] nginx stream: no config with 'proxy_pass \$backend;' under $NGINX_DIR — nothing to patch"; exit 0; fi
mkdir -p "$BK"; chmod 700 "$BK"
stamp=$(date +%Y%m%d-%H%M%S)
bakname() { printf '%s/%s.%s' "$BK" "$(printf '%s' "$1" | tr '/' '_')" "$stamp"; }
patched=""
for f in $files; do
  if grep -Eq '^[[:space:]]*proxy_timeout[[:space:]]' "$f"; then echo "[ops] nginx stream: proxy_timeout already set in $f"; continue; fi
  bak=$(bakname "$f")
  cp -p "$f" "$bak"
  awk '/^[[:space:]]*proxy_pass[[:space:]]+\$backend;/ {
    match($0, /^[[:space:]]*/); ind = substr($0, 1, RLENGTH)
    print ind "__COMMENT__"
    print ind "proxy_timeout __VALUE__;"
  }
  { print }' "$bak" > "$bak.new"
  if [ ! -s "$bak.new" ] || ! grep -q proxy_timeout "$bak.new"; then rm -f "$bak.new"; echo "[ops] FAILED: the patch of $f produced no proxy_timeout, file left as is"; exit 1; fi
  cat "$bak.new" > "$f"; rm -f "$bak.new"
  patched="$patched $f"
  echo "[ops] nginx stream: proxy_timeout added to $f (original: $bak)"
done
if [ -n "$patched" ]; then
  if nginx -t; then
    systemctl reload nginx
  else
    for f in $patched; do cat "$(bakname "$f")" > "$f"; done
    echo "[ops] FAILED: nginx -t rejected the patched config, originals restored"
    exit 1
  fi
fi
grep -Hn proxy_timeout $files || true
`

func renderNginxStreamPatchScript() string {
	return strings.NewReplacer("__COMMENT__", nginxStreamTimeoutComment, "__VALUE__", nginxStreamTimeoutValue).Replace(nginxStreamPatchScript)
}

func patchNginxStreamTimeout(client *ssh.Client) error {
	return runOpsScript(client, "nginx stream proxy_timeout (setup-ops)", renderNginxStreamPatchScript())
}
