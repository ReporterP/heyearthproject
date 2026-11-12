# Аудит админки HiEarth (admin_web) — 2026-11-13

Метод: статический разбор кода `admin_web/src` против `server/backend/internal/admin/*.go`, `cmd/api/main.go`, `cmd/api/entries_admin.go`, `cmd/api/alerts_admin.go`. Базовая линия — `docs/research/2026-10-12-admin-panel-audit.md`; каждое утверждение перепроверено по текущему коду. В браузере ничего не запускалось, прод не трогался: всё, что зависит от реального поведения (визуальная тема, данные в БД, реальные ответы), помечено «не проверено».

Легенда: ✅ работает, ⚠️ работает частично / вводит в заблуждение, ❌ сломано, 💀 мёртвое (недостижимо / ничего не делает / данные никто не читает).

## 0. Что починено с 2026-10-12 (старые утверждения НЕ копировать)

- `GET /servers/{id}` и `GET /settings` больше не отдают секреты (`redactServer` servers_handlers.go:102-107, `redactSettings` settings_handlers.go:41-53). Секретные поля Settings — write-only (`SecretSettingField`).
- Возврат денег: отдельное право `payments.refund` (routes.go:477-509), диалог с суммой, причиной и вводом слова «ВОЗВРАТ» (`RefundDialog.jsx`), без `prompt()`.
- Опасные действия сервера убраны (`removedServerActions` → 410, servers_handlers.go:51-58); остался только `restart_agent` за `server_ops.execute` (UI и бэк согласованы).
- 403 больше не разлогинивает (`client.js:60-67`: разлогин только на 401).
- Нода/выход: полноценный мастер, карточка, задачи, журналы, подтверждение отпечатка SSH, «Проверить туннель» — всё сверено по полям, работает.
- «Online у всех» — исправлено на странице Нод (lifecycle_state отдаётся `nodeItem`).
- Автотюнер: предупреждение при включении и учёт `loop_running` — сделано.

Всё ещё сломано с тех пор (подтверждено по коду): создание админов/ролей, создание пользователя, колонка «План» у пользователей, хоткеи j/k/Enter, MTProto-сохранение (частично), фильтры подписок/платежей/аудита, Inter/slate/cyan в `style.css`, сырые `fetch` (было 9, теперь 15 мест), `prompt()/confirm()` (5 мест).

---

## 1. Оболочка: маршруты, меню, права, палитра

Файлы: `main.jsx`, `components/Sidebar.jsx`, `components/commands.js`, `auth/access.js`, `Layout.jsx`.

| # | Что | Статус | Доказательство |
|---|---|---|---|
| 1 | 23 страницы зарегистрированы в роутере и все есть в меню (кроме `/servers/:id`, он открывается из карточки ноды «Метрики и файлы сервера» NodeDrawer.jsx:325 и ServerDetailPage «Управление нодой») | ✅ | main.jsx:55-79, Sidebar.jsx:15-63 |
| 2 | `/servers` → редирект на `/nodes` | ✅ | main.jsx:64 |
| 3 | Скрытие пунктов меню по правам (`PAGE_PERMS`) и для tenant-админов | ✅ | access.js:5-42 |
| 4 | Защиты на уровне маршрута нет: прямой URL `/admins` у админа без права открывает страницу, она сыплет 403-тостами | ⚠️ | main.jsx:55-79 (нет обёртки с проверкой права) |
| 5 | Палитра ⌘K ищет только страницы и «Выйти»; пользователей/серверы/платежи не ищет | ⚠️ | CommandPalette.jsx:36-45 |
| 6 | Подсказки хоткеев в палитре «⌘D», «⌘U», «⇧⌘Q» ничем не привязаны (существует только ⌘K в Layout.jsx:12 и j/k/Enter на Users) | 💀 | commands.js:17,20,57 vs grep `useHotkey` (только Layout, UsersPage) |
| 7 | `useHotkey` не пропускает ввод: `preventDefault()` на любой `k`/`j`/`Enter` без проверки, что фокус в поле | ❌ | hooks/useHotkey.js:13-18 |
| 8 | Тот же `useHotkey({key:'k'})` без `meta` реагирует и на Ctrl+K (двойное срабатывание вместе с палитрой) | ⚠️ | UsersPage.jsx:143, useHotkey.js:14 |
| 9 | Группа меню «Инфраструктура» содержит 9 пунктов, из них владельцу нужны ~3 | ⚠️ | Sidebar.jsx:31-44 |
| 10 | Названия «Дунинг», «Тенанты», «Релизы», «Маскировка трафика» — жаргон | ⚠️ | commands.js:35, Sidebar.jsx:50 |
| 11 | Мёртвый компонент `components/StatCard.jsx` (никем не импортируется, ещё и со старыми токенами, `rounded-2xl`) | 💀 | grep import: 0 вхождений |

## 2. Дашборд (`/dashboard`)

Назначение: сводка. Вызовы: `GET /overview`, `/traffic-chart?days=30`, `/capacity`. Форма ответов сверена по JSON-тегам (types.go:11-52, capacity_handler.go) — совпадает.

| Действие / блок | Статус | Доказательство |
|---|---|---|
| 9 KPI-карточек со ссылками на разделы | ✅ | DashboardPage.jsx:188-198 |
| Мини-график трафика 30 дн. (подписи дат могут выводиться как ISO-время, `recorded_at::date` сканируется в string; не проверено) | ⚠️ | DashboardPage.jsx:219-224, traffic_handlers.go:206 |
| Виджет «Ёмкость Hub»: константы захардкожены под «VDSina 2 ядра / 32 ТБ / 1200 ₽» — после перехода на новый парк (Timeweb+VDSina) «Запас/Пора масштабировать» и «Себестоимость/юзер» неверны | ⚠️ | capacity_handler.go:25-31 |
| Виджет «Exit · имя»: берёт ОДИН сервер `ORDER BY id LIMIT 1` (может оказаться relay или выключенной нодой; остальные выходы не видны) | ❌ | capacity_handler.go:88-93 |
| «Здоровье серверов»: запрос без фильтра роли/статуса — в список попадают relay (у них нет node-agent, сборщик метрик их пропускает, server_metrics_collector.go:42-47), pending и archived; точка статуса берётся из `servers.status` (доступность агента), а не из lifecycle. Что именно показывается для relay — не проверено | ⚠️ | dashboard_handlers.go:112-120, DashboardPage.jsx:46-48,233-241 |
| Карточки «Серверов / онлайн» считают relay и архив | ⚠️ | dashboard_handlers.go: `COUNT(*) FROM servers`, `status='online'` |
| Нет блока «Бамбук» / входов в РФ / активных алертов — картина здоровья разбросана по 4 страницам | ⚠️ | (Бамбук только на /auto-tuner, входы на /entries, алерты на /alerts) |
| Жаргон/английский: «Ёмкость Hub», «CPU load», «User ID», «Exit · …» | ⚠️ | DashboardPage.jsx:107,111,129,256 |

