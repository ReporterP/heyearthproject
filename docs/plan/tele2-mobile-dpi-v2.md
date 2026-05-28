---
id: tele2-mobile-dpi-v2
beads_id: heyearthproject-ei6j
prd: ../prd/tele2-mobile-dpi-v2.prd.md
status: done
created: 2026-05-27
---

# Plan: Tele2 mobile DPI mitigation v2 (chrome fp + Yandex Cloud SNI)

> **For agentic workers:** REQUIRED SUB-SKILL: Use `superpowers:subagent-driven-development` (recommended) or `superpowers:executing-plans` to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Сменить uTLS fingerprint (ios/safari → chrome/firefox) и Reality SNI (yandex.ru → storage.yandexcloud.net) чтобы Tele2 mobile DPI перестал детектить наши Reality-сессии как подозрительные.

**Architecture:** Hard switch одной SQL-командой `app_settings.reality_sni_domain` + двумя правками в `cmd/api/client_api.go`. Никакой миграции, никаких feature flag'ов, никаких dual inbounds. Auto-tuner отключён (verified) — не нужно трогать его pool. Rollback = одна обратная SQL-команда + `systemctl restart heyearth-api`.

**Tech Stack:** Go 1.22 backend, PostgreSQL 16, Xray-core 26.x VLESS+Reality, deploy через `quick_deploy.go` (SSH-pipe), evidence-driven verification (тестов в репо нет).

---

## Контекст из кодовой базы

**Точка управления Reality SNI — одна:** `app_settings.reality_sni_domain` (default `apple.com`, текущее `yandex.ru`).

Каскад изменения этой настройки:
- **Server-side** `cmd/api/xray_sync.go:207-221, 267-268` — генерирует `dest: <sni>:443` и `serverNames: [<sni>, www.<sni>]` для inbounds `vless-reality` (TCP :10443) и `vless-xhttp` (XHTTP :2443)
- **Client-side URI** `cmd/api/client_api.go:181` — читает в `realitySNI`, вставляет в `sni=` поле VLESS URI для всех 4 профилей (Берёза 1+2, Дуб 1+2)
- **Subscription text format** `cmd/api/subscription_handler.go:183` — то же для plain-text формата (для legacy клиентов)

**Initial Xray sync на startup heyearth-api:** `cmd/api/xray_sync.go:1189-1199` — `StartXraySyncLoop()` вызывает `SyncXrayConfig()` сразу при старте, до запуска debounce-loop'а. Значит после `systemctl restart heyearth-api` xray-config пересобирается с новой SNI в течение секунды.

**Fingerprint в URI хардкожен**, не читается из `app_settings.reality_fingerprint`:
- `cmd/api/client_api.go:230` — `realityFPs := []string{"ios", "safari"}` для Берёза 1+2 (TCP Reality)
- `cmd/api/client_api.go:250` — `xhttpFPs := []string{"ios", "safari"}` для Дуб 1+2 (XHTTP Reality)

Auto-tuner (`cmd/api/auto_tuner.go:127`) имеет `candidateFingerprints = []string{"chrome", "firefox", "safari", "edge"}` — chrome+firefox уже в пуле, но **auto_tuner_enabled = false** в prod с 26 мая (last_run 21 апреля, status idle). Это значит:
- Изменение `auto_tuner.go::sniByCountry["RU"]` бесполезно — код не выполняется
- Хардкоженный `realityFPs/xhttpFPs` в client_api.go — единственная точка для fp-фикса

**Похожие паттерны в репозитории:**
- Hard SNI switch уже делался при включении CDN: `migrations/000052_cdn_fronting.up.sql` тоже использовал app_settings + RequestXraySync — паттерн отработан
- Деплой backend через `quick_deploy.go` уже используется десятки раз, stop service → SSH-pipe binary → start

---

## Архитектурное решение

