package main

// The automation rules of the console (docs/prd/stability-operations.prd.md, part 6; plan C6):
// the catalogue A1…A16 for the «Автоматика» tab and the pure decisions of the rules this engine
// runs itself (A1, A12, A14, A16). The others already run elsewhere («Builtin»): the tab shows
// them with their switch (a setting) or as always on. A2 is the tunnel monitor's: a dead exit
// leaves the balancers live (xray_exits.go), and comes back by itself once it lives again.

import (
	"fmt"
	"time"

	"github.com/heyearth/backend/internal/biz"
)

type autoMode string

const (
	autoOff    autoMode = "off"
	autoDryRun autoMode = "dry-run"
	autoOn     autoMode = "on"
)

type autoRule struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	When    string `json:"when"`
	Does    string `json:"does"`
	Limits  string `json:"limits"`
	Builtin bool   `json:"builtin"` // runs elsewhere; here a showcase and its switch
	Setting string `json:"setting"` // builtin: the setting that switches it ("" — always on)
	Note    string `json:"note"`
}

var autoRules = []autoRule{
	{ID: "A1", Title: "Вход теряет потоки", When: "доля проверок входа за 10 минут ниже 75 % (не меньше 8 проверок) или 3 недоступности подряд",
		Does:   "выводит вход из ротации, включает «Срочное обновление»",
		Limits: "не трогает последний здоровый путь; не чаще раза в час на вход; после двух автовыводов за неделю — только тревога; возврат всегда человеком, через тест"},
	{ID: "A2", Title: "Выход мёртв", When: "оба туннеля выхода не прошли 3 проверки подряд",
		Does:    "монитор туннелей убирает выход из всех балансировщиков вживую, без перезапуска xray (открытые соединения целы); после 2 удачных проверок возвращает его сам",
		Limits:  "последний здоровый выход так не убирается: без здоровых выходов весь трафик, кроме прямых RU-адресов, блокируется, и это перезапуск xray (К3), как раньше",
		Builtin: true,
		Note:    "Исходящие мёртвого выхода остаются в конфиге, меняются только селекторы — как у выхода «вне работы». Своего правила у движка нет: вывод «вне работы» держал бы выход и после того, как он ожил, до кнопки человека."},
	{ID: "A3", Title: "Канарейка: стало хуже после «Всем»", When: "страховочный час после «Всем» показал ухудшение",
		Does: "откатывает флаг, включает «Срочное обновление»", Limits: "один откат на изменение", Builtin: true, Setting: "canary_auto_rollback"},
	{ID: "A4", Title: "Сервис упал", When: "xray, Бамбук, nginx, туннель или API остановился",
		Does: "systemd поднимает его (Restart=on-failure)", Limits: "три падения за 10 минут — стоп и критичная тревога", Builtin: true},
	{ID: "A5", Title: "Выход Бамбука мёртв", When: "текущий выход Бамбука не отвечает", Does: "мгновенный переход на живой", Builtin: true},
	{ID: "A6", Title: "Бамбук: выход быстрее", When: "нашёлся выход быстрее на 40 % и больше",
		Does: "переключает Бамбук только в 04:00–05:30", Limits: "не чаще раза в сутки", Builtin: true, Setting: "bamboo_optimise"},
	{ID: "A7", Title: "Нарушитель квоты в сети", When: "отключённый пользователь всё ещё в сети",
		Does: "один перезапуск xray в ночное окно", Limits: "по умолчанию выключено; не чаще раза в сутки", Builtin: true, Setting: "xray_revoke_restart"},
	{ID: "A8", Title: "Тревога не доставлена", When: "Telegram не принял тревогу 15 минут", Does: "письмо на адрес владельца", Builtin: true, Setting: "alert_email"},
	{ID: "A9", Title: "Учебное восстановление", When: "раз в неделю", Does: "восстанавливает бэкап во временную базу", Limits: "провал — тревога", Builtin: true},
	{ID: "A10", Title: "Сроки", When: "домен < 60, 30, 14 дней; сертификат < 14 дней; обновление сертификата упало", Does: "тревога", Builtin: true},
	{ID: "A11", Title: "Узел ждёт обслуживания", When: "перезагрузка нужна дольше 14 дней; есть обновления безопасности",
		Does: "тревога и строка в «Работах»", Limits: "перезагрузка — только кнопкой", Builtin: true},
	{ID: "A12", Title: "node-agent молчит", When: "node-agent выхода не отвечает 3 проверки подряд при живом туннеле",
		Does: "перезапускает node-agent (на трафик не влияет)", Limits: "не больше 3 раз в час на выход, затем тревога"},
	{ID: "A13", Title: "Узел вернулся", When: "узел был недоступен 2 минуты и больше и вернулся", Does: "задание verify и запись в ленту", Limits: "не чаще раза в час на узел", Builtin: true},
	{ID: "A14", Title: "Диск хаба заполнен", When: "диск хаба занят больше чем на 85 %",
		Does: "чистит журнал до половины его лимита", Limits: "если не помогло — тревога о диске"},
	{ID: "A15", Title: "Изменено мимо пульта", When: "отслеживаемая настройка изменилась без записи в журнале", Does: "запись и тревога", Builtin: true},
	{ID: "A16", Title: "Снимок при критичной тревоге", When: "поднялась критичная тревога",
		Does: "собирает снимок: доска, туннели, ошибки журналов xray, Бамбука, API, nginx; внеочередной прогон синтетики", Limits: "не чаще раза в 10 минут на тревогу"},
}

