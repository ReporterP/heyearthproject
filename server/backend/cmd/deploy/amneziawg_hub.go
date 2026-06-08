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

// amneziawgToolsVersion pins amnezia-vpn/amneziawg-tools (the `awg` + `awg-quick`
// CLIs). The Hub has no PPA from RU, so we git-clone + `make` from source
// (toolchain: make + gcc). This tag carries the same CPS param set (Jc/Jmin/
// Jmax/S1/S2/H1-H4) as amneziawg-go v0.2.18, so configs round-trip cleanly.
const amneziawgToolsVersion = "v1.0.20241018"

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

# ── 3b. Build amneziawg-tools from source (awg + awg-quick CLIs) ──
# These provide the userspace control plane: 'awg' (config apply / show, also
# symlinked as 'wg' so awg_sync.go's 'wg syncconf' works) and 'awg-quick'
# (brings the interface UP: spawns amneziawg-go, applies addr/port/peers/CPS).
# RU Hub has no PPA, so build from source. The tools repo is a Makefile build
# (C + shell), NOT a go build — needs make + gcc.
if command -v awg >/dev/null 2>&1 && command -v awg-quick >/dev/null 2>&1; then
  echo "[awg-hub] amneziawg-tools already installed: $(awg --version 2>&1 | head -1)"
else
  echo "[awg-hub] Installing build toolchain for amneziawg-tools (make, gcc)..."
  apt-get update -qq 2>/dev/null || true
  apt-get install -y -qq git make gcc build-essential 2>/dev/null || apt-get install -y -qq git make gcc 2>/dev/null || true
  if ! command -v make >/dev/null 2>&1 || ! command -v gcc >/dev/null 2>&1; then
    echo "[awg-hub] ERROR: make/gcc not available — cannot build amneziawg-tools."
    exit 1
  fi

  TOOLS_SRC=/tmp/amneziawg-tools-src
  rm -rf "$TOOLS_SRC"
  echo "[awg-hub] Cloning amneziawg-tools @ __TOOLS_VERSION__ ..."
  git clone --depth 1 --branch __TOOLS_VERSION__ https://github.com/amnezia-vpn/amneziawg-tools.git "$TOOLS_SRC"

  # Upstream layout: the wg-tools sources live under src/. 'make install' puts
  # 'awg' + 'awg-quick' into /usr/bin and the man pages; WITH_WGQUICK=yes builds
  # the quick wrapper. PREFIX defaults to /usr — explicit for clarity.
  echo "[awg-hub] Building amneziawg-tools ..."
  make -C "$TOOLS_SRC/src" -j"$(nproc)"
  make -C "$TOOLS_SRC/src" PREFIX=/usr WITH_WGQUICK=yes install

  rm -rf "$TOOLS_SRC"

  # amneziawg-tools installs the binary as 'awg'. awg_sync.go calls 'wg syncconf',
  # so provide a 'wg' → 'awg' shim if the package didn't already create one.
  if ! command -v wg >/dev/null 2>&1; then
    AWG_BIN="$(command -v awg)"
    ln -sf "$AWG_BIN" /usr/bin/wg
    echo "[awg-hub] symlinked wg → $AWG_BIN (for awg_sync.go 'wg syncconf')"
  fi

  if ! command -v awg-quick >/dev/null 2>&1; then
    echo "[awg-hub] ERROR: awg-quick not on PATH after install — interface cannot come UP."
    exit 1
  fi
  echo "[awg-hub] amneziawg-tools installed: awg=$(command -v awg) awg-quick=$(command -v awg-quick)"
fi

# ── 3c. Config path bridge ──
# awg-quick reads /etc/amnezia/amneziawg/<iface>.conf by default; heyearth-api
# (awg_sync.go) writes the canonical config to /opt/heyearth/awg/awg0.conf.
# Symlink so awg-quick AND 'wg syncconf .../awg0.conf' share one source of truth.
mkdir -p /etc/amnezia/amneziawg
ln -sf /opt/heyearth/awg/awg0.conf /etc/amnezia/amneziawg/awg0.conf
echo "[awg-hub] linked /etc/amnezia/amneziawg/awg0.conf → /opt/heyearth/awg/awg0.conf"

# ── 3d. Bootstrap a minimal awg0.conf if heyearth-api hasn't written one yet ──
# T1 only brings infra UP. If there are no AWG peers yet, awg_sync.go has not
# created awg0.conf, but awg-quick needs SOMETHING to read. Write an [Interface]-
# only stub (no peers) so the interface comes up + listens. heyearth-api will
# atomically overwrite this with the real config (same path) on first sync; the
# stub uses a throwaway key that gets replaced — the real PrivateKey comes from
# biz.GetServerAWGKeys() inside awg_sync.go.
if [ ! -f /opt/heyearth/awg/awg0.conf ]; then
  STUB_PRIV="$(awg genkey)"
  cat > /opt/heyearth/awg/awg0.conf <<STUB
