# Подпроект 1 «Стабилизация» — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Убрать массовые разрывы соединений (шторм рестартов xray), утечки трафика через RU IP Hub и «тихие» аварии; ускорить канал Hub→exit; навести эксплуатационную гигиену (бэкапы, fail2ban, журналы) и алерты в Telegram.

**Architecture:** Изменения в Go-бэкенде `server/backend` (генерация конфига xray в `cmd/api/xray_sync.go`, health-check, новые модули алертов/мониторинга в `cmd/api`), миграции PostgreSQL, admin_web (страница настроек алертов), код деплоя/провижининга (`cmd/deploy`, `internal/admin/servers_handlers.go`). Живые изменения пользователей — через gRPC API xray (HandlerService/RoutingService) вместо `systemctl restart`.

**Tech Stack:** Go 1.2x (`database/sql` + `lib/pq`, raw SQL), Xray-core 26.3.27, Hysteria 2.x, PostgreSQL 16, React 19 + Tailwind v4 (admin_web, Linear-theme-lock из CLAUDE.md), systemd, nginx, ufw.

**Spec:** `docs/superpowers/specs/2026-10-11-vpn-stabilization-design.md` · **Research:** `docs/research/2026-10-11-vpn-stability-research.md` · **Beads epic:** `heyearthproject-bfs4` · **Branch:** `vpn/stabilization`

---

## Соглашения для всех задач

- **Верификация.** В проекте нет тестового фреймворка; новые чистые функции покрываем Go-тестами (`go test ./cmd/api/ -run <Name> -v`), остальное — evidence-командами на проде. Каждая задача заканчивается: `cd server/backend && go build ./... && go vet ./cmd/api/` → деплой → проверка.
- **Деплой бэкенда:** `cd server/backend && go build ./... && go run ./cmd/deploy --ssh 'systemctl stop heyearth-api && rm -f /opt/heyearth/mainserver/heyearth-api' && go run quick_deploy.go`, затем `go run ./cmd/deploy --ssh 'systemctl is-active heyearth-api xray nginx'`. Admin web: `go run ./cmd/deploy --admin-web`. `DEPLOY_HOST` в `server/.env` = `138.16.181.225` (новый IP Hub). SSH нестабилен — при «connection lost» повторить.
- **Доступ к проду, если сеть режет порт 22:** SSH через локальный SOCKS Happ: `ssh -o "ProxyCommand=/mingw64/bin/connect.exe -S 127.0.0.1:10808 %h %p" root@138.16.181.225`.
- **Коммиты:** английский, conventional (`fix(xray): ...`), последняя строка тела — `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`, в теле строка `Beads: <id>`.
- **Пользовательские строки** — по-русски, без слов VLESS/Xray/VPN (см. CLAUDE.md «Branding»).

## Общие интерфейсы (используются несколькими фазами — имена не менять)

```go
// cmd/api/alerts.go (создаётся в Task 2)
type AlertLevel string
const (
	AlertWarn     AlertLevel = "warn"
	AlertCritical AlertLevel = "critical"
)
// RaiseAlert отправляет алерт один раз на key; повтор — не чаще cooldown (30 мин).
func RaiseAlert(key string, level AlertLevel, title, details string)
// ResolveAlert шлёт «восстановлено», только если key был поднят.
func ResolveAlert(key, message string)

// cmd/api/xray_restarts.go (создаётся в Task 3)
// recordXrayRestart вызывается после каждого успешного `systemctl restart xray`;
// > 10 за скользящий час → RaiseAlert("xray-restart-storm", AlertCritical, ...).
func recordXrayRestart()
```

Настройки (`app_settings`): `alert_telegram_bot_token`, `alert_telegram_chat_id`, `auto_tuner_enabled`.

## Порядок выполнения

Task 1 (хотфикс) → Tasks 2–3 (алерты/мониторинг) → Tasks 4–6 (маршрутизация без утечек, health-check, мастер-UUID) → Task 7 (горячие изменения через API xray) → Tasks 8–10 (чистка, эксплуатация, Hysteria + бенчмарк).

---

### Task 1: Хотфикс шторма рестартов — детерминированный порядок клиентов

**Beads:** `heyearthproject-0z3x`

**Files:**
- Modify: `server/backend/internal/biz/user.go` (`ListActiveVlessClients`, ~строки 137–175)

Причина: `GROUP BY` без `ORDER BY` → PostgreSQL отдаёт строки в произвольном порядке → конфиг xray «меняется» каждую синхронизацию → `systemctl restart xray` ~1 000 раз/сутки (замер 2026-10-11). Остальные запросы, участвующие в сборке конфига, уже с `ORDER BY` (проверено: `GetPlanIDsWithDirectRoutes`, `GetDirectRouteServicesForPlan`, `ListActiveTrafficMaskingRules`, `ListAllExitNodes`, `ListHealthyExitNodes`); `encoding/json` сортирует ключи map.

- [ ] **Step 1: Зафиксировать базовую метрику (RED)**

Run (на Hub): `journalctl -u xray --since "-1h" --no-pager | grep -c "Xray 26.3.27 started"`
Expected: двузначное число (≈ 35–45 за час) — это и есть дефект.

- [ ] **Step 2: Детерминированный порядок в запросе клиентов**

В `ListActiveVlessClients` заменить конец SQL:

```go
		GROUP BY u.vless_uuid, u.user_id, u.id
		ORDER BY u.user_id
	`)
```

- [ ] **Step 3: Детерминированный порядок серверов тарифа**

В том же цикле:

```go
			srvRows, srvErr := db.DB.Query("SELECT server_id FROM plan_servers WHERE plan_id = $1 ORDER BY server_id", c.PlanID)
```

- [ ] **Step 4: Сборка**

Run: `cd server/backend && go build ./... && go vet ./internal/biz/`
Expected: без вывода, exit 0.

- [ ] **Step 5: Деплой бэкенда** (см. «Соглашения»), затем `systemctl is-active heyearth-api xray`
Expected: `active` ×2.

- [ ] **Step 6: Проверка (GREEN)** — через 60 минут после деплоя:

Run: `journalctl -u xray --since "-1h" --no-pager | grep -c "Xray 26.3.27 started"` и `journalctl -u heyearth-api --since "-1h" --no-pager | grep -c "Конфиг обновлён"`
Expected: ≤ 2 (рестарты только при реальных изменениях: новая/истёкшая подписка, квота). Через сутки: ≤ ~30 (дальнейшее снижение — Task 7).

- [ ] **Step 7: Commit**

```bash
git add server/backend/internal/biz/user.go
git commit -m "fix(xray): deterministic client order — stop ~1000 xray restarts/day" -m "ListActiveVlessClients used GROUP BY without ORDER BY, so row order changed whenever traffic stats rewrote user_subscriptions; the generated config differed every sync and SyncXrayConfig restarted xray, dropping all users' connections." -m "Beads: heyearthproject-0z3x" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---
### Task 2: Алерты в Telegram — ядро, журнал событий, админка

**Beads:** `heyearthproject-bfs4`

**Files:**
- Create: `server/backend/migrations/000055_alert_events.up.sql`, `server/backend/migrations/000055_alert_events.down.sql`
- Create: `server/backend/cmd/api/alerts.go` (AlertLevel, RaiseAlert/ResolveAlert, менеджер с dedup/cooldown, отправка в Telegram, журнал в БД)
- Create: `server/backend/cmd/api/alerts_test.go`
- Create: `server/backend/cmd/api/exit_socks.go` (локальный SOCKS туннеля exit — нужен и алертам, и Task 3)
- Create: `server/backend/cmd/api/alerts_admin.go` (admin API: настройки, тест, журнал)
- Modify: `server/backend/cmd/api/main.go` (регистрация маршрутов перед `admin.RegisterRoutes()`)
- Modify: `server/backend/internal/admin/settings_handlers.go` (`ListSettingsHandler` — не отдавать токен)
- Create: `admin_web/src/pages/AlertsPage.jsx`
- Modify: `admin_web/src/api/admin.js`, `admin_web/src/main.jsx`, `admin_web/src/components/Sidebar.jsx`, `admin_web/src/components/commands.js`, `admin_web/src/i18n/ru.js`, `admin_web/src/i18n/en.js`

Решения: свободный номер миграции — `000055` (последняя в репо `000054_mtproto_fronting_domain`; если к моменту выполнения номер занят другой задачей — взять следующий свободный и переименовать оба файла). `cmd/api` не имеет `init()` с БД (единственный `init()` — в `internal/yookassa`, без БД), поэтому тесты чистой логики лежат в `cmd/api/*_test.go`. Admin-хендлеры живут в `cmd/api` (им нужен отправитель из `package main`) и закрываются `admin.RequirePermission` — так же, как `/api/admin/force-autorenew`. Права — существующие `settings.read`/`settings.write`. Семантика: первый `RaiseAlert` по ключу → сообщение; пока ключ активен — critical повторяется не чаще 30 мин, warn не повторяется до восстановления; `ResolveAlert` шлёт «восстановлено» только если по ключу ушло сообщение; повторный подъём ключа в течение 30 мин после последнего сообщения (флаппинг) подавляется до истечения cooldown. Состояние восстанавливается из `alert_events` после рестарта API (нет дублей на каждом деплое). Ошибки Telegram никогда не содержат токен (URL с `/bot<token>/` вычищается).

- [ ] **Step 1: Миграция журнала алертов**

`server/backend/migrations/000055_alert_events.up.sql`:

```sql
-- Operational alert journal (Telegram alerts), subproject 1 «Стабилизация», Task 2.
-- One row per state transition / delivery attempt; also replayed on API start to
-- restore which alerts are active (no duplicate messages after every deploy).
CREATE TABLE IF NOT EXISTS alert_events (
    id          BIGSERIAL PRIMARY KEY,
    alert_key   TEXT        NOT NULL,
    level       TEXT        NOT NULL CHECK (level IN ('warn', 'critical')),
    kind        TEXT        NOT NULL CHECK (kind IN ('raise', 'repeat', 'suppressed', 'resolve', 'cleared', 'test')),
    title       TEXT        NOT NULL DEFAULT '',
    details     TEXT        NOT NULL DEFAULT '',
    delivered   BOOLEAN     NOT NULL DEFAULT FALSE,
    error       TEXT        NOT NULL DEFAULT '',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_alert_events_created_at ON alert_events (created_at DESC);
CREATE INDEX IF NOT EXISTS idx_alert_events_key_id     ON alert_events (alert_key, id DESC);

INSERT INTO app_settings (key, value) VALUES
    ('alert_telegram_bot_token', ''),
    ('alert_telegram_chat_id', '')
ON CONFLICT (key) DO NOTHING;
```

`server/backend/migrations/000055_alert_events.down.sql`:

```sql
DROP TABLE IF EXISTS alert_events;
DELETE FROM app_settings WHERE key IN ('alert_telegram_bot_token', 'alert_telegram_chat_id');
```

- [ ] **Step 2: Тесты логики алертов (RED)**

`server/backend/cmd/api/alerts_test.go`:

```go
package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type alertHarness struct {
	now    time.Time
	sent   []string
	events []alertEvent
	m      *alertManager
}

// newAlertHarness builds a synchronous manager (queue == nil) with a fake clock,
// fake sender and in-memory event store.
func newAlertHarness() *alertHarness {
	h := &alertHarness{now: time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC)}
	h.m = &alertManager{
		now:      func() time.Time { return h.now },
		send:     func(text string) error { h.sent = append(h.sent, text); return nil },
		record:   func(ev alertEvent) { h.events = append(h.events, ev) },
		cooldown: 30 * time.Minute,
		state:    map[string]*alertState{},
		loaded:   true,
	}
	return h
}

func (h *alertHarness) advance(d time.Duration) { h.now = h.now.Add(d) }

func TestAlertDedupAndCriticalRepeat(t *testing.T) {
	h := newAlertHarness()
	h.m.Raise("k", AlertCritical, "Вход недоступен", "d1")
	h.advance(5 * time.Minute)
	h.m.Raise("k", AlertCritical, "Вход недоступен", "d2")
	if len(h.sent) != 1 {
		t.Fatalf("dedup: want 1 message, got %d", len(h.sent))
	}
	h.advance(26 * time.Minute) // 31 min after the first message
	h.m.Raise("k", AlertCritical, "Вход недоступен", "d3")
	if len(h.sent) != 2 || !strings.Contains(h.sent[1], "ПОВТОР") {
		t.Fatalf("critical must repeat after cooldown: %q", h.sent)
	}
}

func TestAlertWarnNotRepeated(t *testing.T) {
	h := newAlertHarness()
	h.m.Raise("disk", AlertWarn, "Диск", "90%")
	h.advance(3 * time.Hour)
	h.m.Raise("disk", AlertWarn, "Диск", "91%")
	if len(h.sent) != 1 {
		t.Fatalf("warn must not repeat while active, got %d", len(h.sent))
	}
}

func TestAlertResolveOnlyIfRaised(t *testing.T) {
	h := newAlertHarness()
	h.m.Resolve("never", "ok")
	if len(h.sent) != 0 || len(h.events) != 0 {
		t.Fatalf("resolve of an unknown key must be a no-op: %q %v", h.sent, h.events)
	}
	h.m.Raise("k", AlertWarn, "Бэкап", "старый")
	h.m.Resolve("k", "свежий")
	h.m.Resolve("k", "свежий")
	if len(h.sent) != 2 || !strings.Contains(h.sent[1], "ВОССТАНОВЛЕНО") {
		t.Fatalf("want raise + exactly one recovery: %q", h.sent)
	}
}

func TestAlertFlappingSuppressedByCooldown(t *testing.T) {
	h := newAlertHarness()
	h.m.Raise("k", AlertCritical, "T", "") // +0m  sent
	h.advance(time.Minute)
	h.m.Resolve("k", "up") // +1m  sent (recovery)
	h.advance(time.Minute)
	h.m.Raise("k", AlertCritical, "T", "") // +2m  suppressed
	h.advance(time.Minute)
	h.m.Resolve("k", "up") // +3m  cleared silently
	h.advance(time.Minute)
	h.m.Raise("k", AlertCritical, "T", "") // +4m  suppressed
	if len(h.sent) != 2 {
		t.Fatalf("flapping inside cooldown must be silent, got %d: %q", len(h.sent), h.sent)
	}
	h.advance(28 * time.Minute) // +32m, 31m after the last message
	h.m.Raise("k", AlertCritical, "T", "")
	if len(h.sent) != 3 {
		t.Fatalf("still-active alert must go out once cooldown expires, got %d", len(h.sent))
	}
	var kinds []string
	for _, ev := range h.events {
		kinds = append(kinds, ev.Kind)
	}
	if got := strings.Join(kinds, ","); got != "raise,resolve,suppressed,cleared,suppressed,raise" {
		t.Fatalf("event kinds: %s", got)
	}
}

func TestReplayAlertEventsRestoresState(t *testing.T) {
	t0 := time.Date(2026, 10, 12, 10, 0, 0, 0, time.UTC)
	state := replayAlertEvents([]alertEvent{
		{Key: "a", Level: AlertCritical, Kind: "raise", Title: "A", At: t0},
		{Key: "b", Level: AlertWarn, Kind: "raise", Title: "B", At: t0},
		{Key: "b", Level: AlertWarn, Kind: "resolve", Title: "B", At: t0.Add(time.Minute)},
		{Key: "test", Level: AlertWarn, Kind: "test", At: t0},
	})
	if a := state["a"]; a == nil || !a.active || !a.notified || !a.lastSent.Equal(t0) {
		t.Fatalf("a must be active+notified: %+v", a)
	}
	if b := state["b"]; b == nil || b.active {
		t.Fatalf("b must be resolved: %+v", b)
	}
	if _, ok := state["test"]; ok {
		t.Fatal("test events must not create alert state")
	}

	h := newAlertHarness()
	h.now = t0.Add(10 * time.Minute)
	h.m.state = state
	h.m.Raise("a", AlertCritical, "A", "") // restart inside cooldown: no duplicate
	h.m.Resolve("a", "ok")
	if len(h.sent) != 1 || !strings.Contains(h.sent[0], "ВОССТАНОВЛЕНО") {
		t.Fatalf("after restart want only the recovery: %q", h.sent)
	}
}

func TestTelegramSendMessageErrorsHideToken(t *testing.T) {
	const token = "123456789:AAFakeTokenFakeTokenFakeTokenFake12"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/bot"+token+"/sendMessage" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"ok":false,"description":"Bad Request: chat not found"}`))
	}))
	defer srv.Close()
	old := telegramAPIBase
	defer func() { telegramAPIBase = old }()

	telegramAPIBase = srv.URL
	err := telegramSendMessage(srv.Client(), token, "-100", "hi")
	if err == nil || !strings.Contains(err.Error(), "chat not found") {
		t.Fatalf("want Telegram description in error, got %v", err)
	}

	telegramAPIBase = "http://127.0.0.1:1"
	err = telegramSendMessage(&http.Client{Timeout: 3 * time.Second}, token, "-100", "hi")
	if err == nil || strings.Contains(err.Error(), token) {
		t.Fatalf("transport error must not contain the token, got %v", err)
	}
}
```

Run: `cd server/backend && go test ./cmd/api/ -run 'TestAlert|TestReplay|TestTelegram' -v`
Expected: `FAIL github.com/heyearth/backend/cmd/api [build failed]` с `undefined: alertManager`, `undefined: replayAlertEvents`, `undefined: telegramAPIBase`.

- [ ] **Step 3: Локальный SOCKS туннеля exit**

`server/backend/cmd/api/exit_socks.go`:

```go
package main

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"sort"
	"time"

	"golang.org/x/net/proxy"

	"github.com/heyearth/backend/internal/biz"
)

// pickExitSocksPort returns the local SOCKS5 port of a healthy exit's Hysteria2
// client: hysteria2-client-<id>.service listens on 127.0.0.1:(1088+id), the same
// port buildExitNodeOutbound and checkTunnelHealth use (exit id 2 → :1090).
// Anything sent there leaves the Hub through the foreign exit.
func pickExitSocksPort() (int, bool) {
	nodes, err := biz.ListHealthyExitNodes()
	if err != nil {
		return 0, false
	}
	sort.SliceStable(nodes, func(i, j int) bool {
		hi, hj := nodes[i].TunnelProtocol == "hysteria2", nodes[j].TunnelProtocol == "hysteria2"
		if hi != hj {
			return hi
		}
		return nodes[i].ID < nodes[j].ID
	})
	for _, n := range nodes {
		port := 1088 + n.ID
		conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 2*time.Second)
		if err != nil {
			continue // no hysteria2 client for this exit
		}
		_ = conn.Close()
		return port, true
	}
	return 0, false
}

// exitSocksDialer dials through the exit; hostnames are resolved by the exit (SOCKS5 remote DNS).
func exitSocksDialer(port int) (proxy.ContextDialer, error) {
	d, err := proxy.SOCKS5("tcp", fmt.Sprintf("127.0.0.1:%d", port), nil, &net.Dialer{Timeout: 10 * time.Second})
	if err != nil {
		return nil, err
	}
	cd, ok := d.(proxy.ContextDialer)
	if !ok {
		return nil, errors.New("socks dialer does not implement DialContext")
	}
	return cd, nil
}

func exitSocksHTTPClient(port int, timeout time.Duration) (*http.Client, error) {
	d, err := exitSocksDialer(port)
	if err != nil {
		return nil, err
	}
	return &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			DialContext:         d.DialContext,
			TLSHandshakeTimeout: 10 * time.Second,
			DisableKeepAlives:   true,
		},
	}, nil
}
```

`golang.org/x/net` уже прямая зависимость (`go.mod`: v0.47.0), `go get` не нужен.

- [ ] **Step 4: Модуль алертов**

`server/backend/cmd/api/alerts.go`:

```go
package main

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/heyearth/backend/internal/biz"
	"github.com/heyearth/backend/internal/db"
)

// ─────────────────────── Operational alerts → Telegram ───────────────────────

type AlertLevel string

const (
	AlertWarn     AlertLevel = "warn"
	AlertCritical AlertLevel = "critical"
)

const (
	alertCooldown      = 30 * time.Minute
	alertTokenSetting  = "alert_telegram_bot_token"
	alertChatIDSetting = "alert_telegram_chat_id"
)

var (
	errAlertsNotConfigured = errors.New("Telegram не настроен: заполните токен бота и chat_id в админке → Алерты")
	telegramAPIBase        = "https://api.telegram.org"
	mskZone                = time.FixedZone("MSK", 3*60*60)
)

// alertEvent is one alert_events row. Kind: raise | repeat | suppressed | resolve | cleared | test.
type alertEvent struct {
	ID        int64      `json:"id"`
	Key       string     `json:"key"`
	Level     AlertLevel `json:"level"`
	Kind      string     `json:"kind"`
	Title     string     `json:"title"`
	Details   string     `json:"details"`
	Delivered bool       `json:"delivered"`
	Error     string     `json:"error"`
	At        time.Time  `json:"created_at"`
}

type alertState struct {
	active   bool
	notified bool // a raise/repeat message was attempted for the current incident
	level    AlertLevel
	title    string
	lastSent time.Time
}

type alertOutgoing struct {
	ev   alertEvent
	send bool
}

type alertManager struct {
	mu       sync.Mutex
	now      func() time.Time
	send     func(text string) error
	record   func(ev alertEvent)
	load     func() ([]alertEvent, error)
	cooldown time.Duration
	queue    chan alertOutgoing // nil → deliver synchronously (tests)
	state    map[string]*alertState
	loaded   bool
}

var alerts = newProdAlertManager()

func newProdAlertManager() *alertManager {
	m := &alertManager{
		now:      time.Now,
		send:     sendTelegramAlert,
		record:   recordAlertEvent,
		load:     loadRecentAlertEvents,
		cooldown: alertCooldown,
		queue:    make(chan alertOutgoing, 256),
		state:    map[string]*alertState{},
	}
	go func() { // single worker: callers never block on Telegram, order is preserved
		for o := range m.queue {
			m.deliver(o)
		}
	}()
	return m
}

// RaiseAlert sends the alert once per key; while the key stays active a critical
// alert is repeated at most once per cooldown (30 min), a warn alert is not repeated.
func RaiseAlert(key string, level AlertLevel, title, details string) {
	alerts.Raise(key, level, title, details)
}

// ResolveAlert sends a recovery message only if a message for key was sent.
func ResolveAlert(key, message string) { alerts.Resolve(key, message) }

func (m *alertManager) Raise(key string, level AlertLevel, title, details string) {
	m.mu.Lock()
	m.ensureLoadedLocked()
	now := m.now()
	st := m.state[key]
	if st == nil {
		st = &alertState{}
		m.state[key] = st
	}
	coolOK := st.lastSent.IsZero() || now.Sub(st.lastSent) >= m.cooldown
	kind := ""
	switch {
	case !st.active && coolOK:
		kind = "raise"
	case !st.active:
		kind = "suppressed" // flapping: this key produced a message less than cooldown ago
	case !st.notified && coolOK:
		kind = "raise"
	case st.notified && coolOK && level == AlertCritical:
		kind = "repeat"
	}
	if kind == "" {
		m.mu.Unlock()
		return
	}
	send := kind != "suppressed"
	st.active, st.level, st.title = true, level, title
	if send {
		st.notified, st.lastSent = true, now
	}
	m.mu.Unlock()
	m.dispatch(alertEvent{Key: key, Level: level, Kind: kind, Title: title, Details: details, At: now}, send)
}

func (m *alertManager) Resolve(key, message string) {
	m.mu.Lock()
	m.ensureLoadedLocked()
	st := m.state[key]
	if st == nil || !st.active {
		m.mu.Unlock()
		return
	}
	now := m.now()
	kind, send := "cleared", false // nobody was told about this incident → close silently
	if st.notified {
		kind, send = "resolve", true
		st.lastSent = now
	}
	st.active, st.notified = false, false
	ev := alertEvent{Key: key, Level: st.level, Kind: kind, Title: st.title, Details: message, At: now}
	m.mu.Unlock()
	m.dispatch(ev, send)
}

func (m *alertManager) dispatch(ev alertEvent, send bool) {
	if ev.Level == "" {
		ev.Level = AlertWarn
	}
	o := alertOutgoing{ev: ev, send: send}
	if m.queue == nil {
		m.deliver(o)
		return
	}
	select {
	case m.queue <- o:
	default:
		log.Printf("[alerts] очередь переполнена, событие %s/%s отброшено", ev.Key, ev.Kind)
	}
}

func (m *alertManager) deliver(o alertOutgoing) {
	ev := o.ev
	if o.send {
		if err := m.send(formatAlertText(ev)); err != nil {
			ev.Error = err.Error()
		} else {
			ev.Delivered = true
		}
	}
	log.Printf("[alerts] %s %s [%s] %s: %s (delivered=%v %s)", ev.Kind, ev.Key, ev.Level, ev.Title, ev.Details, ev.Delivered, ev.Error)
	if m.record != nil {
		m.record(ev)
	}
}

func (m *alertManager) ensureLoadedLocked() {
	if m.loaded || m.load == nil {
		return
	}
	m.loaded = true
	events, err := m.load()
	if err != nil {
		log.Printf("[alerts] не удалось восстановить состояние алертов из БД: %v", err)
		return
	}
	m.state = replayAlertEvents(events)
}

// replayAlertEvents rebuilds per-key state from journal rows ordered by id ASC.
func replayAlertEvents(events []alertEvent) map[string]*alertState {
	state := make(map[string]*alertState)
	for _, ev := range events {
		if ev.Kind == "test" {
			continue
		}
		st := state[ev.Key]
		if st == nil {
			st = &alertState{}
			state[ev.Key] = st
		}
		switch ev.Kind {
		case "raise", "repeat":
			st.active, st.notified, st.level, st.title, st.lastSent = true, true, ev.Level, ev.Title, ev.At
		case "suppressed":
			st.active, st.notified, st.level, st.title = true, false, ev.Level, ev.Title
		case "resolve":
			st.active, st.notified, st.lastSent = false, false, ev.At
		case "cleared":
			st.active, st.notified = false, false
		}
	}
	return state
}

func formatAlertText(ev alertEvent) string {
	head := "ВНИМАНИЕ"
	if ev.Level == AlertCritical {
		head = "КРИТИЧНО"
	}
	switch ev.Kind {
	case "repeat":
		head = "ПОВТОР · " + head
	case "resolve":
		head = "ВОССТАНОВЛЕНО"
	case "test":
		head = "ТЕСТ"
	}
	details := ev.Details
	if r := []rune(details); len(r) > 3000 { // Telegram limit is 4096 chars
		details = string(r[:3000]) + "…"
	}
	return fmt.Sprintf("HiEarth · %s · %s\n%s\n\n%s · %s MSK",
		head, ev.Title, details, ev.Key, ev.At.In(mskZone).Format("02.01 15:04"))
}

// ── Telegram ──

func sendTelegramAlert(text string) error {
	token := strings.TrimSpace(biz.GetSettingOrDefault(alertTokenSetting, ""))
	chatID := strings.TrimSpace(biz.GetSettingOrDefault(alertChatIDSetting, ""))
	if token == "" || chatID == "" {
		return errAlertsNotConfigured
	}
	directErr := telegramSendMessage(&http.Client{Timeout: 10 * time.Second}, token, chatID, text)
	if directErr == nil {
		return nil
	}
	// api.telegram.org may be throttled from the RU Hub: retry through the exit tunnel.
	port, ok := pickExitSocksPort()
	if !ok {
		return directErr
	}
	client, err := exitSocksHTTPClient(port, 15*time.Second)
	if err != nil {
		return fmt.Errorf("напрямую: %v; exit SOCKS: %v", directErr, err)
	}
	if viaErr := telegramSendMessage(client, token, chatID, text); viaErr != nil {
		return fmt.Errorf("напрямую: %v; через exit :%d: %v", directErr, port, viaErr)
	}
	log.Printf("[alerts] Telegram напрямую недоступен (%v) — отправлено через exit :%d", directErr, port)
	return nil
}

