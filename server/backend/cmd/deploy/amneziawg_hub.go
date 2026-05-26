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

// amneziawgVersion pins the upstream git tag. Upstream amnezia-vpn/amneziawg-go
// publishes only source tags (no prebuilt GitHub Release artifacts), so this
// is the ref we git-clone + go-build from. Latest stable as of 2026-05-26.
const amneziawgVersion = "v0.2.18"

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

# ── 3. Build amneziawg-go from source ──
# Upstream amnezia-vpn/amneziawg-go has no published GitHub Releases — only
# source tags. We git clone + go build. Requires go toolchain on Hub
# (installed by installGoOnServer in --main at /usr/local/go).
INSTALL_BIN=/opt/heyearth/awg/amneziawg-go
if [ -x "$INSTALL_BIN" ]; then
  echo "[awg-hub] Binary already present at $INSTALL_BIN — keeping existing install"
else
  # /usr/local/go/bin is set via /etc/profile.d/go.sh which non-login shells
  # don't source — make sure 'go' is reachable here.
  export PATH="$PATH:/usr/local/go/bin"
  if ! command -v go >/dev/null 2>&1; then
    echo "[awg-hub] ERROR: go toolchain not installed on Hub. Run --main first to install Go."
    exit 1
  fi
  echo "[awg-hub] go version: $(go version)"

  # Ensure git + build deps available
  apt-get install -y -qq git ca-certificates 2>/dev/null || true

  # Clone + checkout pinned tag
  SRC_DIR=/tmp/amneziawg-go-src
  rm -rf "$SRC_DIR"
  echo "[awg-hub] Cloning amneziawg-go @ __VERSION__ ..."
  git clone --depth 1 --branch __VERSION__ https://github.com/amnezia-vpn/amneziawg-go.git "$SRC_DIR"

  # Build (CGO_ENABLED=0 for static binary — no glibc surprise on minimal Hub variants)
  echo "[awg-hub] Building amneziawg-go ..."
  cd "$SRC_DIR"
  CGO_ENABLED=0 go build -o "$INSTALL_BIN" -ldflags "-s -w" .

  chmod +x "$INSTALL_BIN"
  chown root:root "$INSTALL_BIN"

  # Cleanup source tree — keep only the binary
  rm -rf "$SRC_DIR"
  echo "[awg-hub] Binary built at $INSTALL_BIN ($(du -h "$INSTALL_BIN" | cut -f1))"
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