## 3. Пользователи (`/users`) + шторка пользователя

Вызовы: `GET /users` (page,page_size,search,status,sort,dir), `POST /users`, `PATCH /users/{id}`, `DELETE /users/{id}`, `GET /users/{id}/devices|traffic`.

| Действие | Статус | Доказательство |
|---|---|---|
| Список, сортировка (username, valid_until, last_seen, created_at, traffic_30d), пагинация, табы статуса (все/активные/истекают/без плана/заблокированы) | ✅ | UsersPage.jsx:73-83 vs users_handlers.go:14-170 |
| Колонка «План» и бейдж плана в шторке — ВСЕГДА «—» / «no plan»: UI читает `u.plan`, бэкенд отдаёт `current_plan` | ❌ | UsersPage.jsx:244, UserDrawer.jsx:126-127 vs users_handlers.go:148 |
| «+ Создать пользователя»: UI шлёт `{username,password,role,is_blocked}`, бэк требует `user_id` → всегда 400 «user_id is required»; `username`/`password` бэк не читает вообще | ❌ | UsersPage.jsx:106-108 vs users_handlers.go:173-181, types.go `userPayload` |
| Поиск: Enter не отправляет форму (хоткей `Enter` вызывает `preventDefault` до проверки), работает только кнопка «Поиск»; буквы `j`/`k` нельзя набрать в поле поиска | ❌ | useHotkey.js:17, UsersPage.jsx:139-152,154-157 |
| Шторка → «Профиль»: правка username/пароля — тост «OK», но бэк игнорирует эти поля (ничего не сохраняется) | ❌ | ProfileTab.jsx:291-292 vs types.go `userPayload` (нет username/password) |
| Профиль → Telegram ID: из input уходит строка, бэк ждёт `*int64` → 400 «Invalid request body» | ❌ | ProfileTab.jsx:294 vs types.go `TelegramID *int64` |
| Профиль → роль (User/Premium) | ✅ | ProfileTab.jsx:293 |
| Блок/разблок переключателем | ✅ | UserDrawer.jsx:70-81 (PATCH `is_blocked`) |
| Удаление пользователя (жёсткий DELETE, диалог без ввода имени; последствия для платежей/подписок не проверены — каскад FK не смотрел) | ⚠️ | UserDrawer.jsx:83-96, users_handlers.go:246-264 |
| Кнопка «Devices»: показывает `app_version` и `last_seen`, которых бэк не отдаёт (есть `device_label`, `last_seen_at`) → пустые поля | ⚠️ | UserDrawer.jsx:184-185 vs users_handlers.go:300-307 |
| Вкладка «Подписки»: список/создание/правка/удаление, поля совпадают | ✅ | SubscriptionsTab.jsx:76-93 vs subscriptions_handlers.go:117-243 |
| Вкладка «Трафик/Платежи» | ✅ | TrafficPaymentsTab.jsx, biz/traffic.go:338-362 |
| Открытие шторки по ссылке на пользователя вне текущей страницы — не открывается | ⚠️ | UsersPage.jsx:119-128 (комментарий v1) |
| Кнопка «⋯» в строке = тот же клик по строке (заглушка) | 💀 | UsersPage.jsx:269-278 |
| Английский: «Devices», «User/Premium», «blocked/no plan», статусы подписок `active/expired/pending_renewal` | ⚠️ | UserDrawer.jsx:101,127; SubscriptionsTab.jsx:12-17 |
| Создание/правка/удаление подписок не пишет в журнал аудита | ⚠️ | subscriptions_handlers.go (нет `WriteAuditLog`) |

## 4. История подписок (`/subscriptions`)

Вызов: `GET /subscriptions` + `DELETE /subscriptions/{id}`.

| Действие | Статус | Доказательство |
|---|---|---|
| Список, сортировка, пагинация, поиск по UUID пользователя | ✅ | SubscriptionsPage.jsx:59-73 |
| Фильтры «План» и «Статус» — параметры уходят, бэк их не читает (только search/user_db_id) | ❌ | SubscriptionsPage.jsx:62-66 vs subscriptions_handlers.go:16-35 |
| Фильтры «Истекают за N дней», «С промокодом» работают только на текущей странице (20 строк) | ⚠️ | SubscriptionsPage.jsx:81-101 |
| Удалить подписку (диалог) | ✅ | SubscriptionsPage.jsx:103-112 |
| «Открыть юзера» → /users?open&search | ✅ | SubscriptionsPage.jsx:114-126 |
| Статусы на английском (`Active/Expired/Pending Renewal`, `expired` в бейдже) | ⚠️ | SubscriptionsPage.jsx:170-173,245 |
| Страница дублирует вкладку «Подписки» в шторке пользователя | ⚠️ (дубль) | см. «Объединить» |

## 5. Промокоды (`/promocodes`)

Вызовы: `GET/POST /promocodes`, `PATCH/DELETE /promocodes/{code}`, `POST /promocodes/{code}/reapply-plan`, `GET /plans`. Поля сверены с `promocodeFullResponse/Payload` (promocodes_handlers.go:16-49).

