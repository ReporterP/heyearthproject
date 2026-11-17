---
id: direct-routing
beads_id: heyearthproject-agh4
title: Direct routing infrastructure (bypass VPN для российских сервисов)
status: approved
created: 2026-05-23
author: alex
---

# PRD: Direct routing infrastructure

## Проблема

Сейчас весь user-трафик HiEarth уходит через зарубежные exit-ноды. Это ломает доступ к российским сервисам, которые блокируют иностранные IP по гео-фильтрам:

- **Банки** (Alfa, Sberbank, Гoсбанк) отдают `Access Denied` или капчу при заходе с не-RU IP.
- **Госуслуги, ФНС, ФССП** — гео-фильтр включён жёстко.
- **Маркетплейсы** (Avito, Magnit, Леруа Мерлен) — рекомендательные движки и доставка ломаются.
- **Карты** (2gis) — некоторые слои недоступны с иностранного IP.

Пользователь вынужден вручную выключать VPN при заходе на эти сервисы → подрывает основное ценностное предложение продукта ("VPN всегда включён").

## Цель

Дать админу возможность поддерживать список IP/доменов российских сервисов, который автоматически распределяется между пользователями (per-plan) и применяется одновременно в Happ-routing-profile (на устройстве) и в Xray-rules (на Hub). Сидируем актуальной коллекцией из community-источника (`ip-list.json`, 86 сервисов).

## User stories

- Как **админ HiEarth**, я хочу **импортировать готовый JSON со списком сервисов и привязать его к платным планам**, чтобы **массово раскатить bypass без ручного ввода 30K строк**.
- Как **админ**, я хочу **редактировать список сервисов через UI (добавить/удалить/выключить)**, чтобы **оперативно реагировать на новые блокировки или сломанные сервисы**.
- Как **админ**, я хочу **выключать отдельный сервис одним кликом без его удаления**, чтобы **временно откатить bypass и потом включить обратно**.
- Как **админ**, я хочу **видеть какие планы имеют какой сервис**, чтобы **управлять монетизацией (free-trial без bypass / paid с bypass)**.
- Как **пользователь Happ-клиента на платном плане**, я хочу **открывать gosuslugi.ru / сбербанк / 2gis без отключения VPN**, чтобы **VPN всегда был включён без потери удобства**.
- Как **пользователь V2RayNG или прямого SOCKS5 на платном плане**, я хочу **те же сервисы работали через Hub direct**, чтобы **bypass работал независимо от клиента**.

## Acceptance criteria

- [ ] Миграция 000028 создаёт таблицы `direct_route_services` и `plan_direct_services` + permissions `routing.direct.view`/`routing.direct.manage` + setting `direct_routes_version`.
- [ ] `POST /admin/api/direct-routes` создаёт сервис, возвращает 201 + ID; запись появляется в БД; вызывается `RequestXraySync()`.
- [ ] `POST /admin/api/direct-routes/import` с body = содержимое `ip-list.json` создаёт **86 записей** за один вызов; `errors=[]`; created=86; updated=0.
- [ ] После импорта и привязки к плану `all-inclusive`, `GET /api/v1/user/subscribe/<token>` пользователя этого плана содержит `happ://routing/onadd/<b64>` deeplink с populated `DirectIp` (>1000 элементов) и `DirectSites` (>15000 элементов).
- [ ] `/opt/heyearth/xray/config.json` содержит outbound с тегом `direct-freedom` и хотя бы одно routing rule с `outboundTag: "direct-freedom"` и `user: [<UUIDs планов с direct>]`.
- [ ] Bulk-операция "deactivate" для 5 сервисов через UI → 5 строк получили `is_active=FALSE`, RequestXraySync вызван 1 раз (не 5), audit log содержит одну запись `bulk_deactivate`.
- [ ] Удаление сервиса каскадно очищает `plan_direct_services`.
- [ ] Functional smoke: с Happ-клиента на телефоне (план all-inclusive) открыть 2gis.ru → загружается; trace показывает что запрос НЕ ушёл на Hub.
- [ ] Functional smoke: с V2RayNG на телефоне (тот же план) открыть 2gis.ru → загружается; `curl https://api.ipify.org` через тот же VPN отдаёт `198.51.100.236` (Hub IP) для 2gis-доменов и зарубежный exit-IP для всего остального.
- [ ] Админ-UI: страница `/direct-routes` доступна только пользователям с permission `routing.direct.view`.
- [ ] Импорт JSON-формата 2 (HiEarth native) с body `{"version":1,"services":[...]}` работает.
- [ ] При 0 healthy exit-нод direct-rules сохраняются — Happ-клиент продолжает работать с российскими сервисами.

