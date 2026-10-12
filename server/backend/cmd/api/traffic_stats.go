package main

import (
	"log"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/heyearth/backend/internal/biz"
	"github.com/heyearth/backend/internal/db"
	"github.com/heyearth/backend/internal/xraystats"
)

// estimatedBytesPerDial — груба оценка средних байт на одно SOCKS5/TCP dial:
// TLS handshake + small request payload. Понимаемо завышает для лёгких dial'ов
// (DNS) и занижает для тяжёлых (большие downloads), но для выявления abuser'ов
// этого достаточно — они отличаются на порядок.
const estimatedBytesPerDial = 50_000

// lastDialRateCleanup — момент последней очистки старых сэмплов dial_rate_log.
var lastDialRateCleanup struct {
	sync.Mutex
	t time.Time
}

// trafficDelta — per-user разница uplink/downlink за окно опроса.
// Тип на пакет-уровне чтобы recordDialRateSamples() мог его принимать.
type trafficDelta struct {
	upload   int64
	download int64
}

// ─────────────────────── Traffic Stats Collector ───────────────────────
//
// Каждые 60 секунд опрашивает Xray Stats API через gRPC (127.0.0.1:10085),
// получает per-user трафик (upload/download) и записывает в БД.
// Использует QueryStats с reset=true — счётчики обнуляются после чтения.

const xrayStatsAPIAddr = "127.0.0.1:10085"

// statsWindowStart is when the previous successful QueryStats(reset=true) was
// issued: the deltas of the next read cover [statsWindowStart, read]. Zero until
// the first read. Touched only by the collector goroutine.
var statsWindowStart time.Time

// StartTrafficStatsCollector запускает фоновый сборщик трафика.
func StartTrafficStatsCollector() {
	go func() {
		// Ждём запуск Xray
		time.Sleep(15 * time.Second)
		collectTrafficStats()

		ticker := time.NewTicker(60 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			collectTrafficStats()
		}
	}()
	log.Println("[traffic-stats] Фоновый сборщик трафика запущен (каждые 60 сек)")
}

func collectTrafficStats() {
	// A flip of quota_cutoff_enabled is applied at once, not with the 5-minute
	// periodic sync: the watcher compares the flag with the value the last
	// successful sync applied. Polled here, before the early returns below,
	// because this is the only loop that ticks every minute whether or not Xray
	// is reachable and has traffic (checkTrafficLimits runs only on windows with
	// traffic).
	watchQuotaCutoffFlag()

	// Быстрая проверка доступности
	conn, err := net.DialTimeout("tcp", xrayStatsAPIAddr, 2*time.Second)
	if err != nil {
		return // Xray Stats API недоступен — нормально если Xray не установлен
	}
	conn.Close()

	readAt := time.Now() // before the call: the counters are reset no earlier than this
	stats, err := xraystats.QueryStats(xrayStatsAPIAddr, "user>>>", true)
	if err != nil {
		return
	}
	windowStart := statsWindowStart
	statsWindowStart = readAt

	// Группируем по user_id (тип trafficDelta — пакетный, см. выше)
	deltas := make(map[string]*trafficDelta)

	for _, stat := range stats {
		// Формат: "user>>>user_id>>>traffic>>>uplink" / "...>>>downlink"
		parts := strings.Split(stat.Name, ">>>")
		if len(parts) != 4 || parts[0] != "user" {
			continue
		}
		userID := parts[1]
		direction := parts[3]

		if userID == "server@heyearth" || userID == "" {
			continue
		}

		if deltas[userID] == nil {
			deltas[userID] = &trafficDelta{}
		}
		switch direction {
		case "uplink":
			deltas[userID].upload = stat.Value
		case "downlink":
			deltas[userID].download = stat.Value
		}
	}

	// Soft cutoff guard: a user removed live (rmu) whose open session still
	// carries traffic forces a restart (xray_revoked.go). Runs before the early
	// return below so idle watch entries still expire.
	checkRevokedStillOnline(deltas, windowStart, readAt)

	if len(deltas) == 0 {
		return
	}

	saved := 0
	for userID, delta := range deltas {
		if delta.upload == 0 && delta.download == 0 {
			continue
		}

		_, err := db.DB.Exec(`
			INSERT INTO traffic_stats (user_id, upload_bytes, download_bytes)
			VALUES ($1, $2, $3)
		`, userID, delta.upload, delta.download)
		if err != nil {
			log.Printf("[traffic-stats] Ошибка записи для %s: %v", userID, err)
			continue
		}

		// Аккумулируем в user_subscriptions.traffic_used
		_, _ = db.DB.Exec(`
			UPDATE user_subscriptions us
			SET traffic_used = traffic_used + $1
			FROM users u
			WHERE u.id = us.user_id AND u.user_id = $2 AND us.valid_until > NOW()
		`, delta.upload+delta.download, userID)

		saved++
	}

	if saved > 0 {
		log.Printf("[traffic-stats] Записано: %d пользователей", saved)
	}

	// ── Dial-rate observation (P1 fix B3) ──
	// Используем те же deltas чтобы оценить per-user dial-rate.
	// Это observation only — никакой автоматической блокировки.
	recordDialRateSamples(deltas)

	// Проверяем лимиты — логируем превышения
	checkTrafficLimits()
}

