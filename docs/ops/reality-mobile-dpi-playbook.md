# Reality Mobile DPI — Operations Playbook

**Audience:** support + on-call. When a user reports "Берёза/Дуб не работает" on RU mobile, follow this.

**Status:** living document. Last update 2026-05-26.

---

## 1. Hub topology — what to know cold

- **Hub IP:** `45.150.39.236` (`hiearth.ru`)
- **Hub location:** **MOSCOW, RUSSIA** (AS48282, vdsina.ru)
- **Reality dest / serverNames:** `yandex.ru` (domestic — DPI never blocks it)
- **Reality shortIds:** 2 entries (rotation-ready)
- **uTLS fingerprints offered:** chrome, safari (per profile × 2 profiles = 4 URIs per Reality type)

The Hub being in RU is **load-bearing context**: most foreign Reality bypass advice (use `dl.google.com`, `microsoft.com`, etc.) inverts because of ASN-mismatch considerations. For our specific deployment, domestic `yandex.ru` is correct.

---

## 2. Subscription profiles delivered to Happ

In current priority order (lower = shown first):

| # | Label | Protocol | Reality | Port | uTLS | Why |
|---|-------|----------|---------|------|------|-----|
| 0 | 🌳 Берёза 1 | VLESS Reality TCP + Vision | yes | 443 | chrome | Primary stealth |
| 1 | 🌳 Берёза 2 | VLESS Reality TCP + Vision | yes | 443 | safari | iOS / macOS variant |
| 2 | 🌲 Дуб 1 | VLESS Reality XHTTP | yes | 2443 | chrome | Anti-behavioral DPI (HTTP/1.1 long-poll look) |
| 3 | 🌲 Дуб 2 | VLESS Reality XHTTP | yes | 2443 | safari | iOS / macOS variant |
| 4 | 🎋 Бамбук 1 only WiFi | Hysteria2 QUIC + BBR | no | 50443 UDP | — | Speed under good conditions |
| 5 | 🎋 Бамбук 2 only WiFi | Hysteria2 QUIC + Brutal | no | 50443 UDP | — | Speed under bad conditions |

🌴 Пальма (AmneziaWG) — **NOT in subscription**, server-side preserved (`cmd/api/awg_sync.go`, `internal/biz/awg.go`). happ does not support AmneziaWG.

---

## 3. Per-operator behavior — observed (qualitative)

Based on user reports + 2026 industry sources. **NOT measured by us systematically yet — update this table when you see patterns.**

| Operator | Market share | DPI agg. | Берёза TCP | Дуб XHTTP | Бамбук UDP | Notes |
|----------|--------------|----------|------------|-----------|------------|-------|
| **Tele2** | ~14% | **Highest** (testing ground) | Intermittent fails reported 2026-05 | Intermittent fails reported 2026-05 | Expected fail on mobile (UDP filtering) | New TSPU rules land here first |
| **MTS** | Largest | High (testing ground) | Usually OK | Usually OK | Expected fail on mobile | Softer than Rostelecom backbone per sources |
| **Megafon** | Mid | Mid | OK | OK | Mobile fail expected | Less data |
| **Beeline** | Mid | Mid | OK | OK | Mobile fail expected | "Somewhere in the middle" |
| **Rostelecom** (fixed) | — | High on backbone, slow rollout | OK | OK | Mostly OK on WiFi | Receives rules 2-4 weeks after mobile |

**Rule of thumb:** if a user is on **Tele2 or MTS mobile** and reports problems, they're seeing a fresh TSPU rule that hasn't propagated nationally yet. Bug reports from Tele2 are early-warnings for everyone else.

---

## 4. Diagnostic flow for "Reality не работает"

When a user reports issues, ask in this order (each takes 30 sec to answer):

### Q1: Which profile fails?
- Берёза only → Reality TCP path issue (very rare — most stable profile)
- Дуб only → Reality XHTTP path issue (more common — newer transport)
- Both Берёза + Дуб fail → carrier-wide Reality block in progress, or local DNS/route issue
- Only Бамбук fails → UDP/QUIC filter (expected on mobile, normal on WiFi)

### Q2: Operator + connection type?
- Tele2 mobile → expected canary territory. Document the date + which profile fails.
- MTS mobile → also canary. Same.
- WiFi (any operator) → if Reality fails on WiFi, this is bigger — escalate.

### Q3: Did profiles ever work?
- Worked yesterday, broken today → fresh TSPU rule. Wait 24h, often self-resolves as operator rule changes.
- Never worked from start → check user has refreshed subscription after last config change (`profile-update-interval` is 4h)

### Q4: Has subscription been refreshed?
Ask user to do "Pull-to-refresh" in Happ. After our 2026-05-26 ship (T1.1 + T1.2), URIs MUST contain `&spx=%2F`. If they don't, user is on stale subscription.

### Q5 (advanced): try a different uTLS fingerprint
We ship chrome + safari per profile. If chrome fails, safari might pass on the same network (DPI may key on JA3 hash). User can manually switch in Happ.

---

## 5. Quick fixes (in order of escalation)

