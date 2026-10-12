package main

// Hub ops hygiene: `deploy --setup-ops`. Server-side changes of subproject 1 parts 4–5
// live here (and in ops_backup.go / ops_hygiene.go) so a reinstall reproduces them. Idempotent.
//
// Every step is a bash script rendered by Go and executed through runSSHBash. Files are
// installed with put_file (base64 payload, atomic mv, "unchanged" when identical) so a re-run
// changes nothing and restarts nothing.

import (
	"encoding/base64"
	"fmt"
	"log"
	"net"
	"strings"

	"golang.org/x/crypto/ssh"
)

// setupOpsHub — deploy --setup-ops. Idempotent; re-run after any reinstall.
//
// The firewall prune runs first and is fatal (nothing else has been touched yet). The other
// steps are independent: a failing one is reported and the rest still run, then the whole run
// exits non-zero, so a transient apt outage cannot keep the backup timer from being installed.
// Without --backup-target the target already configured in /etc/heyearth/backup.env is kept
// (a re-run must never silently turn the off-box copy off).
func setupOpsHub(client *ssh.Client, cfg *deployConfig, backupTarget, exitPassword string, exitSSHPort int) {
	if err := validateBackupTarget(backupTarget, exitSSHPort); err != nil {
		log.Fatalf("[ops] %v", err)
	}
	pruneStaleFirewallRules(client)

	target, port := resolveBackupTarget(client, backupTarget, exitSSHPort)

	var failed []string
	step := func(name string, run func() error) bool {
		fmt.Printf("[ops] -- %s\n", name)
		if err := run(); err != nil {
			fmt.Printf("[ops] FAILED %s: %v\n", name, err)
			failed = append(failed, name)
			return false
		}
		return true
	}

	backupOK := step("backup timer", func() error { return installBackupTimer(client, target, port) })
	switch {
	case target == "":
		fmt.Println("[ops] WARN: --backup-target не задан — копия бэкапа вне Hub НЕ настроена")
	case backupOK:
		step("offsite backup key", func() error { return installOffsiteBackupKey(client, cfg, target, exitPassword, port) })
	}
	step("journald + fail2ban", func() error { return setupJournaldFail2ban(client, cfg.deployPort, target) })
	step("nginx stream proxy_timeout", func() error { return patchNginxStreamTimeout(client) })
	step("xray API guard", func() error { return installXrayAPIGuard(client) })

	if len(failed) > 0 {
		log.Fatalf("[ops] %d step(s) failed: %s", len(failed), strings.Join(failed, ", "))
	}
	fmt.Println("[ops] setup-ops: all steps OK")
}

// validateBackupTarget accepts "" (no off-box copy) or an IP literal plus a TCP port. The value
// ends up in shell scripts, ssh arguments and a fail2ban config, so it is never free text.
func validateBackupTarget(target string, port int) error {
	if port < 1 || port > 65535 {
		return fmt.Errorf("--exit-node-ssh-port %d is not a TCP port", port)
	}
	if target != "" && net.ParseIP(target) == nil {
		return fmt.Errorf("--backup-target %q is not an IP address", target)
	}
	return nil
}

// resolveBackupTarget returns the flag value, or — when the flag is empty — the target a
// previous run stored in /etc/heyearth/backup.env (invalid or absent → "").
func resolveBackupTarget(client *ssh.Client, flagTarget string, flagPort int) (string, int) {
	if flagTarget != "" {
		return flagTarget, flagPort
	}
	out, _ := runSSHBash(client, "read backup target", `grep -E '^TARGET_(HOST|PORT)=' /etc/heyearth/backup.env 2>/dev/null || true`, false)
	target, port := parseBackupEnvTarget(out, flagPort)
	if target != "" {
		fmt.Printf("[ops] --backup-target не задан: оставляю %s:%d из /etc/heyearth/backup.env\n", target, port)
	}
	return target, port
}

func parseBackupEnvTarget(envLines string, defPort int) (string, int) {
	target, port := "", defPort
	for _, l := range strings.Split(envLines, "\n") {
		l = strings.TrimSpace(l)
		if v, ok := strings.CutPrefix(l, "TARGET_HOST="); ok && net.ParseIP(v) != nil {
			target = v
		}
		if v, ok := strings.CutPrefix(l, "TARGET_PORT="); ok {
			var p int
			if _, err := fmt.Sscanf(v, "%d", &p); err == nil && p >= 1 && p <= 65535 {
				port = p
			}
		}
	}
	return target, port
}

// opsPutFileFunc is prepended to every script that installs files. put_file PATH MODE BASE64
// writes the decoded payload atomically (temp file in the same directory + mv) and sets
// PUT_CHANGED=1 only when the content differs, so callers restart a service only when needed.
const opsPutFileFunc = `PUT_CHANGED=0
put_file() {
  local path="$1" mode="$2" tmp
  mkdir -p "$(dirname "$path")"
  tmp=$(mktemp "$path.XXXXXX") || return 1
  if ! printf '%s' "$3" | base64 -d > "$tmp"; then rm -f "$tmp"; echo "[ops] ABORT: cannot decode payload for $path"; return 1; fi
  chmod "$mode" "$tmp"
  if [ -f "$path" ] && cmp -s "$tmp" "$path"; then
    rm -f "$tmp"; chmod "$mode" "$path"; echo "[ops] unchanged $path"
  else
    mv -f "$tmp" "$path"; PUT_CHANGED=1; echo "[ops] wrote $path"
  fi
}
`

