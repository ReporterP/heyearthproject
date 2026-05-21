---
title: VPN connection stability diagnostics
status: phase_0_1_complete
created: 2026-05-21
type: operational / diagnostic (not feature)
---

# VPN Connection Stability — Diagnostic Design

## Problem

~100 active HiEarth clients experience wave-like ping instability:
* Normal 200-400ms then sudden spike to N/A
* N/A can persist for long periods
* Pattern not visually tracked by user

User-supplied hypotheses (in order of mention):
1. **DPI throttling** — rolling censorship pulses on RU ISPs
2. **Hub capacity saturation** — 1 vCPU / 1.9 GB RAM / 0 swap (vdsina)
3. **Latvia exit-node disconnect side-effect** — load concentration on remaining exits

Initial intel obtained from user:
* Waves existed BEFORE Latvia was disconnected → hypothesis #3 demoted from "root cause" to "amplifier"

## Approach — hybrid B+A (user-approved, executed)

Two-phase, evidence-first. No code/config changes until both phases complete.

### Phase 0 — cheap parallel snapshot (DONE)

Executed three independent read-only data pulls:
* 0.A — Hub resource state
* 0.B — Hub recent logs (last 24h)
* 0.C — Database state (servers, traffic_stats, config_test_results)
* 0.D — Softirq paint over time + conntrack + NIC errors

### Phase 1 — targeted deep-dive (DONE — tunnel branch)

Followed evidence into tunnel branch (Hysteria2 client logs + systemd units + configs).

## Findings

### ✅ NOT the problem
* **NIC**: ens3 RX 2.93 TB / TX 2.80 TB over 64 days, **zero errors/drops/fifo/frame**
* **conntrack**: 3421 / 65536 (5% utilization, 18× headroom)
* **RAM**: 1230 MB available, no swap pressure
* **Application code**: 2000+ lines of `heyearth-api` journal — **zero errors/panics**. Only `[traffic-stats]` and `[xray-sync] Конфиг не изменился`.
* **Xray stderr**: `journalctl -u xray -p err` → `No entries`
* **DPI**: no DPI-pattern signatures in logs (no TLS-reject pulses, no per-ISP failure clustering)

### 🔥 ROOT CAUSE — multiple cooperating factors

**Factor #1 — Zombie hysteria2-client-1.service (Latvia)** *(CRITICAL)*
```
hysteria2-client-1.service - Hysteria2 Client for 155.212.225.172 (HeYearth)
  Active since Apr 06; CPU time 10:49:24
  Config: /opt/heyearth/hysteria/client-1.yaml
  Process PID 470602 alive
  WantedBy=multi-user.target  (enabled at boot)
```
Latvia exit is `disabled` in DB (only Amsterdam shows is_exit_node=t), but systemd unit kept running. For 45+ days it has been actively retrying connections to a dead host. Costs CPU, leaks sockets, pollutes journalctl, contests softirq with the productive tunnel.

**Factor #2 — Hysteria2 SOCKS5 i/o timeout flood** *(MAJOR)*
Constant stream in `hysteria2-client.service` journal:
```
WARN SOCKS5 TCP error  {reqAddr: "178.121.1.197:60387", error: "i/o timeout"}
WARN SOCKS5 TCP error  {reqAddr: "85.172.110.203:14084", error: "i/o timeout"}
WARN SOCKS5 TCP error  {reqAddr: "95.54.22.143:43298", error: "i/o timeout"}
WARN SOCKS5 TCP error  {reqAddr: "45.156.20.27:6881", error: "connection refused"}
```
All reqAddr's are **Russian IP-addresses**. Users (or their P2P/BT clients) are dialing destinations IN Russia THROUGH the Amsterdam tunnel — guaranteed to time out (Amsterdam → RU IP is heavily filtered). Each timeout holds resources 5-30 seconds. At hundreds/min this:
* Eats CPU on the single vCPU
* Backs up SOCKS5 dial queue inside Hysteria2 client
* Causes the main tunnel to slow down for ALL users when burst arrives

**Factor #3 — Single exit = SPOF** *(ARCHITECTURAL)*
Only `Амстердам 32Tb` (89.124.89.126) is `is_exit_node=t, status=online`. When the single tunnel slows/pulses → 100% of users see N/A simultaneously. This is **exactly** the "wave" pattern user described.

### Wave mechanism (synthesis)

```
Steady state:
  Users → Hub Xray → Hysteria2 SOCKS5 client → UDP :50443 → Amsterdam
  + (parasitic) zombie hysteria2-client-1 burning CPU retrying Latvia
  + (parasitic) constant SOCKS5 timeouts to RU IPs (BT/P2P leakage)

Wave trigger:
  Burst of SOCKS5 dials → queue grows → main Hysteria2 client slows
   → CPU softirq + sys spike on 1 vCPU
   → UDP scheduling on tunnel delayed
   → all users see jitter then N/A (no fallback exit)

Recovery:
  Backlog drains → tunnel resumes → ping normalizes
  Until next burst → repeat
```

