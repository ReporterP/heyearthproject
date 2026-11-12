# Аудит функциональности Go-бэкенда HiEarth (13.11.2026)

Метод: чтение кода + grep по `server/backend`, `admin_web/src`, `site/src`, `flutter-app/lib` (только как «кто вызывает»). Прод и БД НЕ проверялись: всё, что зависит от данных в БД или значений app_settings на проде, помечено «не проверено». Пути относительно `server/backend/`.

Статусы: ✅ работает, ⚠️ частично / с багом, ❌ сломано, 💀 мёртвое (нет эффекта или нет вызовов).

Сверка с прошлым аудитом (2026-11-12): часть пунктов уже устарела.
- gRPC ClientApi теперь требует JWT и сверяет `user_id` (`cmd/api/grpc_auth.go:42-80`), reflection включается только `GRPC_REFLECTION=1` (`cmd/api/main.go:1015`). Пункт «gRPC доверяет user_id» закрыт.
- `recover` есть у всех фоновых циклов (`safely`, `cmd/api/monitor.go:109`), `runServerAction` в админке теперь используется (только `restart_agent`).
- Не закрыто: ложные тревоги exit, алерты без бота, дубль формул B2B.

---

## 1. HTTP-маршруты

### 1.1 Публичные / пользовательские (`cmd/api/main.go`)

| Маршрут | Кто вызывает | Статус | Замечания |
|---|---|---|---|
| `/api/v1/health` | nginx/deploy `--status`, Flutter | ✅ | main.go:1030 через `httpGateway` |
| `/api/v1/app/update` (GET) | никто (Flutter ходит по gRPC `CheckAppUpdate`) | 💀 кандидат | main.go:1031, дублирует gRPC; в site/admin нет вызова |
| `/api/v1/auth/register`, `/login` | site, Flutter | ✅ | лимитер 10/мин |
| `/api/v1/user/profile`, `/subscription`, `/payments`, `/payments/{id}`, `/connections`, `/downloads`, `/traffic`, `/subscribe-token`, `/happ-link/`, `/mtproto`, `/awg-config`, `/cards`, `/cards/{id}`, `/auto-renew`, `/pause-subscription`, `/resume-subscription` | site (`site/src/api/user.js`) | ✅ | все найдены в `site/src` |
| `/api/v1/user/subscribe/{token}` | клиенты VPN (Happ и др.), `?format=clash` | ✅ | `subscription_handler.go`; Clash-ветка жива (`clashProfile`), `buildClashConfig` (строка 250) вызывается только тестами |
| `/internal/hysteria2/auth` на :8000 | `deploy --status` (проба) | ⚠️ deprecated | main.go:1052-1059 сам помечен «убрать»; рабочий путь — :8001 |
| `/api/v1/payment/create` | site | ✅ | см. баги промокодов в разд. 3 |
| `/api/v1/payment/check-access` | только Flutter (`payment_api.dart`) | ⚠️ | приложение заморожено |
| `/api/v1/payment/redeem-code` | site | ✅ | это claim-код тенанта, не промокод (`handleRedeemCode`) |
| `/api/v1/payment/tenant-topup` | никто (site использует `/business/topup-init`) | 💀 | main.go:1065, дубль `handleBusinessTopupInit`; работоспособен, но вызовов нет |
| `/api/v1/payment/apply-promocode` | site | ⚠️ | баги, см. разд. 3 |
| `/api/v1/payment/donate`, `/refund`, `/proration-preview` | site | ✅ | |
| `/api/v1/business/create-invoice`, `/me`, `/topup-init`, `/invite`, `/members/` | site | ✅ | |
| `/api/webhooks/yookassa` и `/api/v1/webhooks/yookassa` | ЮKassa | ✅ дубль | оба пути живы; админка пишет `/api/v1/...` в подсказке, CLAUDE.md — `/api/webhooks/...`. Оставить один и обновить ЛК ЮKassa/подсказку |
| `/api/v1/plans`, `/legal-info`, `/app/donation-settings` | site | ✅ | |
| `/robots.txt` | боты | ✅ | |
| `/api/public/create-tenant` | site | ✅ | `internal/admin/routes.go:473` |
| `/api/public/tariff/calculate` | никто (site считает формулу у себя, `BusinessSignupPage.jsx:21`) | 💀 | routes.go:474; формула в трёх местах, см. разд. 3 |

### 1.2 Админские (`internal/admin/routes.go`, `cmd/api/main.go`, `entries_admin.go`, `alerts_admin.go`)

