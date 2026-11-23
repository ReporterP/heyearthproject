package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/heyearth/backend/internal/biz"
)

// ─────────────── The health board ───────────────
// One line of «Операции» shows every path a user's traffic or money takes — the Hub, each
// entry and exit, the subscription, payments, the bot, the domain, the certificate, backups,
// the database, the watchdog — as ok / warn / down / off / unknown.
//
// Every check writes its result here when it ends (board.Set). A result older than three
// intervals of its check is shown as unknown: a check that stopped running proves nothing.
// Entries and exits are read from the database when the board is asked (pathsNow), so their
// state is what the monitors last stored.

// pathState is one cell of the board. State: ok | warn | down | off | unknown.
type pathState struct {
	ID, Label, State, Detail string
	CheckedAt                time.Time
}

// boardIntervals: how often each path's check runs.
var boardIntervals = map[string]time.Duration{
	"hub":          loopProbeInterval,
	"db":           dbHealthInterval,
	"subscription": synthInterval,
	"payments":     paymentSyncInterval,
	"bot":          2 * time.Minute, // a long poll returns at least this often
	"dns":          domainCheckEvery,
	"cert":         housekeepingInterval,
	"backup":       housekeepingInterval,
}

// boardOrder is how the paths are laid out; entries and exits by id inside their group.
var boardOrder = []string{"hub", "entry", "exit", "subscription", "payments", "bot", "dns", "cert", "backup", "db", "watchdog"}

type board struct {
	mu    sync.Mutex
	now   func() time.Time
	every map[string]time.Duration
	paths map[string]pathState
}

func newBoard(every map[string]time.Duration) *board {
	return &board{now: time.Now, every: every, paths: map[string]pathState{}}
}

var healthBoard = newBoard(boardIntervals)

// Set stores the result of a check that has just ended.
func (b *board) Set(id, label, state, detail string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.paths[id] = pathState{ID: id, Label: label, State: state, Detail: detail, CheckedAt: b.now()}
}

func boardGroup(id string) string {
	g, _, _ := strings.Cut(id, ":")
	return g
}

func (b *board) interval(id string) time.Duration {
	if d, ok := b.every[id]; ok {
		return d
	}
	if d, ok := b.every[boardGroup(id)]; ok {
		return d
	}
	return time.Minute
}

// Snapshot returns the board in display order. A result older than three intervals of its
// check comes back as unknown.
func (b *board) Snapshot(now time.Time) []pathState {
	b.mu.Lock()
	out := make([]pathState, 0, len(b.paths))
	for _, p := range b.paths {
		if age := now.Sub(p.CheckedAt); age > 3*b.interval(p.ID) {
			p.State, p.Detail = "unknown", "нет свежей проверки: последняя "+shortAge(age)+" назад"
		}
		out = append(out, p)
	}
	b.mu.Unlock()
	sortPaths(out)
	return out
}

func sortPaths(paths []pathState) {
	rank := func(id string) int {
		for i, g := range boardOrder {
			if boardGroup(id) == g {
				return i
			}
		}
		return len(boardOrder)
	}
	num := func(id string) int {
		_, n, _ := strings.Cut(id, ":")
		v, _ := strconv.Atoi(n)
		return v
	}
	sort.SliceStable(paths, func(i, j int) bool {
		if ri, rj := rank(paths[i].ID), rank(paths[j].ID); ri != rj {
			return ri < rj
		}
		if ni, nj := num(paths[i].ID), num(paths[j].ID); ni != nj {
			return ni < nj
		}
		return paths[i].ID < paths[j].ID
	})
}

// shortAge: «42 с», «3 мин», «2 ч», «5 дн.».
func shortAge(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%d с", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%d мин", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%d ч", int(d.Hours()))
	}
	return fmt.Sprintf("%d дн.", int(d.Hours()/24))
}

// pathsNow is the whole board: what the checks stored, the entries and the exits from the
// database, and the watchdog's pulse.
func pathsNow(ctx context.Context, now time.Time) []pathState {
	paths := healthBoard.Snapshot(now)
	if entries, err := biz.ListAdminEntries(ctx); err == nil {
		for _, e := range entries {
			if e.Kind != "relay" {
				continue
			}
			paths = append(paths, entryPath(e, now))
		}
	}
	if exits, err := biz.ListOnlineExitNodesContext(ctx); err == nil {
		for _, x := range exits {
			paths = append(paths, exitPath(x, now))
		}
	}
	paths = append(paths, watchdogPath(now))
	sortPaths(paths)
	return paths
}

