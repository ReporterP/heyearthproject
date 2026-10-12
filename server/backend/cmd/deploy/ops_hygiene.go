package main

// Hub hygiene steps of `deploy --setup-ops`: journald size cap, fail2ban sshd jail and the nginx
// stream timeouts / connection limits on the live files. The xray API guard lives in
// ops_xray_guard.go.

import (
	"fmt"
	"strings"

	"golang.org/x/crypto/ssh"
)

// ───────────────────────── journald + fail2ban ─────────────────────────

const journaldConf = "# Installed by deploy --setup-ops (cmd/deploy/ops_hygiene.go)\n[Journal]\nSystemMaxUse=1G\n"

// fail2banIgnoreScript computes the jail's ignoreip at run time into IGNORE. It needs
// NEVER_IGNORE (space separated IPs, may be empty): the backup target.
//
// ignoreip = loopback + the address this very SSH session comes from (SSH_CLIENT), so a typo
// in a password can never lock the operator out. The Hub's own addresses are NOT whitelisted
// (the jail sets ignoreself = false): proxy users can no longer make the Hub connect to itself
// (the Hysteria2 ACL and the xray routing reject them, see cmd/api/hub_addrs.go), and an
// address that may be abused to brute-force sshd must not be exempt from the ban.
//
// An exit node is NEVER ignored, not even as the operator's address: its IP is the egress IP of
// every proxy user, so whitelisting it would let anybody brute-force the Hub's root password
// through our own service. The exit IPs are NEVER_IGNORE plus servers.ip_address from the DB
// (best effort); when the operator reaches the Hub through an exit, the jail simply does not
// whitelist that address.
const fail2banIgnoreScript = `is_ip() {
  local v="$1" o
  if [[ "$v" =~ ^[0-9]{1,3}\.[0-9]{1,3}\.[0-9]{1,3}\.[0-9]{1,3}$ ]]; then
    for o in ${v//./ }; do [ "$((10#$o))" -le 255 ] || return 1; done
    return 0
  fi
  [[ "$v" == *:* ]] && [[ "$v" =~ ^[0-9a-fA-F]{0,4}(:[0-9a-fA-F]{0,4}){2,7}$ ]]
}
EXITS="$NEVER_IGNORE $(runuser -u postgres -- psql -d heyearth -tAc "SELECT ip_address FROM servers" 2>/dev/null || true)"
IGNORE="127.0.0.1/8 ::1"
op="${SSH_CLIENT:-}"; op="${op%% *}"
if [ -n "$op" ] && is_ip "$op"; then
  skip=0; for e in $EXITS; do if [ "$e" = "$op" ]; then skip=1; fi; done
  if [ "$skip" = 1 ]; then echo "[ops] fail2ban: NOT whitelisting $op — it is an exit node (the egress IP of all proxy users)"
  else IGNORE="$IGNORE $op"; echo "[ops] fail2ban: ignoring your current address $op"; fi
fi
`

// fail2banJailFunc renders the jail text from SSHD_PORT and IGNORE: 5 failures in 10 minutes
// -> 1 hour ban.
const fail2banJailFunc = `render_jail() {
  printf '# Installed by deploy --setup-ops (cmd/deploy/ops_hygiene.go)\n[sshd]\nenabled = true\nbackend = systemd\nport = %s\nignoreself = false\nmaxretry = 5\nfindtime = 10m\nbantime = 1h\nignoreip = %s\n' "$SSHD_PORT" "$IGNORE"
}
`