func telegramSendMessage(client *http.Client, token, chatID, text string) error {
	payload, _ := json.Marshal(map[string]any{"chat_id": chatID, "text": text, "disable_web_page_preview": true})
	resp, err := client.Post(telegramAPIBase+"/bot"+token+"/sendMessage", "application/json", bytes.NewReader(payload))
	if err != nil {
		return errors.New(redactSecret(err.Error(), token)) // *url.Error embeds the URL with the token
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	var tr struct {
		OK          bool   `json:"ok"`
		Description string `json:"description"`
	}
	_ = json.Unmarshal(raw, &tr)
	if resp.StatusCode != http.StatusOK || !tr.OK {
		desc := tr.Description
		if desc == "" {
			desc = strings.TrimSpace(string(raw))
		}
		return fmt.Errorf("Telegram HTTP %d: %s", resp.StatusCode, redactSecret(desc, token))
	}
	return nil
}

func redactSecret(s, secret string) string {
	if secret == "" {
		return s
	}
	return strings.ReplaceAll(s, secret, "<token>")
}

// sendTestAlert is synchronous: the admin sees the real Telegram error.
func sendTestAlert() error {
	ev := alertEvent{Key: "test", Level: AlertWarn, Kind: "test", Title: "Тестовое сообщение",
		Details: "Алерты HiEarth настроены: сообщения о сбоях будут приходить в этот чат.", At: time.Now()}
	err := sendTelegramAlert(formatAlertText(ev))
	if err != nil {
		ev.Error = err.Error()
	} else {
		ev.Delivered = true
	}
	recordAlertEvent(ev)
	return err
}

// ── Journal (alert_events) ──

const alertEventColumns = `id, alert_key, level, kind, title, details, delivered, error, created_at`

func recordAlertEvent(ev alertEvent) {
	if db.DB == nil {
		return
	}
	if _, err := db.DB.Exec(`INSERT INTO alert_events (alert_key, level, kind, title, details, delivered, error, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		ev.Key, string(ev.Level), ev.Kind, ev.Title, ev.Details, ev.Delivered, ev.Error, ev.At); err != nil {
		log.Printf("[alerts] запись в alert_events: %v", err)
	}
}

func loadRecentAlertEvents() ([]alertEvent, error) {
	return scanAlertEvents(db.DB.Query(`SELECT ` + alertEventColumns + ` FROM (
		SELECT ` + alertEventColumns + ` FROM alert_events
		WHERE kind <> 'test' AND created_at > NOW() - INTERVAL '7 days'
		ORDER BY id DESC LIMIT 5000) t ORDER BY id ASC`))
}

func listAlertEvents(limit int) ([]alertEvent, error) {
	return scanAlertEvents(db.DB.Query(`SELECT `+alertEventColumns+` FROM alert_events ORDER BY id DESC LIMIT $1`, limit))
}

func scanAlertEvents(rows *sql.Rows, err error) ([]alertEvent, error) {
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	events := []alertEvent{}
	for rows.Next() {
		var ev alertEvent
		var level string
		if err := rows.Scan(&ev.ID, &ev.Key, &level, &ev.Kind, &ev.Title, &ev.Details, &ev.Delivered, &ev.Error, &ev.At); err != nil {
			return nil, err
		}
		ev.Level = AlertLevel(level)
		events = append(events, ev)
	}
	return events, rows.Err()
}
```

- [ ] **Step 5: Тесты (GREEN)**

Run: `cd server/backend && go test ./cmd/api/ -run 'TestAlert|TestReplay|TestTelegram' -v`
Expected: 6 × `--- PASS`, итог `ok  github.com/heyearth/backend/cmd/api`.

- [ ] **Step 6: Admin API алертов**

`server/backend/cmd/api/alerts_admin.go`:

```go
package main

import (
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/heyearth/backend/internal/admin"
	"github.com/heyearth/backend/internal/biz"
)

var (
	telegramTokenRe  = regexp.MustCompile(`^\d{5,}:[A-Za-z0-9_-]{30,}$`)
	telegramChatIDRe = regexp.MustCompile(`^(-?\d{1,20}|@[A-Za-z][A-Za-z0-9_]{4,31})$`)
)

// registerAlertAdminRoutes: GET/PUT /api/admin/alerts/settings, POST /api/admin/alerts/test,
// GET /api/admin/alerts/events. RBAC: settings.read / settings.write.
func registerAlertAdminRoutes() {
	http.HandleFunc("/api/admin/alerts/settings", func(w http.ResponseWriter, r *http.Request) {
		perm := "settings.write"
		if r.Method == http.MethodGet {
			perm = "settings.read"
		}
		admin.RequirePermission(perm, handleAdminAlertSettings)(w, r)
	})
	http.HandleFunc("/api/admin/alerts/test", admin.RequirePermission("settings.write", handleAdminAlertTest))
	http.HandleFunc("/api/admin/alerts/events", admin.RequirePermission("settings.read", handleAdminAlertEvents))
}

func maskAlertToken(t string) string {
	if t == "" {
		return ""
	}
	if len(t) <= 8 {
		return "***"
	}
	return t[:4] + "…" + t[len(t)-2:]
}

func handleAdminAlertSettings(w http.ResponseWriter, r *http.Request) {
	setCORS(w, r)
	switch r.Method {
	case http.MethodGet:
		token := biz.GetSettingOrDefault(alertTokenSetting, "")
		writeJSON(w, http.StatusOK, map[string]any{
			"token_set":  token != "",
			"token_hint": maskAlertToken(token), // the token itself is never returned
			"chat_id":    biz.GetSettingOrDefault(alertChatIDSetting, ""),
		})
	case http.MethodPut:
		var req struct {
			BotToken   string `json:"bot_token"` // empty = keep the stored token
			ChatID     string `json:"chat_id"`
			ClearToken bool   `json:"clear_token"`
		}
		if err := parseJSON(r, &req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
			return
		}
		token, chatID := strings.TrimSpace(req.BotToken), strings.TrimSpace(req.ChatID)
		if token != "" && !telegramTokenRe.MatchString(token) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "неверный формат токена (ожидается 123456789:AA…)"})
			return
		}
		if chatID != "" && !telegramChatIDRe.MatchString(chatID) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "неверный chat_id: число (например -1001234567890) или @channel"})
			return
		}
		tokenChanged := req.ClearToken || token != ""
		if tokenChanged {
			if err := biz.SetSetting(alertTokenSetting, token); err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to save token"})
				return
			}
		}
		if err := biz.SetSetting(alertChatIDSetting, chatID); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to save chat_id"})
			return
		}
		admin.WriteAuditLog(r, "update_alert_settings", "setting", "alert_telegram",
			map[string]any{"chat_id": chatID, "token_changed": tokenChanged})
		writeJSON(w, http.StatusOK, map[string]string{"message": "saved"})
	default:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
	}
}

func handleAdminAlertTest(w http.ResponseWriter, r *http.Request) {
	setCORS(w, r)
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "POST only"})
		return
	}
	if err := sendTestAlert(); err != nil {
		status := http.StatusBadGateway // never 401/403: admin_web treats those as logout
		if errors.Is(err, errAlertsNotConfigured) {
			status = http.StatusBadRequest
		}
		writeJSON(w, status, map[string]string{"error": err.Error()})
		return
	}
	admin.WriteAuditLog(r, "send_test_alert", "setting", "alert_telegram", map[string]any{"delivered": true})
	writeJSON(w, http.StatusOK, map[string]string{"message": "Тестовое сообщение отправлено"})
}

func handleAdminAlertEvents(w http.ResponseWriter, r *http.Request) {
	setCORS(w, r)
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "GET only"})
		return
	}
	limit := 100
	if v, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && v > 0 && v <= 500 {
		limit = v
	}
	events, err := listAlertEvents(limit)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to load alert events"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"events": events})
}
```

В `server/backend/cmd/api/main.go` (функция `main`) вставить перед блоком `// Admin panel` / `admin.RegisterRoutes()`:

```go
	// Admin: Telegram alerts — settings (token masked), test message, event journal
	registerAlertAdminRoutes()

	// Admin panel
	admin.RegisterRoutes()
```

- [ ] **Step 7: Токен не уходит через общий список настроек**

`GET /api/admin/settings` (`ListSettingsHandler`) отдаёт все `app_settings` открытым текстом. В `server/backend/internal/admin/settings_handlers.go` заменить тело `ListSettingsHandler`:

```go
func ListSettingsHandler(w http.ResponseWriter, r *http.Request) {
	settings, err := biz.GetAllSettings()
	if err != nil {
		writeAdminError(w, http.StatusInternalServerError, "Failed to load settings")
		return
	}
	// The Telegram alert bot token is write-only: managed via /api/admin/alerts/settings.
	delete(settings, "alert_telegram_bot_token")
	writeAdminJSON(w, http.StatusOK, settings)
}
```

- [ ] **Step 8: Сборка бэкенда**

Run: `cd server/backend && gofmt -l cmd/api/alerts.go cmd/api/alerts_test.go cmd/api/alerts_admin.go cmd/api/exit_socks.go cmd/api/main.go internal/admin/settings_handlers.go && go build ./... && go vet ./cmd/api/ ./internal/admin/ && go test ./cmd/api/ -run 'TestAlert|TestReplay|TestTelegram'`
Expected: `gofmt`/`build`/`vet` без вывода, затем `ok  github.com/heyearth/backend/cmd/api`. (`gofmt -l` — только по своим файлам: в репо уже есть неотформатированные `auto_tuner.go`, `b2b_handlers.go` и др.; файлы — с LF-окончаниями, иначе gofmt их тоже покажет.)

- [ ] **Step 9: API-клиент админки**

В `admin_web/src/api/admin.js` вставить перед строкой `// ── Auto-Tuner ──`:

```js
// ── Alerts (Telegram) ──
export function fetchAlertSettings() {
  return apiFetch('/alerts/settings')
}
export function saveAlertSettings(payload) {
  return apiFetch('/alerts/settings', { method: 'PUT', body: payload })
}
export function sendAlertTest() {
  return apiFetch('/alerts/test', { method: 'POST' })
}
export function fetchAlertEvents(limit = 100) {
  return apiFetch(`/alerts/events?limit=${limit}`)
}

```

- [ ] **Step 10: Страница «Алерты»**

`admin_web/src/pages/AlertsPage.jsx` (тема Linear: токены, Geist Mono для времени/ключей, строки 36px, без теней, `rounded`):

```jsx
import { useEffect, useState } from 'react'
import { RefreshCw, Send } from 'lucide-react'
import { useAuth } from '../auth/AuthContext.jsx'
import { useToast } from '../components/Toast.jsx'
import { fetchAlertSettings, saveAlertSettings, sendAlertTest, fetchAlertEvents } from '../api/admin.js'
import { formatDate } from '../utils/format.js'
import {
  Badge, Button, Card, CardHeader, CardBody, EmptyState, Input, PageHeader, Spinner,
  Table, Thead, Tbody, Tr, Th, Td,
} from '../components/ui/index.js'

const KIND = {
  raise:      { label: 'Тревога',       variant: 'danger' },
  repeat:     { label: 'Повтор',        variant: 'warning' },
  resolve:    { label: 'Восстановлено', variant: 'success' },
  suppressed: { label: 'Подавлено',     variant: 'default' },
  cleared:    { label: 'Снято',         variant: 'default' },
  test:       { label: 'Тест',          variant: 'info' },
}

function Delivery({ ev }) {
  if (ev.kind === 'suppressed' || ev.kind === 'cleared') return <span className="text-tertiary">—</span>
  if (ev.delivered) return <span className="text-success">отправлено</span>
  return <span className="text-danger" title={ev.error}>{ev.error ? 'ошибка' : 'не отправлено'}</span>
}

export default function AlertsPage() {
  const { can } = useAuth()
  const toast = useToast()
  const canWrite = can('settings.write')
  const [loading, setLoading] = useState(true)
  const [saving, setSaving] = useState(false)
  const [testing, setTesting] = useState(false)
  const [cfg, setCfg] = useState({ token_set: false, token_hint: '', chat_id: '' })
  const [token, setToken] = useState('')
  const [chatId, setChatId] = useState('')
  const [events, setEvents] = useState([])

  async function loadEvents() {
    try {
      const data = await fetchAlertEvents(100)
      setEvents(data.events || [])
    } catch (e) { toast.error(e.message) }
  }

  async function load() {
    setLoading(true)
    try {
      const s = await fetchAlertSettings()
      setCfg(s)
      setChatId(s.chat_id || '')
      await loadEvents()
    } catch (e) { toast.error(e.message) } finally { setLoading(false) }
  }

  useEffect(() => { load() }, [])

  async function save(clearToken) {
    setSaving(true)
    try {
      await saveAlertSettings({ bot_token: clearToken ? '' : token, chat_id: chatId, clear_token: clearToken })
      setToken('')
      setCfg(await fetchAlertSettings())
      toast.success(clearToken ? 'Токен удалён' : 'Настройки алертов сохранены')
    } catch (e) { toast.error(e.message) } finally { setSaving(false) }
  }

  async function test() {
    setTesting(true)
    try {
      await sendAlertTest()
      toast.success('Тестовое сообщение отправлено')
    } catch (e) { toast.error(e.message) } finally {
      setTesting(false)
      loadEvents()
    }
  }

  if (loading) return <div className="flex justify-center py-20"><Spinner size="md" /></div>

  return (
    <div className="max-w-5xl space-y-6">
      <PageHeader title="Алерты" subtitle="Сбои входа, шторм рестартов, сертификат, бэкапы и диск — сообщения в Telegram-чат администратора." />

      <Card className="max-w-2xl">
        <CardHeader><h3 className="text-sm font-semibold text-primary">Telegram</h3></CardHeader>
        <CardBody className="space-y-4">
          <div>
            <label className="text-xs text-tertiary block mb-1">Токен бота</label>
            <Input type="password" autoComplete="off" value={token} onChange={e => setToken(e.target.value)}
              placeholder={cfg.token_set ? `Сохранён: ${cfg.token_hint} — введите новый, чтобы заменить` : '123456789:AA…'}
              className="font-mono" disabled={!canWrite} />
            <p className="text-xs text-tertiary mt-1">Создайте бота в @BotFather. Токен хранится на сервере и в админку не возвращается.</p>
          </div>
          <div>
            <label className="text-xs text-tertiary block mb-1">Chat ID</label>
            <Input value={chatId} onChange={e => setChatId(e.target.value)} placeholder="-1001234567890"
              className="font-mono" disabled={!canWrite} />
            <p className="text-xs text-tertiary mt-1">ID чата или канала (или @channel). Добавьте бота в чат и отправьте ему /start.</p>
          </div>
          {canWrite && (
            <div className="flex flex-wrap gap-2 pt-1">
              <Button onClick={() => save(false)} loading={saving}>Сохранить</Button>
              <Button variant="secondary" icon={<Send className="w-3.5 h-3.5" />} onClick={test} loading={testing}
                disabled={!cfg.token_set || !cfg.chat_id}>Отправить тест</Button>
              {cfg.token_set && <Button variant="ghost" onClick={() => save(true)} disabled={saving}>Удалить токен</Button>}
            </div>
          )}
        </CardBody>
      </Card>

      <div className="space-y-2">
        <div className="flex items-center justify-between">
          <h3 className="text-sm font-semibold text-primary">Журнал событий</h3>
          <Button variant="ghost" size="sm" icon={<RefreshCw className="w-3.5 h-3.5" />} onClick={loadEvents}>Обновить</Button>
        </div>
        {events.length === 0 ? (
          <Card><EmptyState title="Событий пока нет" description="Здесь появятся тревоги, восстановления и тестовые сообщения." /></Card>
        ) : (
          <Table>
            <Thead>
              <Tr><Th>Время</Th><Th>Уровень</Th><Th>Событие</Th><Th>Ключ</Th><Th>Сообщение</Th><Th>Доставка</Th></Tr>
            </Thead>
            <Tbody>
              {events.map(ev => {
                const kind = KIND[ev.kind] || { label: ev.kind, variant: 'default' }
                return (
                  <Tr key={ev.id}>
                    <Td mono className="whitespace-nowrap text-secondary">{formatDate(ev.created_at)}</Td>
                    <Td><Badge variant={ev.level === 'critical' ? 'danger' : 'warning'}>{ev.level === 'critical' ? 'критично' : 'внимание'}</Badge></Td>
                    <Td><Badge variant={kind.variant}>{kind.label}</Badge></Td>
                    <Td mono className="text-secondary whitespace-nowrap">{ev.key}</Td>
                    <Td className="max-w-md">
                      <span className="block truncate text-primary" title={ev.details}>
                        {ev.title}{ev.details ? <span className="text-tertiary"> — {ev.details}</span> : null}
                      </span>
                    </Td>
                    <Td><Delivery ev={ev} /></Td>
                  </Tr>
                )
              })}
            </Tbody>
          </Table>
        )}
      </div>
    </div>
  )
}
```

- [ ] **Step 11: Маршрут, сайдбар, Cmd+K, i18n**

`admin_web/src/main.jsx`: после `import DirectRoutesPage from './pages/DirectRoutesPage.jsx'` добавить `import AlertsPage from './pages/AlertsPage.jsx'`; после `<Route path="/mtproto" element={<MTProtoPage />} />` добавить:

```jsx
                  <Route path="/alerts" element={<AlertsPage />} />
```

`admin_web/src/components/Sidebar.jsx`: в импорт из `lucide-react` добавить `Bell` (строка `Settings, ShieldCheck, FileText, Building2, TrendingUp, LogOut, Search,` → `Settings, ShieldCheck, FileText, Building2, TrendingUp, LogOut, Search, Bell,`); в группу `ИНФРАСТРУКТУРА` после элемента `/auto-tuner`:

```jsx
      { to: '/alerts',           icon: Bell,     label: 'navAlerts',         perm: 'settings.read',          hideForTenant: true },
```

`admin_web/src/components/commands.js`: тот же `Bell` в импорт `lucide-react`; после записи `go-auto-tuner`:

```js
  { id: 'go-alerts',          label: 'Алерты',               icon: Bell,            kind: 'page', href: '/alerts',          group: 'Инфраструктура' },
```

`admin_web/src/i18n/ru.js` после `navMTProto: 'MTProto',` → `navAlerts: 'Алерты',`; `admin_web/src/i18n/en.js` после `navMTProto: 'MTProto',` → `navAlerts: 'Alerts',`. (Тексты страницы — по-русски inline, как в `MTProtoPage`/`SettingsPage`.)

- [ ] **Step 12: Сборка админки + STOP-проверка темы**

Run: `cd admin_web && npm run build`
Expected: `✓ built in …`, без ошибок.
Run: `grep -nE "(bg|text|border)-(slate|gray|zinc)-|shadow-|rounded-(2xl|3xl)|Inter|Roboto" admin_web/src/pages/AlertsPage.jsx`
Expected: пусто.

- [ ] **Step 13: Деплой** — бэкенд (см. «Соглашения»), затем `cd server/backend && go run ./cmd/deploy --admin-web`. Внимание: в `quick_deploy.go` захардкожен `host = "45.150.39.236:22"` (старый, отфильтрованный IP) — перед запуском убедиться, что там `138.16.181.225:22` (= `DEPLOY_HOST`), иначе бинарь уедет не туда / SSH повиснет.
Expected: `heyearth-api: ACTIVE`, `systemctl is-active heyearth-api xray nginx` → `active` ×3.

- [ ] **Step 14: Проверка на проде**

1. Миграция: `go run ./cmd/deploy --ssh "sudo -u postgres psql -d heyearth -tA -c 'SELECT version, dirty FROM schema_migrations; SELECT count(*) FROM alert_events;'"` → `55|f` и `0`.
2. Маршруты закрыты: `go run ./cmd/deploy --ssh "curl -s -o /dev/null -w '%{http_code}\n' http://127.0.0.1:8000/api/admin/alerts/events"` → `401`.
3. Telegram с Hub: `go run ./cmd/deploy --ssh "curl -s -m 10 -o /dev/null -w '%{http_code}\n' https://api.telegram.org/"` → `302`/`200` (прямой путь); `000` — значит будет использован обход через exit (в логе появится `отправлено через exit :1090`).
4. **Пользователь** (не агент: ввод токенов — только человеком) создаёт бота в @BotFather, добавляет его в чат, открывает https://admin.hiearth.ru/alerts, вводит токен и chat_id → «Сохранить» → «Отправить тест». Expected: в чате сообщение `HiEarth · ТЕСТ · Тестовое сообщение`; в журнале строка «Тест / отправлено»; поле токена показывает `Сохранён: 1234…xy`.
5. Токен не отдаётся: в залогиненной вкладке админки выполнить `await fetch('/api/admin/settings',{headers:{Authorization:'Bearer '+localStorage.admin_token}}).then(r=>r.json()).then(s=>'alert_telegram_bot_token' in s)` → `false`.
6. `go run ./cmd/deploy --ssh "sudo -u postgres psql -d heyearth -tA -c 'SELECT kind, delivered, error FROM alert_events ORDER BY id DESC LIMIT 3'"` → `test|t|`.
7. Визуально: скриншот `https://admin.hiearth.ru/alerts` через `mcp__claude-in-chrome` — тёмная тема, Geist, hairline-границы, моно-время/ключи, строки 36px, пункт «Алерты» в сайдбаре.

- [ ] **Step 15: Commit**

```bash
git add server/backend/migrations/000055_alert_events.up.sql server/backend/migrations/000055_alert_events.down.sql \
  server/backend/cmd/api/alerts.go server/backend/cmd/api/alerts_test.go server/backend/cmd/api/exit_socks.go \
  server/backend/cmd/api/alerts_admin.go server/backend/cmd/api/main.go server/backend/internal/admin/settings_handlers.go \
  admin_web/src/pages/AlertsPage.jsx admin_web/src/api/admin.js admin_web/src/main.jsx \
  admin_web/src/components/Sidebar.jsx admin_web/src/components/commands.js admin_web/src/i18n/ru.js admin_web/src/i18n/en.js
git commit -m "feat(alerts): Telegram alerting core with dedup, cooldown and event journal" -m "RaiseAlert/ResolveAlert with per-key dedup, 30-min cooldown (critical repeats, warn does not), recovery only after a sent alert, state replayed from alert_events after restart. Telegram via app_settings token/chat_id, falls back to the exit SOCKS when api.telegram.org is unreachable; the token is never returned by the admin API or leaked in errors. Admin page /alerts: settings, test message, event log." -m "Beads: heyearthproject-bfs4" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 3: Мониторинг — шторм рестартов, loop-проба входа, check-host, сертификат, бэкап, диск

**Beads:** `heyearthproject-bfs4`

**Files:**
- Create: `server/backend/cmd/api/xray_restarts.go`, `server/backend/cmd/api/xray_restarts_test.go`
- Modify: `server/backend/cmd/api/xray_sync.go` (`SyncXrayConfig`, сразу после успешного `exec.Command("systemctl", "restart", "xray")`)
- Create: `server/backend/cmd/api/monitor.go`, `server/backend/cmd/api/monitor_test.go`
- Create: `server/backend/cmd/api/monitor_disk_linux.go`, `server/backend/cmd/api/monitor_disk_other.go` (`syscall.Statfs` есть только на Linux, а `go build ./...` гоняется на Windows)
- Modify: `server/backend/cmd/api/main.go` (`StartMonitor()` после `StartGhostReconciler()`)

Пробы и ключи алертов: `xray-restart-storm` (critical, >10 рестартов за скользящий час, проверка при каждом рестарте и раз в минуту); `entry-unreachable-external` (critical, 3 провала подряд loop-пробы: HTTPS `https://<site>/api/v1/health` и TLS-рукопожатие с `reality_sni_domain` на `main_server_host:443` — оба через локальный SOCKS Hysteria2 exit `127.0.0.1:1088+id`, т.е. трафик выходит из-за рубежа и возвращается на публичный IP Hub; если сам туннель не работает — контрольный dial на `1.1.1.1:443` не проходит и проба не засчитывается); `entry-unreachable-ru` (warn, 2 прогона подряд, где все RU-узлы check-host ответили и все не подключились; ошибки check-host и неполные ответы не считаются); `tls-cert-expiry` (warn, < 14 дней, `127.0.0.1:8443` с SNI сайта); `backup-stale` (warn, новейший файл в `/opt/heyearth/backups` старше 26 ч, каталог пуст или отсутствует — до Task 9 этот алерт ожидаемо активен); `disk-usage` (warn, `/` > 85%). Все ключи снимаются `ResolveAlert` при восстановлении. Счётчик рестартов — в памяти (после рестарта API окно начинается заново).

- [ ] **Step 1: Тест скользящего окна (RED)**

`server/backend/cmd/api/xray_restarts_test.go`:

```go
package main

import (
	"testing"
	"time"
)

func TestSlidingWindowCounter(t *testing.T) {
	c := &slidingWindowCounter{window: time.Hour}
	t0 := time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 11; i++ {
		if n := c.Add(t0.Add(time.Duration(i) * time.Minute)); n != i+1 {
			t.Fatalf("add #%d: count %d, want %d", i, n, i+1)
		}
	}
	if n := c.Count(t0.Add(time.Hour)); n != 10 {
		t.Fatalf("event exactly 1h old must expire: count %d, want 10", n)
	}
	if n := c.Count(t0.Add(70*time.Minute + time.Second)); n != 0 {
		t.Fatalf("all events must expire: count %d", n)
	}
}
```

Run: `cd server/backend && go test ./cmd/api/ -run TestSlidingWindowCounter -v`
Expected: `[build failed]`, `undefined: slidingWindowCounter`.

- [ ] **Step 2: Счётчик рестартов xray**

`server/backend/cmd/api/xray_restarts.go`:

```go
package main

import (
	"fmt"
	"sync"
	"time"
)

const (
	xrayRestartWindow    = time.Hour
	xrayRestartThreshold = 10
)

// slidingWindowCounter counts events within the last window. Calls must use non-decreasing time.
type slidingWindowCounter struct {
	mu     sync.Mutex
	window time.Duration
	events []time.Time
}

func (c *slidingWindowCounter) Add(now time.Time) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.events = append(c.events, now)
	return c.pruneLocked(now)
}

func (c *slidingWindowCounter) Count(now time.Time) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.pruneLocked(now)
}

func (c *slidingWindowCounter) pruneLocked(now time.Time) int {
	cutoff := now.Add(-c.window)
	i := 0
	for i < len(c.events) && !c.events[i].After(cutoff) {
		i++
	}
	c.events = c.events[i:]
	return len(c.events)
}

var xrayRestarts = &slidingWindowCounter{window: xrayRestartWindow}

// recordXrayRestart is called after every successful `systemctl restart xray`.
func recordXrayRestart() {
	evaluateXrayRestartStorm(xrayRestarts.Add(time.Now()))
}

// checkXrayRestartWindow re-evaluates without a new restart so the storm alert
// resolves once restarts stop (called every minute by the monitor).
func checkXrayRestartWindow() {
	evaluateXrayRestartStorm(xrayRestarts.Count(time.Now()))
}

func evaluateXrayRestartStorm(n int) {
	if n > xrayRestartThreshold {
		RaiseAlert("xray-restart-storm", AlertCritical, "Шторм рестартов xray",
			fmt.Sprintf("%d перезапусков xray за последний час (порог %d). Каждый рестарт рвёт соединения всех пользователей.",
				n, xrayRestartThreshold))
		return
	}
	ResolveAlert("xray-restart-storm", fmt.Sprintf("Рестарты xray в норме: %d за последний час.", n))
}
```

Run: `cd server/backend && go test ./cmd/api/ -run TestSlidingWindowCounter -v`
Expected: `--- PASS: TestSlidingWindowCounter`, `ok`.

- [ ] **Step 3: Учёт рестартов в SyncXrayConfig**

В `server/backend/cmd/api/xray_sync.go`, функция `SyncXrayConfig`, блок `// Restart Xray (Xray-core не поддерживает reload — только restart)` привести к виду (добавляется одна строка `recordXrayRestart()`; если Task 7 уже заменил часть рестартов на API — строка остаётся сразу после оставшегося успешного рестарта):

```go
	// Restart Xray (Xray-core не поддерживает reload — только restart)
	cmd := exec.Command("systemctl", "restart", "xray")
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("restart xray: %w (%s)", err, strings.TrimSpace(string(output)))
	}
	recordXrayRestart()
```

- [ ] **Step 4: Тест разбора check-host (RED)**

`server/backend/cmd/api/monitor_test.go`:

```go
package main

import "testing"

func TestParseCheckHostTCPResult(t *testing.T) {
	cases := []struct{ name, body, verdict string }{
		{"one node ok", `{"ru1.node.check-host.net":[{"time":0.05,"address":"138.16.181.225"}],"ru2.node.check-host.net":[{"error":"Connection timed out"}],"ru3.node.check-host.net":[{"error":"Connection timed out"}]}`, "reachable"},
		{"all failed", `{"ru1.node.check-host.net":[{"error":"Connection timed out"}],"ru2.node.check-host.net":[{"error":"Connection refused"}]}`, "unreachable"},
		{"pending", `{"ru1.node.check-host.net":[{"error":"Connection timed out"}],"ru2.node.check-host.net":null}`, "unknown"},
		{"empty", `{}`, "unknown"},
	}
	for _, tc := range cases {
		tally, err := parseCheckHostTCPResult([]byte(tc.body))
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if got := tally.verdict(); got != tc.verdict {
			t.Fatalf("%s: verdict %s (%+v), want %s", tc.name, got, tally, tc.verdict)
		}
	}
	if _, err := parseCheckHostTCPResult([]byte(`<html>rate limited</html>`)); err == nil {
		t.Fatal("non-JSON body must be an error (not counted as a failure)")
	}
}
```

Run: `cd server/backend && go test ./cmd/api/ -run TestParseCheckHostTCPResult -v`
Expected: `[build failed]`, `undefined: parseCheckHostTCPResult`.

- [ ] **Step 5: Загрузка диска (Linux + заглушка)**

`server/backend/cmd/api/monitor_disk_linux.go`:

```go
//go:build linux

package main

import "syscall"

// diskUsagePercent matches df: used / (used + available to non-root).
func diskUsagePercent(path string) (float64, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, err
	}
	used := float64(st.Blocks - st.Bfree)
	avail := float64(st.Bavail)
	if used+avail == 0 {
		return 0, nil
	}
	return used / (used + avail) * 100, nil
}
```

`server/backend/cmd/api/monitor_disk_other.go`:

```go
//go:build !linux

package main

import "errors"

func diskUsagePercent(path string) (float64, error) {
	return 0, errors.New("disk usage check is supported on linux only")
}
```

- [ ] **Step 6: Монитор**

`server/backend/cmd/api/monitor.go`:

```go
package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"runtime"
	"strings"
	"time"

	"golang.org/x/net/proxy"

	"github.com/heyearth/backend/internal/biz"
)

// ─────────────────────── Monitoring (spec part 6) ───────────────────────
// Every result goes to RaiseAlert/ResolveAlert; nothing here talks to Telegram directly.

const (
	loopProbeInterval      = time.Minute
	loopProbeFailThreshold = 3
	checkHostInterval      = 15 * time.Minute
	checkHostFailThreshold = 2
	housekeepingInterval   = 10 * time.Minute
	certWarnDays           = 14
	backupDir              = "/opt/heyearth/backups"
	backupMaxAge           = 26 * time.Hour
	diskWarnPercent        = 85.0
	defaultCheckHostNodes  = "ru1.node.check-host.net,ru2.node.check-host.net,ru3.node.check-host.net"
)

var (
	checkHostBase  = "https://check-host.net"
	loopProbeFails int       // touched only by the loop-probe goroutine
	loopProbeOKLog time.Time // rate-limits the "probe OK" log line to once an hour
	checkHostFails int       // touched only by the check-host goroutine
)

func StartMonitor() {
	if runtime.GOOS != "linux" {
		log.Println("[monitor] не Linux — мониторинг отключён (локальная разработка)")
		return
	}
	go runEvery(90*time.Second, loopProbeInterval, func() {
		runLoopProbe()
		checkXrayRestartWindow()
	})
	go runEvery(5*time.Minute, checkHostInterval, runCheckHostProbe)
	go runEvery(2*time.Minute, housekeepingInterval, func() {
		checkSiteCertExpiry()
		checkBackupFreshness()
		checkDiskUsage()
	})
	log.Printf("[monitor] запущен: loop-проба каждые %v, check-host каждые %v, сертификат/бэкап/диск каждые %v",
		loopProbeInterval, checkHostInterval, housekeepingInterval)
}

func runEvery(initialDelay, interval time.Duration, fn func()) {
	time.Sleep(initialDelay)
	for {
		func() {
			defer func() {
				if r := recover(); r != nil {
					log.Printf("[monitor] panic: %v", r)
				}
			}()
			fn()
		}()
		time.Sleep(interval)
	}
}

func monitorEntryHost() string {
	return strings.TrimSpace(biz.GetSettingOrDefault("main_server_host", "hiearth.ru"))
}

func monitorSiteHost() string {
	if base := publicBaseURL(); base != "" {
		if u, err := url.Parse(base); err == nil && u.Hostname() != "" {
			return u.Hostname()
		}
	}
	return "hiearth.ru"
}

// ── (a) loop probe: Hub → exit SOCKS → public entry of the Hub ──

func runLoopProbe() {
	port, ok := pickExitSocksPort()
	if !ok {
		log.Println("[monitor] loop-проба пропущена: нет здорового exit с локальным SOCKS")
		return
	}
	dialer, err := exitSocksDialer(port)
	if err != nil {
		log.Printf("[monitor] loop-проба пропущена: %v", err)
		return
	}
	if err := socksDialCheck(dialer, "1.1.1.1:443"); err != nil { // a broken tunnel says nothing about the entry
		log.Printf("[monitor] loop-проба пропущена: туннель exit :%d не работает: %v", port, err)
		return
	}
	entry, site := monitorEntryHost(), monitorSiteHost()
	sni := biz.GetSettingOrDefault("reality_sni_domain", "apple.com")
	var problems []string
	if err := probeSiteHealth(dialer, entry, site); err != nil {
		problems = append(problems, fmt.Sprintf("сайт https://%s/api/v1/health: %v", site, err))
	}
	if err := probeTLSHandshake(dialer, entry, sni); err != nil {
		problems = append(problems, fmt.Sprintf("TLS-вход (SNI %s): %v", sni, err))
	}
	if len(problems) == 0 {
		loopProbeFails = 0
		if time.Since(loopProbeOKLog) > time.Hour {
			loopProbeOKLog = time.Now()
			log.Printf("[monitor] loop-проба OK: %s:443 доступен снаружи (через exit :%d)", entry, port)
		}
		ResolveAlert("entry-unreachable-external", fmt.Sprintf("Вход %s:443 снова доступен снаружи (проба через exit :%d).", entry, port))
		return
	}
	loopProbeFails++
	log.Printf("[monitor] loop-проба провалена (%d подряд): %s", loopProbeFails, strings.Join(problems, "; "))
	if loopProbeFails >= loopProbeFailThreshold {
		RaiseAlert("entry-unreachable-external", AlertCritical, "Вход недоступен снаружи",
			fmt.Sprintf("%d проверок подряд через exit :%d не прошли до %s:443.\n%s",
				loopProbeFails, port, entry, strings.Join(problems, "\n")))
	}
}

func socksDialCheck(dialer proxy.ContextDialer, addr string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return err
	}
	return conn.Close()
}

func probeSiteHealth(dialer proxy.ContextDialer, entryHost, siteHost string) error {
	target := net.JoinHostPort(entryHost, "443")
	client := &http.Client{
		Timeout: 20 * time.Second,
		Transport: &http.Transport{
			// Always dial the public entry; SNI, Host and certificate check use the site name.
			DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
				return dialer.DialContext(ctx, network, target)
			},
			TLSClientConfig:     &tls.Config{ServerName: siteHost},
			TLSHandshakeTimeout: 10 * time.Second,
			DisableKeepAlives:   true,
		},
	}
	resp, err := client.Get("https://" + siteHost + "/api/v1/health")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return nil
}

func probeTLSHandshake(dialer proxy.ContextDialer, entryHost, sni string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	conn, err := dialer.DialContext(ctx, "tcp", net.JoinHostPort(entryHost, "443"))
	if err != nil {
		return err
	}
	defer conn.Close()
	// Reality answers with the dest site's certificate: we test the path, not the PKI.
	return tls.Client(conn, &tls.Config{ServerName: sni, InsecureSkipVerify: true}).HandshakeContext(ctx)
}

// ── (b) check-host.net: TCP :443 from RU nodes ──

type checkHostTally struct{ OK, Failed, Pending int }

// verdict: "reachable" if any node connected; "unreachable" only if every node
// answered and all failed; otherwise "unknown" (not counted either way).
func (t checkHostTally) verdict() string {
	switch {
	case t.OK > 0:
		return "reachable"
	case t.Pending == 0 && t.Failed > 0:
		return "unreachable"
	default:
		return "unknown"
	}
}

// parseCheckHostTCPResult parses /check-result/<id> of a check-tcp request:
// {"ru1.node.check-host.net":[{"time":0.05,"address":"1.2.3.4"}],"ru2...":[{"error":"Connection timed out"}],"ru3...":null}
func parseCheckHostTCPResult(body []byte) (checkHostTally, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		return checkHostTally{}, fmt.Errorf("check-host: bad JSON: %w", err)
	}
	var t checkHostTally
	for _, v := range raw {
		var results []struct {
			Time  *float64 `json:"time"`
			Error string   `json:"error"`
		}
		if string(v) == "null" || json.Unmarshal(v, &results) != nil || len(results) == 0 {
			t.Pending++
			continue
		}
		switch r := results[0]; {
		case r.Error != "":
			t.Failed++
		case r.Time != nil:
			t.OK++
		default:
			t.Pending++
		}
	}
	return t, nil
}

func checkHostGet(client *http.Client, u string) ([]byte, error) {
	req, err := http.NewRequest(http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 256<<10))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("check-host HTTP %d", resp.StatusCode)
	}
	return body, nil
}

func checkHostTCP(target string, nodes []string) (checkHostTally, string, error) {
	client := &http.Client{Timeout: 15 * time.Second}
	q := url.Values{}
	q.Set("host", target)
	for _, n := range nodes {
		q.Add("node", n)
	}
	body, err := checkHostGet(client, checkHostBase+"/check-tcp?"+q.Encode())
	if err != nil {
		return checkHostTally{}, "", err
	}
	var start struct {
		OK            int    `json:"ok"`
		RequestID     string `json:"request_id"`
		PermanentLink string `json:"permanent_link"`
	}
	if err := json.Unmarshal(body, &start); err != nil || start.OK != 1 || start.RequestID == "" {
		return checkHostTally{}, "", fmt.Errorf("check-host не принял запрос: %s", strings.TrimSpace(string(body)))
	}
	var tally checkHostTally
	for attempt := 0; attempt < 6; attempt++ {
		time.Sleep(5 * time.Second)
		res, err := checkHostGet(client, checkHostBase+"/check-result/"+url.PathEscape(start.RequestID))
		if err != nil {
			return checkHostTally{}, start.PermanentLink, err
		}
		if tally, err = parseCheckHostTCPResult(res); err != nil {
			return checkHostTally{}, start.PermanentLink, err
		}
		if tally.Pending == 0 {
			break
		}
	}
	return tally, start.PermanentLink, nil
}

func splitCSV(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func runCheckHostProbe() {
	entry := monitorEntryHost()
	nodes := splitCSV(biz.GetSettingOrDefault("monitor_checkhost_nodes", defaultCheckHostNodes))
	tally, link, err := checkHostTCP(net.JoinHostPort(entry, "443"), nodes)
	if err != nil {
		log.Printf("[monitor] check-host недоступен, результат не учитывается: %v", err)
		return
	}
	verdict := tally.verdict()
	log.Printf("[monitor] check-host %s:443: OK=%d fail=%d pending=%d → %s %s", entry, tally.OK, tally.Failed, tally.Pending, verdict, link)
	switch verdict {
	case "reachable":
		checkHostFails = 0
		ResolveAlert("entry-unreachable-ru", fmt.Sprintf("Вход %s:443 снова доступен с RU-узлов check-host (%d из %d).",
			entry, tally.OK, tally.OK+tally.Failed))
	case "unreachable":
		checkHostFails++
		if checkHostFails >= checkHostFailThreshold {
			RaiseAlert("entry-unreachable-ru", AlertWarn, "Вход недоступен из РФ",
				fmt.Sprintf("%d проверки подряд: ни один RU-узел check-host не подключился к %s:443.\n%s", checkHostFails, entry, link))
		}
	}
}

// ── (c) site certificate, (d) backups, (e) disk ──

func fetchCertNotAfter(addr, sni string) (time.Time, error) {
	// InsecureSkipVerify: we must read the certificate even if it already expired.
	conn, err := tls.DialWithDialer(&net.Dialer{Timeout: 5 * time.Second}, "tcp", addr,
		&tls.Config{ServerName: sni, InsecureSkipVerify: true})
	if err != nil {
		return time.Time{}, err
	}
	defer conn.Close()
	certs := conn.ConnectionState().PeerCertificates
	if len(certs) == 0 {
		return time.Time{}, errors.New("сервер не прислал сертификат")
	}
	return certs[0].NotAfter, nil
}

func checkSiteCertExpiry() {
	site := monitorSiteHost()
	notAfter, err := fetchCertNotAfter("127.0.0.1:8443", site)
	if err != nil {
		log.Printf("[monitor] сертификат %s: не удалось прочитать: %v", site, err)
		return
	}
	days := int(time.Until(notAfter).Hours() / 24)
	if days < certWarnDays {
		RaiseAlert("tls-cert-expiry", AlertWarn, "Сертификат сайта скоро истечёт",
			fmt.Sprintf("Сертификат %s действует до %s (осталось %d дн.). Проверьте продление certbot: systemctl list-timers | grep certbot.",
				site, notAfter.In(mskZone).Format("02.01.2006"), days))
		return
	}
	ResolveAlert("tls-cert-expiry", fmt.Sprintf("Сертификат %s продлён: действует до %s.", site, notAfter.In(mskZone).Format("02.01.2006")))
}

func newestEntryModTime(dir string) (time.Time, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return time.Time{}, err
	}
	var newest time.Time
	for _, e := range entries {
		if info, err := e.Info(); err == nil && info.ModTime().After(newest) {
			newest = info.ModTime()
		}
	}
	return newest, nil
}

func checkBackupFreshness() {
	newest, err := newestEntryModTime(backupDir)
	switch {
	case err != nil:
		RaiseAlert("backup-stale", AlertWarn, "Бэкапов нет", fmt.Sprintf("Не удалось прочитать %s: %v", backupDir, err))
	case newest.IsZero():
		RaiseAlert("backup-stale", AlertWarn, "Бэкапов нет", fmt.Sprintf("Каталог %s пуст.", backupDir))
	case time.Since(newest) > backupMaxAge:
		RaiseAlert("backup-stale", AlertWarn, "Бэкап устарел",
			fmt.Sprintf("Последний бэкап в %s — %v назад (порог %v).", backupDir, time.Since(newest).Round(time.Minute), backupMaxAge))
	default:
		ResolveAlert("backup-stale", fmt.Sprintf("Свежий бэкап: %v назад.", time.Since(newest).Round(time.Minute)))
	}
}

func checkDiskUsage() {
	pct, err := diskUsagePercent("/")
	if err != nil {
		log.Printf("[monitor] диск: %v", err)
		return
	}
	if pct > diskWarnPercent {
		RaiseAlert("disk-usage", AlertWarn, "Диск заполняется",
			fmt.Sprintf("Занято %.1f%% на / (порог %.0f%%). Проверьте journalctl --disk-usage и %s.", pct, diskWarnPercent, backupDir))
		return
	}
	ResolveAlert("disk-usage", fmt.Sprintf("Диск в норме: занято %.1f%%.", pct))
}
```

Run: `cd server/backend && go test ./cmd/api/ -run 'TestParseCheckHostTCPResult|TestSlidingWindowCounter' -v`
Expected: 2 × `--- PASS`, `ok`.

- [ ] **Step 7: Запуск монитора из main**

В `server/backend/cmd/api/main.go` (функция `main`) после вызова `StartGhostReconciler()`:

```go
	StartGhostReconciler()

	// Monitoring → Telegram alerts: external loop probe of the entry via an exit,
	// check-host from RU, site certificate, backups, disk, xray restart storm.
	StartMonitor()
```

- [ ] **Step 8: Сборка, vet (обе ОС), все тесты**

Run: `cd server/backend && gofmt -l cmd/api/xray_restarts.go cmd/api/xray_restarts_test.go cmd/api/xray_sync.go cmd/api/monitor.go cmd/api/monitor_test.go cmd/api/monitor_disk_linux.go cmd/api/monitor_disk_other.go cmd/api/main.go && go build ./... && go vet ./cmd/api/ && GOOS=linux GOARCH=amd64 go vet ./cmd/api/ && go test ./cmd/api/ -v 2>&1 | grep -E "^(--- |ok|FAIL)"`
Expected: `gofmt`/`build`/`vet` без вывода; 8 строк `--- PASS: …` (6 из Task 2 + 2 новых) и `ok  github.com/heyearth/backend/cmd/api`.

- [ ] **Step 9: Деплой бэкенда** (см. «Соглашения»), затем `systemctl is-active heyearth-api xray nginx`
Expected: `active` ×3.

- [ ] **Step 10: Проверка на проде (evidence)**

1. Через 3 мин после деплоя: `go run ./cmd/deploy --ssh "journalctl -u heyearth-api --since '-5min' --no-pager | grep -E '\[monitor\]|\[alerts\]'"`
   Expected: `[monitor] запущен: …`, `[monitor] loop-проба OK: <host>:443 доступен снаружи (через exit :1090)`; нет `loop-проба провалена`. Если `loop-проба пропущена: нет здорового exit` — проверить `ss -ltnp | grep 1090` и `systemctl is-active hysteria2-client-2`.
2. Через 7 мин: та же команда → строка `[monitor] check-host <host>:443: OK=… fail=… pending=0 → reachable https://check-host.net/check-report/…`.
3. Бэкап (до Task 9 ожидаемо): `[alerts] raise backup-stale [warn] Бэкапов нет …` и одно сообщение в Telegram «HiEarth · ВНИМАНИЕ · Бэкапов нет»; повторов нет (warn).
4. Рестарты: `go run ./cmd/deploy --ssh "journalctl -u heyearth-api --since '-1h' --no-pager | grep -c 'Конфиг обновлён'"` ≤ 10 и в `alert_events` нет `xray-restart-storm`: `go run ./cmd/deploy --ssh "sudo -u postgres psql -d heyearth -tA -c 'SELECT alert_key, kind, delivered FROM alert_events ORDER BY id DESC LIMIT 10'"`.
5. Сертификат/диск: `go run ./cmd/deploy --ssh "df -h / | tail -1; echo | openssl s_client -connect 127.0.0.1:8443 -servername hiearth.ru 2>/dev/null | openssl x509 -noout -enddate"` — если занято < 85% и сертификат > 14 дней, в журнале нет `disk-usage`/`tls-cert-expiry`.
6. Аварийная проверка loop-пробы — **только с явного согласия пользователя** (≈5 мин, реальных пользователей не затрагивает: дропаются лишь новые соединения с IP exit на :443 Hub; туннели Hub→exit инициирует Hub). Узнать IP exit: `sudo -u postgres psql -d heyearth -tA -c "SELECT id, ip_address FROM servers WHERE is_exit_node AND lifecycle_state='online'"`. Затем `iptables -I INPUT -s <EXIT_IP> -p tcp --dport 443 -m comment --comment hiearth-monitor-test -j DROP`; через ~4 мин в Telegram «КРИТИЧНО · Вход недоступен снаружи»; откат `iptables -D INPUT -s <EXIT_IP> -p tcp --dport 443 -m comment --comment hiearth-monitor-test -j DROP`; в течение 1–2 мин «ВОССТАНОВЛЕНО · Вход недоступен снаружи». Проверить, что правила не осталось: `iptables -S INPUT | grep -c hiearth-monitor-test` → `0`.
7. Страница https://admin.hiearth.ru/alerts показывает события из пп. 3 и 6.

- [ ] **Step 11: Commit**

```bash
git add server/backend/cmd/api/xray_restarts.go server/backend/cmd/api/xray_restarts_test.go server/backend/cmd/api/xray_sync.go \
  server/backend/cmd/api/monitor.go server/backend/cmd/api/monitor_test.go server/backend/cmd/api/monitor_disk_linux.go \
  server/backend/cmd/api/monitor_disk_other.go server/backend/cmd/api/main.go
git commit -m "feat(monitor): external entry probes, restart-storm and hygiene alerts" -m "Loop probe every minute: site health + Reality-SNI TLS handshake to the Hub's public entry through the exit's local SOCKS (3 failures -> critical). check-host.net TCP probe from RU nodes every 15 min (2 runs with all nodes failing -> warn; check-host errors are ignored). xray restarts counted in a sliding hour (>10 -> critical). Site certificate < 14 days, backup older than 26 h, disk > 85% -> warn. Every alert resolves on recovery." -m "Beads: heyearthproject-bfs4" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```
### Task 4: Без утечек — `block` по умолчанию, `fallbackTag` у балансеров, блок всех пользовательских inbound

**Beads:** `heyearthproject-bfs4` · **Research:** C2

**Files:**
- Create: `server/backend/cmd/api/xray_routing.go` — чистые хелперы маршрутизации + leak-guard
- Create: `server/backend/cmd/api/xray_routing_test.go`
- Modify: `server/backend/cmd/api/xray_sync.go` (`SyncXrayConfig`: шапка файла, перенос inbound `api-in`/`socks-awg`, outbounds, правила, балансеры, ветка «нет exit», `dns.tag`, guard перед `json.MarshalIndent`)
- Вспомогательный, НЕ коммитится: `C:/tmp/xray_stand.sh` → на Hub `/tmp/xray_stand.sh` (нужен и в Tasks 5–6)

Тесты — прямо в `cmd/api`: `init()` в пакете нет (только в `internal/pb/*` и `internal/yookassa`, без БД).

**Кто сейчас попадает в default outbound (`outbounds[0]` = `direct` → RU-IP Hub):**
1. **Отказ балансера.** Стратегия вернула пустой тег, `fallbackTag` нет → `PickRoute` ошибка → dispatcher берёт default handler. Случается при каждой пометке «dead» единственного exit observatory (P6: ~37 раз/сутки) — утечка уже реальная.
2. **Нет здоровых exit.** Блок-правило покрывает только `vless-reality`, `vless-xhttp`; `shadowsocks` (:8388), `socks-awg` (Пальма), `vless-xhttp-cdn` уходят в default.
3. **Встроенный DNS xray** (`dns.tag = "dns-out"`, UDP 8.8.8.8/1.1.1.1) маршрутизируется как трафик: при живых exit — catch-all → Амстердам, без exit — default `direct`. Потребители — только freedom с `UseIP` (`direct-freedom`, `mask-*`); сам роутинг не резолвит (catch-all и блок по inboundTag срабатывают в первом проходе `IPIfNonMatch`). При `block` по умолчанию без явного правила DNS уйдёт в blackhole, freedom (`UseIP` ≠ `ForceIP`) после таймаута откатится на системный резолвер → многосекундные задержки RU-direct во время аварии exit. Решение — явное правило `dns-out → direct`. Побочный эффект желательный: RU-direct резолвится с Hub (RU-CDN-ответы; research Medium «DNS xray резолвит через Амстердам»); нового наружу не уходит — к этим доменам Hub подключается сам.
4. **От default не зависят:** `api-in` (явное правило), observatory (пробы идут прямо через outbound), sniffing `routeOnly` у `socks-awg`, адреса exit-outbounds — IP. Бамбук (hysteria2-server) — не xray; его первый outbound `via_exit`.
5. **Явный freedom-выход с RU-IP остаётся по дизайну:** per-plan `direct-freedom`, RU-split `socks-awg` и `mask-*` (правило по домену **без фильтра по пользователю**, хотя комментарий говорит «premium only»). Вне скоупа; число `mask-*` фиксируем в Step 2.

- [ ] **Step 1: Хелпер для Hub (стенд + проверки)**

Create `C:/tmp/xray_stand.sh`:

```bash
#!/usr/bin/env bash
# Stabilization Tasks 4-6 helper; runs on the Hub as root; not committed.
#   stand - configs derived from the live one: xray run -test + egress via stand SOCKS :20808
#   check - leak-relevant facts of the live config + egress via live socks-awg :10808
#   sub   - one active VLESS user's subscription: VLESS lines vs master-UUID hits
set -u
X=/opt/heyearth/xray/xray
py() { python3 - "$@" <<'PY'
import json, sys
cmd = sys.argv[1]
c = json.load(open("/opt/heyearth/xray/config.json"))
rules, bal = c["routing"]["rules"], c["routing"].get("balancers", [])
if cmd == "ru-domain":
    doms = [d.split(":", 1)[1] for r in rules
            if r.get("inboundTag") == ["socks-awg"] and "domain" in r for d in r["domain"]]
    print(next((d for d in ("yandex.ru", "vk.com", "mail.ru") if d in doms), doms[0] if doms else ""))
elif cmd == "facts":
    print("default outbound:", c["outbounds"][0]["tag"], c["outbounds"][0]["protocol"])
    print("inbounds:", [i["tag"] for i in c["inbounds"]])
    print("balancers:", [(b["tag"], b.get("fallbackTag")) for b in bal])
    print("dns rule:", any(r.get("inboundTag") == ["dns-out"] for r in rules))
    print("no-exit block rule:", [r["inboundTag"] for r in rules
                                  if r.get("outboundTag") == "block" and "inboundTag" in r])
    print("mask outbounds:", [x["tag"] for x in c["outbounds"] if x["tag"].startswith("mask-")])
else:  # stand config: <mode> <out>
    mode, out = cmd, sys.argv[2]
    c["inbounds"] = [dict(i, port=20808) for i in c["inbounds"] if i["tag"] == "socks-awg"]
    first = "direct" if mode == "dead-legacy" else "block"  # dead-legacy = pre-Task-4 semantics
    c["outbounds"].sort(key=lambda x: x["tag"] != first)     # stable sort: only `first` moves
    for b in bal:
        if mode == "dead-legacy":
            b.pop("fallbackTag", None)
        else:
            b["fallbackTag"] = "block"
        if mode.startswith("dead"):
            b["selector"] = ["exit-dead"]                    # balancer without usable members
    if mode != "dead-legacy" and not any(r.get("inboundTag") == ["dns-out"] for r in rules):
        rules.insert(1, {"type": "field", "inboundTag": ["dns-out"], "outboundTag": "direct"})
    if mode == "noexits":
        c["routing"].pop("balancers", None)
        c.pop("observatory", None)
        rules[:] = [r for r in rules if "balancerTag" not in r]
        rules.append({"type": "field", "outboundTag": "block", "inboundTag":
                      ["vless-reality", "vless-xhttp", "shadowsocks", "vless-xhttp-cdn", "socks-awg"]})
    json.dump(c, open(out, "w"), indent=1)
PY
}
egress() { local ip; ip=$(curl -s --max-time 8 --socks5-hostname "127.0.0.1:$1" https://api.ipify.org) || ip=""; echo "ipify=${ip:-BLOCKED}"; }
ru() {
  local d; d=$(py ru-domain)
  if [ -z "$d" ]; then echo "ru=skip"; return; fi
  echo "ru($d)=$(curl -s -o /dev/null --max-time 8 -w '%{http_code} %{time_total}s' --socks5-hostname "127.0.0.1:$1" "https://$d/")"
}
case "${1:-}" in
  stand)
    for m in dead-legacy dead-fixed fixed noexits; do
      py "$m" "/tmp/xs-$m.json"
      t=$("$X" run -test -config "/tmp/xs-$m.json" 2>&1 | tail -1)
      "$X" run -config "/tmp/xs-$m.json" >"/tmp/xs-$m.log" 2>&1 & pid=$!
      sleep 4
      echo "$m | $t | $(egress 20808) | $(ru 20808)"
      kill "$pid"; wait "$pid" 2>/dev/null
    done ;;
  check)
    py facts
    echo "xray -test live: $("$X" run -test -config /opt/heyearth/xray/config.json 2>&1 | tail -1)"
    echo "live socks-awg: $(egress 10808) | $(ru 10808)" ;;
  sub)
    T=$(sudo -u postgres psql -d heyearth -tAc "SELECT u.subscribe_token FROM users u JOIN user_subscriptions s ON s.user_id = u.id JOIN plans p ON p.id = s.plan_id WHERE s.valid_until > now() AND u.subscribe_token IS NOT NULL AND 'vless' = ANY(p.allowed_protocols) ORDER BY s.valid_until DESC LIMIT 1")
    M=$(sudo -u postgres psql -d heyearth -tAc "SELECT value FROM app_settings WHERE key = 'vless_uuid'")
    B=$(curl -s -A Happ "http://127.0.0.1:8000/api/v1/user/subscribe/$T")
    echo "subscription: vless_lines=$(grep -c '^vless://' <<<"$B") master_uuid_hits=$(grep -c "${M:-no-master-uuid}" <<<"$B")" ;;
  *) echo "usage: $0 stand|check|sub"; exit 2 ;;
esac
```

Стенд держит только `socks-awg` на `127.0.0.1:20808` (остальные порты заняты боевым xray). Семантика default/балансеров/DNS от inbound не зависит, поэтому проверяется честно; второй xray живёт ~5 с на режим.

- [ ] **Step 2: RED + стенд на конфиге прода (до правок кода)**

Run:
```bash
cd server/backend && B64=$(base64 -w0 /c/tmp/xray_stand.sh) && go run ./cmd/deploy --ssh "echo $B64 | base64 -d > /tmp/xray_stand.sh && bash /tmp/xray_stand.sh check && bash /tmp/xray_stand.sh stand && command -v xray; /opt/heyearth/xray/xray version | head -1"
```
Expected:
- `check`: `default outbound: direct freedom`, `balancers: [('exit-balancer', None), …]`, `dns rule: False`, `no-exit block rule: []`, `mask outbounds: […]` (записать число), `xray -test live: Configuration OK.`, `live socks-awg: ipify=89.124.89.126`.
- `stand`: `dead-legacy | Configuration OK. | ipify=<IP Hub: 138.16.181.225 или 45.150.39.236>` — **утечка C2 на текущей семантике**; `dead-fixed | Configuration OK. | ipify=BLOCKED`; `fixed | Configuration OK. | ipify=89.124.89.126 | ru(yandex.ru)=200|30x, <3s`; `noexits | Configuration OK. | ipify=BLOCKED | ru(yandex.ru)=200|30x, <3s` (RU-direct жив без exit, DNS через `direct`).
- `command -v xray`: если в PATH другой xray (deploy ставил 25.1.1), `SyncXrayConfig` валидирует им (`exec.LookPath`); `fallbackTag` понимают обе версии — версию записать, здесь не чинить.

Если `dead-fixed` не `BLOCKED` или какой-либо `-test` не `Configuration OK.` — СТОП, разбираться до правок кода.

- [ ] **Step 3: Падающие тесты**

Create `server/backend/cmd/api/xray_routing_test.go`:

```go
package main

import (
	"reflect"
	"testing"
)

func TestRoutingBuilders(t *testing.T) {
	b := newBalancer("plan-3-balancer", []string{"exit-2"}, "random")
	if b["fallbackTag"] != "exit-2" || b["tag"] != "plan-3-balancer" || !reflect.DeepEqual(b["selector"], []string{"exit-2"}) {
		t.Fatalf("unexpected balancer: %v", b)
	}
	if e := newBalancer("plan-9-balancer", nil, "random"); e["fallbackTag"] != "block" {
		t.Fatalf("empty balancer must fall back to block: %v", e)
	}
	r := noExitBlockRule([]string{"vless-reality", "socks-awg"})
	if r["outboundTag"] != "block" || !reflect.DeepEqual(r["inboundTag"], []string{"vless-reality", "socks-awg"}) {
		t.Fatalf("unexpected no-exit rule: %v", r)
	}
	d := dnsDirectRule()
	if d["outboundTag"] != "direct" || !reflect.DeepEqual(d["inboundTag"], []string{"dns-out"}) {
		t.Fatalf("unexpected dns rule: %v", d)
	}
	inbounds := []map[string]interface{}{
		{"tag": "vless-reality"}, {"tag": "vless-xhttp"}, {"tag": "shadowsocks"},
		{"tag": "vless-xhttp-cdn"}, {"tag": "api-in"}, {"tag": "socks-awg"},
	}
	want := []string{"vless-reality", "vless-xhttp", "shadowsocks", "vless-xhttp-cdn", "socks-awg"}
	if got := userInboundTags(inbounds); !reflect.DeepEqual(got, want) {
		t.Fatalf("userInboundTags = %v, want %v", got, want)
	}
}

func TestCheckNoDirectLeak(t *testing.T) {
	direct := map[string]interface{}{"protocol": "freedom", "tag": "direct"}
	good := []map[string]interface{}{newBlockOutbound(), direct}
	withBal := map[string]interface{}{"balancers": []map[string]interface{}{
		newBalancer("exit-balancer", []string{"exit-2"}, "random"),
	}}
	legacyBal := map[string]interface{}{"balancers": []map[string]interface{}{
		{"tag": "exit-balancer", "selector": []string{"exit-2"}},
	}}
	if err := checkNoDirectLeak(good, withBal); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}
	if err := checkNoDirectLeak(good, map[string]interface{}{}); err != nil {
		t.Fatalf("no-exit config rejected: %v", err)
	}
	if checkNoDirectLeak([]map[string]interface{}{direct, newBlockOutbound()}, withBal) == nil {
		t.Fatal("direct-first config accepted")
	}
	if checkNoDirectLeak(good, legacyBal) == nil {
		t.Fatal("balancer without fallbackTag accepted")
	}
	directFallback := map[string]interface{}{"balancers": []map[string]interface{}{
		{"tag": "exit-balancer", "selector": []string{"exit-2"}, "fallbackTag": "direct"},
	}}
	if checkNoDirectLeak(good, directFallback) == nil {
		t.Fatal("balancer falling back to direct accepted")
	}
}
```

- [ ] **Step 4: Убедиться, что тесты падают**

Run: `cd server/backend && go test ./cmd/api/ -run 'TestRoutingBuilders|TestCheckNoDirectLeak' -v`
Expected: FAIL компиляции — `undefined: newBalancer`, `undefined: noExitBlockRule`, … .

- [ ] **Step 5: Реализация хелперов**

Create `server/backend/cmd/api/xray_routing.go`:

```go
package main

import (
	"fmt"
	"strings"
)

// Routing invariants of the generated xray config (C2, research 2026-10-11):
//   - outbounds[0], xray's default handler, is the blackhole "block";
//   - every balancer falls back to its first member — an exit our own health
//     monitor (checkTunnelHealth, 3-fail/2-ok hysteresis) considers healthy —
//     or to "block" when it has none; never to a direct/freedom outbound.
//     Falling back to the exit (not block) matters: xray's observatory marks
//     the only exit "dead" ~37x/day on short probe failures, and block would
//     turn each of those into a user-visible outage;
//   - with no healthy exit, every user inbound is blocked.
//
// Otherwise unmatched traffic and balancer failures egress via the first
// outbound, which used to be "direct", i.e. the Hub's RU IP.
const (
	xrayBlockTag  = "block"
	xrayDirectTag = "direct"  // Hub-local egress, used only by xray's own DNS
	xrayDNSTag    = "dns-out" // dns.tag: inboundTag of xray's built-in DNS queries
	xrayAPIInTag  = "api-in"
)

func newBlockOutbound() map[string]interface{} {
	return map[string]interface{}{
		"protocol": "blackhole",
		"tag":      xrayBlockTag,
		"settings": map[string]interface{}{"response": map[string]string{"type": "none"}},
	}
}

// newBalancer builds a balancer whose fallback is never the default outbound.
// selector holds only exits our health monitor reports healthy (exitTags in
// SyncXrayConfig), so when observatory briefly marks them all dead the
// fallback is the first of them; with an empty selector it is block.
func newBalancer(tag string, selector []string, strategy string) map[string]interface{} {
	fallback := xrayBlockTag
	if len(selector) > 0 && strings.HasPrefix(selector[0], "exit-") {
		fallback = selector[0]
	}
	return map[string]interface{}{
		"tag":         tag,
		"selector":    selector,
		"strategy":    map[string]string{"type": strategy},
		"fallbackTag": fallback,
	}
}

// userInboundTags returns, in config order, the tags of all inbounds that carry
// user traffic: everything except the stats API inbound.
func userInboundTags(inbounds []map[string]interface{}) []string {
	tags := make([]string, 0, len(inbounds))
	for _, in := range inbounds {
		if tag, _ := in["tag"].(string); tag != "" && tag != xrayAPIInTag {
			tags = append(tags, tag)
		}
	}
	return tags
}

// noExitBlockRule blocks all user inbounds. It is appended after the explicit
// RU-direct rules, so RU-direct keeps working while every exit is down.
func noExitBlockRule(inboundTags []string) map[string]interface{} {
	return map[string]interface{}{
		"type":        "field",
		"inboundTag":  inboundTags,
		"outboundTag": xrayBlockTag,
	}
}

// dnsDirectRule sends xray's own DNS queries out of the Hub. Their only
// consumers are freedom outbounds with UseIP (direct-freedom, mask-*), which
// connect from the Hub anyway. Without it the queries would hit the default
// outbound (block) whenever no exit is up.
func dnsDirectRule() map[string]interface{} {
	return map[string]interface{}{
		"type":        "field",
		"inboundTag":  []string{xrayDNSTag},
		"outboundTag": xrayDirectTag,
	}
}

// checkNoDirectLeak is the last guard before the config is written: a config
// violating the invariants is rejected and the running one stays in place.
func checkNoDirectLeak(outbounds []map[string]interface{}, routing map[string]interface{}) error {
	if len(outbounds) == 0 {
		return fmt.Errorf("no outbounds")
	}
	if tag, _ := outbounds[0]["tag"].(string); tag != xrayBlockTag {
		return fmt.Errorf("default outbound is %q, want %q", tag, xrayBlockTag)
	}
	if proto, _ := outbounds[0]["protocol"].(string); proto != "blackhole" {
		return fmt.Errorf("default outbound protocol is %q, want blackhole", proto)
	}
	raw, ok := routing["balancers"]
	if !ok {
		return nil
	}
	balancers, ok := raw.([]map[string]interface{})
	if !ok {
		return fmt.Errorf("routing.balancers has type %T", raw)
	}
	for _, b := range balancers {
		fb, _ := b["fallbackTag"].(string)
		if fb != xrayBlockTag && !strings.HasPrefix(fb, "exit-") {
			return fmt.Errorf("balancer %v has fallbackTag %q, want %q or an exit-* tag", b["tag"], fb, xrayBlockTag)
		}
	}
	return nil
}
```

- [ ] **Step 6: Тесты зелёные**

Run: команда из Step 4. Expected: `--- PASS: TestRoutingBuilders`, `--- PASS: TestCheckNoDirectLeak`, `ok  	github.com/heyearth/backend/cmd/api`.

- [ ] **Step 7: Подключить хелперы в `SyncXrayConfig`** (`cmd/api/xray_sync.go`; правила `user → balancerTag` не трогать — это Task 7)

a) Шапка файла: `//   - Если exit-нод нет — трафик идёт напрямую (freedom)` →
```go
//   - Если exit-нод нет — пользовательский трафик блокируется (кроме явных RU-direct правил)
```

b) Перенос inbound: вырезать без изменений блок от `	// ── Xray Stats API ──` до закрывающей `	})` append'а inbound `socks-awg` включительно (заканчивается прямо перед `	// API routing rule: перенаправляем трафик api-in → api handler`) и вставить непосредственно перед `	// ── Outbounds ──` (после блока `if biz.GetSettingOrDefault("cdn_enabled", "false") == "true" {`). Сразу после вставленного блока:
```go
	// Every inbound except api-in carries user traffic; the no-exit block rule
	// must cover all of them (C2: shadowsocks, socks-awg and CDN used to leak).
	userInbounds := userInboundTags(inbounds)
```

c) Литерал `outbounds := []map[string]interface{}{ … }` (direct + block) заменить целиком:
```go
	// ── Outbounds ──
	// outbounds[0] is xray's default handler: unmatched traffic and balancer
	// failures without fallbackTag land there. It MUST be block (C2).
	outbounds := []map[string]interface{}{
		newBlockOutbound(),
		{
			// Hub-local egress for xray's own DNS client only (dnsDirectRule).
			"protocol": "freedom", "tag": xrayDirectTag,
			"settings": map[string]interface{}{"domainStrategy": "UseIP"},
			"streamSettings": map[string]interface{}{
				"sockopt": map[string]interface{}{
					"tcpFastOpen": true,
					"tcpNoDelay":  true,
				},
			},
		},
	}
```

