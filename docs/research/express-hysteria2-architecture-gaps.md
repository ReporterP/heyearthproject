---
id: express-hysteria2-architecture-gaps
beads_id: heyearthproject-3pc0
created: 2026-05-25
---

# Research: Express (Hysteria2) — расхождения с текущей архитектурой

End-to-end flow и 10 точек расхождения с Xray/VLESS production-системой. Базируется на проверке live-state Хаба 2026-05-25.

## End-to-end flow

```
Happ → UDP :50443 (или 50000-50100 с port-hop через iptables REDIRECT)
     → hysteria2-server (Hub, systemd)
     → POST 127.0.0.1:8000/internal/hysteria2/auth (loopback-only)
     → biz.ValidateHysteria2Auth → SELECT users.hysteria2_password
     → принят, форвардит через outbound `via_exit`
     → SOCKS5 127.0.0.1:1090 (manual hysteria client из /opt/heyearth/hysteria/client-2.yaml)
     → QUIC → Exit node NL 192.0.2.126 → интернет
```

Egress IP идентичен VLESS-юзерам с NL exit (geo тот же).

## 10 расхождений

1. **Sync trigger** — Xray debounced 2s + atomic temp+rename + `xray run -test`; Hysteria2 periodic 5min без debounce/atomic/validate.
2. **Per-user identity** — Xray UUID в `clients[]`; Hysteria2 per-request HTTP auth.
3. **Per-plan routing** — Xray plan-N-balancer с weighted exits; Hysteria2 один exit.
4. **Exit failover** — Xray `tunnel-monitor` hysteresis 3/2; Hysteria2 прибит к одному exit.
5. **Hub→Exit tunnel** — Xray использует systemd `hysteria2-client.service`; Express использует НЕ-systemd manual process из `client-2.yaml`. Не в `deploy --status`.
6. **Domain routing** — Xray geosite + direct-route bypass для RU; Hysteria2 ACL `via_exit(all)` (yandex.ru через NL).
7. **Traffic accounting** — Xray per-UUID gRPC stats → `traffic_stats`; Hysteria2 stats API НЕ собирается → cycle limit НЕ работает.
8. **Quota cutoff** — Xray исключает quota-exceeded UUIDs; Hysteria2 валидирует только пароль (квота игнор).
9. **DPI camouflage** — Xray TLS via nginx SNI router + auto-tuner ротирует SNI/fingerprint/xPaddingBytes каждые 4h; Hysteria2 hardcoded Salamander обфс, auto-tuner не трогает.
10. **Plan gating** — Сейчас СНЯТО для preview; `plans.express_enabled` есть но не используется в `client_api.go::buildHubConnectionOptions`.

## Что нужно до GA

| # | Что | Как |
|---|---|---|
| 1 | Quota enforcement | Сборщик hysteria stats API через unix socket → `traffic_stats` записи per-UUID. Cutoff через сброс `users.hysteria2_password` → auth backend rejects |
| 2 | Exit failover | Либо systemd `hysteria2-client.service` (multi-exit fallback), либо tunnel-monitor хук, переписывающий `client-2.yaml` |
| 3 | RU direct-route | Добавить `direct(geoip:ru)` в hysteria2 ACL перед `via_exit(all)` |
| 4 | DPI ротация | Auto-tuner: периодическая смена obfs password (требует client refresh у юзеров) |
| 5 | Sync alignment | `biz.NotifySubscriptionChange()` → также триггерит hysteria2 sync (рукопожатие через app_settings.hysteria2_config_version) |
| 6 | Plan gating вернуть | В `client_api.go` проверять `plan.ExpressEnabled` перед эмитом `hysteria2://` URI |

## Сейчас работает

- hysteria2-server active на UDP :50443
- iptables REDIRECT 50000:50100 → 50443 для port hopping
- Cert access: `ssl-cert` group, hysteria user member, certbot renewal-hook сохраняет perms
- Auth backend: per-user пароль валидируется через POST /internal/hysteria2/auth (loopback-only)
- Outbound: ACL `via_exit(all)` через SOCKS5 1090 → NL exit 192.0.2.126
- URI в подписке: `hysteria2://...?obfs=salamander&...&mport=50000-50100` priority -2 (happ пробует первым)

## Файлы

- `cmd/api/client_api.go::buildHubConnectionOptions` — URI generation
- `cmd/api/hysteria2_sync.go::renderHysteria2Config` — server config render + sync loop
- `cmd/api/hysteria2_auth_handler.go` — auth backend HTTP endpoint
- `internal/biz/hysteria2_auth.go` — ValidateHysteria2Auth + GetUserHysteria2Password (lazy gen)
- `internal/admin/hysteria2_handlers.go` — admin status endpoint
- `cmd/deploy/hysteria2_hub.go` — setup script (binary + systemd unit + ssl-cert group + iptables)
- `migrations/000048_express_hysteria2.{up,down}.sql` — schema (users.hysteria2_password, plans.express_enabled/up_mbps/down_mbps, app_settings.hysteria2_*)
