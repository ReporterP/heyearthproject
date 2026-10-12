package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"runtime"
	"runtime/debug"
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
	loopProbeStaleAfter    = 10 * time.Minute // probes skipped for longer than this → "consecutive" starts over
	checkHostInterval      = 15 * time.Minute
	checkHostFailThreshold = 2
	housekeepingInterval   = 10 * time.Minute
	certWarnDays           = 14
	certReadFailThreshold  = 3
	backupDir              = "/opt/heyearth/backups"
	backupGlob             = "backup-*.tar.gz" // regular, non-empty files only (see newestBackupModTime)
	backupMaxAge           = 26 * time.Hour
	diskWarnPercent        = 85.0
	defaultCheckHostNodes  = "ru1.node.check-host.net,ru2.node.check-host.net,ru3.node.check-host.net"
)

var (
	checkHostBase    = "https://check-host.net"
	loopProbeState   loopProbeStreak // touched only by the loop-probe goroutine
	loopProbeOKLog   time.Time       // rate-limits the "probe OK" log line to once an hour
	loopProbeSkipLog logGate         // rate-limits the "probe skipped" log line to once an hour
	checkHostFails   int             // touched only by the check-host goroutine
	certReadFails    int             // touched only by the housekeeping goroutine
)

func StartMonitor() {
	if runtime.GOOS != "linux" {
		log.Println("[monitor] не Linux — мониторинг отключён (локальная разработка)")
		return
	}
	// Each step has its own recover: a panic in one must not skip the others.
	go runEvery(90*time.Second, loopProbeInterval, func() {
		safely("loop-проба", runLoopProbe)
		safely("NRestarts xray", pollXraySystemdRestarts)
		safely("окно рестартов xray", checkXrayRestartWindow)
		safely("Бамбук-проба", runBambooProbe) // last: it can wait on UDP timeouts for tens of seconds
	})
	go runEvery(5*time.Minute, checkHostInterval, func() {
		safely("check-host", runCheckHostProbe)
	})
	go runEvery(2*time.Minute, housekeepingInterval, func() {
		safely("сертификат", checkSiteCertExpiry)
		safely("бэкап", checkBackupFreshness)
		safely("копия бэкапа", checkOffsiteBackupFreshness)
		safely("диск", checkDiskUsage)
	})
	log.Printf("[monitor] запущен: loop-проба и Бамбук-проба каждые %v, check-host каждые %v, сертификат/бэкап/диск каждые %v",
		loopProbeInterval, checkHostInterval, housekeepingInterval)
}

func runEvery(initialDelay, interval time.Duration, fn func()) {
	time.Sleep(initialDelay)
	for {
		safely("цикл", fn)
		time.Sleep(interval)
	}
}

// safely runs fn and turns a panic into a log line with the stack trace.
func safely(name string, fn func()) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("[monitor] panic в %q: %v\n%s", name, r, debug.Stack())
		}
	}()
	fn()
}

// logGate lets a message through at most once per interval. Single-goroutine use.
type logGate struct{ last time.Time }

func (g *logGate) allow(now time.Time, every time.Duration) bool {
	if !g.last.IsZero() && now.Sub(g.last) < every {
		return false
	}
	g.last = now
	return true
}

// loopProbeStreak counts consecutive failed loop probes. Probes that were skipped
// (no healthy exit, tunnel down) for longer than loopProbeStaleAfter break the streak,
// so "3 in a row" cannot stretch over hours of unrelated failures.
type loopProbeStreak struct {
	fails int
	last  time.Time // time of the previous verdict
}