| Действие | Статус | Доказательство |
|---|---|---|
| Список, поиск, пагинация | ✅ | PromocodesPage.jsx:46,165-172 |
| Фильтр «Активные/Выключенные» — клиентский, только по текущей странице | ⚠️ | PromocodesPage.jsx:134-139 |
| Создать/изменить (4 типа, планы, лимиты, срок); валидацию бэка не читал | ✅ | PromocodesPage.jsx:71-89,240-400 |
| Удалить (ConfirmDialog) | ✅ | PromocodesPage.jsx:91-100 |
| «План→всем» — нативный `window.confirm`, неочевидная подпись, переписывает историю подписок | ⚠️ | PromocodesPage.jsx:102-118,220-227 |
| SearchPaginationBar на старых токенах (`bg-surface-alt`, `bg-brand`, `border-white/10`) | ⚠️ (тема) | SearchPaginationBar.jsx:17-40 |

## 6. Платежи (`/payments`)

Вызовы: `GET/POST /payments`, `PATCH /payments/{id}`, `POST /payments/{id}/cancel|check-status`, `POST /sync-pending-payments` (сырой fetch).

| Действие | Статус | Доказательство |
|---|---|---|
| Список, сортировка, пагинация, фильтр по статусу, «Реальный доход» | ✅ | PaymentsPage.jsx:150-169 vs payments_handlers.go:16-149 |
| Фильтры «С даты» / «По дату» — параметры `from`/`to` бэк игнорирует | ❌ | PaymentsPage.jsx:155 vs payments_handlers.go:17-43 |
| «Статус» (опрос ЮKassa) | ✅ | PaymentsPage.jsx:400-416, admin_payment_actions.go:139-179 |
| «Вернуть» (возврат): сумма, причина, слово ВОЗВРАТ, право payments.refund | ✅ | RefundDialog.jsx, routes.go:477-509 |
| «Sync pending» — сырой `fetch` с `Bearer null` при пустом токене, 401 не разлогинивает | ⚠️ | PaymentsPage.jsx:235-245 |
| «Создать платёж» / «Ред.» — РУЧНАЯ запись/правка платежа без аудит-лога и без пометки `test`: статус `succeeded` сразу попадает в «Реальный доход»; подписку не активирует; правка перезаписывает `notes`/`metadata` | ⚠️ (опасно) | PaymentsPage.jsx:175-233,397; payments_handlers.go:157-215 |
| Плашка «Сумма видимых» складывает суммы разных валют/статусов | ⚠️ | PaymentsPage.jsx:171-173,276-281 |
| Английские статусы «Pending/Succeeded/Waiting for capture», колонки «User», «ID», бейджи `TEST`/`RECOVERED` | ⚠️ | PaymentsPage.jsx:28-35,336,359-360 |
| `shadow-xl` на поповере JSON; кнопка «JSON» — техническая деталь | ⚠️ (тема) | PaymentsPage.jsx:97-117 |
| API-метод `resendPaymentNotifications` (`POST /payments/resend`) нигде не вызывается | 💀 | admin.js:90, grep: 0 вызовов |

## 7. Операции платежей («Дунинг») (`/payment-ops`)

Вызовы: `GET /email-log`, `GET /payment-retries`, `POST /payment-retries/{id}/cancel`.

| Действие | Статус | Доказательство |
|---|---|---|
| Журнал email (поиск, пагинация) | ✅ | PaymentOpsPage.jsx:44-83 vs email_log_handlers.go:24-100 |
| Очередь повторных списаний | ✅ | PaymentOpsPage.jsx:85-149 vs email_log_handlers.go:115+ |
| «Отменить» повтор — нативный `prompt()` с английским `admin_manual_cancel` по умолчанию | ⚠️ | PaymentOpsPage.jsx:90-98 |
| Жаргон/английский: «Дунинг», «Retry queue (dunning)», «Email log», «Grace до» | ⚠️ | commands.js:35, PaymentOpsPage.jsx:162-172 |

## 8. Трафик (`/traffic`)

Вызовы: `GET /traffic?period&page&search`, `GET /traffic/{user_id}?period`.

| Действие | Статус | Доказательство |
|---|---|---|
| Таблица по пользователям (сегодня/неделя/месяц), поиск, пагинация | ✅ | TrafficPage.jsx:79-91 vs traffic_handlers.go:15-92 |
| Клик по строке → график по дням | ✅ | TrafficPage.jsx:93-100,204-241 |
| Поиск шлёт запрос на каждую букву (нет debounce) | ⚠️ | TrafficPage.jsx:128-134 |
| Английское: «User ID», «Username», «Upload/Download» | ⚠️ | TrafficPage.jsx:153-154,55-56 |
| Дублирует колонку «Трафик 30д» на Users и вкладку «Трафик» в шторке | ⚠️ (дубль) | см. «Объединить» |

## 9. Ноды (`/nodes`) + мастер + карточка — ЯДРО РАБОТЫ ВЛАДЕЛЬЦА

Вызовы: `GET /nodes?role=`, `POST /servers/probe`, `POST /servers`, `POST /servers/{id}/host-key|preflight|provision|verify|deprovision|mtproxy|check-tunnel`, `PUT /servers/{id}/routing`, `DELETE /servers/{id}`, `GET /provision-jobs…`, `POST /provision-jobs/{id}/resume`, `GET /servers/{id}/logs`, `GET /ssh-keys/hub`. Поля запросов мастера сверены (`createRequest` ↔ `createNodePayload`, provision_handlers.go:369-377). Новая модель — самая чистая часть админки: русский язык, ConfirmDanger с вводом имени, подсказки «почему кнопка неактивна», тесты на модели (`*.test.js`).