# /opt/heyearth/awg/awg0.conf
# Bootstrap stub written by --setup-awg (T1). heyearth-api awg_sync.go
# overwrites this atomically with the real key + peers on first sync.
[Interface]
PrivateKey = ${STUB_PRIV}
ListenPort = 51820
Address = 10.66.66.1/24
STUB
  chown amnezia-wg:amnezia-wg /opt/heyearth/awg/awg0.conf
  chmod 640 /opt/heyearth/awg/awg0.conf
  echo "[awg-hub] wrote bootstrap [Interface]-only awg0.conf (no peers yet)"
else
  echo "[awg-hub] awg0.conf already present (heyearth-api manages it) — leaving as-is"
fi

# ── 4. systemd unit ──
# awg-quick brings the interface UP: it spawns amneziawg-go for awg0, then
# applies Address/ListenPort/Peers/CPS from awg0.conf via 'awg setconf'. We use
# a oneshot+RemainAfterExit unit (the standard wg-quick@ pattern, inlined for a
# fixed iface). amneziawg-go is on PATH via the symlink below so awg-quick finds
# the AmneziaWG userspace impl instead of a kernel module (Hub has no awg kmod).
#
# Unit name stays 'awg' and iface stays 'awg0' — awg_sync.go does
# 'systemctl restart awg' and 'wg syncconf awg0 ...' and MUST keep matching.
#
# WG_QUICK_USERSPACE_IMPLEMENTATION tells awg-quick to use the userspace binary;
# WG_SUDO=1 / direct root run means no sudo needed (unit runs as root).
ln -sf /opt/heyearth/awg/amneziawg-go /usr/bin/amneziawg-go
cat > /etc/systemd/system/awg.service <<'UNIT'
[Unit]
Description=AmneziaWG userspace VPN server (Palma) via awg-quick
Documentation=https://github.com/amnezia-vpn/amneziawg-tools
After=network-online.target
Wants=network-online.target

[Service]
Type=oneshot
RemainAfterExit=yes
User=root
Environment=WG_QUICK_USERSPACE_IMPLEMENTATION=amneziawg-go
Environment=WG_SUDO=1
ExecStart=/usr/bin/awg-quick up awg0
ExecStop=/usr/bin/awg-quick down awg0
# Re-apply config on 'systemctl reload awg' without tearing the tunnel down.
ExecReload=/bin/sh -c 'exec /usr/bin/wg syncconf awg0 <(/usr/bin/awg-quick strip awg0)'

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

# ── 5b. Enable IPv4 forwarding persistently ──
# AWG peers send packets destined off-host; the kernel must route between awg0
# and the Hub's egress NIC. NAT/policy-routing to Exit is Task 2 — here we only
# flip the forwarding switch so the interface is usable. Persist via sysctl.d.
cat > /etc/sysctl.d/99-heyearth-awg.conf <<'SYSCTL'
# AmneziaWG (Palma) — enable routing of peer traffic. Installed by --setup-awg.
net.ipv4.ip_forward = 1
SYSCTL
sysctl --system >/dev/null 2>&1 || sysctl -p /etc/sysctl.d/99-heyearth-awg.conf >/dev/null 2>&1 || true
echo "[awg-hub] net.ipv4.ip_forward = $(cat /proc/sys/net/ipv4/ip_forward)"

# ── 6. Enable + start service ──
systemctl daemon-reload
systemctl enable awg >/dev/null 2>&1
systemctl restart awg
echo "[awg-hub] awg.service enabled and started (awg-quick up awg0)"

# ── 7. Health check (hard — interface MUST be up after T1) ──
sleep 2
FAIL=0
if ip link show awg0 >/dev/null 2>&1; then
  STATE="$(ip -br link show awg0 | awk '{print $2}')"
  echo "[awg-hub] awg0 present, state=$STATE"
else
  echo "[awg-hub] ERROR: awg0 interface does not exist"
  FAIL=1
fi
if ss -ulnp 2>/dev/null | grep -qE ':51820\b'; then
  echo "[awg-hub] UDP :51820 listener present"
else
  echo "[awg-hub] ERROR: no UDP listener on :51820"
  FAIL=1
fi
if awg show awg0 >/dev/null 2>&1; then
  echo "[awg-hub] awg show awg0 OK:"
  awg show awg0 | sed 's/^/[awg-hub]   /'
else
  echo "[awg-hub] ERROR: 'awg show awg0' failed"
  FAIL=1
fi
if [ "$FAIL" = "1" ]; then
  echo "[awg-hub] ── diagnostics ──"
  systemctl status awg --no-pager -l | sed 's/^/[awg-hub]   /' || true
  journalctl -u awg -n 40 --no-pager | sed 's/^/[awg-hub]   /' || true
  exit 1
fi

echo "[awg-hub] Setup complete — awg0 UP, listening on :51820."
`, "__VERSION__", amneziawgVersion)
	script = strings.ReplaceAll(script, "__TOOLS_VERSION__", amneziawgToolsVersion)

	out, err := runSSH(client, script, true)
	if err != nil {
		fmt.Println(out)
		fmt.Printf("[awg-hub] WARN: setup script returned error: %v\n", err)
		return
	}
	fmt.Println(out)
	fmt.Println("[awg-hub] Готово. Проверь: go run ./cmd/deploy --status")
}
