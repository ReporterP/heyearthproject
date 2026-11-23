package main

// Hub ops hygiene: `deploy --setup-ops`. Server-side changes of subproject 1 parts 4–5
// live here (and in ops_backup.go / ops_hygiene.go / ops_egress_guard.go) so a reinstall
// reproduces them. Idempotent.
//
// Every step is a bash script rendered by Go and executed through runSSHBash. Files are
// installed with put_file (base64 payload, atomic mv, "unchanged" when identical) so a re-run
// changes nothing and restarts nothing.

import (
	"encoding/base64"
	"fmt"
	"log"
	"net"
	"regexp"
	"strings"

	"golang.org/x/crypto/ssh"
)

// backupConfig is the resolved off-box backup configuration: flags, else what a previous run
// stored in /etc/heyearth/backup.env.
type backupConfig struct {
	target       string // exit IP, "" = local backups only
	port         int    // its sshd port
	ageRecipient string // age1… public key the off-box copy is encrypted to
}

// setupOpsHub — deploy --setup-ops. Idempotent; re-run after any reinstall.
//
// The firewall prune runs first and is fatal (nothing else has been touched yet). The other
// steps are independent: a failing one is reported and the rest still run, then the whole run
// exits non-zero, so a transient apt outage cannot keep the backup timer from being installed.
// Without --backup-target / --backup-age-recipient the values stored in /etc/heyearth/backup.env
// are kept (a re-run must never silently turn the off-box copy off); if backup.env cannot be
// read, the backup steps are skipped instead of guessing.
func setupOpsHub(client *ssh.Client, cfg *deployConfig, backupTarget, exitPassword string, exitSSHPort int, ageRecipient, ddosMode, sshPassword string) {
	if err := validateBackupFlags(backupTarget, exitSSHPort, ageRecipient); err != nil {
		log.Fatalf("[ops] %v", err)
	}
	pruneStaleFirewallRules(client)

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

	var bc backupConfig
	if step("backup config", func() (err error) {
		bc, err = resolveBackupConfig(client, backupTarget, exitSSHPort, ageRecipient)
		return err
	}) {
		if step("backup timer", func() error { return installBackupTimer(client, bc) }) {
			if bc.target == "" {
				fmt.Println("[ops] WARN: --backup-target не задан — копия бэкапа вне Hub НЕ настроена")
			} else {
				step("offsite backup key", func() error { return installOffsiteBackupKey(client, cfg, bc.target, exitPassword, bc.port) })
			}
		}
	} else {
		bc.target = backupTarget // fail2ban still must not whitelist the exit
	}
	step("journald + fail2ban", func() error { return setupJournaldFail2ban(client, cfg.deployPort, bc.target) })
	step("nginx stream timeouts + limits", func() error { return patchNginxStream(client) })
	step("egress guard", func() error { return installEgressGuard(client) })
	step("AmneziaWG remains (D7)", func() error { return removeAWGHub(client) })
	failed = append(failed, setupHardening(client, cfg, ddosMode, sshPassword)...)

	if len(failed) > 0 {
		log.Fatalf("[ops] %d step(s) failed: %s", len(failed), strings.Join(failed, ", "))
	}
	fmt.Println("[ops] setup-ops: all steps OK")
}

// hygieneStep is one step of deploy --setup-hygiene.
type hygieneStep struct{ name, script string }

// hygieneSteps are the Hub steps of stability phase 0 that change nothing for users (impact
// class К0): none restarts or reloads a VPN service (TestHygieneSteps). setup-ops applies the
// journal and the drill timer as well; the other two only live here.
func hygieneSteps() []hygieneStep {
	return []hygieneStep{
		{"journald retention", renderJournaldScript()},
		{"restart on failure", renderRestartPolicyScript()},
		{"backup restore drill timer", renderBackupVerifyTimerScript()},
		{"Bamboo log level", renderBambooLogLevelScript()},
	}
}

// setupHygiene runs hygieneSteps on the Hub (deploy --setup-hygiene).
func setupHygiene(client *ssh.Client) {
	fmt.Println("[hygiene] К0: xray, Бамбук, nginx, туннели и API не перезапускаются")
	var failed []string
	for _, st := range hygieneSteps() {
		fmt.Printf("[hygiene] -- %s\n", st.name)
		if err := runOpsScript(client, st.name+" (setup-hygiene)", st.script); err != nil {
			fmt.Printf("[hygiene] FAILED %s: %v\n", st.name, err)
			failed = append(failed, st.name)
		}
	}
	if len(failed) > 0 {
		log.Fatalf("[hygiene] %d step(s) failed: %s", len(failed), strings.Join(failed, ", "))
	}
	fmt.Println("[hygiene] setup-hygiene: all steps OK")
}

// ageRecipientRe is an age X25519 public key: "age1" + 58 bech32 characters.
var ageRecipientRe = regexp.MustCompile(`^age1[qpzry9x8gf2tvdw0s3jn54khce6mua7l]{58}$`)