**Hard switch без миграционной логики.** Одно изменение в `app_settings.reality_sni_domain` + `systemctl restart heyearth-api` каскадно пересоберёт xray config и начнёт эмитить новые URI. Существующие подписки в клиентах сломаются на 1-2 минуты до автоматического refresh при открытии Happ (`happ-distribution` issue #142 подтверждает auto-refresh при foreground).

**Рассмотренные альтернативы (из brainstorming):**
1. **Выбрано:** Hard switch — простой код, простой rollback, минимум сюрпризов. Downtime приемлем (Happ refresh-ит подписку автоматически).
2. Отклонено: Multi-SNI в одном inbound (yandex.ru + storage.yandexcloud.net в serverNames) — Reality `dest` это один backend, нельзя сразу двум reproxy-хостам. Усложнение без выгоды.
3. Отклонено: Dual Reality inbounds на разных портах с grace period 7 дней — требует new keypair + параллельный xray config, ломает простоту single source of truth.
4. Отклонено: Feature flag в app_settings (`reality_sni_target` enum) — конкретно для этого изменения rollback и так делается одной SQL-командой, флаг бы только размывал ответственность.

**Fingerprint выбор chrome+firefox а не chrome+chrome:** диверсификация ClientHello JA3 внутри одной подписки — если оператор когда-то добавит fp=chrome в blocklist (теоретический риск), вторая запись (firefox) останется рабочей.

---

## Затрагиваемые файлы

### Изменить (code)
- `server/backend/cmd/api/client_api.go:230,250` — fp arrays `[ios,safari]` → `[chrome,firefox]`, обновить рядом стоящие comment-блоки (строки 206-229 и 246-249)

### Изменить (production state, не code)
- PostgreSQL prod `app_settings` row `reality_sni_domain`: `yandex.ru` → `storage.yandexcloud.net`
- Прод service `heyearth-api`: restart для re-trigger initial Xray sync

### НЕ менять (намеренно)
- `cmd/api/auto_tuner.go::sniByCountry["RU"]` — auto_tuner_enabled=false, мёртвый код для этой задачи
- `cmd/api/xray_sync.go` — generator берёт SNI из app_settings, ничего перекодить
- `cmd/api/subscription_handler.go` — то же
- `internal/biz/*` — нет затронутой бизнес-логики
- Migration files — изменение `app_settings.value` не схемное, не нужна миграция

---

## Декомпозиция (5 задач)

### Task 1: Сменить fingerprint в client_api.go на chrome+firefox

**Beads:** создать sub-task через `bd create` с parent-child связью к `heyearthproject-ei6j`.

**Files:**
- Modify: `server/backend/cmd/api/client_api.go:206-265` (Берёза + Дуб блоки)

- [ ] **Step 1: Прочитать актуальный код** (для exact match при Edit)

```bash
cd C:/Users/alex/Documents/heyearthproject/server/backend
# Покажет 30 строк вокруг текущего realityFPs slice
sed -n '218,232p' cmd/api/client_api.go
```

Expected: видим хардкоженный `realityFPs := []string{"ios", "safari"}` на строке 230 + длинный комментарий выше про «TSPU targets Chrome JA3».

- [ ] **Step 2: Заменить Берёза fp + комментарий**

Edit `cmd/api/client_api.go`:

OLD (строки 218-230):
```go
			// 🌳 Берёза 1..2 — TCP Reality :443, 2 uTLS variants.
			// URI params restored to pre-Hysteria2 working state — NO spx, NO fragment,
			// NO alt-port/alt-IP variants. User-reported these additions broke
			// the previously-working Reality path. Keep tcpKeepAlive* and flow at end
			// (load-bearing for happ-desktop URI parser, verified working pre-Hysteria2).
			// uTLS fingerprints: ios + safari (both Apple-style, less commonly fingerprinted
// by RU TSPU). chrome was previously the first fingerprint but user reports
// Берёза 1 (chrome) and Дуб 1 (chrome) both fail to connect ("n/a"), while
// Берёза 2 and Дуб 2 (safari) work — TSPU has started targeting Chrome JA3
// signatures specifically because Chrome is the most popular uTLS profile
// used by VPN clients. Switching the first profile to ios (Apple mobile)
// keeps two distinct fingerprints for diversity and avoids the blocklist.
realityFPs := []string{"ios", "safari"}
```

NEW:
```go
			// 🌳 Берёза 1..2 — TCP Reality :443, 2 uTLS variants.
			// URI params restored to pre-Hysteria2 working state — NO spx, NO fragment,
			// NO alt-port/alt-IP variants. User-reported these additions broke
			// the previously-working Reality path. Keep tcpKeepAlive* and flow at end
			// (load-bearing for happ-desktop URI parser, verified working pre-Hysteria2).
			//
			// uTLS fingerprints: chrome + firefox.
			// Rationale (research: docs/research/2026-05-27-happ-tele2-tls-handshake.md):
			// fp=ios and fp=safari have documented Xray-core regressions — XTLS #5918
			// ("firstLen=0, EOF" on iOS+XHTTP: TLS handshake completes but HTTP data
			// never flows) and XTLS #4852 (uTLS ClientHello extension order). These
			// regressions manifest on Tele2 mobile DPI as «плавающая ошибка TLS
			// рукопожатия» in Happ ping check. Chrome is the most-tested uTLS profile
			// (used by 80% of Reality clients) and has no open regressions. Firefox
			// is the second-most-tested, kept as JA3 diversification within one
			// subscription.
			realityFPs := []string{"chrome", "firefox"}
```

- [ ] **Step 3: Заменить Дуб fp + комментарий**

OLD (строки 246-250):
```go
			// 🌲 Дуб 1..2 — XHTTP Reality :2443, 2 uTLS variants.
			// Pre-Hysteria2 working URI shape — no spx, no fragment.
			xhttpPort := biz.GetSettingOrDefault("xhttp_port", "2443")
			// Same rationale as realityFPs above — chrome → ios для Дуб 1.
xhttpFPs := []string{"ios", "safari"}
```

NEW:
```go
			// 🌲 Дуб 1..2 — XHTTP Reality :2443, 2 uTLS variants.
			// Pre-Hysteria2 working URI shape — no spx, no fragment.
			xhttpPort := biz.GetSettingOrDefault("xhttp_port", "2443")
			// Same rationale as realityFPs above (research 2026-05-27) — chrome + firefox
			// to avoid ios/safari XTLS regressions, especially XHTTP-specific #5918.
			xhttpFPs := []string{"chrome", "firefox"}
```

- [ ] **Step 4: Compile check**

```bash
cd C:/Users/alex/Documents/heyearthproject/server/backend
go build ./cmd/api/...
echo "BUILD_EXIT=$?"
```

Expected: `BUILD_EXIT=0` без ошибок компиляции.

- [ ] **Step 5: Локальный sanity grep — убедиться нигде не осталось ios/safari в emitter'ах**

```bash
grep -nE '"ios"|"safari"' cmd/api/client_api.go cmd/api/subscription_handler.go
```

Expected: пусто (либо только в неактивных комментариях, не в коде).

- [ ] **Step 6: Создать Beads sub-task + commit**

```bash
cd C:/Users/alex/Documents/heyearthproject
bd create "T1: Switch Reality/XHTTP fp from ios+safari to chrome+firefox in client_api.go" -t task -p 1
# Запомнить вернувшийся ID, например heyearthproject-AAAA
bd dep add <T1-id> heyearthproject-ei6j --type parent-child
bd update <T1-id> --claim --status=in_progress
```

```bash
cd C:/Users/alex/Documents/heyearthproject
git add server/backend/cmd/api/client_api.go
git commit -m "$(cat <<'EOF'
feat(reality): switch URI fp from ios+safari to chrome+firefox

Берёза 1+2 and Дуб 1+2 emit fp=chrome (first variant) + fp=firefox
(second variant) instead of fp=ios + fp=safari.

Motivation (docs/research/2026-05-27-happ-tele2-tls-handshake.md):
- XTLS #5918 — ios+XHTTP "firstLen=0, EOF" regression
- XTLS #4852 — ios/safari uTLS ClientHello extension order quirks
- Above manifest on Tele2 mobile DPI as flapping "TLS handshake error"
  in Happ ping check
- chrome is the most-tested uTLS profile; firefox kept for JA3 diversity

Beads: heyearthproject-ei6j (T1)

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

- [ ] **Step 7: Close T1 in Beads**

```bash
bd close <T1-id>
```

---

### Task 2: SQL UPDATE `reality_sni_domain` + restart heyearth-api

**Files:**
- Modify: PostgreSQL prod row `app_settings.reality_sni_domain`
- Restart: `heyearth-api.service`

- [ ] **Step 1: Создать Beads sub-task**

```bash
cd C:/Users/alex/Documents/heyearthproject
bd create "T2: Hard-switch reality_sni_domain to storage.yandexcloud.net + restart api" -t task -p 1
bd dep add <T2-id> heyearthproject-ei6j --type parent-child
bd dep add <T2-id> <T1-id> --type blocks
bd update <T2-id> --claim --status=in_progress
```

- [ ] **Step 2: Deploy T1 binary в прод**

```bash
cd C:/Users/alex/Documents/heyearthproject/server/backend
# quick_deploy.go — SSH-pipe binary upload, idempotent, no SFTP
go run ./cmd/deploy --ssh 'systemctl stop heyearth-api && rm -f /opt/heyearth/mainserver/heyearth-api'
go run quick_deploy.go
```

Expected: SSH stream показывает upload binary + автоматический start через systemd. Финальная строка `heyearth-api active (running)`.

- [ ] **Step 3: Записать baseline текущей SNI** (для возможного rollback)

```bash
cd C:/Users/alex/Documents/heyearthproject/server/backend
go run ./cmd/deploy --ssh "sudo -u postgres psql -d heyearth -t -c \"SELECT value FROM app_settings WHERE key='reality_sni_domain';\""
```

Expected output: `yandex.ru` (текущее значение, нужно запомнить для rollback).

- [ ] **Step 4: SQL UPDATE на storage.yandexcloud.net**

```bash
cd C:/Users/alex/Documents/heyearthproject/server/backend
go run ./cmd/deploy --ssh "sudo -u postgres psql -d heyearth -c \"UPDATE app_settings SET value='storage.yandexcloud.net', updated_at=NOW() WHERE key='reality_sni_domain';\""
```

Expected: `UPDATE 1`.

- [ ] **Step 5: Restart heyearth-api** (триггерит StartXraySyncLoop → SyncXrayConfig → write new xray config.json → reload xray)

```bash
cd C:/Users/alex/Documents/heyearthproject/server/backend
go run ./cmd/deploy --ssh 'systemctl restart heyearth-api && sleep 3 && systemctl is-active heyearth-api'
```

Expected: `active`. Если `failed` — немедленно перейти к Task 5 (Rollback).

- [ ] **Step 6: Записать DB state после изменения**

```bash
cd C:/Users/alex/Documents/heyearthproject/server/backend
go run ./cmd/deploy --ssh "sudo -u postgres psql -d heyearth -t -c \"SELECT key, value, updated_at FROM app_settings WHERE key='reality_sni_domain';\""
```

Expected output: `reality_sni_domain | storage.yandexcloud.net | <дата сегодня>`.

- [ ] **Step 7: Close T2 в Beads**

```bash
bd close <T2-id>
```

Никакого git commit — production state change, не кода.

---

### Task 3: Verify chain — DB → xray config → URI

**Files:**
- Read-only verification на проде

- [ ] **Step 1: Создать Beads sub-task**

```bash
cd C:/Users/alex/Documents/heyearthproject
bd create "T3: Verify SNI+fp chain — DB to xray config to subscription URI" -t task -p 1
bd dep add <T3-id> heyearthproject-ei6j --type parent-child
bd dep add <T3-id> <T2-id> --type blocks
bd update <T3-id> --claim --status=in_progress
```

- [ ] **Step 2: Verify xray config.json содержит storage.yandexcloud.net**

```bash
cd C:/Users/alex/Documents/heyearthproject/server/backend
go run ./cmd/deploy --ssh "jq '.inbounds[] | select(.protocol==\"vless\") | {tag, port, dest: .streamSettings.realitySettings.dest, serverNames: .streamSettings.realitySettings.serverNames}' /opt/heyearth/xray/config.json"
```

Expected: для `vless-reality` (port 10443) и `vless-xhttp` (port 2443):
```json
{
  "tag": "vless-reality",
  "port": 10443,
  "dest": "storage.yandexcloud.net:443",
  "serverNames": ["storage.yandexcloud.net", "www.storage.yandexcloud.net"]
}
```

Если `dest` всё ещё `yandex.ru:443` — heyearth-api не пересобрал config. Перезапустить ещё раз: `systemctl restart heyearth-api && sleep 5`.

- [ ] **Step 3: Verify xray.service здоров (нет crash-loop)**

```bash
cd C:/Users/alex/Documents/heyearthproject/server/backend
go run ./cmd/deploy --ssh "systemctl is-active xray.service && journalctl -u xray.service --since '2 minutes ago' --no-pager | grep -iE 'error|fail|panic' | head -20"
```

Expected: `active`, и grep пустой (нет errors). Если xray crashed/restarting — перейти к T5 Rollback.

- [ ] **Step 4: Verify VLESS URI содержит chrome+firefox + storage.yandexcloud.net**

```bash
cd C:/Users/alex/Documents/heyearthproject/server/backend
# subscribe_token взят из PRD — это наш test-юзер Tele2 mobile + desktop combo
go run ./cmd/deploy --ssh "curl -sk --resolve hiearth.ru:443:127.0.0.1 -H 'User-Agent: Happ' https://hiearth.ru/api/v1/user/subscribe/d329def4-c54f-4448-9721-8e420fd21425 -o /tmp/sub2.txt && grep -oE 'vless://[^[:space:]]+' /tmp/sub2.txt | head -4"
```

Expected: 4 URI, в каждом `sni=storage.yandexcloud.net`, и `fp=chrome` / `fp=firefox` чередуются:
- Берёза 1: `...&sni=storage.yandexcloud.net&fp=chrome&...#%F0%9F%8C%B3%20%D0%91%D0%B5%D1%80%D1%91%D0%B7%D0%B0%201`
- Берёза 2: `...&sni=storage.yandexcloud.net&fp=firefox&...#...Берёза%202`
- Дуб 1: `...&sni=storage.yandexcloud.net&fp=chrome&...&type=xhttp...#...Дуб%201`
- Дуб 2: `...&sni=storage.yandexcloud.net&fp=firefox&...&type=xhttp...#...Дуб%202`

- [ ] **Step 5: Sanity TLS-probe на новый dest с самого Hub**

```bash
cd C:/Users/alex/Documents/heyearthproject/server/backend
go run ./cmd/deploy --ssh "echo | openssl s_client -connect storage.yandexcloud.net:443 -servername storage.yandexcloud.net 2>&1 | grep -E 'subject=|issuer=|Verification' | head -5"
```

Expected: видим валидный TLS сертификат от Yandex CA (subject содержит `*.yandexcloud.net`). Это подтверждает что Hub может реально reverse-proxy туда (требование Reality dest reachability).

- [ ] **Step 6: Close T3 в Beads**

```bash
bd close <T3-id>
```

---

### Task 4: E2E test на Tele2 mobile с другом пользователя

**Files:**
- None — это interactive test со внешним user'ом

- [ ] **Step 1: Создать Beads sub-task**

```bash
cd C:/Users/alex/Documents/heyearthproject
bd create "T4: E2E verify on Tele2 mobile (Happ refresh + ping + browse)" -t task -p 1
bd dep add <T4-id> heyearthproject-ei6j --type parent-child
bd dep add <T4-id> <T3-id> --type blocks
bd update <T4-id> --claim --status=in_progress
```

- [ ] **Step 2: Очистить буфер xray-лога чтобы видеть только новые события**

```bash
cd C:/Users/alex/Documents/heyearthproject/server/backend
# Запомнить current journal cursor, чтобы дальше тейлить только новое
go run ./cmd/deploy --ssh "date -Iseconds"
```

Expected: текущее время UTC. Запомнить для следующих grep'ов как `--since '<этот timestamp>'`.

- [ ] **Step 3: Попросить пользователя выполнить на телефоне на Tele2:**

> 1. Открыть Happ
> 2. **Refresh подписки** (свайп вниз на главном экране или Setting → Update Subscription)
> 3. Выбрать профиль **Берёза 1** (первый в списке)
> 4. Включить VPN
> 5. Нажать **Ping** — 3 раза подряд, с интервалом ~3 секунды
> 6. Открыть в браузере **google.com** — дождаться полной загрузки
> 7. Открыть **t.me** — дождаться

Между шагами 3-6 пользователь сообщает наблюдаемые результаты в чат.

- [ ] **Step 4: Параллельно смотреть xray-лог на наличие accepted-коннектов от UUID `0feec1d4-...`**

```bash
cd C:/Users/alex/Documents/heyearthproject/server/backend
# T_START = timestamp из Step 2
go run ./cmd/deploy --ssh "journalctl -u xray.service --since '<T_START>' --no-pager | grep -E '5f5f66b7-6fbc-4b08-b4cd-8d6c8cd6dffc' | head -30"
```

Expected: видим минимум 10+ accepted-записей за окно теста, destinations включают `google.com` и `149.154.x` (Telegram DC). Время между accepted — секунды, не «1 раз в минуту».

- [ ] **Step 5: Проверить отсутствие новых rejected-записей**

```bash
cd C:/Users/alex/Documents/heyearthproject/server/backend
go run ./cmd/deploy --ssh "journalctl -u xray.service --since '<T_START>' --no-pager | grep -iE 'rejected|invalid request user id' | head -10"
```

Expected: либо пусто, либо только не-наши UUID. Если есть rejected с `0feec1d4-...` — Reality auth fail, проверить что keypair не изменился.

- [ ] **Step 6: Спросить пользователя про субъективный результат**

> Друг сообщил:
> 1. Ping показал стабильно OK 3 раза подряд? (без «плавающей TLS handshake error»)
> 2. Google.com открылся через VPN?
> 3. Telegram открылся через VPN?

Если ВСЕ три = да → переход к Step 7 (success).
Если 1+ из них = нет → переход к Task 5 (Rollback).

- [ ] **Step 7: Записать findings + Close T4**

```bash
cd C:/Users/alex/Documents/heyearthproject
bd update <T4-id> --notes "E2E pass: ping stable 3/3, google+telegram open, xray accepted N entries from 5f5f66b7 in test window"
bd close <T4-id>
```

- [ ] **Step 8: Записать insight в Beads memory для будущих сессий**

```bash
cd C:/Users/alex/Documents/heyearthproject
bd remember "Tele2 mobile DPI fix v2 (2026-05-27): switched Reality SNI yandex.ru → storage.yandexcloud.net + fp ios/safari → chrome/firefox. Tele2 whitelist allows Yandex Cloud ASN. Single SQL toggle + 2-line code change. Rollback: UPDATE app_settings SET value='yandex.ru' WHERE key='reality_sni_domain' + restart heyearth-api."
```

---

### Task 5: Rollback (CONDITIONAL — выполняется только если T3/T4 fail)

**Files:**
- Modify: PostgreSQL prod row `app_settings.reality_sni_domain`
- Optional: revert commit T1

**Trigger conditions:**
- T3 Step 3: xray.service в crash-loop
- T3 Step 4: VLESS URI emit-ит неправильное значение SNI или fp
- T3 Step 5: openssl s_client не подключается к storage.yandexcloud.net:443
- T4 Step 6: друг говорит что ping всё ещё flapping и сайты не открываются

- [ ] **Step 1: Создать Beads sub-task если триггер сработал**

```bash
cd C:/Users/alex/Documents/heyearthproject
bd create "T5: ROLLBACK — revert SNI to yandex.ru (Yandex Cloud not whitelist or other failure)" -t task -p 0
bd dep add <T5-id> heyearthproject-ei6j --type parent-child
bd update <T5-id> --claim --status=in_progress
bd update <T5-id> --notes "Triggered by: <which step failed and why>"
```

- [ ] **Step 2: SQL UPDATE назад на yandex.ru**

```bash
cd C:/Users/alex/Documents/heyearthproject/server/backend
go run ./cmd/deploy --ssh "sudo -u postgres psql -d heyearth -c \"UPDATE app_settings SET value='yandex.ru', updated_at=NOW() WHERE key='reality_sni_domain';\""
```

Expected: `UPDATE 1`.

- [ ] **Step 3: Restart heyearth-api для re-trigger sync**

```bash
cd C:/Users/alex/Documents/heyearthproject/server/backend
go run ./cmd/deploy --ssh 'systemctl restart heyearth-api && sleep 3 && systemctl is-active heyearth-api'
```

Expected: `active`.

- [ ] **Step 4: Verify rollback успешен**

```bash
cd C:/Users/alex/Documents/heyearthproject/server/backend
go run ./cmd/deploy --ssh "jq -r '.inbounds[] | select(.tag==\"vless-reality\") | .streamSettings.realitySettings.dest' /opt/heyearth/xray/config.json"
```

Expected: `yandex.ru:443`.

- [ ] **Step 5: Решение про fp-фикс (T1)**

Fp-фикс (chrome+firefox) **независим** от SNI-фикса и может остаться даже при rollback C. Решение:

- Если друг сказал что после refresh ситуация СТАЛА лучше но не идеально → **оставить fp-фикс**, откатить только SNI. Beads notes: "C reverted, A kept — partial improvement".
- Если друг сказал что СТАЛО ХУЖЕ → откатить и fp-фикс:
```bash
cd C:/Users/alex/Documents/heyearthproject
git revert <T1-commit-hash>  # revert chrome+firefox commit
cd server/backend
go run ./cmd/deploy --ssh 'systemctl stop heyearth-api && rm -f /opt/heyearth/mainserver/heyearth-api'
go run quick_deploy.go
```

- [ ] **Step 6: Close T5 + reopen epic с новыми гипотезами**

```bash
cd C:/Users/alex/Documents/heyearthproject
bd close <T5-id>
bd update heyearthproject-ei6j --status=open --notes "v2 rolled back; storage.yandexcloud.net hypothesis incorrect. Next: try (a) console.yandex.ru, (b) port 443 for XHTTP, (c) Hysteria2 primary for Happ (B)."
```

---

## Test strategy

Тестов в проекте нет → evidence-driven verification на каждом шаге:

- [ ] `go build ./cmd/api/...` проходит (T1 Step 4)
- [ ] Grep исходников: `[ios|safari]` не встречаются в emitter-коде (T1 Step 5)
- [ ] DB content: `app_settings.reality_sni_domain` = `storage.yandexcloud.net` (T2 Step 6)
- [ ] systemd: `heyearth-api` + `xray.service` оба `active` (T2 Step 5, T3 Step 3)
- [ ] xray config.json: `dest=storage.yandexcloud.net:443`, `serverNames` содержит её (T3 Step 2)
- [ ] xray.service логи: нет error/fail/panic в 2-минутном окне после reload (T3 Step 3)
- [ ] Subscription URI: 4 URI с правильными sni/fp (T3 Step 4)
- [ ] Hub→storage.yandexcloud.net TLS reachable (T3 Step 5)
- [ ] xray accepted entries для test user UUID `0feec1d4-...` за окно теста — N+ записей, реальные destinations (T4 Step 4)
- [ ] xray rejected entries для test user UUID — ноль (T4 Step 5)
- [ ] Пользователь report: ping стабильно OK + сайты открываются (T4 Step 6)

---

## Rollback plan

Полный rollback в проде = 3 команды, ~30 секунд:

```bash
cd C:/Users/alex/Documents/heyearthproject/server/backend
go run ./cmd/deploy --ssh "sudo -u postgres psql -d heyearth -c \"UPDATE app_settings SET value='yandex.ru', updated_at=NOW() WHERE key='reality_sni_domain';\""
go run ./cmd/deploy --ssh 'systemctl restart heyearth-api && sleep 3 && systemctl is-active heyearth-api'
# Verify
go run ./cmd/deploy --ssh "jq -r '.inbounds[0].streamSettings.realitySettings.dest' /opt/heyearth/xray/config.json"
# Expected: yandex.ru:443
```

Для отката fp-фикса дополнительно:

```bash
cd C:/Users/alex/Documents/heyearthproject
git revert <T1-commit>
cd server/backend
go run ./cmd/deploy --ssh 'systemctl stop heyearth-api && rm -f /opt/heyearth/mainserver/heyearth-api'
go run quick_deploy.go
```

Все изменения изолированы, нет миграций схемы, нет новых файлов на диске Hub — rollback гарантированно вернёт систему в pre-fix state.

---

## Deployment notes

**Очередность операций критична:**
1. T1 binary deploy ДО SQL UPDATE — иначе clients с UA=Happ refresh-нут подписку и получат старый fp но новую SNI (mismatch), что усложнит дебаг.
2. После SQL UPDATE на новый SNI обязателен `systemctl restart heyearth-api` — иначе initial sync не сработает, и xray config останется со старым dest.
3. T3 verification ОБЯЗАТЕЛЬНА перед T4 (e2e test с пользователем) — нет смысла беспокоить друга если xray не успел reload.

**Никаких новых env-переменных не добавляется.**
**Никаких миграций БД не нужно** (только UPDATE существующей строки в app_settings).
**nginx config не трогается.**
**hysteria2 / exit-node не трогаются** (Hub→Exit транспорт остаётся как есть — Hysteria2 :443).

**Тайминг:**
- T1 (code + deploy): ~10 мин
- T2 (SQL + restart): ~3 мин
- T3 (verification): ~5 мин
- T4 (e2e with friend): ~10 мин (зависит от availability друга)
- T5 (conditional rollback): ~5 мин если потребуется

**Точка no-return:** до T2 Step 4 (SQL UPDATE) всё обратимо локально без production воздействия. Начиная с T2 Step 4 — production изменено, нужен либо успех, либо T5 rollback.
