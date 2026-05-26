// server/backend/cmd/deploy/amneziawg_hub.go
package main

// AmneziaWG Hub provisioner.
//
// Installs the upstream `amneziawg-go` userspace VPN binary, creates a
// systemd unit, opens UDP :51820 via ufw, then exits. The actual server
// config (awg0.conf — interface keys + per-peer CPS params) is generated
// by heyearth-api at runtime (see cmd/api/awg_sync.go), so this script
// does NOT write a config file.
//
// Mirror of hysteria2_hub.go: same SSH-script-via-heredoc shape, same
// idempotence semantics, same WARN-on-error policy.
//
// MVP scope (see docs/superpowers/specs/2026-05-26-amneziawg-palma-design.md):
//  - fixed listen port 51820/UDP (no port-hopping)
//  - no DNAT REDIRECT range (Hysteria2 has it; AWG does not need it yet)
//  - CPS obfuscation is per-peer, lives in awg0.conf, NOT in this script

import (
	"fmt"
	"strings"

	"golang.org/x/crypto/ssh"
)

// amneziawgVersion pins the upstream release.
const amneziawgVersion = "v0.2.3"

// setupAmneziaWGHub provisions amneziawg-go on the Hub. Idempotent.
// Mirrors setupHysteria2Hub signature exactly.
func setupAmneziaWGHub(client *ssh.Client, cfg *deployConfig) {
	fmt.Println("[awg-hub] Установка amneziawg-go на главный сервер...")

	script := strings.ReplaceAll(`set -e

# ── 1. System user/group (filesystem ownership only) ──
if ! id amnezia-wg >/dev/null 2>&1; then
  useradd --system --no-create-home --shell /usr/sbin/nologin amnezia-wg
  echo "[awg-hub] User 'amnezia-wg' created"
fi

# ── 2. Install directory ──
mkdir -p /opt/heyearth/awg
chown amnezia-wg:amnezia-wg /opt/heyearth/awg
chmod 750 /opt/heyearth/awg

# ── 3. Download + install amneziawg-go binary ──
INSTALL_BIN=/opt/heyearth/awg/amneziawg-go
NEED_DOWNLOAD=1
if [ -x "$INSTALL_BIN" ]; then
  echo "[awg-hub] Binary already present at $INSTALL_BIN — keeping existing install"
  NEED_DOWNLOAD=0
fi

if [ "$NEED_DOWNLOAD" = "1" ]; then
  apt-get install -y -qq wget tar 2>/dev/null || true
  cd /tmp
  TARBALL=amneziawg-go-linux-amd64.tar.gz
  URL="https://github.com/amnezia-vpn/amneziawg-go/releases/download/__VERSION__/${TARBALL}"
  SHA_URL="https://github.com/amnezia-vpn/amneziawg-go/releases/download/__VERSION__/SHA256SUMS"
  echo "[awg-hub] Downloading $URL ..."
  wget -q "$URL" -O "$TARBALL"

  # Best-effort SHA256 verification
  if wget -q "$SHA_URL" -O SHA256SUMS 2>/dev/null && [ -s SHA256SUMS ]; then
    if sha256sum --check --ignore-missing SHA256SUMS 2>/dev/null | grep -q "${TARBALL}: OK"; then
      echo "[awg-hub] SHA256 verified for $TARBALL"
    else
      echo "[awg-hub] WARN: SHA256 mismatch for $TARBALL — refusing to install"
      exit 1
    fi
  else
    echo "[awg-hub] WARN: SHA256SUMS not available upstream, skipping integrity check"
  fi

  tar -xzf "$TARBALL"
  if [ -f amneziawg-go ]; then
    mv amneziawg-go "$INSTALL_BIN"
  elif [ -f "amneziawg-go-__VERSION__/amneziawg-go" ]; then
    mv "amneziawg-go-__VERSION__/amneziawg-go" "$INSTALL_BIN"
  else
    echo "[awg-hub] ERROR: unexpected tarball layout — could not find amneziawg-go binary"
    ls -la
    exit 1
  fi
  chmod +x "$INSTALL_BIN"
  chown root:root "$INSTALL_BIN"
  rm -f "$TARBALL" SHA256SUMS
  echo "[awg-hub] Binary installed at $INSTALL_BIN"
fi

# ── 4. systemd unit ──
cat > /etc/systemd/system/awg.service <<'UNIT'
[Unit]
Description=AmneziaWG userspace VPN server (Palma)
Documentation=https://github.com/amnezia-vpn/amneziawg-go
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
ExecStart=/opt/heyearth/awg/amneziawg-go -f awg0
Restart=always
RestartSec=5
User=root
CapabilityBoundingSet=CAP_NET_ADMIN CAP_NET_RAW CAP_SYS_MODULE
AmbientCapabilities=CAP_NET_ADMIN CAP_NET_RAW
Environment=WG_PROCESS_FOREGROUND=1
Environment=LOG_LEVEL=info
NoNewPrivileges=true
ProtectHome=true
PrivateTmp=true

[Install]
WantedBy=multi-user.target
UNIT

# ── 5. Firewall ──
if command -v ufw >/dev/null && ufw status 2>/dev/null | grep -q "Status: active"; then
  if ! ufw status | grep -q "51820/udp"; then
    ufw allow 51820/udp comment 'AmneziaWG Palma'
    echo "[awg-hub] ufw allow 51820/udp added"
  else
    echo "[awg-hub] ufw rule for 51820/udp already present"
  fi
fi

if command -v netfilter-persistent >/dev/null; then
  netfilter-persistent save 2>/dev/null || true
fi

# ── 6. Enable service ──
systemctl daemon-reload
systemctl enable awg >/dev/null 2>&1
echo "[awg-hub] systemd unit installed and enabled (not started yet — waits for awg0.conf)"

# ── 7. Health check ──
sleep 1
if ss -ulnp 2>/dev/null | grep -qE ':51820\b'; then
  echo "[awg-hub] UDP :51820 listener present"
else
  echo "[awg-hub] WARN: no UDP listener on :51820 yet — expected on first install."
fi

echo "[awg-hub] Setup complete."
`, "__VERSION__", amneziawgVersion)

	out, err := runSSH(client, script, true)
	if err != nil {
		fmt.Println(out)
		fmt.Printf("[awg-hub] WARN: setup script returned error: %v\n", err)
		return
	}
	fmt.Println(out)
	fmt.Println("[awg-hub] Готово. Проверь: go run ./cmd/deploy --status")
}