// entryPath reads an entry's cell from what the entry monitor stored.
func entryPath(e biz.Entry, now time.Time) pathState {
	p := pathState{ID: fmt.Sprintf("entry:%d", e.ID), Label: fmt.Sprintf("Вход %d", e.ID), State: "unknown", CheckedAt: now}
	if e.Health.ExternalAt != nil {
		p.CheckedAt = *e.Health.ExternalAt
	}
	switch {
	case e.Lifecycle != "online":
		p.State, p.Detail = "off", "не настроен или выключен"
	case e.Health.External == biz.EntryUnhealthy:
		p.State, p.Detail = "down", "не открывается снаружи"
	case e.Rotation == biz.EntryRotationOff:
		p.State, p.Detail = "off", "вне ротации"
		if e.RotationReason != "" {
			p.Detail += ": " + e.RotationReason
		}
	case e.Health.External == biz.EntryHealthy && e.Health.RU == "unreachable":
		p.State, p.Detail = "warn", "не открывается из домашних сетей РФ"
	case e.Health.External == biz.EntryHealthy && e.Rotation == biz.EntryRotationTest:
		p.State, p.Detail = "warn", "на тесте: только у тестового пользователя"
	case e.Health.External == biz.EntryHealthy:
		p.State = "ok"
	}
	return p
}

// exitPath reads an exit's cell from its tunnel status.
func exitPath(x biz.ExitNodeTunnel, now time.Time) pathState {
	p := pathState{ID: fmt.Sprintf("exit:%d", x.ID), Label: fmt.Sprintf("Выход %d", x.ID), State: "unknown", CheckedAt: now}
	switch x.TunnelStatus {
	case "healthy":
		p.State = "ok"
	case "unhealthy":
		p.State, p.Detail = "down", "оба пути к выходу не работают"
	}
	return p
}

// watchdogPath: is the pulse to the outside watchdog going out.
func watchdogPath(now time.Time) pathState {
	p := pathState{ID: "watchdog", Label: "Сторож", CheckedAt: now}
	configured, ok, at := apiPulse.state()
	switch {
	case !configured:
		p.State, p.Detail = "off", "внешний сторож не настроен: адрес пульса не задан"
	case ok:
		p.State, p.CheckedAt = "ok", at
	default:
		p.State, p.Detail, p.CheckedAt = "warn", "пульс API не принят внешним сторожем", at
	}
	return p
}

// ─────────────── the domain's paid-till date ───────────────

const (
	domainCheckEvery    = 24 * time.Hour
	domainExpiryKey     = "domain-expiry"
	domainWarnDays      = 60
	domainCriticalDays  = 14
	rebootPendingKey    = "reboot-pending"
	rebootPendingDays   = 14
	rebootRequiredFile  = "/var/run/reboot-required"
	backupVerifyKey     = "backup-verify-failed"
	backupVerifyStatus  = "/opt/heyearth/state/backup-verify.status"
	backupVerifyMaxAge  = 9 * 24 * time.Hour // a weekly drill: older than this, the result says nothing
	whoisTimeout        = 15 * time.Second
	whoisDefaultServer  = "whois.iana.org:43"
	whoisRUServer       = "whois.tcinet.ru:43"
	whoisMaxAnswerBytes = 64 << 10
)

var errWhois = errors.New("whois: no paid-till date in the answer")

var whoisDateRe = regexp.MustCompile(`(?im)^\s*(?:paid-till|registry expiry date|expiry date|expiration date|expires)\s*:\s*(\S+)`)