d) Начало `rules := []map[string]interface{}{` — новым первым элементом (остальное без изменений):
```go
	rules := []map[string]interface{}{
		// 0. Встроенный DNS xray → с Hub. Явно: default outbound теперь block.
		dnsDirectRule(),
		// 1. Блокировка приватных IP — inter-spoke изоляция
```

e) Внутри `if len(exitTags) > 0 {` — дефолтный балансер:
```go
		balancers := []map[string]interface{}{
			newBalancer("exit-balancer", exitTags, balancerStrategy),
		}
```
и в цикле per-plan вместо `balancers = append(balancers, map[string]interface{}{ … })`:
```go
			balancers = append(balancers, newBalancer(balancerTag, planTags, balancerStrategy))
```

f) Ветку `} else {` («Нет здоровых exit-нод») заменить целиком:
```go
	} else {
		// Нет здоровых exit-нод: блокируем ВЕСЬ пользовательский трафик на всех
		// пользовательских inbound (включая shadowsocks, socks-awg, CDN).
		// Явные RU-direct правила выше продолжают работать.
		log.Println("[xray-sync] ВНИМАНИЕ: нет здоровых exit-нод, ВЕСЬ пользовательский трафик блокируется")
		rules = append(rules, noExitBlockRule(userInbounds))
		routing["rules"] = rules
	}
```

g) В `"dns": map[string]interface{}{` строку `"tag":           "dns-out",` → `"tag":           xrayDNSTag,`.

h) Перед `	// Сериализуем конфиг`:
```go
	// Leak guard: never write a config whose default outbound or balancer
	// fallback could send user traffic out via the Hub's RU IP.
	if err := checkNoDirectLeak(outbounds, routing); err != nil {
		return fmt.Errorf("xray config leak guard: %w", err)
	}

```

- [ ] **Step 8: Сборка, vet, тесты**

Run: `cd server/backend && gofmt -w cmd/api/xray_routing.go cmd/api/xray_routing_test.go cmd/api/xray_sync.go && go build ./... && go vet ./cmd/api/ && go test ./cmd/api/ -v 2>&1 | tail -3`
Expected: build/vet без вывода; последняя строка `ok  	github.com/heyearth/backend/cmd/api`.

- [ ] **Step 9: Деплой бэкенда** (см. «Соглашения»), затем:

Run: `go run ./cmd/deploy --ssh 'systemctl is-active heyearth-api xray nginx; journalctl -u heyearth-api --since "-3min" --no-pager | grep -E "xray-sync|leak guard" | tail -5'`
Expected: `active` ×3; есть `[xray-sync] Конфиг обновлён: … N exit-нод …` (N ≥ 1, учитывает `load_weight`); строк `leak guard` нет. Ровно один рестарт xray (структурное изменение).

