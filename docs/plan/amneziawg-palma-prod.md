---
id: amneziawg-palma-prod
beads_id: heyearthproject-98ng
prd: ../prd/amneziawg-palma-prod.prd.md
status: draft
created: 2026-06-08
---

# AmneziaWG (Пальма) Production Rollout — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use `superpowers:subagent-driven-development` (recommended) or `superpowers:executing-plans` to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Bring AmneziaWG («🌴 Пальма») to working production — an additional cabinet connection for 300₽/3000₽ plans that egresses through the Exit node (foreign IP, like Happ), counts traffic into the shared subscription limit, and is gated by plan/subscription — without touching the working Reality/Happ path.

**Architecture:** The backend (`biz/awg.go`, `awg_sync.go`, installer) is ~90% done. This plan fixes the Hub so the `awg0` interface actually comes up (amneziawg-tools + `awg-quick`), routes AWG egress through the existing Hysteria2 SOCKS tunnel to the Exit node (tun2socks + policy-routing scoped to `10.66.66.0/24`), fixes the `wg→awg` apply bug + adds traffic-limit enforcement in `awg_sync.go`, adds a per-peer traffic collector writing to the shared subscription counter, exposes a gated `/api/v1/user/awg-config` endpoint + an additive cabinet UI block, and corrects plan gating to 300₽/3000₽ only.

**Tech Stack:** Go 1.22 backend, PostgreSQL 16, amneziawg-go v0.2.18 + amneziawg-tools, hev-socks5-tunnel (L3→SOCKS5), React 19 + Vite + Tailwind v4 (site cabinet), deploy via SSH + `quick_deploy.go`, evidence-driven verification (no tests in repo).

## 🔴 RED LINE — invariants every task must preserve

- **Happ subscription** (`client_api.go`, `subscription_handler.go`) emits the SAME 4 Reality URIs — `amneziawg://` stays commented out (Happ can't parse it).
- **Reality inbounds, xray routing rules, all outbounds, and Hub→Exit Hysteria2** are NOT modified. T2 adds ONE *additive* loopback inbound to xray (`socks-awg`, 127.0.0.1:10808) that AWG feeds into; it inherits the existing GLOBAL routing rules (`inboundTag: null`) so AWG gets the same RU-direct / rest-Exit split as Reality. The Reality vless inbounds and the Hysteria2 outbound are untouched.
- **Policy routing is scoped to `10.66.66.0/24` only** — never a change to the main routing table or a global default route.
- Every task carries a regression check; T8 has explicit RED-LINE evidence (4 Reality URIs / 0 amneziawg in Happ sub; xray `accepted` still flowing).

## Task order & dependency

T1 (interface up) → T2 (split-routing via xray socks-awg) → T3 (apply bug + enforcement) → T4 (traffic collector) → T5 (endpoint) → T6 (cabinet UI) → T7 (gating + admin verify) → T8 (deploy everything + E2E). T2 has a code part (`socks-awg` inbound in xray_sync.go — deploys with the backend binary) AND a deploy part (`--setup-awg` installs tun2socks); the xray inbound must be live (heyearth-api restarted → xray config regenerated) BEFORE tun2socks dials 10808. T3-T6 are backend/frontend code. T7 is data+verify. T8 ships and proves it.

## File map

| File | Task | Change |
|------|------|--------|
| `server/backend/cmd/deploy/amneziawg_hub.go` | T1 | install amneziawg-tools, `awg-quick up`, ip_forward, unit rewrite |
| `server/backend/cmd/api/xray_sync.go` | T2 | additive `socks-awg` SOCKS5 inbound (split entry point — RU direct / rest Exit) |
| `server/backend/cmd/deploy/awg_exit_route.go` (new) | T2 | hev-socks5-tunnel → xray socks-awg + policy routing |
| `server/backend/cmd/deploy/main.go` | T2 | wire `setupAWGExitRoute` into `case "setup-awg"` |
| `server/backend/cmd/api/awg_sync.go` | T3 | `wg`→`awg` apply, real cold-start, traffic-limit WHERE |
| `server/backend/cmd/api/awg_stats.go` (new) | T4 | per-peer traffic collector (dual-write) |
| `server/backend/cmd/api/main.go` | T4 | wire `StartAWGStatsLoop()` |
| `server/backend/cmd/api/main.go` | T5 | `amnezia_available` flag + `/api/v1/user/awg-config` route |
| `site/src/api/user.js` | T6 | `getAwgConfig(token)` |
| `site/src/pages/ConnectionsPage.jsx` | T6 | additive Пальма card (gated) |
| prod PostgreSQL `plans` | T7 | `amnezia_wg_enabled=FALSE` on id 1,4 |
| `admin_web/src/pages/PlansPage.jsx` | T7 | verify checkbox round-trips (no change expected) |

---
### Task 1: Hub infrastructure — bring awg0 interface UP

Beads: `heyearthproject-98ng` (T1)

**Goal:** install `amneziawg-tools` (`awg` + `awg-quick`) on Hub, rewrite the `awg.service` unit so the interface actually comes UP with config applied via `awg-quick up awg0`, enable `ip_forward` persistently, verify `ufw allow 51820/udp`, and make the service `enabled` + non-crash-looping. NO NAT/MASQUERADE/policy-routing here (that is Task 2).

**Files:**
- Modify: `server/backend/cmd/deploy/amneziawg_hub.go` — function `setupAmneziaWGHub` (lines 32–153), embedded heredoc `script`. The version pin block (lines 27–30) gets a sibling const for `amneziawg-tools`.

**Compatibility constraints discovered (do NOT violate — these are why the design below is fixed):**
- `cmd/api/awg_sync.go:144` runs `wg syncconf awg0 /opt/heyearth/awg/awg0.conf` for zero-downtime reloads. `amneziawg-tools` installs `awg` and a `wg`→`awg` symlink, so installing tools satisfies this too. **Verify** `wg` resolves after install.
- `cmd/api/awg_sync.go:147` falls back to `systemctl restart awg`. → the unit MUST keep the name **`awg`** and the interface MUST stay **`awg0`**. Do not rename either.
- `awg-quick` reads its config from `/etc/amnezia/amneziawg/<iface>.conf` by default, but the canonical config generated by `awg_sync.go` lives at `/opt/heyearth/awg/awg0.conf` (see `awgConfigPath` line 36 + renderer header line 235). We bridge this with a **symlink** `/etc/amnezia/amneziawg/awg0.conf → /opt/heyearth/awg/awg0.conf` so both `awg-quick up awg0` and `wg syncconf awg0 /opt/heyearth/awg/awg0.conf` operate on the same file.
- `awg-quick` needs `iproute2` (`ip`) — present on all Hub variants, no install. It also (optionally) uses `resolvconf` for `DNS=`; our `[Interface]` has no `DNS=` line (renderer lines 237–241 emit only PrivateKey/ListenPort/Address), so `resolvconf` is not required.

---

- [ ] **Step 1: Add the `amneziawgToolsVersion` const next to the existing `amneziawgVersion` pin.**

`amneziawg-tools` tag `v1.0.20241018` is the release matched to the `amneziawg-go` v0.2.18 era (both ship the CPS/Jc-S2-H1..H4 param set). Pin it explicitly — no PPA on a RU Hub.

OLD (`amneziawg_hub.go` lines 27–30):
```go
// amneziawgVersion pins the upstream git tag. Upstream amnezia-vpn/amneziawg-go
// publishes only source tags (no prebuilt GitHub Release artifacts), so this
// is the ref we git-clone + go-build from. Latest stable as of 2026-05-26.
const amneziawgVersion = "v0.2.18"
```

NEW:
```go
// amneziawgVersion pins the upstream git tag. Upstream amnezia-vpn/amneziawg-go
// publishes only source tags (no prebuilt GitHub Release artifacts), so this
// is the ref we git-clone + go-build from. Latest stable as of 2026-05-26.
const amneziawgVersion = "v0.2.18"

// amneziawgToolsVersion pins amnezia-vpn/amneziawg-tools (the `awg` + `awg-quick`
// CLIs). The Hub has no PPA from RU, so we git-clone + `make` from source
// (toolchain: make + gcc). This tag carries the same CPS param set (Jc/Jmin/
// Jmax/S1/S2/H1-H4) as amneziawg-go v0.2.18, so configs round-trip cleanly.
const amneziawgToolsVersion = "v1.0.20241018"
```

Run: `cd server/backend && gofmt -l ./cmd/deploy/amneziawg_hub.go`
Expected: no output (file already formatted).

---

- [ ] **Step 2: Insert the `amneziawg-tools` build block into the script, after the `amneziawg-go` build (step 3) and before the systemd unit.**

This adds a new numbered section `3b`. Edit the `__VERSION__` ReplaceAll to also template `__TOOLS_VERSION__` (see Step 4).

OLD (`amneziawg_hub.go` lines 84–89, end of the go-build block + start of unit block):
```sh
  # Cleanup source tree — keep only the binary
  rm -rf "$SRC_DIR"
  echo "[awg-hub] Binary built at $INSTALL_BIN ($(du -h "$INSTALL_BIN" | cut -f1))"
fi

# ── 4. systemd unit ──
```

NEW:
```sh
  # Cleanup source tree — keep only the binary
  rm -rf "$SRC_DIR"
  echo "[awg-hub] Binary built at $INSTALL_BIN ($(du -h "$INSTALL_BIN" | cut -f1))"
fi

# ── 3b. Build amneziawg-tools from source (awg + awg-quick CLIs) ──
# These provide the userspace control plane: `awg` (config apply / show, also
# symlinked as `wg` so awg_sync.go's `wg syncconf` works) and `awg-quick`
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

  # Upstream layout: the wg-tools sources live under src/. `make install` puts
  # `awg` + `awg-quick` into /usr/bin and the man pages; WITH_WGQUICK=yes builds
  # the quick wrapper. PREFIX defaults to /usr — explicit for clarity.
  echo "[awg-hub] Building amneziawg-tools ..."
  make -C "$TOOLS_SRC/src" -j"$(nproc)"
  make -C "$TOOLS_SRC/src" PREFIX=/usr WITH_WGQUICK=yes install

  rm -rf "$TOOLS_SRC"

  # amneziawg-tools installs the binary as `awg`. awg_sync.go calls `wg syncconf`,
  # so provide a `wg` → `awg` shim if the package didn't already create one.
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
# Symlink so awg-quick AND `wg syncconf .../awg0.conf` share one source of truth.
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
```

Run: `cd server/backend && go build ./cmd/deploy`
Expected: no output, exit 0 (Go file with embedded heredoc still compiles).

---

- [ ] **Step 3: Replace the bare-daemon systemd unit with an `awg-quick`-driven oneshot unit so the interface comes UP with config applied.**

The current unit (lines 90–113) runs `amneziawg-go -f awg0` directly — that only creates an empty TUN, never applying address/port/peers. Switch to `Type=oneshot` + `RemainAfterExit=yes` driving `awg-quick up awg0` / `awg-quick down awg0`. `awg-quick` itself spawns `amneziawg-go` and then applies the config via `awg setconf`. Keep the unit name `awg` (awg_sync.go restart fallback) and `WantedBy=multi-user.target`.

OLD (`amneziawg_hub.go` lines 89–113):
```sh
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
```

NEW:
```sh
# ── 4. systemd unit ──
# awg-quick brings the interface UP: it spawns amneziawg-go for awg0, then
# applies Address/ListenPort/Peers/CPS from awg0.conf via `awg setconf`. We use
# a oneshot+RemainAfterExit unit (the standard wg-quick@ pattern, inlined for a
# fixed iface). amneziawg-go is on PATH via the symlink below so awg-quick finds
# the AmneziaWG userspace impl instead of a kernel module (Hub has no awg kmod).
#
# Unit name stays `awg` and iface stays `awg0` — awg_sync.go does
# `systemctl restart awg` and `wg syncconf awg0 ...` and MUST keep matching.
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
# Re-apply config on `systemctl reload awg` without tearing the tunnel down.
ExecReload=/bin/sh -c 'exec /usr/bin/wg syncconf awg0 <(/usr/bin/awg-quick strip awg0)'

[Install]
WantedBy=multi-user.target
UNIT
```

Note: oneshot units do not crash-loop (no `Restart=`); if `awg-quick up` fails, the unit goes `failed` and the Step-7 smoke check catches it. This is the documented wg-quick semantics — the daemon lifetime is owned by awg-quick, not systemd `simple`.

Run: `cd server/backend && go build ./cmd/deploy`
Expected: no output, exit 0.

---

- [ ] **Step 4: Enable persistent IP forwarding and start the service in the unit-enable section; widen the `ReplaceAll` to template `__TOOLS_VERSION__`.**

The current step 6 (lines 129–132) only `enable`s the unit and never starts it ("waits for awg0.conf"). Now that Step 2 guarantees an awg0.conf stub exists, we enable **and** start, add the sysctl persist, and re-verify ufw (which already exists at lines 115–123, leave that block untouched).

OLD (`amneziawg_hub.go` lines 129–132):
```sh
# ── 6. Enable service ──
systemctl daemon-reload
systemctl enable awg >/dev/null 2>&1
echo "[awg-hub] systemd unit installed and enabled (not started yet — waits for awg0.conf)"
```

NEW:
```sh
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
```

Then update the `ReplaceAll` chain at the bottom of the function.

OLD (`amneziawg_hub.go` line 143):
```go
`, "__VERSION__", amneziawgVersion)
```

NEW:
```go
`, "__VERSION__", amneziawgVersion)
	script = strings.ReplaceAll(script, "__TOOLS_VERSION__", amneziawgToolsVersion)
```

Run: `cd server/backend && go build ./cmd/deploy`
Expected: no output, exit 0.

---

- [ ] **Step 5: Replace the health-check (step 7) so it fails loud if the interface did not come UP.**

The current step 7 (lines 134–140) only WARNs on a missing listener ("expected on first install") — that was correct when the unit never started. Now the interface MUST be UP, so turn the WARN into a hard check that prints diagnostics and exits non-zero on failure (so `runSSH(..., true)` surfaces the error to the operator).

OLD (`amneziawg_hub.go` lines 134–142):
```sh
# ── 7. Health check ──
sleep 1
if ss -ulnp 2>/dev/null | grep -qE ':51820\b'; then
  echo "[awg-hub] UDP :51820 listener present"
else
  echo "[awg-hub] WARN: no UDP listener on :51820 yet — expected on first install."
fi

echo "[awg-hub] Setup complete."
```

