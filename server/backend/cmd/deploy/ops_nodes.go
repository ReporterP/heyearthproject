package main

// deploy --harden-nodes [--node-upgrade]: the host hardening of config/node_hardening.go (the
// exit recipe's step host_hardening) on every node already in the servers table — exits with a
// fail2ban jail for SSH, entries without (their recipe has its own) — and, with --node-upgrade,
// the pending security updates (no reboot). Runs from the Hub with the Hub's key; a node's host
// key must match servers.ssh_host_key_fp (the owner-confirmed one), else the node is skipped.

import (
	"fmt"
	"strings"

	"github.com/heyearth/backend/internal/config"
	"golang.org/x/crypto/ssh"
)

// nodeSSHFunc: nodessh <id> runs stdin on node <id> with its host key checked against the DB.
const nodeSSHFunc = `Q() { runuser -u postgres -- psql -d heyearth -tAc "$1"; }
nodessh() {
  local id=$1 ip port fp kh got rc
  ip=$(Q "select ip_address from servers where id=$id"); port=$(Q "select coalesce(ssh_port,22) from servers where id=$id")
  fp=$(Q "select coalesce(ssh_host_key_fp,'') from servers where id=$id")
  kh=$(mktemp); ssh-keyscan -T 10 -p "$port" -t ed25519 "$ip" 2>/dev/null > "$kh"
  got=$(ssh-keygen -lf "$kh" 2>/dev/null | awk '{print $2}')
  if [ -z "$fp" ] || [ "$got" != "$fp" ]; then echo "[nodes] node $id: host key does not match the confirmed one — skipped"; rm -f "$kh"; return 1; fi
  timeout 900 ssh -i /opt/heyearth/state/hub-ssh/id_ed25519 -p "$port" -o BatchMode=yes -o ConnectTimeout=15 -o StrictHostKeyChecking=yes -o UserKnownHostsFile="$kh" "root@$ip" 'bash -s'
  rc=$?; rm -f "$kh"; return $rc
}
`

const nodeUpgradeScript = `export DEBIAN_FRONTEND=noninteractive NEEDRESTART_MODE=a
apt-get -o DPkg::Lock::Timeout=300 update -qq
apt-get -o DPkg::Lock::Timeout=300 -y -qq -o Dpkg::Options::=--force-confdef -o Dpkg::Options::=--force-confold upgrade --with-new-pkgs >/tmp/heyearth-upgrade.log 2>&1 && echo "[harden] upgrade OK" || { echo "[harden] upgrade FAILED:"; tail -5 /tmp/heyearth-upgrade.log; }
echo "[harden] pending security: $(apt list --upgradable 2>/dev/null | grep -c security) reboot=$([ -f /var/run/reboot-required ] && echo needed || echo no)"
`

// renderNodeHardeningScript is what one node runs (stdin of bash -s).
func renderNodeHardeningScript(jail bool, sshPort int, upgrade bool) string {
	var files []opsFile
	for _, f := range config.NodeHardeningFiles(jail, sshPort, "") {
		files = append(files, opsFile{f.Path, fmt.Sprintf("%o", f.Mode), f.Data})
	}
	s := "set -u\n" + opsPutFileFunc + putFileCalls(files) + config.NodeHardeningApplyScript(jail)
	if upgrade {
		s += nodeUpgradeScript
	}
	return s
}

func hardenNodes(client *ssh.Client, upgrade bool) error {
	out, err := runSSHBash(client, "nodes list", `runuser -u postgres -- psql -d heyearth -tAc "select id||' '||role||' '||coalesce(ssh_port,22) from servers where lifecycle_state <> 'removed' order by id"`, true)
	if err != nil {
		return err
	}
	var failed []string
	for _, l := range strings.Split(strings.TrimSpace(out), "\n") {
		var id, port int
		var role string
		if n, _ := fmt.Sscanf(strings.TrimSpace(l), "%d %s %d", &id, &role, &port); n != 3 {
			continue
		}
		jail := role == "exit"
		fmt.Printf("[nodes] -- node %d (%s)\n", id, role)
		script := nodeSSHFunc + fmt.Sprintf("nodessh %d <<'HEYEARTH_NODE_EOF'\n%sHEYEARTH_NODE_EOF\n", id, renderNodeHardeningScript(jail, port, upgrade))
		res, err := runSSHBash(client, fmt.Sprintf("harden node %d", id), script, false)
		fmt.Print(res)
		if err != nil || !strings.Contains(res, "HARDEN_OK=1") {
			failed = append(failed, fmt.Sprint(id))
		}
	}
	if len(failed) > 0 {
		return fmt.Errorf("nodes not hardened: %s", strings.Join(failed, ", "))
	}
	return nil
}