// parseWhoisPaidTill finds the date the domain is paid till in a whois answer.
func parseWhoisPaidTill(answer string) (time.Time, error) {
	m := whoisDateRe.FindStringSubmatch(answer)
	if m == nil {
		return time.Time{}, errWhois
	}
	for _, layout := range []string{time.RFC3339, "2006-01-02T15:04:05Z0700", "2006-01-02", "2006.01.02"} {
		if t, err := time.Parse(layout, m[1]); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("whois: unreadable date %q", m[1])
}

// whoisPaidTill asks the zone's whois server (the .ru registry for .ru and .рф names).
func whoisPaidTill(host string) (time.Time, error) {
	server := whoisDefaultServer
	if strings.HasSuffix(host, ".ru") || strings.HasSuffix(host, ".su") || strings.HasSuffix(host, ".xn--p1ai") {
		server = whoisRUServer
	}
	conn, err := net.DialTimeout("tcp", server, whoisTimeout)
	if err != nil {
		return time.Time{}, err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(whoisTimeout))
	if _, err := fmt.Fprintf(conn, "%s\r\n", host); err != nil {
		return time.Time{}, err
	}
	answer, err := io.ReadAll(io.LimitReader(bufio.NewReader(conn), whoisMaxAnswerBytes))
	if err != nil && len(answer) == 0 {
		return time.Time{}, err
	}
	return parseWhoisPaidTill(string(answer))
}

// domainExpiryVerdict: under 60 days the domain's cell is warn and the owner is told; under
// 14 days it is critical.
func domainExpiryVerdict(host string, paidTill, now time.Time) (state string, level AlertLevel, detail string, alert bool) {
	days := int(paidTill.Sub(now).Hours() / 24)
	detail = fmt.Sprintf("оплачен до %s, осталось %d дн.", biz.MoscowTime(paidTill).Format("02.01.2006"), days)
	switch {
	case paidTill.Before(now) || days < domainCriticalDays:
		return "down", AlertCritical, detail, true
	case days < domainWarnDays:
		return "warn", AlertWarn, detail, true
	}
	return "ok", "", detail, false
}

// domainCheck looks at the site's domain once a day.
type domainCheck struct {
	host     string
	board    *board
	now      func() time.Time
	paidTill func(host string) (time.Time, error)
	raise    func(key string, level AlertLevel, title, details string)
	resolve  func(key, message string)
}

func (c domainCheck) run() {
	till, err := c.paidTill(c.host)
	if err != nil {
		// No answer is no verdict: the alert stays as it is.
		log.Printf("[monitor] срок домена %s не прочитан: %v", c.host, err)
		c.board.Set("dns", "Домен", "unknown", "срок оплаты не прочитан")
		return
	}
	state, level, detail, alert := domainExpiryVerdict(c.host, till, c.now())
	c.board.Set("dns", "Домен", state, detail)
	if alert {
		c.raise(domainExpiryKey, level, "Заканчивается оплата домена", "Домен "+c.host+" "+detail+
			" Если его не продлить, сайт, кабинет, подписка и оплаты перестанут открываться у всех.")
		return
	}
	c.resolve(domainExpiryKey, "Домен "+c.host+" "+detail)
}

func checkDomainExpiry() {
	domainCheck{host: monitorSiteHost(), board: healthBoard, now: time.Now, paidTill: whoisPaidTill,
		raise: RaiseAlertNoRepeat, resolve: ResolveAlert}.run()
}

// ─────────────── a pending reboot, the restore drill ───────────────

// rebootPendingVerdict: since is when the Hub started asking for a reboot (zero: it does
// not). Past 14 days the Hub's cell is warn and the owner is told once.
func rebootPendingVerdict(since, now time.Time) (state, detail string, alert bool) {
	if since.IsZero() {
		return "ok", "", false
	}
	days := int(now.Sub(since).Hours() / 24)
	if days > rebootPendingDays {
		return "warn", fmt.Sprintf("нужна перезагрузка %d дн.", days), true
	}
	return "ok", fmt.Sprintf("ждёт перезагрузки %d дн.", days), false
}

// hubRebootNote is the reboot part of the Hub's cell; it raises reboot-pending when due.
func hubRebootNote(now time.Time) (state, detail string) {
	var since time.Time
	if st, err := os.Stat(rebootRequiredFile); err == nil {
		since = st.ModTime()
	}
	state, detail, alert := rebootPendingVerdict(since, now)
	if alert {
		RaiseAlertNoRepeat(rebootPendingKey, AlertWarn, "Хаб давно ждёт перезагрузки",
			"Обновления ядра установлены, но не действуют: "+detail+" Перезагрузка хаба рвёт соединения всех (К3) — выберите окно 04:00–05:30 МСК.")
	} else {
		ResolveAlert(rebootPendingKey, "Хаб перезагружен или перезагрузка больше не нужна.")
	}
	return state, detail
}

// backupVerifyVerdict reads backup-verify.status («<unix time> <systemd result>»): failed is
// true when the last weekly restore drill ran recently and did not pass.
func backupVerifyVerdict(status string, now time.Time) (failed bool, detail string) {
	fields := strings.Fields(status)
	if len(fields) != 2 {
		return false, ""
	}
	sec, err := strconv.ParseInt(fields[0], 10, 64)
	if err != nil {
		return false, ""
	}
	at := time.Unix(sec, 0)
	if now.Sub(at) > backupVerifyMaxAge {
		return false, ""
	}
	if fields[1] != "success" {
		return true, "учебное восстановление " + biz.MoscowTime(at).Format("02.01") + " не прошло"
	}
	return false, "учебное восстановление " + biz.MoscowTime(at).Format("02.01") + " прошло"
}

// backupVerifyNote is the drill part of the backup's cell; it raises backup-verify-failed.
func backupVerifyNote(now time.Time) (failed bool, detail string) {
	body, err := os.ReadFile(backupVerifyStatus)
	if err != nil {
		return false, ""
	}
	failed, detail = backupVerifyVerdict(string(body), now)
	if failed {
		RaiseAlertNoRepeat(backupVerifyKey, AlertWarn, "Учебное восстановление бэкапа не прошло",
			"Еженедельная проверка восстановила последний бэкап во временную базу с ошибкой: бэкапы есть, но восстановиться из них может не получиться. Подробности: journalctl -u heyearth-backup-verify -n 30.")
	} else if detail != "" {
		ResolveAlert(backupVerifyKey, "Учебное восстановление бэкапа снова проходит.")
	}
	return failed, detail
}