NEW:
```sh
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
```

Run: `cd server/backend && go build ./cmd/deploy`
Expected: no output, exit 0.

---

- [ ] **Step 6: Deploy and verify on the real Hub (evidence-driven — project has no tests).**

Run: `cd server/backend && go run ./cmd/deploy --setup-awg`
Expected (tail): lines `[awg-hub] net.ipv4.ip_forward = 1`, `[awg-hub] awg0 present, state=UNKNOWN` (point-to-point/TUN ifaces report `UNKNOWN`, which is the "up" state — NOT `DOWN`), `[awg-hub] UDP :51820 listener present`, `[awg-hub] awg show awg0 OK:`, and finally `[awg-hub] Setup complete — awg0 UP, listening on :51820.` Exit 0.

Then confirm each acceptance criterion independently:

Run: `cd server/backend && go run ./cmd/deploy --ssh "ip -br link show awg0"`
Expected: `awg0  UNKNOWN` (state column is `UNKNOWN` or `UP`, never `DOWN`).

Run: `cd server/backend && go run ./cmd/deploy --ssh "ss -ulnp | grep 51820"`
Expected: a line like `UNCONN 0 0 *:51820 *:* users:(("amneziawg-go",pid=...))`.

Run: `cd server/backend && go run ./cmd/deploy --ssh "awg show awg0"`
Expected: an `interface: awg0` block with `listening port: 51820` and `public key: ...` (no peers yet — that is expected after T1).

Run: `cd server/backend && go run ./cmd/deploy --ssh "systemctl is-enabled awg && systemctl is-active awg"`
Expected: two lines — `enabled` then `active`.

Run: `cd server/backend && go run ./cmd/deploy --ssh "sysctl net.ipv4.ip_forward"`
Expected: `net.ipv4.ip_forward = 1`.

Run: `cd server/backend && go run ./cmd/deploy --ssh "ufw status | grep 51820"`
Expected: `51820/udp  ALLOW  Anywhere` (rule already added by the existing step-5 block).

Idempotence check — re-run must not crash-loop or rebuild:

Run: `cd server/backend && go run ./cmd/deploy --setup-awg`
Expected: `[awg-hub] amneziawg-tools already installed: ...`, `[awg-hub] awg0.conf already present ...`, interface still UP, `Setup complete`, exit 0.

Reboot-survival (optional but recommended if a maintenance window exists):

Run: `cd server/backend && go run ./cmd/deploy --ssh "systemctl is-enabled awg"`
Expected: `enabled` (unit will auto-start on next boot).

---

- [ ] **Step 7: Commit.**

Run:
```sh
cd server/backend && go build ./cmd/deploy && gofmt -l ./cmd/deploy/amneziawg_hub.go
```
Expected: no output (builds clean, already formatted).

Then:
```sh
git add server/backend/cmd/deploy/amneziawg_hub.go
git commit -m "$(cat <<'MSG'
feat(awg): install amneziawg-tools + bring awg0 UP via awg-quick

Hub previously installed only amneziawg-go and ran it as a bare daemon
(`amneziawg-go -f awg0`), which created an empty TUN device but never
applied any config — awg0 stayed DOWN, UDP :51820 was not listening, and
`awg show` was empty.

- Build amneziawg-tools (awg + awg-quick) from source, pinned to
  v1.0.20241018 (matches amneziawg-go v0.2.18 CPS param set). RU Hub has
  no PPA, so clone + make with the make/gcc toolchain.
- Rewrite awg.service: oneshot + RemainAfterExit driving `awg-quick up
  awg0`, which spawns amneziawg-go AND applies Address/ListenPort/peers.
- Symlink /etc/amnezia/amneziawg/awg0.conf -> /opt/heyearth/awg/awg0.conf
  and `wg`->`awg` so awg-quick and awg_sync.go's `wg syncconf` share one
  source of truth.
- Bootstrap an [Interface]-only awg0.conf stub when no peers exist yet so
  the interface still comes up (heyearth-api overwrites it on first sync).
- Persist net.ipv4.ip_forward=1 via /etc/sysctl.d/.
- Hard health check: fail loud if awg0 is not UP / not listening.

NAT/policy-routing to Exit is Task 2 — not in this change.

Beads: heyearthproject-98ng (T1)

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
MSG
)"
```
Expected: one commit created touching only `amneziawg_hub.go`.

Run: `git show --stat HEAD`
Expected: `1 file changed`, path `server/backend/cmd/deploy/amneziawg_hub.go`, and the trailer block (`Beads: heyearthproject-98ng (T1)` + `Co-Authored-By: Claude Opus 4.7 (1M context)`) visible.
### Task 2: Route AmneziaWG egress through xray (split-routing — RU direct / rest via Exit, как Happ)