// renderHostHygieneScript caps journald (restart only when the drop-in changed, vacuum every
// time), installs fail2ban if missing (apt failure aborts: no half-configured jail) and writes
// the sshd jail. The jail must answer `fail2ban-client status sshd` afterwards. neverIgnore is
// the backup target (an exit): it is never whitelisted.
func renderHostHygieneScript(sshPort int, neverIgnore string) string {
	return `set -eu
` + opsPutFileFunc + putFileCalls([]opsFile{{"/etc/systemd/journald.conf.d/heyearth.conf", "644", journaldConf}}) + `if [ "$PUT_CHANGED" = 1 ]; then systemctl restart systemd-journald; fi
journalctl --vacuum-size=1G >/dev/null 2>&1 || true
echo "[ops] journald: $(journalctl --disk-usage)"
if ! command -v fail2ban-client >/dev/null 2>&1; then
  timeout 300 apt-get update -qq || true
  DEBIAN_FRONTEND=noninteractive timeout 600 apt-get install -y -qq fail2ban python3-systemd
fi
SSHD_PORT=` + fmt.Sprintf("%d", sshPort) + `
NEVER_IGNORE='` + neverIgnore + `'
` + fail2banIgnoreScript + fail2banJailFunc + `PUT_CHANGED=0
put_file /etc/fail2ban/jail.d/heyearth-sshd.conf 644 "$(render_jail | base64 -w0)"
systemctl enable fail2ban >/dev/null 2>&1
if [ "$PUT_CHANGED" = 1 ] || ! systemctl is-active --quiet fail2ban; then systemctl restart fail2ban; fi
ok=0
for i in 1 2 3 4 5 6 7 8 9 10; do
  if fail2ban-client status sshd; then ok=1; break; fi
  sleep 1
done
[ "$ok" = 1 ] || { echo "[ops] FAILED: fail2ban jail sshd is not running"; exit 1; }
`
}

func setupJournaldFail2ban(client *ssh.Client, sshPort int, neverIgnore string) error {
	return runOpsScript(client, "journald + fail2ban (setup-ops)", renderHostHygieneScript(sshPort, neverIgnore))
}

// ───────────────────────── nginx stream timeouts + limits ─────────────────────────

// These are the single source for both the generator (buildNginxStreamConf, used on reinstall)
// and the patch of the live files below; TestNginxStreamPatchScript keeps the two byte-identical.
// proxy_timeout 3600s equals xray's connIdle. so_keepalive on the CLIENT-facing listen sockets
// (idle 60 s, probe every 10 s, 3 probes ≈ 90 s) makes the kernel notice vanished clients — with
// an hour of allowed silence a dead phone would otherwise hold its session and file descriptors
// for the full hour (proxy_socket_keepalive would only cover the loopback upstream, which is
// useless here). The worker limits leave room for the long-lived sessions (2 descriptors per
// proxied connection).
const (
	nginxStreamTimeoutComment = "# = xray connIdle 3600s; default 10m cut idle long-lived sessions behind the SNI router."
	nginxStreamTimeoutValue   = "3600s"
	nginxClientKeepalive      = "60s:10s:3"
	nginxWorkerRlimitNofile   = "65535"
	nginxWorkerConnections    = "8192"
)