Вызывающий — `admin_web/src` (api/admin.js и сырые fetch). Перечислены только маршруты с проблемами; все остальные (users, subscriptions, promocodes, payments, plans, releases, admin-users/roles/permissions, ssh-keys, settings, traffic*, tenants, b2b-analytics, direct-routes/*, audit-log, email-log, payment-retries, nodes, provision-jobs, entries/*, alerts/*, auto-tuner/*, hysteria2/status, capacity, overview, mtproto, traffic-masking) имеют вызов в админке: ✅.

| Маршрут | Вызов | Статус | Замечания |
|---|---|---|---|
| `GET /api/admin/servers/{id}/metrics` | нет (UI берёт метрики из `load_metrics` деталки) | 💀 | routes.go:181, `GetServerMetricsHandler` (servers_handlers.go:648) |
| `GET /api/admin/servers/{id}/files`, `.../file-content` | в `admin.js:136` есть `fetchServerFiles`, но его никто не импортирует | 💀 | routes.go:187-199; файловый браузер по SSH без UI. `PUT file-content` отвечает 410 |
| `GET /api/admin/servers/generate-tunnel-creds` | нет | 💀 (410) | routes.go:116 `goneHandler` |
| `POST /servers/{id}/actions`: `fetch_tunnel_creds`, `sync_tunnel`, `sync_metrics`, `setup_exit_node`, `reprovision`, `reinstall_mtproxy` | UI шлёт только `restart_agent` (`ServerDetailPage.jsx:87`) | 💀 (410) | `removedServerActions`, servers_handlers.go:51-58, ветка 479-482 и `movedServerActionMsg` (507) |
| `POST /api/admin/force-autorenew` | нет | 💀 (отладка) | main.go:1099; можно оставить как ручной рычаг, но без UI и без аудит-лога не проверено |
| `POST /api/admin/sync-pending-payments` | `PaymentsPage.jsx` (сырой fetch) | ✅ | |
| `POST /api/admin/b2b/create-invoice` | форма «счёт» в админке | ✅ | |
| `PATCH /admin-users/{id}`, `/admin-roles/{id}` | UI | ⚠️ | нет DELETE ни у админов, ни у ролей (routes.go:218-229) |
| `POST /api/admin/payments` (создать платёж вручную) | `admin.js:79` | ✅ не проверено глубже | |
| `/api/admin/mtproto*` | MTProtoPage | ⚠️ | пишет `mtproto_host` (мёртвый ключ), см. разд. 4 |

Прочее по маршрутам:
- Через общий `POST /api/admin/settings` можно записать ЛЮБОЙ ключ app_settings (белого списка нет, `settings_handlers.go:96`). Это и источник «мёртвых ручек» из разд. 4.
- `GET /api/admin/entries` использует публичный `setCORS`, а не админский CORS (`entries_admin.go:67`). Не проверено, влияет ли на практике.

---

## 2. Фоновые задачи при старте

Порядок старта: `cmd/api/main.go:942-995`. Хвост цепочки (`StartTunnelHealthMonitor`, `StartCertExpiryMonitor`, `StartAutoTuner`) стартует ИЗ `StartXraySyncLoop` (`xray_sync.go:1558-1564`), которая делает ранний `return`, если `/opt/heyearth/xray/config.json` нет (строка 1536). Побочный эффект: на любом хосте без xray (стенд, отдельный API) не работают монитор туннелей, MTProto-health, автотюнер и проверка сертификатов.

| Задача | Файл | Период | Что делает | Используется? | Гейт / дефолт | Риск вреда |
|---|---|---|---|---|---|---|
| xray sync | xray_sync.go:1533 | старт + 5 мин + debounce 2 с | пишет конфиг xray, live-apply | ✅ ядро | нет файла конфига → выключен | высокий (ломает всех при ошибке); есть `xray run -test` |
| tunnel health monitor | xray_sync.go:1275 | 30 с | проба exit-ов, hysteresis 3/2, алерты, `servers.tunnel_status`, MTProto-проба | ✅ | через XraySyncLoop | пишет `reputation_score` без читателей (💀) |
| cert expiry | xray_sync.go:1571 | 24 ч | читает `ca-cert.pem`, `client-cert.pem`, legacy | ⚠️ | через XraySyncLoop | только `log.Printf`, без `RaiseAlert`; сертификаты нод (leaf, 1 год) не проверяются; сертификат сайта проверяет другая функция (monitor.go `checkSiteCertExpiry`) |
| auto-tuner | auto_tuner.go:176 | 4 ч (настр.) | SNI, DNS, mux, fingerprint, обфс Hysteria2 | ⚠️ | `auto_tuner_enabled` дефолт `false` (миграция 000056); старт только если было `true` в момент запуска API | высокий, см. 3.1 |
| hysteria2 sync | hysteria2_sync.go:553 | 5 мин | `/etc/hysteria/server.yaml`, выбор exit для «Бамбука» | ✅ | no-op без hysteria | |
| hysteria2 stats | hysteria2_stats.go:49 | 60 с | дельты трафика в `traffic_stats` | ✅ | | |
| AWG sync / stats | awg_sync.go:301, awg_stats.go:48 | 5 мин / 60 с | `awg0.conf`, трафик пиров | ✅ | нет `/opt/heyearth/awg` → выкл | пишет `awg_last_sync_at/_error/_peer_count` без читателей |
| traffic collector | traffic_stats.go:48 | 60 с | xray Stats API → `traffic_stats`, `user_subscriptions.traffic_used`, `dial_rate_log` | ✅ | | `traffic_stats` без очистки (см. разд. 5) |
| server metrics collector | internal/admin/server_metrics_collector.go:27 | 10 с | gRPC mTLS к каждой ноде, `servers.load_metrics`, `status` | ✅ | | новое mTLS-соединение + чтение сертификатов с диска на КАЖДЫЙ опрос каждой ноды; пишет `servers.updated_at` каждые 10 с |
| subscription cron | subscription_cron.go:20 | 1 ч | автопродление за 24 ч до конца, перезапуск sync по истёкшим | ✅ | | баг «бесконечный trial», см. 3.5 |
| B2B billing | subscription_cron.go:37,111 | 1 ч, срабатывает «1-е число, час 0» | списание с кошельков тенантов | ⚠️ | `last_billing_month` | нет догонки: если API лежал/рестартовал в час 0 первого числа, месяц не спишется; `time.Sleep(1h)` дрейфует. Предупреждение о балансе так же привязано к `Hour()==9` |
| payment sync | payment_sync.go:45 | 5 мин | довыбор pending, refund-reconcile, алерты «не применён» | ✅ | | pending-записи без `yookassa_payment_id` (упал вызов ЮKassa) остаются pending навсегда |
| email reminder | email_cron.go:19 | 15 мин | письмо за 2-4 дня до автосписания | ⚠️ | `smtp_enabled` дефолт `false`; письмо только если есть email | без SMTP это холостой цикл; email у анонимных пользователей обычно пуст |
| dunning | dunning_cron.go:24 | 30 мин | ретраи неудачных автосписаний | ⚠️ | | баги, см. 3.6 |
| ghost reconciler | ghost_reconciler.go:32 | 30 мин | сверка юнитов hysteria2-client-N с БД, автоустановка MTProxy | ✅ | `ghost_auto_cleanup` дефолт `true`, `ghost_auto_repair` `false`, `mtproto_auto_install` `true` (миграции 000040/000042) | ВРЕД: автоочистка удаляет юнит/останавливает процесс на Hub; `mtproto_auto_install=true` ставит MTProxy на каждый online-exit даже при `mtproto_enabled=false` (порт 8443 открывается без нужды). Ключей нет в админке (только через общий POST /settings) |
| monitor (5 циклов) | monitor.go:63 | 30 с…2 мин | БД, loop-проба, check-host, Бамбук-проба, входы, сертификат сайта, бэкапы, диск | ✅ | `runtime.GOOS=="linux"`; без Telegram алерты не доходят (по прошлому аудиту бот не настроен; не проверено) | |
| entry monitor | entry_monitor.go, из monitor.go:90 | 100 с | пробы входов, failover | ✅ | `entry_failover_enabled` читается как `!= "false"`, т.е. по умолчанию ВКЛ | автопереключение основного адреса |
| quota cutoff | quota_cutoff.go, `watchQuotaCutoffFlag` из traffic_stats.go:70 | 60 с | отсечка по квоте для xray | 💀 по умолчанию | `quota_cutoff_enabled` — ни UI, ни миграции, ни deploy его не ставят (grep по всему репо), дефолт «выключено» | Hysteria2 режет по квоте ВСЕГДА (`hysteria2_auth.go:155`), VLESS/XHTTP — никогда, пока кто-то не вставит ключ вручную: асимметрия. На проде значение не проверено |
| auth listener :8001 | hysteria2_auth_listener.go:111 | — | колбэк авторизации hysteria | ✅ | `log.Fatalf` при неудаче bind | |
| alerts retention | alerts.Init | — | очистка `alert_events` | ✅ | | |

Цикл subscription cron: при `auto_renew=true` и отсутствии сохранённой карты пишет строку в лог КАЖДЫЙ час на подписку, пока она в окне 24 ч (шум, не вред).

---

## 3. Подсистемы

### 3.1 Автотюнер — ⚠️ (выключен по умолчанию, не «чинить включением»)

| Что | Статус | Доказательство |
|---|---|---|
| Выключен миграцией, старт только если был включён | ⚠️ | миграция 000056 ставит `false`; `StartAutoTuner` возвращается сразу (auto_tuner.go:176-181). Админка предупреждает `loop_running` (auto_tuner_handlers.go:12-24), «Запустить сейчас» без запущенного цикла — отказ |
| Смена SNI хаба | ❌ вредна при включении | `tuneSNI` пишет `reality_sni_domain` (auto_tuner.go:350) и ставит только `serverChanged`; `config_version` НЕ растёт. Сервер Reality принимает только `[SNI, www.SNI]` (xray_sync.go:526, 585), у клиентов в подписке старый `sni=`: до обновления подписки старые конфиги ломаются |
| Оптимизация fingerprint | 💀 | `reality_fingerprint` не читается генераторами подписки (отпечатки `chrome`,`firefox` зашиты, client_api.go:381-394, clash `p.fp`); тюнер лишь пишет ключ и вызывает `incrementConfigVersion()` (auto_tuner.go:279-281): баннер «обновите подписку» без реальных изменений |
| Оптимизация DNS | ❌ | `net.DialTimeout("udp", …:53)` (auto_tuner.go:373) всегда «успешен» без отправки пакета; «лучший» DNS определяется шумом задержки; запись `dns_servers` перезапускает синк xray |
| Mux | ⚠️ | эвристика по числу нод, без измерений (auto_tuner.go:402-430) |
| `xPaddingBytes` | ✅ но не тюнится | зашито `"100-1000"` (xray_sync.go:568) |
| Ротация obfs Hysteria2 раз в 42 итерации | ⚠️ | живёт внутри цикла тюнера (auto_tuner.go:296-313): при выключенном тюнере обфс не ротируется; при ротации клиенты получают новый obfs только после перечитывания подписки |
| `config_test_results` | ⚠️ | пишется (`saveTestResult`), читается админкой (history); без очистки, при включённом тюнере растёт |
| `auto_tune` на `servers` | ⚠️ | `isAutoTuneEnabled` по умолчанию `true` при ошибке (auto_tuner.go:658-665) |

### 3.2 Маскировка трафика (traffic_masking_rules) — ⚠️ работает не так, как называется

- Правило создаёт outbound `freedom` и маршрут `domain:<source_domain>` для ВСЕХ пользователей и inbound-ов (включая AWG) (xray_sync.go:706-725, 836-842; код сам это признаёт в комментарии).
- `mask_domain` не используется нигде; `tenant_id` правила игнорируется; «доступно только premium» (`TrafficMaskingAvailable`, client_identity.go:322) никак не влияет на маршрутизацию, а любой активный платящий — premium (`is_premium TRUE`, user.go:152).
- По сути — второй, недокументированный механизм «прямого» трафика поверх `direct_routes`. Решить: удалить или переименовать и слить с direct-routes.

### 3.3 Ghost reconciler — ✅ работает, но агрессивные дефолты (см. таблицу задач)

### 3.4 MTProxy (Telegram) — ⚠️

| Что | Статус | Доказательство |
|---|---|---|
| Выдача ссылки `tg://proxy` пользователю | ✅ | main.go:733-770; требует `mtproto_enabled=true` (дефолт `false`), непустой `mtproto_secret` и healthy-exit |
| Автоустановка | ⚠️ | дефолт `mtproto_auto_install=true`, не зависит от `mtproto_enabled` (ghost_reconciler.go:107) |
| `mtproto_host` | 💀 | админка пишет/читает (mtproto_handlers.go:29,49), генератор ссылок берёт IP из `PickMTProtoExit`; миграция 000042 ключ удалила |
| `PUT /api/admin/mtproto` | ⚠️ | перезаписывает ВСЕ 7 ключей из тела (mtproto_handlers.go:58-66): клиент, не приславший поле, обнулит значение (в т.ч. `secret`). Не проверено, шлёт ли UI все поля |
| Health | ✅ | `checkMTProtoHealth`, 3/2 hysteresis, `disabled` защищён (biz/mtproto.go) |

### 3.5 Подписки, автопродление, пробный план — ❌ ВАЖНО

- Любая новая подписка создаётся с `auto_renew = true`, колонка `plans.auto_renew_default` игнорируется (biz/plans.go:534, 549, 582). `AutoRenewDefault` только читается/пишется админкой (💀).
- `processAutoRenew` для плана с `price_rub <= 0` продлевает бесплатно «автоматически» (subscription_cron.go:177-180) и НЕ проверяет `max_activations_per_user` (проверяется только в `payment/create`, payment_handlers.go:104).
- Итог: пользователь, активировавший пробный план (0 ₽/7 дн/10 ГБ) через `payment/create`, получает продление каждые 7 дней бесплатно, пока не снимет `auto_renew`. Ветка с промокодом (`biz.ApplyPromocode`) вставляет подписку без `auto_renew` (дефолт FALSE) и такой проблемы не имеет. Влияние на проде не проверено; проверка: `SELECT count(*) FROM user_subscriptions us JOIN plans p ON p.id=us.plan_id WHERE p.price_rub=0 AND us.auto_renew AND us.valid_until>now()`.
- Лимит квоты: 10 ГБ считается циклами (см. 2), т.е. «бесплатно навсегда» по 10 ГБ/мес.

### 3.6 Dunning (повторные списания) — ⚠️

- `CancelActiveRetries` не вызывается нигде (biz/dunning.go:168): ручная оплата не отменяет очередь ретраев. `attemptRetry` (dunning_cron.go:51-120) не проверяет, что подписка уже продлена: возможно двойное списание с сохранённой карты.
- `ClaimRetry` ставит `in_progress` (dunning.go:97); сброса зависших `in_progress` нет: при падении между claim и итогом запись «залипает» навсегда и висит в `GetActiveRetryForUser`.
- Платёж `pending` после `ChargeRecurring` считается «неуспех попытки» (dunning_cron.go:109-118), хотя вебхук может позже провести его: риск лишней попытки.
- Письма: `smtp_enabled=false` по умолчанию, письма уходят только при известном email.

### 3.7 Email (SMTP, шаблоны) — ⚠️

Рабочий, но выключен: `smtp_enabled` дефолт `false` (миграция 000033). Настройки есть в админке (SettingsPage), лог писем `email_log` читается админкой. Не проверено, включён ли на проде.

### 3.8 Промокоды (4 типа) — ❌ два пути с разной логикой

Типы: `discount_percent`, `discount_fixed`, `free_trial`, `traffic_gift`. Два независимых пути применения.

| Путь | Что делает | Проблемы |
|---|---|---|
| `payment/create` + `biz.ApplyPromoToPrice` (plans.go:592) | скидка/бесплатно, проверяет `is_active`, лимит, срок, `applies_to_plans`, увеличивает счётчик ДО оплаты | счётчик `current_uses` сгорает при брошенной оплате; `free_trial`: возвращаемые `freeTrialDays` отбрасываются (`_`, payment_handlers.go:120) — цена 0 активирует ПОЛНУЮ длительность плана (годовой = 365 дней), а не `free_days`; `traffic_gift`: `trafficGiftBytes` тоже отбрасывается, поле `traffic_gift_bytes` нигде не применяется 💀 |
| `payment/apply-promocode` и gRPC `ApplyPromocode` → `biz.ApplyPromocode` (user.go:326) | выдаёт N дней на план `plan_id_granted`/free-trial | не проверяет `is_active` и не смотрит на `promo_type`: код типа `discount_percent`/`traffic_gift` даёт бесплатные 30 дней; код не нормализуется в верхний регистр (в отличие от пути выше, где `ToUpper`); gRPC-вариант (main.go:130) не вызывает `CheckPromoPerUserLimit`; `per_user_limit` считается по `user_subscriptions.promocode_used`, а при продлении существующей подписки поле перезаписывается, лимит обходится |

Реферальной системы в коде нет (grep `referral|referrer` по .go/.sql пуст): это не «мёртвое», а отсутствует.

### 3.9 B2B / тенанты — ⚠️ деньги списываются, прав нет

| Что | Статус | Доказательство |
|---|---|---|
| Месячное списание кошелька | ✅ (с оговоркой догонки, см. 2) | billing.go:35-70 |
| Пополнение (`business/topup-init`, вебхук, `CreditTenantWalletTx`) | ✅ | payments.go:154 |
| Авто-возобновление после пополнения | ❌ | письмо обещает «аккаунт автоматически возобновится» (billing.go:109), но `CreditTenantWalletTx` меняет статус только `inactive→active`, `suspended` остаётся (payments.go:177-181) |
| Приостановка тенанта | 💀 | `status='suspended'` нигде не проверяется: ни доступ (`GetUserAccessState` смотрит только `tenant_id>0`, client_identity.go:304-317), ни xray-клиенты (`activeVlessClientsQuery` смотрит `user_subscriptions`, user.go:156-170) |
| `max_users`, `max_servers`, `use_provider_servers`, `servers.tenant_id` | 💀 для доступа | участвуют только в формуле биллинга и в админке; лимит участников при `RedeemTenantClaimCode`/`AddTenantMember` не проверяется |
| Членство даёт VPN? | ⚠️ | gRPC `CanConnect=true` для члена тенанта, но реальный доступ (xray/hysteria/awg) выдаётся только по `user_subscriptions`: нужно ли тенанту раздавать подписки участникам — не видно в коде, уточнить у владельца |
| Приглашения (`business/invite`) | ✅ | роль зашита в код `tn-r-<role>-…`; `RedeemTenantClaimCode` делает `ON CONFLICT … SET role=EXCLUDED.role` (payments.go:489): админ тенанта, использовавший код роли `user`, понижает сам себя; перезаписывает `users.tenant_id` |
| Цены B2B | ❌ три копии формулы | `biz.CalculateTenantMonthlyCharge` (billing.go:21-34), `PublicTariffCalculateHandler` (tenants_handlers.go:204-216), JS `BusinessSignupPage.jsx:21`. Настройки `b2b_price_*` и `b2b_base_own_servers` (6 карточек в `SettingsPage.jsx:242-247`) не читаются никем |

### 3.10 Прочие подсистемы

| Подсистема | Статус | Доказательство |
|---|---|---|
| Per-plan балансировщики, `load_weight`, `plan_servers` | ✅ | xray_sync.go (plan-N-balancer), `load_weight` в biz/mtproto.go и xray |
| Palma / AmneziaWG | ✅ | `handleUserAWGConfig` (main.go:623), sync/stats; гейт `plan.AmneziaWGEnabled`. `awg_mimic_i1` читается, но не задаётся никем (опциональный ключ) |
| Shadowsocks / SOCKS / HTTP | 💀 остатки | см. разд. 5: ветка SS в node-agent, колонки `tunnel_ss_*`, `Hub→exit` на SS не генерится (xray_exits.go:38). ВНИМАНИЕ: `servers.tunnel_ss_password` — это пароль Hysteria2-туннеля (`HubProvisionExisting`, server_lifecycle.go:217; provision/keys.go:192); колонку нельзя дропать, только переименовать |
| Пользовательские устройства / лимиты | 💀 лимита нет | `user_devices` пишется только при gRPC-регистрации (Flutter, client_identity.go:455); никакого ограничения числа устройств нет, пользователи Happ не регистрируются; админка показывает список |
| Квота/отсечка | ⚠️ | см. разд. 2 (`quota_cutoff_enabled`) |
| Токены пользователей / сессии | ⚠️ | `RevokeUserSession`, `RevokeAllUserSessions` не вызываются (user_session.go:144,151): нет logout, смена пароля не отзывает сессии; `user_sessions` не чистится по `expires_at` |
| `reputation_score` | 💀 | пишется монитором туннелей (server.go:12-33, xray_sync.go:1420-1423), нигде не читается |
| `uptime_ratio` | 💀 | колонка (000010) нигде не пишется и не читается |
| `dial_rate_log` | 💀 write-only | пишется каждую минуту (traffic_stats.go:156-210), читателей в Go и админке нет, настройка `dial_rate_threshold_per_min` не имеет UI |
| `config_test_results` | ⚠️ | только автотюнер, см. 3.1 |
| `ssh_keys` | ⚠️ legacy | приватный ключ хранится в БД открытым текстом (`ssh_keys.private_key`, biz/ssh_keys.go:65), пароли — AES; провижининг берёт его как запасной (provision/store_sql.go:80-86); основной путь теперь Hub-ключ. `GetSshKeyByID` не вызывается |
| `ssh_password` (plain) | ✅ мигрируется | `migrateLegacySSHPasswords` (provision_wiring.go:190); колонка остаётся NOT NULL, пустая |
| Релизы / `CheckAppUpdate` | ✅ | gRPC + админка; REST-зеркало не используется. Приложение Flutter заморожено |
| ConfigApi gRPC | 💀 | в `proto` и `pb` такого сервиса нет; nginx-блоки `location /heyearth.v1.ConfigApi/` остаются в `cmd/deploy/main.go:978,1079` |
| CDN-фронтинг (`cdn_enabled/host/xh_path`) | ⚠️ скрытая | читается в подписке и xray (client_api.go:237-246, xray_sync.go:357-365); включить можно только SQL, дефолт `false`, UI нет |
| `refunds` | ✅ | `CreateRefundWithKey` с идемпотентностью; `Client.CreateRefund` (client.go:126) остался только в тестах |

---

## 4. app_settings: мёртвые ручки

Метод: статический разбор (`GetSetting*`, `SetSetting`, SQL в `app_settings`) плюс ручная проверка. Ключи, читаемые через переменную (`for _, k := range []string{...}`) скрипт мог не поймать; такие я перепроверил вручную (`entry_*`, `last_billing_month`, `last_tenant_warning_date`).

### 4.1 Пишутся из админки, в Go не читаются (мёртвые ручки)

| Ключ | Где пишется | Вердикт |
|---|---|---|
| `b2b_price_per_user`, `b2b_price_our_servers`, `b2b_base_own_servers`, `b2b_price_extra_server`, `b2b_price_premium_user`, `b2b_price_unlimited` | SettingsPage.jsx:242-247 | 💀 цены зашиты в коде (разд. 3.9) |
| `app_build_repo_url`, `app_build_repo_branch`, `app_build_site_url`, `app_build_api_base`, `app_build_platforms` | SettingsPage.jsx:258-262 | 💀 нет ни одного читателя во всём репозитории (кроме UI) |
| `hub_vless_port` | SettingsPage.jsx:230 | 💀 порты клиентам зашиты (443/2443, `xhttp_port` только читается с дефолтом) |
| `admin_theme_mode` | SettingsPage (по grep) | 💀 в Go не встречается; возможно читается фронтом (не проверено) |
| `mtproto_host` | mtproto_handlers.go:29,49 | 💀 |
| `plans.auto_renew_default` (колонка, не ключ) | PlansPage | 💀 |
| `api_endpoints` | SettingsPage.jsx:231 | ⚠️ читается только в gRPC `CheckAppUpdate` (settings.go:93), т.е. для замороженного Flutter |

### 4.2 Читаются в коде, но никто не пишет (жив только дефолт)

`xhttp_port` (дефолт 2443, client_api.go:231), `hub_country` (auto_tuner.go:328), `monitor_checkhost_nodes` (monitor.go:421), `awg_mimic_i1` (awg.go:255), `dial_rate_threshold_per_min` (dial_rate.go:61, в БД только сид миграции), `cdn_*` (только сид), `entry_failover_test_user`, `subscription_update_interval_hours` (пишутся через `entries/settings`, ✅), `quota_cutoff_enabled` (никто не пишет: разд. 2), `ghost_auto_cleanup` / `ghost_auto_repair` / `mtproto_auto_install` (сид миграций; в админке только `mtproto_auto_install`), `hub_main_server_host` vs `main_server_host` (два ключа: SettingsPage пишет `hub_main_server_host`, читается `main_server_host` в client_api.go:215 и ещё `hub_main_server_host` в entries_admin.go:374; проверить, какой из них живой на проде, не проверено).

### 4.3 Пишутся, но не читаются (телеметрия или мусор)

`awg_last_sync_at`, `awg_last_error`, `awg_peer_count` (awg_sync.go:171-183), `config_version_updated_at` (auto_tuner.go:533), `subscription_version` (читается через `GetSubscriptionVersion`, но у функции нет вызовов; значение, видимо, отдаётся клиентам косвенно: не проверено), `server_archive_retention_days` (сид миграции 000040, очистки архива нет), `reality_public_key`/`reality_short_id`/`vless_port`/`vless_uuid` (пишет `cmd/deploy`; публичные ключи читаются, `vless_port`/`vless_uuid` нет).

Ложные срабатывания скрипта (ключи рабочие): `last_billing_month`, `last_tenant_warning_date` (читаются сырым SQL, subscription_cron.go:122,153), `entry_failover_enabled` (читается циклом, entry_monitor.go:221).

---

## 5. Мёртвый код

### 5.1 Функции без вызовов (grep по слову, комментарии исключены; проверено по одной)

| Файл:строка | Символ |
|---|---|
| cmd/api/payment_sync.go:225 | `SyncSinglePayment` |
| internal/admin/plans_handlers.go:87 | `PublicListPlansHandler` |
| internal/admin/auth.go:59 | `Claims.IsSuperAdmin` |
| internal/biz/country.go:20,38 | `CountryCodeToFlag`, `GetPrimaryExitCountryCode` |
| internal/biz/direct_routes.go:161 | `GetDirectRouteServiceBySlug` |
| internal/biz/direct_routes_query.go:88,191 | `GetPlanIDsWithDirectRoutes`, `GetDirectRoutesVersion` |
| internal/biz/dunning.go:168 | `CancelActiveRetries` (но он нужен: см. 3.6, правильнее подключить) |
| internal/biz/happ_crypto.go:61 | `InvalidateHappCryptLink` |
| internal/biz/happ_routing.go:126,256 | `BuildHappRoutingDeeplinkForPlan`, `BuildHappRoutingDeeplink` |
| internal/biz/payments.go:40,49 | `UpdatePaymentYookassa`, `GetPaymentByYookassaID` |
| internal/biz/payments.go:516,531,551 | `LinkTelegramUser`, `GetLinkedAccounts`, `SetActiveLinkedAccount` (остаток Telegram-бота) |
| internal/biz/plans.go:397 | `IsProtocolAllowed` |
| internal/biz/server.go:133,143,177 | `UpdateTunnelStatus`, `UpdateTunnelStatusWithLatency`, `UpdateTunnelStatusWithError` |
| internal/biz/server_lifecycle.go:106,191 | `GetSubscriptionVersion`, `withTimeout` |
| internal/biz/settings.go:79 | `GetPriceRub` (ключи `price_*` не существуют) |
| internal/biz/ssh_keys.go:45 | `GetSshKeyByID` |
| internal/biz/traffic_masking.go:19 | `ListActiveTrafficMaskingRules` (используется только `...Context`) |
| internal/biz/user.go:61 | `GetUserByUsername` |
| internal/biz/user_session.go:144,151 | `RevokeUserSession`, `RevokeAllUserSessions` (нужны: см. 3.10) |
| internal/config/config.go:17-37 | `AddSSScript`, `DataFile`, `MappingFile`, `PlatformFile`, `BotUserMapFile` (остатки Python-бота и SS) |
| internal/middleware/ratelimit.go:169 | `MaxBodySize` |
| internal/yookassa/client.go:71,74,101,115 | `IsTestMode`, `ShopID`, `CapturePayment`, `CancelPayment` |
| internal/yookassa/webhook.go:117,129,142 | `ExtractRefundPaymentID`, `ExtractObjectAsPayment`, `ExtractObjectAsRefund` |
| cmd/deploy/main.go:312,323 | `findFlutterCmd`, `flutterExec` |

Только в тестах: `buildClashConfig` (subscription_handler.go:250), `ListQuotaExceededUserIDs`, `ListActiveVlessClients` (оба «без Context» дубли), `Client.CreateRefund`, `InstallMTProxyOnNode`, `RelayPreflight`, `HysteriaInstallError`, `ExitXrayGuardScript`, `Node.CredentialsError`, `hubSigner.Sign`, `ExpressSelector.Choose` (в тесте используется, вне теста подключается иначе: проверить).

### 5.2 Удалённые функции с живыми остатками

- `removedServerActions`, `goneHandler`-маршруты, `setupGoneMsg`, `movedServerActionMsg`, `fileWriteGoneMsg`, `tunnelCredsGoneMsg` (servers_handlers.go:33-58, 479-507; routes.go:116,197): UI их не шлёт. Удалить вместе с тестами (`servers_safety_test.go` и др.).
- node-agent: `CreateUser`/`DeleteUser` (cmd/node-agent/main.go:27, 69) не вызываются Hub-ом (Hub использует только `GetMetrics`: admin/servers_handlers.go:674, provision_handlers.go:277, server_metrics_collector.go:117). Это Shadowsocks-эпоха: `shadowsocks-libev@`, `/etc/shadowsocks-libev/users`, jq-редактирование xray-конфига с подстановкой UUID в `bash -c`. Они открыты на mTLS :50052 без вызывающих: поверхность атаки без пользы.
- Колонки и поля: `servers.tunnel_ss_method` (пишется формой UpdateServer servers_handlers.go:414-415, значений нет), `tunnel_private_key` (provision обнуляет), `ssh_password` (всегда пусто), `reputation_score`, `uptime_ratio`.
- `/heyearth.v1.ConfigApi/` в nginx (cmd/deploy/main.go:978,1079).
- В git лежат бинарники и инструменты: `.build/heyearth-api` 23 МБ, `.build/node-agent` 16 МБ, `.build/heyearth-bot` 17 МБ (бота в `cmd/` нет), `.tools/protoc/bin/protoc.exe`; в рабочем дереве `api.exe`, `deploy.exe`, `node-agent.exe`, `cmd/api/api.exe` (в git не отслеживаются, но засоряют). `git status` показывает изменённые `.build/*`: каждый деплой раздувает историю.

### 5.3 Рост таблиц без очистки

`traffic_stats` (строка на пользователя в минуту активности, traffic_stats.go:129), `user_sessions` (строка на логин, никогда не удаляется по сроку), `config_test_results`, `payments` pending без `yookassa_payment_id`. Очистки нет (grep `DELETE FROM` — только `alert_events`, `dial_rate_log`). Не критично сейчас (42 активных пользователя), но неограниченный рост.

---

## 6. Итоговые списки

### 6.1 Удалить

1. Маршруты 410 и `removedServerActions` с тестами; `GET/PUT file-content`, `files`, `GET /servers/{id}/metrics` (или подключить в UI, если нужны); `fetchServerFiles` в `admin.js`.
2. node-agent `CreateUser`/`DeleteUser` + Shadowsocks-ветка, и соответствующие RPC в `proto/node_agent`.
3. nginx `ConfigApi` (cmd/deploy/main.go:978,1079).
4. `POST /api/v1/payment/tenant-topup` (дубль `business/topup-init`), `GET /api/v1/app/update` (если Flutter остаётся на gRPC), второй маршрут вебхука ЮKassa (оставить один).
5. `/api/public/tariff/calculate` и `PublicListPlansHandler` после слияния формул (см. «починить»).
6. Мёртвые функции из 5.1 (кроме `CancelActiveRetries`, `Revoke*Session`: их надо подключить).
7. Карточки настроек `b2b_price_*`, `app_build_*`, `hub_vless_port`, `mtproto_host` в `SettingsPage`/`MTProtoPage` и поле в `UpdateMTProtoSettings`; колонку `plans.auto_renew_default` либо подключить (лучше), либо убрать.
8. Телеметрия без читателей: `dial_rate_log` + таблица + настройка, `reputation_score`, `uptime_ratio`, `awg_last_*`, `config_version_updated_at`.
9. `internal/config` остатки бота/SS, `.build/heyearth-bot`, бинарники из git (`.build/*`, `.tools/protoc`).

### 6.2 Починить (по приоритету)

1. **Пробный план продлевается бесконечно** (3.5): ставить `auto_renew` из `plans.auto_renew_default` (для free-trial `false`) и/или не продлевать бесплатные планы в `processAutoRenew`; перед этим выполнить SQL-проверку из 3.5, чтобы оценить масштаб.
2. **Промокоды** (3.8): единый путь применения; `free_trial` через `free_days`, `traffic_gift` реально начислять, `ApplyPromocode` проверять `is_active` и тип, нормализовать регистр, подключить `CheckPromoPerUserLimit` в gRPC; считать использование в отдельной таблице погашений, увеличивать `current_uses` только после успешной оплаты.
3. **Dunning** (3.6): вызывать `CancelActiveRetries` при успешной оплате/продлении; в `attemptRetry` проверять, что подписка ещё не продлена; сбрасывать зависшие `in_progress` по таймауту.
4. **B2B**: возобновлять `suspended` при пополнении (как обещает письмо); догонять месячное списание (`last_billing_month` сравнивать, а не ждать час 0); решить, что значит `suspended`/`max_users` для доступа; одна формула цены (из `app_settings` или кода, но не три копии).
5. **Квоты**: определиться — `quota_cutoff_enabled` включить на проде (и дать UI-переключатель) или убрать, чтобы VLESS и Hysteria2 вели себя одинаково.
6. **Сессии**: logout, отзыв при смене пароля, очистка просроченных `user_sessions`; подключить `RevokeUserSession`.
7. **Cert expiry**: вместо `log.Printf` использовать `RaiseAlert`; добавить проверку сертификатов нод.
8. **Ранний return `StartXraySyncLoop`**: вынести `StartTunnelHealthMonitor`/`StartCertExpiryMonitor`/`StartAutoTuner` из него в `main`.
9. **Автотюнер** (если когда-нибудь включать): DNS-тест реальным запросом, SNI-смену делать только вместе с bump `config_version` и переходным списком `serverNames`, убрать «fingerprint-оптимизацию» без читателей.
10. **Ghost reconciler**: дефолт `ghost_auto_cleanup` на `false` либо вывести в UI; `mtproto_auto_install` ставить только при `mtproto_enabled=true`.
11. Метрики нод: переиспользовать gRPC-соединение и TLS-конфиг вместо нового на каждый опрос раз в 10 с.
12. `traffic_stats`, `user_sessions`, `config_test_results`: добавить retention.
13. `ssh_keys.private_key` шифровать так же, как пароли, либо выкинуть таблицу и страницу.

### 6.3 Доделать или выключить

| Что | Предложение |
|---|---|
| Маскировка трафика (3.2) | выключить/удалить, либо реально реализовать подмену SNI и учёт `tenant_id` |
| Автотюнер (3.1) | оставить выключенным; если нужен только ротатор obfs Hysteria2, вынести ротацию в отдельную задачу |
| `quota_cutoff_enabled` | включить осознанно или удалить код отсечки |
| Лимит устройств | либо реализовать (Happ их не регистрирует, потому нужна привязка к токену подписки), либо убрать таблицу/страницу `user_devices` |
| Реферальная система | отсутствует, решать продуктово |
| Тенанты: выдача доступа участникам | описать, что именно покупает тенант (`max_users`, свои серверы), и либо реализовать enforcement, либо упростить модель |
| CDN-фронтинг | дать UI-переключатель или удалить |
| MTProxy | единый флаг: `mtproto_enabled` управляет и установкой, и выдачей |
| Settings API без белого списка | ввести список разрешённых ключей, иначе мёртвые ручки будут появляться снова |
| Приложение Flutter | вместе с ним замороженные части API: `check-access`, gRPC `ClientApi` (Register/Login/…), `user_devices`, `CheckAppUpdate`, `api_endpoints` |

### 6.4 Не проверено

- Значения app_settings и данные БД на проде (включая `quota_cutoff_enabled`, `smtp_enabled`, число бесплатных подписок с `auto_renew`).
- Читает ли фронт `admin_theme_mode`; шлёт ли `MTProtoPage` все поля `PUT /api/admin/mtproto`.
- Влияние `setCORS` вместо админского в `entries_admin.go`.
- Какой из `hub_main_server_host` / `main_server_host` реально выставлен на проде.
- Доставка алертов в Telegram (по прошлому аудиту бот не настроен).
- Утверждение прошлого аудита про `PublicCreateTenantHandler` с `Access-Control-Allow-Origin: *` не перепроверялось.
