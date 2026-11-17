package main

import "golang.org/x/crypto/ssh"

// Cleanup plan D7: AmneziaWG («Пальма») is gone from the code; this step takes its remains off a
// Hub that once ran --setup-awg. Idempotent: on a clean Hub it only reports «nothing to remove».
// The directory is moved aside (not deleted) so a rollback still has the binaries and keys.
const removeAWGScript = `set -u
found=0
for u in awg-tun2socks awg; do
  if [ -f "/etc/systemd/system/$u.service" ] || systemctl list-unit-files --no-legend "$u.service" 2>/dev/null | grep -q "^$u.service"; then
    systemctl disable --now "$u" >/dev/null 2>&1 || true
    rm -f "/etc/systemd/system/$u.service"
    echo "[ops] awg: unit $u stopped and removed"; found=1
  fi
done
[ "$found" = 1 ] && systemctl daemon-reload
# the tun2socks policy route and forwarding of the old awg_exit_route.go
if ip rule show | grep -q "from 10.66.66.0/24 lookup 100"; then ip rule del from 10.66.66.0/24 lookup 100 && echo "[ops] awg: policy rule removed"; found=1; fi
ip route flush table 100 2>/dev/null || true
iptables -D FORWARD -i awg0 -o awgtun -j ACCEPT 2>/dev/null && found=1 || true
iptables -D FORWARD -i awgtun -o awg0 -j ACCEPT 2>/dev/null && found=1 || true
for l in awgtun awg0; do
  if ip link show "$l" >/dev/null 2>&1; then ip link del "$l" && echo "[ops] awg: link $l removed"; found=1; fi
done
# the ufw rules --setup-awg added (443/udp and 51820/udp, comment «AmneziaWG Palma»); Bamboo's
# hop range and port stay untouched
if command -v ufw >/dev/null 2>&1; then
  for r in 443/udp 51820/udp; do
    if ufw show added 2>/dev/null | grep -E "^ufw allow $r( |$)" | grep -qi "amneziawg"; then
      ufw delete allow "$r" >/dev/null && echo "[ops] awg: ufw allow $r removed"; found=1
    fi
  done
fi
if [ -d /opt/heyearth/awg ]; then
  dst=/root/awg-removed-$(date +%Y%m%d)
  mv /opt/heyearth/awg "$dst" && echo "[ops] awg: /opt/heyearth/awg moved to $dst"; found=1
fi
[ "$found" = 1 ] || echo "[ops] awg: nothing to remove"
exit 0`

func removeAWGHub(client *ssh.Client) error {
	_, err := runSSHBash(client, "awg cleanup (setup-ops)", removeAWGScript, true)
	return err
}