// nginxStreamPatchScript brings the live nginx config to the wanted state:
//
//   - in every file that holds the SNI router (`proxy_pass $backend;`), inside that server
//     block: `proxy_timeout 3600s;` above the proxy_pass and `so_keepalive=60s:10s:3` on every
//     `listen` (a different proxy_timeout / so_keepalive is replaced; a stale
//     proxy_socket_keepalive of an earlier version of this tool is dropped);
//   - in the file with `events {`: `worker_rlimit_nofile 65535;` in the main context and
//     `worker_connections 8192;` in events (absent -> added, different -> replaced).
//
// The files come from `nginx -T` (the "# configuration file <path>:" headers), i.e. exactly the
// files nginx reads — not from a search of the directory, which finds stale copies. A file is
// only written when its content changes; originals are copied to /var/backups/heyearth-nginx
// (never into /etc/nginx, where a copy would be included twice by a glob). nginx -t must pass
// before the reload; if it does not, every patched file is restored and the script fails. After
// the reload the effective config (`nginx -T` again) must contain proxy_timeout, the limits and
// so_keepalive on every listen of the SNI router, else the script FAILS.
//
// NGINX_BACKUP_DIR is a unit-test hook, unset in production.
const nginxStreamPatchScript = `set -u
BK="${NGINX_BACKUP_DIR:-/var/backups/heyearth-nginx}"
dump=$(nginx -T 2>/dev/null) || { echo "[ops] FAILED: nginx -T does not work (is the current nginx config valid?) — nothing patched"; exit 1; }
routers=$(printf '%s\n' "$dump" | awk '/^# configuration file /{f=$4; sub(/:$/,"",f); next} /^[[:space:]]*proxy_pass[[:space:]]+\$backend;/{print f}' | sort -u)
mains=$(printf '%s\n' "$dump" | awk '/^# configuration file /{f=$4; sub(/:$/,"",f); next} /^[[:space:]]*events[[:space:]]*\{/{print f}' | sort -u)
if [ -z "$routers" ]; then echo "[ops] nginx: no config read by nginx has 'proxy_pass \$backend;' — nothing to patch"; exit 0; fi
ROUTER_AWK='FNR == NR {
  code = $0; sub(/#.*/, "", code)
  if (!inb && code ~ /^[[:space:]]*server[[:space:]]*\{/) { inb = 1; bid++; depth = 0 }
  if (inb) {
    blk[FNR] = bid
    n = gsub(/\{/, "{", code); m = gsub(/\}/, "}", code); depth += n - m
    if ($0 ~ /^[[:space:]]*proxy_pass[[:space:]]+\$backend;/) router[bid] = 1
    if (depth <= 0) inb = 0
  }
  next
}
{
  b = blk[FNR]
  if (b != "" && router[b]) {
    if ($0 ~ /^[[:space:]]*proxy_timeout[[:space:]]/ || $0 ~ /^[[:space:]]*proxy_socket_keepalive[[:space:]]/ || index($0, "__COMMENT__") > 0) next
    if ($0 ~ /^[[:space:]]*listen[[:space:]]/) {
      line = $0
      if (match(line, /so_keepalive=[^ ;]*/)) line = substr(line, 1, RSTART - 1) "so_keepalive=__KA__" substr(line, RSTART + RLENGTH)
      else sub(/[[:space:]]*;/, " so_keepalive=__KA__;", line)
      print line
      next
    }
    if ($0 ~ /^[[:space:]]*proxy_pass[[:space:]]+\$backend;/) {
      match($0, /^[[:space:]]*/); ind = substr($0, 1, RLENGTH)
      print ind "__COMMENT__"
      print ind "proxy_timeout __VALUE__;"
    }
  }
  print
}'
MAIN_AWK='FNR == NR {
  code = $0; sub(/#.*/, "", code)
  if (depth == 0 && code ~ /^[[:space:]]*worker_rlimit_nofile[[:space:]]/) hasR = 1
  if (inev && depth == 1 && code ~ /^[[:space:]]*worker_connections[[:space:]]/) hasC = 1
  if (depth == 0 && code ~ /^[[:space:]]*events[[:space:]]*\{/) inev = 1
  n = gsub(/\{/, "{", code); m = gsub(/\}/, "}", code); depth += n - m
  if (depth == 0) inev = 0
  next
}
FNR == 1 { depth = 0; inev = 0 }
{
  line = $0; code = $0; sub(/#.*/, "", code); addC = 0
  match(line, /^[[:space:]]*/); ind = substr(line, 1, RLENGTH)
  if (depth == 0 && code ~ /^[[:space:]]*worker_rlimit_nofile[[:space:]]/) {
    val = code; sub(/^[[:space:]]*worker_rlimit_nofile[[:space:]]+/, "", val); sub(/[[:space:]]*;.*$/, "", val)
    if (val != "__RLIMIT__") line = ind "worker_rlimit_nofile __RLIMIT__;"
  }
  if (depth == 0 && code ~ /^[[:space:]]*events[[:space:]]*\{/) {
    if (!hasR) { print ind "worker_rlimit_nofile __RLIMIT__;"; hasR = 1 }
    inev = 1
    if (!hasC) { addC = 1; hasC = 1 }
  }
  if (inev && depth == 1 && code ~ /^[[:space:]]*worker_connections[[:space:]]/) {
    val = code; sub(/^[[:space:]]*worker_connections[[:space:]]+/, "", val); sub(/[[:space:]]*;.*$/, "", val)
    if (val != "__CONNS__") line = ind "worker_connections __CONNS__;"
  }
  print line
  if (addC) print ind "    worker_connections __CONNS__;"
  n = gsub(/\{/, "{", code); m = gsub(/\}/, "}", code); depth += n - m
  if (depth == 0) inev = 0
}'
mkdir -p "$BK"; chmod 700 "$BK"
stamp=$(date +%Y%m%d-%H%M%S)
bakname() { printf '%s/%s.%s' "$BK" "$(printf '%s' "$1" | tr '/' '_')" "$stamp"; }
changed=""
rollback() { for f in $changed; do cat "$(bakname "$f")" > "$f"; done; }
patch_file() {
  local f="$1" prog="$2" tmp
  tmp=$(mktemp "$BK/new.XXXXXX") || return 1
  if ! awk "$prog" "$f" "$f" > "$tmp"; then rm -f "$tmp"; echo "[ops] FAILED: awk failed on $f"; return 1; fi
  if [ ! -s "$tmp" ]; then rm -f "$tmp"; echo "[ops] FAILED: the patch of $f came out empty"; return 1; fi
  if cmp -s "$tmp" "$f"; then rm -f "$tmp"; echo "[ops] nginx: $f already as wanted"; return 0; fi
  if [ ! -f "$(bakname "$f")" ]; then cp -p "$f" "$(bakname "$f")" || { rm -f "$tmp"; return 1; }; fi
  changed="$changed $f"
  cat "$tmp" > "$f" || { rm -f "$tmp"; return 1; }
  rm -f "$tmp"
  echo "[ops] nginx: patched $f (original: $(bakname "$f"))"
}
for f in $routers; do patch_file "$f" "$ROUTER_AWK" || { rollback; echo "[ops] FAILED: originals restored"; exit 1; }; done
for f in $mains; do patch_file "$f" "$MAIN_AWK" || { rollback; echo "[ops] FAILED: originals restored"; exit 1; }; done
if [ -n "$changed" ]; then
  if ! nginx -t; then rollback; echo "[ops] FAILED: nginx -t rejected the patched config, originals restored"; exit 1; fi
  systemctl reload nginx || { echo "[ops] FAILED: systemctl reload nginx"; exit 1; }
  sleep 1
fi
final=$(nginx -T 2>/dev/null) || { echo "[ops] FAILED: nginx -T does not work after the patch"; exit 1; }
bad=0
for re in 'proxy_timeout[[:space:]]+__VALUE__;' 'worker_rlimit_nofile[[:space:]]+__RLIMIT__;' 'worker_connections[[:space:]]+__CONNS__;'; do
  if ! printf '%s\n' "$final" | grep -Eq "^[[:space:]]*$re"; then echo "[ops] FAILED: the effective nginx config lacks '$re'"; bad=1; fi
done
missing=$(printf '%s\n' "$final" | awk -v files="$(printf '%s' "$routers" | tr '\n' ' ')" '
  BEGIN { n = split(files, a, " "); for (i = 1; i <= n; i++) isr[a[i]] = 1 }
  /^# configuration file / { f = $4; sub(/:$/, "", f); next }
  isr[f] && /^[[:space:]]*listen[[:space:]]/ { total++; if ($0 !~ /so_keepalive=__KA__/) print f ": " $0 }
  END { if (total == 0) print "no listen directive found in the SNI router file(s)" }')
if [ -n "$missing" ]; then echo "[ops] FAILED: the SNI router has listeners without so_keepalive=__KA__:"; printf '%s\n' "$missing"; bad=1; fi
printf '%s\n' "$final" | grep -nE '^[[:space:]]*(proxy_timeout|worker_rlimit_nofile|worker_connections)[[:space:]]|so_keepalive' || true
[ "$bad" = 0 ] || exit 1
for p in $(pgrep -f 'nginx: worker' 2>/dev/null | head -n 1); do grep 'Max open files' "/proc/$p/limits" 2>/dev/null | sed 's/^/[ops] nginx worker: /' || true; done
echo "[ops] nginx: proxy_timeout __VALUE__, so_keepalive=__KA__ on the SNI router listeners, worker_rlimit_nofile __RLIMIT__, worker_connections __CONNS__ are in the effective config"
`

func renderNginxStreamPatchScript() string {
	return strings.NewReplacer(
		"__COMMENT__", nginxStreamTimeoutComment,
		"__VALUE__", nginxStreamTimeoutValue,
		"__RLIMIT__", nginxWorkerRlimitNofile,
		"__CONNS__", nginxWorkerConnections,
		"__KA__", nginxClientKeepalive,
	).Replace(nginxStreamPatchScript)
}

func patchNginxStream(client *ssh.Client) error {
	return runOpsScript(client, "nginx stream timeouts + limits (setup-ops)", renderNginxStreamPatchScript())
}