// recordDialRateSamples оценивает per-user dials/min из дельт и записывает в dial_rate_log.
// Окно = 60 сек (соответствует тикеру collectTrafficStats).
// Раз в час чистит сэмплы старше 24 часов.
func recordDialRateSamples(deltas map[string]*trafficDelta) {
	if len(deltas) == 0 {
		return
	}

	windowStart := time.Now().Truncate(time.Minute)
	windowStartISO := windowStart.Format(time.RFC3339)
	threshold := biz.GetDialRateThreshold()

	samples := make([]biz.DialRateSample, 0, len(deltas))
	for userID, d := range deltas {
		if d == nil {
			continue
		}
		// Оценка количества dial'ов в окне ≈ uplink_bytes / estimatedBytesPerDial.
		// Только uplink — downlink включает payload-ответы которые накачивает не клиент.
		estimatedDials := int(d.upload / estimatedBytesPerDial)
		if estimatedDials > threshold {
			log.Printf("[dial-rate] WARN user=%s dials≈%d/min (threshold=%d) up=%dKB down=%dKB",
				userID, estimatedDials, threshold, d.upload/1024, d.download/1024)
		}
		samples = append(samples, biz.DialRateSample{
			UserUUID:       userID,
			WindowStartISO: windowStartISO,
			Dials:          estimatedDials,
			UpBytes:        d.upload,
			DownBytes:      d.download,
		})
	}

	if err := biz.PersistDialRateSamples(samples, threshold); err != nil {
		log.Printf("[dial-rate] Ошибка сохранения: %v", err)
		return
	}

	// Раз в час чистим старые сэмплы.
	lastDialRateCleanup.Lock()
	doCleanup := time.Since(lastDialRateCleanup.t) > time.Hour
	if doCleanup {
		lastDialRateCleanup.t = time.Now()
	}
	lastDialRateCleanup.Unlock()
	if doCleanup {
		if n, err := biz.CleanupDialRateLog(); err == nil && n > 0 {
			log.Printf("[dial-rate] Очистка: %d старых записей удалено", n)
		}
	}
}

// checkTrafficLimits — delegates to biz, logs offenders and requests a debounced
// xray sync (2 s) instead of waiting for the 5-minute periodic one, but ONLY when
// the fresh decision differs from the one the last successful sync applied
// (quotaSyncNeeded): the flag flipped, or the set of users to cut off changed in
// either direction (a new offender while armed; a cycle reset or a payment
// restores one). While nothing changes — a user may stay over quota for a whole
// cycle — no sync is requested. An armed sync removes the user live
// (`xray api rmu`), which only refuses NEW connections; sessions already open
// keep working until the soft cutoff guard sees their traffic in the next full
// stats window and forces a restart (≈1–2 min, checkRevokedStillOnline).
// Per-user lines in both modes go through quotaWarnings: a user is logged when
// first seen over quota (new offenders at once), then at most once per
// quotaWarnEvery while they stay over it; the per-sync summary line of
// SyncXrayConfig keeps the count visible. The "decision changed" line goes
// through quotaChangeNotices keyed by the diff: while syncs keep failing the
// request is repeated every minute, the same line at most hourly. A failed read
// skips the tick: no sync is requested, so nothing is restored or cut.
func checkTrafficLimits() {
	quota, err := loadQuotaCutoff()
	if err != nil {
		log.Printf("[traffic-stats] checkTrafficLimits: %v — проверка пропущена, синхронизация не запрошена", err)
		return
	}
	now := time.Now()
	for _, line := range quotaOffenderLines(quota, quotaWarnings, now, quotaWarnEvery) {
		log.Print(line)
	}
	// appliedState locks quotaSyncMemory.mu only for the copy: requestSync is
	// never called with it held.
	applied := quotaMemory.appliedState()
	if !quotaSyncNeeded(quota, applied) {
		// Nothing to say, recorded anyway: a diff that disappears (applied) and
		// comes back is a change and is logged again.
		quotaChangeNotices.due("", now, quotaWarnEvery)
		return
	}
	if line, key := quotaChangeNotice(quota, applied); quotaChangeNotices.due(key, now, quotaWarnEvery) {
		log.Print(line)
	}
	requestSync()
}