func (s *loopProbeStreak) record(now time.Time, ok bool) int {
	if !s.last.IsZero() && now.Sub(s.last) > loopProbeStaleAfter {
		s.fails = 0
	}
	s.last = now
	if ok {
		s.fails = 0
	} else {
		s.fails++
	}
	return s.fails
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

func logLoopProbeSkipped(reason string) {
	if loopProbeSkipLog.allow(time.Now(), time.Hour) {
		log.Printf("[monitor] loop-проба пропущена: %s (повтор записи не чаще раза в час)", reason)
	}
}

func runLoopProbe() {
	port, ok := pickExitSocksPort()
	if !ok {
		logLoopProbeSkipped("нет здорового exit с локальным SOCKS")
		return
	}
	dialer, err := exitSocksDialer(port)
	if err != nil {
		logLoopProbeSkipped(err.Error())
		return
	}
	if err := socksDialCheck(dialer, "1.1.1.1:443"); err != nil { // a broken tunnel says nothing about the entry
		logLoopProbeSkipped(fmt.Sprintf("туннель exit :%d не работает: %v", port, err))
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
		loopProbeState.record(time.Now(), true)
		if time.Since(loopProbeOKLog) > time.Hour {
			loopProbeOKLog = time.Now()
			log.Printf("[monitor] loop-проба OK: %s:443 доступен снаружи (через exit :%d)", entry, port)
		}
		ResolveAlert("entry-unreachable-external", fmt.Sprintf("Вход %s:443 снова доступен снаружи (проба через exit :%d).", entry, port))
		return
	}
	fails := loopProbeState.record(time.Now(), false)
	log.Printf("[monitor] loop-проба провалена (%d подряд): %s", fails, strings.Join(problems, "; "))
	if fails >= loopProbeFailThreshold {
		RaiseAlert("entry-unreachable-external", AlertCritical, "Вход недоступен снаружи",
			fmt.Sprintf("%d проверок подряд через exit :%d не прошли до %s:443.\n%s",
				fails, port, entry, strings.Join(problems, "\n")))
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
// Only the nodes we asked for are counted: a requested node missing from the response is
// pending, a node we did not ask for is ignored (check-host may substitute nodes), so
// "unreachable" really means every requested node answered and all of them failed.
func parseCheckHostTCPResult(body []byte, requested []string) (checkHostTally, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		return checkHostTally{}, fmt.Errorf("check-host: bad JSON: %w", err)
	}
	var t checkHostTally
	seen := make(map[string]bool, len(requested))
	for _, node := range requested {
		if seen[node] {
			continue
		}
		seen[node] = true
		v, present := raw[node]
		var results []struct {
			Time  *float64 `json:"time"`
			Error string   `json:"error"`
		}
		if !present || string(v) == "null" || json.Unmarshal(v, &results) != nil || len(results) == 0 {
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
		if tally, err = parseCheckHostTCPResult(res, nodes); err != nil {
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
	if len(nodes) == 0 { // blank setting: never let check-host pick arbitrary (non-RU) nodes
		nodes = splitCSV(defaultCheckHostNodes)
	}
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

// certExpiryAlert decides whether the certificate needs an alert. An already expired
// certificate is critical with its own wording: that also escalates the warn raised by
// the 14-day check earlier (a repeated warn would be silently deduplicated).
func certExpiryAlert(site string, notAfter, now time.Time) (level AlertLevel, title, details string, alert bool) {
	left := notAfter.Sub(now)
	if left <= 0 {
		return AlertCritical, "Сертификат сайта истёк",
			fmt.Sprintf("Сертификат %s истёк %s, браузеры показывают ошибку. Срочно проверьте продление certbot: systemctl list-timers | grep certbot.",
				site, notAfter.In(mskZone).Format("02.01.2006 15:04")), true
	}
	if days := int(left.Hours() / 24); days < certWarnDays {
		return AlertWarn, "Сертификат сайта скоро истечёт",
			fmt.Sprintf("Сертификат %s действует до %s (осталось %d дн.). Проверьте продление certbot: systemctl list-timers | grep certbot.",
				site, notAfter.In(mskZone).Format("02.01.2006"), days), true
	}
	return "", "", "", false
}

func checkSiteCertExpiry() {
	site := monitorSiteHost()
	notAfter, err := fetchCertNotAfter("127.0.0.1:8443", site)
	if err != nil {
		certReadFails++
		log.Printf("[monitor] сертификат %s: не удалось прочитать (%d подряд): %v", site, certReadFails, err)
		if certReadFails >= certReadFailThreshold {
			RaiseAlert("tls-cert-check-failed", AlertWarn, "Не удаётся проверить сертификат сайта",
				fmt.Sprintf("%d проверок подряд не удалось прочитать сертификат %s на 127.0.0.1:8443: %v. Срок действия не контролируется, проверьте nginx: systemctl status nginx.",
					certReadFails, site, err))
		}
		return
	}
	certReadFails = 0
	ResolveAlert("tls-cert-check-failed", fmt.Sprintf("Сертификат %s снова читается.", site))
	if level, title, details, alert := certExpiryAlert(site, notAfter, time.Now()); alert {
		RaiseAlert("tls-cert-expiry", level, title, details)
		return
	}
	ResolveAlert("tls-cert-expiry", fmt.Sprintf("Сертификат %s продлён: действует до %s.", site, notAfter.In(mskZone).Format("02.01.2006")))
}

// newestBackupModTime returns the mtime of the newest real backup among the entries of
// the backup directory: a regular, non-empty file named backup-*.tar.gz (the format of
// doBackup and of the Task 9 timer). Directories (the <ts>/ working dir created before
// pg_dump, lost+found), partial/temporary files and empty files do not count.
// The zero time means "no valid backup".
func newestBackupModTime(entries []fs.DirEntry) time.Time {
	var newest time.Time
	for _, e := range entries {
		if ok, _ := path.Match(backupGlob, e.Name()); !ok {
			continue
		}
		info, err := e.Info()
		if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 {
			continue
		}
		if info.ModTime().After(newest) {
			newest = info.ModTime()
		}
	}
	return newest
}

func checkBackupFreshness() {
	entries, err := os.ReadDir(backupDir)
	var newest time.Time
	if err == nil {
		newest = newestBackupModTime(entries)
	}
	switch {
	case err != nil:
		RaiseAlert("backup-stale", AlertWarn, "Бэкапов нет", fmt.Sprintf("Не удалось прочитать %s: %v", backupDir, err))
	case newest.IsZero():
		RaiseAlert("backup-stale", AlertWarn, "Бэкапов нет", fmt.Sprintf("В %s нет ни одного непустого файла %s.", backupDir, backupGlob))
	case time.Since(newest) > backupMaxAge:
		RaiseAlert("backup-stale", AlertWarn, "Бэкап устарел",
			fmt.Sprintf("Последний бэкап в %s — %v назад (порог %v).", backupDir, time.Since(newest).Round(time.Minute), backupMaxAge))
	default:
		ResolveAlert("backup-stale", fmt.Sprintf("Свежий бэкап: %v назад.", time.Since(newest).Round(time.Minute)))
	}
}

// ── offsite backup copy (the encrypted copy on the exit, written by heyearth-backup.sh) ──

const (
	backupEnvPath      = "/etc/heyearth/backup.env"
	offsiteStampName   = "last-offsite" // written by the backup script only after the exit confirmed the sha256
	offsiteLastErrName = "last-error"
	offsiteMaxAge      = backupMaxAge
)

// offsiteConfigured reports whether backup.env enables the off-box copy: a non-empty TARGET_HOST.
func offsiteConfigured(backupEnv string) bool {
	for _, line := range strings.Split(backupEnv, "\n") {
		if v, ok := strings.CutPrefix(strings.TrimSpace(line), "TARGET_HOST="); ok {
			return strings.TrimSpace(v) != ""
		}
	}
	return false
}

type offsiteVerdict int

const (
	offsiteNotConfigured offsiteVerdict = iota // no TARGET_HOST: nothing to watch
	offsiteFresh
	offsiteStale
)

// decideOffsiteBackup judges the off-box copy. stampMod is the mtime of last-offsite (zero when
// the file does not exist), envMod the mtime of backup.env. The reference point is the later
// of the two: a copy that was configured (or re-pointed at another exit) a moment ago gets a
// full period for its first run instead of an immediate alert, and a Hub that has never made a
// copy is reported once that period has passed. age is measured from the reference point.
func decideOffsiteBackup(configured bool, stampMod, envMod, now time.Time) (offsiteVerdict, time.Duration) {
	if !configured {
		return offsiteNotConfigured, 0
	}
	ref := stampMod
	if envMod.After(ref) {
		ref = envMod
	}
	age := now.Sub(ref)
	if age > offsiteMaxAge {
		return offsiteStale, age
	}
	return offsiteFresh, age
}

func checkOffsiteBackupFreshness() {
	env, err := os.ReadFile(backupEnvPath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			ResolveAlert("offsite-backup-stale", "Копия бэкапа вне Hub больше не настроена (нет "+backupEnvPath+").")
		} else {
			log.Printf("[monitor] копия бэкапа: не удалось прочитать %s: %v", backupEnvPath, err)
		}
		return
	}
	var envMod, stampMod time.Time
	if info, err := os.Stat(backupEnvPath); err == nil {
		envMod = info.ModTime()
	}
	if info, err := os.Stat(path.Join(backupDir, offsiteStampName)); err == nil && info.Mode().IsRegular() {
		stampMod = info.ModTime()
	}
	verdict, age := decideOffsiteBackup(offsiteConfigured(string(env)), stampMod, envMod, time.Now())
	switch verdict {
	case offsiteNotConfigured:
		ResolveAlert("offsite-backup-stale", "Копия бэкапа вне Hub отключена в "+backupEnvPath+".")
	case offsiteStale:
		details := fmt.Sprintf("Зашифрованная копия бэкапа на exit-ноде не обновлялась %v (порог %v); последний успех: %s. "+
			"Проверьте journalctl -u heyearth-backup -n 30 и %s.",
			age.Round(time.Minute), offsiteMaxAge, offsiteLastSuccess(stampMod), path.Join(backupDir, offsiteLastErrName))
		RaiseAlert("offsite-backup-stale", AlertWarn, "Копия бэкапа вне Hub устарела", details)
	case offsiteFresh:
		ResolveAlert("offsite-backup-stale", fmt.Sprintf("Копия бэкапа вне Hub свежая: %v назад.", age.Round(time.Minute)))
	}
}

func offsiteLastSuccess(stampMod time.Time) string {
	if stampMod.IsZero() {
		return "ни разу"
	}
	return stampMod.In(mskZone).Format("02.01.2006 15:04")
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
