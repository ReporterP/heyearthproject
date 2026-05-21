# Tasklist — VPN Stability P1

> Human-readable task overview. Beads holds task metadata; this file holds evidence-commands and human context.

**Epic:** `heyearthproject-w3ry` — VPN stability diagnostics
**Spec:** [docs/superpowers/specs/2026-05-21-vpn-stability-diagnostics-design.md](../superpowers/specs/2026-05-21-vpn-stability-diagnostics-design.md)
**Plan:** [docs/superpowers/plans/2026-05-21-vpn-stability-p1.md](../superpowers/plans/2026-05-21-vpn-stability-p1.md)

## Status

✅ **P0.1 applied 2026-05-21 19:37 MSK** — Latvia ghost systemd-unit stopped, disabled, archived. Measured: CPU idle 77.5→84.5%, interrupts 4000→1300-3100, SOCKS5 errors stream→7/min. User reported "no perceived improvement" — main flood factor still active.

🟡 **P1 ready for execution** (4 tasks below)

⏭️ **P2 deferred** — second exit-node (SPOF), vdsina noisy-neighbor steal, automatic abuser-block based on dial_rate_log.

---

## Tasks

### `heyearthproject-h4oq` — B1: block XMPP ports 5222/5223 in Xray routing (P1)

**Why:** 26 of 28 SOCKS5 errors target `www.google.com:5222` (Google Talk, dead since 2017). Chinese stock ROMs (OPPO/Realme/Vivo) infinite-retry forever. Block at Xray routing → never even reaches Hysteria2.

**Files:** `server/backend/cmd/api/xray_sync.go` (~line 329)

**Evidence before fix:**
```bash
go run ./cmd/deploy --ssh "journalctl -u xray --since '5 minutes ago' --no-pager | grep -c ':5222'"
# → ~47 / 5min  (4% of all dials)
```

**Evidence after fix:**
```bash
go run ./cmd/deploy --ssh "journalctl -u hysteria2-client --since '5 minutes ago' --no-pager | grep -c 'SOCKS5 TCP error'"
# → < 5 / 5min  (was 28)
```

---

### `heyearthproject-5yzw` — B2: ACL block-list in Hysteria2 client.yaml on hub (P1)

**Why:** Even if Xray rule misses something, the Hysteria2 client itself can reject obvious garbage (private CIDRs, port 0, BitTorrent ports) locally instead of forwarding to Amsterdam to time out.

**Files:** `/opt/heyearth/hysteria/client.yaml` (on hub — hand-managed)

**Pre-flight:**
```bash
go run ./cmd/deploy --ssh "cat /opt/heyearth/hysteria/client.yaml"   # record original
go run ./cmd/deploy --ssh "cp /opt/heyearth/hysteria/client.yaml /opt/heyearth/state/zombie-backup-2026-05-21/client.yaml.before-acl"
```

**Evidence after fix:**
```bash
go run ./cmd/deploy --ssh "python3 -c 'import yaml; yaml.safe_load(open(\"/opt/heyearth/hysteria/client.yaml\"))' && echo YAML_OK"
go run ./cmd/deploy --ssh "systemctl is-active hysteria2-client"   # → active
go run ./cmd/deploy --ssh "journalctl -u hysteria2-client --since '5 minutes ago' --no-pager | grep -E 'SOCKS5 TCP error.*(:6881|:6882|:0\")' | wc -l"
# → 0
```

⚠️ **Tunnel restart interrupts all users ~3 sec.** Do during off-peak.

**Rollback:**
```bash
go run ./cmd/deploy --ssh "cp /opt/heyearth/state/zombie-backup-2026-05-21/client.yaml.before-acl /opt/heyearth/hysteria/client.yaml && systemctl restart hysteria2-client"
```

---

### `heyearthproject-j2d9` — B3: per-user dial-rate monitoring (P2 priority, observation only)

**Why:** Top-3 users = 67% of all xray dials. One user `a4ef01a9` sustained 1.55 dial/sec = 10× median. Need data to identify abuse vs heavy-legit-use vs broken-apps. **Observation only this cycle** — automatic blocking is a separate decision after 1 week of data.

**Files (create):**
* `server/backend/migrations/000038_dial_rate_log.up.sql`
* `server/backend/migrations/000038_dial_rate_log.down.sql`
* `server/backend/internal/biz/dial_rate.go`
* `server/backend/cmd/api/dial_rate_monitor.go`

**Files (modify):**
* `server/backend/cmd/api/main.go` — register `StartDialRateMonitor()` next to other background loops

**Threshold:** 100 dials/min (tunable via `app_settings.dial_rate_threshold_per_min`).

**Evidence after deploy (wait 3-5 min):**
```bash
go run ./cmd/deploy --ssh "journalctl -u heyearth-api --since '90 seconds ago' --no-pager | grep -E 'dial-rate-monitor'"
# → "[dial-rate-monitor] запущен (окно 60 сек)"

go run ./cmd/deploy --ssh "sudo -u postgres psql heyearth -c 'SELECT user_uuid, dials_in_window, threshold_exceeded FROM dial_rate_log ORDER BY window_start DESC LIMIT 10;'"
# → >= 5 rows, threshold_exceeded=true for known top users (a4ef01a9, 6b18ef35, a5b0c747)
```

---

### `heyearthproject-g6gl` — Verification: end-to-end after B1+B2+B3 (P2, blocked by all three)

**Why:** Single point to confirm wave-pattern improved.

**Evidence:**
```bash
# 1. XMPP rule active
go run ./cmd/deploy --ssh "journalctl -u xray --since '5 minutes ago' --no-pager | grep -E ':5222|:5223' | head -5"
# → empty or [blocked]

# 2. SOCKS5 flood gone
go run ./cmd/deploy --ssh "journalctl -u hysteria2-client --since '5 minutes ago' --no-pager | grep -c 'SOCKS5 TCP error'"
# → < 5

# 3. dial_rate observer working
go run ./cmd/deploy --ssh "sudo -u postgres psql heyearth -c \"SELECT count(*) AS samples, count(*) FILTER (WHERE threshold_exceeded) AS exceeded, MAX(dials_in_window) AS peak FROM dial_rate_log WHERE window_start > NOW() - INTERVAL '15 minutes';\""
# → samples >= 10

# 4. CPU paint
go run ./cmd/deploy --ssh "mpstat 2 10"
# → avg idle >= 86%, softirq <= 2.0%

# 5. User smoke test
# Ask user: 30-min continuous ping from RU client, count N/A intervals vs baseline
```

---

## Dependencies (Beads)

```
heyearthproject-w3ry (epic)
├─ heyearthproject-h4oq (B1 XMPP block)
├─ heyearthproject-5yzw (B2 Hysteria2 ACL)
├─ heyearthproject-j2d9 (B3 dial-rate monitor)
└─ heyearthproject-g6gl (verification) ← blocked by all three above
```

Available ready-to-work via `bd ready`:
* `heyearthproject-h4oq` (B1)
* `heyearthproject-5yzw` (B2)
* `heyearthproject-j2d9` (B3)
