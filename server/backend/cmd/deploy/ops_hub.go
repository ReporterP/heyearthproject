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

// Deletes ufw allow-rules of ports without a listener; a port that still has one is
// kept and reported. Not in the list (kept): 22,80,443,2443,8444/tcp; 443,50443,51820,30000:65000/udp.
//
// Fail-closed, in two phases. Phase 1 asks `ss` about every port and only collects
// candidates; the exit status of each `ss` call is checked (never hidden behind a pipe), and
// any failure aborts the script before ufw is touched at all. Phase 2 deletes the collected
// rules. An empty `ss` answer therefore always means "no listener", never "ss broke".
// `ufw status` is captured once (not piped into `grep -q`, which would SIGPIPE it under pipefail).
const pruneFirewallScript = `set -u
set -o pipefail
command -v ufw >/dev/null 2>&1 || { echo "[ops] ufw not installed — skip"; exit 0; }
command -v ss >/dev/null 2>&1 || { echo "[ops] ABORT: ss not installed — firewall left untouched"; exit 1; }
ss -Hlnt >/dev/null 2>&1 && ss -Hlnu >/dev/null 2>&1 || { echo "[ops] ABORT: ss does not support -H — firewall left untouched"; exit 1; }
cands=""
for entry in 1080/tcp 8080/tcp 8388/tcp 4443/tcp 5443/tcp 9443/tcp 44443/tcp 30443/tcp 1080/udp 8388/udp; do
  port="${entry%%/*}"; proto="${entry##*/}"
  if [ "$proto" = tcp ]; then
    out=$(ss -Hlnt "sport = :$port") || { echo "[ops] ABORT: ss failed for $port/$proto — firewall left untouched"; exit 1; }
  else
    out=$(ss -Hlnu "sport = :$port") || { echo "[ops] ABORT: ss failed for $port/$proto — firewall left untouched"; exit 1; }
  fi
  if [ -n "$out" ]; then
    echo "[ops] KEEP $entry — listener:"; ss -Hlntup "sport = :$port" || echo "$out"
  else
    cands="$cands $entry"
  fi
done
st=$(ufw status) || { echo "[ops] ABORT: ufw status failed — firewall left untouched"; exit 1; }
rc=0
for entry in $cands; do
  if printf '%s\n' "$st" | grep -E "^$entry[[:space:]]" >/dev/null; then
    if ufw --force delete allow "$entry" >/dev/null; then echo "[ops] DELETED $entry (no listener)"; else echo "[ops] FAILED to delete $entry"; rc=1; fi
  else
    echo "[ops] ABSENT $entry"
  fi
done
ufw status
exit $rc
`

func pruneStaleFirewallRules(client *ssh.Client) {
	if _, err := runSSH(client, pruneFirewallScript, true); err != nil {
		log.Fatalf("[ops] ufw prune: %v", err)
	}
}