| Действие | Статус | Доказательство |
|---|---|---|
| Список по ролям (выходы / резервные входы в РФ), автообновление 15 с, состояние, оба пути туннеля | ✅ | NodesPage.jsx:77-204, provision_handlers.go:1097-1113 |
| Мастер «Добавить ноду»: адрес+порт, пароль root (необязательно; ключ Hub вместо пароля), проверка ключа SSH с подтверждением отпечатка, предпроверка, настройка, прогресс | ✅ | AddNodeWizard.jsx, wizardModel.js:222-252 |
| Карточка: «Проверить туннель» (только чтение) | ✅ | NodeDrawer.jsx:301-304, tunnel_check.go |
| «Проверить настройку», «Настроить/Переустановить», «Переустановить Telegram-прокси» | ✅ | NodeDrawer.jsx:305-322, routes.go:149-154 |
| Переключатель «Запасной TCP-путь» (с предупреждением) | ✅ | NodeDrawer.jsx:286,388-393 |
| Вкладка «Тарифы и приоритет» (PUT /routing) | ✅ | NodeDrawer.jsx:330-375 |
| «Задачи» + прогресс + resume | ✅ | NodeDrawer.jsx:377-419, JobProgress.jsx |
| «Журналы» (node-agent, xray, hysteria2, mtproxy) | ✅ | NodeDrawer.jsx:421-460 vs servers_handlers.go:690-720 |
| «Опасная зона»: отключить / удалить запись (ввод имени) | ✅ | NodeDrawer.jsx:630-648 |
| Подтверждение отпечатка SSH + «Сверить заново» | ✅ | NodeDrawer.jsx:96-257 |
| В списке и в карточке НЕТ нагрузки (CPU/RAM/подключения/трафик): `nodeItem` их не отдаёт; метрики есть только на отдельной странице `/servers/:id` по малозаметной ссылке внизу «Обзора» — для «следить за нагрузкой» это главный пробел | ⚠️ | provision_handlers.go:1097-1113 (нет load_metrics), NodeDrawer.jsx:325 |
| Hub в таблице нод отсутствует (он не в `servers`) — его нагрузка видна только в виджете на дашборде | ⚠️ | NodesPage.jsx, capacity_handler.go |
| Тот же мастер открывается и со страницы «Входы» (`initialRole="relay"`): вход в РФ добавляется из двух меню | ⚠️ (дубль) | EntriesPage.jsx:372-375, NodesPage.jsx:196-200 |
| Страна вводится кодом ISO (NL/LV) — владельцу нужен выпадающий список | ⚠️ | wizardModel.js:231, provision_handlers.go:410-418 |

## 10. Входы (`/entries`)

Вызовы: `GET /entries`, `GET /entries/preview`, `PUT /entries/{id}`, `PUT /entries/settings`, `PUT /entries/mode`, `POST/GET /servers/{id}/mobile-check`. Форма ответа покрыта тестами на бэке (biz/entries_test.go:311) и совпадает.

| Действие | Статус | Доказательство |
|---|---|---|
| Баннер состояния, режимы hub/relay/auto, выбор резервного входа | ✅ | EntriesPage.jsx:98-162 vs entries_admin.go:282-355 |
| Таблица входов (снаружи / Бамбук через вход / из РФ), автообновление 60 с | ✅ | EntriesPage.jsx:279-304 |
| «Проверить с телефона» | ✅ | EntriesPage.jsx:296-300, mobile_check.go |
| Настройки (интервал обновления подписки, тестовый пользователь) | ✅ | EntriesPage.jsx:315-333 vs entries_admin.go:181-224 |
| «Учебная авария» с вводом слова | ✅ | EntriesPage.jsx:336-366 |
| Селектор «Адрес в подписке: домен» заблокирован текстом «после раздела «Домены»», а такого раздела в админке нет | 💀 | EntriesPage.jsx:293 vs Sidebar.jsx (нет /domains) |
| Захардкоженные домены в инструкции по DNS: `['hiearth.ru','www.hiearth.ru','admin.hiearth.ru','site.hiearth.ru']` | ⚠️ (хардкод) | EntriesPage.jsx:166 |
| Раздел пересекается с «Ноды → Резервные входы» (один набор серверов в двух меню) | ⚠️ (дубль) | NodesPage.jsx:6-9, EntriesPage.jsx |
| Жаргон «Бамбук», «A-записи», «TTL» без пояснений | ⚠️ | EntriesPage.jsx:170-196 |

## 11. Карточка сервера (`/servers/:id`) — «Метрики и файлы»

Вызовы: `GET /servers/{id}`, `GET /servers/{id}/logs`, `POST /servers/{id}/actions` (restart_agent), `POST check-tunnel`.

| Действие | Статус | Доказательство |
|---|---|---|
| Датчики CPU/RAM/Connections + мини-графики (история 20 точек только в памяти вкладки), автообновление 15 с | ✅ | ServerDetailPage.jsx:117-155,271-293 |
| Пути «Hysteria (UDP)» и «TCP (VLESS Reality)» + «Проверить туннель» | ✅ | ServerDetailPage.jsx:296-313 |
| «Перезапустить агент» (за `server_ops.execute`, UI и бэк согласованы) | ✅ | ServerDetailPage.jsx:87-89,320, routes.go:176-179 |
| Журнал: в списке есть `heyearth-api` и `nginx`, но бэк пускает только node-agent/xray/hysteria2/mtproxy и молча подставляет node-agent → UI показывает выбранный «heyearth-api», а выводит журнал node-agent. Для не-exit (relay) дефолт именно `heyearth-api` | ❌ (вводит в заблуждение) | ServerDetailPage.jsx:123,336-340 vs servers_handlers.go:690,699-702 |
| Сырые термины: «VLESS Reality», `HYSTERIA2:50443`, «MTP: OK/Down/Install…», «Connections», «CPU History», «Uptime: 3h 5m», «Пути до exit» | ⚠️ | ServerDetailPage.jsx:243-249,274,289-290,299,306-307 |
| Хардкод цветов `#6366f1/#22d3ee/#f59e0b` вместо токенов | ⚠️ (тема) | ServerDetailPage.jsx:26,57,273-274,289-291 |
| Файловый браузер сервера: ссылка называется «Метрики и файлы», маршруты `/files` и API `fetchServerFiles` есть, UI нет | 💀 | admin.js:136-139 (0 вызовов), NodeDrawer.jsx:325 |
| API-обёртка `updateServer` (PATCH /servers/{id}) нигде не вызывается | 💀 | admin.js:104 |

## 12. SSH-ключи (`/ssh-keys`)