// validateBackupFlags accepts "" (no off-box copy) or an IP literal plus a TCP port, and ""
// or a well-formed age recipient. The values end up in shell scripts, ssh arguments and config
// files, so they are never free text.
func validateBackupFlags(target string, port int, ageRecipient string) error {
	if port < 1 || port > 65535 {
		return fmt.Errorf("--exit-node-ssh-port %d is not a TCP port", port)
	}
	if target != "" && net.ParseIP(target) == nil {
		return fmt.Errorf("--backup-target %q is not an IP address", target)
	}
	if ageRecipient != "" && !ageRecipientRe.MatchString(ageRecipient) {
		return fmt.Errorf("--backup-age-recipient %q is not an age public key (age1…, 62 characters)", ageRecipient)
	}
	return nil
}

// readBackupEnvScript tells "no backup.env yet" (ENV-ABSENT, a fresh Hub) apart from a real
// failure (non-zero exit: unreadable file, grep error). grep exit 1 (no matching line) is fine.
const readBackupEnvScript = `f=/etc/heyearth/backup.env
if [ ! -e "$f" ]; then echo ENV-ABSENT; exit 0; fi
grep -E '^(TARGET_HOST|TARGET_PORT|AGE_RECIPIENT)=' "$f"; rc=$?
if [ "$rc" -gt 1 ]; then echo "cannot read $f (grep exit $rc)" >&2; exit 1; fi
echo ENV-OK
`

// resolveBackupConfig merges the flags with the stored configuration. Flags win; a flag target
// takes its port from --exit-node-ssh-port, a stored target keeps its stored port. An error
// (unreadable backup.env, malformed stored values, a target without a recipient) aborts the
// backup steps: guessing here could silently switch the off-box copy off or send it unencrypted.
func resolveBackupConfig(client *ssh.Client, flagTarget string, flagPort int, flagRecipient string) (backupConfig, error) {
	out, err := runSSHBash(client, "read backup.env", readBackupEnvScript, true)
	if err != nil {
		return backupConfig{}, fmt.Errorf("cannot read the stored backup configuration, backup steps skipped: %w", err)
	}
	stored, err := parseBackupEnv(out)
	if err != nil {
		return backupConfig{}, err
	}
	return mergeBackupConfig(stored, flagTarget, flagPort, flagRecipient)
}

func mergeBackupConfig(stored backupConfig, flagTarget string, flagPort int, flagRecipient string) (backupConfig, error) {
	bc := stored
	if flagTarget != "" {
		bc.target, bc.port = flagTarget, flagPort
	} else if bc.target != "" {
		fmt.Printf("[ops] --backup-target не задан: оставляю %s:%d из /etc/heyearth/backup.env\n", bc.target, bc.port)
	}
	if bc.port == 0 {
		bc.port = flagPort
	}
	if flagRecipient != "" {
		bc.ageRecipient = flagRecipient
	}
	if bc.target != "" && bc.ageRecipient == "" {
		return backupConfig{}, fmt.Errorf("the off-box copy to %s needs an age recipient: pass --backup-age-recipient age1… "+
			"(the public key of a key pair you generated offline with age-keygen; plain archives are never sent off the Hub)", bc.target)
	}
	return bc, nil
}

// parseBackupEnv parses the output of readBackupEnvScript. The first line must be ENV-ABSENT or
// ENV-OK, and any stored value that is present must be well-formed — otherwise it is an error,
// never "treat as not configured".
func parseBackupEnv(out string) (backupConfig, error) {
	lines := strings.Split(strings.ReplaceAll(out, "\r", ""), "\n")
	var bc backupConfig
	state := ""
	for _, l := range lines {
		l = strings.TrimSpace(l)
		switch {
		case l == "":
		case l == "ENV-ABSENT" || l == "ENV-OK":
			state = l
		case strings.HasPrefix(l, "TARGET_HOST="):
			v := strings.TrimPrefix(l, "TARGET_HOST=")
			if v != "" && net.ParseIP(v) == nil {
				return backupConfig{}, fmt.Errorf("backup.env has a malformed TARGET_HOST=%q, backup steps skipped", v)
			}
			bc.target = v
		case strings.HasPrefix(l, "TARGET_PORT="):
			var p int
			if _, err := fmt.Sscanf(strings.TrimPrefix(l, "TARGET_PORT="), "%d", &p); err != nil || p < 1 || p > 65535 {
				return backupConfig{}, fmt.Errorf("backup.env has a malformed %q, backup steps skipped", l)
			}
			bc.port = p
		case strings.HasPrefix(l, "AGE_RECIPIENT="):
			v := strings.TrimPrefix(l, "AGE_RECIPIENT=")
			if v != "" && !ageRecipientRe.MatchString(v) {
				return backupConfig{}, fmt.Errorf("backup.env has a malformed AGE_RECIPIENT, backup steps skipped")
			}
			bc.ageRecipient = v
		}
	}
	if state == "" {
		return backupConfig{}, fmt.Errorf("unexpected answer while reading backup.env (%q), backup steps skipped", strings.TrimSpace(out))
	}
	return bc, nil
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