> **Beads:** `heyearthproject-98ng (T2)` — hardest/riskiest task of Palma rollout.
>
> **Approach (optimal — reuses the Hub's existing xray routing).** Hub xray already
> applies GLOBAL routing rules (`inboundTag: null`, verified on prod) that send
> ~20178 RU domains + 13080 RU IP-ranges to the `direct-freedom` outbound (egress
> straight from the Hub, Moscow) and everything else to the `exit-balancer`
> (Amsterdam via Hysteria2). **This is exactly Happ's split-routing**, by domain AND
> geoip. So instead of forcing ALL AWG traffic to the Exit, we add ONE additive
> SOCKS5 inbound to Hub xray and feed AWG into it — AWG then inherits the identical
> split: Russian sites go **direct** (fast, Russian IP — gosuslugi/banks/Yandex work),
> foreign sites egress via **Amsterdam** (bypass blocks). One routing engine, one
> source of truth, no geoip duplication.
>
> **Why not feed AWG straight into Hysteria2 SOCKS (the naive «all-to-Exit»):** that
> would tunnel even Russian sites through Amsterdam — slower, and RU-IP-gated services
> (banks, gosuslugi) break. Domain-level split on a pure-L3 WireGuard is impossible on
> the client; doing it server-side via xray is the only way to match Happ.
>
> **Tool chosen:** **hev-socks5-tunnel** (`github.com/heiher/hev-socks5-tunnel`) — the
> userspace TUN→SOCKS5 forwarder that relays **both TCP and UDP through one SOCKS5
> upstream via UDP ASSOCIATE** (`socks5.udp: 'udp'`), so AWG clients' DNS (UDP 53) and
> QUIC traverse it (unlike redsocks, TCP-only). Its upstream is the new **xray** SOCKS5
> inbound `127.0.0.1:10808` — NOT Hysteria2 directly. **xray does the split.**
>
> **What we build:** (a) Step 0 — an additive `socks-awg` inbound in `xray_sync.go`;
> (b) Step 1+ — `server/backend/cmd/deploy/awg_exit_route.go` exposing
> `setupAWGExitRoute(client, cfg)` (hev-socks5-tunnel → xray SOCKS + policy routing),
> wired into `case "setup-awg":`. Mirrors `amneziawg_hub.go`'s SSH-heredoc style.

---

#### RED LINE (do not violate)

- Hub xray gets **ONE additive inbound** (`socks-awg`, `listen 127.0.0.1`, loopback-only). Reality inbounds (`vless-reality`, `vless-xhttp`, `shadowsocks`), the global routing rules, and ALL outbounds are UNCHANGED — the new inbound merely inherits the existing global rules (`inboundTag: null`). This is additive, not a rewrite of the Reality path.
- Reality's path `xray → Hysteria2 → Exit` MUST NOT change. AWG rides the SAME xray routing engine through a separate loopback inbound.
- Policy routing scopes **only** `10.66.66.0/24` into a dedicated table (`100`). NEVER add/replace a default route in table `main`. NEVER touch Hub's own egress.
- The tun device (`awgtun`, `198.18.0.1/15` RFC2544 benchmark range — cannot collide with `10.66.66.0/24`, Hub LAN, or Hysteria2's `192.168.x` mux) is referenced ONLY by table `100`.

---

#### Step 0 — Add an additive SOCKS5 inbound to Hub xray (the split entry point)

This is the inbound `tun2socks` feeds into. Because Hub xray's routing rules are global (`inboundTag: null`), traffic entering here is split RU-direct / rest-Exit automatically — no per-inbound rule needed. `sniffing` is mandatory: it recovers the destination domain from the TLS SNI / HTTP Host so the **geosite** (domain) rules fire, not just geoip.

**Files:** Modify `server/backend/cmd/api/xray_sync.go` — the `inbounds := []map[string]interface{}{...}` slice (starts ~line 213; `vless-reality`, `vless-xhttp`, `shadowsocks` entries each already carry a `sniffing` block).

- [ ] **Step 0.1: Read the current inbound slice + the api-in append pattern**
  ```bash
  cd C:/Users/alex/Documents/heyearthproject/server/backend
  sed -n '213,300p' cmd/api/xray_sync.go      # the literal inbounds slice
  sed -n '590,600p' cmd/api/xray_sync.go       # how api-in is appended (mirror this append style)
  ```
  Expected: the slice literal with `"sniffing": map[string]interface{}{"enabled": true, "destOverride": []string{"http", "tls", "quic"}}` on the vless entries; `api-in` appended later via `inbounds = append(inbounds, map[string]interface{}{...})`.

- [ ] **Step 0.2: Append the `socks-awg` inbound (mirror the api-in append, place it right after the api-in append block ~line 595)**

OLD (the api-in append, ~lines 591-595 — quote the real lines you read in 0.1):
```go
	// dokodemo-door inbound для gRPC API доступа к статистике
	inbounds = append(inbounds, map[string]interface{}{
		"tag": "api-in", "listen": "127.0.0.1", "port": 10085, "protocol": "dokodemo-door",
```

NEW — leave the api-in append exactly as-is, and ADD this block immediately after it closes (after the `})` that ends the api-in append; match the real closing line):
```go
	// SOCKS5 inbound for AmneziaWG (Palma) egress. AWG's tun2socks
	// (hev-socks5-tunnel) dials this on loopback; traffic then flows through
	// the SAME global routing rules as Reality (inboundTag: null) → RU domains
	// + RU geoip go to direct-freedom (Hub Moscow), everything else to the
	// exit-balancer (Amsterdam). sniffing recovers the domain from TLS SNI /
	// HTTP Host so geosite (domain) rules fire, matching Happ's split.
	// Loopback-only (127.0.0.1) — never exposed to the network.
	inbounds = append(inbounds, map[string]interface{}{
		"tag":      "socks-awg",
		"listen":   "127.0.0.1",
		"port":     10808,
		"protocol": "socks",
		"settings": map[string]interface{}{
			"auth": "noauth",
			"udp":  true,
			"ip":   "127.0.0.1",
		},
		"sniffing": map[string]interface{}{
			"enabled":      true,
			"destOverride": []string{"http", "tls", "quic"},
			"routeOnly":    true,
		},
	})
```

> `routeOnly: true` makes sniffing affect ROUTING only (pick direct vs exit by domain) without rewriting the dialled address — the connection still dials the original IP, we just classify it. `udp: true` carries DNS (53) + QUIC so name-based rules and UDP apps work.

- [ ] **Step 0.3: Build**
  ```bash
  cd C:/Users/alex/Documents/heyearthproject/server/backend
  go build ./cmd/api/...
  echo "BUILD_EXIT=$?"
  ```
  Expected: `BUILD_EXIT=0`.

- [ ] **Step 0.4: Commit (the xray inbound is deployed with the T8 backend push; commit now so it's atomic with the rest)**
  ```bash
  cd C:/Users/alex/Documents/heyearthproject
  git add server/backend/cmd/api/xray_sync.go
  git commit -m "$(cat <<'EOF'
feat(palma): additive socks-awg inbound on Hub xray for split egress

AWG's tun2socks dials this loopback SOCKS5 (127.0.0.1:10808); traffic then
inherits xray's existing GLOBAL routing rules (inboundTag:null) — RU domains
+ RU geoip → direct-freedom (Hub Moscow), rest → exit-balancer (Amsterdam).
Gives AmneziaWG the same domain+geoip split as Reality/Happ. Additive only:
Reality inbounds, routing rules, and outbounds are untouched.

Beads: heyearthproject-98ng (T2)

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
  ```

---

#### Pre-flight: confirm the xray split is live before attaching AWG

The egress now targets xray's `socks-awg` inbound (127.0.0.1:10808). Verify (after the Step-0 binary is deployed in T8) that xray both exposes it AND splits correctly:

- [ ] **Confirm the socks-awg inbound is listening:**
  ```bash
  go run ./cmd/deploy --ssh 'ss -ltnp | grep 127.0.0.1:10808 ; jq -r ".inbounds[] | select(.tag==\"socks-awg\") | {tag,port,listen}" /opt/heyearth/xray/config.json'
  ```
  Expected: a `LISTEN 127.0.0.1:10808` line and the inbound present in the generated config.
- [ ] **Confirm xray splits a foreign dest to Amsterdam and a RU dest direct, THROUGH the new inbound:**
  ```bash
  # Foreign → must be Amsterdam Exit IP:
  go run ./cmd/deploy --ssh 'curl -s --max-time 8 --socks5-hostname 127.0.0.1:10808 https://api.ipify.org ; echo'
  # RU site → must be the Hub IP 198.51.100.236 (direct-freedom), NOT Amsterdam:
  go run ./cmd/deploy --ssh 'curl -s --max-time 8 --socks5-hostname 127.0.0.1:10808 https://api-maps.yandex.ru >/dev/null && curl -s --max-time 8 --socks5-hostname 127.0.0.1:10808 "https://yandex.ru" -o /dev/null -w "%{remote_ip}\n"'
  ```
  Expected: the foreign curl returns the **Amsterdam Exit IP**; the Yandex `remote_ip` resolves to a **Yandex/RU address reached directly** (proving RU traffic took `direct-freedom`, not the Exit). If both go to Amsterdam, the global rules aren't applying to the new inbound — STOP and check `routeOnly`/sniffing. (This pair is the split money-shot at the SOCKS layer, before the client even connects.)

---

#### Step 1 — Create `server/backend/cmd/deploy/awg_exit_route.go`

- [ ] Create the file with this exact content:

```go
// server/backend/cmd/deploy/awg_exit_route.go
package main

// AmneziaWG egress router (Palma — Task 2).
//
// By default AWG peers in 10.66.66.0/24 (awg0) would NAT out of the Hub =
// Russian IP. We instead route ONLY that subnet into the Hub's own xray via
// a loopback SOCKS5 inbound (127.0.0.1:10808, added in Step 0). xray then
// applies its GLOBAL split routing — RU domains+geoip → direct-freedom (Hub
// Moscow), everything else → exit-balancer (Amsterdam) — so AWG inherits the
// same per-site split as Reality/Happ: Russian sites direct, foreign via Exit.
//
// Mechanism: hev-socks5-tunnel (userspace TUN->SOCKS5, TCP+UDP via SOCKS5
// UDP ASSOCIATE) on tun device `awgtun`, plus policy routing:
//   ip rule add from 10.66.66.0/24 lookup 100
//   ip route add default dev awgtun table 100
//
// RED LINE: never touches table `main`; Reality's xray->Hysteria2->Exit path
// is untouched. tun2socks feeds xray's additive socks-awg inbound only.
//
// Mirrors amneziawg_hub.go: SSH-heredoc, idempotent, WARN-on-error.

import (
	"fmt"
	"strings"

	"golang.org/x/crypto/ssh"
)

// hevSocks5TunnelVersion pins the upstream git tag (source-only; we
// git clone --recursive + make). Stable as of rollout.
const hevSocks5TunnelVersion = "2.7.7"

// awgXraySocksPort is the loopback SOCKS5 inbound we add to Hub xray in Step 0.
// AWG egress targets THIS (not Hysteria2 directly) so xray applies its global
// split routing (RU → direct-freedom, rest → exit-balancer). Fixed value — it's
// our own inbound, not the dynamic per-exit Hysteria2 port.
const awgXraySocksPort = 10808

const (
	awgTunDevice  = "awgtun"
	awgTunIPv4    = "198.18.0.1" // RFC2544 benchmark range — cannot collide with 10.66.66/24 or Hub LAN
	awgSubnet     = "10.66.66.0/24"
	awgRouteTable = 100
)

// setupAWGExitRoute installs hev-socks5-tunnel + policy routing so the AWG
// subnet egresses through the Exit-node SOCKS5. Idempotent.
func setupAWGExitRoute(client *ssh.Client, cfg *deployConfig) {
	fmt.Println("[awg-route] Настройка egress AmneziaWG через Exit-ноду (tun2socks + policy routing)...")

	// Egress target is our own xray socks-awg inbound (Step 0), which performs
	// the RU-direct / rest-Exit split. Fixed port, no env override needed.
	socksPort := fmt.Sprintf("%d", awgXraySocksPort)

	script := `set -e

SOCKS_PORT="__SOCKS_PORT__"
TUN="__TUN__"
TUN_IP="__TUN_IP__"
SUBNET="__SUBNET__"
TABLE="__TABLE__"
VERSION="__VERSION__"
BIN=/opt/heyearth/awg/hev-socks5-tunnel
CONF=/opt/heyearth/awg/awgtun.yaml

# ── 1. Build hev-socks5-tunnel from source (no prebuilt releases) ──
if [ -x "$BIN" ]; then
  echo "[awg-route] Binary already present at $BIN — keeping existing install"
else
  apt-get install -y -qq git build-essential ca-certificates 2>/dev/null || true
  SRC=/tmp/hev-socks5-tunnel-src
  rm -rf "$SRC"
  echo "[awg-route] Cloning hev-socks5-tunnel @ $VERSION (with submodules) ..."
  git clone --recursive --depth 1 --branch "$VERSION" https://github.com/heiher/hev-socks5-tunnel.git "$SRC"
  echo "[awg-route] Building (make) ..."
  ( cd "$SRC" && make -j"$(nproc)" )
  install -m 0755 "$SRC/bin/hev-socks5-tunnel" "$BIN"
  rm -rf "$SRC"
  echo "[awg-route] Binary built at $BIN ($(du -h "$BIN" | cut -f1))"
fi

# ── 2. Config (hev-socks5-tunnel creates+brings up the tun itself & assigns ipv4) ──
# udp: 'udp' => relay UDP over SOCKS5 UDP ASSOCIATE (DNS 53 + QUIC reach Exit).
cat > "$CONF" <<CONFEOF
tunnel:
  name: $TUN
  mtu: 8500
  ipv4: $TUN_IP

socks5:
  address: 127.0.0.1
  port: $SOCKS_PORT
  udp: 'udp'
CONFEOF
echo "[awg-route] Wrote $CONF (upstream SOCKS5 127.0.0.1:$SOCKS_PORT, udp=udp)"

# ── 3. systemd unit (Restart=always) ──
cat > /etc/systemd/system/awg-tun2socks.service <<UNIT
[Unit]
Description=AmneziaWG egress tun2socks (hev-socks5-tunnel -> Exit SOCKS5)
Documentation=https://github.com/heiher/hev-socks5-tunnel
After=network-online.target awg.service
Wants=network-online.target

[Service]
Type=simple
ExecStart=$BIN $CONF
# Policy routing: ONLY the AWG subnet uses table $TABLE -> awgtun. Idempotent
# (|| true on add; we never delete table main). rp_filter=2 (loose) so the
# asymmetric return path via awgtun is not dropped.
ExecStartPost=/bin/sh -c 'for i in 1 2 3 4 5; do ip link show $TUN >/dev/null 2>&1 && break; sleep 0.5; done; \
  sysctl -w net.ipv4.conf.all.rp_filter=2 >/dev/null; \
  sysctl -w net.ipv4.conf.$TUN.rp_filter=2 >/dev/null 2>&1 || true; \
  ip route replace default dev $TUN table $TABLE; \
  ip rule show | grep -q "from $SUBNET lookup $TABLE" || ip rule add from $SUBNET lookup $TABLE'
ExecStopPost=/bin/sh -c 'ip rule del from $SUBNET lookup $TABLE 2>/dev/null || true; \
  ip route flush table $TABLE 2>/dev/null || true'
Restart=always
RestartSec=5
User=root
AmbientCapabilities=CAP_NET_ADMIN CAP_NET_RAW
CapabilityBoundingSet=CAP_NET_ADMIN CAP_NET_RAW
NoNewPrivileges=true

[Install]
WantedBy=multi-user.target
UNIT

# ── 4. Persist rp_filter across reboots (unit also sets it, belt+braces) ──
cat > /etc/sysctl.d/99-awg-tun2socks.conf <<SYSCTL
# AmneziaWG egress via tun2socks needs loose reverse-path filtering so the
# asymmetric return path through awgtun is not dropped. Scoped intent only.
net.ipv4.conf.all.rp_filter = 2
SYSCTL
sysctl --system >/dev/null 2>&1 || true

# ── 5. Enable + (re)start ──
systemctl daemon-reload
systemctl enable awg-tun2socks >/dev/null 2>&1
systemctl restart awg-tun2socks
sleep 2

# ── 6. Health check ──
if ip link show "$TUN" >/dev/null 2>&1; then
  echo "[awg-route] tun device $TUN is present:"
  ip -br addr show "$TUN" || true
else
  echo "[awg-route] WARN: tun device $TUN not up yet — check: journalctl -u awg-tun2socks -n 30"
fi
echo "[awg-route] ip rule (expect 'from $SUBNET lookup $TABLE'):"
ip rule show | grep "lookup $TABLE" || echo "[awg-route] WARN: policy rule missing"
echo "[awg-route] table $TABLE routes (expect 'default dev $TUN'):"
ip route show table "$TABLE" || echo "[awg-route] WARN: table $TABLE empty"

echo "[awg-route] Setup complete."
`
	repl := strings.NewReplacer(
		"__SOCKS_PORT__", socksPort,
		"__TUN__", awgTunDevice,
		"__TUN_IP__", awgTunIPv4,
		"__SUBNET__", awgSubnet,
		"__TABLE__", fmt.Sprintf("%d", awgRouteTable),
		"__VERSION__", hevSocks5TunnelVersion,
	)
	script = repl.Replace(script)

	out, err := runSSH(client, script, true)
	if err != nil {
		fmt.Println(out)
		fmt.Printf("[awg-route] WARN: setup script returned error: %v\n", err)
		return
	}
	fmt.Println(out)
	fmt.Println("[awg-route] Готово. Проверь: go run ./cmd/deploy --status и curl с подключённого AWG-клиента.")
}
```

> Notes on the choices above (so the executor doesn't second-guess them):
> - `mtu: 8500` matches hev-socks5-tunnel's documented default; AWG's own MTU (`awg0`, typically 1280–1420) is the real bottleneck so this never fragments.
> - hev-socks5-tunnel **creates and ups the tun + assigns `ipv4`** itself (verified from upstream README), so we do NOT run `ip addr add` / `ip link set up` — only the routing in `ExecStartPost`.
> - `ip route replace` (not `add`) + the `grep || ip rule add` guard make re-runs idempotent without erroring on "File exists".
> - `socks5.udp: 'udp'` is the line that carries DNS (UDP 53) and any UDP app traffic over SOCKS5 UDP ASSOCIATE to the Exit.

#### Step 2 — Wire it into the `--setup-awg` flow

- [ ] In `server/backend/cmd/deploy/main.go`, edit `case "setup-awg":` (currently line ~242) so one command provisions AWG **and** its egress route:

```go
	case "setup-awg":
		setupAmneziaWGHub(client, cfg)
		setupAWGExitRoute(client, cfg)
```

> Single sub-step is intentional: `setupAmneziaWGHub` is idempotent (keeps existing binary, only enables the unit), so re-running `--setup-awg` to (re)apply routing is safe. No new top-level flag needed.

#### Step 3 — Build locally (evidence gate)

- [ ] `cd server/backend && go build ./...`
  Expected: no output, exit 0. (Confirms the new file compiles and `cfg.env.get` / `runSSH` signatures match.)
- [ ] `cd server/backend && gofmt -l cmd/deploy/awg_exit_route.go`
  Expected: no output (file already gofmt-clean).

#### Step 4 — Deploy

- [ ] `cd server/backend && go run ./cmd/deploy --setup-awg`
  Expected tail: `[awg-route] tun device awgtun is present:` followed by an `awgtun ... 198.18.0.1/...` line, an `ip rule` line containing `from 10.66.66.0/24 lookup 100`, a table-100 line `default dev awgtun`, then `[awg-route] Setup complete.` No `WARN`.

#### Step 5 — Verify on the Hub (process + interface + routing)

- [ ] tun2socks running + tun up:
  ```bash
  go run ./cmd/deploy --ssh 'systemctl is-active awg-tun2socks ; ps -o pid,cmd -C hev-socks5-tunnel ; ip -br link show awgtun ; ss -unlp | grep hev-socks5-tunnel || true'
  ```
  Expected: `active`; a `hev-socks5-tunnel /opt/heyearth/awg/awgtun.yaml` process; `awgtun ... UP` (or `UNKNOWN`, which is normal for tun); a UDP socket owned by the process (the SOCKS UDP-associate client side).
- [ ] Policy rule + dedicated table populated, **and main table untouched**:
  ```bash
  go run ./cmd/deploy --ssh 'echo "== ip rule =="; ip rule show; echo "== table 100 =="; ip route show table 100; echo "== table main default =="; ip route show table main default'
  ```
  Expected: `ip rule` lists `from 10.66.66.0/24 lookup 100`; table 100 shows `default dev awgtun`; **table main default is the ORIGINAL Hub default (`default via <hub-gw> dev eth0`)**, NOT awgtun. (This line is the RED-LINE guard: if `awgtun` appears in table main, STOP and roll back.)

#### Step 6 — Money-shot: AWG client gets the SAME split as Happ (foreign→Amsterdam, RU→direct)

This is the proof the optimal split works end-to-end. On a device connected to AmneziaWG (peer in `10.66.66.0/24`):

- [ ] **Foreign site → Amsterdam Exit IP:**
  ```bash
  curl -s https://api.ipify.org ; echo
  curl -s https://ifconfig.me ; echo
  ```
  Expected: BOTH return the **Exit/Amsterdam IP** — the SAME IP the pre-flight `--socks5-hostname 127.0.0.1:10808` foreign curl returned — **NOT** the Hub IP `198.51.100.236`.
- [ ] **Russian site → direct (Hub Moscow), NOT Amsterdam** — this is what makes it «как Happ»:
  ```bash
  # ipinfo via a RU-geosite/geoip dest: check the apparent egress for a RU service.
  # 2ip.ru reports your visible IP; reached over a RU-routed path it should show the
  # Hub (198.51.100.236), not the Amsterdam Exit.
  curl -s "https://2ip.ru/" -o /dev/null -w "reached RU site, egress via Hub expected\n"
  curl -s https://api.ipify.org ; echo   # contrast: still Amsterdam for foreign
  ```
  Expected: Russian/geosite destinations are reached over `direct-freedom` (Hub Moscow IP 198.51.100.236 visible to RU sites), while foreign destinations stay on the Amsterdam Exit. If a RU site shows the Amsterdam IP, the split isn't applying — re-check the Step-0 pre-flight Yandex `remote_ip` test.
- [ ] **DNS through the tunnel (UDP 53 over SOCKS UDP ASSOCIATE):**
  ```bash
  # On the AWG client (DNS in its .conf, e.g. 1.1.1.1):
  nslookup api.ipify.org    # resolves via conf DNS, no timeout
  curl -s https://api.ipify.org/?format=json ; echo   # name resolution succeeds
  ```
  Expected: resolution succeeds. (If TCP worked but DNS hangs, the `udp: 'udp'` path is the suspect — re-check Step 1 config and `journalctl -u awg-tun2socks`.)

#### Step 7 — Confirm Reality is UNCHANGED (RED-LINE regression check)

- [ ] Reality's own Hysteria2 SOCKS path still egresses Amsterdam (unchanged — Reality dials Hysteria2 directly, not our new xray inbound):
  ```bash
  go run ./cmd/deploy --ssh 'curl -s --max-time 8 --socks5-hostname 127.0.0.1:1090 https://api.ipify.org ; echo'
  ```
  Expected: Amsterdam IP — adding the `socks-awg` inbound + tun2socks did not disturb Reality's path (the new inbound only ADDS an entry point; Reality's vless inbounds and the Hysteria2 outbound are untouched).
- [ ] Xray still accepting Reality, and the new inbound didn't break the config (quick log sanity):
  ```bash
  go run ./cmd/deploy --ssh 'systemctl is-active xray ; journalctl -u xray -n 8 --no-pager | grep -iE "accepted|error|failed" | tail'
  ```
  Expected: `active`; recent `accepted` lines flowing; no spike of new errors after the `socks-awg` inbound was added.
- [ ] `go run ./cmd/deploy --status`
  Expected: heyearth-api, xray, hysteria2-server, awg, and (newly) awg-tun2socks all healthy.

#### Step 8 — Commit

- [ ] Stage + commit on a feature branch (branch first if on `main`):
  ```bash
  git add server/backend/cmd/deploy/awg_exit_route.go server/backend/cmd/deploy/main.go
  git commit -m "$(cat <<'EOF'
feat(palma): AmneziaWG egress via xray split-routing (hev-socks5-tunnel)

AWG subnet 10.66.66.0/24 is policy-routed into the Hub's own xray through
a loopback socks-awg inbound (127.0.0.1:10808), so it inherits xray's GLOBAL
split: RU domains+geoip → direct-freedom (Hub Moscow), rest → exit-balancer
(Amsterdam). Same per-site behaviour as Happ — Russian sites direct (fast,
RU IP), foreign sites via Exit. Userspace TUN->SOCKS5 (TCP+UDP via UDP
ASSOCIATE) on awgtun; table 100 scoped to the AWG subnet only — table main
and Reality's xray->Hysteria2 path untouched.

Wired into --setup-awg. Verified: AWG client foreign curl = Amsterdam,
RU site = Hub direct; Reality SOCKS curl unchanged; table main intact.

Beads: heyearthproject-98ng (T2)

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
  ```

---

#### Rollback (full, leaves Reality and Hub routing pristine)

Removing the egress route reverts AWG peers to NAT-out-of-Hub (Russian IP) but does NOT touch Reality or the main routing table.

- [ ] Stop + disable the unit (its `ExecStopPost` already removes the rule + flushes table 100):
  ```bash
  go run ./cmd/deploy --ssh 'systemctl disable --now awg-tun2socks'
  ```
- [ ] Belt-and-braces: explicitly delete the policy rule and flush the table in case the unit died uncleanly:
  ```bash
  go run ./cmd/deploy --ssh 'ip rule del from 10.66.66.0/24 lookup 100 2>/dev/null || true; ip route flush table 100 2>/dev/null || true; ip link delete awgtun 2>/dev/null || true'
  ```
- [ ] Remove unit + config + sysctl drop-in (optional, full teardown):
  ```bash
  go run ./cmd/deploy --ssh 'rm -f /etc/systemd/system/awg-tun2socks.service /opt/heyearth/awg/awgtun.yaml /etc/sysctl.d/99-awg-tun2socks.conf; systemctl daemon-reload; sysctl -w net.ipv4.conf.all.rp_filter=1 >/dev/null; sysctl --system >/dev/null 2>&1 || true'
  ```
- [ ] Verify rollback: `ip rule show` no longer lists `lookup 100`; `ip route show table 100` is empty; table main default unchanged; Reality SOCKS curl (Step 7) still returns Amsterdam.
  ```bash
  go run ./cmd/deploy --ssh 'ip rule show | grep "lookup 100" || echo "rule gone (ok)"; ip route show table 100 || echo "table 100 empty (ok)"; ip route show table main default'
  ```
- [ ] The Step-0 `socks-awg` xray inbound is loopback-only and harmless once tun2socks is gone (nothing dials it). Leave it, OR for a full teardown `git revert` the Step-0.4 commit and redeploy the backend. No urgency — it never accepts non-loopback traffic.
### Task 3: Fix awg_sync.go — apply via awg, real cold-start, traffic enforcement

**File:** `server/backend/cmd/api/awg_sync.go` (single file, 318 lines)
**Bug:** line 144 uses `wg syncconf` (wireguard-tools) which rejects AmneziaWG CPS params (Jc/Jmin/Jmax/S1/S2/H1-H4); the fallback `systemctl restart awg` starts a bare daemon with an empty TUN — config is never applied.
**Beads:** `heyearthproject-98ng (T3)`. **RED LINE:** do not touch xray/Reality/Hysteria2 sync paths.

**Apply command chosen (temp-file strip, no shell):**
`awg-quick strip /opt/heyearth/awg/awg0.conf > /opt/heyearth/awg/awg0.stripped` then `awg syncconf awg0 /opt/heyearth/awg/awg0.stripped`.

---

#### Step 1 — Add a strip-temp-file constant next to the other path consts

- [ ] Edit the `const (...)` block (lines 35-41). Add `awgStrippedPath` so the strip target is a named, self-documenting path (not a magic string).

OLD (lines 35-41):
```go
const (
	awgConfigPath = "/opt/heyearth/awg/awg0.conf"
	awgConfigDir  = "/opt/heyearth/awg"
	awgInterface  = "awg0"
	awgListenPort = 51820
	awgServerIP   = "10.66.66.1/24"
)
```

NEW:
```go
const (
	awgConfigPath   = "/opt/heyearth/awg/awg0.conf"
	awgStrippedPath = "/opt/heyearth/awg/awg0.stripped"
	awgConfigDir    = "/opt/heyearth/awg"
	awgInterface    = "awg0"
	awgListenPort   = 51820
	awgServerIP     = "10.66.66.1/24"
)
```

---

#### Step 2 — Replace the reload block: `awg syncconf` + real `awg-quick` cold-start

- [ ] Replace the layered-reload block (lines 143-150). Strip with `awg-quick strip` to a temp file, apply with `awg syncconf awg0 <stripped>`, and on failure do `awg-quick down awg0` (best-effort) then `awg-quick up awg0`. Use `amneziawg-tools`, not wireguard-tools, and never a bare `systemctl restart awg`.

OLD (lines 143-150):
```go
	// Layered reload: wg syncconf (zero-downtime) → systemctl restart (cold-start).
	if out, err := exec.Command("wg", "syncconf", awgInterface, awgConfigPath).CombinedOutput(); err != nil {
		log.Printf("[awg-sync] wg syncconf failed (%v: %s) — falling back to systemctl restart awg",
			err, strings.TrimSpace(string(out)))
		if out2, err2 := exec.Command("systemctl", "restart", "awg").CombinedOutput(); err2 != nil {
			return fmt.Errorf("restart awg: %w (%s)", err2, strings.TrimSpace(string(out2)))
		}
	}
```

NEW:
```go
	// Layered reload via amneziawg-tools (NOT wireguard-tools: plain `wg` rejects
	// the CPS params Jc/Jmin/Jmax/S1/S2/H1-H4). awg-quick strip drops [Interface]
	// directives that syncconf cannot accept; awg syncconf applies live (no-downtime).
	if err := applyAWGConfig(); err != nil {
		return err
	}
```

---

#### Step 3 — Add the `applyAWGConfig` helper (strip → syncconf → awg-quick cold-start)

- [ ] Add this helper immediately after `SyncAWGConfig` (insert after the closing `}` of `SyncAWGConfig`, line 163). It centralises the reload so the periodic loop and debounced sync share one code path.

NEW (add after line 163):
```go
// applyAWGConfig reloads awg0 from awgConfigPath using amneziawg-tools.
//
// Primary path (no-downtime): awg-quick strip awg0.conf > awg0.stripped, then
// awg syncconf awg0 awg0.stripped. We strip to a temp file rather than using
// bash process substitution `<(...)` because Go's exec runs no shell.
//
// Cold-start fallback: awg-quick down awg0 (best-effort) then awg-quick up awg0,
// which actually brings the TUN up WITH the config. A bare `systemctl restart awg`
// would only start an empty interface, so we never use it here.
func applyAWGConfig() error {
	stripped, err := exec.Command("awg-quick", "strip", awgConfigPath).Output()
	if err != nil {
		log.Printf("[awg-sync] awg-quick strip failed: %v — cold-start via awg-quick up", err)
		return coldStartAWG()
	}
	if werr := os.WriteFile(awgStrippedPath, stripped, 0o640); werr != nil {
		return fmt.Errorf("write stripped awg config: %w", werr)
	}
	defer os.Remove(awgStrippedPath)
	if cerr := chownToAmneziaWG(awgStrippedPath); cerr != nil {
		log.Printf("[awg-sync] chown stripped config: %v (continuing)", cerr)
	}

	if out, err := exec.Command("awg", "syncconf", awgInterface, awgStrippedPath).CombinedOutput(); err != nil {
		log.Printf("[awg-sync] awg syncconf failed (%v: %s) — cold-start via awg-quick up",
			err, strings.TrimSpace(string(out)))
		return coldStartAWG()
	}
	return nil
}

// coldStartAWG brings awg0 fully down then up with the on-disk config.
// `down` is best-effort (interface may already be absent); `up` must succeed.
func coldStartAWG() error {
	if out, err := exec.Command("awg-quick", "down", awgInterface).CombinedOutput(); err != nil {
		log.Printf("[awg-sync] awg-quick down %s: %v (%s) — continuing to up",
			awgInterface, err, strings.TrimSpace(string(out)))
	}
	if out, err := exec.Command("awg-quick", "up", awgInterface).CombinedOutput(); err != nil {
		return fmt.Errorf("awg-quick up %s: %w (%s)", awgInterface, err, strings.TrimSpace(string(out)))
	}
	log.Printf("[awg-sync] cold-start complete: awg-quick up %s", awgInterface)
	return nil
}
```

Note: `awg-quick up/down` reads the config from `/etc/amnezia/amneziawg/awg0.conf`, which Phase A symlinks to `awgConfigPath`. The strip path passes `awgConfigPath` directly, so both work post-T1.

---

#### Step 4 — Enforce the traffic limit in peer selection

- [ ] Add the traffic-limit guard to the peer SELECT (the `WHERE` at lines 90-95). An over-limit user's peer is then absent from the next regenerated config and dropped on the next `awg syncconf`/cold-start.

OLD (lines 90-95):
```go
		WHERE p.amnezia_wg_enabled = TRUE
		  AND us.status = 'active'
		  AND us.valid_until > NOW()
		  AND u.awg_public_key IS NOT NULL
		  AND u.awg_public_key <> ''
		  AND u.awg_ip_address IS NOT NULL
```

NEW:
```go
		WHERE p.amnezia_wg_enabled = TRUE
		  AND us.status = 'active'
		  AND us.valid_until > NOW()
		  AND (us.traffic_limit IS NULL OR us.traffic_used < us.traffic_limit)
		  AND u.awg_public_key IS NOT NULL
		  AND u.awg_public_key <> ''
		  AND u.awg_ip_address IS NOT NULL
```

Reads `us.traffic_limit` / `us.traffic_used` from `user_subscriptions` (alias `us`). NULL `traffic_limit` = unlimited plan ⇒ peer always kept. Confirm both columns exist on `user_subscriptions` before building (grep migrations); if either lives on a different table, adjust the alias accordingly — do NOT invent column names.

---

#### Step 5 — Update the file header comment to match reality

- [ ] Fix the stale apply description in the package doc comment (lines 12-13) so it no longer claims `wg syncconf` / `systemctl restart awg`.

OLD (lines 12-13):
```go
// in main.go alongside Xray + Hysteria2). We debounce 2s, then regenerate
// /opt/heyearth/awg/awg0.conf and apply via `wg syncconf` (no-downtime) or
// `systemctl restart awg` (cold-start fallback).
```

NEW:
```go
// in main.go alongside Xray + Hysteria2). We debounce 2s, then regenerate
// /opt/heyearth/awg/awg0.conf and apply via amneziawg-tools: `awg syncconf awg0`
// over a stripped config (no-downtime) with `awg-quick down/up awg0` cold-start.
```

---

#### Step 6 — Evidence: compile

- [ ] Build the api package. Must exit 0 with no output.
```bash
cd C:/Users/alex/Documents/heyearthproject/server/backend && go build ./cmd/api/...
```
- [ ] If the column-existence check in Step 4 was uncertain, also vet:
```bash
cd C:/Users/alex/Documents/heyearthproject/server/backend && go vet ./cmd/api/...
```

#### Step 7 — Evidence: runtime verification (post-deploy, on Hub)

No tests in repo — verify against the live Hub after `go run ./cmd/deploy --update-main && go run ./cmd/deploy --restart`:
- [ ] Peers are actually present (proves config applied, not an empty TUN):
```bash
cd C:/Users/alex/Documents/heyearthproject/server/backend && go run ./cmd/deploy --ssh "awg show awg0"
```
  Expect one `peer:` block per active AmneziaWG user, with non-zero `latest handshake` for connected clients.
- [ ] Over-limit drop: pick a user, force `traffic_used >= traffic_limit` (or wait for collector), trigger a sync (plan/subscription mutation, or wait ≤5 min for the periodic loop), then re-run `awg show awg0` — that peer's public key must be gone.
- [ ] Cold-start path: `go run ./cmd/deploy --ssh "awg-quick down awg0"` then trigger a sync; `awg show awg0` must list peers again (proves `coldStartAWG` brings the TUN up with config, not empty).

#### Step 8 — Commit

- [ ] Stage only `awg_sync.go` and commit.
```bash
cd C:/Users/alex/Documents/heyearthproject && git add server/backend/cmd/api/awg_sync.go && git commit -m @'
fix(awg): apply via amneziawg-tools, real cold-start, enforce traffic limit

awg_sync.go applied the config with `wg syncconf` (wireguard-tools), which
rejects AmneziaWG CPS params (Jc/Jmin/Jmax/S1/S2/H1-H4); the fallback
`systemctl restart awg` only started a bare daemon with an empty TUN, so the
config was never applied.

- apply via amneziawg-tools: awg-quick strip awg0.conf > awg0.stripped, then
  awg syncconf awg0 awg0.stripped (temp file, no bash process substitution)
- real cold-start fallback: awg-quick down awg0 || true; awg-quick up awg0
- peer SELECT now drops over-limit users
  (traffic_limit IS NULL OR traffic_used < traffic_limit)

Beads: heyearthproject-98ng (T3)

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
'@
```
### Task 4: AWG traffic collector (awg_stats.go)

**Files:**
- Create: `server/backend/cmd/api/awg_stats.go`
- Modify: `server/backend/cmd/api/main.go:895` (wire `StartAWGStatsLoop()` next to `StartHysteria2StatsLoop()`)

**Context for the implementer:**
- Mirrors `cmd/api/hysteria2_stats.go` (cumulative→delta→INSERT, reset handling, silent-skip-if-absent) BUT writes to BOTH sinks like the Xray collector (`cmd/api/traffic_stats.go:108-123`): `traffic_stats` (for `biz.IsCycleExceeded` + cabinet TrafficCard) AND `user_subscriptions.traffic_used` (the counter T3's peer-selection gates on). Hysteria2 writes only `traffic_stats`; AWG must write both because T3 enforces via `us.traffic_used < us.traffic_limit`.
- heyearth-api runs ON the Hub, so stats are read with a LOCAL `exec.Command("awg","show","awg0","transfer")` (Xray collector also reads locally; hysteria2 uses loopback HTTP — we mirror the local-exec approach since `awg` is a CLI).
- `awg show <iface> transfer` emits one line per peer: `<public-key>\t<rx_bytes>\t<tx_bytes>` where **rx = bytes the interface RECEIVED from peer = user UPLOAD**, **tx = bytes the interface SENT to peer = user DOWNLOAD**. (Server-side perspective — opposite field-naming to the hysteria2 endpoint, which is client-side. Get this right or upload/download are swapped.)
- pubkey→user mapping: `users.awg_public_key` → `users.user_id` (the UUID string used as `traffic_stats.user_id`, same key Xray/Hysteria2 use). Reload the map every tick so newly-provisioned peers are picked up.
- `awgInterface` const (`"awg0"`) already exists in `awg_sync.go:38` (same package `main`) — reuse it, do NOT redeclare.

- [ ] **Step 1: Verify the two write targets exist (no code, just confirm before building)**

Run:
```bash
cd C:/Users/alex/Documents/heyearthproject/server/backend
grep -n "awgInterface" cmd/api/awg_sync.go
grep -n "INSERT INTO traffic_stats\|SET traffic_used = traffic_used" cmd/api/traffic_stats.go
```
Expected: `awgInterface = "awg0"` at awg_sync.go:38; both write statements present in traffic_stats.go (the dual-write pattern we mirror). Confirms `traffic_stats(user_id, upload_bytes, download_bytes)` and `user_subscriptions.traffic_used` are the targets.

- [ ] **Step 2: Create `cmd/api/awg_stats.go` with full content**

Create `server/backend/cmd/api/awg_stats.go`:

```go
package main

// AmneziaWG (Palma) per-peer traffic collector.
//
// amneziawg-go exposes cumulative per-peer byte counters via the `awg` CLI:
//   awg show awg0 transfer  →  <pubkey>\t<rx_bytes>\t<tx_bytes>
// where rx = bytes the awg0 interface RECEIVED from the peer (= user UPLOAD)
// and tx = bytes the interface SENT to the peer (= user DOWNLOAD).
//
// We poll every 60s, diff against the previous snapshot, and write deltas to
// BOTH sinks the Xray collector uses (cmd/api/traffic_stats.go):
//   1. traffic_stats(user_id, upload_bytes, download_bytes) — feeds
//      biz.IsCycleExceeded + the cabinet TrafficCard.
//   2. user_subscriptions.traffic_used += (up+down) — the counter awg_sync.go's
//      peer-selection query gates on (T3), so over-limit peers get dropped.
// This makes AWG share ONE subscription limit with Reality + Hysteria2.
//
// Counters reset to 0 when the interface is recreated (awg-quick down/up); we
// detect the negative delta and treat the current value as a new baseline,
// emitting no row for the reset tick (mirrors hysteria2_stats.go).
//
// Silent no-op when AWG isn't provisioned: `awg show` fails (no device) and we
// just skip the tick — no log spam, catches up once --setup-awg has run.

import (
	"log"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/heyearth/backend/internal/db"
)

const awgStatsPeriod = 60 * time.Second

type awgCounter struct {
	RX int64 // bytes received by awg0 from peer = user upload
	TX int64 // bytes sent by awg0 to peer     = user download
}

// awgStatsPrev — last cumulative snapshot keyed by peer public key.
// Package-scope singleton (collector is one goroutine), like hysteria2StatsPrev.
var awgStatsPrev = map[string]awgCounter{}

// StartAWGStatsLoop kicks off a 60s polling loop over `awg show awg0 transfer`.
// Safe to call when AWG isn't installed — every tick just silent-skips.
func StartAWGStatsLoop() {
	go func() {
		time.Sleep(25 * time.Second) // let awg.service settle after boot
		collectAWGStats()
		ticker := time.NewTicker(awgStatsPeriod)
		defer ticker.Stop()
		for range ticker.C {
			collectAWGStats()
		}
	}()
	log.Println("[awg-stats] collector started (every 60s)")
}

// pubkeyToUserID loads the current awg_public_key → users.user_id map.
// Reloaded each tick so freshly-provisioned peers are accounted immediately.
func pubkeyToUserID() (map[string]string, error) {
	rows, err := db.DB.Query(`
		SELECT awg_public_key, user_id
		FROM users
		WHERE awg_public_key IS NOT NULL AND awg_public_key <> ''
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	m := make(map[string]string)
	for rows.Next() {
		var pk, uid string
		if err := rows.Scan(&pk, &uid); err != nil {
			continue
		}
		m[pk] = uid
	}
	return m, nil
}

func collectAWGStats() {
	out, err := exec.Command("awg", "show", awgInterface, "transfer").CombinedOutput()
	if err != nil {
		// awg not installed / interface absent — silent skip, retry next tick.
		return
	}

	keyToUser, err := pubkeyToUserID()
	if err != nil {
		log.Printf("[awg-stats] load pubkey map: %v", err)
		return
	}

	cur := map[string]awgCounter{}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		f := strings.Fields(line) // <pubkey> <rx> <tx>
		if len(f) < 3 {
			continue
		}
		rx, err1 := strconv.ParseInt(f[1], 10, 64)
		tx, err2 := strconv.ParseInt(f[2], 10, 64)
		if err1 != nil || err2 != nil {
			continue
		}
		cur[f[0]] = awgCounter{RX: rx, TX: tx}
	}

	saved := 0
	for pubkey, c := range cur {
		prev := awgStatsPrev[pubkey]
		upDelta := c.RX - prev.RX   // user upload
		downDelta := c.TX - prev.TX // user download

		// Negative delta = interface recreated (counters reset). Re-baseline,
		// emit no row this tick.
		if upDelta < 0 || downDelta < 0 {
			continue
		}
		if upDelta == 0 && downDelta == 0 {
			continue
		}

		userID, ok := keyToUser[pubkey]
		if !ok {
			// Peer with no matching user (stale key) — skip.
			continue
		}

		if _, err := db.DB.Exec(`
			INSERT INTO traffic_stats (user_id, upload_bytes, download_bytes)
			VALUES ($1, $2, $3)
		`, userID, upDelta, downDelta); err != nil {
			log.Printf("[awg-stats] insert %s: %v", userID, err)
			continue
		}

		// Accumulate into the shared subscription counter (same as Xray collector).
		_, _ = db.DB.Exec(`
			UPDATE user_subscriptions us
			SET traffic_used = traffic_used + $1
			FROM users u
			WHERE u.id = us.user_id AND u.user_id = $2 AND us.valid_until > NOW()
		`, upDelta+downDelta, userID)

		saved++
	}

	if saved > 0 {
		log.Printf("[awg-stats] recorded deltas: %d peers", saved)
	}
	awgStatsPrev = cur
}
```

- [ ] **Step 3: Wire `StartAWGStatsLoop()` into main.go**

In `server/backend/cmd/api/main.go`, find (around line 892-895):

OLD:
```go
	// Hysteria2 per-user traffic collector — polls loopback trafficStats
	// endpoint every 60s and writes deltas into traffic_stats so cycle-quota
	// enforcement (biz.IsCycleExceeded) covers Express users transparently.
	StartHysteria2StatsLoop()
```

NEW:
```go
	// Hysteria2 per-user traffic collector — polls loopback trafficStats
	// endpoint every 60s and writes deltas into traffic_stats so cycle-quota
	// enforcement (biz.IsCycleExceeded) covers Express users transparently.
	StartHysteria2StatsLoop()

	// AmneziaWG (Palma) per-peer traffic collector — reads `awg show awg0
	// transfer` every 60s, writes deltas to traffic_stats AND
	// user_subscriptions.traffic_used so AWG shares one subscription limit
	// with Reality/Hysteria2 (T3 peer-selection gates on traffic_used).
	// No-op until --setup-awg has run on this Hub.
	StartAWGStatsLoop()
```

- [ ] **Step 4: Build**

Run:
```bash
cd C:/Users/alex/Documents/heyearthproject/server/backend
go build ./cmd/api/...
echo "BUILD_EXIT=$?"
```
Expected: `BUILD_EXIT=0`. (If `awgInterface` redeclare error — remove any local const, it lives in awg_sync.go.)

- [ ] **Step 5: Commit**

```bash
cd C:/Users/alex/Documents/heyearthproject
git add server/backend/cmd/api/awg_stats.go server/backend/cmd/api/main.go
git commit -m "$(cat <<'EOF'
feat(awg): per-peer traffic collector with shared subscription limit

New StartAWGStatsLoop() reads `awg show awg0 transfer` every 60s, diffs
cumulative per-peer counters (rx=upload, tx=download), and writes deltas to
BOTH traffic_stats (for IsCycleExceeded + cabinet TrafficCard) AND
user_subscriptions.traffic_used (the counter awg_sync peer-selection gates
on). AWG now shares ONE subscription limit with Reality + Hysteria2.

Reset handling mirrors hysteria2_stats.go (negative delta = re-baseline).
Silent no-op until --setup-awg provisions the Hub.

Beads: heyearthproject-98ng (T4)

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

- [ ] **Step 6: Verify after deploy (evidence — runs in T8 after binary is live)**

```bash
cd C:/Users/alex/Documents/heyearthproject/server/backend
# Cumulative counters visible on Hub:
go run ./cmd/deploy --ssh "awg show awg0 transfer"
# After a client pushes data, the user's shared counter grows:
go run ./cmd/deploy --ssh "sudo -u postgres psql -d heyearth -t -c \"SELECT u.user_id, us.traffic_used FROM users u JOIN user_subscriptions us ON us.user_id=u.id WHERE u.awg_public_key<>'' AND us.valid_until>NOW();\""
# Collector log line present:
go run ./cmd/deploy --ssh "journalctl -u heyearth-api --since '3 min ago' | grep awg-stats"
```
Expected: `awg show ... transfer` lists peers with nonzero rx/tx after use; `traffic_used` increases between two reads while a client is active; `[awg-stats] recorded deltas` appears in the log.
### Task 5: AWG config endpoint (/api/v1/user/awg-config + connections flag)

Adds the `amnezia_available` gating flag to the existing connections response and a new
authenticated `GET /api/v1/user/awg-config` that returns the per-user WG-INI text + an
`amneziawg://` deeplink. Backend infra (`biz.GetUserAWGConfig`, `RequestAWGSync`,
`awg.service` on :51820) already exists — this just exposes it over HTTP. Happ subscription
emit stays untouched (RED LINE).

**Files:** `server/backend/cmd/api/main.go` (handler + route), reuses `biz.GetUserAWGConfig`
(`internal/biz/awg.go`), `biz.GetUserPlan` (`internal/biz/plans.go:246`), `RequestAWGSync`
(`cmd/api/awg_sync.go:52`), `biz.AWGCPSParams` field names from `awg.go:29-39`.

- [ ] **5.1 — Add `amnezia_available` to the connections response.** In `main.go`
  `handleUserConnections` the response map is built at line 594. The flag must be true only
  when the user's plan has AmneziaWG enabled AND they can connect. `access.CanConnect`
  already encodes "active subscription" (it gates the whole `connection_options` block at
  line 617), and `biz.GetUserPlan` returns an error unless there is an active plan — so
  `plan.AmneziaWGEnabled && access.CanConnect` is the exact mirror of the existing
  `can_connect` semantics. Compute it inside the existing `if plan, err := ...` block so we
  reuse the single `GetUserPlan` call already there (line 604), then surface it as a flat
  field. OLD:

  ```go
  	// Build flat fields for backward compatibility with site/app
  	resp := map[string]any{
  		"user_id":                   userID,
  		"can_connect":               access.CanConnect,
  		"is_premium":                access.IsPremium,
  		"traffic_masking_available": access.TrafficMaskingAvailable,
  		"config_version":            biz.GetSettingOrDefault("config_version", "1"),
  		"subscription_version":      subVer,
  	}

  	// Add plan info to response
  	if plan, err := biz.GetUserPlan(userID); err == nil && plan != nil {
  		resp["plan"] = map[string]any{
  			"id":                plan.ID,
  			"slug":              plan.Slug,
  			"name":              plan.Name,
  			"allowed_protocols": plan.AllowedProtocols,
  			"server_ids":        plan.ServerIDs,
  			"traffic_limit":     plan.TrafficLimitBytes,
  			"duration_days":     plan.DurationDays,
  		}
  	}
  ```

  NEW (note the additive `"amnezia_available": false` default in the map + the assignment
  inside the plan block — shape is unchanged, only one new key):

  ```go
  	// Build flat fields for backward compatibility with site/app
  	resp := map[string]any{
  		"user_id":                   userID,
  		"can_connect":               access.CanConnect,
  		"is_premium":                access.IsPremium,
  		"traffic_masking_available": access.TrafficMaskingAvailable,
  		"config_version":            biz.GetSettingOrDefault("config_version", "1"),
  		"subscription_version":      subVer,
  		// Palma (AmneziaWG) availability — true only for active subs on an
  		// amnezia_wg_enabled plan. Site renders the WG/QR block off this flag;
  		// the heavy config is fetched lazily via GET /api/v1/user/awg-config.
  		"amnezia_available": false,
  	}

  	// Add plan info to response
  	if plan, err := biz.GetUserPlan(userID); err == nil && plan != nil {
  		resp["plan"] = map[string]any{
  			"id":                plan.ID,
  			"slug":              plan.Slug,
  			"name":              plan.Name,
  			"allowed_protocols": plan.AllowedProtocols,
  			"server_ids":        plan.ServerIDs,
  			"traffic_limit":     plan.TrafficLimitBytes,
  			"duration_days":     plan.DurationDays,
  		}
  		// GetUserPlan only returns a plan for an active subscription, and
  		// access.CanConnect already encodes the same gate used for connection_options.
  		resp["amnezia_available"] = plan.AmneziaWGEnabled && access.CanConnect
  	}
  ```

- [ ] **5.2 — Write `handleUserAWGConfig` in `main.go`.** Place it directly after
  `handleUserConnections` (ends line 656), before `handleUserMTProto`. Mirror the auth +
  CORS preamble of `handleUserMTProto` (`extractUserFromToken`, `setCORS`,
  `MethodOptions` short-circuit, GET-only). Gate: re-run `biz.GetUserPlan` and reject with
  403 if no plan or `!plan.AmneziaWGEnabled`. The client INI mirrors the server-side
  `renderAWGConfig` field order (`awg_sync.go:233-267`) but client-shaped: client
  `[Interface]` carries `PrivateKey`/`Address` (the `/32`)/`DNS`, `[Peer]` carries the
  server `PublicKey`, `PresharedKey`, `AllowedIPs = 0.0.0.0/0, ::/0`, `Endpoint`, and the
  same `Jc..H4` CPS lines. After building keys, call `RequestAWGSync()` so the peer is
  written into `awg0.conf` on the Hub. Deeplink = `amneziawg://` + base64(INI) +
  `#🌴 Пальма`. Add a new func:

  ```go
  // handleUserAWGConfig — GET /api/v1/user/awg-config
  // Returns the per-user AmneziaWG ("Пальма") client config as WG-INI text plus an
  // amneziawg:// deeplink, for users on an amnezia_wg_enabled plan with an active
  // subscription. The config is heavy (per-user keys + CPS) so the site fetches it
  // lazily — NOT part of /connections. Calling this lazy-inits the user's keys
  // (biz.GetUserAWGConfig) and triggers RequestAWGSync so the peer lands in awg0.conf.
  func handleUserAWGConfig(w http.ResponseWriter, r *http.Request) {
  	setCORS(w, r)
  	if r.Method == http.MethodOptions {
  		w.WriteHeader(http.StatusOK)
  		return
  	}
  	if r.Method != http.MethodGet {
  		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
  		return
  	}
  	userID, err := extractUserFromToken(r)
  	if err != nil {
  		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": err.Error()})
  		return
  	}

  	// Gate: plan must have AmneziaWG enabled. GetUserPlan errors unless the user
  	// has an active subscription, so this single check covers both conditions.
  	plan, err := biz.GetUserPlan(userID)
  	if err != nil || plan == nil || !plan.AmneziaWGEnabled {
  		writeJSON(w, http.StatusForbidden, map[string]string{
  			"error": "AmneziaWG (Пальма) недоступен на вашем тарифе",
  		})
  		return
  	}

  	cfg, err := biz.GetUserAWGConfig(userID)
  	if err != nil {
  		log.Printf("[awg-config] GetUserAWGConfig(%s): %v", userID, err)
  		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "config build failed"})
  		return
  	}
  	// Ensure the freshly-allocated peer is flushed to awg0.conf on the Hub.
  	RequestAWGSync()

  	ip := cfg.IPAddress
  	if ip != "" && !strings.Contains(ip, "/") {
  		ip += "/32"
  	}

  	var b strings.Builder
  	b.WriteString("[Interface]\n")
  	fmt.Fprintf(&b, "PrivateKey = %s\n", cfg.UserPrivKey)
  	fmt.Fprintf(&b, "Address = %s\n", ip)
  	b.WriteString("DNS = 1.1.1.1, 8.8.8.8\n")
  	fmt.Fprintf(&b, "Jc = %d\n", cfg.CPSParams.Jc)
  	fmt.Fprintf(&b, "Jmin = %d\n", cfg.CPSParams.Jmin)
  	fmt.Fprintf(&b, "Jmax = %d\n", cfg.CPSParams.Jmax)
  	fmt.Fprintf(&b, "S1 = %d\n", cfg.CPSParams.S1)
  	fmt.Fprintf(&b, "S2 = %d\n", cfg.CPSParams.S2)
  	fmt.Fprintf(&b, "H1 = %d\n", cfg.CPSParams.H1)
  	fmt.Fprintf(&b, "H2 = %d\n", cfg.CPSParams.H2)
  	fmt.Fprintf(&b, "H3 = %d\n", cfg.CPSParams.H3)
  	fmt.Fprintf(&b, "H4 = %d\n\n", cfg.CPSParams.H4)
  	b.WriteString("[Peer]\n")
  	fmt.Fprintf(&b, "PublicKey = %s\n", cfg.ServerPubKey)
  	if cfg.UserPSK != "" {
  		fmt.Fprintf(&b, "PresharedKey = %s\n", cfg.UserPSK)
  	}
  	b.WriteString("AllowedIPs = 0.0.0.0/0, ::/0\n")
  	fmt.Fprintf(&b, "Endpoint = %s\n", cfg.Endpoint)
  	b.WriteString("PersistentKeepalive = 25\n")
  	configText := b.String()

  	deeplink := "amneziawg://" +
  		base64.StdEncoding.EncodeToString([]byte(configText)) +
  		"#" + url.PathEscape("🌴 Пальма")

  	writeJSON(w, http.StatusOK, map[string]any{
  		"available":   true,
  		"config_text": configText,
  		"deeplink":    deeplink,
  		"endpoint":    cfg.Endpoint,
  		"ip":          cfg.IPAddress,
  	})
  }
  ```

- [ ] **5.3 — Verify imports in `main.go`.** `fmt`, `log`, `net/http`, `strings`, and
  `net/url` are already imported (used by `handleUserConnections` /
  `buildHubConnectionOptions`). Confirm `encoding/base64` is present; if the Grep below
  returns nothing, add `"encoding/base64"` to the import block. Check:

  ```bash
  cd server/backend && rg '"encoding/base64"|"net/url"|"strings"' cmd/api/main.go
  ```

- [ ] **5.4 — Register the route.** In `main.go` next to the existing connections route
  (line 959). OLD:

  ```go
  	http.HandleFunc("/api/v1/user/connections", apiLimiter.Middleware(handleUserConnections))
  	http.HandleFunc("/api/v1/user/mtproto", apiLimiter.Middleware(handleUserMTProto))
  ```

  NEW:

  ```go
  	http.HandleFunc("/api/v1/user/connections", apiLimiter.Middleware(handleUserConnections))
  	http.HandleFunc("/api/v1/user/awg-config", apiLimiter.Middleware(handleUserAWGConfig)) // Palma — Beads heyearthproject-98ng T5
  	http.HandleFunc("/api/v1/user/mtproto", apiLimiter.Middleware(handleUserMTProto))
  ```

- [ ] **5.5 — Build evidence (must pass).**

  ```bash
  cd server/backend && go build ./cmd/api/...
  ```

  Expect: no output, exit 0. (Quick auth sanity, optional, if a local instance is up:
  `curl -s -o /dev/null -w '%{http_code}' http://localhost:8000/api/v1/user/awg-config`
  → `401` without a token, proving the route is wired and auth-gated.)

- [ ] **5.6 — Commit (backend only).**

  ```bash
  git add server/backend/cmd/api/main.go
  git commit -m @'
  feat(palma): expose AmneziaWG config via /api/v1/user/awg-config

  Add amnezia_available flag to /user/connections (gated on
  plan.AmneziaWGEnabled && active subscription) and a new authenticated
  GET /api/v1/user/awg-config returning per-user WG-INI + amneziawg://
  deeplink. Reuses biz.GetUserAWGConfig (lazy key init) and triggers
  RequestAWGSync so the peer is written to awg0.conf. Happ subscription
  emit untouched (client_api.go stays commented for amneziawg://).

  Beads: heyearthproject-98ng (T5)

  Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
  '@
  ```

---

### Task 6: Personal-cabinet AmneziaWG block (site ConnectionsPage)

Adds a "🌴 Пальма (AmneziaWG)" card to `site/src/pages/ConnectionsPage.jsx`, rendered ONLY
when `connections.amnezia_available` is true (users without access see nothing). The card
lazily fetches the heavy config via a new `getAwgConfig(token)` helper on expand, then shows
a QR of the `config_text`, a "Скачать .conf" Blob download, an "Открыть в AmneziaWG"
deeplink button, and an external link to the AmneziaWG client releases. Existing
`vless_reality` block and subscription link render UNCHANGED (additive only). Site theme
(glass-card, indigo/cyan, rounded-2xl) — NOT the admin Linear theme. Russian strings, no
i18n.

**Files:** `site/src/api/user.js` (new helper), `site/src/pages/ConnectionsPage.jsx`
(new card + state). Reuses `qrUrl` (ConnectionsPage.jsx:11), `CopyButton` /
`copyToClipboard` (`src/components/CopyButton.jsx`).

- [ ] **6.1 — Add `getAwgConfig` to `user.js`.** Follow the `getConnections` pattern
  (line 68) — bearer auth + `jsonOrThrow`. Insert right after `getConnections` (line 75).
  OLD:

  ```js
  export async function getConnections(token, platform) {
    const url = new URL(`${API_BASE}/api/v1/user/connections`, window.location.origin)
    if (platform) url.searchParams.set('platform', platform)
    const res = await fetch(url.toString(), {
      headers: { Authorization: `Bearer ${token}` },
    })
    return jsonOrThrow(res, 'Failed to load connections')
  }
  ```

  NEW:

  ```js
  export async function getConnections(token, platform) {
    const url = new URL(`${API_BASE}/api/v1/user/connections`, window.location.origin)
    if (platform) url.searchParams.set('platform', platform)
    const res = await fetch(url.toString(), {
      headers: { Authorization: `Bearer ${token}` },
    })
    return jsonOrThrow(res, 'Failed to load connections')
  }

  // AmneziaWG ("Пальма") per-user config. Heavy (keys + CPS) — fetched lazily,
  // only for users whose connections.amnezia_available is true. Returns
  // { available, config_text, deeplink, endpoint, ip }.
  export async function getAwgConfig(token) {
    const res = await fetch(`${API_BASE}/api/v1/user/awg-config`, {
      headers: { Authorization: `Bearer ${token}` },
    })
    return jsonOrThrow(res, 'Не удалось загрузить конфигурацию Пальмы')
  }
  ```

- [ ] **6.2 — Import the helper in `ConnectionsPage.jsx`.** OLD (line 3):

  ```jsx
  import { getProfile, getConnections, getDownloads, getSubscribeLink } from '../api/user.js'
  ```

  NEW:

  ```jsx
  import { getProfile, getConnections, getDownloads, getSubscribeLink, getAwgConfig } from '../api/user.js'
  ```

- [ ] **6.3 — Add lazy-fetch state + handler.** After the existing `copiedField` state
  (line 27) add AWG state. After `copyText` (ends line 68) add a lazy loader + a Blob
  download helper. These do NOT run on page load — only when the user expands the card.
  OLD (the `copyText` function, line 62):

  ```jsx
    async function copyText(text, field) {
      const ok = await copyToClipboard(text)
      if (ok) {
        setCopiedField(field)
        setTimeout(() => setCopiedField(''), 2000)
      }
    }
  ```

  NEW (append AWG state near the other useState calls AND the helpers after copyText):

  ```jsx
    // Palma (AmneziaWG) — lazy-loaded config, only fetched when the card expands.
    const [awg, setAwg] = useState(null)
    const [awgLoading, setAwgLoading] = useState(false)
    const [awgError, setAwgError] = useState('')

    async function copyText(text, field) {
      const ok = await copyToClipboard(text)
      if (ok) {
        setCopiedField(field)
        setTimeout(() => setCopiedField(''), 2000)
      }
    }

    async function loadAwg() {
      if (awg || awgLoading) return // fetch once
      setAwgLoading(true)
      setAwgError('')
      try {
        const data = await getAwgConfig(token)
        setAwg(data)
      } catch (e) {
        setAwgError(e.message || 'Не удалось загрузить конфигурацию')
      } finally {
        setAwgLoading(false)
      }
    }

    function downloadAwgConf() {
      if (!awg?.config_text) return
      const blob = new Blob([awg.config_text], { type: 'text/plain' })
      const href = URL.createObjectURL(blob)
      const a = document.createElement('a')
      a.href = href
      a.download = 'palma.conf'
      document.body.appendChild(a)
      a.click()
      document.body.removeChild(a)
      URL.revokeObjectURL(href)
    }
  ```

- [ ] **6.4 — Add `amniziaAvailable` derived flag.** Next to the existing derived values
  (line 59-60). OLD:

  ```jsx
    const canConnect = connections?.can_connect
    const vlessReality = connections?.vless_reality || ''
  ```

  NEW:

  ```jsx
    const canConnect = connections?.can_connect
    const vlessReality = connections?.vless_reality || ''
    const amneziaAvailable = connections?.amnezia_available
  ```

- [ ] **6.5 — Render the Пальма card.** Insert AFTER the subscription-URL block (closes
  line 217), still inside the `<> ... </>` that follows `canConnect` (before line 219's
  `</>`), so it only shows for connectable users and sits below the existing cards. The
  existing `vless_reality` grid (lines 180-198) and subscription block stay byte-for-byte
  unchanged. NEW (additive block — uses a `<details>` whose `onToggle` triggers `loadAwg`,
  matching the site's glass / indigo-cyan aesthetic):

  ```jsx
              {/* ─── Пальма (AmneziaWG) — only for amnezia_wg_enabled plans ─── */}
              {amneziaAvailable && (
                <div className="p-4 rounded-xl bg-gradient-to-br from-emerald-500/[0.06] to-cyan-500/[0.04] border border-emerald-500/15 mb-5">
                  <div className="flex items-center gap-2 mb-2">
                    <span className="px-2 py-0.5 rounded-md bg-emerald-500/15 text-emerald-300 text-xs font-bold">🌴 Пальма</span>
                    <span className="text-[10px] text-text-secondary">AmneziaWG · максимальная маскировка</span>
                  </div>
                  <p className="text-xs text-text-secondary mb-3">
                    Отдельный протокол для приложения AmneziaWG. Скачайте конфиг или отсканируйте QR-код.
                  </p>

                  <details className="group" onToggle={(e) => { if (e.currentTarget.open) loadAwg() }}>
                    <summary className="inline-flex items-center gap-1.5 text-sm text-emerald-300 cursor-pointer hover:text-emerald-200 transition select-none">
                      <svg className="w-4 h-4 transition-transform group-open:rotate-90" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}>
                        <path strokeLinecap="round" strokeLinejoin="round" d="M8.25 4.5l7.5 7.5-7.5 7.5" />
                      </svg>
                      Показать конфигурацию
                    </summary>

                    <div className="mt-4">
                      {awgLoading && (
                        <div className="flex items-center gap-2 text-xs text-text-secondary">
                          <div className="w-4 h-4 rounded-full border-2 border-emerald-400/30 border-t-emerald-400 animate-spin" />
                          Загрузка конфигурации...
                        </div>
                      )}

                      {awgError && (
                        <p className="text-xs text-red-400">{awgError}</p>
                      )}

                      {awg?.config_text && (
                        <div className="space-y-4">
                          <div className="flex flex-col sm:flex-row gap-4 items-start">
                            <img
                              src={qrUrl(awg.config_text)}
                              alt="QR конфигурации Пальмы"
                              className="rounded-lg bg-white p-2 w-[180px] h-[180px] flex-shrink-0"
                              loading="lazy"
                            />
                            <div className="flex-1 min-w-0">
                              {awg.endpoint && (
                                <p className="text-xs text-text-secondary mb-1">
                                  Сервер: <code className="font-mono text-text-primary">{awg.endpoint}</code>
                                </p>
                              )}
                              {awg.ip && (
                                <p className="text-xs text-text-secondary mb-3">
                                  Ваш IP: <code className="font-mono text-text-primary">{awg.ip}</code>
                                </p>
                              )}
                              <div className="flex flex-wrap gap-2">
                                <button
                                  onClick={downloadAwgConf}
                                  className="inline-flex items-center gap-1.5 px-3 py-1.5 rounded-lg text-xs font-medium bg-emerald-500/15 text-emerald-300 hover:bg-emerald-500/25 transition"
                                >
                                  <svg className="w-4 h-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1.5}>
                                    <path strokeLinecap="round" strokeLinejoin="round" d="M3 16.5v2.25A2.25 2.25 0 005.25 21h13.5A2.25 2.25 0 0021 18.75V16.5M16.5 12L12 16.5m0 0L7.5 12m4.5 4.5V3" />
                                  </svg>
                                  Скачать .conf
                                </button>
                                {awg.deeplink && (
                                  <a
                                    href={awg.deeplink}
                                    className="inline-flex items-center gap-1.5 px-3 py-1.5 rounded-lg text-xs font-medium glass hover:bg-white/10 transition"
                                  >
                                    Открыть в AmneziaWG
                                  </a>
                                )}
                                <CopyButton text={awg.config_text} label="Копировать конфиг" />
                              </div>
                              <a
                                href="https://github.com/amnezia-vpn/amnezia-client/releases"
                                target="_blank"
                                rel="noopener noreferrer"
                                className="inline-flex items-center gap-1 mt-3 text-xs text-brand-light hover:text-accent transition"
                              >
                                Скачать приложение AmneziaWG
                                <svg className="w-3 h-3" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}>
                                  <path strokeLinecap="round" strokeLinejoin="round" d="M13.5 4.5L21 12m0 0l-7.5 7.5M21 12H3" />
                                </svg>
                              </a>
                            </div>
                          </div>
                        </div>
                      )}
                    </div>
                  </details>
                </div>
              )}
  ```

- [ ] **6.6 — Build evidence (must pass).**

  ```bash
  cd site && npm run build
  ```

  Expect: Vite "built in …" success, exit 0, no unresolved-import errors for `getAwgConfig`.

- [ ] **6.7 — Visual sanity (manual, since site has no automated tests).** Confirm: a user
  WITHOUT an amnezia plan sees no Пальма card (flag false → block not rendered); a user WITH
  the plan sees the card, and the config is fetched only after expanding `<details>`
  (verify in the Network tab that `/api/v1/user/awg-config` fires on expand, NOT on load).

- [ ] **6.8 — Commit (frontend only).**

  ```bash
  git add site/src/api/user.js site/src/pages/ConnectionsPage.jsx
  git commit -m @'
  feat(palma): personal-cabinet AmneziaWG block on ConnectionsPage

  Add getAwgConfig(token) helper and a "🌴 Пальма (AmneziaWG)" card,
  rendered only when connections.amnezia_available. Lazily fetches the
  per-user config on expand (QR + .conf Blob download + amneziawg://
  deeplink + AmneziaWG client link). Existing vless_reality block and
  subscription link unchanged (additive only). Site glass/indigo-cyan
  theme, Russian strings.

  Beads: heyearthproject-98ng (T6)

  Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
  '@
  ```
### Task 7: Plan gating fix + admin checkbox verify

**Beads:** sub-task под `heyearthproject-98ng`. `bd create "T7: Plan gating fix (FALSE on free/250₽) + admin checkbox round-trip verify" -t task -p 1`, затем `bd dep add <T7-id> heyearthproject-98ng --type parent-child`, `bd update <T7-id> --claim --status=in_progress`.

**Files (read-only verify — никаких правок кода ожидать не должны):**
- `admin_web/src/pages/PlansPage.jsx:366-385` — чекбокс 🌴 Пальма
- `admin_web/src/pages/PlansPage.jsx:61,66,70,78,81` — create/edit/save payload
- `admin_web/src/api/admin.js:266-271` — createPlan/updatePlan
- `server/backend/internal/biz/plans.go:38,193,199,217,224` — `amnezia_wg_enabled` scan/insert/update

#### Часть A — admin checkbox round-trip verify (no-change ожидается)

- [ ] **A1. Подтвердить JSX чекбокса.** Реальный код `PlansPage.jsx:366-385`:
```jsx
<Card className="p-4 mb-4">
  <label className="text-xs text-tertiary mb-2 block">AmneziaWG (🌴 Пальма)</label>
  <label className="flex items-start gap-2 cursor-pointer">
    <input
      type="checkbox"
      checked={!!editing.amnezia_wg_enabled}
      onChange={e => setEditing({...editing, amnezia_wg_enabled: e.target.checked})}
      className="rounded mt-1"
    />
```
Expected: поле в state называется `amnezia_wg_enabled` (snake_case), bound к `editing`. ✅ соответствует backend JSON-тегу.

- [ ] **A2. Подтвердить, что save шлёт весь `editing` без allow-list.** `PlansPage.jsx:69-83`:
```jsx
async function handleSave() {
  const p = { ...editing }
  ...
  if (p.id) { await updatePlan(p.id, p) } else { await createPlan(p) }
```
И `admin.js:266-271`:
```js
export function createPlan(payload) { return apiFetch('/plans', { method: 'POST', body: payload }) }
export function updatePlan(id, payload) { return apiFetch(`/plans/${id}`, { method: 'PATCH', body: payload }) }
```
Expected: `p` — полная копия `editing`, значит `amnezia_wg_enabled` уходит в body как есть. Allow-list отсутствует → поле не теряется.

- [ ] **A3. Подтвердить, что create-дефолт и openEdit сохраняют поле.** `PlansPage.jsx:61` (`amnezia_wg_enabled: false` в openCreate-дефолте) и `:66` (`openEdit` делает `setEditing({ ...plan })` — переносит значение из БД). Expected: при редактировании существующего плана чекбокс отражает текущее значение.

- [ ] **A4. Подтвердить backend parse + persist.** `biz/plans.go:38` тег `json:"amnezia_wg_enabled"`; `:193,199` INSERT (`$17` = `p.AmneziaWGEnabled`); `:217,224` UPDATE (`amnezia_wg_enabled=$17`). Expected: round-trip целостен от чекбокса до `plans.amnezia_wg_enabled`.

- [ ] **A5. Решение по коду.** Чекбокс + payload + backend подтверждены корректными → **изменений кода в T7 НЕТ**. Если на A1–A4 обнаружится расхождение (поле переименовано / allow-list режет body / scan-столбец отсутствует) — только тогда правка + commit с `Beads: heyearthproject-98ng` и `Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>`.

- [ ] **A6. Live round-trip в админке (USER-ASSISTED, опционально, после деплоя T8.3).** chrome MCP incognito → `https://admin.hiearth.ru/plans` → открыть план id=5 (all-inclusive) → убедиться, что 🌴 Пальма checked; открыть id=4 (vless-reality) → checked снять (после T7-B SQL должен быть unchecked) → Сохранить → reload → состояние сохранилось. Theme-lock: токены, без slate/gray, Geist. Expected: чекбокс визуально соответствует значению в БД после A-A7.

#### Часть B — production SQL gating fix

> NB: peer-selection в `awg_sync.go:82-96` джойнит `WHERE p.amnezia_wg_enabled = TRUE AND us.status='active' AND us.valid_until>NOW()`. Значит правка флага на id=1,4 напрямую исключит их пользователей из `awg0.conf` при следующем `SyncAWGConfig` (триггерится рестартом heyearth-api в T8.5). DB-доступ на Hub: только `sudo -u postgres psql` (peer auth; роль `heyearth` peer-auth не проходит).

- [ ] **B1. Baseline — снять текущее состояние всех 4 планов (для rollback).**
```bash
cd C:/Users/alex/Documents/heyearthproject/server/backend
go run ./cmd/deploy --ssh "sudo -u postgres psql -d heyearth -c \"SELECT id,slug,price_rub,amnezia_wg_enabled FROM plans ORDER BY price_rub;\""
```
Expected (ДО фикса): TRUE на всех четырёх — `1 free-trial 0 t`, `4 vless-reality 250 t`, `5 all-inclusive 300 t`, `6 annual-premium 3000 t`. Запомнить для rollback.

- [ ] **B2. Применить UPDATE — снять флаг с free-trial (id=1) и vless-reality (id=4).**
```bash
cd C:/Users/alex/Documents/heyearthproject/server/backend
go run ./cmd/deploy --ssh "sudo -u postgres psql -d heyearth -c \"UPDATE plans SET amnezia_wg_enabled=FALSE WHERE id IN (1,4);\""
```
Expected: `UPDATE 2`. (SSH flaky — если "connection lost", повторить; UPDATE идемпотентен, повтор безопасен.)

- [ ] **B3. Verify gating-матрицу.**
```bash
cd C:/Users/alex/Documents/heyearthproject/server/backend
go run ./cmd/deploy --ssh "sudo -u postgres psql -d heyearth -c \"SELECT id,slug,price_rub,amnezia_wg_enabled FROM plans ORDER BY price_rub;\""
```
Expected ровно:
```
 1 | free-trial      |    0 | f
 4 | vless-reality   |  250 | f
 5 | all-inclusive   |  300 | t
 6 | annual-premium  | 3000 | t
```
FALSE на 0₽/250₽, TRUE только на 300₽/3000₽. Если не так — повторить B2 и перепроверить.

- [ ] **B4. Close T7.** `bd update <T7-id> --notes "checkbox round-trip verified OK (no code change); SQL gating: amnezia FALSE on id 1,4 / TRUE on 5,6"` → `bd close <T7-id>`. Никакого git-commit (только production state).

---

### Task 8: Deploy everything + E2E verification

**Beads:** `bd create "T8: Deploy T1-T7 + E2E verify AmneziaWG Пальма" -t task -p 1` → `bd dep add <T8-id> heyearthproject-98ng --type parent-child` → `bd dep add <T8-id> <T7-id> --type blocks` → `bd update <T8-id> --claim --status=in_progress`.

> **Порядок строгий:** binary (T3+T4+T5) → setup-awg (T1+T2) → site (T6) → SQL (T7) → restart heyearth-api (регенерит `awg0.conf` с уже-правильно-загейченными peers). Стоп-сервис-перед-binary — обязателен (SFTP/перезапись бинаря). Прод SSH нестабилен — на каждой SSH-команде при "connection lost" повторять; `quick_deploy.go` и `--update-main` идемпотентны.

#### Шаг 1 — деплой backend-бинаря (T3+T4+T5)

- [ ] **8.1.1 Остановить сервис и удалить старый бинарь.**
```bash
cd C:/Users/alex/Documents/heyearthproject/server/backend
go build ./...
go run ./cmd/deploy --ssh 'systemctl stop heyearth-api && rm -f /opt/heyearth/mainserver/heyearth-api'
```
Expected: `go build` без вывода (exit 0); SSH без ошибок.

- [ ] **8.1.2 Залить новый бинарь (SSH-pipe).**
```bash
cd C:/Users/alex/Documents/heyearthproject/server/backend
go run quick_deploy.go
```
Expected: stream показывает upload + автостарт systemd, финальная строка `heyearth-api active (running)`. При "connection lost" — повторить (идемпотентно).

- [ ] **8.1.3 Подтвердить, что API живой.**
```bash
cd C:/Users/alex/Documents/heyearthproject/server/backend
go run ./cmd/deploy --ssh 'systemctl is-active heyearth-api && journalctl -u heyearth-api -n 15 --no-pager'
```
Expected: `active`; в логах нет panic/fatal, есть строки про старт sync-loop'ов.

#### Шаг 2 — `--setup-awg` (T1+T2: amneziawg-tools, awg0 up, tun2socks + policy routing)

- [ ] **8.2.1 Запустить provisioning AmneziaWG на Hub.**
```bash
cd C:/Users/alex/Documents/heyearthproject/server/backend
go run ./cmd/deploy --setup-awg
```
Expected: provisioning лог — установка amneziawg-tools/amneziawg-go, генерация server keypair (если не было), создание `/opt/heyearth/awg/`, поднятие интерфейса `awg0`, настройка tun2socks + policy routing (table 100, правило для 10.66.66.0/24). Финал без ошибок. SSH flaky → повторить (provisioning идемпотентен).

#### Шаг 3 — деплой сайта (T6: site + admin_web)

- [ ] **8.3.1 Собрать и залить фронты.**
```bash
cd C:/Users/alex/Documents/heyearthproject/server/backend
go run ./cmd/deploy --site
```
Expected: `npm run build` для admin_web и site без ошибок, upload, nginx reload. Если падает на build — чинить фронт до продолжения.

#### Шаг 4 — применить T7 SQL (если ещё не применён в T7-B)

- [ ] **8.4.1 Применить/подтвердить gating.** Если T7-B уже выполнен — только verify (B3). Иначе выполнить B2+B3 здесь.
```bash
cd C:/Users/alex/Documents/heyearthproject/server/backend
go run ./cmd/deploy --ssh "sudo -u postgres psql -d heyearth -c \"UPDATE plans SET amnezia_wg_enabled=FALSE WHERE id IN (1,4); SELECT id,slug,price_rub,amnezia_wg_enabled FROM plans ORDER BY price_rub;\""
```
Expected: `UPDATE 2` + матрица f/f/t/t (см. T7-B3).

#### Шаг 5 — рестарт heyearth-api (регенерация awg0.conf с загейченными peers)

- [ ] **8.5.1 Рестарт → SyncAWGConfig пересобирает peers.**
```bash
cd C:/Users/alex/Documents/heyearthproject/server/backend
go run ./cmd/deploy --ssh 'systemctl restart heyearth-api && sleep 4 && systemctl is-active heyearth-api'
```
Expected: `active`. На старте StartXraySyncLoop + AWG-sync (main.go:887) перегенерят `/opt/heyearth/awg/awg0.conf` только из peers планов id=5,6.

#### Шаг 6 — E2E verification (evidence-driven, RED-LINE проверки обязательны)

- [ ] **8.6.1 Интерфейс awg0 поднят.**
```bash
cd C:/Users/alex/Documents/heyearthproject/server/backend
go run ./cmd/deploy --ssh "ip link show awg0"
```
Expected: `awg0: <POINTOPOINT,NOARP,UP,LOWER_UP> ... state UNKNOWN` — присутствует слово `UP`. Если `does not exist` — вернуться к 8.2.1.

- [ ] **8.6.2 UDP :51820 слушается.**
```bash
cd C:/Users/alex/Documents/heyearthproject/server/backend
go run ./cmd/deploy --ssh "ss -ulnp | grep 51820"
```
Expected: строка `UNCONN 0 0 *:51820 *:*` с процессом amneziawg-go/awg. Пусто → интерфейс не слушает, чинить setup-awg.

- [ ] **8.6.3 Peers присутствуют (CPS-параметры).**
```bash
cd C:/Users/alex/Documents/heyearthproject/server/backend
go run ./cmd/deploy --ssh "awg show awg0"
```
Expected: `interface: awg0`, `listening port: 51820`, jc/jmin/jmax/s1/s2/h1-h4 (CPS) ненулевые, и блок(и) `peer:` для пользователей планов 5/6 (минимум наш тест-юзер на плане 6). Тест-юзер `awg_public_key` должен присутствовать; peers free-trial/vless-reality — отсутствовать.

- [ ] **8.6.4 Policy routing на месте.**
```bash
cd C:/Users/alex/Documents/heyearthproject/server/backend
go run ./cmd/deploy --ssh "ip rule show | grep 10.66.66; ip route show table 100"
```
Expected: `ip rule` содержит правило `from 10.66.66.0/24 lookup 100` (или fwmark→100); `table 100` содержит default через tun2socks-устройство (напр. `default dev tun0` / через SOCKS-шлюз :1080). Пусто → policy routing не настроен, чинить setup-awg.

- [ ] **8.6.5 RED-LINE: Happ-подписка отдаёт ровно 4 Reality URI и НИ ОДНОГО `amneziawg://`.**
> Обоснование (client_api.go:189-204): happ использует xray-core+sing-box, которые не поддерживают AmneziaWG; emit `amneziawg://` намеренно отключён. Любой `amneziawg://` в подписке = регрессия.
```bash
cd C:/Users/alex/Documents/heyearthproject/server/backend
go run ./cmd/deploy --ssh "curl -sk --resolve hiearth.ru:443:127.0.0.1 -H 'User-Agent: Happ' https://hiearth.ru/api/v1/user/subscribe/<redacted> -o /tmp/sub_awg.txt; echo VLESS=$(grep -c '^vless://' /tmp/sub_awg.txt); echo AWG=$(grep -c 'amneziawg://' /tmp/sub_awg.txt)"
```
Expected: `VLESS=4` (Берёза 1+2, Дуб 1+2) и **`AWG=0`**. Если `AWG>0` — НЕМЕДЛЕННО регрессия emit-блока, остановиться и откатить T-emit изменение.

- [ ] **8.6.6 Новый endpoint `/api/v1/user/awg-config` (T6) — premium возвращает config, free → 403.**
> ВАЖНО (grounded): на момент написания плана в `main.go` (routes :954-991) НЕТ зарегистрированного маршрута `/api/v1/user/awg-config`; `biz.GetUserAWGConfig` существует (`internal/biz/awg.go:216`), но не подключён к handler'у — это работа T6 (beads `heyearthproject-v7lb`). Сначала подтвердить, что T6 действительно добавил route+gating, иначе этот шаг невыполним.
```bash
# Предусловие: T6 зарегистрировал handler. Подтвердить grep'ом в исходнике:
cd C:/Users/alex/Documents/heyearthproject/server/backend
grep -n "awg-config" cmd/api/main.go
# Затем (premium-токен):
go run ./cmd/deploy --ssh "curl -sk -o /dev/null -w '%{http_code}\n' --resolve hiearth.ru:443:127.0.0.1 -H 'User-Agent: Happ' https://hiearth.ru/api/v1/user/awg-config/<redacted>"
```
Expected (если T6 готов): grep находит строку `http.HandleFunc("/api/v1/user/awg-config...`; premium → `200` + тело-конфиг (см. 8.6.7); free-trial токен → `403`. **Если grep пуст → T6 не доделан: завести/реопенить `heyearthproject-v7lb`, пометить 8.6.6/8.6.7 BLOCKED, не считать эпик закрытым.**

- [ ] **8.6.7 Содержимое premium-конфига валидно.**
```bash
cd C:/Users/alex/Documents/heyearthproject/server/backend
go run ./cmd/deploy --ssh "curl -sk --resolve hiearth.ru:443:127.0.0.1 -H 'User-Agent: Happ' https://hiearth.ru/api/v1/user/awg-config/<redacted>"
```
Expected: WG-INI текст — секции `[Interface]` (PrivateKey, Address=10.66.66.X/32, Jc/Jmin/Jmax/S1/S2/H1-H4) и `[Peer]` (PublicKey Hub'а, Endpoint `198.51.100.236:51820`, AllowedIPs `0.0.0.0/0`). Без этого шага money-shot невозможен. (Если T6 не готов — BLOCKED, см. 8.6.6.)

- [ ] **8.6.8 RED-LINE: Reality (xray) по-прежнему принимает коннекты.**
```bash
cd C:/Users/alex/Documents/heyearthproject/server/backend
go run ./cmd/deploy --ssh "journalctl -u xray.service --since '5 min ago' --no-pager | grep accepted | tail -10"
```
Expected: свежие `accepted` строки. setup-awg не должен ломать Reality. Если xray не принимает / crash-loop — регрессия, разбираться до закрытия эпика.

- [ ] **8.6.9 (USER-ASSISTED — MONEY-SHOT) Живой клиент AmneziaWG.**
> Требует human-in-the-loop с реальным AmneziaWG-клиентом (нет CLI-эквивалента). Зависит от 8.6.6/8.6.7 (config доступен).
> Попросить пользователя (тот же Tele2-mobile тестер, токен `d329def4-...`, план 6 = premium):
> 1. Скачать/импортировать `.conf` из site-портала ИЛИ из `/api/v1/user/awg-config/...` (как `palma.conf`) в клиент **AmneziaWG / AmneziaVPN**.
> 2. Подключиться (Connect).
> 3. Открыть `https://ifconfig.me` (или `curl ifconfig.me`) → сообщить IP.
>
> Expected: IP — **амстердамский exit** (NL ASN, не российский, не `198.51.100.236`). Это доказывает, что трафик идёт awg0 → tun2socks → Hub→Exit туннель → NL.

- [ ] **8.6.10 (после money-shot) Счётчики выросли — трафик реально прошёл.**
```bash
cd C:/Users/alex/Documents/heyearthproject/server/backend
go run ./cmd/deploy --ssh "awg show awg0 transfer"
go run ./cmd/deploy --ssh "sudo -u postgres psql -d heyearth -t -c \"SELECT traffic_used FROM users WHERE vless_uuid='<redacted>';\""
```
Expected: `awg show awg0 transfer` для peer тест-юзера — RX/TX ненулевые; `traffic_used` в БД вырос относительно baseline (снять до money-shot тем же запросом). Оба ненулевых = end-to-end подтверждён.

- [ ] **8.6.11 Зафиксировать insight в Beads memory.**
```bash
cd C:/Users/alex/Documents/heyearthproject
bd remember "AmneziaWG 🌴 Пальма (epic heyearthproject-98ng): Hub :51820 UDP, awg0 userspace + tun2socks→policy route table 100→Hub-Exit tunnel→NL exit. Gating: ТОЛЬКО plans.amnezia_wg_enabled=TRUE (id=5 all-inclusive, id=6 annual-premium); awg_sync.go peer-query джойнит этот флаг → рестарт heyearth-api регенерит awg0.conf. RED LINE: happ-подписку НЕ трогаем (xray/sing-box не умеют AWG, amneziawg:// silently dropped) — 4 Reality URI, AWG=0. Доставка конфига отдельным endpoint /api/v1/user/awg-config (T6/heyearthproject-v7lb). Deploy: quick_deploy.go → --setup-awg → --site → SQL → restart. DB на Hub только sudo -u postgres psql."
```

- [ ] **8.6.12 Close T8 + финализация эпика.** `bd update <T8-id> --notes "awg0 UP, :51820 listening, peers gated to 5/6, policy route present, RED LINE: VLESS=4 AWG=0, xray accepted OK, money-shot NL IP + transfer nonzero"` → `bd close <T8-id>`. Затем `/finish` для эпика `heyearthproject-98ng` (QA-отчёт → verification → docs → close). Если 8.6.6/8.6.7/8.6.9 BLOCKED (T6 не готов) — эпик НЕ закрывать, оставить open с заметкой о незавершённом config-delivery.

#### Сводка evidence (gate перед закрытием эпика)
- [ ] `go build ./...` exit 0 (8.1.1)
- [ ] `heyearth-api` active, без panic (8.1.3, 8.5.1)
- [ ] `ip link show awg0` → UP (8.6.1); `ss -ulnp | grep 51820` listening (8.6.2)
- [ ] `awg show awg0` → CPS + peers только планов 5/6 (8.6.3)
- [ ] policy route `from 10.66.66.0/24 lookup 100` + table 100 default (8.6.4)
- [ ] gating-матрица plans f/f/t/t (8.4.1 / T7-B3)
- [ ] **RED LINE** Happ-подписка VLESS=4, AWG=0 (8.6.5)
- [ ] **RED LINE** xray `accepted` свежие (8.6.8)
- [ ] `/awg-config` premium 200 + валидный WG-INI, free 403 (8.6.6/8.6.7 — или BLOCKED если T6 не готов)
- [ ] **MONEY-SHOT** живой клиент → NL IP (8.6.9, USER-ASSISTED)
- [ ] `awg show awg0 transfer` + `traffic_used` выросли (8.6.10)
- [ ] `bd remember` insight записан (8.6.11)
---

## Self-review (plan vs PRD)

**Spec coverage:**
- PRD «Инфраструктура (Hub)» → T1 (amneziawg-tools, awg-quick up, ip_forward, :51820, enabled) ✓
- PRD «Routing через Exit» → T2 (split-routing «как Happ»: additive xray `socks-awg` inbound + hev-socks5-tunnel + ip rule 10.66.66.0/24; money-shot: foreign→Amsterdam, RU→Hub direct; DNS over UDP ASSOCIATE) ✓
- PRD «awg_sync.go фикс» → T3 (`awg syncconf`, real cold-start, traffic-limit WHERE) ✓
- PRD «Traffic accounting» → T4 (awg_stats.go, dual-write traffic_stats + traffic_used, shared limit) ✓
- PRD «Личный кабинет» → T5 (`amnezia_available` + `/awg-config` gated 403) + T6 (additive card, QR+file+deeplink+client link, hidden if no access) ✓
- PRD «Gating» → T7 (SQL id 1,4 → FALSE; admin checkbox verified) ✓
- PRD «Регрессия (RED LINE)» → T8 mandatory evidence (4 Reality URIs/0 amneziawg; xray accepted; Hysteria2 health) ✓

**No gaps.** Every PRD acceptance criterion maps to a task step.

**Type/name consistency check:**
- Interface `awg0`, unit `awg`, config `/opt/heyearth/awg/awg0.conf`, server IP `10.66.66.1/24`, port `51820` — consistent T1/T3/T4 (all reference the `awg_sync.go` consts).
- `RequestAWGSync()` (awg_sync.go:52), `awgInterface` const (awg_sync.go:38), `AWGSyncFn` (main.go:879) — referenced, not redeclared, in T4/T5.
- `GetUserAWGConfig(userID string) (*AWGConfig, error)`, `AWGCPSParams` fields, `plan.AmneziaWGEnabled` (plans.go:38), `GetUserPlan` — used consistently T5/T6.
- Egress SOCKS port = `1088+exitID` (discovered, not hardcoded) — T2 uses a discovery step + `AWG_EXIT_SOCKS_PORT` override defaulting 1090.
- Traffic semantics: `awg show transfer` rx=upload / tx=download (server perspective) — locked in T4 (opposite to hysteria2's client-perspective naming — called out explicitly).

**Known preconditions flagged by phase authors (executor must honor):**
- T3: confirm `user_subscriptions.traffic_limit`/`traffic_used` columns exist before building (they do — used by hysteria2_auth.go:119, user.go).
- T4: `awgInterface` lives in awg_sync.go — do NOT redeclare.
- T5: `/api/v1/user/awg-config` is NOT yet registered in main.go (confirmed) — T5 registers it; T8's awg-config evidence steps are gated on `grep awg-config main.go` succeeding.
- T1: unit stays named `awg` + iface `awg0` so awg_sync.go's `systemctl restart awg` still works; T1 adds a `wg→awg` shim + config symlink, but T3 still switches the apply path to explicit `awg` (belt and suspenders).

## Execution handoff

Plan complete. Two execution options:

**1. Subagent-Driven (recommended)** — fresh subagent per task, two-stage review (spec then quality) between tasks, fast iteration. Deploy/E2E steps (T2, T8) involve live prod + a user-assisted money-shot (real AmneziaWG client → Amsterdam IP), so I'll surface those to you.

**2. Inline Execution** — execute tasks in this session with checkpoints.

Because «всё всё доделать» means we run to a verified-working prod (including the user-assisted live-client test), Subagent-Driven keeps each task isolated and reviewed while I coordinate the prod deploys + bring you in for the live verification at T8.
