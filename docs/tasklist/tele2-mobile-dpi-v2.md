---
id: tele2-mobile-dpi-v2
beads_id: heyearthproject-ei6j
plan: ../plan/tele2-mobile-dpi-v2.md
status: ready
created: 2026-05-27
---

# Tasklist: Tele2 mobile DPI mitigation v2 (chrome fp + Yandex Cloud SNI)

Зеркало Beads для человекочитаемой навигации. Single source of truth — `bd list --tree heyearthproject-ei6j`.

## Фаза 1: Code change (binary deploy)

- [x] **heyearthproject-9mqz** — T1: Switch Reality/XHTTP fp from ios+safari to chrome+firefox ✓ 2026-05-27
  - Файлы: `server/backend/cmd/api/client_api.go:218-265` + `subscription_handler.go:192-196` (Clash.Meta YAML emitter — discovered as in-scope sister-change)
  - Verify: `go build ./cmd/api/...` exit 0; `grep -nE '"ios"|"safari"' cmd/api/{client_api,subscription_handler}.go` returns empty (exit 1)
  - Commits: `1a6a242` (main: client_api.go), `769625a` (sister: subscription_handler.go HeYearth-Mobile fp safari→firefox)

## Фаза 2: Production state change

- [x] **heyearthproject-nuyf** — T2: Hard-switch reality_sni_domain + restart heyearth-api ✓ 2026-05-28
  - Файлы: prod PostgreSQL `app_settings.reality_sni_domain`, prod service `heyearth-api`
  - Verify: `psql -c "SELECT value FROM app_settings WHERE key='reality_sni_domain'"` = `storage.yandexcloud.net`; `systemctl is-active heyearth-api` = `active`
  - Deployed T1 binary via quick_deploy (22MB, 2.4s), UPDATE 1, restart 16:21:49 MSK

## Фаза 3: Verification

- [x] **heyearthproject-ywbk** — T3: Verify chain DB → xray config → subscription URI ✓ 2026-05-28
  - Verify a: ✓ оба VLESS inbounds `dest=storage.yandexcloud.net:443`, serverNames содержит её
  - Verify b: ✓ `xray.service` active, нет error/fail/panic за 5 мин
  - Verify c: ✓ 4 URI — Берёза 1=chrome, Берёза 2=firefox, Дуб 1=chrome, Дуб 2=firefox; все `sni=storage.yandexcloud.net`
  - Verify d: ✓ TLS probe `storage.yandexcloud.net:443` → Verify return code 0, cert `O=YANDEX LLC, CN=*.storage-1.yandexcloud.net`

- [x] **heyearthproject-u54t** — T4: E2E test on Tele2 mobile ✓ 2026-05-28 PASS
  - Interactive с другом пользователя на Tele2 mobile + Happ
  - Verify: **272 accepted-коннекта за 10 мин** от `5f5f66b7-` (telegram/wps/office/google-analytics) vs ~1/час до фикса
  - Friend report: **Берёза 2 + Дуб 2 (firefox) работают**. Берёза 1/Дуб 1 (chrome) режутся Tele2 Chrome-JA3 DPI — kept for diversity (desktop/др. операторы)
  - Finding записан в `bd remember [tele2-ru-mobile-dpi-targets-chrome-utls-ja3]`

## Side quests / Conditional

- [x] **heyearthproject-harz** — T5: ROLLBACK (conditional, P0) — ✓ NOT NEEDED 2026-05-28
  - Триггер не сработал: T3 чисто, T4 passed. storage.yandexcloud.net IS whitelisted Tele2.
  - Фикс остаётся в проде.

## Discovered side quests
_(пусто — заполнится во время execute если найдём что-то связанное но out-of-scope)_

## Deferred
- **B-фикс** (Hysteria2 primary для UA=Happ) — обсуждается после оценки результата A+C
- Перенос XHTTP-профиля Дуб с :2443 на :443 через nginx stream — если A+C недостаточно
- Wire `reality_fingerprint` setting из app_settings в client_api.go — технический долг (auto-tuner пишет, никто не читает)

## Verification log
- **heyearthproject-9mqz** (T1): [2026-05-27] `go build ./cmd/api/...` exit 0; `grep '"ios"|"safari"'` empty ✓ commits 1a6a242 + 769625a
- **heyearthproject-nuyf** (T2): [2026-05-28 16:21] quick_deploy 22MB ✓; `UPDATE 1` reality_sni_domain=storage.yandexcloud.net; restart 16:21:49 MSK; api+xray active ✓
- **heyearthproject-ywbk** (T3): [2026-05-28] xray config dest=storage.yandexcloud.net:443 (both inbounds) ✓; 4 URI chrome/firefox+new SNI ✓; TLS probe code 0 (Yandex LLC) ✓
- **heyearthproject-u54t** (T4): [2026-05-28 16:35] 272 accepted conns/10min from 5f5f66b7 (telegram/wps/office/google) ✓ friend confirms Берёза 2+Дуб 2 (firefox) work on Tele2 ✓
- **heyearthproject-harz** (T5): [2026-05-28] rollback NOT triggered — fix succeeded, storage.yandexcloud.net whitelisted ✓

## Outcome

**SUCCESS.** Tele2 mobile + Happ restored. Key finding: Tele2 RU DPI targets Chrome uTLS JA3 — fp=firefox profiles (Берёза 2, Дуб 2) work, fp=chrome (Берёза 1, Дуб 1) blocked. SNI=storage.yandexcloud.net (Yandex Cloud ASN) passes Tele2 whitelist. Combined effect: 272 conns/10min vs ~1/hour before. chrome profiles kept for desktop + other carriers (per-user fallback diversity). User decided: leave profile order as-is.