- [ ] **Step 10: Evidence конфига (GREEN)**

Run: `go run ./cmd/deploy --ssh 'test -f /tmp/xray_stand.sh || echo "re-upload: Step 2"; bash /tmp/xray_stand.sh check'`
Expected: `default outbound: block blackhole`; `inbounds: ['vless-reality', 'vless-xhttp', 'shadowsocks', ('vless-xhttp-cdn',) 'api-in', 'socks-awg']`; `balancers: [('exit-balancer', 'exit-2'), ('plan-N-balancer', 'exit-2'), …]` (fallback — первый здоровый по нашему монитору exit, не block и не direct); `dns rule: True`; `no-exit block rule: []` (exit жив); `xray -test live: Configuration OK.`; `live socks-awg: ipify=89.124.89.126 | ru(yandex.ru)=200|30x`, `time_total` < 3 с.

- [ ] **Step 11: E2E через каждый пользовательский inbound**

- `socks-awg` (путь Пальмы) — Step 10: `ipify=89.124.89.126`.
- Берёза (`vless-reality`, :443 → nginx stream → :10443): в Happ на рабочей машине обновить подписку, выбрать «🌳 Берёза 1»; локально `curl -s --max-time 10 --socks5-hostname 127.0.0.1:10808 https://api.ipify.org` → `89.124.89.126`.
- Дуб (`vless-xhttp`, :2443): «🌲 Дуб 1», та же команда → `89.124.89.126`.
- Облако (`vless-xhttp-cdn`): только если он есть в `inbounds` (Step 10); иначе пропуск (P13 — профиль и так не работает).
- `shadowsocks` :8388 — профиля нет; покрыт правилами (Step 10) и стендом (Step 2). Бамбук — не xray, не затронут.

Expected: нигде не IP Hub (138.16.181.225 / 45.150.39.236); `go run ./cmd/deploy --ssh 'journalctl -u xray --since "-10min" --no-pager | grep -ciE "failed to (lookup|resolve)"'` → `0`. Реальную ветку «нет exit» проверяем в окне обслуживания Task 5 Step 9 — без отдельного простоя.

- [ ] **Step 12: Commit**

```bash
git add server/backend/cmd/api/xray_routing.go server/backend/cmd/api/xray_routing_test.go server/backend/cmd/api/xray_sync.go
git commit -m "fix(xray): block as default outbound, balancer fallbackTag, block every user inbound without exits" -m "outbounds[0] was the freedom 'direct': balancer failures (observatory marking the only exit dead, ~37x/day) and inbounds missing from the no-exit rule (shadowsocks, socks-awg, CDN) egressed via the Hub's RU IP. Block is now the default, balancers fall back to block, the no-exit rule covers all user inbounds, xray's own DNS gets an explicit Hub-local rule, and a leak guard refuses configs that break this." -m "Beads: heyearthproject-bfs4" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 5: Реальный health-check Hysteria2-exit + алерты exit down/up

**Beads:** `heyearthproject-bfs4` · **Research:** C4 · **Предусловие:** Task 2 влит (`RaiseAlert`, `ResolveAlert`, `AlertWarn`, `AlertCritical` в `cmd/api/alerts.go`), Task 4 задеплоен.

**Files:**
- Create: `server/backend/cmd/api/tunnel_probe.go` — `probeViaSOCKS5`, константы пробы, ключ алерта
- Create: `server/backend/cmd/api/tunnel_probe_test.go`
- Modify: `server/backend/cmd/api/xray_sync.go` (`checkTunnelHealth` — `case "hysteria2":`; `checkAllTunnels` — алерты)

`golang.org/x/net v0.47.0` — уже прямая зависимость; пакет `golang.org/x/net/proxy` есть в модуле, `go.mod` не меняется. SOCKS5 hysteria-клиентов на Hub без авторизации (`socks5: listen: 127.0.0.1:<1088+id>`).

Почему сейчас не работает: `case "hysteria2":` лишь открывает TCP на `127.0.0.1:1088+id`; порт слушает локальный клиент и при мёртвом exit → всегда `healthy`. Латентность ~0 мс, а `biz.UpdateExitRTT` пишет только `> 0` → RTT hysteria2-exit не обновляется вообще.

- [ ] **Step 1: RED — состояние на проде**

Run: `cd server/backend && go run ./cmd/deploy --ssh 'sudo -u postgres psql -d heyearth -c "SELECT id, ip_address, tunnel_protocol, tunnel_status, latency_ms, last_rtt_ms, now() - last_rtt_at AS rtt_age FROM servers WHERE is_exit_node"'`
Expected: hysteria2-exit (id 2): `latency_ms` 0, `last_rtt_ms` NULL или `rtt_age` — часы/дни. Записать `ip_address` exit #2 (для Step 10).

- [ ] **Step 2: Падающие тесты**

Create `server/backend/cmd/api/tunnel_probe_test.go`:

```go
package main

import (
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"
)

// startTestSOCKS5 runs a minimal no-auth SOCKS5 CONNECT proxy (IPv4 targets) on loopback.
func startTestSOCKS5(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go serveTestSOCKS5(c)
		}
	}()
	return ln.Addr().String()
}

func serveTestSOCKS5(c net.Conn) {
	defer c.Close()
	b := make([]byte, 256)
	if _, err := io.ReadFull(c, b[:2]); err != nil { // VER NMETHODS
		return
	}
	if _, err := io.ReadFull(c, b[:b[1]]); err != nil { // METHODS
		return
	}
	c.Write([]byte{5, 0}) // method: no auth
	// request: VER CMD RSV ATYP; only CONNECT (1) to IPv4 (1) is needed here
	if _, err := io.ReadFull(c, b[:4]); err != nil || b[1] != 1 || b[3] != 1 {
		return
	}
	if _, err := io.ReadFull(c, b[:6]); err != nil { // addr + port
		return
	}
	up, err := net.Dial("tcp", net.JoinHostPort(net.IP(b[:4]).String(), strconv.Itoa(int(b[4])<<8|int(b[5]))))
	if err != nil {
		c.Write([]byte{5, 5, 0, 1, 0, 0, 0, 0, 0, 0})
		return
	}
	defer up.Close()
	c.Write([]byte{5, 0, 0, 1, 0, 0, 0, 0, 0, 0})
	go io.Copy(up, c)
	io.Copy(c, up)
}

func statusServer(t *testing.T, code int) string {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(code) }))
	t.Cleanup(srv.Close)
	return srv.URL
}

func TestProbeViaSOCKS5OK(t *testing.T) {
	lat, err := probeViaSOCKS5(startTestSOCKS5(t), statusServer(t, http.StatusNoContent)+"/generate_204", 2*time.Second)
	if err != nil || lat <= 0 {
		t.Fatalf("probe = (%v, %v), want (>0, nil)", lat, err)
	}
}

func TestProbeViaSOCKS5WrongStatus(t *testing.T) {
	// An error page / captive portal behind the tunnel is not a healthy exit.
	if _, err := probeViaSOCKS5(startTestSOCKS5(t), statusServer(t, http.StatusOK), 2*time.Second); err == nil {
		t.Fatal("status 200 accepted, want error")
	}
}

// C4: the local SOCKS port accepts TCP, but the tunnel behind it is dead.
func TestProbeViaSOCKS5DeadTunnel(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() { time.Sleep(3 * time.Second); c.Close() }() // never answers
		}
	}()
	start := time.Now()
	if _, err := probeViaSOCKS5(ln.Addr().String(), "https://www.gstatic.com/generate_204", time.Second); err == nil {
		t.Fatal("dead tunnel reported healthy")
	}
	if d := time.Since(start); d > 2500*time.Millisecond {
		t.Fatalf("probe took %v, timeout not enforced", d)
	}
}
```

- [ ] **Step 3: Убедиться, что тесты падают**

Run: `cd server/backend && go test ./cmd/api/ -run TestProbeViaSOCKS5 -v`
Expected: FAIL компиляции — `undefined: probeViaSOCKS5`.

- [ ] **Step 4: Реализация пробы**

Create `server/backend/cmd/api/tunnel_probe.go`:

```go
package main

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"

	"golang.org/x/net/proxy"
)

const (
	// exitProbeURL is fetched THROUGH an exit tunnel: a 204 proves the whole path
	// Hub -> tunnel client -> exit -> internet, not just a listening local port (C4).
	exitProbeURL     = "https://www.gstatic.com/generate_204"
	exitProbeTimeout = 5 * time.Second
)

// exitDownAlertKey is the alert dedup key for one exit node.
func exitDownAlertKey(serverID int) string {
	return fmt.Sprintf("exit-down-%d", serverID)
}

// probeViaSOCKS5 GETs targetURL through the no-auth SOCKS5 proxy at socksAddr
// (hostname resolved on the far side) and returns the elapsed time. Any status
// other than 204 is an error. The whole probe is bounded by timeout.
func probeViaSOCKS5(socksAddr, targetURL string, timeout time.Duration) (time.Duration, error) {
	d, err := proxy.SOCKS5("tcp", socksAddr, nil, &net.Dialer{Timeout: timeout})
	if err != nil {
		return 0, fmt.Errorf("socks5 dialer %s: %w", socksAddr, err)
	}
	cd, ok := d.(proxy.ContextDialer)
	if !ok {
		return 0, fmt.Errorf("socks5 dialer %s: no DialContext", socksAddr)
	}
	client := &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			Proxy:               nil, // never pick up HTTP(S)_PROXY from the environment
			DialContext:         cd.DialContext,
			DisableKeepAlives:   true,
			TLSHandshakeTimeout: timeout,
		},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, targetURL, nil)
	if err != nil {
		return 0, err
	}
	start := time.Now()
	resp, err := client.Do(req)
	if err != nil {
		return 0, fmt.Errorf("GET %s via socks5 %s: %w", targetURL, socksAddr, err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	if resp.StatusCode != http.StatusNoContent {
		return 0, fmt.Errorf("GET %s via socks5 %s: status %d, want 204", targetURL, socksAddr, resp.StatusCode)
	}
	return time.Since(start), nil
}
```

- [ ] **Step 5: Тесты зелёные**

Run: команда из Step 3. Expected: `--- PASS` ×3 (`OK`, `WrongStatus`, `DeadTunnel`), `ok`.

- [ ] **Step 6: Подключить пробу в `checkTunnelHealth`** (`cmd/api/xray_sync.go`)

В doc-комментарии строку `// - Hysteria2: проверка локального SOCKS5 порта Hysteria2-клиента на Hub` → `// - Hysteria2: GET generate_204 через локальный SOCKS5 Hysteria2-клиента (весь путь до exit)`. Блок `case "hysteria2":` заменить целиком:

```go
	case "hysteria2":
		// C4: a listening local SOCKS port says nothing about the exit. Fetch a
		// real 204 THROUGH the tunnel (Hub -> hysteria2 client -> exit -> internet).
		hy2Port := 1088 + node.ID
		localAddr := fmt.Sprintf("127.0.0.1:%d", hy2Port)
		rtt, err := probeViaSOCKS5(localAddr, exitProbeURL, exitProbeTimeout)
		if err != nil {
			return "unhealthy", 0, fmt.Sprintf("hysteria2 via %s: %v", localAddr, err)
		}
		ms := int(rtt.Milliseconds())
		if ms < 1 {
			ms = 1
		}
		return "healthy", ms, ""
```

- [ ] **Step 7: Алерты в `checkAllTunnels`** (гистерезис 3/2 и запись RTT не меняются)

a) Ранний выход и счётчик — заменить `if len(nodes) == 0 { return }` … `for _, node := range nodes {`:
```go
	if len(nodes) == 0 {
		// No online exit at all: SyncXrayConfig blocks every user inbound.
		RaiseAlert("all-exits-down", AlertCritical, "Нет ни одной exit-ноды",
			"В servers нет online exit-нод — пользовательский трафик заблокирован (кроме RU-direct).")
		return
	}

	statusChanged := false
	healthyCount := 0
	for _, node := range nodes {
```

b) Сразу после закрывающей `}` блока `switch oldStatus { … }` (за веткой `default: // "unknown" or empty — use probe result directly`):
```go
		if newStatus == "healthy" {
			healthyCount++
		}
```

c) В `if newStatus != oldStatus {` заменить ветки `unhealthy` и `healthy && oldStatus == "unhealthy"` (последняя ветка `else` с логом `%s -> %s` остаётся как есть):
```go
			if newStatus == "unhealthy" {
				log.Printf("[tunnel-monitor] ALERT: Exit-нода %d (%s, %s) — туннель НЕДОСТУПЕН (после %d проверок)",
					node.ID, node.Name, node.IPAddress, hysteresisUnhealthy)
				biz.RecordFailedConnection(node.ID)
				RaiseAlert(exitDownAlertKey(node.ID), AlertWarn,
					fmt.Sprintf("Exit-нода %s недоступна", node.Name),
					fmt.Sprintf("#%d %s (%s, %s): проверка туннеля не проходит. Ошибка: %s",
						node.ID, node.Name, node.IPAddress, node.TunnelProtocol, probeError))
			} else if newStatus == "healthy" && oldStatus == "unhealthy" {
				log.Printf("[tunnel-monitor] OK: Exit-нода %d (%s, %s) — туннель ВОССТАНОВЛЕН",
					node.ID, node.Name, node.IPAddress)
				biz.RecordSuccessfulConnection(node.ID)
				ResolveAlert(exitDownAlertKey(node.ID),
					fmt.Sprintf("Exit-нода %s (#%d) снова доступна, RTT %d мс", node.Name, node.ID, latencyMs))
			} else {
```

d) После цикла по `nodes`, перед `	// Если статус какой-то ноды изменился — пересинхронизируем Xray (debounced)`:
```go
	// Evaluated every cycle (RaiseAlert dedups by key with a 30-min cooldown),
	// so an outage that began before an API restart still alerts.
	if healthyCount == 0 {
		RaiseAlert("all-exits-down", AlertCritical, "Все exit-ноды недоступны",
			fmt.Sprintf("0 из %d exit-нод healthy — пользовательский трафик заблокирован (кроме RU-direct).", len(nodes)))
	} else {
		ResolveAlert("all-exits-down",
			fmt.Sprintf("Exit-ноды снова доступны: %d из %d healthy", healthyCount, len(nodes)))
	}

```

- [ ] **Step 8: Сборка, vet, тесты, деплой**

Run: `cd server/backend && gofmt -w cmd/api/tunnel_probe.go cmd/api/tunnel_probe_test.go cmd/api/xray_sync.go && go build ./... && go vet ./cmd/api/ && go test ./cmd/api/ -v 2>&1 | tail -3`
Expected: последняя строка `ok  	github.com/heyearth/backend/cmd/api`. Затем деплой бэкенда (см. «Соглашения») и через 2 мин:

Run: `go run ./cmd/deploy --ssh 'sudo -u postgres psql -d heyearth -c "SELECT id, tunnel_protocol, tunnel_status, latency_ms, last_rtt_ms, now() - last_rtt_at AS rtt_age FROM servers WHERE is_exit_node"; journalctl -u heyearth-api --since "-3min" --no-pager | grep -c "НЕДОСТУПЕН"'`
Expected: exit #2 `healthy`, `latency_ms`/`last_rtt_ms` реальные (≈ 100–1500), `rtt_age` < `00:01:00`; счётчик `НЕДОСТУПЕН` = `0` (нет ложных срабатываний).

- [ ] **Step 9: ⚠️ ОКНО ОБСЛУЖИВАНИЯ — остановка hysteria2-client-2** (ночью, ~5 мин: все профили и Бамбук недоступны; предупредить заранее. `systemd-run` сам поднимет клиента через 150 с, даже если SSH оборвётся.)

Run:
```bash
go run ./cmd/deploy --ssh 'systemctl stop hysteria2-client-2 && systemd-run --on-active=150 systemctl start hysteria2-client-2 && for i in $(seq 1 14); do sleep 10; echo "$(date +%T) $(sudo -u postgres psql -d heyearth -tAc "SELECT tunnel_status FROM servers WHERE id = 2")"; done; bash /tmp/xray_stand.sh check'
```
Expected: `healthy` → `unhealthy` не позже ~100 с; `check`: `balancers: []`, `no-exit block rule: [['vless-reality', 'vless-xhttp', 'shadowsocks', 'socks-awg']]` (+ `vless-xhttp-cdn`, если включён), `live socks-awg: ipify=BLOCKED | ru(yandex.ru)=200|30x` — реальная ветка «нет exit» из Task 4. Telegram: warn «Exit-нода … недоступна» + critical «Все exit-ноды недоступны». Happ («Берёза 1») + `curl … https://api.ipify.org` — ошибка, НЕ IP Hub.

Восстановление (таймер уже стартовал клиента):

Run: `go run ./cmd/deploy --ssh 'for i in $(seq 1 12); do sleep 10; echo "$(date +%T) $(sudo -u postgres psql -d heyearth -tAc "SELECT tunnel_status FROM servers WHERE id = 2")"; done; bash /tmp/xray_stand.sh check; journalctl -u heyearth-api --since "-8min" --no-pager | grep -E "tunnel-monitor|нет здоровых" | tail -6'`
Expected: `healthy` в течение ~90 с после старта; `balancers: [('exit-balancer', 'exit-2'), …]`, `ipify=89.124.89.126`; в журнале `НЕДОСТУПЕН` → `ВОССТАНОВЛЕН`; в Telegram два «восстановлено». SSH оборвался — повторить команду.

- [ ] **Step 10: ⚠️ То же окно — сценарий C4 «клиент жив, exit мёртв»** (именно его старая проверка пропускала)

Run (IP exit #2 из Step 1, ожидаемо `89.124.89.126`):
```bash
go run ./cmd/deploy --ssh 'IP=89.124.89.126; iptables -I OUTPUT -d $IP -p udp -j DROP && systemd-run --on-active=150 iptables -D OUTPUT -d $IP -p udp -j DROP && echo "listening: $(ss -lnt | grep -c "127.0.0.1:1090 ")" && for i in $(seq 1 14); do sleep 10; echo "$(date +%T) $(sudo -u postgres psql -d heyearth -tAc "SELECT tunnel_status || chr(32) || coalesce(left(tunnel_error, 90), chr(45)) FROM servers WHERE id = 2")"; done'
```
Expected: `listening: 1` (порт клиента открыт — старая проверка сказала бы healthy); не позже ~110 с `unhealthy hysteria2 via 127.0.0.1:1090: GET https://www.gstatic.com/generate_204 via socks5 …` (timeout/EOF); алерты как в Step 9. Через 150 с таймер снимет правило — повторить команду восстановления из Step 9 (ожидания те же) и `go run ./cmd/deploy --ssh 'iptables -S OUTPUT | grep -c 89.124.89.126'` → `0`.

- [ ] **Step 11: Commit**

```bash
git add server/backend/cmd/api/tunnel_probe.go server/backend/cmd/api/tunnel_probe_test.go server/backend/cmd/api/xray_sync.go
git commit -m "fix(tunnel-monitor): probe hysteria2 exits with a real generate_204 through the tunnel, alert on exit down/up" -m "The hysteria2 check only dialed the local SOCKS port, which the hysteria client keeps listening while the exit is dead, so a dead exit stayed healthy and its RTT was never recorded. The probe now GETs https://www.gstatic.com/generate_204 through that port (5 s timeout, expects 204); 3-fail/2-ok hysteresis unchanged. Transitions raise/resolve exit-down-<id> (warn); zero healthy exits raises all-exits-down (critical)." -m "Beads: heyearthproject-bfs4" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 6: Мастер-UUID сервера никогда не уходит пользователю

**Beads:** `heyearthproject-bfs4` · **Research:** H7

**Files:**
- Modify: `server/backend/cmd/api/client_api.go` (`buildHubConnectionOptions` — сигнатура, без fallback; `GetConnectionBundle`; импорт `log`)
- Modify: `server/backend/cmd/api/subscription_handler.go` (`handleSubscriptionLink`)
- Modify: `server/backend/cmd/api/main.go` (`handleUserConnections` — только вызов `buildHubConnectionOptions`; блок `socksUUID, _ := biz.GetUserVlessUUID` fallback'а не имеет — его поля `socks5_*` убирает Task 8)

Сейчас при ошибке `biz.GetUserVlessUUID` (БД недоступна/таймаут) подставляется `app_settings.vless_uuid` — UUID сервера (`server@heyearth`, premium, вне квоты и учёта); клиент кэширует его навсегда. `buildClashConfig` уже делает правильно — не трогаем.

- [ ] **Step 1: RED**

Run: `cd server/backend && grep -rn 'GetSettingOrDefault("vless_uuid"' cmd/ internal/`
Expected: `cmd/api/client_api.go:169:		userVlessUUID = biz.GetSettingOrDefault("vless_uuid", "")`.

- [ ] **Step 2: `buildHubConnectionOptions` возвращает ошибку**

В `cmd/api/client_api.go` в импорты добавить `"log"` (между `"fmt"` и `"net/url"`). Всё от сигнатуры функции до закрывающей `}` блока `if err != nil { userVlessUUID = biz.GetSettingOrDefault("vless_uuid", "") }` (включая комментарий `// Per-user UUID используется как пароль для прокси-авторизации`) заменить на:

```go
func buildHubConnectionOptions(userID string) ([]*heyearth_v1.ConnectionOption, error) {
	mainServerHost := biz.GetSettingOrDefault("main_server_host", "")

	// Per-user UUID. NEVER fall back to the server master UUID
	// (app_settings.vless_uuid): it bypasses per-user accounting, quota and
	// revocation (H7). A lookup failure is an error for the caller.
	userVlessUUID, err := biz.GetUserVlessUUID(userID)
	if err != nil {
		log.Printf("[connection-options] user %s: vless uuid lookup failed: %v", userID, err)
		return nil, fmt.Errorf("vless uuid: %w", err)
	}
```

Последнюю строку функции `	return opts` → `	return opts, nil`.

- [ ] **Step 3: Вызывающие стороны**

`cmd/api/client_api.go`, `GetConnectionBundle`: `	options := buildHubConnectionOptions(userID)` →
```go
	options, err := buildHubConnectionOptions(userID)
	if err != nil {
		return nil, status.Error(codes.Unavailable, "connection options temporarily unavailable")
	}
```

`cmd/api/subscription_handler.go`, `handleSubscriptionLink`: `	options := buildHubConnectionOptions(userID)` →
```go
	options, err := buildHubConnectionOptions(userID)
	if err != nil {
		// 503 keeps the client's cached profiles; never substitute another UUID.
		w.WriteHeader(http.StatusServiceUnavailable)
		w.Write([]byte("temporarily unavailable"))
		return
	}
```

`cmd/api/main.go`, `handleUserConnections`, внутри `if access.CanConnect {`: `		options := buildHubConnectionOptions(userID)` →
```go
		options, optErr := buildHubConnectionOptions(userID)
		if optErr != nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "Подключения временно недоступны, попробуйте позже"})
			return
		}
```

- [ ] **Step 4: Сборка и GREEN по коду**

Run: `cd server/backend && gofmt -w cmd/api/client_api.go cmd/api/main.go && go build ./... && go vet ./cmd/api/ && grep -rn 'GetSettingOrDefault("vless_uuid"' cmd/ internal/; echo "exit=$?"`
(`subscription_handler.go` в gofmt не прогонять — он уже не gofmt-чистый в чужом doc-комментарии, лишний diff.)
Expected: build/vet без вывода; grep ничего не печатает, `exit=1`.

- [ ] **Step 5: Деплой бэкенда** (см. «Соглашения»), затем evidence:

Run: `go run ./cmd/deploy --ssh 'test -f /tmp/xray_stand.sh || echo "re-upload: Task 4 Step 2"; bash /tmp/xray_stand.sh sub; journalctl -u heyearth-api --since "-5min" --no-pager | grep -c "vless uuid lookup failed"'`
Expected: `subscription: vless_lines=4` (Берёза ×2 + Дуб ×2; 5 с CDN; пользователь с тарифом, разрешающим vless) `master_uuid_hits=0`; счётчик ошибок `0`. Ветку ошибки БД на проде не воспроизводим: Step 4 доказывает, что fallback-кода нет, а компилятор заставил обработать ошибку во всех трёх вызовах.

- [ ] **Step 6: Commit**