| Действие | Статус | Доказательство |
|---|---|---|
| Карточка «Ключ Hub» (публичный ключ + отпечаток, копирование) — нужна владельцу при ручной выдаче сервера | ✅ | SshKeysPage.jsx:34-66, provision_handlers.go:1044 |
| Список/добавление/правка/удаление сохранённых приватных ключей: мастер нод подключается по ключу Hub или паролю; `ssh_key_id` нигде в provisioning/UI не присваивается серверу (grep по Go: только servers_handlers.go/types.go/biz/ssh_keys.go). Владелец вставляет в форму приватный ключ, который ничего не использует | 💀 | SshKeysPage.jsx:68-200 |
| Копирование отпечатка: «Copied», «Click to copy» на английском | ⚠️ | SshKeysPage.jsx:128,164 |
| `ConfirmDialog` на старых токенах | ⚠️ (тема) | ConfirmDialog.jsx:9-12 |

## 13. Релизы (`/releases`)

Вызовы: `GET /releases`, `POST /releases` (multipart), `POST /releases/activate`.

| Действие | Статус | Доказательство |
|---|---|---|
| Список / загрузка файла / активировать | ✅ | ReleasesPage.jsx:42-85 vs releases.go:20-130 |
| Ошибки загрузки: бэк отвечает `http.Error` (text/plain) → UI показывает только «Ошибка 400/500» без причины | ⚠️ | releases.go:59-125, client.js:74-77 |
| «Select a file» по-английски, подпись «APK файл» для любой платформы | ⚠️ | ReleasesPage.jsx:63,159 |

## 14. Маскировка трафика (`/traffic-masking`)

Правила реально читаются xray-синхронизацией (`biz/traffic_masking.go:20-49` → `cmd/api/xray_sync.go:277-317`).

| Действие | Статус | Доказательство |
|---|---|---|
| Список / создать / править / включить-выключить / удалить | ✅ | TrafficMaskingPage.jsx:39-112 vs traffic_masking_handlers.go |
| Подпись «Подмена SNI домена» — жаргон; название противоречит политике «без VPN/Xray/SNI в UI» | ⚠️ | TrafficMaskingPage.jsx:118 |

## 15. Прямая маршрутизация (`/direct-routes`) + 3 диалога

Вызовы: `GET/POST /direct-routes`, `/stats`, `/groups`, `/bulk`, `/import`, `/discover`, `PUT/DELETE /direct-routes/{id}`. Всё — сырые `fetch`.

| Действие | Статус | Доказательство |
|---|---|---|
| Список, поиск (debounce), фильтры группа/активные, сортировка, статистика | ✅ | DirectRoutesPage.jsx:44-95 vs direct_routes_handler.go:101-147 |
| Загрузка списка: ответ не проверяется на `ok`, при 401/403 молча пустая страница вместо выхода/сообщения | ⚠️ | DirectRoutesPage.jsx:69-77 |
| Создать/править/импорт JSON/«Найти по домену» (поля create/edit сверены) | ✅ | DirectRouteEditDialog.jsx:80-126, direct_routes_handler.go:38-62 |
| Массовые действия, удаление — нативный `window.confirm` | ⚠️ | DirectRoutesPage.jsx:105,125 |
| Пагинации нет: бэк отдаёт все записи вместе со списками доменов/IP; `page` в `useUrlState` не используется | ⚠️ | direct_routes_handler.go:137-146, DirectRoutesPage.jsx:36 |
| Тема: три диалога целиком на `slate-*`/`shadow-2xl` (61 нарушение) | ⚠️ (тема) | DirectRouteEditDialog.jsx, DirectRoutesImportDialog.jsx, DiscoveryDialog.jsx |
| `<a href="/plans">` — полная перезагрузка вместо роутера; `grid-cols-4` без адаптива | ⚠️ | DirectRoutesPage.jsx:166,174 |
| «Edit»/«Del»; поля «Timeout», «DNS серверы», CIDR — избыточно для владельца | ⚠️ | DirectRoutesPage.jsx:263-264 |

## 16. MTProto (`/mtproto`)

Вызовы: сырой `fetch` `GET/PUT /api/admin/mtproto`, `POST …/generate-secret`.

| Действие | Статус | Доказательство |
|---|---|---|
| Загрузка и «Сохранить»: форма не содержит host/port, а PUT пишет `mtproto_host`=«» и `mtproto_port`=«» (на деле значение возвращается к дефолту 8443 — `GetSettingOrDefault` для пустого, biz/settings.go:69-75): ручной порт затирается | ⚠️ | MTProtoPage.jsx:37-67 vs mtproto_handlers.go:55-60 |
| «Regenerate» секрета: сразу пишет секрет в БД (не по «Сохранить», как обещает тост) и использует ЗАПИСАННЫЙ в БД fronting-домен, а не введённый | ❌ | MTProtoPage.jsx:69-81 vs mtproto_handlers.go:83-96 |
| Любая ошибка выводится как «HTTP 400»/«HTTP 401»: полезный текст бэка «Задайте Fronting domain…» теряется; 401 не разлогинивает | ❌ | MTProtoPage.jsx:13-23 vs mtproto_handlers.go:86-88 |
| `confirm()` нативный | ⚠️ | MTProtoPage.jsx:70 |
| Хардкод-плейсхолдеры `mt.hiearth.ru`, `@hiearth`; английский: «Fronting domain (fake-TLS SNI)», «Secret», «Regenerate», «Sponsor channel» | ⚠️ (хардкод) | MTProtoPage.jsx:122-169 |
| Дубль: переустановка Telegram-прокси — кнопка в карточке ноды; настройки — отдельная страница | ⚠️ (дубль) | NodeDrawer.jsx:318-321 |

## 17. «Протоколы и маскировка» / Автотюнер (`/auto-tuner`)

Вызовы: `GET /auto-tuner/status|history`, `POST toggle|interval|run-now|cleanup`, `GET /hysteria2/status` (BambooCard). Формы ответов сверены (auto_tuner_handlers.go:59-74,134-155).

