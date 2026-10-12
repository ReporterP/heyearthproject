package main

// Hub ops hygiene: `deploy --setup-ops`. Server-side changes of subproject 1 parts 4–5
// live here so a reinstall reproduces them. Idempotent.

import (
	"log"

	"golang.org/x/crypto/ssh"
)

func setupOpsHub(client *ssh.Client, cfg *deployConfig) {
	pruneStaleFirewallRules(client)
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
