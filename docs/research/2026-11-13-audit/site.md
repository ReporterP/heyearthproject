# Аудит сайта HiEarth (site/ + эндпоинты API) — 2026-11-13

Метод: чтение кода site/src (19 страниц, 12 компонентов, api/*.js, AuthContext, main.jsx) и обработчиков в
server/backend/cmd/api/*.go, internal/biz, internal/admin. Ничего в проекте не менялось.
Одну находку (регистрация) проверил живьём: поднял `vite` на :5199, подменил `fetch`, нажал кнопку, затем остановил сервер.
Статусы: ✅ работает / ⚠️ работает с изъянами / ❌ сломано / 💀 мёртвое. «(не проверено)» = выведено по коду, на проде не смотрел.
Пути ниже относительно `site/src/` (страницы — `pages/`) и `server/backend/` (`cmd/api/...`, `internal/...`).

## 0. Сверка прежних аудитов

| Утверждение | Итог |
|---|---|
| «Сырой fetch без 401, протухшая сессия даёт сырые ошибки» | Верно и хуже: проверка 401 в трёх страницах не сработает никогда (см. §1). Живая проверка: `/dashboard` показывает «invalid token format» и кнопку «Повторить». |
| «Цены B2B захардкожены и могут разойтись с `b2b_price_*`» | Неточно. Backend настроек `b2b_price_*` не читает вообще (grep по *.go: 0 вхождений). Настройки есть только в админке (`admin_web/src/pages/SettingsPage.jsx:242-247`) и ни на что не влияют. Числа 250/1000/100 зашиты в четырёх местах (§9). |
| «`happ-link` зависит от `crypto.happ.su`» | Верно, и зависимость блокирует кабинет: см. §5. |
| `site-redesign-audit.md` (Т-Банк в политике) | Исправлено, в PrivacyPage сейчас ЮKassa. |

## 1. Сквозное: авторизация, ошибки, 401

**❌ Проверка 401 на страницах не срабатывает.** `DashboardPage.jsx:248`, `ConnectionsPage.jsx:111`, `SettingsPage.jsx:55` ищут в тексте ошибки `'401'` или `'Unauthorized'`.
Backend отвечает `{"error":"invalid token format" | "token expired" | "session revoked or not found"}` (`internal/biz/user_session.go:82-119`), без этих слов.
Итог: клиент с протухшей/отозванной сессией видит сырую английскую ошибку + «Повторить». Лечит только перезагрузка: `AuthContext.jsx:16-21` при монтировании разлогинивает по 401 от `/user/profile`.
Остальные страницы (`PaymentsHistory`, `PaymentDetail`, `BusinessDashboard`, `ClaimCode`, `Payment`, `Checkout`) 401 не обрабатывают совсем.
- `PaymentsHistoryPage.jsx:62-65` глушит любую ошибку в «пусто»: просроченная сессия выглядит как «Нет активной подписки / Карты не сохранены / Платежей пока нет». Клиент решит, что потерял подписку.
- `ConnectionsPage.jsx:97` делает `getConnections().catch(() => null)`, затем `!canConnect` показывает «Нет активной подписки. Оформите подписку» (`:157+`). Backend специально отвечает 503 при сбое БД (`cmd/api/main.go:520-570`), чтобы не врать «нет доступа». Сайт эту защиту сводит на нет: платящий клиент в момент сбоя БД получает призыв оплатить заново.

**⚠️ Сбой БД = разлогин.** `extractUserFromToken` при ошибке чтения сессии (`SessionLookupError`) возвращает тот же 401, что и отозванная сессия. Различает их только `handleUserConnections` (`main.go:515`). `handleUserProfile` и остальные отдают 401, а `AuthContext.jsx:19` по 401 стирает токен. Одна секунда проблем с БД при открытии сайта — и клиента выкинуло на /login, а ID без пароля у него может быть не сохранён (см. §3).

**⚠️ Сырые английские тексты backend попадают в UI.** `jsonOrThrow` (`api/user.js:3-11`) пробрасывает `data.error` как есть:
- логин: `user is blocked` (остальные `password`/`not found` LoginPage перекладывает на русский, `LoginPage.jsx:44-54`);
- профиль: `username already taken`, `username can only contain letters...`, `password must be at least 4 characters`, `invalid current password` (`internal/biz/client_identity.go:124-195`);
- лимит 429: `too many requests` (`internal/middleware/ratelimit.go:189`), промокод/код ограничены 10 за 10 минут (`main.go:1002`);
- Dashboard, промокод: `invalid or expired code` и т.п.

**⚠️ Токен.** JWT в `localStorage` на 30 дней; «Выйти» (`SettingsPage.jsx:97`) только стирает его локально. Эндпоинта отзыва сессии нет (`user_sessions` отзывается только админкой/БД). Украденный токен живёт до конца срока.

**⚠️ Нет единого `apiFetch`.** `fetch` вызывается напрямую в `LandingPage`, `PaymentPage` (planы, proration), `ClaimCodePage`, `BusinessInvoicePage`, `ConnectPage`, `SiteFooter`, `Offer/Privacy/RefundPage`. Нет таймаутов и повторов.

## 2. Лендинг `/` — ⚠️

- Планы тянутся из `GET /api/v1/plans` (✅, `main.go:775`; поля `id, slug, name, description, price_rub, duration_days, traffic_limit_bytes, features(string JSON), direct_routing_enabled` совпадают, `internal/biz/plans.go:16-44`). Цены не захардкожены.
- **❌ Выдуманные цифры и отзывы.** `LandingPage.jsx:145-147`: «99.9% аптайм, <15 мс, 50+ локаций» зашиты константами. По CLAUDE.md локаций две (Hub + exit Амстердам), «50+» неправда (не проверено на проде, но противоречит архитектуре). `:36-38`: три отзыва с именами («Мария К.», «Дмитрий А.», «Иван С.»; «уже полгода», «перевёл всю команду») вымышлены. Это недостоверная реклама, юридический риск, а не просто косметика.
- **⚠️** `:196` «Оплата помесячно, отмена в любой момент», но тарифы приходят и на год, и на 7 дней; текст не зависит от данных. «Бесплатный старт, без карты» (`:325`) верен только если тариф free-trial активен; лендинг при этом фильтрует `price_rub > 0` (`:97`) и бесплатного тарифа не показывает. Регистрация триал сама не выдаёт (`RegisterAnonymousUser`, `client_identity.go:86`): клиент попадает в кабинет «Нет активной подписки» и должен сам дойти до /payment.
- **⚠️ «Выбрать» у тарифа** (`:241`) ведёт на `/payment` без выбранного тарифа. Гость уходит на `/login?redirect=/payment` (RequireAuth), а не на регистрацию; выбор теряется. Покупатель без аккаунта упирается в форму входа.
- **⚠️ Секция «Приложения»** (`:308-322`): четыре одинаковые плитки Windows/Android/iOS/macOS с одной иконкой ведут на `/register` или `/connections`. Ссылок на магазины нет, Happ не упомянут нигде на лендинге, шаг 02 «Установите приложение» без названия. `/connections` без подписки показывает «Нет активной подписки».
- **⚠️ SEO:** `index.html` содержит `<meta name="robots" content="noindex, nofollow">`. Маркетинговый сайт не индексируется. Если это сознательно (блокировки), то лендинг с отзывами и «Тарифы» остаются без органики. Есть `manifest.json` с `start_url:/dashboard`, sitemap и og-тегов нет.
- **⚠️ Мобильный:** три одновременных canvas-анимации (`AmbientFireflies` fixed + hero + CTA, `components/Fireflies.jsx`) с `ctx.shadowBlur = 11` на каждый кадр и каждую частицу. На слабых Android это нагрузка и батарея (вывод по коду, не измерял). Есть отключение для `prefers-reduced-motion`.

## 3. Регистрация `/register` — ❌ (критично)

**❌ Экран «Сохраните ваш ID» не показывается.** `RegisterPage.jsx:30` `useEffect → if (isLoggedIn) navigate('/dashboard')`; `:61` `login(data.token)` в обработчике регистрации. После `login` `isLoggedIn` становится true и эффект тут же уводит на кабинет. Проверено живьём: после клика по «Получить HiEarth ID» URL = `/dashboard` за <1.5 с, экран с ID, кнопки «Скопировать/Скачать файл», галочка «Я сохранил ID» и форма «логин+пароль» (`:150-290`) недостижимы. Они мёртвый код.
ID — единственный ключ входа, восстановить нельзя (так написано на самой странице и на LoginPage). Клиент увидит ID в кабинете только усечённым до 120 px (`DashboardPage.jsx:363`, кнопка копирования рядом) и целиком на /settings. Большинство об этом не догадается и потеряет аккаунт при очистке браузера.
Починка: не вызывать `login()` до подтверждения, либо убрать редирект из эффекта при `generatedId`.
- Бэкенд `POST /api/v1/auth/register` ✅ (`main.go:198`): `{user_id, token, profile}`; страница читает `data.id || data.user_id` ✅.
- ⚠️ Пароль/логин при регистрации: клиент проверяет только длину, сервер — ещё алфавит `[A-Za-z0-9_-]`; кириллический логин вернёт английскую ошибку (см. §1).
- ⚠️ Без капчи и привязки анонимные регистрации бесконечны (лимит 10/мин с IP). Триал ограничен `max_activations_per_user` на пользователя (`plans.go:697`), значит новый аккаунт = новый триал (бизнес-риск, не проверял настройки плана).
- ⚠️ `?redirect=` в /register не читается (`RegisterPage.jsx`), а LoginPage ссылается на `/register` без параметра (`LoginPage.jsx`). См. §12 (приглашения B2B).

## 4. Вход `/login` — ⚠️

- `POST /api/v1/auth/login {id_or_username, password}` ✅ совпадает (`main.go:245`).
- Поле пароля скрыто за «У меня есть пароль» (`LoginPage.jsx:128`); если пароль задан и ID введён без него, сервер вернёт `password required`, страница сама покажет поле (`:44-47`). Нормально.
- ⚠️ Вход по голому ID без пароля у аккаунтов без пароля: ID работает как единственный секрет (по замыслу). После утечки ID скачивание ключей и подписки открыто. Предлагать пароль стоит активнее (сейчас он скрыт в `/settings`).
- ⚠️ «Потеряли ID?» → личная почта `<личная почта владельца>` захардкожена (`LoginPage.jsx:171`), а не `legal_email` из настроек.
- ⚠️ `redirect` из query берётся без проверки (`:30` `navigate(redirectTo)`); внешний URL `pushState` отвергнет, open redirect нет, но путь вида `/foo` ведёт на 404.

## 5. Подключение: `/connect`, подписка, QR, deep link Happ — ⚠️ (внешняя зависимость)

Цепочка: кабинет → `GET /api/v1/user/subscribe-token` (auth) → ссылка `/connect?sub=<token>` → `GET /api/v1/user/happ-link/<token>` (без auth) → `happ://crypt5/...` через `https://crypto.happ.su/api-v2.php` (`internal/biz/happ_crypto.go:26,66`).
- ✅ Формат ответов совпадает: `subscribe_url`, `happ_link` (`subscription_handler.go:524,576`), QR строится на клиенте (`components/QRCode.jsx`, `qrcode` ленивым импортом), токен наружу не уходит.
- **❌ `crypto.happ.su` блокирует кабинет.** `handleGetSubscribeToken` синхронно зовёт `GetOrFetchHappCryptLink` с таймаутом 10 с (`subscription_handler.go:559`, `happ_crypto.go:74`), хотя `DashboardPage` и `ConnectionsPage` значение `happ_link` не используют. Они берут только `subscribe_url`. Результат неудачи не кэшируется. Если сервис недоступен, `Promise.all` в `DashboardPage.jsx:219-225` и `ConnectionsPage.jsx:95-100` ждёт все 10 с: клиент смотрит скелетон. Это повторяется при каждом открытии кабинета. Кэш в `users.happ_crypt_link` заполняется только после успеха.
- **⚠️ Утечка токена третьей стороне.** В `crypto.happ.su` отправляется полный `subscribe_url`, а токен в нём — bearer-секрет подписки (`handleSubscriptionLink` отдаёт конфиги без авторизации). Политика конфиденциальности (§5 Передача третьим лицам) говорит, что кроме ЮKassa данные никому не передаются (`PrivacyPage.jsx`). Противоречие.
- **⚠️ Кэш не инвалидируется.** `InvalidateHappCryptLink` нигде не вызывается (grep). Токен сейчас не ротируется, но если появится ротация, в `happ_crypt_link` останется старый.
- **⚠️ `/connect` без запасного варианта.** Если `happ-link` вернул 502 или `null`, страница пишет «Быструю ссылку подготовить не удалось» и предлагает QR/копирование (`ConnectPage.jsx:128-131`), но простой `happ://add/<subscribe_url>` без шифрования не пробует. Ручная вставка работает, а кнопка «Добавить в приложение» мертва.
- ⚠️ Автозапуск `window.location.href = happLink` без жеста пользователя (`:79`): на iOS Safari/встроенных браузерах без установленного Happ покажет системную ошибку; через 1.5 с страница предлагает установку (`:80-82`). Приемлемо.
- ⚠️ `/connect?sub=<token>` светит bearer-токен в URL (историю, логи nginx, Referer). По замыслу, но стоит поставить `Referrer-Policy: no-referrer`.
- ⚠️ Объявление в Happ (`buildSubscriptionAnnounce`, `subscription_handler.go:635`) ведёт на `https://hiearth.ru/checkout` («Подписка истекла. Продлите →»). Такого маршрута нет: есть только `/checkout/:paymentId` (`main.jsx:75`), `/checkout` попадает в React-404 «Тропа не найдена» (nginx отдаёт SPA через `try_files`). **Кнопка продления в самом Happ у всех истёкших клиентов ведёт на 404.** Нужен `/payment`.
- Приложения в `ConnectionsPage`/`ConnectPage`/Dashboard: Happ, V2RayTun, v2rayNG, Streisand (названия сторонних клиентов; «V2Ray» формально не из запрещённого списка, но «Clash» и «v2rayNG» видны пользователю).

## 6. Подключение `/connections` — ⚠️

- `GET /api/v1/user/connections` ✅ (`can_connect, vless_reality, amnezia_available, config_version`), `GET /user/awg-config` ✅ (`available, config_text, deeplink, endpoint, ip`), `GET /user/downloads` ✅ (`{downloads:{platform:{url,version}}}`).
- **⚠️ Сырая `vless://…` ссылка** показывается пользователю («Показать ссылку целиком», вкладка «Прокси-ссылка», `ConnectionsPage.jsx:307-318`). Это формально текст пользовательского интерфейса с запрещённым протоколом, а у `Пальма` — «AmneziaWG» и URL `amnezia-vpn/...` (`:345-354`, URL не виден, имя видно). Слова VPN/VLESS/Xray/Shadowsocks в тексте сайта нет (grep по src: только в именах переменных и в `vless://` ссылке).
- ⚠️ Плитка «Подключение» в кабинете отключена (`DashboardPage.jsx:433`) при `!hasActiveSub`, где `hasActiveSub` считается по `profile.current_plan` (подписка). Сотрудник B2B (доступ по `users.tenant_id`, `GetUserAccessState` разрешает подключение без личной подписки) видит «Нет активной подписки» и не получает ссылку (см. §12).
- ⚠️ Устаревший баннер `configUpdated` опирается на `localStorage`: у нового устройства или браузера сравнивать не с чем (в целом ок).
- ⚠️ MTProto-блок («Прокси для Telegram») дублируется на `/dashboard` (`ConnectionActions`) и `/connections` (`TelegramProxyButton`), по два запроса `getMtproto`. В `/dashboard` кнопки Telegram стоят прямо в «героях» рядом с главной кнопкой.

## 7. Тарифы и оплата `/payment`, `/checkout/:id` — ⚠️ / ❌ по мелочам

- Список тарифов и `proration-preview` ✅ совпадают (`main.go:775`, `payment_handlers.go:487`; `original_price, proration_credit, final_price, bonus_days, is_downgrade_blocked, early_renewal_days_threshold`).
- **⚠️ Автопродление: текст противоречит поведению.** Заголовок страницы: «Автопродление включено по умолчанию» (`PaymentPage.jsx:179`), оферта §4 тоже («включается по умолчанию»). На деле чекбокс «Сохранить карту» выключен (`:51`, `saveCard=false`), сайт всегда шлёт `save_card:false` (`api/user.js:93`), а backend для явного `false` не берёт `auto_renew_default` плана (`payment_handlers.go:179-182`). Без галочки карта не сохраняется и автосписания не будет. Это безопасно, но документ врёт. Либо включать по умолчанию (с согласием), либо править оферту и заголовок.
- **❌ «Почту не храним»** (`:342`). Email пишется в `payments.payer_email` (`payment_handlers.go:239`) и показывается в `/payments/:id` (`PaymentDetailPage.jsx`). Политика конфиденциальности §2 говорит «адрес почты собираем при оплате». Два текста противоречат друг другу.
- ⚠️ Промокод при оплате применяется «вслепую»: итог на экране без скидки, пишется «проверим при оплате» (`:330`), ошибку клиент узнаёт только после «Оплатить». Отдельного эндпоинта предпросмотра нет.
- ⚠️ Промокод в кабинете (`DashboardPage.jsx:282`) идёт на `/api/v1/payment/apply-promocode` ✅ (`{message}`; ответ без `profile`, `:291` проверка не нужна). Лимит 10 за 10 минут по IP общий с кодами компаний.
- ⚠️ Свободный тариф: `PaymentPage` показывает его и активирует через `create` с `price 0` (`payment_handlers.go:157`) ✅.
- **⚠️ Виджет ЮKassa.** `CheckoutPage.jsx:106-110` по событию `success` вызывает `checkout.destroy()` и ждёт `complete`, чтобы показать результат и уйти на кабинет. Если `complete` после `destroy` не приходит, клиент остаётся на пустой странице (не проверено; события виджета надо сверить с документацией ЮKassa). Надёжнее показывать успех и редирект на `success`.
- **❌ Тупик после обновления страницы.** `confirmation_token` лежит только в `sessionStorage` (`PaymentPage.jsx:109-110`). После F5/нового таба страница пишет «Сессия оплаты не найдена» и предлагает «Открыть этот платёж», но для embedded-платежа `confirmation_url` пуст (`payment_handlers.go:226-228`), и `PaymentDetailPage` не показывает «Продолжить оплату» (кнопка только при `confirmation_url`). Клиенту остаётся выбрать тариф заново, а висящий `pending` платёж остаётся в истории.
- **⚠️ Ложное «Платёж обрабатывается».** `payment_pending` кладётся в `sessionStorage` до открытия виджета (`PaymentPage.jsx:108`). Если клиент ушёл, не заплатив, то при следующем открытии кабинета во вкладке всплывёт попап «Платёж обрабатывается. Тариф «X»» (`DashboardPage.jsx:236-246`). И наоборот: успешная оплата → редирект → гонка с webhook → тоже «обрабатывается, обновите страницу чуть позже», автообновления нет. «Активировано» вычисляется по совпадению первого слова названия тарифа с `current_plan` (хрупко).
- ✅ Embedded-виджет подключается с `confirmation_token`; `return_url` `/dashboard?t=1`; ошибки показываются.
- ⚠️ Нет страницы результата оплаты, только попап в кабинете. Редирект-платёж (`confirmation_url` у B2B и донатов) возвращает на `/dashboard` или `/business` без статуса.
- ⚠️ Донат (`DashboardPage.jsx:306`) → `POST /payment/donate` ✅ `{confirmation_url}`; текст «Поможете выпустить нативное приложение для iOS/macOS» зашит как дефолт (`:495`), хотя Flutter-приложение заморожено (docs/research/2026-11-12-system-audit.md, п.13). Тексты из `donation_title/donation_text` (настройки) ✅.

## 8. Платежи `/payments`, `/payments/:id`, автопродление, пауза, карты, возврат — ❌ (возврат)

- `GET /user/payments`, `/user/payments/{id}`, `/user/cards`, `POST /user/auto-renew`, `pause/resume` ✅ существуют, поля совпадают (`UserPaymentDetail`, `UserSubscriptionDetail`, `handleUserPaymentDetail`).
- **❌ Возврат из кабинета не работает ни у одного платежа.** `PaymentsHistoryPage.jsx:354` показывает «Возврат» только для `status === 'completed'`; `POST /payment/refund` тоже принимает только `completed` (`cards_handlers.go:160`). YooKassa-платежи получают `succeeded` (`mapYookassaStatus`, `payment_handlers.go:439-447`; `biz.IsPaidPaymentStatus` в `internal/biz/refunds.go:36` знает оба, а ручка — нет). `completed` остался в унаследованных записях. Кнопки не видно, а вызов отклонён бы. Страница возврата (`RefundPage`) при этом описывает ручной возврат по почте, а заявка из кабинета только дописывает `payments.notes` без уведомления админа (`cards_handlers.go:167`). Решать: либо починить на `IsPaidPaymentStatus` и выдать админу очередь, либо убрать кнопку и модал.
- **⚠️ Пауза не сохраняет оплаченное время.** UI обещает «Приостановить — подписка сохранится, карта сохранится, списаний не будет» (`PaymentsHistoryPage.jsx:123,278`). `handlePauseSubscription` только ставит `paused_until` (`pause_handlers.go:81`), `valid_until` не сдвигается и при возобновлении не продлевается (grep по `paused_until`: cron лишь пропускает автосписание). Три месяца паузы съедают оплаченный период. Либо продлевать `valid_until`, либо убрать обещание.
- **⚠️ Удаление карты удаляет все карты** и выключает автопродление всех подписок. Сайт зовёт `DELETE /api/v1/user/cards/{id}` (`api/user.js:156`), а `handleDeleteCard` читает `payment_method_id` из тела, а не из пути (`cards_handlers.go:79-125`): пустое тело = «сделать неактивными все». Сейчас способ один, но сообщение «удалить «Карта ••1234»» неточно.
- ⚠️ «Отменить подписку» (`:134-160`) = выключить автопродление + удалить карты, ошибки удаления глотаются (`catch {}`), `setCards([])` ставится в любом случае. Реальной отмены/возврата нет (формулировка честная: «доступ сохранится до конца периода»).
- ⚠️ `hasActiveSub = !!currentPlan` (`:75`) не учитывает истёкшую подписку. `profile.current_plan` есть только у активной, так что ок, но дашборд и этот экран считают по-разному (на дашборде ещё `isExpired`).
- ⚠️ Автопродление: текст «Спишем … в день окончания» (`:231`), оферта «за 1 день до окончания» (`OfferPage.jsx` §4); cron берёт «в ближайшие 24 часа» (`subscription_cron.go:51-52`). Несовпадение формулировок.
- ✅ Тумблер автопродления отражает серверное состояние (`active.auto_renew`), сохраняется (`handleToggleAutoRenew`).
- ✅ Деталь платежа: ЮKassa-ID, чек 54-ФЗ, таймлайн. ⚠️ в таймлайне для `failed/canceled` заголовок «Возврат» выбирается по `status === 'refunded'` внутри ветки `isFailed` — недостижимо (`PaymentDetailPage.jsx:134`).
- ⚠️ `formatBytes` в `TrafficCard.jsx:10` показывает «GB/MB» латиницей, остальной сайт «ГБ/МБ».

## 9. Настройки `/settings` — ⚠️

- `PATCH /user/profile` ✅ (`username`, `new_password`, `current_password`); порядок «сперва пароль, потом логин» учтён на сервере (`main.go:340`).
- Имя без пароля серверу нельзя (`SetUsername` требует пароль, `client_identity.go:139`), страница это поясняет только ошибкой на русском.
- ⚠️ Нет списка устройств/сессий, хотя таблицы `user_devices`/`user_sessions` есть. Эндпоинта `/user/devices` нет вообще. «Устройства» как функция кабинета отсутствуют (лимит устройств в тарифах тоже не показывается).
- ⚠️ Ошибка загрузки профиля не показывается (`SettingsPage.jsx:52-60`: не 401 → пустая форма с «—»).
- ✅ Копирование ID, подтверждение выхода.

## 10. Бизнес: `/business`, `/business/signup`, `/business/invoice` — ⚠️/❌

- **⚠️ Цены захардкожены в четырёх местах и не из backend:** `BusinessPage.jsx:33-35` (250/1000/100), `BusinessSignupPage.jsx:24-26,138,187-189` (расчёт), `internal/admin/tenants_handlers.go:207-217` (`PublicTariffCalculateHandler`, на сайте не используется), `internal/biz/billing.go:26-32` (реальное месячное списание). Настройки `b2b_price_*` в админке (`admin_web/.../SettingsPage.jsx:242-247`) 💀 мертвы: backend их не читает. Админ может «изменить цену» и ничего не поменяется.
- **⚠️ Предрасчёт игнорирует «Premium +50 ₽/польз.» и «Unlimited 20 000»**, которые есть в `PublicTariffCalculateHandler`. Реальное списание (`billing.go`) их тоже не знает (только `plan == "super"` = 20 000). Три формулы расходятся.
- **⚠️ `POST /api/public/create-tenant`** (`internal/admin/tenants_handlers.go:562`): ✅ путь и поля (`company_name, email, max_users, max_servers, use_our_servers`) совпадают, ответ `{tenant_id, message, claim_url?}`. Замечания:
  - публичный, `Access-Control-Allow-Origin: *`, лимит общий публичный;
  - если e-mail совпадает с `users.email`, этот чужой пользователь молча становится админом новой компании (`:634-647`) — спам и подмена кабинета, а наличие `claim_url` в ответе позволяет угадать, есть ли такой аккаунт (перечисление e-mail, не проверено насколько `users.email` заполнен);
  - `max_users`/`max_servers` без верхней границы;
  - страница обещает «Письмо отправлено на {email}» (`BusinessSignupPage.jsx:101`), при отключённом SMTP письмо не уходит (`internal/email/client.go`, «Send возвращает nil»), сайт об этом не узнаёт. Показанная `claim_url` остаётся единственным способом активации, и экран её показывает только когда e-mail новый.
- **⚠️ Маркетинг: «Коды доступа для сотрудников без регистрации»** (`BusinessPage.jsx:28`) и «сотрудник перейдёт по ней и получит доступ без отдельной регистрации» (`BusinessDashboardPage.jsx:276`) неверны: приглашение работает только для залогиненного аккаунта (`ClaimCodePage.jsx:27`), нужен собственный HiEarth ID.
- **⚠️ «Без предоплаты … сразу получите доступ в панель»** (`BusinessPage.jsx` CTA). Тенант создаётся `inactive`, до первого платежа не активен (`BusinessDashboardPage.jsx:236`).
- **⚠️ `/business/invoice`:** `POST /api/v1/business/create-invoice` ✅ (`inn, company_name, amount, email, description, return_url` → `confirmation_url, amount, email, company_name`; лимит 10/час, потолок 1 000 000 ₽, `invoice_handlers.go`). Но это не счёт на оплату с реквизитами и безналом, а ссылка на оплату картой в ЮKassa с чеком на юрлицо. Страница обещает «Счёт … чек-накладная по 54-ФЗ». Юрлицо, ожидающее платёжное поручение, не получит реквизитов. Нужно либо переименовать («Ссылка на оплату»), либо добавить безнал.
- ⚠️ Публичная форма автоматически создаёт тенанта на любой ИНН (`resolveTenantForInvoice`), лимит 10/час на IP — засорение таблицы возможно.
- ⚠️ Ссылка на `/business/dashboard` **нигде не размещена**: ни в шапке, ни на /business, ни в кабинете (grep по `business/dashboard`: только роут и редиректы). Админ компании должен знать адрес.
- ✅ `POST /business/topup-init`, `GET /business/me`, `invite`, `members` существуют, поля совпадают (`b2b_handlers.go`).

## 11. Бизнес-кабинет `/business/dashboard` — ⚠️

- Данные: `tenant{name,inn,plan,status,balance,max_users,...}, role, members[{user_db_id,user_id,username,email,role,joined_at}], transactions, invoices` ✅ совпадают с JSON-тегами.
- **⚠️ «Тариф: basic»** (`:156`, `t.plan` сырое значение) и «Списание {max_users} мест × тариф «{plan}» каждое 1-е число» (`:226`). Реальная сумма (`users×250+1000+…`) в кабинете не показывается, клиент не видит, сколько спишется.
- **⚠️ Места не ограничены:** индикатор «N из M мест» косметический. `RedeemTenantClaimCode` (`internal/biz/payments.go:456-495`) не сверяет `max_users` и не смотрит статус тенанта; `isMemberOfTenant` (`client_identity.go:326`) считает доступом любое `tenant_id > 0`, в том числе у `inactive`/`suspended` компании. Приостановка за неуплату поэтому не отключает уже привязанных сотрудников (по коду, не проверено на проде).
- ⚠️ 401/ошибки: `load()` ловит только `'no B2B membership'` (`:73`), всё остальное — карточка «Ошибка» с сырым английским.
- ⚠️ Нет кнопки оплатить по ожидающему счёту (`invoices[].status==='pending'`, ссылка на оплату не отдаётся); `handleTopup` при пустом `confirmation_url` молча ничего не делает (`:98-100`).
- ⚠️ Нет разделов «Подключение» для сотрудников и подсказки «куда идти».

## 12. Приглашение `/claim/:code` — ❌

Цепочка владельца: «Пригласить» → `claim_url` (`/claim/tn-r-user-…`, 7 дней, `b2b_handlers.go:338`) → сотрудник.
1. **❌ Новичок без аккаунта теряет приглашение.** `ClaimCodePage.jsx:27` уводит гостя на `/login?redirect=/claim/…`, но на странице входа единственный путь для нового человека — «Зарегистрироваться» → `/register`, который `redirect` не наследует. Регистрация ведёт на `/dashboard`, ссылка потеряна (см. §3: ещё и не показывается ID).
2. **❌ После успешного `redeem` роль `user` (дефолтная в приглашении) попадает на ошибку.** `ClaimCodePage.jsx:44` → `/business/dashboard?welcome=1`, а `handleBusinessMe` для `role == user` отвечает 403 «no dashboard access for this role» (`b2b_handlers.go:155-159`). Страница показывает «Ошибка: no dashboard access for this role» с кнопкой «Повторить». Параметр `welcome` нигде не читается (grep). Самая частая роль приглашённых заканчивается красной ошибкой.
3. **❌ Сотрудник без личной подписки не видит ссылку подключения.** Backend даёт `can_connect=true` по `users.tenant_id`, а кабинет рисует «Нет активной подписки / Оформить подписку» и блок подключения скрыт (`DashboardPage.jsx:266`, зависит от `profile.current_plan`).
- ✅ Подтверждение «Присоединиться?» перед привязкой, ✅ `POST /payment/redeem-code {code}` существует (`payment_handlers.go:574`, лимит 10/10 мин).

## 13. Юридические страницы — ⚠️

- `/offer`, `/privacy`, `/refund` ✅ в роутере и в подвале; данные оператора из `GET /api/v1/legal-info` ✅ (`legal_company_name, legal_address, legal_email, legal_phone, legal_inn`).
- ⚠️ Если `legal_address` в настройках пуст, в оферте и политике остаётся «Указывается по запросу» (`OfferPage.jsx`, `PrivacyPage.jsx`), а ИНН просто не выводится. Для публичной оферты это неполные реквизиты (какие значения на проде, не проверял).
- ⚠️ «Дата обновления: 07 апреля 2026 г.» во всех трёх (захардкожено). С тех пор менялись: входы (relay), Telegram-прокси, платежи.
- **❌ Возврат §5** (`RefundPage.jsx:53`): автопродление отключается «в разделе «Настройки»». Такого переключателя в `/settings` нет, он в «Оплата и подписка» (`/payments`).
- ⚠️ Политика §2/§5: не упомянут `crypto.happ.su` (получает ссылку подписки) и запись email в `payments` при отсутствии оплаты через счёт; §4 в публичной политике хвастает «шифрованием паролей SSH AES-256-GCM» и «mTLS» — внутренняя инфраструктура, лишнее для политики конфиденциальности. §6 «хранятся … 30 дней после окончания, после чего могут быть удалены»: задачи очистки в коде не нашёл (не проверено).
- ⚠️ `OfferPage` ссылается на возврат через `<a href="/refund">` (полная перезагрузка вместо `<Link>`).
- ✅ ЛКС: TOC `LegalToc` работает, мобильный вид адекватный.

## 14. Поддержка / контакты — ❌ (нет)

Страницы поддержки, FAQ-раздела, контактов нет. Единственный канал — личная почта `<личная почта владельца>`: захардкожена как запасная в `SiteFooter.jsx:15`, `Offer/Privacy/RefundPage`, `LoginPage.jsx:171`, `DashboardPage.jsx:584` и как дефолт backend (`main.go:797`). Telegram/чата нет, формы обращения нет. Платящий клиент с неработающей подпиской видит «Если что-то не работает… напишите на почту».

## 15. 404 — ✅

`NotFoundPage` (`main.jsx:34`) есть, ведёт на `/`. Но он же показывается для `/checkout` из объявления Happ (§5).

## 16. Мобильный вид (по коду)

- ✅ Тап-цели 44 px у кнопок, `safe-area-inset-bottom` в `PaymentPage` и дашборде, липкая нижняя панель «Оплатить» (`PaymentPage.jsx:416-428`), шапка с бургером, сетки `sm:`/`lg:` переходят в одну колонку.
- ⚠️ Анимации (см. §2), на «Пальме» длинный QR 168 px с большим конфигом (`awg.config_text` QR может не прочитаться, ограничение QR ~2,9 КБ; конфиг с ключами + CPS обычно 1,5-2 КБ, не проверено).
- ⚠️ Нет `Referrer-Policy`/`X-Frame-Options`/CSP в шаблоне nginx для сайта (`cmd/deploy/main.go:995-1006`, только `no-cache` для index.html и nosniff для geoip). Кабинет можно встроить в iframe. Реальный конфиг на проде мог отличаться (не проверено).

## 17. Таблица эндпоинтов (сайт → backend)

| Вызов | Метод | Backend | Статус |
|---|---|---|---|
| `/api/v1/auth/register` | POST | `main.go:198` | ✅ (но UI пропускает экран ID) |
| `/api/v1/auth/login` | POST | `main.go:245` | ✅ |
| `/api/v1/user/profile` | GET/PATCH | `main.go:303` | ✅ |
| `/api/v1/user/subscription` | GET | `main.go:359` | ✅ `{items[]}` |
| `/api/v1/user/payments`, `/{id}` | GET | `main.go:378,401` | ✅ |
| `/api/v1/user/connections`, `/awg-config`, `/downloads`, `/traffic`, `/mtproto` | GET | `main.go:509…` | ✅ |
| `/api/v1/user/subscribe-token` | GET | `subscription_handler.go:524` | ⚠️ блокируется `crypto.happ.su` |
| `/api/v1/user/happ-link/{token}` | GET | `subscription_handler.go:576` | ⚠️ внешняя зависимость |
| `/api/v1/plans`, `/legal-info`, `/app/donation-settings` | GET | `main.go:1121-1123` | ✅ |
| `/api/v1/payment/create`, `/apply-promocode`, `/donate`, `/proration-preview` | POST/GET | `payment_handlers.go` | ✅ |
| `/api/v1/payment/refund` | POST | `cards_handlers.go:126` | ❌ только `completed` |
| `/api/v1/payment/redeem-code` | POST | `payment_handlers.go:574` | ✅ |
| `/api/v1/user/cards`, `/cards/{id}`, `/auto-renew`, `/pause-subscription`, `/resume-subscription` | GET/DELETE/POST | `cards_handlers.go`, `pause_handlers.go` | ⚠️ см. §8 |
| `/api/v1/business/me`, `/topup-init`, `/invite`, `/members/{id}`, `/create-invoice` | GET/POST/PATCH/DELETE | `b2b_handlers.go`, `invoice_handlers.go` | ✅ |
| `/api/public/create-tenant` | POST | `internal/admin/routes.go:473` | ⚠️ §10 |
| `/api/public/tariff/calculate` | POST | `routes.go:474` | 💀 сайт не вызывает |
| `/api/v1/payment/check-access`, `/tenant-topup` | POST | `main.go:1063,1065` | 💀 сайт не вызывает (`tenant-topup` дублирует `business/topup-init`) |
| `/api/v1/user/subscribe/{token}` | GET | `subscription_handler.go:54` | ✅ для Happ |

Не подключено к роутам сайта: маршрутов без страниц и страниц без маршрутов нет (все 19 файлов подключены в `main.jsx`). Ссылок на `/business/dashboard` и `/checkout` (без id) в интерфейсе нет (последний ведёт Happ).

---

## УДАЛИТЬ

1. Экран «Аккаунт создан» с галочкой и опциональной формой логина в `RegisterPage.jsx:150-290`, если не будет починен редирект (сейчас мёртвый код). Лучше починить, а не удалять (см. «починить» №1).
2. `payment_success_plan` в `sessionStorage` (`CheckoutPage.jsx:101`): пишется, нигде не читается.
3. Параметр `?welcome=1` в `ClaimCodePage.jsx:44` (не читается).
4. `/api/public/tariff/calculate`, `/api/v1/payment/check-access`, `/api/v1/payment/tenant-topup`: сайт не использует. Убрать вместе с «Premium/Unlimited» из тарификатора, раз `billing.go` их не знает.
5. Настройки `b2b_price_*` в админке (`SettingsPage.jsx:242-247`) или подключить к backend: пока вводят в заблуждение.
6. Выдуманные блоки лендинга: три отзыва (`LandingPage.jsx:36-38`) и счётчики «99.9% / <15 мс / 50+» (`:145-147`).
7. Дубль MTProto-блока: оставить либо в `DashboardPage.ConnectionActions`, либо `TelegramProxyButton` на `/connections`.
8. Мёртвая ветка `status === 'refunded'` внутри `isFailed` в `PaymentDetailPage.jsx:134`.
9. Лишний прямой вызов `crypto.happ.su` из `handleGetSubscribeToken` (кабинет не использует `happ_link`).

## ПОЧИНИТЬ (по убыванию важности)

1. **Регистрация теряет ID** (`RegisterPage.jsx:30,61`): не логинить до подтверждения или не редиректить при `generatedId`. Пробросить `?redirect=` в `/register` и из LoginPage.
2. **Объявление Happ ведёт на 404** (`subscription_handler.go:653-654`): `/checkout` → `/payment` (или добавить роут `/checkout`).
3. **Возврат из кабинета мёртв** (`cards_handlers.go:160`, `PaymentsHistoryPage.jsx:354`): использовать `biz.IsPaidPaymentStatus`; решить, нужна ли заявка из кабинета.
4. **Кабинет ждёт `crypto.happ.su` до 10 с** (`subscription_handler.go:559`): убрать из `/subscribe-token`, на `/connect` дать запасной `happ://add/<url>`, кэшировать неудачи; упомянуть сторонний сервис в политике или перейти на локальную генерацию.
5. **Единый `apiFetch`**: 401 по `res.status`, а не по тексту; 503 «временно недоступно» без «Нет активной подписки»; русские сообщения вместо сырых `invalid token format`/`user is blocked`/`too many requests`; backend: `SessionLookupError` → 503 во всех `/user/*`, чтобы `AuthContext` не разлогинивал при сбое БД.
6. **B2B-сотрудник**: после `redeem` вести роль `user` в `/dashboard`, а не на 403; `profile`/`can_connect` для членов тенанта показывать блок подключения; проверять `max_users` и статус тенанта при `redeem` и при доступе.
7. **Тексты, расходящиеся с поведением:** «Автопродление включено по умолчанию» (`PaymentPage.jsx:179`, оферта §4), «Почту не храним» (`:342`), «подписка сохранится» при паузе (либо продлевать `valid_until`), «раздел «Настройки»» в `RefundPage.jsx:53`, «без регистрации» (B2B, `BusinessPage.jsx:28`, `BusinessDashboardPage.jsx:276`), «Оплата помесячно» (`LandingPage.jsx:196`), «Счёт на оплату» (`/business/invoice`).
8. **Embedded-платёж:** после обновления страницы дать продолжить (сохранять токен на сервере или создавать новый платёж), `payment_pending` не ставить до подтверждённого старта оплаты, поллить статус платежа вместо «обновите страницу позже»; на `success` показывать результат (`CheckoutPage.jsx:107`).
9. **Цены B2B** взять из одной настройки/эндпоинта: `billing.go`, предрасчёт, лендинг B2B.
10. **`DELETE /user/cards/{id}`** (`cards_handlers.go:79`): читать id из пути и удалять одну карту.
11. **Публичный `create-tenant`:** не продвигать чужой `users.email`, ограничить `max_users/max_servers`, не раскрывать наличие аккаунта, капча/лимит; честно сообщать, ушло ли письмо.
12. Контакты поддержки в одной настройке (не личная почта в коде в 7 местах), добавить страницу «Помощь/Контакты».
13. Ссылка на `/business/dashboard` из шапки/кабинета для членов тенанта.
14. `noindex` в `index.html` пересмотреть (оставить только для кабинета); обновить даты на правовых страницах; заполнить `legal_address/legal_inn`.

## УПРОСТИТЬ

1. Одна точка загрузки кабинета: Dashboard, Connections и Payments независимо грузят профиль/подписку/ссылку (`Promise.all` по 4-5 запросов на каждую страницу). Общий контекст `useAccount()` с кэшем и единой обработкой ошибок.
2. Три копии `copyToClipboard`, три копии `formatTraffic/formatDuration`, две копии `strengthOf` (Register и Settings), два `Empty`, два `ModalOverlay`: вынести в `lib/`.
3. Лендинг-«приложения» и шаги: заменить плитки на два понятных шага «Установите Happ → добавьте подписку» со ссылками на магазины.
4. Пауза/отмена/удаление карты: оставить два действия — «Автопродление вкл/выкл» и «Удалить карту»; «Отменить подписку» и пауза на 1/2/3 месяца дублируют друг друга и обещают то, чего не делают.
5. `ClaimCodePage` + `/claim` + `redeem-code` + «код компании» в `create-tenant`: один путь для администратора и сотрудника (`claim_url` + регистрация с возвратом).
6. Три конкурирующих способа «показать подключение» (кнопка «Добавить в приложение», ссылка на кабинете, вкладки на `/connections`) свести к одной странице `/connect`.
7. Анимации: один `Fireflies` на страницу или совсем выключить на мобильных/`pointer: coarse`.