## Out of scope

- **Routing profiles per-user**: возможность пользователю самому выбирать какие группы (russia/vk/yandex) для него direct. Это отдельная фича на потом.
- **Per-service DNS в Xray**: поле `dns_servers` хранится в БД, но в Xray `dns`-секцию не пробрасывается.
- **Auto-update из внешнего источника**: GitHub-репо с актуальным списком, cron импорт. Сейчас только manual через UI.
- **Per-service traffic statistics**: счётчики "сколько байт ушло direct через 2gis".
- **Wildcard SNI matching** (`*.gosuslugi.ru` → matches `cabinet.gosuslugi.ru`): полагаемся на Xray-формат `domain:` который уже даёт subdomain matching.
- **GeoIP routing для bypass**: "всё что разрешает gosuslugi → direct" по геолокации IP. Сейчас явный allowlist по доменам/IP.

## Риски и open questions

- 🟡 **Размер Xray-config**: при подключении всех 86 сервисов к одному плану config распухнет на ~2-3 MB (содержит ~30K доменов + ~11K IPs). Xray справится (trie + radix tree), но мониторим время `xray run -test` после import — если >5s, переделываем в per-service rules или per-group rules.
- 🟡 **Subscription size**: deeplink `happ://routing/onadd/<b64>` пройдёт через base64-кодирование того же массива. Размер подписки может вырасти на ~1.5 MB. Проверяем что Happ парсит подписку этого размера без таймаута.
- 🟢 **Concurrent admin edits**: last-write-wins без optimistic locking. Допустимо для редкой админ-активности; UI будет показывать `updated_at` чтобы админ видел "кто-то отредактировал минуту назад".
- 🟢 **Невалидные домены в bulk-import**: skip-with-warning — валидные применяются, невалидные попадают в отчёт. Альтернатива (atomic rollback на одной ошибке) хуже UX-но.
- 🟡 **Anonymous SOCKS5**: правила с `user:[...]` не сработают для inbound без user-binding. Сейчас anonymous SOCKS5 не используется (per-user auth), но если когда-нибудь добавим — direct не сработает на нём.

## Зависимости

- **Блокируется на:** ничто (изолированная фича, новые таблицы, новый UI page, новые routing rules).
- **Блокирует:** будущая фича "per-user routing profiles" (пользователь сам управляет direct).

## Метрики успеха

Через 7-14 дней после deploy:

- **Метрика 1 (technical):** `direct_route_services` содержит ≥86 строк; ≥80 имеют `is_active=TRUE`.
- **Метрика 2 (technical):** Xray-config содержит outbound `direct-freedom` и ≥1 rule с этим outbound.
- **Метрика 3 (user value):** ручная проверка топ-5 банков (Alfa/Sber/Tinkoff/VTB/Otkritie), госуслуг, и 5 маркетплейсов через Happ-client на платном плане → все открываются без ручного отключения VPN.
- **Метрика 4 (support):** количество тикетов на тему "сайт X не открывается с VPN" падает на ≥50% в течение 14 дней.
- **Метрика 5 (admin UX):** админ может за <5 минут добавить новый сервис в bypass и убедиться что он работает у пользователя.

## Ссылки

- Spec: `docs/superpowers/specs/2026-05-23-direct-routing-infrastructure-design.md`
- Plan: `docs/plan/direct-routing.md` (будет создан writing-plans)
- Beads epic: heyearthproject-agh4
- Источник seed-данных: `C:\Users\alex\Downloads\ip-list.json` (community list, 86 сервисов)