```bash
git add server/backend/cmd/api/client_api.go server/backend/cmd/api/subscription_handler.go server/backend/cmd/api/main.go
git commit -m "fix(api): never hand out the master VLESS UUID when the per-user lookup fails" -m "buildHubConnectionOptions fell back to app_settings.vless_uuid on a DB error, so users could receive and cache the server UUID that bypasses per-user accounting, quota and revocation. It now returns an error: gRPC answers Unavailable, the subscription and /connections endpoints answer 503, so clients keep their cached profiles." -m "Beads: heyearthproject-bfs4" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---
### Task 7: Горячие изменения пользователей через API xray — без рестарта

**Beads:** эпик `heyearthproject-bfs4` · **Spec:** §1.2 «Горячие изменения через API xray (подход A)»

Сейчас любое изменение набора пользователей (новая/истёкшая подписка, продление, квота) меняет конфиг → `SyncXrayConfig` делает `systemctl restart xray` → рвутся соединения **всех** пользователей. После задачи такие изменения применяются в работающий xray через его API (`HandlerService` — клиенты inbound, `RoutingService` — правила), конфиг пишется атомарно **без** рестарта. Рестарт остаётся для структурных изменений и как откат при любой ошибке API (не хуже текущего поведения).

**Опирается на Tasks 1–6:** Task 1 — `ORDER BY` в `ListActiveVlessClients`; Task 2 — `RaiseAlert`/`ResolveAlert`; Task 3 — `recordXrayRestart()` после рестарта; Task 4 — `block` первый outbound + `fallbackTag` у балансеров; Task 5 — `checkTunnelHealth`.

**Подзадачи:** 7a стенд (до кода, gate) → 7b классификатор (TDD) → 7c генератор конфига → 7d применение через API → 7e деплой и метрики.

**Семантика CLI — проверена по исходникам Xray-core тег `v26.3.27`** (`main/commands/all/api/{inbound_user_add,inbound_user_remove,inbound_user_count,rules_add,rules_remove,rules_list,shared}.go`, `app/router/router.go`, `app/proxyman/command/command.go`, `proxy/vless/validator.go`, `infra/conf/{api,xray,vless}.go`). Флаги идут **до** позиционных аргументов (Go `flag`); `-s/--server` (по умолчанию `127.0.0.1:8080` — указывать всегда), `-t/-timeout` (сек, по умолчанию 3).

| Команда (точный синтаксис) | Вход / выход | Важное |
|---|---|---|
| `xray api adu --server=127.0.0.1:10085 -timeout=10 FILE.json` | FILE = `{"inbounds":[{полный inbound: tag, listen, port, protocol, settings:{clients:[…], decryption}, streamSettings}]}`. Клиент — как в конфиге: `{"id":"<uuid>","flow":"xtls-rprx-vision","email":"<user_id>","level":1}` (для XHTTP-inbound — без `flow`). stdout: `processing inbound: <tag>` / `add user: <email>` / `result: ok` … `Added N user(s) in total.` | Inbound целиком проходит `InboundDetourConfig.Build()` (без `port` → ошибка) — поэтому передаём объект inbound из нового конфига. Клиенты без `email` молча пропускаются. Ошибка по юзеру (`User x already exists.`) только печатается, **exit 0** → сверяем N. |
| `xray api rmu --server=127.0.0.1:10085 -timeout=10 -tag=<inbound> -- <email>…` | stdout `remove user: <email>` … `Removed N user(s) in total.`, exit 0 даже при ошибках | email без учёта регистра. `MemoryValidator.Del` только чистит карты — **живые соединения удалённого не рвутся** (стенд S6). |
| `xray api inboundusercount --server=127.0.0.1:10085 -tag=<inbound>` | `{"count": N}`; при N = 0 поле опущено → `{}` | считает пользователей с email |
| `xray api adrules --server=127.0.0.1:10085 -timeout=10 FILE.json` (**без** `-append`) | FILE = `{"routing":{"domainStrategy":…,"balancers":[…],"rules":[…]}}`; stdout `{}` | `ReloadRules(shouldAppend=false)` **заменяет все правила и все балансеры** содержимым файла → порядок = порядок файла; `domainStrategy` не меняется. |
| `xray api adrules -append …` | — | добавляет **в конец** (после catch-all); существующий `ruleTag` → `duplicate ruleTag …`, exit 1. «Замена по тегу» = `rmrules <tag>` + `-append` **теряет позицию** — не используем. |
| `xray api rmrules --server=… <ruleTag>…`, `xray api lsrules --server=…` | lsrules: `{"rules":[{"tag":"<outbound>","ruleTag":"<tag>"},…]}`, пустые поля опущены | сверка порядка правил |
| конфиг: `"api":{"tag":"api","services":["HandlerService","RoutingService","StatsService"]}` | | имена без учёта регистра (`infra/conf/api.go`) |

Ошибки dial/decode/build → `base.Fatalf` → exit 1. Классификатор — в отдельном пакете `internal/xraydiff`: в `cmd/api` нет `init()`, требующего БД, но чистый пакет тестируется без сборки `main`.

**Остаточные эффекты (осознанно):** (1) появление/исчезновение балансера тарифа (первый/последний пользователь тарифа с привязкой к серверам) — структурное изменение → один рестарт; (2) во время `adrules` (миллисекунды) xray пересобирает список правил без блокировки `PickRoute` — новое соединение в этот момент может уйти в default outbound (`block` после Task 4), клиент переподключится; открытые соединения не затрагиваются; (3) `rmu` не рвёт открытые соединения — отсечка по квоте/истечению действует на новые подключения, уже открытые (в т. ч. mux) живут до закрытия → follow-up в 7e.

---

#### Task 7a: Стенд — проверить семантику API на бинаре прода (ДО кода)

**Files:**
- Create: `server/backend/scripts/xray_api_stand.sh`

Стенд — отдельный процесс xray 26.3.27 (тот же бинарь `/opt/heyearth/xray/xray`) на свободных loopback-портах (API 20085, VLESS Reality 20443, VLESS XHTTP 20444, тестовые SOCKS-клиенты 20801/20802, локальный HTTP 20999) в отдельных unit'ах `systemd-run`. Боевой xray не трогается; всё удаляется при выходе (`trap`).

- [ ] **Step 1: Создать скрипт стенда**

```bash
#!/usr/bin/env bash
# Task 7 stand: checks `xray api` adu/rmu/adrules/inboundusercount semantics with the
# production xray binary on a throw-away instance (loopback ports 20085 api, 20443
# VLESS Reality, 20444 VLESS XHTTP, 20801/20802 test SOCKS, 20999 local HTTP).
# Does not touch the production xray. Run on the Hub as root; cleans up on exit.
set -u
XRAY=${XRAY:-/opt/heyearth/xray/xray}
DIR=/root/xray-stand
API=127.0.0.1:20085
SNI=${SNI:-yandex.ru}
URL=${URL:-https://www.gstatic.com/generate_204}
OKCODE=${OKCODE:-204}
SID=a1b2c3d4e5f60708
FAILS=0

cleanup() { systemctl stop xray-stand xray-stand-a xray-stand-b xray-stand-http >/dev/null 2>&1; rm -rf "$DIR"; }
trap cleanup EXIT
cleanup
mkdir -p "$DIR" && cd "$DIR" || exit 1

ok()  { echo "PASS  $1"; }
bad() { echo "FAIL  $1"; FAILS=$((FAILS + 1)); }
expect() { if [ "$2" = "$3" ]; then ok "$1 (= $3)"; else bad "$1: want '$2', got '$3'"; fi; }
api() { local sub=$1; shift; "$XRAY" api "$sub" --server=$API "$@" 2>&1; }
count() { api inboundusercount -tag="$1" | tr -d ' \n' | sed -n 's/.*"count":\([0-9]*\).*/\1/p' | grep . || echo 0; }
ruletags() { api lsrules | tr -d ' \n' | grep -o '"ruleTag":"[^"]*"' | cut -d'"' -f4 | paste -sd, -; }
probe() { curl -s -o /dev/null -m 8 -w '%{http_code}' -x "socks5h://127.0.0.1:$1" "$URL"; }

"$XRAY" version | head -1
KEYS=$("$XRAY" x25519)
PRIV=$(echo "$KEYS" | awk -F': ' '/^PrivateKey/{print $2}')
PUB=$(echo "$KEYS" | awk -F': ' '/^Password|^Public key/{print $2}')
UA=$("$XRAY" uuid)
UB=$("$XRAY" uuid)
CA='{"id":"'$UA'","flow":"xtls-rprx-vision","email":"a@stand","level":0}'
CB='{"id":"'$UB'","flow":"xtls-rprx-vision","email":"b@stand","level":1}'
CBX='{"id":"'$UB'","email":"b@stand","level":1}'

reality() { echo '{"show":false,"dest":"'$SNI':443","xver":0,"serverNames":["'$SNI'"],"privateKey":"'$PRIV'","shortIds":["'$SID'"]}'; }
in_tcp() { echo '{"tag":"vr-test","listen":"127.0.0.1","port":20443,"protocol":"vless","settings":{"clients":'"$1"',"decryption":"none"},"streamSettings":{"network":"tcp","security":"reality","realitySettings":'"$(reality)"'}}'; }
in_xhttp() { echo '{"tag":"vx-test","listen":"127.0.0.1","port":20444,"protocol":"vless","settings":{"clients":'"$1"',"decryption":"none"},"streamSettings":{"network":"xhttp","security":"reality","xhttpSettings":{"path":"/"},"realitySettings":'"$(reality)"'}}'; }
routing() { # $1 = JSON array of users of plan 1 (same rule shapes as production)
	cat <<EOF
{"domainStrategy":"IPIfNonMatch",
 "balancers":[{"tag":"plan-1-balancer","selector":["direct"],"strategy":{"type":"random"},"fallbackTag":"block"}],
 "rules":[
  {"type":"field","ruleTag":"api","inboundTag":["api-in"],"outboundTag":"api"},
  {"type":"field","ruleTag":"private","ip":["geoip:private"],"outboundTag":"block"},
  {"type":"field","ruleTag":"plan-1-users","user":$1,"balancerTag":"plan-1-balancer"},
  {"type":"field","ruleTag":"catch-all","network":"tcp,udp","outboundTag":"block"}]}
EOF
}
cat >server.json <<EOF
{"log":{"loglevel":"warning"},
 "api":{"tag":"api","services":["HandlerService","RoutingService","StatsService"]},
 "stats":{},
 "policy":{"levels":{"0":{"statsUserUplink":true,"statsUserDownlink":true},"1":{"statsUserUplink":true,"statsUserDownlink":true}}},
 "inbounds":[{"tag":"api-in","listen":"127.0.0.1","port":20085,"protocol":"dokodemo-door","settings":{"address":"127.0.0.1"}},
  $(in_tcp "[$CA]"), $(in_xhttp "[]")],
 "outbounds":[{"tag":"block","protocol":"blackhole"},{"tag":"direct","protocol":"freedom"},{"tag":"api","protocol":"freedom"}],
 "routing":$(routing '["a@stand"]')}
EOF
client() { # $1 = local SOCKS port, $2 = VLESS uuid
	cat >"client-$1.json" <<EOF
{"log":{"loglevel":"warning"},
 "inbounds":[{"listen":"127.0.0.1","port":$1,"protocol":"socks","settings":{"udp":false}}],
 "outbounds":[{"protocol":"vless","settings":{"vnext":[{"address":"127.0.0.1","port":20443,
   "users":[{"id":"$2","encryption":"none","flow":"xtls-rprx-vision"}]}]},
   "streamSettings":{"network":"tcp","security":"reality",
   "realitySettings":{"fingerprint":"chrome","serverName":"$SNI","publicKey":"$PUB","shortId":"$SID"}}}]}
EOF
}
client 20801 "$UA"
client 20802 "$UB"
"$XRAY" run -test -config server.json >/dev/null || { "$XRAY" run -test -config server.json; exit 1; }
systemd-run --quiet --collect --unit=xray-stand "$XRAY" run -config "$DIR/server.json"
systemd-run --quiet --collect --unit=xray-stand-a "$XRAY" run -config "$DIR/client-20801.json"
systemd-run --quiet --collect --unit=xray-stand-b "$XRAY" run -config "$DIR/client-20802.json"
sleep 3

echo "== S1 baseline"
expect "S1 user A (in plan-1-users) reaches the internet" "$OKCODE" "$(probe 20801)"
expect "S1 user B (unknown to inbound) is rejected" 000 "$(probe 20802)"
expect "S1 inboundusercount vr-test" 1 "$(count vr-test)"
expect "S1 inboundusercount vx-test (prints {})" 0 "$(count vx-test)"
expect "S1 lsrules order" "api,private,plan-1-users,catch-all" "$(ruletags)"

echo "== S2 adu: VLESS user with flow + level"
echo '{"inbounds":['"$(in_tcp "[$CB]")"']}' >adu-b.json
expect "S2 adu output" "Added 1 user(s) in total." "$(api adu adu-b.json | tail -1)"
expect "S2 inboundusercount vr-test" 2 "$(count vr-test)"
expect "S2 B accepted but not in plan rule -> catch-all block" 000 "$(probe 20802)"
journalctl -u xray-stand --no-pager -n 100 | grep "b@stand" | tail -1 | sed 's/^/      access: /'

echo "== S3 adu of an existing user"
expect "S3 duplicate add counted as 0" "Added 0 user(s) in total." "$(api adu adu-b.json | tail -1)"
expect "S3 inboundusercount unchanged" 2 "$(count vr-test)"

echo "== S4 adrules WITHOUT -append = replace all rules + balancers"
echo '{"routing":'"$(routing '["a@stand","b@stand"]')"'}' >rules-ab.json
T0=$(date +%s%N); api adrules rules-ab.json >/dev/null; RC=$?; T1=$(date +%s%N)
expect "S4 adrules exit code" 0 "$RC"
echo "      adrules took $(((T1 - T0) / 1000000)) ms"
expect "S4 order preserved" "api,private,plan-1-users,catch-all" "$(ruletags)"
expect "S4 user B (added by adu, flow vision) works via plan-1-balancer" "$OKCODE" "$(probe 20802)"
expect "S4 user A still works" "$OKCODE" "$(probe 20801)"
if api statsquery -pattern "user>>>b@stand>>>traffic>>>downlink" | grep -q '"value"'; then ok "S4 traffic of API-added user B is counted (statsquery)"; else bad "S4 no traffic stats for b@stand"; fi

echo "== S5 adrules -append: why per-tag replacement is NOT used"
echo '{"routing":{"rules":[{"type":"field","ruleTag":"plan-1-users","user":["a@stand","b@stand"],"balancerTag":"plan-1-balancer"}]}}' >plan1.json
OUT=$(api adrules -append plan1.json); RC=$?
if [ "$RC" -ne 0 ] && echo "$OUT" | grep -q "duplicate ruleTag"; then ok "S5 -append with existing ruleTag -> 'duplicate ruleTag' error"; else bad "S5 rc=$RC out=$OUT"; fi
api rmrules plan-1-users >/dev/null
api adrules -append plan1.json >/dev/null
expect "S5 rmrules + adrules -append lands AFTER catch-all" "api,private,catch-all,plan-1-users" "$(ruletags)"
expect "S5 ...so user B is blocked by catch-all" 000 "$(probe 20802)"
api adrules rules-ab.json >/dev/null
expect "S5 full replacement restores order" "api,private,plan-1-users,catch-all" "$(ruletags)"
expect "S5 user B works again" "$OKCODE" "$(probe 20802)"

echo "== S6 rmu: new connections rejected; what happens to a live one"
if command -v python3 >/dev/null; then
	head -c 100000000 /dev/zero >big.bin
	systemd-run --quiet --collect --unit=xray-stand-http --working-directory="$DIR" python3 -m http.server 20999 --bind 127.0.0.1
	sleep 1
	curl -s -o /dev/null -m 20 --limit-rate 1M -w '%{size_download}' -x socks5h://127.0.0.1:20802 http://127.0.0.1:20999/big.bin >dl.txt &
	DL=$!
	sleep 3
fi
expect "S6 rmu output" "Removed 1 user(s) in total." "$(api rmu -tag=vr-test -- b@stand | tail -1)"
expect "S6 inboundusercount vr-test" 1 "$(count vr-test)"
expect "S6 new connection of B rejected" 000 "$(probe 20802)"
if [ -n "${DL:-}" ]; then
	wait "$DL"
	BYTES=$(cat dl.txt)
	if [ "${BYTES:-0}" -gt 10000000 ]; then echo "INFO  S6 live download SURVIVED rmu: $BYTES bytes in 20 s"; else echo "INFO  S6 live download was CUT by rmu: $BYTES bytes"; fi
fi

echo "== S7 XHTTP inbound, client without flow"
echo '{"inbounds":['"$(in_xhttp "[$CBX]")"']}' >adu-bx.json
expect "S7 adu output" "Added 1 user(s) in total." "$(api adu adu-bx.json | tail -1)"
expect "S7 inboundusercount vx-test" 1 "$(count vx-test)"
expect "S7 rmu output" "Removed 1 user(s) in total." "$(api rmu -tag=vx-test -- b@stand | tail -1)"
expect "S7 inboundusercount vx-test back to 0" 0 "$(count vx-test)"

echo
if [ "$FAILS" -eq 0 ]; then echo "STAND RESULT: ALL PASS"; else echo "STAND RESULT: $FAILS FAIL(S)"; fi
```

- [ ] **Step 2: Прогнать стенд на Hub**

Run: `cd server/backend && go run ./cmd/deploy --ssh "$(cat scripts/xray_api_stand.sh)"`
(вывод печатается дважды — особенность `--ssh`. Если порт 22 режется: `ssh -o "ProxyCommand=/mingw64/bin/connect.exe -S 127.0.0.1:10808 %h %p" root@138.16.181.225 'bash -s' < scripts/xray_api_stand.sh`. Если с Hub недоступен gstatic (в S1 у A `000`): `go run ./cmd/deploy --ssh "export URL=http://cp.cloudflare.com/generate_204; $(cat scripts/xray_api_stand.sh)"`.)

Expected:

| Шаг | Проверяет | Ожидание |
|---|---|---|
| S1 | базовая линия | A → `204`, B → `000`; `vr-test`=1, `vx-test`=0 (`{}`); порядок `api,private,plan-1-users,catch-all` |
| S2 | `adu` VLESS с `flow` + `level` | `Added 1 user(s) in total.`; count=2; B принят, но не в plan-правиле → catch-all `block` → `000` (строка `access:` с `b@stand` — для глаз) |
| S3 | повторный `adu` | `Added 0 user(s) in total.` (при exit 0), count=2 |
| S4 | `adrules` без `-append` | exit 0; порядок **не изменился**; B (добавлен через adu, Vision) → `204`; трафик B виден в `statsquery` (учёт квоты работает для добавленных через API); печатает время вызова (ориентир < 300 ms) |
| S5 | почему не `-append` | `duplicate ruleTag`; после `rmrules`+`-append` порядок `api,private,catch-all,plan-1-users`, B → `000`; полная замена восстанавливает порядок, B → `204` |
| S6 | `rmu` | `Removed 1 user(s) in total.`; count=1; новое соединение B → `000`; `INFO  S6 live download SURVIVED rmu` (ожидаемо) |
| S7 | XHTTP-inbound, клиент без `flow` | `Added 1…`, count=1, `Removed 1…`, count=0 |

Последняя строка: `STAND RESULT: ALL PASS`.

- [ ] **Step 3: Gate — решение по результатам стенда**

- `ALL PASS` → 7b–7d как написано (полная замена routing через `adrules` без `-append`).
- FAIL в S2/S3/S6/S7 (adu/rmu/count ведут себя не так) → **STOP**: Task 7 не внедрять, рестарт-путь остаётся; вывод стенда — в `docs/research/2026-10-11-vpn-stability-research.md`, сообщить владельцу.
- FAIL только в S4 (полная замена ломает порядок/трафик) → **вариант B — хвостовой блок**, внести до 7c:
  1. В 7c все plan-правила генерировать **одним непрерывным блоком прямо перед catch-all**: сначала все `plan-<id>-direct-domains`/`-direct-ip`, затем все `plan-<id>-users` (direct тарифа обязан идти раньше users того же тарифа; между тарифами порядок не важен — множества пользователей не пересекаются); catch-all получает `"ruleTag": "default-exit"`. Перенос direct-правил за masking-правила меняет смысл только при пересечении доменов — проверить, что масок нет: `go run ./cmd/deploy --ssh "grep -c '\"outboundTag\": \"mask-' /opt/heyearth/xray/config.json"` → `0`.
  2. В 7d шаг 3 `applyUserOnlyDiff` заменить на: `runXrayAPI(xrayBin, "rmrules", <все plan-теги старого и нового конфига>…, "default-exit")`, затем `adrules -append` файла `{"routing":{"rules":[<правила нового конфига от первого plan-* до конца>]}}`, затем та же сверка `lsrules` == `d.RuleTags` (`RemoveRule` по несуществующему тегу ошибкой не является).
  3. Повторить стенд с хвостовой заменой в S4 (механика показана в S5).
- S6 `SURVIVED` (ожидаемо) → follow-up в 7e Step 8.

- [ ] **Step 4: Commit**

```bash
git add server/backend/scripts/xray_api_stand.sh
git commit -m "test(xray): stand script for live user API semantics (adu/rmu/adrules)" -m "Runs a throw-away xray 26.3.27 on spare loopback ports and checks adu with Vision flow, rmu, inboundusercount and adrules ordering (full replace vs -append) before the live-apply code ships." -m "Beads: heyearthproject-bfs4" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

#### Task 7b: Классификатор изменений конфига (TDD)

**Files:**
- Create: `server/backend/internal/xraydiff/xraydiff_test.go`
- Create: `server/backend/internal/xraydiff/xraydiff.go`

Правила: конфиги парсятся в `map[string]any` и сравниваются канонически (`reflect.DeepEqual` — форматирование и порядок ключей не важны). Из обоих вырезаются `inbounds[*].settings.clients` (ключ — email без учёта регистра) и правила с `ruleTag` на `plan-`; остаток обязан совпасть, иначе `Structural` (путь первого отличия — в `Reason`). Клиенты сравниваются как множества: новый → add, пропавший → remove, изменённый (level/uuid/flow) → remove+add. `RoutingChanged` — список `routing.rules` отличается (содержимое или позиция plan-правил) → `adrules`. Клиент без email, дубль email, дубль plan-тега → `Structural`.

- [ ] **Step 1: Написать падающий тест**

```go
package xraydiff

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func vless(email string, level int, flow bool) map[string]any {
	c := map[string]any{"id": "uuid-" + email, "email": email, "level": level}
	if flow {
		c["flow"] = "xtls-rprx-vision"
	}
	return c
}

func inbound(tag string, port int, flow bool) map[string]any {
	return map[string]any{"tag": tag, "listen": "127.0.0.1", "port": port, "protocol": "vless",
		"settings": map[string]any{"decryption": "none", "clients": []any{vless("alice", 1, flow), vless("bob", 1, flow)}}}
}

func rule(tag string, kv ...any) map[string]any {
	r := map[string]any{"type": "field"}
	if tag != "" {
		r["ruleTag"] = tag
	}
	for i := 0; i < len(kv); i += 2 {
		r[kv[i].(string)] = kv[i+1]
	}
	return r
}

// baseConfig mirrors the shape SyncXrayConfig generates (Task 7c).
func baseConfig() map[string]any {
	return map[string]any{
		"api": map[string]any{"tag": "api", "services": []any{"HandlerService", "RoutingService", "StatsService"}},
		"inbounds": []any{inbound("vless-reality", 10443, true), inbound("vless-xhttp", 2443, false),
			map[string]any{"tag": "socks-awg", "port": 10808, "protocol": "socks", "settings": map[string]any{"auth": "noauth"}}},
		"outbounds": []any{map[string]any{"tag": "block", "protocol": "blackhole"}, map[string]any{"tag": "exit-1", "protocol": "socks"}},
		"routing": map[string]any{"domainStrategy": "IPIfNonMatch",
			"balancers": []any{map[string]any{"tag": "exit-balancer", "selector": []any{"exit-1"}},
				map[string]any{"tag": "plan-3-balancer", "selector": []any{"exit-1"}}},
			"rules": []any{
				rule("", "inboundTag", []any{"api-in"}, "outboundTag", "api"),
				rule("", "ip", []any{"geoip:private"}, "outboundTag", "block"),
				rule("plan-3-direct-domains", "user", []any{"alice", "bob"}, "domain", []any{"domain:ya.ru"}, "outboundTag", "direct-freedom"),
				rule("plan-3-users", "user", []any{"alice", "bob"}, "balancerTag", "plan-3-balancer"),
				rule("", "network", "tcp,udp", "balancerTag", "exit-balancer"),
			}},
	}
}

func findInbound(cfg map[string]any, tag string) map[string]any {
	for _, raw := range cfg["inbounds"].([]any) {
		if in := raw.(map[string]any); in["tag"] == tag {
			return in
		}
	}
	panic(tag)
}

func setClients(cfg map[string]any, tag string, cs ...any) {
	findInbound(cfg, tag)["settings"].(map[string]any)["clients"] = cs
}

func routing(cfg map[string]any) map[string]any { return cfg["routing"].(map[string]any) }
func rules(cfg map[string]any) []any            { return routing(cfg)["rules"].([]any) }
func setRules(cfg map[string]any, rs ...any)    { routing(cfg)["rules"] = rs }

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// ops renders the user operations as "tag+email" / "tag-email" for comparison.
func ops(r Result) []string {
	var out []string
	for _, ic := range r.Inbounds {
		for _, e := range ic.Removed {
			out = append(out, ic.Tag+"-"+e)
		}
		for _, c := range ic.Added {
			out = append(out, ic.Tag+"+"+c["email"].(string))
		}
	}
	return out
}

func TestClassify(t *testing.T) {
	addCarol := func(c map[string]any) {
		setClients(c, "vless-reality", vless("alice", 1, true), vless("bob", 1, true), vless("carol", 1, true))
		setClients(c, "vless-xhttp", vless("alice", 1, false), vless("bob", 1, false), vless("carol", 1, false))
		rules(c)[3].(map[string]any)["user"] = []any{"alice", "bob", "carol"}
	}
	tests := []struct {
		name      string
		mutate    func(map[string]any)
		kind      Kind
		ops       []string // user-only: expected operations
		rules     []string // user-only: expected ChangedRules
		routing   bool     // user-only: expected RoutingChanged
		reasonHas string   // structural: substring of Reason
	}{
		{name: "identical", mutate: func(map[string]any) {}, kind: None},
		{name: "client order only", kind: None, mutate: func(c map[string]any) {
			setClients(c, "vless-reality", vless("bob", 1, true), vless("alice", 1, true))
		}},
		{name: "user added", kind: UserOnly, mutate: addCarol, routing: true, rules: []string{"plan-3-users"},
			ops: []string{"vless-reality+carol", "vless-xhttp+carol"}},
		{name: "quota cutoff", kind: UserOnly, routing: true, rules: []string{"plan-3-users"},
			ops: []string{"vless-reality-bob", "vless-xhttp-bob"}, mutate: func(c map[string]any) {
				setClients(c, "vless-reality", vless("alice", 1, true))
				setClients(c, "vless-xhttp", vless("alice", 1, false))
				rules(c)[3].(map[string]any)["user"] = []any{"alice"}
			}},
		{name: "modified client = remove+add", kind: UserOnly, ops: []string{"vless-reality-alice", "vless-reality+alice"},
			mutate: func(c map[string]any) { setClients(c, "vless-reality", vless("alice", 0, true), vless("bob", 1, true)) }},
		{name: "new plan rule", kind: UserOnly, routing: true, rules: []string{"plan-5-direct-ip"}, mutate: func(c map[string]any) {
			r := rules(c)
			setRules(c, r[0], r[1], r[2], rule("plan-5-direct-ip", "user", []any{"alice"}, "ip", []any{"5.255.255.0/24"}, "outboundTag", "direct-freedom"), r[3], r[4])
		}},
		{name: "plan rule moved", kind: UserOnly, routing: true, mutate: func(c map[string]any) {
			r := rules(c)
			setRules(c, r[0], r[1], r[2], r[4], r[3])
		}},
		{name: "catch-all changed", kind: Structural, reasonHas: "routing.rules", mutate: func(c map[string]any) {
			rules(c)[4].(map[string]any)["balancerTag"] = "plan-3-balancer"
		}},
		{name: "balancer changed", kind: Structural, reasonHas: "routing.balancers", mutate: func(c map[string]any) {
			routing(c)["balancers"].([]any)[0].(map[string]any)["selector"] = []any{"exit-1", "exit-2"}
		}},
		{name: "exit outbound added", kind: Structural, reasonHas: "outbounds", mutate: func(c map[string]any) {
			c["outbounds"] = append(c["outbounds"].([]any), map[string]any{"tag": "exit-2"})
		}},
		{name: "api services changed", kind: Structural, reasonHas: "api.services", mutate: func(c map[string]any) {
			c["api"].(map[string]any)["services"] = []any{"StatsService"}
		}},
		{name: "users + port change", kind: Structural, reasonHas: "inbounds", mutate: func(c map[string]any) {
			addCarol(c)
			findInbound(c, "vless-xhttp")["port"] = 2444
		}},
		{name: "client without email", kind: Structural, reasonHas: "no email", mutate: func(c map[string]any) {
			setClients(c, "vless-reality", vless("alice", 1, true), map[string]any{"id": "uuid-x"})
		}},
		{name: "duplicate email", kind: Structural, reasonHas: "duplicate email", mutate: func(c map[string]any) {
			setClients(c, "vless-reality", vless("bob", 1, true), vless("Bob", 0, true))
		}},
		{name: "duplicate plan ruleTag", kind: Structural, reasonHas: "duplicate ruleTag", mutate: func(c map[string]any) {
			rules(c)[2].(map[string]any)["ruleTag"] = "plan-3-users"
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			newCfg := baseConfig()
			tt.mutate(newCfg)
			got, err := Classify(mustJSON(t, baseConfig()), mustJSON(t, newCfg))
			if err != nil {
				t.Fatalf("Classify: %v", err)
			}
			if got.Kind != tt.kind {
				t.Fatalf("kind = %v (%s), want %v", got.Kind, got.Reason, tt.kind)
			}
			if tt.kind == Structural {
				if !strings.Contains(got.Reason, tt.reasonHas) {
					t.Errorf("reason = %q, want substring %q", got.Reason, tt.reasonHas)
				}
				return
			}
			if g := ops(got); strings.Join(g, ",") != strings.Join(tt.ops, ",") {
				t.Errorf("ops = %v, want %v", g, tt.ops)
			}
			if strings.Join(got.ChangedRules, ",") != strings.Join(tt.rules, ",") {
				t.Errorf("ChangedRules = %v, want %v", got.ChangedRules, tt.rules)
			}
			if got.RoutingChanged != tt.routing {
				t.Errorf("RoutingChanged = %v, want %v", got.RoutingChanged, tt.routing)
			}
		})
	}
}

func TestClassifyExpectationsAndPayloads(t *testing.T) {
	newCfg := baseConfig()
	setClients(newCfg, "vless-reality", vless("alice", 1, true), vless("bob", 1, true), vless("carol", 1, true))
	got, err := Classify(mustJSON(t, baseConfig()), mustJSON(t, newCfg))
	if err != nil {
		t.Fatal(err)
	}
	if want := map[string]int{"vless-reality": 3, "vless-xhttp": 2}; !reflect.DeepEqual(got.UserCounts, want) {
		t.Errorf("UserCounts = %v, want %v", got.UserCounts, want)
	}
	if want := []string{"", "", "plan-3-direct-domains", "plan-3-users", ""}; !reflect.DeepEqual(got.RuleTags, want) {
		t.Errorf("RuleTags = %v, want %v", got.RuleTags, want)
	}
	if a, r := got.UserTotals(); a != 1 || r != 0 {
		t.Errorf("UserTotals = +%d/-%d, want +1/-0", a, r)
	}
	payload, total, err := got.AddUsersPayload()
	if err != nil || total != 1 {
		t.Fatalf("AddUsersPayload total=%d err=%v", total, err)
	}
	var adu struct {
		Inbounds []struct {
			Tag      string
			Port     int
			Settings struct{ Clients []map[string]any }
		}
	}
	if err := json.Unmarshal(payload, &adu); err != nil {
		t.Fatal(err)
	}
	if len(adu.Inbounds) != 1 || adu.Inbounds[0].Tag != "vless-reality" || adu.Inbounds[0].Port != 10443 {
		t.Fatalf("adu inbounds = %+v", adu.Inbounds)
	}
	if cs := adu.Inbounds[0].Settings.Clients; len(cs) != 1 || cs[0]["email"] != "carol" || cs[0]["flow"] != "xtls-rprx-vision" {
		t.Fatalf("adu clients = %v", cs)
	}
	rp, err := got.RoutingPayload()
	if err != nil || !strings.Contains(string(rp), `"plan-3-balancer"`) || !strings.HasPrefix(string(rp), `{"routing":`) {
		t.Fatalf("RoutingPayload = %s, %v", rp, err)
	}
	if _, err := Classify([]byte("{"), payload); err == nil {
		t.Error("invalid JSON: want error")
	}
}

func TestParseCLIOutput(t *testing.T) {
	cases := []struct {
		got, want int
	}{
		{ParseAddedTotal("processing inbound: vless-reality\nadd user: carol\nresult: ok\nAdded 1 user(s) in total.\n"), 1},
		{ParseAddedTotal("add user: bob\nUser bob already exists.\nAdded 0 user(s) in total.\n"), 0},
		{ParseAddedTotal("failed to dial 127.0.0.1:10085\n"), -1},
		{ParseRemovedTotal("remove user: bob\nRemoved 1 user(s) in total.\n"), 1},
	}
	for i, c := range cases {
		if c.got != c.want {
			t.Errorf("case %d: got %d, want %d", i, c.got, c.want)
		}
	}
	if n, err := ParseUserCount("{\n    \"count\": 42\n}\n"); err != nil || n != 42 {
		t.Errorf("ParseUserCount(42) = %d, %v", n, err)
	}
	if n, err := ParseUserCount("{}\n"); err != nil || n != 0 {
		t.Errorf("ParseUserCount({}) = %d, %v", n, err)
	}
	if _, err := ParseUserCount("failed to get inbound user count\n"); err == nil {
		t.Error("ParseUserCount(garbage): want error")
	}
	tags, err := ParseRuleTags("{\n \"rules\": [\n  {\"tag\": \"api\"},\n  {\"ruleTag\": \"plan-3-users\"},\n  {}\n ]\n}\n")
	if err != nil || !reflect.DeepEqual(tags, []string{"", "plan-3-users", ""}) {
		t.Errorf("ParseRuleTags = %v, %v", tags, err)
	}
}
```

- [ ] **Step 2: Запустить тест — RED**

Run: `cd server/backend && go test ./internal/xraydiff/`
Expected: ошибка сборки `undefined: Result`, `undefined: Kind`, `undefined: None` … и `FAIL github.com/heyearth/backend/internal/xraydiff [build failed]`.

- [ ] **Step 3: Реализация**

```go
// Package xraydiff classifies the difference between two generated xray configs
// and builds/parses the `xray api` CLI payloads used to apply user-only changes.
//
// UserOnly: only inbounds[*].settings.clients and routing rules whose ruleTag
// starts with "plan-" differ — applied live via adu/rmu/adrules, no restart.
// Structural: anything else differs — `systemctl restart xray`.
// Pure (no I/O), fully unit-tested.
package xraydiff

import (
	"encoding/json"
	"fmt"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// PlanRulePrefix marks routing rules whose content depends only on the user list.
const PlanRulePrefix = "plan-"

type Kind int

const (
	None       Kind = iota // running xray needs no change
	UserOnly               // only clients and plan-* rules differ
	Structural             // restart required
)

func (k Kind) String() string { return [...]string{"none", "user-only", "structural"}[k] }

// InboundChange lists the user operations for one inbound.
type InboundChange struct {
	Tag     string
	Removed []string         // emails to remove (deleted + modified clients), sorted
	Added   []map[string]any // clients to add (new + modified), sorted by email
	inbound map[string]any   // inbound object of the new config, without clients
}

type Result struct {
	Kind           Kind
	Reason         string          // first structural difference
	Inbounds       []InboundChange // inbounds with user operations, sorted by tag
	RoutingChanged bool            // routing.rules differ (plan-* content or position)
	ChangedRules   []string        // plan-* ruleTags added/removed/modified (for logs)
	UserCounts     map[string]int  // inbound tag -> clients in the new config
	RuleTags       []string        // ruleTag ("" if none) of every new rule, in order
	routing        any             // routing object of the new config
}

// UserTotals returns the number of add and remove operations.
func (r Result) UserTotals() (added, removed int) {
	for _, ic := range r.Inbounds {
		added, removed = added+len(ic.Added), removed+len(ic.Removed)
	}
	return added, removed
}

type parts struct {
	rest      map[string]any                       // config without clients and plan-* rules
	clients   map[string]map[string]map[string]any // inbound tag -> lower(email) -> client
	inbounds  map[string]map[string]any            // inbound tag -> inbound (without clients)
	planRules map[string]any                       // ruleTag -> rule
	ruleTags  []string
}

// Classify compares the config xray runs from (oldJSON) with the new one.
func Classify(oldJSON, newJSON []byte) (Result, error) {
	var oldCfg, newCfg map[string]any
	if err := json.Unmarshal(oldJSON, &oldCfg); err != nil {
		return Result{}, fmt.Errorf("parse old config: %w", err)
	}
	if err := json.Unmarshal(newJSON, &newCfg); err != nil {
		return Result{}, fmt.Errorf("parse new config: %w", err)
	}
	if reflect.DeepEqual(oldCfg, newCfg) {
		return Result{Kind: None}, nil
	}
	oldP, err := split(oldCfg)
	if err != nil {
		return Result{Kind: Structural, Reason: "old config: " + err.Error()}, nil
	}
	newP, err := split(newCfg)
	if err != nil {
		return Result{Kind: Structural, Reason: "new config: " + err.Error()}, nil
	}
	if key := firstDiff(oldP.rest, newP.rest); key != "" {
		return Result{Kind: Structural, Reason: "changed " + key}, nil
	}
	res := Result{Kind: UserOnly, UserCounts: map[string]int{}, RuleTags: newP.ruleTags, routing: newCfg["routing"]}
	for _, tag := range unionKeys(oldP.clients, newP.clients) {
		oldC, newC := oldP.clients[tag], newP.clients[tag]
		ic := InboundChange{Tag: tag, inbound: newP.inbounds[tag]}
		if ic.inbound == nil {
			ic.inbound = oldP.inbounds[tag]
		}
		for _, k := range sortedKeys(oldC) {
			if nc, ok := newC[k]; !ok || !reflect.DeepEqual(oldC[k], nc) {
				ic.Removed = append(ic.Removed, oldC[k]["email"].(string))
			}
		}
		for _, k := range sortedKeys(newC) {
			if oc, ok := oldC[k]; !ok || !reflect.DeepEqual(oc, newC[k]) {
				ic.Added = append(ic.Added, newC[k])
			}
		}
		res.UserCounts[tag] = len(newC)
		if len(ic.Removed)+len(ic.Added) > 0 {
			res.Inbounds = append(res.Inbounds, ic)
		}
	}
	for _, tag := range unionKeys(oldP.planRules, newP.planRules) {
		if !reflect.DeepEqual(oldP.planRules[tag], newP.planRules[tag]) {
			res.ChangedRules = append(res.ChangedRules, tag)
		}
	}
	res.RoutingChanged = !reflect.DeepEqual(rulesOf(oldCfg), rulesOf(newCfg))
	if len(res.Inbounds) == 0 && !res.RoutingChanged {
		return Result{Kind: None}, nil // only client order / formatting differs
	}
	return res, nil
}

func rulesOf(cfg map[string]any) any {
	routing, _ := cfg["routing"].(map[string]any)
	return routing["rules"]
}

// split separates the user-dependent parts of a config from the rest.
func split(cfg map[string]any) (parts, error) {
	p := parts{rest: clone(cfg).(map[string]any), clients: map[string]map[string]map[string]any{},
		inbounds: map[string]map[string]any{}, planRules: map[string]any{}}
	ins, _ := p.rest["inbounds"].([]any)
	for i, raw := range ins {
		in, ok := raw.(map[string]any)
		if !ok {
			return p, fmt.Errorf("inbounds[%d] is not an object", i)
		}
		settings, _ := in["settings"].(map[string]any)
		rawClients, has := settings["clients"]
		if !has {
			continue
		}
		tag, _ := in["tag"].(string)
		if _, dup := p.clients[tag]; tag == "" || dup {
			return p, fmt.Errorf("inbounds[%d]: empty or duplicate tag %q", i, tag)
		}
		list, _ := rawClients.([]any)
		byEmail := map[string]map[string]any{}
		for j, rc := range list {
			c, _ := rc.(map[string]any)
			email, _ := c["email"].(string)
			if email == "" {
				return p, fmt.Errorf("inbound %s: client %d has no email", tag, j)
			}
			key := strings.ToLower(email) // xray matches emails case-insensitively
			if _, dup := byEmail[key]; dup {
				return p, fmt.Errorf("inbound %s: duplicate email %s", tag, email)
			}
			byEmail[key] = c
		}
		delete(settings, "clients")
		p.clients[tag], p.inbounds[tag] = byEmail, in
	}
	routing, _ := p.rest["routing"].(map[string]any)
	rules, _ := routing["rules"].([]any)
	if rules == nil {
		return p, nil
	}
	kept := make([]any, 0, len(rules))
	for i, raw := range rules {
		r, ok := raw.(map[string]any)
		if !ok {
			return p, fmt.Errorf("routing.rules[%d] is not an object", i)
		}
		tag, _ := r["ruleTag"].(string)
		p.ruleTags = append(p.ruleTags, tag)
		if !strings.HasPrefix(tag, PlanRulePrefix) {
			kept = append(kept, r)
			continue
		}
		if _, dup := p.planRules[tag]; dup {
			return p, fmt.Errorf("duplicate ruleTag %s", tag)
		}
		p.planRules[tag] = r
	}
	routing["rules"] = kept
	return p, nil
}

// firstDiff returns the dotted path of the first differing key ("" if equal).
func firstDiff(a, b map[string]any) string {
	for _, k := range unionKeys(a, b) {
		if reflect.DeepEqual(a[k], b[k]) {
			continue
		}
		am, aok := a[k].(map[string]any)
		bm, bok := b[k].(map[string]any)
		if sub := firstDiff(am, bm); aok && bok && sub != "" {
			return k + "." + sub
		}
		return k
	}
	return ""
}

func clone(v any) any {
	switch t := v.(type) {
	case map[string]any:
		m := make(map[string]any, len(t))
		for k, x := range t {
			m[k] = clone(x)
		}
		return m
	case []any:
		s := make([]any, len(t))
		for i, x := range t {
			s[i] = clone(x)
		}
		return s
	}
	return v
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func unionKeys[V any](a, b map[string]V) []string {
	set := map[string]bool{}
	for k := range a {
		set[k] = true
	}
	for k := range b {
		set[k] = true
	}
	return sortedKeys(set)
}

// ───── `xray api` CLI payloads and output (Xray-core 26.3.27 main/commands/all/api) ─────

// AddUsersPayload returns the `xray api adu` input: {"inbounds":[...]} with, per
// inbound, the full inbound object of the new config (InboundDetourConfig.Build
// needs listen/port/streamSettings) whose clients are only the users to add.
// total = number of users added (0 => nil payload).
func (r Result) AddUsersPayload() (payload []byte, total int, err error) {
	var ins []any
	for _, ic := range r.Inbounds {
		if len(ic.Added) == 0 {
			continue
		}
		in := clone(ic.inbound).(map[string]any)
		settings, _ := in["settings"].(map[string]any)
		if settings == nil {
			settings = map[string]any{}
			in["settings"] = settings
		}
		clients := make([]any, 0, len(ic.Added))
		for _, c := range ic.Added {
			clients = append(clients, c)
		}
		settings["clients"] = clients
		ins = append(ins, in)
		total += len(ic.Added)
	}
	if total == 0 {
		return nil, 0, nil
	}
	payload, err = json.Marshal(map[string]any{"inbounds": ins})
	return payload, total, err
}

// RoutingPayload returns the `xray api adrules` input (used WITHOUT -append): the
// whole routing of the new config. Without -append xray replaces ALL rules and
// balancers (app/router/router.go ReloadRules), so live order == config order.
func (r Result) RoutingPayload() ([]byte, error) {
	if r.routing == nil {
		return nil, fmt.Errorf("new config has no routing")
	}
	return json.Marshal(map[string]any{"routing": r.routing})
}

var (
	reAdded   = regexp.MustCompile(`Added (\d+) user\(s\) in total`)
	reRemoved = regexp.MustCompile(`Removed (\d+) user\(s\) in total`)
	reCount   = regexp.MustCompile(`"count":\s*"?(\d+)`)
)

// ParseAddedTotal: N from "Added N user(s) in total." (adu exits 0 even when some
// users fail; failures are only printed). -1 if the line is missing.
func ParseAddedTotal(out string) int { return firstInt(reAdded, out) }

// ParseRemovedTotal: N from "Removed N user(s) in total." (rmu). -1 if missing.
func ParseRemovedTotal(out string) int { return firstInt(reRemoved, out) }

// ParseUserCount parses inboundusercount output {"count": N}; the field is
// omitted when N == 0, so "{}" means 0.
func ParseUserCount(out string) (int, error) {
	if n := firstInt(reCount, out); n >= 0 {
		return n, nil
	}
	if strings.Contains(out, "{") {
		return 0, nil
	}
	return 0, fmt.Errorf("inboundusercount: unexpected output %q", strings.TrimSpace(out))
}

// ParseRuleTags parses lsrules output {"rules":[{"tag":..,"ruleTag":..}]} into
// the ordered ruleTags ("" for rules without one).
func ParseRuleTags(out string) ([]string, error) {
	i, j := strings.Index(out, "{"), strings.LastIndex(out, "}")
	if i < 0 || j < i {
		return nil, fmt.Errorf("lsrules: no JSON in %q", strings.TrimSpace(out))
	}
	var v struct {
		Rules []struct {
			RuleTag string `json:"ruleTag"`
		} `json:"rules"`
	}
	if err := json.Unmarshal([]byte(out[i:j+1]), &v); err != nil {
		return nil, fmt.Errorf("lsrules: %w", err)
	}
	tags := make([]string, 0, len(v.Rules))
	for _, r := range v.Rules {
		tags = append(tags, r.RuleTag)
	}
	return tags, nil
}

func firstInt(re *regexp.Regexp, out string) int {
	m := re.FindStringSubmatch(out)
	if m == nil {
		return -1
	}
	n, err := strconv.Atoi(m[1])
	if err != nil {
		return -1
	}
	return n
}
```

- [ ] **Step 4: Запустить тест — GREEN**

Run: `cd server/backend && go test ./internal/xraydiff/ -v && go vet ./internal/xraydiff/`
Expected: 15 подтестов `--- PASS: TestClassify/…`, затем

```
--- PASS: TestClassify (0.00s)
--- PASS: TestClassifyExpectationsAndPayloads (0.00s)
--- PASS: TestParseCLIOutput (0.00s)
PASS
ok  	github.com/heyearth/backend/internal/xraydiff
```

`go vet` — без вывода.

- [ ] **Step 5: Commit**

```bash
git add server/backend/internal/xraydiff/
git commit -m "feat(xray): xraydiff — classify user-only vs structural config changes" -m "Pure classifier: only inbound clients and plan-* tagged routing rules may differ for a live (no-restart) apply; builds adu/adrules payloads and parses xray api CLI output. Table-driven tests." -m "Beads: heyearthproject-bfs4" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

#### Task 7c: Генератор конфига — одно правило на тариф с `ruleTag`, API-сервисы

**Files:**
- Modify: `server/backend/cmd/api/xray_sync.go` (`SyncXrayConfig`: блок `// ── Per-plan direct-routing rules ──`, цикл после комментария `// Per-plan routing rules: match user email → plan-specific balancer`, `"api"` → `"services"`)

Порядок правил не меняется: plan-direct — на прежнем месте (после блокировок, до socks-awg/masking), plan-users — на месте прежних per-user правил (после masking, перед catch-all). Внутри блока users порядок между тарифами не важен: у клиента ровно один `PlanID`, множества пользователей не пересекаются.

- [ ] **Step 1: plan-direct правила — `ruleTag` и сортировка пользователей**

В цикле `for _, planID := range directPlanIDs` после `if len(userIDs) == 0 { continue }` заменить два блока `if len(domains) > 0 {…}` и `if len(ips) > 0 {…}` на:

```go
		// One rule per plan with a stable ruleTag (xraydiff: plan-* rules are
		// user-only and are applied live through `xray api adrules`).
		sort.Strings(userIDs)
		if len(domains) > 0 {
			rules = append(rules, map[string]interface{}{
				"type":        "field",
				"ruleTag":     fmt.Sprintf("plan-%d-direct-domains", planID),
				"user":        userIDs,
				"domain":      domains,
				"outboundTag": "direct-freedom",
			})
		}
		if len(ips) > 0 {
			rules = append(rules, map[string]interface{}{
				"type":        "field",
				"ruleTag":     fmt.Sprintf("plan-%d-direct-ip", planID),
				"user":        userIDs,
				"ip":          ips,
				"outboundTag": "direct-freedom",
			})
		}
```

- [ ] **Step 2: одно правило `plan-<id>-users` вместо правила на пользователя**

Внутри `if len(exitTags) > 0 {` заменить комментарий `// Per-plan routing rules: match user email → plan-specific balancer` и следующий за ним цикл `for _, client := range activeClients {…}` (тот, что добавляет `"user": []string{client.UserID}`) на код ниже. Блок `// Default rule: all remaining traffic → default balancer…` (catch-all) остаётся сразу после него без изменений.

```go
		// Per-plan routing rules: ONE rule per plan (ruleTag plan-<id>-users) with the
		// sorted emails of its users → plan balancer. Each client has exactly one
		// PlanID, so user sets of different plans are disjoint and the semantics equal
		// the former one-rule-per-user list at the same position (before catch-all).
		planUsers := map[int][]string{}
		for _, client := range activeClients {
			if _, blocked := quotaExceeded[client.UUID]; blocked {
				continue // quota cutoff — user already missing from clients arrays
			}
			if _, ok := planBalancers[client.PlanID]; ok && client.UserID != "" {
				planUsers[client.PlanID] = append(planUsers[client.PlanID], client.UserID)
			}
		}
		planIDs := make([]int, 0, len(planUsers))
		for id := range planUsers {
			planIDs = append(planIDs, id)
		}
		sort.Ints(planIDs)
		for _, planID := range planIDs {
			users := planUsers[planID]
			sort.Strings(users)
			rules = append(rules, map[string]interface{}{
				"type":        "field",
				"ruleTag":     fmt.Sprintf("plan-%d-users", planID),
				"user":        users,
				"balancerTag": planBalancers[planID],
			})
		}
```

- [ ] **Step 3: Включить HandlerService и RoutingService**

В map `config` заменить блок `"api"`:

```go
		// API handler: stats + live user/rule changes (Task 7). Listens only via
		// the loopback dokodemo inbound api-in (127.0.0.1:10085).
		"api": map[string]interface{}{
			"tag": "api",
			"services": []string{
				"HandlerService",
				"RoutingService",
				"StatsService",
			},
		},
```

- [ ] **Step 4: Сборка**

Run: `cd server/backend && go build ./... && go vet ./...`
Expected: без вывода, exit 0.

- [ ] **Step 5: Commit** (без деплоя — деплой вместе с 7d)

```bash
git add server/backend/cmd/api/xray_sync.go
git commit -m "refactor(xray): one routing rule per plan with ruleTag; enable Handler/Routing API" -m "Per-user plan rules collapse into plan-<id>-users (sorted users, same position before catch-all); per-plan direct rules get plan-<id>-direct-domains/-ip tags. User sets of plans are disjoint, so routing semantics are unchanged. HandlerService and RoutingService are enabled for live user changes." -m "Beads: heyearthproject-bfs4" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

#### Task 7d: Применение user-only изменений через API xray, откат на рестарт

**Files:**
- Create: `server/backend/cmd/api/xray_live.go`
- Modify: `server/backend/cmd/api/xray_sync.go` (`SyncXrayConfig`: хвост функции от комментария `// Skip restart if config hasn't actually changed` до конца; imports)

Порядок применения: `rmu` (удалённые + изменённые) → `adu` (новые + изменённые, один вызов) → при `RoutingChanged`: `adrules` без `-append` + сверка `lsrules` с порядком конфига → `inboundusercount` по **каждому** inbound с клиентами == числу клиентов в конфиге → только после этого `rename` готового `.tmp.json` (он уже прошёл `xray run -test`). Любая ошибка/несовпадение → `xrayRuntimeStale = true` → обычный путь rename + `systemctl restart xray` (рестарт заодно лечит частично применённое состояние). `BumpSubscriptionVersion()` на user-only пути **не** вызывается: содержимое подписки других пользователей не меняется. Обращение через тот же бинарь `xrayBin`, что и `run -test` (ассеты `geoip.dat` находятся так же — проверено тем, что `run -test` уже работает из процесса API).

- [ ] **Step 1: Создать `cmd/api/xray_live.go`**

```go
package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/heyearth/backend/internal/xraydiff"
)

// ─────────────── Live (no-restart) user changes via the xray API ───────────────
// xraydiff.UserOnly diffs are pushed into the running xray through api-in
// (127.0.0.1:10085) with the xray CLI (Xray-core 26.3.27):
//   rmu  → HandlerService.AlterInbound(RemoveUser), one call per inbound
//   adu  → HandlerService.AlterInbound(AddUser), one call for all inbounds
//   adrules WITHOUT -append → RoutingService.AddRule: replaces ALL rules+balancers
//   lsrules / inboundusercount → verification
// Any error or mismatch is returned and SyncXrayConfig falls back to restart.

// xrayRuntimeStale: the running xray may differ from the config file (failed API
// apply / restart / rename) → next SyncXrayConfig restarts unconditionally.
// Guarded by xraySyncMu.
var xrayRuntimeStale bool

// runXrayAPI runs `xray api <sub> --server=127.0.0.1:10085 -timeout=10 <args>` and
// returns stdout. xray exits 1 (base.Fatalf) on dial/decode/build errors.
func runXrayAPI(xrayBin, sub string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	full := append([]string{"api", sub, "--server=" + xrayStatsAPIAddr, "-timeout=10"}, args...)
	cmd := exec.CommandContext(ctx, xrayBin, full...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return stdout.String(), fmt.Errorf("xray api %s: %w (%s %s)", sub, err,
			strings.TrimSpace(stderr.String()), strings.TrimSpace(stdout.String()))
	}
	return stdout.String(), nil
}

// runXrayAPIWithFile writes payload to a 0600 temp file next to the config,
// runs `xray api <sub> [flags] <file>` and removes the file.
func runXrayAPIWithFile(xrayBin, sub string, payload []byte) (string, error) {
	f, err := os.CreateTemp(filepath.Dir(xrayConfigPath), "live-api-*.json")
	if err != nil {
		return "", fmt.Errorf("%s temp file: %w", sub, err)
	}
	defer os.Remove(f.Name())
	if _, err := f.Write(payload); err != nil {
		f.Close()
		return "", fmt.Errorf("%s temp file: %w", sub, err)
	}
	if err := f.Close(); err != nil {
		return "", fmt.Errorf("%s temp file: %w", sub, err)
	}
	return runXrayAPI(xrayBin, sub, f.Name())
}

// applyUserOnlyDiff applies a UserOnly diff to the running xray and verifies it.
func applyUserOnlyDiff(xrayBin string, d xraydiff.Result) error {
	// 1. Remove deleted/modified users ("--" ends flag parsing before emails).
	for _, ic := range d.Inbounds {
		if len(ic.Removed) == 0 {
			continue
		}
		out, err := runXrayAPI(xrayBin, "rmu", append([]string{"-tag=" + ic.Tag, "--"}, ic.Removed...)...)
		if err != nil {
			return err
		}
		if n := xraydiff.ParseRemovedTotal(out); n != len(ic.Removed) {
			return fmt.Errorf("rmu %s: removed %d of %d: %s", ic.Tag, n, len(ic.Removed), strings.TrimSpace(out))
		}
	}
	// 2. Add new/modified users: one adu call, one full inbound object per tag.
	payload, total, err := d.AddUsersPayload()
	if err != nil {
		return fmt.Errorf("adu payload: %w", err)
	}
	if total > 0 {
		out, err := runXrayAPIWithFile(xrayBin, "adu", payload)
		if err != nil {
			return err
		}
		if n := xraydiff.ParseAddedTotal(out); n != total {
			return fmt.Errorf("adu: added %d of %d: %s", n, total, strings.TrimSpace(out))
		}
	}
	// 3. Routing: replace all rules + balancers, then live order must equal config order.
	if d.RoutingChanged {
		rp, err := d.RoutingPayload()
		if err != nil {
			return err
		}
		if _, err := runXrayAPIWithFile(xrayBin, "adrules", rp); err != nil {
			return err
		}
		out, err := runXrayAPI(xrayBin, "lsrules")
		if err != nil {
			return err
		}
		live, err := xraydiff.ParseRuleTags(out)
		if err != nil {
			return err
		}
		if !reflect.DeepEqual(live, d.RuleTags) {
			return fmt.Errorf("lsrules order mismatch: live %v, want %v", live, d.RuleTags)
		}
	}
	// 4. Every inbound with clients must hold exactly the configured number of users.
	tags := make([]string, 0, len(d.UserCounts))
	for tag := range d.UserCounts {
		tags = append(tags, tag)
	}
	sort.Strings(tags)
	for _, tag := range tags {
		out, err := runXrayAPI(xrayBin, "inboundusercount", "-tag="+tag)
		if err != nil {
			return err
		}
		got, err := xraydiff.ParseUserCount(out)
		if err != nil {
			return err
		}
		if got != d.UserCounts[tag] {
			return fmt.Errorf("inboundusercount %s = %d, want %d", tag, got, d.UserCounts[tag])
		}
	}
	return nil
}
```

- [ ] **Step 2: Встроить классификатор в `SyncXrayConfig`**

Заменить весь хвост `SyncXrayConfig` — от строки `// Skip restart if config hasn't actually changed (avoids dropping active connections)` (или канонического сравнения, если его ввела более ранняя задача) до последнего `return nil` перед закрывающей `}` функции — на код ниже. В нём уже есть `recordXrayRestart()` из Task 3; если Tasks 2–3 добавили после рестарта другие строки — перенести их в ветку после `xrayRuntimeStale = false`.

```go
	// ── Live path: user-only changes are applied through the xray API ──
	// Canonical comparison (parsed JSON) with the config xray runs from.
	// xrayRuntimeStale (set after a failed API apply/restart) forces a restart.
	if !xrayRuntimeStale {
		if existingConfig, err := os.ReadFile(xrayConfigPath); err == nil {
			diff, derr := xraydiff.Classify(existingConfig, configJSON)
			switch {
			case derr != nil:
				log.Printf("[xray-sync] diff error: %v — restart", derr)
			case diff.Kind == xraydiff.None:
				_ = os.Remove(tmpPath)
				log.Println("[xray-sync] Конфиг не изменился, пропускаем restart")
				return nil
			case diff.Kind == xraydiff.UserOnly:
				start := time.Now()
				if aerr := applyUserOnlyDiff(xrayBin, diff); aerr != nil {
					xrayRuntimeStale = true
					log.Printf("[xray-sync] user-only apply via API failed: %v — fallback to restart", aerr)
					RaiseAlert("xray-live-apply", AlertWarn, "Горячее применение пользователей не удалось", aerr.Error())
				} else {
					if err := os.Rename(tmpPath, xrayConfigPath); err != nil {
						_ = os.Remove(tmpPath)
						xrayRuntimeStale = true
						return fmt.Errorf("rename xray config after API apply: %w", err)
					}
					ResolveAlert("xray-live-apply", "Горячее применение пользователей снова работает")
					added, removed := diff.UserTotals()
					log.Printf("[xray-sync] user-only applied via API: +%d/-%d users, rules %v (routing pushed=%v), %d ms",
						added, removed, diff.ChangedRules, diff.RoutingChanged, time.Since(start).Milliseconds())
					return nil
				}
			default:
				log.Printf("[xray-sync] structural change (%s) — restart", diff.Reason)
			}
		}
	}

	// Atomic rename
	if err := os.Rename(tmpPath, xrayConfigPath); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("rename xray config: %w", err)
	}

	// Restart Xray: structural change, failed API apply or stale runtime.
	cmd := exec.Command("systemctl", "restart", "xray")
	if output, err := cmd.CombinedOutput(); err != nil {
		xrayRuntimeStale = true
		return fmt.Errorf("restart xray: %w (%s)", err, strings.TrimSpace(string(output)))
	}
	xrayRuntimeStale = false
	recordXrayRestart()

	// Notify clients that subscription config has changed.
	biz.BumpSubscriptionVersion()

	log.Printf("[xray-sync] Конфиг обновлён: %d VLESS клиентов, %d exit-нод, %d правил маскировки",
		len(vlessClients), len(exitTags), len(maskRouteEntries))

	return nil
```

Imports `xray_sync.go`: добавить `"github.com/heyearth/backend/internal/xraydiff"` (в группу `github.com/heyearth/backend/...`). `bytes` использовался только в удалённом `bytes.Equal`: `grep -n 'bytes\.' server/backend/cmd/api/xray_sync.go` → нет вывода → удалить строку `"bytes"` из imports (есть вывод — оставить). `time` уже импортирован.

- [ ] **Step 3: Сборка, vet, тесты**

Run: `cd server/backend && gofmt -l cmd/api internal/xraydiff && go build ./... && go vet ./... && go test ./internal/xraydiff/`
Expected: `gofmt`/`build`/`vet` без вывода; `ok  	github.com/heyearth/backend/internal/xraydiff`.

- [ ] **Step 4: Commit**

```bash
git add server/backend/cmd/api/xray_live.go server/backend/cmd/api/xray_sync.go
git commit -m "feat(xray): apply user-only config changes via xray API without restart" -m "SyncXrayConfig classifies the new config against the running one (xraydiff). User-only diffs go through xray api rmu/adu/adrules (full routing replace) and are verified with lsrules order and inboundusercount; the validated config is then renamed in place without systemctl restart. Any API error or mismatch marks the runtime stale and falls back to the restart path." -m "Beads: heyearthproject-bfs4" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

#### Task 7e: Деплой и доказательства

**Files:**
- Modify: `docs/research/2026-10-11-vpn-stability-research.md` (раздел «Task 7: горячие изменения — стенд и метрики»)

- [ ] **Step 1: Зафиксировать базовую метрику (до деплоя)**

Run: `cd server/backend && go run ./cmd/deploy --ssh 'journalctl -u xray --since "-24h" --no-pager | grep -c "Xray 26.3.27 started"'`
Expected: число рестартов за сутки после Task 1 (ориентир ≤ ~30) — записать.

- [ ] **Step 2: Деплой бэкенда** (см. «Соглашения»): `go build ./...` → stop + `quick_deploy.go` → `go run ./cmd/deploy --ssh 'systemctl is-active heyearth-api xray nginx'`
Expected: `active` ×3.

- [ ] **Step 3: Первая синхронизация — ровно один структурный рестарт**

Run: `go run ./cmd/deploy --ssh 'journalctl -u heyearth-api --since "-10min" --no-pager | grep -E "structural change|user-only|apply via API failed|Конфиг обновлён"'`
Expected: `structural change (changed api.services) — restart` и `Конфиг обновлён: …` (новые API-сервисы + переход на plan-правила); строк `apply via API failed` нет. Сразу проверить вход реальным клиентом (Берёза/Дуб/Бамбук через Happ).

- [ ] **Step 4: API работает, правила и счётчики совпадают с конфигом**

Run:
```bash
go run ./cmd/deploy --ssh '/opt/heyearth/xray/xray api lsrules --server=127.0.0.1:10085 | grep -c "\"ruleTag\": \"plan-"; grep -c "\"ruleTag\": \"plan-" /opt/heyearth/xray/config.json'
go run ./cmd/deploy --ssh "for t in vless-reality vless-xhttp; do /opt/heyearth/xray/xray api inboundusercount --server=127.0.0.1:10085 -tag=\$t; done; python3 -c \"import json;c=json.load(open('/opt/heyearth/xray/config.json'));print({i['tag']:len(i['settings']['clients']) for i in c['inbounds'] if 'clients' in i.get('settings',{})})\""
```
Expected: два одинаковых числа ≥ 1; `count` по каждому inbound равен числу клиентов из словаря python.

- [ ] **Step 5: Живое изменение без рестарта**

Попросить владельца выдать тестовому аккаунту подписку в админке (или дождаться естественного события — новая оплата/истечение). Во время события у владельца открыт Happ с долгим соединением (видео).
Run: `go run ./cmd/deploy --ssh 'journalctl -u heyearth-api --since "-30min" --no-pager | grep "user-only applied via API"; journalctl -u xray --since "-30min" --no-pager | grep -c "Xray 26.3.27 started"'`
Expected: строка `user-only applied via API: +2/-0 users, rules [plan-<id>-users] (routing pushed=true), NNN ms` (rules может быть `[plan-<id>-direct-…]` или `[]`, если у тарифа нет своего балансера) (+2 = vless-reality и vless-xhttp; +3 при включённом CDN), счётчик рестартов `0`; видео у владельца не прерывалось.

- [ ] **Step 6: Отсечка по квоте больше не рестартует xray** (при первом естественном превышении квоты)

Run: `go run ./cmd/deploy --ssh 'journalctl -u heyearth-api --since "-7d" --no-pager | grep -E "превысил cycle-лимит|quota cutoff|user-only applied|Конфиг обновлён" | tail -20'`
Expected: после первой строки `превысил cycle-лимит` для пользователя — `quota cutoff: 1 user(s) excluded…` и `user-only applied via API: +0/-2 users, rules […] …`; строки `Конфиг обновлён` в эту минуту нет. Повторные минутные `превысил` дают `Конфиг не изменился` (классификатор `None`).

- [ ] **Step 7: Метрики через сутки обычной работы**

Run:
```bash
go run ./cmd/deploy --ssh 'journalctl -u xray --since "-24h" --no-pager | grep -c "Xray 26.3.27 started"'
go run ./cmd/deploy --ssh 'journalctl -u heyearth-api --since "-24h" --no-pager | grep -c "user-only applied via API"; journalctl -u heyearth-api --since "-24h" --no-pager | grep -c "apply via API failed"'
go run ./cmd/deploy --ssh 'journalctl -u heyearth-api --since "-24h" --no-pager | grep -o "structural change (.*)" | sort | uniq -c'
```
Expected: рестартов **≤ 5**; `user-only applied` ≥ 1 (если были события подписок); `apply via API failed` = `0` (при > 0 — смотреть текст ошибки и алерт `xray-live-apply` в Telegram); структурные причины — только смена exit-нод/балансеров/настроек админки. Алерт `xray-restart-storm` (Task 3) не срабатывал.

- [ ] **Step 8: Follow-up по живым соединениям и документирование**

Если в стенде S6 `SURVIVED`:
Run: `bd create "xray: rmu не рвёт живые соединения — пользователь после отсечки по квоте держит открытые сессии" -t task -p 2 -d "Task 7: отсечка по квоте/истечению через rmu действует только на новые подключения; открытые (mux) живут до закрытия (connIdle 3600 с). Варианты: ночной рестарт при наличии таких пользователей, учёт трафика после отсечки, connIdle для level 0."`
Expected: `Created issue: heyearthproject-<hash>`.
Дописать в `docs/research/2026-10-11-vpn-stability-research.md` раздел «Task 7: горячие изменения — стенд и метрики»: вывод стенда (S1–S7, время `adrules`), рестартов/сутки до и после, число `user-only applied`, ссылка на follow-up.

- [ ] **Step 9: Commit**

```bash
git add docs/research/2026-10-11-vpn-stability-research.md
git commit -m "docs(vpn): task 7 stand results and live-apply metrics" -m "Records the xray api stand output (adu/rmu/adrules semantics on 26.3.27) and xray restarts/day before and after live user changes." -m "Beads: heyearthproject-bfs4" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---
### Task 8: Чистка — автотюнер, Shadowsocks :8388, лишние ufw-правила, socks/http, мёртвый код

**Beads:** эпик `heyearthproject-bfs4` · **Spec:** «Часть 4. Чистка»

**Files:**
- Create: `server/backend/migrations/000056_stabilization_cleanup.{up,down}.sql`, `server/backend/cmd/deploy/ops_hub.go`
- Modify: `cmd/api/auto_tuner.go` (`StartAutoTuner`), `internal/admin/auto_tuner_handlers.go` (`AutoTunerStatusHandler`)
- Modify: `cmd/api/xray_sync.go` (`SyncXrayConfig` inbounds, `buildExitNodeOutbound`)
- Modify: `cmd/api/main.go` (`handleUserConnections`), `cmd/api/subscription_handler.go` (`buildClashConfig`), `cmd/api/client_api.go` (`buildHubConnectionOptions`), `internal/biz/hysteria2_auth.go` (+`CanUseHysteria2`)
- Modify: `cmd/deploy/main.go` (`main`, `setupXray`, `seedConnectionSettings`, `doStatus`)
- Delete: `server/backend/internal/xray/`

(пути без префикса — от `server/backend/`)

> **Номер миграции.** 000056 предполагает, что Task 2 занял 000055. Перед созданием `ls server/backend/migrations | tail -2` и взять следующий номер **выше максимального** — golang-migrate применяет только версии больше текущей в `schema_migrations`, меньший номер, добавленный позже, молча пропустится. Занят — переименовать оба файла.

- [ ] **Step 1: Исходное состояние (RED)**

На Hub (`ssh root@138.16.181.225`):
```bash
ss -lntup | grep -E ':(1080|8080|8388|4443|5443|9443|44443|30443)\b' || echo "no listeners"
ufw status | grep -E '^(1080|8080|8388|4443|5443|9443|44443|30443)/'
q() { runuser -u postgres -- psql -d heyearth -tAc "$1"; }
q "SELECT value FROM app_settings WHERE key='auto_tuner_enabled'"; q "SELECT slug, allowed_protocols FROM plans ORDER BY id"
grep -c '"xPaddingBytes"' /opt/heyearth/xray/config.json
```
Expected: слушает только xray на `:8388` (tcp+udp); 10 устаревших ufw-правил (+`(v6)`); `true`; есть тарифы с `socks`/`http`; счётчик ≥ 3.
Локально: `cd server/backend && grep -rn 'backend/internal/xray"' --include=*.go .` → пусто (`internal/xraystats` — другой пакет).

- [ ] **Step 2: Миграция**

`migrations/000056_stabilization_cleanup.up.sql`:
```sql
-- Subproject 1, Task 8 (spec 2026-10-11, part 4).
-- 1) Auto-tuner off: it silently rotated Reality SNI / Hysteria2 obfs (research C3, H2).
UPDATE app_settings SET value = 'false' WHERE key = 'auto_tuner_enabled';
INSERT INTO app_settings (key, value) VALUES ('auto_tuner_enabled', 'false') ON CONFLICT (key) DO NOTHING;

-- 2) SOCKS/HTTP user inbounds are gone (000041). Snapshot for an exact down migration.
CREATE TABLE IF NOT EXISTS plans_protocols_backup_000056 AS
  SELECT id, allowed_protocols FROM plans WHERE allowed_protocols && ARRAY['socks','http']::text[];
UPDATE plans
   SET allowed_protocols = CASE
         WHEN cardinality(array_remove(array_remove(allowed_protocols, 'socks'), 'http')) = 0
           THEN ARRAY['vless']::text[]
         ELSE array_remove(array_remove(allowed_protocols, 'socks'), 'http') END,
       updated_at = NOW()
 WHERE allowed_protocols && ARRAY['socks','http']::text[];

-- 3) Settings that described the removed inbounds (1080 / 8080 / 8388).
DELETE FROM app_settings WHERE key IN ('socks5_port', 'http_proxy_port', 'ss_port', 'ss_method');
```
`migrations/000056_stabilization_cleanup.down.sql`:
```sql
UPDATE plans p SET allowed_protocols = b.allowed_protocols, updated_at = NOW()
  FROM plans_protocols_backup_000056 b WHERE b.id = p.id;
DROP TABLE IF EXISTS plans_protocols_backup_000056;
UPDATE app_settings SET value = 'true' WHERE key = 'auto_tuner_enabled';
INSERT INTO app_settings (key, value) VALUES ('socks5_port', '1080'), ('http_proxy_port', '8080'),
  ('ss_port', '8388'), ('ss_method', '2022-blake3-aes-128-gcm') ON CONFLICT (key) DO NOTHING;
```

- [ ] **Step 3: Автотюнер выключен по умолчанию**

В `cmd/api/auto_tuner.go` (2 места: начало `StartAutoTuner` и проверка в `case <-ticker.C:`) и `internal/admin/auto_tuner_handlers.go` заменить `GetSettingOrDefault("auto_tuner_enabled", "true")` → `GetSettingOrDefault("auto_tuner_enabled", "false")`. Код тюнера остаётся.
Run: `grep -rn 'auto_tuner_enabled", "true"' cmd internal` → пусто.

- [ ] **Step 4: xray_sync.go — без inbound `shadowsocks` и без `xPaddingBytes` в realitySettings**

В `SyncXrayConfig` из среза `inbounds := []map[string]interface{}{` удалить элемент `{ "tag": "shadowsocks", "listen": "0.0.0.0", "port": 8388, … }` (3 строки + скобки). В `realitySettings` обоих inbound (`vless-reality`, `vless-xhttp`) удалить строку `"xPaddingBytes": "100-1000",` (в `xhttpSettings` она остаётся — там валидна); итог:
```go
				"realitySettings": map[string]interface{}{
					"show": false, "dest": realitySNI + ":443", "xver": 0,
					"serverNames": []string{realitySNI, "www." + realitySNI},
					"privateKey":  keys.PrivateKey,
					"shortIds":    []string{keys.ShortID, ""},
				},
```
В `buildExitNodeOutbound`, `case "vless":` → `"realitySettings": map[string]interface{}{"fingerprint": getFingerprint(), "serverName": sni},`. Если Task 4 внёс `"shadowsocks"` в `inboundTag` блок-правила «нет здоровых exit» — убрать.
Run: `cd server/backend && gofmt -l cmd/api; grep -n 'xPaddingBytes' cmd/api/xray_sync.go; grep -n '"shadowsocks"' cmd/api/xray_sync.go`
Expected: gofmt пусто; одна строка `xPaddingBytes` (в `xhttpSettings`); `"shadowsocks"` только в `case "shadowsocks":` и `"protocol": "shadowsocks"` exit-outbound/health-check.

- [ ] **Step 5: Бамбук — только по тарифу; без socks/http в /connections и Clash**

`internal/biz/hysteria2_auth.go`, после `ValidateHysteria2Auth`:
```go
// CanUseHysteria2 — the same plan gate as ValidateHysteria2Auth (express_enabled AND
// 'hysteria2'/'all' allowed). Builders use it so «Бамбук» is never offered to a plan
// the auth backend would reject.
func CanUseHysteria2(userID string) bool {
	var allowed []string
	var enabled bool
	err := db.DB.QueryRow(`
		SELECT p.allowed_protocols, p.express_enabled
		FROM user_subscriptions us
		JOIN users u ON u.id = us.user_id
		JOIN plans p ON p.id = us.plan_id
		WHERE u.user_id = $1 AND us.valid_until > NOW() AND us.status = 'active'
		ORDER BY us.valid_until DESC LIMIT 1
	`, userID).Scan(pq.Array(&allowed), &enabled)
	return err == nil && enabled && containsProto(allowed, "hysteria2")
}
```
`cmd/api/client_api.go`, `buildHubConnectionOptions`, блок `// 2. Hysteria2 «Бамбук»`: строку `	if mainServerHost != "" {` прямо перед `hy2Password, hy2Err := biz.GetUserHysteria2Password(userID)` → `	if mainServerHost != "" && biz.CanUseHysteria2(userID) { // same gate as the auth backend`.
`cmd/api/subscription_handler.go`, `buildClashConfig`: строки `canSOCKS := …`, `canHTTP := …` удалить, `canHysteria2 := biz.IsProtocolAllowed(userID, "hysteria2")` → `canHysteria2 := biz.CanUseHysteria2(userID)`; удалить блоки `// 2. SOCKS` и `// 3. HTTP proxy` (до `	namesList := ""`).
`cmd/api/main.go`, `handleUserConnections`: всё от `// Подключения доступны только при активной подписке` до `writeJSON(w, http.StatusOK, resp)` заменить на:
```go
	// Подключения только при активной подписке. socks5_*/http_proxy_* больше не отдаются:
	// inbound-ы 1080/8080 удалены, site/ и flutter-app/ эти поля не читают (grep 2026-10-12).
	if access.CanConnect {
		options := buildHubConnectionOptions(userID)
		resp["connection_options"] = options
		for _, opt := range options {
			if opt.Protocol == "vless" {
				resp["vless_reality"] = opt.Uri
			}
		}
	}
	writeJSON(w, http.StatusOK, resp)
```
Run: `grep -n 'socks5_\|http_proxy_\|canSOCKS\|canHTTP' cmd/api/*.go` → пусто.

- [ ] **Step 6: Удалить `internal/xray`**

Run: `cd server/backend && grep -rn 'backend/internal/xray"' --include=*.go . ; git rm -r internal/xray`
Expected: grep пусто; `rm '…/config_generator.go'`, `rm '…/tunnel_manager.go'`.

- [ ] **Step 7: Деплой-код больше не создаёт и не открывает 1080/8080/8388**

`cmd/deploy/main.go`, `setupXray`: в bootstrap-`config.json` удалить inbound-объекты `shadowsocks`, `socks5-proxy`, `http-proxy` (массив кончается на `}` объекта `vless-reality`, затем `  ],`); `echo "[xray] Конфиг записан (…)"` → `echo "[xray] Конфиг записан (VLESS:10443; остальное генерирует heyearth-api)"`; `Description=Xray Service (VLESS Reality + Shadowsocks + Proxy)` → `Description=Xray Service (VLESS Reality)`. Секцию `# ── 5. Открыть порты в firewall ──` целиком заменить на:
```sh
# ── 5. Firewall: только порты со слушателем (spec 2026-10-11, часть 4) ──
# 443 → nginx stream (SNI → 8443 / 10443); 2443 → Xray XHTTP. 1080/8080/8388 удалены — не открывать.
if command -v ufw >/dev/null 2>&1; then
  for r in 22/tcp 80/tcp 443/tcp 2443/tcp; do ufw allow "$r" 2>/dev/null || true; done
  ufw --force enable 2>/dev/null || true
  ufw status numbered 2>/dev/null || true
else
  iptables -I INPUT -p tcp --dport 2443 -j ACCEPT 2>/dev/null || true
fi
```
Секция `# ── 7.`: `ss -tlnp | grep -E ':(10443|8388|1080|8080)\s' || echo …` → `ss -tlnp | grep -E ':10443\s' || echo "ВНИМАНИЕ: Xray не слушает 10443!"`.
`seedConnectionSettings`, первый `INSERT … VALUES`: оставить только `('main_server_host', '$MAIN_HOST', CURRENT_TIMESTAMP), ('vless_port', '443', CURRENT_TIMESTAMP)` (строки `socks5_port`, `http_proxy_port`, `ss_port`, `ss_method` удалить).
`doStatus`: `grep -E '10443|:443 |8388|1080|8080'` → `grep -E ':(443|2443|10443) '`.

- [ ] **Step 8: `--setup-ops`: чистка ufw с проверкой `ss` перед каждым правилом**

Create `cmd/deploy/ops_hub.go`:
```go
package main

// Hub ops hygiene: `deploy --setup-ops`. Server-side changes of subproject 1 parts 4–5
// live here so a reinstall reproduces them. Idempotent.

import (
	"log"

	"golang.org/x/crypto/ssh"
)

func setupOpsHub(client *ssh.Client, cfg *deployConfig) {
	pruneStaleFirewallRules(client)
}

// Deletes ufw allow-rules of ports without a listener; a port that still has one is
// kept and reported. Not in the list (kept): 22,80,443,2443,8444/tcp; 443,50443,51820,30000:65000/udp.
const pruneFirewallScript = `set -u
command -v ufw >/dev/null 2>&1 || { echo "[ops] ufw not installed — skip"; exit 0; }
prune() {
  rule="$1"; proto="$2"; port="${rule%%:*}"
  if [ "$proto" = tcp ]; then n=$(ss -Hlnt "sport = :$port" | wc -l); else n=$(ss -Hlnu "sport = :$port" | wc -l); fi
  if [ "$n" -gt 0 ]; then echo "[ops] KEEP $rule/$proto — listener:"; ss -Hlntup "sport = :$port"; return; fi
  if ufw status | grep -qE "^$rule/$proto[[:space:]]"; then
    ufw --force delete allow "$rule/$proto" >/dev/null && echo "[ops] DELETED $rule/$proto (no listener)"
  else
    echo "[ops] ABSENT $rule/$proto"
  fi
}
for p in 1080 8080 8388 4443 5443 9443 44443 30443; do prune "$p" tcp; done
for p in 1080 8388; do prune "$p" udp; done
ufw status
`

func pruneStaleFirewallRules(client *ssh.Client) {
	if _, err := runSSH(client, pruneFirewallScript, true); err != nil {
		log.Fatalf("[ops] ufw prune: %v", err)
	}
}
```
`cmd/deploy/main.go`, `main`: после `sshCmd := flag.String(…)` — `setupOpsFlag := flag.Bool("setup-ops", false, "эксплуатация Hub: чистка ufw, бэкапы, fail2ban, journald, nginx stream timeout")`; в `switch` перед `case *sshCmd != "":` — `case *setupOpsFlag:` / `mode = "setup-ops"`; после блока `if mode == "fix-db" {…}` — `if mode == "setup-ops" { setupOpsHub(client, cfg); return }`; в текст `log.Fatal("Укажи один из флагов: …")` добавить `--setup-ops, `.

- [ ] **Step 9: Сборка** — `cd server/backend && go build ./... && go vet ./cmd/api/ ./cmd/deploy/ ./internal/biz/ ./internal/admin/` → без вывода.

- [ ] **Step 10: Деплой бэкенда и проверка**

Деплой по «Соглашениям». На Hub:
```bash
systemctl is-active heyearth-api xray; q "SELECT version, dirty FROM schema_migrations"; q "SELECT slug, allowed_protocols FROM plans ORDER BY id"
python3 -c "import json;c=json.load(open('/opt/heyearth/xray/config.json'));r=lambda x:x.get('streamSettings',{}).get('realitySettings',{});print([i['tag'] for i in c['inbounds']],[x['tag'] for x in c['inbounds']+c['outbounds'] if 'xPaddingBytes' in r(x)])"
ss -lntup | grep -E ':8388\b' || echo "8388 closed"
```
Expected: `active active`; `51|f`; нет `socks`/`http` в тарифах; список inbound без `shadowsocks`, второй список `[]`; `8388 closed`.

- [ ] **Step 11: Чистка ufw на проде (через код)**

Run: `cd server/backend && go run ./cmd/deploy --setup-ops`
Expected: `[ops] DELETED …` для 1080, 8080, 8388, 4443, 5443, 9443, 44443, 30443 /tcp и 1080, 8388 /udp; ни одного `KEEP` (если есть — разобраться со слушателем, правило остаётся); в `ufw status` только 22, 80, 443, 2443, 8444 /tcp и 443, 50443, 51820, 30000:65000 /udp (+v6). Повтор — только `ABSENT`.

- [ ] **Step 12: Выдача и автотюнер (GREEN)**

На Hub:
```bash
S="SELECT u.subscribe_token FROM users u JOIN user_subscriptions us ON us.user_id=u.id JOIN plans p ON p.id=us.plan_id WHERE us.valid_until>NOW() AND us.status='active' AND u.subscribe_token IS NOT NULL AND"
G="p.express_enabled AND p.allowed_protocols && ARRAY['hysteria2','all']"
T_NO=$(q "$S NOT ($G) LIMIT 1"); T_YES=$(q "$S $G LIMIT 1"); [ -n "$T_NO" ] && [ -n "$T_YES" ] && echo tokens-ok
for T in "$T_NO" "$T_YES"; do curl -s -A 'Happ/3.0' "http://127.0.0.1:8000/api/v1/user/subscribe/$T" | grep -c '^hysteria2://'; done
curl -s "http://127.0.0.1:8000/api/v1/user/subscribe/$T_YES?format=clash" | grep -cE 'type: (socks5|http)$'
journalctl -u heyearth-api --since "-30min" --no-pager | grep -m1 'auto-tuner'
```
Expected: `tokens-ok`; `0` и `2`; `0`; `[auto-tuner] Disabled via setting`. Токены не печатаются. Реальным Happ: Берёза/Дуб у пользователя без Express работают, у Express-пользователя есть Бамбук.

- [ ] **Step 13: Commit**
```bash
git add -A server/backend/migrations server/backend/cmd server/backend/internal/biz/hysteria2_auth.go server/backend/internal/admin/auto_tuner_handlers.go server/backend/internal/xray
git commit -m "chore(cleanup): disable auto-tuner, drop shadowsocks/socks/http leftovers and dead internal/xray" -m "Bamboo only for plans passing the Express gate; deploy no longer opens 1080/8080/8388; --setup-ops prunes listener-less ufw rules." -m "Beads: heyearthproject-bfs4" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 9: Эксплуатация — ежедневный бэкап с копией на exit, fail2ban, journald, nginx stream timeout

**Beads:** эпик `heyearthproject-bfs4` · **Spec:** «Часть 5. Эксплуатация»

**Files:**
- Modify: `server/backend/cmd/deploy/ops_hub.go`, `server/backend/cmd/deploy/main.go` (`main`: `--backup-target`; `buildNginxStreamConf`)
- Create: `docs/runbooks/restore-hub-backup.md`

**Копия вне Hub — отдельный SSH-ключ с forced command, не sshpass.** `servers.ssh_password` хранится зашифрованным (AES-256-GCM), bash-таймер его не расшифрует, а пароль открытым текстом в файле на Hub — тот же root-секрет exit. Ключ `/root/.ssh/heyearth_backup_ed25519` разрешён на exit только как `command="/usr/local/sbin/heyearth-backup-receive",no-pty,no-port-forwarding,…`: украденный ключ умеет лишь положить `backup-*.tar.gz` (имя проверяется regex), shell не даёт; переживает смену пароля (подпроект 4).

- [ ] **Step 1: Предпроверка**

Hub: `systemd-analyze calendar '*-*-* 04:00:00 Europe/Moscow' | grep -i 'next elapse'; q "SELECT pg_size_pretty(pg_database_size('heyearth'))"; df -h /opt/heyearth | tail -1; journalctl --disk-usage; systemctl is-active fail2ban; grep -n proxy_timeout /etc/nginx/stream.d/heyearth.conf`. Exit (`ssh root@89.124.89.126`): `df -h /var/backups | tail -1`. Локально: `grep -c '^EXIT_NODE_SSH_PASSWORD=' server/.env` → `1` (root-пароль exit вписывает владелец; `.env` не коммитится).
Expected: next elapse 04:00 MSK; на exit свободно ≥ 7× размера БД; journal ≈ 3.9G; fail2ban `inactive`; `proxy_timeout` отсутствует.

- [ ] **Step 2: `proxy_timeout` в генераторе nginx stream**

`cmd/deploy/main.go`, `buildNginxStreamConf`: в блоке `server {` между `ssl_preread on;` и `proxy_pass $backend;` вставить
```
    # = xray connIdle 3600s; default 10m cut idle long-lived sessions behind the SNI router.
    proxy_timeout 3600s;
```

- [ ] **Step 3: Бэкап, проверка восстановления, таймер (ops_hub.go)**

Импорт в `ops_hub.go` → `"encoding/base64"`, `"fmt"`, `"log"`, `"strings"`, `"golang.org/x/crypto/ssh"`. Дописать:
```go
const backupScript = `#!/usr/bin/env bash
# Installed by deploy --setup-ops (cmd/deploy/ops_hub.go). DO NOT EDIT. Restore: docs/runbooks/restore-hub-backup.md
set -euo pipefail
umask 077
source /etc/heyearth/backup.env
TS=$(date +%Y%m%d-%H%M%S); ARCHIVE="$BACKUP_DIR/backup-$TS.tar.gz"
WORK=$(mktemp -d "$BACKUP_DIR/.work-XXXXXX"); trap 'rm -rf "$WORK"' EXIT
fail() { echo "$(date -Is) $*" > "$BACKUP_DIR/last-error"; echo "ERROR: $*" >&2; exit 1; }
runuser -u postgres -- pg_dump -Fc -d heyearth > "$WORK/db.dump" || fail "pg_dump failed"
USERS=$(runuser -u postgres -- psql -d heyearth -tAc "SELECT count(*) FROM users") || fail "users count failed"
printf 'created_at=%s\nusers_count=%s\nhost=%s\n' "$(date -Is)" "$USERS" "$(hostname)" > "$WORK/meta.txt"
PATHS=()
for p in opt/heyearth/state opt/heyearth/mainserver/.env opt/heyearth/mainserver/certs opt/heyearth/hysteria \
         etc/letsencrypt etc/nginx/conf.d etc/nginx/stream.d; do
  if [ -e "/$p" ]; then PATHS+=("$p"); fi
done
tar -C / -czf "$WORK/files.tar.gz" "${PATHS[@]}" || fail "tar of config files failed"
tar -C "$WORK" -czf "$ARCHIVE.part" db.dump meta.txt files.tar.gz || fail "final tar failed"
mv -f "$ARCHIVE.part" "$ARCHIVE"
ls -1t "$BACKUP_DIR"/backup-*.tar.gz | tail -n +$((KEEP + 1)) | xargs -r rm -f --
date -Is > "$BACKUP_DIR/last-success"
echo "local: $ARCHIVE ($(du -h "$ARCHIVE" | cut -f1), users=$USERS)"
if [ -z "${TARGET_HOST:-}" ]; then echo "WARN: no TARGET_HOST, offsite skipped" >&2; rm -f "$BACKUP_DIR/last-error"; exit 0; fi
SUM=$(sha256sum "$ARCHIVE" | cut -d' ' -f1)
for i in 1 2 3; do
  if RESP=$(ssh -i "$SSH_KEY" -p "$TARGET_PORT" -o BatchMode=yes -o ConnectTimeout=20 -o ServerAliveInterval=15 \
        "root@$TARGET_HOST" "backup-$TS.tar.gz" < "$ARCHIVE" 2>"$WORK/ssh.err") && [ "$RESP" = "OK $SUM" ]; then
    date -Is > "$BACKUP_DIR/last-offsite"; rm -f "$BACKUP_DIR/last-error"
    echo "offsite: $TARGET_HOST OK (sha256 $SUM)"; exit 0
  fi
  echo "offsite attempt $i failed: ${RESP:-} $(cat "$WORK/ssh.err")" >&2; sleep 30
done
fail "offsite copy to $TARGET_HOST failed after 3 attempts (local archive kept)"
`

// Restores an archive into scratch DB heyearth_restore_test and compares users count
// with meta.txt. Usage: heyearth-backup-verify.sh [archive] (default: newest).
const backupVerifyScript = `#!/usr/bin/env bash
set -euo pipefail
umask 077
ARCHIVE="${1:-$(ls -1t /opt/heyearth/backups/backup-*.tar.gz | sed -n 1p)}"
WORK=$(mktemp -d)
cleanup() { runuser -u postgres -- dropdb --if-exists heyearth_restore_test || true; rm -rf "$WORK"; }
trap cleanup EXIT
tar -C "$WORK" -xzf "$ARCHIVE" db.dump meta.txt
EXPECTED=$(sed -n 's/^users_count=//p' "$WORK/meta.txt")
runuser -u postgres -- dropdb --if-exists heyearth_restore_test
runuser -u postgres -- createdb heyearth_restore_test
runuser -u postgres -- pg_restore --no-owner --no-acl --exit-on-error -d heyearth_restore_test < "$WORK/db.dump"
GOT=$(runuser -u postgres -- psql -d heyearth_restore_test -tAc "SELECT count(*) FROM users")
MIG=$(runuser -u postgres -- psql -d heyearth_restore_test -tAc "SELECT version || ' dirty=' || dirty FROM schema_migrations")
echo "archive=$ARCHIVE users_expected=$EXPECTED users_restored=$GOT schema=$MIG"
if [ "$EXPECTED" = "$GOT" ]; then echo "RESTORE-TEST: OK"; else echo "RESTORE-TEST: MISMATCH"; exit 1; fi
`

const backupServiceUnit = "[Unit]\nDescription=HiEarth Hub backup (pg_dump + state + certs, offsite copy)\nWants=network-online.target\nAfter=network-online.target postgresql.service\n\n[Service]\nType=oneshot\nExecStart=/opt/heyearth/bin/heyearth-backup.sh\nNice=10\nIOSchedulingClass=idle\nTimeoutStartSec=30min\n"

const backupTimerUnit = "[Unit]\nDescription=Daily HiEarth Hub backup at 04:00 MSK\n\n[Timer]\nOnCalendar=*-*-* 04:00:00 Europe/Moscow\nPersistent=true\n\n[Install]\nWantedBy=timers.target\n"

// installBackupTimer writes files via base64 (no heredoc quoting), creates the
// backup SSH key once and enables the timer.
func installBackupTimer(client *ssh.Client, target string, port int) {
	env := fmt.Sprintf("BACKUP_DIR=/opt/heyearth/backups\nKEEP=14\nTARGET_HOST=%s\nTARGET_PORT=%d\nSSH_KEY=/root/.ssh/heyearth_backup_ed25519\n", target, port)
	files := []struct{ path, mode, content string }{
		{"/etc/heyearth/backup.env", "600", env},
		{"/opt/heyearth/bin/heyearth-backup.sh", "700", backupScript},
		{"/opt/heyearth/bin/heyearth-backup-verify.sh", "700", backupVerifyScript},
		{"/etc/systemd/system/heyearth-backup.service", "644", backupServiceUnit},
		{"/etc/systemd/system/heyearth-backup.timer", "644", backupTimerUnit},
	}
	var sb strings.Builder
	sb.WriteString("set -e\nmkdir -p /etc/heyearth /opt/heyearth/bin /opt/heyearth/backups /root/.ssh\nchmod 700 /opt/heyearth/backups /root/.ssh\n")
	for _, f := range files {
		fmt.Fprintf(&sb, "echo '%s' | base64 -d > %s\nchmod %s %s\n", base64.StdEncoding.EncodeToString([]byte(f.content)), f.path, f.mode, f.path)
	}
	sb.WriteString("[ -f /root/.ssh/heyearth_backup_ed25519 ] || ssh-keygen -q -t ed25519 -N '' -C heyearth-backup@hub -f /root/.ssh/heyearth_backup_ed25519\n" +
		"systemctl daemon-reload\nsystemctl enable --now heyearth-backup.timer\nsystemctl list-timers heyearth-backup.timer --no-pager\n")
	if _, err := runSSH(client, sb.String(), true); err != nil {
		log.Fatalf("[ops] backup timer: %v", err)
	}
}
```

- [ ] **Step 4: Приёмник на exit и ключ (ops_hub.go)**

Дописать:
```go
// Runs on the exit node: forced-command receiver + authorized_keys entry for the Hub key only.
const backupReceiverInstallScript = `set -e
mkdir -p /var/backups/heyearth-hub && chmod 700 /var/backups/heyearth-hub
cat > /usr/local/sbin/heyearth-backup-receive <<'EOF'
#!/usr/bin/env bash
# Installed by Hub deploy --setup-ops. Stores stdin as /var/backups/heyearth-hub/<name>, keeps 7.
set -euo pipefail
umask 077
DIR=/var/backups/heyearth-hub; KEEP=7; NAME="${SSH_ORIGINAL_COMMAND:-}"
if [ "$NAME" = "ping" ]; then echo PONG; exit 0; fi
if ! [[ "$NAME" =~ ^backup-[0-9]{8}-[0-9]{6}\.tar\.gz$ ]]; then echo "rejected: $NAME" >&2; exit 2; fi
cat > "$DIR/$NAME.part"; mv -f "$DIR/$NAME.part" "$DIR/$NAME"
ls -1t "$DIR"/backup-*.tar.gz | tail -n +$((KEEP + 1)) | xargs -r rm -f --
echo "OK $(sha256sum "$DIR/$NAME" | cut -d' ' -f1)"
EOF
chmod 755 /usr/local/sbin/heyearth-backup-receive
mkdir -p /root/.ssh && chmod 700 /root/.ssh && touch /root/.ssh/authorized_keys
grep -v 'heyearth-backup@hub' /root/.ssh/authorized_keys > /root/.ssh/authorized_keys.new || true
echo '__AUTH_LINE__' >> /root/.ssh/authorized_keys.new
mv -f /root/.ssh/authorized_keys.new /root/.ssh/authorized_keys && chmod 600 /root/.ssh/authorized_keys
`

func installOffsiteBackupKey(hub *ssh.Client, cfg *deployConfig, target, password string, port int) {
	pub, err := runSSH(hub, "cat /root/.ssh/heyearth_backup_ed25519.pub", true)
	pub = strings.TrimSpace(pub)
	if err != nil || !strings.HasPrefix(pub, "ssh-ed25519 ") {
		log.Fatalf("[ops] backup public key missing on Hub: %v", err)
	}
	if password == "" {
		password = cfg.env.get("EXIT_NODE_SSH_PASSWORD", cfg.deployPassword)
	}
	exit, err := sshDial(fmt.Sprintf("%s:%d", target, port), "root", password, "")
	if err != nil {
		log.Fatalf("[ops] SSH to backup target %s:%d: %v", target, port, err)
	}
	defer exit.Close()
	authLine := `command="/usr/local/sbin/heyearth-backup-receive",no-pty,no-port-forwarding,no-agent-forwarding,no-X11-forwarding ` + pub
	if _, err := runSSH(exit, strings.ReplaceAll(backupReceiverInstallScript, "__AUTH_LINE__", authLine), true); err != nil {
		log.Fatalf("[ops] install receiver on %s: %v", target, err)
	}
	hostKey, err := runSSH(exit, "cat /etc/ssh/ssh_host_ed25519_key.pub", true)
	f := strings.Fields(hostKey)
	if err != nil || len(f) < 2 {
		log.Fatalf("[ops] read exit host key: %v", err)
	}
	known := target
	if port != 22 {
		known = fmt.Sprintf("[%s]:%d", target, port)
	}
	probe := fmt.Sprintf("set -e\nssh-keygen -R '%[1]s' >/dev/null 2>&1 || true\necho '%[1]s %[2]s %[3]s' >> /root/.ssh/known_hosts\n"+
		"ssh -i /root/.ssh/heyearth_backup_ed25519 -p %[4]d -o BatchMode=yes -o ConnectTimeout=20 root@%[5]s ping\n", known, f[0], f[1], port, target)
	if out, err := runSSH(hub, probe, true); err != nil || !strings.Contains(out, "PONG") {
		log.Fatalf("[ops] offsite SSH probe failed: %v", err)
	}
	fmt.Println("[ops] offsite backup key installed, probe: PONG")
}
```

- [ ] **Step 5: journald, fail2ban, nginx stream и новый `setupOpsHub`**

Заменить `setupOpsHub` и дописать:
```go
// setupOpsHub — deploy --setup-ops. Idempotent; re-run after any reinstall.
func setupOpsHub(client *ssh.Client, cfg *deployConfig, backupTarget, exitPassword string, exitSSHPort int) {
	pruneStaleFirewallRules(client)
	script := strings.ReplaceAll(hostHygieneScript, "__IGNORE_IP__", backupTarget)
	if _, err := runSSH(client, script, true); err != nil {
		log.Fatalf("[ops] journald/fail2ban/nginx: %v", err)
	}
	installBackupTimer(client, backupTarget, exitSSHPort)
	if backupTarget == "" {
		fmt.Println("[ops] WARN: --backup-target не задан — копия бэкапа вне Hub НЕ настроена")
		return
	}
	installOffsiteBackupKey(client, cfg, backupTarget, exitPassword, exitSSHPort)
}

// journald cap, fail2ban sshd jail (5 fails / 10 min -> 1 h; the backup target is in
// ignoreip so the Hub is always reachable from the exit), and stream proxy_timeout on
// the live file (buildNginxStreamConf carries it for reinstalls).
const hostHygieneScript = `set -e
mkdir -p /etc/systemd/journald.conf.d
printf '# Installed by deploy --setup-ops\n[Journal]\nSystemMaxUse=1G\n' > /etc/systemd/journald.conf.d/heyearth.conf
systemctl restart systemd-journald
journalctl --vacuum-size=1G >/dev/null 2>&1 || true
echo "[ops] journald: $(journalctl --disk-usage)"
if ! command -v fail2ban-client >/dev/null 2>&1; then
  timeout 300 apt-get update -qq || true
  DEBIAN_FRONTEND=noninteractive timeout 600 apt-get install -y -qq fail2ban python3-systemd
fi
printf '# Installed by deploy --setup-ops\n[sshd]\nenabled = true\nbackend = systemd\nmaxretry = 5\nfindtime = 10m\nbantime = 1h\nignoreip = 127.0.0.1/8 ::1 __IGNORE_IP__\n' > /etc/fail2ban/jail.d/heyearth-sshd.conf
systemctl enable fail2ban >/dev/null 2>&1 && systemctl restart fail2ban && sleep 2
fail2ban-client status sshd
F=/etc/nginx/stream.d/heyearth.conf
if [ -f "$F" ] && ! grep -q proxy_timeout "$F"; then
  cp -p "$F" "$F.bak"
  sed -i 's/^\( *\)proxy_pass \$backend;/\1proxy_timeout 3600s;\n\1proxy_pass $backend;/' "$F"
  if nginx -t; then systemctl reload nginx; else mv -f "$F.bak" "$F"; echo "[ops] nginx -t failed, restored"; exit 1; fi
fi
grep -n proxy_timeout "$F" || echo "[ops] no stream conf"
`
```
`cmd/deploy/main.go`, `main`: после `setupOpsFlag := …` — `backupTarget := flag.String("backup-target", "", "IP exit-ноды для копии бэкапа (--setup-ops); пароль: --exit-node-password или EXIT_NODE_SSH_PASSWORD")`; вызов → `setupOpsHub(client, cfg, *backupTarget, *exitNodePassword, *exitNodeSSHPort)`.

- [ ] **Step 6: Runbook** — Create `docs/runbooks/restore-hub-backup.md`:
````markdown
# Восстановление Hub из бэкапа

Бэкап: `heyearth-backup.timer`, 04:00 MSK (`go run ./cmd/deploy --setup-ops --backup-target <exit-IP>`).
Hub: `/opt/heyearth/backups/backup-YYYYmmdd-HHMMSS.tar.gz` (14 шт.); exit 89.124.89.126: `/var/backups/heyearth-hub/` (7 шт.).
Архив: `db.dump` (`pg_dump -Fc heyearth`), `meta.txt` (`users_count`), `files.tar.gz` (`/opt/heyearth/state` — ключи Reality, `/opt/heyearth/mainserver/.env` и `certs` — mTLS CA, `/opt/heyearth/hysteria`, `/etc/letsencrypt`, `/etc/nginx/conf.d`, `/etc/nginx/stream.d`). Статус: `/opt/heyearth/backups/last-success`, `last-offsite`, `last-error`. Ручной бэкап: `systemctl start heyearth-backup`.

## Проверка без восстановления (ежемесячно)
`/opt/heyearth/bin/heyearth-backup-verify.sh [архив]` → `RESTORE-TEST: OK` (временная БД `heyearth_restore_test`, сверка числа пользователей, БД удаляется).

## Полное восстановление на новом Hub
1. `go run ./cmd/deploy --main` с новым `DEPLOY_HOST` в `server/.env`.
2. `scp root@89.124.89.126:/var/backups/heyearth-hub/backup-<ts>.tar.gz /root/` (по паролю root exit; ключ бэкапа умеет только писать).
3. `systemctl stop heyearth-api xray hysteria2-server 'hysteria2-client-*'`; `mkdir -p /root/restore && tar -C /root/restore -xzf /root/backup-<ts>.tar.gz`.
4. БД: `OWNER=$(runuser -u postgres -- psql -tAc "SELECT pg_get_userbyid(datdba) FROM pg_database WHERE datname='heyearth'")`; `runuser -u postgres -- pg_restore --clean --if-exists --no-owner --role="$OWNER" -d heyearth < /root/restore/db.dump`; `SELECT count(*) FROM users` = `users_count` из `meta.txt`.
5. Файлы: `tar -C / -xzf /root/restore/files.tar.gz`.
6. `systemctl daemon-reload && systemctl start heyearth-api && sleep 5 && systemctl restart xray hysteria2-server 'hysteria2-client-*' nginx`.
7. `go run ./cmd/deploy --status`; Берёза/Дуб/Бамбук реальным Happ; `curl -s https://hiearth.ru/api/v1/health`.
8. `go run ./cmd/deploy --setup-ops --backup-target 89.124.89.126`.
Новый IP → клиентам нужен refresh подписки (`main_server_host`); ключи Reality из архива сохраняют ссылки валидными.

## Частично
Только ключи Reality: из `files.tar.gz` взять `opt/heyearth/state/reality-keys.json` → `/opt/heyearth/state/` → `systemctl restart heyearth-api`. Только БД: шаги 3–4, 6. `schema_migrations.dirty = t` — прерванная миграция, не рестор: `journalctl -u heyearth-api`.

## fail2ban
`fail2ban-client status sshd`; разбан `fail2ban-client set sshd unbanip <IP>`. С exit (в `ignoreip`) Hub доступен всегда.
````

- [ ] **Step 7: Сборка** — `cd server/backend && go build ./... && go vet ./cmd/deploy/` → без вывода.

- [ ] **Step 8: Выполнить на проде**

Run: `cd server/backend && go run ./cmd/deploy --setup-ops --backup-target 89.124.89.126`
Expected: только `[ops] ABSENT …` (ufw вычищен в Task 8); `[ops] journald: … take up` ≤ 1.0G; `Status for the jail: sshd`; `proxy_timeout 3600s;` в выводе grep; строка `heyearth-backup.timer` с NEXT 04:00 MSK; `[ops] offsite backup key installed, probe: PONG`. Повторный запуск — тот же результат без изменений.

- [ ] **Step 9: Первый бэкап и копия вне Hub**

Hub: `systemctl start heyearth-backup.service; systemctl show heyearth-backup -p Result; journalctl -u heyearth-backup -n 5 --no-pager | grep -E 'local:|offsite:'; sha256sum "$(ls -1t /opt/heyearth/backups/backup-*.tar.gz | sed -n 1p)"; ls /opt/heyearth/backups/last-error 2>/dev/null || echo no-error`. Exit: `ls -l /var/backups/heyearth-hub/; sha256sum /var/backups/heyearth-hub/backup-*.tar.gz`.
Expected: `Result=success`; `local: …backup-2026….tar.gz (…, users=N)`; `offsite: 89.124.89.126 OK (sha256 …)`; хеши Hub и exit совпадают, файлы `-rw-------`; `no-error`.

- [ ] **Step 10: Тестовое восстановление** — Hub: `/opt/heyearth/bin/heyearth-backup-verify.sh; runuser -u postgres -- psql -lqt | grep -c heyearth_restore_test`
Expected: `users_expected=N users_restored=N schema=51 dirty=f`, `RESTORE-TEST: OK`, затем `0`.

- [ ] **Step 11: Остальная evidence** — `nginx -T 2>/dev/null | grep proxy_timeout; journalctl --disk-usage; sleep 600; fail2ban-client status sshd | grep -E 'Total failed|Currently banned'`
Expected: `proxy_timeout 3600s;`; ≤ 1.0G; `Total failed` > 0 (публичный :22 перебирают постоянно). Через сутки: свежий архив ~04:00 MSK в `ls -lt /opt/heyearth/backups`, `last-offsite` — сегодня (алерт «бэкап > 26 ч» из Task 3 молчит).

- [ ] **Step 12: Commit**
```bash
git add server/backend/cmd/deploy/ops_hub.go server/backend/cmd/deploy/main.go docs/runbooks/restore-hub-backup.md
git commit -m "feat(ops): daily offsite backups, fail2ban, journald cap, nginx stream timeout via --setup-ops" -m "Timer 04:00 MSK: pg_dump -Fc + keys/certs/.env, keep 14, sha256-verified copy to the exit through a forced-command SSH key; verify script restores into a scratch DB. fail2ban sshd 5/10m->1h, journald 1G, stream proxy_timeout 3600s." -m "Beads: heyearthproject-bfs4" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 10: Hysteria v2.6.1 → v2.13.0 и бенчмарк канала Hub → exit

**Beads:** эпик `heyearthproject-bfs4` · **Spec:** «Часть 3. Канал Hub → exit»

**Files:**
- Create: `server/backend/internal/config/versions.go`, `server/backend/internal/biz/hub_provisioner_test.go`
- Modify: `cmd/deploy/hysteria2_hub.go` (удалить `hysteria2Version`), `cmd/deploy/main.go` (`setupExitNodeDeploy`: exit-скрипт и `hy2ClientScript`), `internal/admin/servers_handlers.go` («Шаг 3: Hysteria2», `hy2Setup`)
- Modify: `internal/biz/hub_provisioner.go` (`hysteriaClientYAML` + `HubTunnelParams`, `LoadHubTunnelParams`, `SyncHysteriaClientConfig`), `cmd/api/hysteria2_sync.go` (`StartHysteria2SyncLoop`, +`syncHubTunnelClients`)
- Modify: `docs/research/2026-10-11-vpn-stability-research.md` (§7)

**Цель:** `app/v2.13.0` (Latest, 05.10.2026; сверено со страницей releases 2026-10-12). **Changelog 2.6.1 → 2.13.0, что касается нас:**
- **2.8.2 — несовместимость:** клиент ≥ 2.8.2 → сервер < 2.8.2 = UDP-форвардинг не работает (TCP ок); новый сервер со старыми клиентами работает ⇒ **сначала exit (сервер), потом Hub** (там и клиент туннеля, и сервер для Happ). Откат — в обратном порядке: сначала Hub, потом exit.
- 2.8.0: `congestion: {type: bbr|reno, bbrProfile: standard|conservative|aggressive}` (нужно вариантам 3a/3b); без `bandwidth` у направления — BBR, с `bandwidth` — Brutal; `bandwidth` сервера ограничивает только Brutal. 2.8.1: краш сервера при BBR/Reno-клиенте (⇒ ≥ 2.8.2). 2.7.0: исправлен BBR, славший быстрее полосы.
- 2.11.0: Chrome QUIC parrot у клиента по умолчанию — с ним `maxIdleTimeout` фиксирован 30 s, Ed25519-серт сервера ломает handshake (у exit ECDSA P-256, у Hub LE — ок).
- 2.9.2 security (обход UDP ACL, OOM в sniff); 2.12.1 сервер шлёт stateless reset; 2.13.0 `sniff.timeout` только TCP, удалён TUN `route.strict`; 2.6.4 `pinSHA256` только leaf (не используем).
- Наши ключи (`masquerade.type: 404`, `auth.http.insecure`, `trafficStats`, `acl.geoip/inline`, `outbounds.socks5.addr`, `quic.disablePathMTUDiscovery`) в 2.13.0 на месте (сверено с `app/cmd/server.go`). Прежний бинарь хранится как `/usr/local/bin/hysteria.bak`.

- [ ] **Step 1: Инвентаризация**

Hub:
```bash
hysteria version | awk '$1=="Version:"'; systemctl list-units 'hysteria2*' --no-pager --plain | awk '{print $1, $3, $4}'
sed 's/^auth: .*/auth: <redacted>/' /opt/heyearth/hysteria/client-2.yaml; ss -lntup | grep -E ':(1090|1091|1190|1290)\b'
A=$(printf '%s' "$(awk '$1=="auth:"{print $2}' /opt/heyearth/hysteria/client-2.yaml)" | sha256sum)
B=$(printf '%s' "$(q "SELECT tunnel_ss_password FROM servers WHERE id=2")" | sha256sum); [ "$A" = "$B" ] && echo AUTH-MATCH || echo AUTH-DIFFERS
```
Exit: `hysteria version | awk '$1=="Version:"'; systemctl list-units 'hysteria*' --no-pager --plain; grep -A2 '^bandwidth' /opt/heyearth/hysteria/server.yaml`
Expected: `v2.6.1` на обоих; активны `hysteria2-server`, `hysteria2-client-2` (Hub), `hysteria2` (exit); `server: 89.124.89.126:<PORTS>` — **записать `<PORTS>`** (ожидается `20000-50000`); `bandwidth 100/100` на обоих; слушает только 1090; `AUTH-MATCH` (при `AUTH-DIFFERS` — STOP: генератор Step 9 возьмёт auth из БД; сначала согласовать `servers.tunnel_ss_password` с exit).

- [ ] **Step 2: Одна константа версии + установщик с обновлением и `.bak`**

Create `internal/config/versions.go`:
```go
package config

// HysteriaVersion — the single pinned apernet/hysteria release for every install path
// (cmd/deploy/hysteria2_hub.go, cmd/deploy/main.go setupExitNodeDeploy,
// internal/admin/servers_handlers.go). Upgrade exits (servers) before the Hub:
// 2.8.2+ clients lose UDP against older servers.
const HysteriaVersion = "v2.13.0"

// HysteriaDownloadURL — linux-amd64 asset; the tag "app/vX.Y.Z" has its slash encoded.
func HysteriaDownloadURL() string {
	return "https://github.com/apernet/hysteria/releases/download/app%2F" + HysteriaVersion + "/hysteria-linux-amd64"
}

// HysteriaInstallScript — idempotent shell: installs/upgrades /usr/local/bin/hysteria to
// HysteriaVersion, checks the download runs, keeps the previous binary as hysteria.bak.
// Does not restart units.
func HysteriaInstallScript() string {
	return `HY_WANT="` + HysteriaVersion + `"
HY_CUR=$(/usr/local/bin/hysteria version 2>/dev/null | awk '$1=="Version:"{print $2}')
if [ "$HY_CUR" != "$HY_WANT" ]; then
  if curl -fsSL -o /usr/local/bin/hysteria.new "` + HysteriaDownloadURL() + `" && chmod +x /usr/local/bin/hysteria.new && /usr/local/bin/hysteria.new version >/dev/null; then
    if [ -x /usr/local/bin/hysteria ]; then cp -p /usr/local/bin/hysteria /usr/local/bin/hysteria.bak; fi
    mv -f /usr/local/bin/hysteria.new /usr/local/bin/hysteria
    echo "[hysteria] installed $HY_WANT (previous: ${HY_CUR:-none} -> /usr/local/bin/hysteria.bak)"
  else
    rm -f /usr/local/bin/hysteria.new; echo "[hysteria] ERROR: download/verify of $HY_WANT failed, binary unchanged" >&2; false
  fi
else
  echo "[hysteria] $HY_WANT already installed"
fi
`
}
```
Замены (импорт `"github.com/heyearth/backend/internal/config"` в `cmd/deploy/hysteria2_hub.go` и `cmd/deploy/main.go`; в `servers_handlers.go` он уже есть):
1. `hysteria2_hub.go`: удалить `const hysteria2Version = "v2.6.1"`; блок от `# ── 1. Install hysteria binary ──` до первого `fi` → строки `# ── 1. Install / upgrade hysteria binary (previous kept as hysteria.bak) ──` и `__HYSTERIA_INSTALL__`; хвост `` `, "__VERSION__", hysteria2Version) `` → `` `, "__HYSTERIA_INSTALL__", config.HysteriaInstallScript()) ``.
2. `main.go`, `setupExitNodeDeploy`, exit-скрипт: блок `if [ ! -f /usr/local/bin/hysteria ]; then … echo "[hy2] Binary уже существует"` + `fi` → `%s`; хвост `` `, exitIP) `` → `` `, config.HysteriaInstallScript(), exitIP) `` (единственный другой глагол — `IP: %s` ниже, порядок сохраняется).
3. Там же `hy2ClientScript`: блок `# Установка Hysteria2 binary если нет` … `fi` → `# Установка/обновление Hysteria2 binary` + `%s`; в аргументы `fmt.Sprintf` первым вставить `config.HysteriaInstallScript(), `.
4. `servers_handlers.go`, «Шаг 3: Hysteria2»: `` runCmd(client, `set -e `` + блок `if [ ! -f … ]; then curl … fi` → `` runCmd(client, "set -e\n"+config.HysteriaInstallScript()+` `` (дальше со строки `mkdir -p /opt/heyearth/certs /opt/heyearth/hysteria` без изменений).
5. Там же `hy2Setup`: блок `if [ ! -f … ]; then … fi` → `%s`; хвост `` `, serviceName, serviceName) `` → `` `, config.HysteriaInstallScript(), serviceName, serviceName) ``.

Run: `cd server/backend && grep -rn 'v2\.6\.1' --include=*.go . ; go build ./... && go vet ./cmd/deploy/ ./internal/admin/ ./internal/config/`
Expected: grep пусто (комментарии про «v2.6.x»/SIGHUP — не пины, остаются); сборка без вывода.
```bash
git add server/backend/internal/config/versions.go server/backend/cmd/deploy server/backend/internal/admin/servers_handlers.go
git commit -m "chore(hysteria): pin v2.13.0 in one constant with upgrade-aware installer" -m "Five install paths hardcoded v2.6.1 and installed only if absent; the installer now upgrades in place, verifies the binary and keeps hysteria.bak." -m "Beads: heyearthproject-bfs4" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

- [ ] **Step 3: Скрипты бенчмарка на Hub и замер на 2.6.1**

`/root/hy2-bench/variant.sh` — тот же YAML, что `biz.hysteriaClientYAML` после Step 9 (без ACL-блока — на скорость не влияет):
```bash
#!/usr/bin/env bash
# Usage: variant.sh v1|v2 <mbps>|v3a|v3b   env: PORT (1090), OUT (client-2.yaml)
set -euo pipefail
ORIG=/opt/heyearth/hysteria/client-2.yaml.orig
SERVER=$(awk '$1=="server:"{print $2}' "$ORIG"); AUTH=$(awk '$1=="auth:"{print $2}' "$ORIG")
PORT=${PORT:-1090}; OUT=${OUT:-/opt/heyearth/hysteria/client-2.yaml}
case "${1:-}" in
  v1)  CC=$(printf 'bandwidth:\n  up: 100 mbps\n  down: 100 mbps') ;;
  v2)  CC=$(printf 'bandwidth:\n  up: %s mbps\n  down: %s mbps' "$2" "$2") ;;
  v3a) CC=$(printf 'congestion:\n  type: bbr\n  bbrProfile: standard') ;;
  v3b) CC=$(printf 'congestion:\n  type: bbr\n  bbrProfile: aggressive') ;;
  *) echo "usage: $0 v1|v2 <mbps>|v3a|v3b" >&2; exit 2 ;;