| Действие | Статус | Доказательство |
|---|---|---|
| Карточка «Бамбук» (доступность снаружи, выбранный выход, ротация obfs) — полезна, но спрятана в разделе «Автотюнер» | ✅ | AutoTunerPage.jsx:147, BambooCard.jsx |
| Включить/выключить автотюнер: предупреждение и флажок «Понимаю последствия», учёт `loop_running` | ✅ | AutoTunerPage.jsx:61-80,379-400 |
| «Запустить сейчас» с причиной блокировки | ✅ | AutoTunerPage.jsx:88-93 |
| Интервал | ✅ | AutoTunerPage.jsx:170-179 |
| «Очистить» историю без подтверждения | ⚠️ | AutoTunerPage.jsx:120-127 |
| История тестов SNI/выходов, активные параметры — данные верные, но целиком жаргон: «SNI домен», «TLS Fingerprint», «Transport/Flow (tcp + xtls-rprx-vision)», «Mux Concurrency», «VLESS Reality», «Hub Inbound», «Exit Nodes», «Applied/ACTIVE» | ⚠️ | AutoTunerPage.jsx:212-240,256,274,303,320 |

## 18. Алерты (`/alerts`)

Вызовы: `GET/PUT /alerts/settings`, `POST /alerts/test`, `GET /alerts/events`. Поля сверены с alerts_admin.go.

| Действие | Статус | Доказательство |
|---|---|---|
| Настройка бота Telegram (токен write-only, Chat ID), «Отправить тест», удалить токен (двухшаговое подтверждение) | ✅ | AlertsPage.jsx:183-203,216-259, alerts_admin.go:79-153 |
| Баннер «не доставлено / Telegram не настроен» | ✅ | AlertsPage.jsx:78-129 |
| Журнал событий со сворачиванием повторов | ✅ | AlertsPage.jsx:38-56,261-309 |
| Колонка «Ключ» показывает внутренние ключи алертов как есть | ⚠️ | AlertsPage.jsx:296 |
| Лучшая страница для «следить за здоровьем», но живёт в «Инфраструктуре» и не отражена на дашборде | ⚠️ | |

## 19. Тарифы (`/plans`)

Вызовы: `GET/POST/PATCH/DELETE /plans`, `GET /servers?page_size=100`. Поля сверены с `biz/plans.go:30-43`.

| Действие | Статус | Доказательство |
|---|---|---|
| Список карточками, создать/править/удалить, цена/срок/трафик/протоколы/возможности/Express/Пальма/автопродление/макс. активаций | ✅ | PlansPage.jsx:69-102,130-450 |
| Список «Серверы (выбрать доступные)» берётся из `/servers` без фильтра роли: в нём relay-серверы (входы), выбирать их для тарифа бессмысленно | ⚠️ | PlansPage.jsx:43,266-283 vs servers_handlers.go:133-175 |
| Привязку «тариф ↔ сервер» можно править в двух местах: здесь (`server_ids`) и в карточке ноды «Тарифы и приоритет» — два редактора одного отношения | ⚠️ (дубль) | PlansPage.jsx:264-285, NodeDrawer.jsx:330-375 |
| Сырые названия протоколов `VLESS`, `HYSTERIA2`, `XHTTP` в бейджах и чекбоксах — нарушение политики именования | ⚠️ | PlansPage.jsx:26,147-149,257 |
| Жаргон: «Express (низколатентный UDP)», «Up/Down Mbps», «Brutal congestion control», «Slug (URL-ключ)», placeholder `vless-reality` | ⚠️ | PlansPage.jsx:182-186,317-361 |
| Удаление плана — подтверждение без ввода имени; последствия для активных подписок не проверены | ⚠️ | PlansPage.jsx:453-460 |
| Права тарифов привязаны к `settings.read/write`, отдельного права нет | ⚠️ | access.js:21, routes.go:230-249 |

## 20. Администраторы (`/admins`)

Вызовы: `GET/POST/PATCH /admin-users`, `GET/POST/PATCH /admin-roles`, `GET /admin-permissions`.

| Действие | Статус | Доказательство |
|---|---|---|
| Список админов и ролей (чтение): роли админа приходят как `[]string` (ключи), UI ждёт объекты и сверяет в чекбоксах по `id` → в форме правки ни одна роль не отмечена | ⚠️ | AdminsPage.jsx:81,262 vs auth.go:32 |
| Создать админа: UI шлёт `role_ids`, бэк читает `role_keys` → всегда 400 «at least one role is required» | ❌ | AdminsPage.jsx:88 vs auth.go:96,396 |
| Править админа: имя/пароль/активен работает; смена ролей молча не применяется (`role_keys` nil) | ⚠️ | AdminsPage.jsx:88-94 vs auth.go:515 |
| Создать роль: UI шлёт `{key,name,permissions}`, бэк читает `{role_key,permission_keys}` → 400 «role_key and name are required» | ❌ | AdminsPage.jsx:122 vs auth.go:99-104,560 |
| Править роль: права (`permissions` вместо `permission_keys`) не сохраняются; ключ роли в карточке пуст — UI читает `r.key`, бэк отдаёт `role_key` | ❌ | AdminsPage.jsx:113,122,226 vs auth.go:42,659+ |
| Смены собственного пароля нет нигде (в Settings только «Выйти») | ⚠️ | SettingsPage.jsx:94-101 |

## 21. Журнал аудита (`/audit-log`)

| Действие | Статус | Доказательство |
|---|---|---|
| Список, пагинация | ✅ | AuditLogPage.jsx:44-62, audit_handlers.go:70-130 |
| Фильтры «Админ (username)», «С даты», «По дату» — бэк их не читает (читает `search`, `action`, `target_type`) | ❌ | AuditLogPage.jsx:49-52 vs audit_handlers.go:71-93 |
| Фильтр «Действие» — точное совпадение + запрос на каждый символ | ⚠️ | AuditLogPage.jsx:79-85, audit_handlers.go:84-88 |
| Пустые детали выводятся как «{}» (проверка `!== '{}'` на объекте всегда истинна) | ⚠️ | AuditLogPage.jsx:~139 |
| Действия показываются сырыми ключами `update_user`, `create_server` | ⚠️ | AuditLogPage.jsx:128 |