func autoRuleByID(id string) autoRule {
	for _, r := range autoRules {
		if r.ID == id {
			return r
		}
	}
	return autoRule{}
}

// ── A1 ──

type share struct{ OK, Total, Checks int }

type pathHealth struct {
	ID      int // 0 — the Hub
	Healthy bool
	Known   bool // a fresh check exists; unknown counts as unhealthy
}

type a1Input struct {
	EntryID  int
	Shares   map[string]share // tcp443 | tcp2443 | udp over the last 10 minutes
	DownRuns int              // unreachable checks in a row
	Paths    []pathHealth     // the Hub and the other entries in rotation
	LastAuto []time.Time      // this entry's earlier automatic removals
}

type a1Decision struct {
	Remove  bool
	Reason  string // what fired ("" — nothing)
	Blocked string // why it does not act although it fired
}

const (
	a1MinChecks   = 8
	a1MinSharePct = 75
	a1DownRuns    = 3
	a1Gap         = time.Hour
	a1StreakSpan  = 7 * 24 * time.Hour
	a1StreakMax   = 2
)

func a1Decide(in a1Input, now time.Time) a1Decision {
	var d a1Decision
	for _, kind := range []string{biz.EntrySampleTCP443, biz.EntrySampleTCP2443, biz.EntrySampleUDP} {
		s, ok := in.Shares[kind]
		if !ok || s.Checks < a1MinChecks || s.Total <= 0 {
			continue
		}
		if pct := s.OK * 100 / s.Total; pct < a1MinSharePct {
			d.Reason = fmt.Sprintf("%s: %d %% потоков за 10 минут (%d проверок)", kind, pct, s.Checks)
			break
		}
	}
	if d.Reason == "" && in.DownRuns >= a1DownRuns {
		d.Reason = fmt.Sprintf("недоступен %d проверки подряд", in.DownRuns)
	}
	if d.Reason == "" {
		return d
	}
	others := 0
	for _, p := range in.Paths {
		if p.ID != in.EntryID && p.Known && p.Healthy {
			others++
		}
	}
	recent, streak := false, 0
	for _, t := range in.LastAuto {
		if now.Sub(t) < a1Gap {
			recent = true
		}
		if now.Sub(t) < a1StreakSpan {
			streak++
		}
	}
	switch {
	case others == 0:
		d.Blocked = "последний здоровый путь: других входов в ротации со свежей хорошей проверкой нет"
	case recent:
		d.Blocked = "не чаще раза в час на вход"
	case streak >= a1StreakMax:
		d.Blocked = "дальше вручную"
	default:
		d.Remove = true
	}
	return d
}

// ── A12 ──

const (
	a12Misses       = 3
	a12MaxPerHour   = 3
	a12RestartsSpan = time.Hour
)

// a12Decide: restart node-agent after 3 misses while the tunnel is alive, at most 3 an hour.
func a12Decide(fails int, tunnelHealthy bool, restarts []time.Time, now time.Time) (restart bool, alert bool) {
	if fails < a12Misses || !tunnelHealthy {
		return false, false
	}
	n := 0
	for _, t := range restarts {
		if now.Sub(t) < a12RestartsSpan {
			n++
		}
	}
	if n >= a12MaxPerHour {
		return false, true
	}
	return true, false
}

// ── A14 ──

const a14DiskPct = 85

// a14Decide: above 85 % vacuum the journal to half its limit; nothing left to vacuum → the alert.
func a14Decide(diskPct int, journalBytes, limitBytes int64) (vacuumTo int64, alert bool) {
	if diskPct <= a14DiskPct {
		return 0, false
	}
	if half := limitBytes / 2; journalBytes > half {
		return half, false
	}
	return 0, true
}

// ── A16 ──

const snapshotEvery = 10 * time.Minute

func snapshotDue(_ string, level AlertLevel, last, now time.Time) bool {
	return level == AlertCritical && (last.IsZero() || now.Sub(last) >= snapshotEvery)
}