## Recommended fixes (ordered by impact/cost)

### P0 — safe, immediate, high-leverage

**P0.1 — Stop & disable zombie Latvia systemd-unit**
```bash
systemctl stop hysteria2-client-1
systemctl disable hysteria2-client-1
mv /etc/systemd/system/hysteria2-client-1.service /tmp/latvia-ghost.service.bak
mv /opt/heyearth/hysteria/client-1.yaml /tmp/latvia-client-1.yaml.bak
systemctl daemon-reload
```
**Risk**: zero. Service has been targeting dead host for 45 days.
**Expected effect**: 1-3% CPU freed, journal noise eliminated.
**Verify**: `systemctl status hysteria2-client-1` → not-found, journal stays quiet.

**P0.2 — Restart main Hysteria2 client to flush SOCKS5 backlog**
```bash
systemctl restart hysteria2-client
```
**Risk**: low — 3-second reconnect interruption to Amsterdam, affects all users briefly.
**Expected effect**: backlog cleared, fresh state. Useful baseline post-P0.1.
**Window**: do during low-traffic hours (or accept brief disruption).

### P1 — short-term (1-2 days)

**P1.1 — Add a second exit-node** (eliminate SPOF)
* Options:
  - Re-enable Latvia (155.212.225.172) if hardware still alive
  - Provision new EU/Asia node (Hetzner, Aeza, BunnyShell — same as hub)
  - Costs ~250-590 ₽/мес depending on provider
* Plug into DB → `INSERT INTO servers ... is_exit_node=true, load_weight=1`
* Hub auto-tunnels to it after `RequestXraySync()` debounce
**Risk**: medium — needs provisioning + node-agent setup + mTLS cert.

**P1.2 — Investigate SOCKS5 → RU IP traffic source**
* Sample 100 reqAddr values from `hysteria2-client.service` journal
* Cross-reference: WHO sends? Single user? Many users? BT swarms?
* Possible mitigations:
  - Block per-user excessive failed-dial rate (rate-limit at Xray inbound)
  - Filter RU CIDR ranges in Hysteria2 outbound (deny destinations inside RU)
  - Educate / quota P2P-heavy users

### P2 — medium-term (1 week)

**P2.1 — Multi-tunnel with health-based routing**
* Already implemented in code (`StartTunnelHealthMonitor`) — just needs ≥2 exits to actually balance
* Validate weighted distribution works after P1.1

**P2.2 — Monitor SOCKS5 timeouts via metrics**
* Add Prometheus counter for `hysteria2_socks5_timeout_total`
* Alert when rate > N/sec sustained

**P2.3 — Hub vertical scaling decision**
* If concurrent users grow past ~30, 1 vCPU will be limiting
* For now CPU avg 23% across 5-second samples — workable
* Document trigger: "scale at sustained avg idle <50% for 1h"

## Non-goals (this cycle)

* No fixes applied DURING diagnostics — separated cleanly per safety policy.
* No restarts touching live tunnel without explicit user signoff.
* No changes to Xray config — those go through `RequestXraySync()`.

## Test/verify strategy for fixes

After P0.1 + P0.2:
```bash
# 1. Zombie service gone
go run ./cmd/deploy --ssh 'systemctl status hysteria2-client-1' # → not found

# 2. CPU profile cleaner (idle should improve avg)
go run ./cmd/deploy --ssh 'mpstat 1 10'

# 3. Main tunnel healthy
go run ./cmd/deploy --ssh 'systemctl status hysteria2-client | head -10'

# 4. User can ping through tunnel — manual check from RU client
ping -c 30 hiearth.ru   # baseline before/after, check for N/A frequency

# 5. SOCKS5 error rate
go run ./cmd/deploy --ssh 'journalctl -u hysteria2-client --since "5 minutes ago" --no-pager | grep -c "SOCKS5 TCP error"'
```

After P1.1 (second exit-node):
```bash
# servers table shows 2 enabled exits
go run ./cmd/deploy --ssh 'sudo -u postgres psql heyearth -c "SELECT id,name,country_code,status,tunnel_status FROM servers WHERE is_exit_node;"'

# Xray config reflects both exits in balancer
go run ./cmd/deploy --ssh 'grep -c balancer /opt/heyearth/xray/config.json'
```

## Risk register

* **R-01** Restart hysteria2-client briefly drops all users (~3 sec). Accept during off-peak.
* **R-02** Disabling Latvia ghost should be no-op but very theoretically — if any client-side config still expected it as fallback, no harm (already dead 45 days).
* **R-03** Provisioning new exit-node requires SSH + mTLS cert generation. Use existing `cmd/deploy/--setup-exit-node` path.
* **R-04** If SOCKS5 RU-IP flood originates from a few users, blocking will affect them. Communicate or quota gently.

## Deliverable

This document. Plan for fixes goes to `superpowers:writing-plans` as its own cycle once user approves P0 + P1 scope.