### Level 0 — User-side, instant

1. **Pull-to-refresh** subscription in Happ (most common fix after our deploy)
2. **Try different profile** — Берёза 2 vs Берёза 1, Дуб vs Берёза
3. **Switch Wi-Fi ↔ mobile** to compare
4. **Restart Happ** — sometimes clears a stuck TCP session

### Level 1 — Server-side, no code changes

1. **Force-resync Xray config** — `systemctl restart heyearth-api` (triggers fresh sync)
2. **Check Xray running** — `systemctl status xray`; restart if dead: `systemctl restart xray`
3. **Check sockopt active** — `grep -B1 -A6 tcpKeepAlive /opt/heyearth/xray/config.json` should show 2 inbound blocks + 1 outbound

### Level 2 — Config tweaks (try one at a time, atomic rollback)

1. **Try alternative SNI** for a small user cohort — `UPDATE app_settings SET value='microsoft.com' WHERE key='reality_sni_domain';` then restart heyearth-api. **Rollback:** same command with `yandex.ru`.
2. **Widen shortIds rotation** — add a third shortId via UPDATE in `app_settings` (`reality_short_id` currently has 1 named + 1 empty)
3. **Toggle xPaddingBytes range** — currently `100-1000`. Try `200-1500` if TSPU starts pinning padding.

### Level 3 — Architectural (planned, not yet shipped)

See `docs/research/2026-05-26-reality-mobile-dpi-deep-dive.md` Section D Tier 3:
- CDN fronting (Cloudflare Reality)
- Multi-SNI rotation per user
- Alternative protocols (TUIC, ShadowTLS)

---

## 6. Evidence commands (paste-ready)

```bash
# Hub overall health
cd server/backend && go run ./cmd/deploy --status

# heyearth-api logs (last 50)
go run ./cmd/deploy --ssh 'journalctl -u heyearth-api -n 50 --no-pager'

# Xray service status
go run ./cmd/deploy --ssh 'systemctl status xray --no-pager | head -10'

# Verify sockopt in deployed xray config (T1.2)
go run ./cmd/deploy --ssh 'grep -B1 -A6 tcpKeepAlive /opt/heyearth/xray/config.json'

# Verify spx in subscription (T1.1) — replace TOKEN with real subscribe_token
go run ./cmd/deploy --ssh 'curl -s -A "Happ/2.0.0" https://hiearth.ru/api/v1/user/subscribe/<TOKEN> | grep -oE "spx=[^&#]*"'

# Get a real test token (any user with active sub)
go run ./cmd/deploy --ssh 'sudo -u postgres psql -d heyearth -tA -c "SELECT u.subscribe_token FROM users u JOIN user_subscriptions s ON s.user_id=u.id WHERE s.status='\''active'\'' AND s.valid_until > NOW() LIMIT 1;"'

# Active Reality SNI in DB
go run ./cmd/deploy --ssh 'sudo -u postgres psql -d heyearth -tA -c "SELECT key, value FROM app_settings WHERE key LIKE '\''%reality%'\''"'
```

---

## 7. Deploy checklist after any Reality-related change

After ANY change to `client_api.go` (URI emission) or `xray_sync.go` (Xray config):

```bash
cd server/backend

# 1. Compile (mandatory — never deploy uncompiled)
go build ./...

# 2. Stop service (avoid binary-while-running issues)
go run ./cmd/deploy --ssh 'systemctl stop heyearth-api && rm -f /opt/heyearth/mainserver/heyearth-api'

# 3. Deploy via SSH pipe (SFTP hangs on this hosting)
go run quick_deploy.go

# 4. Verify Xray accepted the new config — NO "validation failed" in logs
go run ./cmd/deploy --ssh 'journalctl -u heyearth-api -n 30 --no-pager | grep -iE "xray-sync|validation|reality" | tail -10'

# 5. Verify subscription URI shape (e.g. spx=, sni=, fp=)
go run ./cmd/deploy --ssh 'curl -s -A "Happ/2.0.0" https://hiearth.ru/api/v1/user/subscribe/<TOKEN> | head -c 800'
```

**Critical:** if you see `cannot unmarshal string into Go struct field` in step 4 — your config types are wrong. Xray sync is silently failing and your changes are NOT live until you fix it (this exact bug bit us on 2026-05-26 with `scMaxBufferedPosts`).

---

## 8. Change log

- **2026-05-26** — Shipped T1.1 (spx=%2F in 4 Reality URIs) + T1.2 (bilateral sockopt tcpKeepAlive on both Reality inbounds) + T9-followup (fixed scMaxBufferedPosts int64 type bug). Commit `734e3b5`.
- **2026-05-26** — Discovered Hub is physically in RU (Moscow, vdsina AS48282), revoking Tier 2 dest-swap recommendation. Decision: keep `reality_sni_domain = yandex.ru`.
- **prior** — R1+R2+R3 mobile DPI mitigations (Hysteria2 port range 30000-65000, hop_interval=15, PMTUD disable). See `docs/research/2026-05-26-mobile-dpi-tele2.md`.