## 22. Тенанты (`/tenants`) и B2B-аналитика (`/b2b-analytics`)

| Действие | Статус | Доказательство |
|---|---|---|
| Список/создать/править/удалить компанию | ✅ | TenantsPage.jsx:123-161 vs tenants_handlers.go:19-43 |
| Кнопка «Счёт» (создание счёта ЮKassa для тенанта): `createTenantInvoice` вызывает `apiFetch('/api/admin/b2b/create-invoice')`, а `apiFetch` уже добавляет префикс `/api/admin` → запрос уходит на `/api/admin/api/admin/b2b/create-invoice` (404) | ❌ | admin.js:319-321 + client.js:1,66 vs main.go:1086 |
| «Привязать платежи»: сырые `fetch` без обработки 401; список компаний в селекторе = только текущая страница (20) | ⚠️ | TenantsPage.jsx:61,79,379-381 |
| B2B-аналитика: KPI (сырой `fetch`), работает | ✅ | B2BAnalyticsPage.jsx:44 vs tenants_handlers.go:227-260 |
| Два раздела вместо одного | ⚠️ (дубль) | |

## 23. Настройки (`/settings`)

| Действие | Статус | Доказательство |
|---|---|---|
| Аккаунт: только имя и «Выйти» | ✅ | SettingsPage.jsx:94-101 |
| Тема тёмная/светлая: пишет `admin_theme_mode` (нигде не читается) и вешает класс `light`; параллельно живёт `ThemeProvider`/`ThemePicker` (data-theme) — две системы, одна декоративная | 💀 | SettingsPage.jsx:81-86 vs ThemePicker.jsx |
| Секреты ЮKassa/SMTP — write-only | ✅ | SettingsPage.jsx:144,163, settings_handlers.go:41-53 |
| ЮKassa Shop ID, тестовый режим, e-mail чеков, SMTP, юр. данные, окно продления, донат-блок (значения — произвольные строки без валидации; «true/false» вводятся текстом) | ✅ | SettingsPage.jsx:143-216 |
| «Домен сертификата Бамбука (hub_main_server_host)» — правка опасна («менять только вместе с сертификатом»); настоящие `main_server_host`, `site_url` только на чтение | ⚠️ | SettingsPage.jsx:223-229 |
| Мёртвые настройки (карточки пишут ключи, которые бэкенд не читает): `hub_vless_port`, шесть `b2b_price_*`, пять `app_build_*` | 💀 | SettingsPage.jsx:230,242-247,258-262; grep по Go: 0 использований (`api_endpoints` читается: biz/settings.go:93) |
| Общий `POST /settings` пишет любой ключ без белого списка | ⚠️ | settings_handlers.go:100-135 |
| Переключатель языка RU/EN — английская локаль неполная | ⚠️ | SettingsPage.jsx:119-131 |

---

## 24. Сквозные проблемы

**Сырые fetch в обход `apiFetch` (нет обработки 401/403, нет единых сообщений): 15 мест**
`PaymentsPage.jsx:237`, `MTProtoPage.jsx:20`, `TenantsPage.jsx:61,79`, `B2BAnalyticsPage.jsx:44`, `DirectRoutesPage.jsx:69-71,107,127`, `DirectRouteEditDialog.jsx:99,109`, `DirectRoutesImportDialog.jsx:75`, `DiscoveryDialog.jsx:73,124`. (`AuthContext.jsx:45` — проверка токена при старте, допустима.)

**Нативные `confirm()/prompt()`: 5 мест**
`DirectRoutesPage.jsx:105,125`, `MTProtoPage.jsx:70`, `PaymentOpsPage.jsx:91`, `PromocodesPage.jsx:106`.

**Тема (Linear dark lock):**
- `style.css:3-24` по-прежнему переопределяет токены: `--color-accent: #22d3ee` (циан вместо violet), `--color-text-primary/secondary` на slate-hex, `body { font-family: 'Inter' }` (style.css:21, без @layer — перебивает Geist из theme.css:33). Видно ли это на проде — не проверял визуально.
- `slate-*/gray-*`: 64 вхождения — `DirectRouteEditDialog` (14), `DirectRoutesImportDialog` (20), `DiscoveryDialog` (27), `StatusBadge` (3, плюс `green-/red-/yellow-/orange-500`).
- `shadow-*`: `CommandPalette, ConfirmDialog, CrudDialog, DirectRoute*Dialog ×3, ThemePicker, Toast, ui/Dialog, PaymentsPage` (10 файлов). Разрешён только `ui/Dialog` (shadow-2xl по правилу).
- `rounded-xl/2xl`: `ConfirmDialog, CrudDialog, ErrorBoundary, StatCard`.
- Старые токены (`surface-card`, `text-text-*`, `bg-brand`, `border-white/10`, `bg-red-600`): `LoginPage, ConfirmDialog, CrudDialog, ErrorBoundary, SearchPaginationBar, SettingCard, Toast, StatusBadge, StatCard`.
- `ThemePicker` предлагает светлую тему `notion` (ThemePicker.jsx:12), что противоречит theme-lock.
- Две системы модалок: `ui/Dialog` (новая) и `CrudDialog`/`ConfirmDialog`/три самописных `fixed inset-0` (старые).

**Английский в русском интерфейсе (выборочно):** «Devices», «Sync pending», «Email log», «Retry queue (dunning)», «Regenerate», «Secret», «Sponsor channel», «Edit/Del», «User ID/Username/User», «Upload/Download», «Connections», «CPU History», «Config version», «Applied/ACTIVE», «Pending/Succeeded/Failed/Canceled/Waiting for capture», `active/expired/cancelled/pending_renewal`, «Copied», «Click to copy», «Select a file».

**Жаргон по политике бренда (VLESS/Xray/Reality/SNI/Hysteria):** Автотюнер (почти весь), ServerDetailPage (VLESS Reality, HYSTERIA2:порт), Plans (VLESS/HYSTERIA2/XHTTP, Express, Mbps), Traffic Masking («SNI»), MTProto («fake-TLS SNI»), Settings («SNI Hysteria»).

