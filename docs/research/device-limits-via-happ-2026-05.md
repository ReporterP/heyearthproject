---
id: device-limits-via-happ-2026-05
created: 2026-05-26
---

# Research: Лимит количества устройств для happ-юзеров

## Вопрос

«Можем ли через happ отслеживать тип устройства? Ограничить — 2 телефона + 3 ПК, например. Если тип не определить — просто лимит количества.»

## Что happ присылает при fetch'е подписки

| Источник | Что есть | Reliability |
|---|---|---|
| **User-Agent header** | Содержит подстроку `happ` (мы уже используем для формата ответа), но **формат не документирован** — Happ-proxy явно позволяет провайдерам менять UA через `change-user-agent` и юзерам — вручную в настройках | ❌ Нельзя надёжно различить phone vs desktop |
| **Client IP** | Adress из TCP socket | 🟡 Mobile IPs меняются между Wi-Fi/4G, NAT юзеров за одним IP даёт false positives |
| **Sec-CH-UA-Platform header** | Modern client hint | ❌ happ — native app, не браузер, не присылает client hints |
| **Subscription URL path** | Единственное что мы контролируем — путь | ✅ Это базис для slot-based approach |

**Вывод:** через само присутствие happ-юзера определить тип устройства **нельзя надёжно**. Платформа = декларация юзера, а не sensor reading.

## Что уже есть в проекте

| Артефакт | Описание |
|---|---|
| `user_devices(user_id, device_id, platform, device_label, last_seen_at)` | Таблица заполняется только нашим Flutter app через gRPC RegisterDevice. **happ-юзеры там не появляются.** |
| `users.subscribe_token` | Один UUID на юзера. URL `/api/v1/user/subscribe/<token>` неограниченно работает на любом количестве устройств — лимита НЕТ |
| `Subscription-Userinfo` header | Reports cycle traffic + expire, **не device count** |

## Как делают конкуренты

### Mullvad VPN (industry standard)
- Каждое устройство получает **уникальный WireGuard config**, привязанный к slot
- Account слотов фиксировано **5** для всех (flat price model)
- В кабинете: список slots → можно revoke по имени
- Нет per-device-type лимита
- При попытке создать 6-й slot — «Удалите старый или get a second account»

### NordVPN / Surfshark
- Простой лимит «one-time use up to N devices»
- Detection через unique device-id в app (как у нас — gRPC-side)
- happ-аналоги (third-party subscription clients) НЕ отслеживаются → users with happ имеют unlimited devices, что ломает биллинг

### Vendor pattern — Per-Device Subscription URLs (Mullvad / AmneziaVPN)
1. Юзер создаёт slot в кабинете: «+ Добавить устройство»
2. Получает уникальный subscribe_url с per-slot токеном
3. Импортирует в happ ОДНОГО устройства
4. Server tracks `last_seen_at` per token
5. Slot count enforced на API level: `POST /devices` возвращает 409 если max reached

## Рекомендованный подход для HeYearth

### Architecture: «Slot-based subscription URLs» (как Mullvad)

```sql
CREATE TABLE user_subscription_slots (
    id            SERIAL PRIMARY KEY,
    user_id       INT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    slot_token    UUID NOT NULL UNIQUE,
    device_type   VARCHAR(20) NOT NULL,  -- 'phone' | 'desktop' | 'tablet' | 'tv' | 'other'
    label         VARCHAR(255) NOT NULL DEFAULT '',  -- юзерский nickname
    first_seen_ua TEXT NOT NULL DEFAULT '',
    first_seen_ip INET,
    last_seen_at  TIMESTAMPTZ,
    last_seen_ua  TEXT NOT NULL DEFAULT '',
    last_seen_ip  INET,
    fetch_count   INT NOT NULL DEFAULT 0,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX ON user_subscription_slots(user_id);
CREATE INDEX ON user_subscription_slots(slot_token);
```

### Plans schema extension

```sql
ALTER TABLE plans
  ADD COLUMN max_devices INT NOT NULL DEFAULT 5,
  ADD COLUMN max_phones INT,    -- NULL = no per-type limit
  ADD COLUMN max_desktops INT,
  ADD COLUMN max_tablets INT,
  ADD COLUMN max_tvs INT;
```

Если все `max_*` (кроме `max_devices`) = NULL → действует общий лимит. Если заданы — enforced per-type. Гибкость.

### Flow

1. **Юзер заходит в кабинет → раздел «Мои устройства»**
   - Видит список slots (label, type icon, last seen, last IP — masked)
   - Кнопка «+ Добавить устройство» (если не достигнут лимит)
