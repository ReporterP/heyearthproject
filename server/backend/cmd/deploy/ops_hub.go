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
// Fail-closed: if `ss` is missing or does not support the flags used below, the script
// aborts before touching ufw (an empty `ss` answer would otherwise read as "no listener").
const pruneFirewallScript = `set -u
command -v ufw >/dev/null 2>&1 || { echo "[ops] ufw not installed — skip"; exit 0; }
command -v ss >/dev/null 2>&1 || { echo "[ops] ss not installed — abort, ufw untouched"; exit 1; }
ss -Hlnt >/dev/null 2>&1 && ss -Hlnu >/dev/null 2>&1 || { echo "[ops] ss does not support -H — abort, ufw untouched"; exit 1; }
rc=0
prune() {
  rule="$1"; proto="$2"; port="${rule%%:*}"
  if [ "$proto" = tcp ]; then n=$(ss -Hlnt "sport = :$port" | wc -l); else n=$(ss -Hlnu "sport = :$port" | wc -l); fi
  if [ "$n" -gt 0 ]; then echo "[ops] KEEP $rule/$proto — listener:"; ss -Hlntup "sport = :$port"; return; fi
  if ufw status | grep -qE "^$rule/$proto[[:space:]]"; then
    if ufw --force delete allow "$rule/$proto" >/dev/null; then echo "[ops] DELETED $rule/$proto (no listener)"; else echo "[ops] FAILED to delete $rule/$proto"; rc=1; fi
  else
    echo "[ops] ABSENT $rule/$proto"
  fi
}
for p in 1080 8080 8388 4443 5443 9443 44443 30443; do prune "$p" tcp; done
for p in 1080 8388; do prune "$p" udp; done
ufw status
exit $rc
`

func pruneStaleFirewallRules(client *ssh.Client) {
	if _, err := runSSH(client, pruneFirewallScript, true); err != nil {
		log.Fatalf("[ops] ufw prune: %v", err)
	}
}