**Мёртвый код:** `components/StatCard.jsx`; API-обёртки `resendPaymentNotifications`, `updateServer`, `fetchServerFiles`; бэкенд-маршруты без UI: `PATCH /servers/{id}`, `/servers/{id}/files`, `/servers/{id}/file-content`, `POST /payments/resend`, `/force-autorenew`, `/servers/{id}/metrics`.

---

## 25. Рекомендации

### Удалить / скрыть (владелец: серверы, нагрузка, пользователи/тарифы/платежи)

1. **SSH-ключи**: оставить только карточку «Ключ Hub» (перенести в мастер/«Серверы»); список приватных ключей удалить (💀).
2. **Релизы** — деплой приложений, не ежедневная задача: скрыть.
3. **Маскировка трафика**, **Прямая маршрутизация**, **MTProto**, **Автотюнер** — собрать в один раздел «Дополнительно»/«Для инженера»; Бамбук-карточку вынести на дашборд.
4. **B2B-аналитика** — слить в «Тенанты» как вкладку; оба скрыть, пока нет B2B-клиентов.
5. **Операции платежей («Дунинг»)** — слить в «Платежи» второй вкладкой «Не прошло продление».
6. **История подписок** — убрать как отдельную страницу (есть вкладка в шторке пользователя), либо оставить как «Истекают скоро».
7. **Трафик** — слить с «Пользователи» (колонка и вкладка уже есть) либо вкладка дашборда.
8. **Страница `/servers/:id`** — влить метрики (CPU/RAM/подключения) в карточку ноды (вкладка «Нагрузка») и убрать как отдельный экран.
9. **Входы** vs **Ноды → Резервные входы**: один раздел «Серверы» с вкладками «Выходы» / «Входы в РФ» (режим hub/relay/auto, DNS, учебная авария — подвкладка).
10. Убрать мёртвое: Settings → `hub_vless_port`, `b2b_price_*`, `app_build_*`, переключатель темы; EntriesPage → опция «домен — после раздела Домены»; хоткей-подсказки ⌘D/⌘U/⇧⌘Q; кнопку «⋯» в Users; `StatCard.jsx`; три неиспользуемых API-обёртки.

### Починить

1. **Админы**: `role_ids`→`role_keys` (строки), `key`→`role_key`, `permissions`→`permission_keys`, показывать `r.role_key`, отмечать роли админа по ключам (AdminsPage.jsx:81,88,113,122,226,262).
2. **Создание пользователя**: убрать форму (пользователей создаёт клиентское приложение) либо слать `user_id` (UsersPage.jsx:106).
3. **Колонка «План»**: `u.plan`→`u.current_plan` (UsersPage.jsx:244, UserDrawer.jsx:126-127); профиль: убрать правку username/пароля или реализовать на бэке; `telegram_id` отправлять числом (ProfileTab.jsx:291-294).
4. **Хоткеи**: в `useHotkey` не обрабатывать события из `input/textarea/select/contenteditable` и требовать отсутствие модификаторов; убрать «Enter» (UsersPage.jsx:147-152).
5. **Счёт тенанту**: путь `/b2b/create-invoice` вне префикса `/api/admin` в `apiFetch` → зарегистрировать маршрут и вызывать `apiFetch('/b2b/create-invoice')` либо поправить вызов (admin.js:320).
6. **Фильтры без бэка**: подписки `plan_id/status`, платежи `from/to`, аудит `admin/from/to` — реализовать (subscriptions_handlers.go:16, payments_handlers.go:17, audit_handlers.go:70) или убрать из UI.
7. **Журнал ServerDetail**: список сервисов привести к `nodeLogServices`; дефолт для relay не `heyearth-api`.
8. **MTProto**: перевести на `apiFetch`, не затирать host/port, «Regenerate» не писать в БД до «Сохранить» или передавать введённый домен.
9. **Дашборд**: ёмкость — по всем выходам (не `LIMIT 1`), константы 2 ядра/32 ТБ вынести в настройки; список здоровья и счётчики — `role='exit'` + relay отдельно, без archived.
10. **Платежи**: ручное создание/правка — `test=true` по умолчанию, писать аудит, запретить менять `status` у платежей ЮKassa; либо убрать кнопки.
11. **Сырые `fetch`** (15 мест) → `apiFetch`; **native confirm/prompt** (5) → `ConfirmDanger`/`Dialog`.
12. **Тема**: удалить `style.css:3-24`, заменить `StatusBadge`/`ConfirmDialog`/`CrudDialog`/три диалога DirectRoutes на `ui/*`, убрать светлые темы из `ThemePicker`.
13. **Релизы**: ошибки бэка отдавать JSON (releases.go `http.Error` → `writeAdminError`).
14. **Тарифы**: отфильтровать relay в списке серверов; оставить одно место редактирования привязки тариф↔сервер.

### Упростить (для нетехнического владельца)

1. Одно «Здоровье» на главной: выходы (статус обоих путей, CPU/RAM из node-agent), Hub, Бамбук, входы в РФ, последние алерты — ссылки на карточки. Сейчас это 4 страницы.
2. В списке нод — колонки «CPU/RAM» и «Подключения», индикаторы; карточку сократить до «Обзор / Нагрузка / Настройки / Опасная зона».
3. В мастере: страна — выпадающий список (флаг+название), подсказка «пароль root можно не вводить — Hub войдёт по ключу».
4. Бренд: «Бамбук/Берёза/Дуб/Пальма» оставить; VLESS/HYSTERIA2/XHTTP/SNI/Reality/Mux/Brutal убрать из видимых подписей (тултип «для инженера»).
5. Перевести остатки английского (список в §24), статусы подписок/платежей — русские подписи.
6. Поиск ⌘K — расширить: пользователи (UUID/ник), ноды (имя/IP), платёж (ID).
7. Settings: «Аккаунт (пароль, выход)», «Платежи (ЮKassa)», «Почта», «Юр. данные», «Уведомления»; остальное убрать.