2. **Выбирает тип** (Phone 📱 / Desktop 💻 / Tablet 📲 / TV 📺) и nickname (опционально)
3. **API создаёт slot:**
   - Проверяет `count_for_user_and_type <= plan.max_<type>` AND `total <= plan.max_devices`
   - Генерит UUID slot_token
   - Возвращает URL: `https://hiearth.ru/api/v1/user/subscribe/<slot_token>`
   - Опционально: `dt=phone` query-параметр (для analytics, не для security)
4. **Импорт в happ:** один URL → одно устройство
5. **При первом fetch'е:**
   - Server записывает `first_seen_ua` + `first_seen_ip`
   - Если эти fields уже заполнены и новый IP сильно отличается AND UA — может быть signal «slot used on other device» → log warning admin'у
6. **Каждый fetch:** обновляет `last_seen_at`, increments fetch_count
7. **Revoke:** DELETE slot → следующий fetch вернёт 404, happ потеряет подписку → no traffic

### Backward compatibility

- Legacy `users.subscribe_token` остаётся работать неограниченно
- Новые юзеры по умолчанию получают **1 slot** при регистрации (для max_devices=5 могут добавить ещё 4)
- В кабинете: opt-in переключатель «Использовать систему устройств» → старый URL отключается, юзер мигрирует через создание slots вручную

### Что мы достигаем по типам устройств

**Honest limitation:** тип устройства — это **декларация юзера**, не sensor measurement. Если юзер выберет «Phone» но импортирует URL в desktop happ — система НЕ заметит. UA-based detection ненадёжно (см. выше).

**Smart-mitigations:**

1. **First-fetch UA signature** — записываем UA при первом fetch'е. Если позже происходит fetch с радикально другим UA (например `happ-windows/1.0` → `happ-android/2.0`) — slot maybe shared. Не блок но flag для admin.
2. **Geographic-IP heuristic** — если slot объявлен Phone но first_seen_ip из datacenter range — подозрительно (фишинг/share). Опционально, требует MaxMind GeoIP2 база.
3. **Concurrent active sessions** — Xray gRPC stats умеет показывать сколько активных streams на конкретный UUID. Если slot объявлен «Phone» но видим 3 concurrent streams (PC + smartphone + tablet) — alert.

Эти три механизма не блокируют юзера, но дают админу видимость в bad behaviour.

### Subscription URL response при достижении лимита

При попытке fetch'ить slot который был revoked → 410 Gone с happ-deeplink на наш «нужно создать новый slot в кабинете» — happ просто перестаёт получать новые конфиги, текущая сессия продолжится до timeout (~60 sec на cycle).

## Эффект для биллинга

- Phase 1: opt-in, нет принудительной миграции, бесплатно для всех
- Phase 2: новые планы с дифференцированным `max_devices` (например free-trial = 1, socks-basic = 2, all-inclusive = 5, annual-premium = 10)
- Phase 3: разделение по типам — продвинутая монетизация: «Family plan» = 3 phone + 2 desktop + 1 TV

## Открытые вопросы (для brainstorming перед спекой)

1. **Принудительная миграция?** Или оставить legacy `users.subscribe_token` навсегда параллельно?
2. **Если slot revoked в момент когда happ держит активный QUIC stream — обрывать?** (Xray sync removed user from clients) или нет?
3. **Поделить max_devices по типам в planах ИЛИ один общий лимит?** YAGNI: начать с общего, добавить per-type если будет спрос на «Family plan».
4. **`first_seen_ua` — отображать в кабинете?** Pro: юзер видит «зашёл я или взломщик». Con: leak информации, если кабинет скомпрометирован.
5. **Slot label — required или optional?** Mullvad обходится без labels, slots безымянные. AmneziaVPN — labels.
6. **Auto-revoke inactive slots?** Если slot не fetch'ил подписку 90 дней — auto-delete. Освобождает квоту тихо.

## Sources

- [Happ App Management (User-Agent customization)](https://www.happ.su/main/dev-docs/app-management)
- [Mullvad VPN device limit 2026](https://www.wowtechub.com/blog/how-many-devices-can-you-use-mullvad-on-in-2026-full-limits-and-usage-guide/)
- [Mullvad device slot mechanics](https://www.acciyo.com/mullvad-vpn-device-limit-everything-you-need-to-know/)
- [Mullvad FAQ — devices](https://mullvad.net/en/help/faq)
- [User-Agent header / client hints](https://developer.mozilla.org/en-US/docs/Web/HTTP/Reference/Headers/User-Agent)