esac
cat > "$OUT" <<EOF
server: $SERVER
auth: $AUTH
tls:
  insecure: true
transport:
  udp:
    hopInterval: 30s
quic:
  initStreamReceiveWindow: 8388608
  maxStreamReceiveWindow: 8388608
  initConnReceiveWindow: 20971520
  maxConnReceiveWindow: 20971520
$CC
socks5:
  listen: 127.0.0.1:$PORT
EOF
chmod 600 "$OUT"
```
`/root/hy2-bench/measure.sh`:
```bash
#!/usr/bin/env bash
# Usage: measure.sh <label> <port> [port2 port3] — several ports = parallel downloads, speeds summed.
set -uo pipefail
LABEL=$1; shift
URL="https://speed.cloudflare.com/__down?bytes=25000000"
one() { curl -s --max-time 90 --socks5-hostname "127.0.0.1:$1" -o /dev/null -w '%{speed_download}' "$URL"; }
runs=()
for i in 1 2 3; do
  files=(); pids=()
  for p in "$@"; do f=$(mktemp); files+=("$f"); one "$p" > "$f" & pids+=($!); done
  wait "${pids[@]}"; runs+=("$(awk '{t+=$1} END{printf "%.0f", t}' "${files[@]}")"); rm -f "${files[@]}"
