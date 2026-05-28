---
id: tele2-mobile-dpi-v2
beads_id: heyearthproject-ei6j
title: Tele2 mobile DPI mitigation v2 (chrome fp + Yandex Cloud SNI)
status: done
created: 2026-05-27
author: alex
---

# PRD: Tele2 mobile DPI mitigation v2 (chrome fp + Yandex Cloud SNI)

## Проблема

Пользователь HiEarth на **Tele2 mobile** в России использует Happ для подключения через VPN. С его десктопа те же VLESS+Reality профили (Берёза 1+2, Дуб 1+2) работают идеально — сервер видит десятки accepted Reality handshakes за минуту. С телефона на Tele2:

- Подписка обновляется (HTTPS на `hiearth.ru:443` проходит)
- При попытке открыть сайт через VPN — сайты не открываются
- Ping-test в Happ показывает «ошибка TLS рукопожатия», причём **плавающе**: иногда OK, через секунду снова ошибка

Research (`docs/research/2026-05-27-happ-tele2-tls-handshake.md`) выявил три высоко-confidence root causes:

1. **SNI↔IP correlation в Tele2 whitelist DPI** — текущий `sni=yandex.ru` указывает на наш Moscow Hub IP (vdsina ASN, не Yandex ASN). Tele2 mobile DPI работает в whitelist-режиме: пропускает только SNI, чей реальный IP совпадает с whitelisted ASN. Mismatch = drop через несколько секунд.
2. **uTLS fp=ios и fp=safari имеют документированные регрессии** в Xray-core (issues #4852, #5918). Особенно XHTTP+ios даёт «firstLen=0, EOF» — TLS handshake завершается успешно, но HTTP-данные не идут.
3. **CGNAT port rotation на Tele2** каждые 5-10 мин — DPI re-классифицирует поток после каждой ротации, и при «подозрительной» SNI/fp комбинации режет.

## Цель

Сменить два client-side параметра в выдаваемых VLESS URI и xray-конфиге Hub так, чтобы Tele2 mobile DPI перестал детектить нашу Reality-сессию как подозрительную:

- **A**: TLS fingerprint в URI = `chrome` + `firefox` (вместо `ios` + `safari`)
- **C**: Reality SNI = `storage.yandexcloud.net` (вместо `yandex.ru`)

Изменения распространяются на ВСЕХ пользователей (не только Tele2), потому что:
- Chrome fingerprint лучше тестируется в uTLS и работает на любых ОС/клиентах
- Yandex Cloud SNI попадает в whitelist всех российских mobile-операторов (не только Tele2)

## User stories

- Как **пользователь Happ на Tele2 mobile**, я хочу чтобы Reality-профили Берёза и Дуб работали стабильно, чтобы я мог открывать сайты через VPN без плавающих ошибок TLS рукопожатия.
- Как **пользователь Happ на МТС/Билайн/Мегафон mobile**, я хочу чтобы DPI этих операторов тоже не резало моё подключение (одна архитектура whitelist-DPI у всех российских mobile-carrier'ов).
- Как **админ HiEarth**, я хочу чтобы переключение SNI имело простой rollback (одна SQL-команда), на случай если новая SNI окажется не whitelisted.

## Acceptance criteria

- [x] `cmd/api/client_api.go` строки 230 и 250 эмитят `[]string{"chrome", "firefox"}` вместо `[]string{"ios", "safari"}` — commit `1a6a242`
- [x] Комментарии выше обоих slice'ов обновлены: убрана устаревшая мотивация про «TSPU таргетит Chrome JA3»; добавлена ссылка на research-документ — commit `1a6a242` (+ sister fix `769625a` для Clash.Meta emitter)
- [x] `app_settings.reality_sni_domain` в production DB = `storage.yandexcloud.net` — UPDATE 1 (2026-05-28)
- [x] После restart файл `/opt/heyearth/xray/config.json` содержит для обоих VLESS inbounds `"dest": "storage.yandexcloud.net:443"` и `"serverNames"` содержит `"storage.yandexcloud.net"` — verified via jq
- [x] `xray.service` после reload активен, без crash-loop — verified, нет error/fail/panic
- [x] `curl /api/v1/user/subscribe/<test-token>` с UA=Happ возвращает 4 VLESS URI: Берёза 1=`fp=chrome`, Берёза 2=`fp=firefox`, Дуб 1=`fp=chrome`, Дуб 2=`fp=firefox`, все `sni=storage.yandexcloud.net` — verified
- [x] Пользователь на Tele2 mobile refresh-ит подписку → подключается стабильно. **Нюанс:** работают только firefox-профили (Берёза 2, Дуб 2); chrome-профили (Берёза 1, Дуб 1) режутся Tele2 Chrome-JA3 DPI. Пользователь использует Берёза 2/Дуб 2 — стабильная связь подтверждена.
- [x] В xray access log видны accepted Reality handshakes от UUID `0feec1d4-…` с реальными destinations — **272 коннекта за 10 мин** (telegram/wps/office/google) vs ~1/час до фикса

## Out of scope

- ❌ **B**: Hysteria2-профиль как primary для UA=Happ. Обсуждается отдельно в следующей итерации.
- ❌ Изменение `auto_tuner.go` — auto_tuner_enabled = false с 26 мая, кода не выполняется. Если когда-то включат — обновят candidates тогда.
- ❌ Добавление AmneziaWG (Пальма) для mobile. Отдельная фича (`heyearthproject-ei6j` epic не покрывает).
- ❌ Перенос XHTTP-профиля Дуб с порта 2443 на 443 через nginx stream. Может быть отдельной задачей если A+C недостаточно.
- ❌ Multi-SNI Reality inbound, dual Reality inbounds, feature flag для SNI rollback. Hard switch предпочтён (см. brainstorming).
- ❌ Изменение Hub→Exit транспорта (Hysteria2). Touch-not.
- ❌ Изменение `reality_fingerprint` в app_settings (auto_tuner-managed, но client_api.go его всё равно игнорирует — это технический долг для другой итерации).

## Риски и open questions

- 🔴 **Главный риск:** гипотеза research'а о том, что Tele2 whitelist включает Yandex Cloud, может быть неверна. storage.yandexcloud.net указывает на IP Yandex Cloud ASN (AS200350), но наш Hub IP — vdsina (AS44676). Если Tele2 проверяет SNI ↔ destinationIP а не SNI ↔ destinationASN — будет тот же mismatch, и фикс не сработает. **Mitigation:** немедленное E2E тестирование с конкретным Tele2 пользователем сразу после деплоя. Rollback простой — одна SQL-команда.
- 🔴 **Хвостовой риск:** старые URI у клиентов имеют `sni=yandex.ru` + (для уже подключившихся) cached pbk. После switch xray начнёт отвергать sni=yandex.ru → старые сессии fail-нут до refresh подписки. Happ автоматически делает refresh при открытии. **Mitigation:** ничего не делаем; те кто открывает Happ — переживут за минуту.
- 🟡 **Open question:** нужно ли в `serverNames` оставлять `www.storage.yandexcloud.net` (xray_sync.go автоматически добавляет `www.` префикс)? Безвредно но «грязно» — `www.` версия не резолвится в DNS. Не блокирующий вопрос.
- 🟡 Если фикс A работает а C нет — оставляем A, откатываем C. Если C работает а A нет — оставляем C, откатываем A. Не атомарный rollback.
- 🟢 Для chrome fingerprint в uTLS есть лёгкая корреляция с трафиком 80% всех VPN-клиентов (это самый популярный fp). Если кто-то делает blanket-Chrome-JA3 detection — пострадает. Но research показал — современные DPI это не делают (нечем отличить Chrome-VPN от Chrome-browser).

## Зависимости

- Блокируется на: ничего (research завершён, дизайн утверждён)
- Блокирует: возможная следующая итерация B (Hysteria2 primary для Happ) — нужны данные что A+C недостаточно
- Связанные документы:
  - Research: `docs/research/2026-05-27-happ-tele2-tls-handshake.md`
  - Предыдущие итерации: `docs/research/2026-05-26-mobile-dpi-tele2.md`, `docs/research/2026-05-26-reality-mobile-dpi-deep-dive.md`

## Метрики успеха

В течение 24 часов после деплоя:
- ✅ Test user (Tele2 mobile) — VPN работает без жалоб, в xray-логе видны accepted handshakes от его UUID каждые 1-5 минут активного использования (вместо «1 раз в час» как сейчас)
- ✅ Другие пользователи не сообщают о регрессе (Telegram-канал поддержки молчит)
- ✅ Server-side health: `journalctl -u xray.service --since '24h ago' | grep -iE 'rejected|failed|reality'` показывает не больше rejected-записей чем за прошлые 24h baseline

Через неделю:
- ✅ Если получим feedback от других Tele2/МТС mobile пользователей — VPN работает стабильно

## Ссылки

- Beads epic: `heyearthproject-ei6j`
- Research: `docs/research/2026-05-27-happ-tele2-tls-handshake.md`
- Связанный PR: (создадим в /execute фазе)