// opsFile is one file installed by put_file.
type opsFile struct{ path, mode, content string }

// putFileCalls renders one put_file call per file (payload base64: no quoting problems).
func putFileCalls(files []opsFile) string {
	var sb strings.Builder
	for _, f := range files {
		fmt.Fprintf(&sb, "put_file %s %s '%s'\n", f.path, f.mode, base64.StdEncoding.EncodeToString([]byte(f.content)))
	}
	return sb.String()
}

// runOpsScript runs a rendered script under bash and wraps a failure with the step label.
func runOpsScript(client *ssh.Client, label, script string) error {
	_, err := runSSHBash(client, label, script, true)
	return err
}

// Deletes the plain `ufw allow <port>/<proto>` rules of ports that have no listener; a port
// that still has one is kept and reported. Not in the list (kept): 22,80,443,2443,8444/tcp;
// 443,50443,51820,30000:65000/udp.
//
// Rules are read from `ufw show added` (the commands as the admin typed them; works while ufw
// is inactive, and one line covers both IPv4 and IPv6), never from `ufw status`. Only the exact
// line "ufw allow <port>/<proto>" is a deletion candidate: `ufw delete allow X` exits 0 with
// "Could not delete non-existent rule" when X is not there, so a looser match (DENY/LIMIT, out,
// source-restricted, interface rules) would be reported as deleted while the port stays open.
// Every other line that mentions a pruned port (including a protocol-less `ufw allow 1080`) is
// printed as "[ops] MANUAL <line>" and left alone; it does not affect the exit status.
//
// Fail-closed, in two phases. Phase 1 reads the rules and asks `ss` about every port, collecting
// candidates; the exit status of each call is checked (never hidden behind a pipe) and any failure
// aborts the script before ufw is changed at all. Phase 2 deletes the candidates, then re-reads
// `ufw show added`: a candidate that is still there is FAILED (exit 1). An empty `ss` answer
// therefore always means "no listener", never "ss broke".
const pruneFirewallScript = `set -u
set -o pipefail
command -v ufw >/dev/null 2>&1 || { echo "[ops] ufw not installed — skip"; exit 0; }
command -v ss >/dev/null 2>&1 || { echo "[ops] ABORT: ss not installed — firewall left untouched"; exit 1; }
ss -Hlnt >/dev/null 2>&1 && ss -Hlnu >/dev/null 2>&1 || { echo "[ops] ABORT: ss does not support -H — firewall left untouched"; exit 1; }
ports="1080 8080 8388 4443 5443 9443 44443 30443"
entries="1080/tcp 8080/tcp 8388/tcp 4443/tcp 5443/tcp 9443/tcp 44443/tcp 30443/tcp 1080/udp 8388/udp"
added=$(ufw show added) || { echo "[ops] ABORT: ufw show added failed — firewall left untouched"; exit 1; }
cands=""
for entry in $entries; do
  port="${entry%%/*}"; proto="${entry##*/}"
  if [ "$proto" = tcp ]; then
    out=$(ss -Hlnt "sport = :$port") || { echo "[ops] ABORT: ss failed for $port/$proto — firewall left untouched"; exit 1; }
  else
    out=$(ss -Hlnu "sport = :$port") || { echo "[ops] ABORT: ss failed for $port/$proto — firewall left untouched"; exit 1; }
  fi
  if [ -n "$out" ]; then
    echo "[ops] KEEP $entry — listener:"; ss -Hlntup "sport = :$port" || echo "$out"
  elif printf '%s\n' "$added" | grep -Fx "ufw allow $entry" >/dev/null; then
    cands="$cands $entry"
  else
    echo "[ops] ABSENT $entry"
  fi
done
pat=$(echo $ports | tr ' ' '|')
mention=$(printf '%s\n' "$added" | grep -E "(^|[^0-9])($pat)([^0-9]|\$)") || true
while IFS= read -r line; do
  [ -n "$line" ] || continue
  plain=0
  for entry in $entries; do
    if [ "$line" = "ufw allow $entry" ]; then plain=1; fi
  done
  if [ "$plain" = 0 ]; then echo "[ops] MANUAL $line"; fi
done <<MENTIONS
$mention
MENTIONS
rc=0
deleted=""
for entry in $cands; do
  if ufw --force delete allow "$entry" >/dev/null; then deleted="$deleted $entry"; else echo "[ops] FAILED to delete $entry"; rc=1; fi
done
if [ -n "$deleted" ]; then
  if after=$(ufw show added); then
    for entry in $deleted; do
      if printf '%s\n' "$after" | grep -Fx "ufw allow $entry" >/dev/null; then
        echo "[ops] FAILED $entry is still present after delete"; rc=1
      else
        echo "[ops] DELETED $entry (no listener)"
      fi
    done
  else
    for entry in $deleted; do echo "[ops] FAILED to verify $entry (ufw show added failed)"; done
    rc=1
  fi
fi
ust=$(ufw status) || ust=""
case "$ust" in *inactive*) echo "[ops] NOTE: ufw is inactive — the rules are edited but not enforced" ;; esac
printf '%s\n' "$ust"
exit $rc
`

func pruneStaleFirewallRules(client *ssh.Client) {
	if _, err := runSSHBash(client, "ufw prune (setup-ops)", pruneFirewallScript, true); err != nil {
		log.Fatalf("[ops] ufw prune: %v", err)
	}
}