done
MED=$(printf '%s\n' "${runs[@]}" | sort -n | sed -n 2p)
RTT=$(for i in 1 2 3 4 5; do curl -s --socks5-hostname "127.0.0.1:$1" -o /dev/null -w '%{time_starttransfer}\n' https://www.gstatic.com/generate_204; done | sort -n | sed -n 3p)
awk -v l="$LABEL" -v m="$MED" -v r="$RTT" 'BEGIN{printf "%s\t%.2f MB/s\t%.1f Mbit/s\tRTT %.0f ms\n", l, m/1048576, m*8/1e6, r*1000}' | tee -a /root/hy2-bench/results.tsv
```
Run (ночь MSK): `chmod +x /root/hy2-bench/*.sh; cp -p /opt/heyearth/hysteria/client-2.yaml /opt/heyearth/hysteria/client-2.yaml.orig; /root/hy2-bench/measure.sh V0-as-is-2.6.1 1090`
Expected: строка порядка `V0-as-is-2.6.1  1.80 MB/s  15.1 Mbit/s  RTT … ms` (research P4).

- [ ] **Step 4: Обновить exit (сервер) первым**

Exit:
```bash
curl -fsSL -o /usr/local/bin/hysteria.new "https://github.com/apernet/hysteria/releases/download/app%2Fv2.13.0/hysteria-linux-amd64" && chmod +x /usr/local/bin/hysteria.new
/usr/local/bin/hysteria.new version | awk '$1=="Version:"'
timeout 3 /usr/local/bin/hysteria.new server -c /opt/heyearth/hysteria/server.yaml --disable-update-check 2>&1 | tail -2
cp -p /usr/local/bin/hysteria /usr/local/bin/hysteria.bak && mv -f /usr/local/bin/hysteria.new /usr/local/bin/hysteria
systemctl restart hysteria2 && sleep 2 && systemctl is-active hysteria2
```
Expected: `Version: v2.13.0`; проверка конфига кончается `address already in use` (YAML разобран, порт держит живой сервер), не ошибкой парсинга; `active`. Hub: `/root/hy2-bench/measure.sh V0-exit-2.13 1090` → ненулевая скорость. Откат exit: `cp -p /usr/local/bin/hysteria.bak /usr/local/bin/hysteria && systemctl restart hysteria2`.

- [ ] **Step 5: Обновить Hub через код, проверить Бамбук**

Run: `cd server/backend && go run ./cmd/deploy --ssh 'curl -fsSL -o /tmp/hy-new "https://github.com/apernet/hysteria/releases/download/app%2Fv2.13.0/hysteria-linux-amd64" && chmod +x /tmp/hy-new && (timeout 3 /tmp/hy-new server --config /etc/hysteria/server.yaml --disable-update-check 2>&1 | tail -2); rm -f /tmp/hy-new'`
Expected: `address already in use` (конфиг Hub, включая `masquerade.type: 404`, принят).
Run: `go run ./cmd/deploy --setup-hysteria2-hub && go run ./cmd/deploy --ssh 'systemctl restart hysteria2-client-2; systemctl is-active --quiet hysteria2-client-express && systemctl restart hysteria2-client-express; sleep 3; systemctl is-active hysteria2-server hysteria2-client-2'`
Expected: `[hysteria] installed v2.13.0 (previous: v2.6.1 -> /usr/local/bin/hysteria.bak)`, `[hysteria2-hub] hysteria2-server is active`, `active active`. Hub: `/root/hy2-bench/measure.sh V0-as-is-2.13 1090` — ненулевая.
Бамбук (владелец, Happ на Wi-Fi): обновить подписку → «🎋 Бамбук 1 only WiFi» → https://2ip.ru = 89.124.89.126; то же «Бамбук 2». Сервер: `journalctl -u hysteria2-server --since "-10min" --no-pager | grep -c 'client connected'` ≥ 1. Не подключается → откат Hub (`cp -p /usr/local/bin/hysteria.bak /usr/local/bin/hysteria && systemctl restart hysteria2-server hysteria2-client-2`) и разбор.

- [ ] **Step 6: Бенчмарк вариантов 1–3 (одна ночная сессия)**

Exit, собственная полоса: `for i in 1 2 3; do curl -s -o /dev/null -w '%{speed_download}\n' 'https://speed.cloudflare.com/__down?bytes=25000000'; done | sort -n | sed -n 2p | awk '{printf "%d\n", $1*8/1e6}'` → `B` Мбит/с; `BW = min(100, floor(0.9 × B))` (100 — `bandwidth` сервера exit, выше Brutal не поднимется).
Hub (каждый вариант = перезапись client-2.yaml + рестарт туннеля ≈ 2 с простоя):
```bash
cd /root/hy2-bench; BW=<BW>
./variant.sh v1 && diff <(grep -v '^#' /opt/heyearth/hysteria/client-2.yaml.orig) /opt/heyearth/hysteria/client-2.yaml
for v in v1 "v2 $BW" v3a v3b; do ./variant.sh $v && systemctl restart hysteria2-client-2 && sleep 5 && ./measure.sh "V-${v// /-}" 1090; done
```
Expected: diff — только окна QUIC/ACL/пустые строки, `server`/`auth` одинаковы; 4 строки `V-v1`, `V-v2-<BW>`, `V-v3a`, `V-v3b` в `results.tsv`. **Лучший одиночный `S`** — максимальная медиана при RTT ≤ 1,2 × RTT `V-v1`.

- [ ] **Step 7: Бенчмарк варианта 4 — 3 параллельных туннеля**
```bash
cd /root/hy2-bench; ./variant.sh <S> && systemctl restart hysteria2-client-2
for k in 1 2; do OUT=/root/hy2-bench/t$k.yaml PORT=$((1090 + 100*k)) ./variant.sh <S>
  systemd-run --unit=hy2-bench-t$k /usr/local/bin/hysteria client -c /root/hy2-bench/t$k.yaml --disable-update-check; done
sleep 5; ./measure.sh V4-base-1tunnel 1090 1090 1090; ./measure.sh V4-3tunnels 1090 1190 1290
systemctl stop hy2-bench-t1 hy2-bench-t2
cp -p /opt/heyearth/hysteria/client-2.yaml.orig /opt/heyearth/hysteria/client-2.yaml && systemctl restart hysteria2-client-2
```
(`<S>` — аргументы лучшего одиночного, напр. `v3a`.) Expected: две строки; `hysteria2-client-2` active; 1190/1290 свободны. **V4 побеждает**, если `V4-3tunnels` ≥ 1,3 × `V4-base-1tunnel`.

- [ ] **Step 8: Результаты — в ресёрч**

Дописать в `docs/research/2026-10-11-vpn-stability-research.md` (числа из `cat /root/hy2-bench/results.tsv`):
```markdown
## 7. Бенчмарк канала Hub → exit (Hysteria v2.13.0, 2026-10-__)

Метод: `curl --socks5-hostname 127.0.0.1:<port>` 25 МБ с speed.cloudflare.com, 3 прогона, медиана; RTT — медиана 5 × `generate_204`. Ночь MSK, Hub 138.16.181.225 → exit 89.124.89.126; собственная полоса exit B = … Мбит/с.

| Вариант | Клиент Hub | МБ/с | Мбит/с | RTT, мс |
|---|---|---|---|---|
| V0 / V0' | как было: 2.6.1 / 2.13.0 | | | |
| V1 | Brutal 100/100 | | | |
| V2 | Brutal BW/BW (BW = …) | | | |
| V3a / V3b | BBR standard / aggressive | | | |
| V4-base / V4 | S: 3 загрузки через 1 туннель / через 3 туннеля | | | |

**Победитель:** … (медиана, RTT). Цель ≥ 5,6 МБ/с: достигнута / нет; упор в exit (steal 48 %, B) — предел железа, замена exit — подпроект 2.
```

- [ ] **Step 9: Победитель — в код (генератор client-<id>.yaml)**

`internal/biz/hub_provisioner.go`: импорты + `"bytes"`, `"strconv"`; после `type HysteriaClientConfig struct {…}`:
```go
// HubTunnelParams — congestion tuning of the Hub→exit Hysteria2 tunnel (benchmark:
// research doc §7). app_settings hub_tunnel_* override; defaults = benchmark winner.
type HubTunnelParams struct {
	CC          string // "brutal" | "bbr"
	BBRProfile  string // standard | conservative | aggressive
	UpMbps      int    // brutal only
	DownMbps    int    // brutal only
	ServerPorts string // exit port or hopping range after "IP:"
}

const (
	defaultHubTunnelCC          = "bbr"
	defaultHubTunnelBBRProfile  = "standard"
	defaultHubTunnelUpMbps      = 100
	defaultHubTunnelDownMbps    = 100
	defaultHubTunnelServerPorts = "20000-50000"
)

func LoadHubTunnelParams() HubTunnelParams {
	p := HubTunnelParams{
		CC:          GetSettingOrDefault("hub_tunnel_cc", defaultHubTunnelCC),
		BBRProfile:  GetSettingOrDefault("hub_tunnel_bbr_profile", defaultHubTunnelBBRProfile),
		UpMbps:      defaultHubTunnelUpMbps,
		DownMbps:    defaultHubTunnelDownMbps,
		ServerPorts: GetSettingOrDefault("hub_tunnel_server_ports", defaultHubTunnelServerPorts),
	}
	if n, err := strconv.Atoi(GetSettingOrDefault("hub_tunnel_up_mbps", "")); err == nil && n > 0 {
		p.UpMbps = n
	}
	if n, err := strconv.Atoi(GetSettingOrDefault("hub_tunnel_down_mbps", "")); err == nil && n > 0 {
		p.DownMbps = n
	}
	return p
}

// Brutal = explicit bandwidth; otherwise no bandwidth (it would switch that direction
// back to Brutal) + `congestion` (hysteria >= 2.8).
func (p HubTunnelParams) congestionYAML() string {
	if p.CC == "brutal" {
		return fmt.Sprintf("bandwidth:\n  up: %d mbps\n  down: %d mbps\n", p.UpMbps, p.DownMbps)
	}
	return fmt.Sprintf("congestion:\n  type: bbr\n  bbrProfile: %s\n", p.BBRProfile)
}

// SyncHysteriaClientConfig rewrites client-<id>.yaml only when the rendered content
// differs; changed=true tells the caller to restart hysteria2-client-<id>.
func SyncHysteriaClientConfig(c HysteriaClientConfig) (bool, error) {
	path := filepath.Join(hubHysteriaConfigDir, fmt.Sprintf("client-%d.yaml", c.ServerID))
	want := []byte(hysteriaClientYAML(c, LoadHubTunnelParams()))
	if cur, err := os.ReadFile(path); err == nil && bytes.Equal(cur, want) {
		return false, nil
	}
	if err := os.WriteFile(path+".tmp", want, 0o600); err != nil {
		return false, fmt.Errorf("write %s.tmp: %w", path, err)
	}
	if err := os.Rename(path+".tmp", path); err != nil {
		_ = os.Remove(path + ".tmp")
		return false, fmt.Errorf("rename %s: %w", path, err)
	}
	return true, nil
}
```
Константы выставить по победителю (в коде выше — V3a): V1 → `"brutal"`, 100/100; V2 → `"brutal"`, BW/BW; V3a → `"bbr"`/`"standard"`; V3b → `"bbr"`/`"aggressive"`; `defaultHubTunnelServerPorts` = `<PORTS>` из Step 1. Если победил V4 — константы по `S`, а параллельные туннели — отдельной задачей: `bd create "Hub→exit: parallel hysteria tunnels (V4 won benchmark)"` с дизайном: порты `1088+id+100k`, юниты `hysteria2-client-<id>-t<k>` (имя не матчит `ListHubHysteriaUnits`), outbounds `exit-<id>-t<k>` в `exitTags` и в `planTags` по префиксу `exit-<id>-t`.
В `hysteriaClientYAML`: сигнатура → `func hysteriaClientYAML(c HysteriaClientConfig, p HubTunnelParams) string`; первая строка шаблона → `# Generated by heyearth-api hub_provisioner.go for server id=%d. DO NOT EDIT BY HAND (tuning: app_settings hub_tunnel_*)`; `server: %s:20000-50000` → `server: %s:%s`; блок `bandwidth:` (3 строки) → `%s`; аргументы → `c.ServerID, c.ExitIP, p.ServerPorts, c.AuthKey, p.congestionYAML(), c.SocksPort`. В `WriteHysteriaClientConfig`: `hysteriaClientYAML(c)` → `hysteriaClientYAML(c, LoadHubTunnelParams())`.

Create `internal/biz/hub_provisioner_test.go`:
```go
package biz

import (
	"strings"
	"testing"
)

func TestHysteriaClientYAMLCongestion(t *testing.T) {
	c := HysteriaClientConfig{ServerID: 2, ExitIP: "203.0.113.7", AuthKey: "secret", SocksPort: 1090}
	cases := []struct {
		p        HubTunnelParams
		want     []string
		mustLack string
	}{
		{HubTunnelParams{CC: "brutal", UpMbps: 80, DownMbps: 80, ServerPorts: "20000-50000"},
			[]string{"server: 203.0.113.7:20000-50000\n", "bandwidth:\n  up: 80 mbps\n  down: 80 mbps\n", "listen: 127.0.0.1:1090\n"}, "congestion:"},
		{HubTunnelParams{CC: "bbr", BBRProfile: "aggressive", ServerPorts: "50443"},
			[]string{"server: 203.0.113.7:50443\n", "congestion:\n  type: bbr\n  bbrProfile: aggressive\n"}, "bandwidth:"},
	}
	for _, tc := range cases {
		got := hysteriaClientYAML(c, tc.p)
		for _, w := range tc.want {
			if !strings.Contains(got, w) {
				t.Errorf("cc=%s: missing %q in:\n%s", tc.p.CC, w, got)
			}
		}
		if strings.Contains(got, tc.mustLack) {
			t.Errorf("cc=%s: must not contain %q:\n%s", tc.p.CC, tc.mustLack, got)
		}
	}
}
```
`cmd/api/hysteria2_sync.go`, дописать:
```go
// syncHubTunnelClients converges every Hub→exit tunnel config to the canonical renderer
// (biz.hysteriaClientYAML + hub_tunnel_*), whichever path provisioned it (deploy CLI,
// admin panel, lifecycle FSM). Restarts only units whose file changed.
func syncHubTunnelClients() {
	if _, err := os.Stat("/opt/heyearth/hysteria"); err != nil {
		return
	}
	nodes, err := biz.ListAllExitNodes()
	if err != nil {
		log.Printf("[hub-tunnel] list exit nodes: %v", err)
		return
	}
	for _, n := range nodes {
		if n.TunnelProtocol != "hysteria2" || n.TunnelSSPass == "" {
			continue
		}
		unit := fmt.Sprintf("hysteria2-client-%d", n.ID)
		if exec.Command("systemctl", "cat", unit).Run() != nil {
			continue // not provisioned on this Hub — lifecycle FSM owns creation
		}
		changed, err := biz.SyncHysteriaClientConfig(biz.HysteriaClientConfig{
			ServerID: n.ID, ExitIP: n.IPAddress, AuthKey: n.TunnelSSPass, SocksPort: 1088 + n.ID,
		})
		if err != nil {
			log.Printf("[hub-tunnel] %s: %v", unit, err)
			continue
		}
		if !changed {
			continue
		}
		if out, err := exec.Command("systemctl", "restart", unit).CombinedOutput(); err != nil {
			log.Printf("[hub-tunnel] restart %s: %v (%s)", unit, err, strings.TrimSpace(string(out)))
			continue
		}
		log.Printf("[hub-tunnel] %s: config changed (cc=%s), restarted", unit, biz.LoadHubTunnelParams().CC)
	}
}
```
В `StartHysteria2SyncLoop` вызвать `syncHubTunnelClients()` после начального `SyncHysteria2Config()` и в `for range ticker.C {` после периодического.
Run: `cd server/backend && gofmt -l cmd/api internal/biz; go test ./internal/biz/ -run TestHysteriaClientYAMLCongestion -v && go build ./... && go vet ./cmd/api/ ./internal/biz/`
Expected: `--- PASS: TestHysteriaClientYAMLCongestion`, `ok`, иного вывода нет.

- [ ] **Step 10: Деплой генератора (GREEN)**

Деплой бэкенда по «Соглашениям». Через 1 мин на Hub:
```bash
journalctl -u heyearth-api --since "-3min" --no-pager | grep 'hub-tunnel'
sed 's/^auth: .*/auth: <redacted>/' /opt/heyearth/hysteria/client-2.yaml | sed -n '1,22p'
systemctl is-active hysteria2-client-2; /root/hy2-bench/measure.sh WINNER-generated 1090
sleep 330; journalctl -u heyearth-api --since "-6min" --no-pager | grep -c 'hub-tunnel.*restarted'
```
Expected: одна строка `[hub-tunnel] hysteria2-client-2: config changed (cc=<победитель>), restarted`; файл начинается `# Generated by heyearth-api hub_provisioner.go…`, `server: 89.124.89.126:<PORTS>`, блок победителя; `active`; медиана в пределах ±20 % от замера победителя; `0` повторных рестартов за период синхронизации. Бамбук и Берёза/Дуб реальным Happ работают.

- [ ] **Step 11: Commit**
```bash
git add server/backend/internal/biz/hub_provisioner.go server/backend/internal/biz/hub_provisioner_test.go server/backend/cmd/api/hysteria2_sync.go docs/research/2026-10-11-vpn-stability-research.md
git commit -m "perf(tunnel): render Hub->exit hysteria config from app_settings, benchmark winner as default" -m "Benchmarked Brutal 100/100, Brutal at measured bandwidth, BBR standard/aggressive and parallel tunnels on Hysteria 2.13.0 (research doc section 7); the winner is the code default, hub_tunnel_* override it, and the API converges every client-<id>.yaml to it." -m "Beads: heyearthproject-bfs4" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

## Self-review: покрытие спецификации

| Раздел спеки | Задачи | Статус |
|---|---|---|
| 1.1 Детерминированная сборка (хотфикс) | Task 1 | ✅ выполнено 2026-10-12 00:15 MSK: рестартов 40/ч → 1 за 11 мин (разовый), коммит `3e71722` |
| 1.2 Горячие изменения через API xray | Task 7 (7a стенд → 7e) | план |
| 2 Маршрутизация без утечек, health-check, мастер-UUID | Tasks 4, 5, 6 | план |
| 3 Канал Hub→exit (Hysteria + бенчмарк) | Task 10 | план |
| 4 Чистка | Task 8 | план |
| 5 Эксплуатация | Task 9 | план |
| 6 Мониторинг и алерты | Tasks 2, 3 (+ алерты exit в Task 5) | план |
| вне спеки: `quick_deploy.go` хардкодил старый IP | — | ✅ коммит `21f1063` |

## Уточнения и отступления от спеки (согласовать при исполнении)

1. **Task 4 — fallback балансеров.** Не всегда `block`: fallback = первый exit, который **наш** монитор (`checkTunnelHealth`, гистерезис 3/2) считает здоровым; `block` — только когда таких нет. Причина: observatory xray ~37 раз/сутки кратко помечает единственный exit «dead» — с `block` это стало бы 37 видимыми обрывами, с `direct` (как сейчас) — 37 утечками через RU IP Hub. Guard `checkNoDirectLeak` запрещает fallback на `direct`/freedom.
2. **Task 4 — DNS xray.** Явное правило `dns-out → direct`: встроенный DNS xray раньше уходил через default outbound; теперь RU-direct домены резолвятся с Hub (а не через Амстердам) — намеренная смена поведения.
3. **Task 7 — `adrules` заменяет весь блок `routing` (без `-append`)**, а не одно правило по тегу; порядок совпадает с файлом, проверка через `xray api lsrules`. Возможно короткое окно при перезаливке правил — измерить на стенде (7a). `rmu` не рвёт уже открытые соединения: отсечка по квоте действует на новые подключения (стенд S6; follow-up в 7e, если нужна мгновенная отсечка).
4. **Task 3 — RU-проба через check-host** делает только `check-tcp` (TLS-уровень даёт loop-проба через exit).
5. **Task 10 — вариант «2–3 параллельных туннеля»** в бенчмарке измеряется, но перманентная реализация вынесена в отдельную Beads-задачу (создаётся только если вариант победит).
6. **Миграции:** `000055_alert_events` (Task 2), `000056_stabilization_cleanup` (Task 8). Перед созданием сверить `ls server/backend/migrations | tail -2` — номер должен быть выше максимального.
7. **Окна обслуживания** (короткие, с автооткатом через `systemd-run`): Task 5 Steps 9–10 (~5 мин без exit, проверка детекта и блокировки). Согласовать время с владельцем.

## Execution handoff

Исполнение — `superpowers:subagent-driven-development` (свежий субагент на задачу + ревью спеки и качества) или `superpowers:executing-plans` инлайн, по задачам в порядке выше. После каждой задачи: build → деплой → evidence → commit → `bd` отметка.
