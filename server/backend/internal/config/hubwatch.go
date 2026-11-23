package config

import (
	"errors"
	"fmt"
	"net"
	"regexp"
	"strings"
)

// The Hub watcher on an exit (docs/prd/stability-operations.prd.md, part 2): once a minute a
// timer on the exit opens the site's health check on the Hub's public address (the one it was
// installed with, then the one the site's name resolves to). Three failures
// in a row and a message goes straight to the owner's Telegram chat — the exits reach
// Telegram, the Hub may be dead. A watcher bot of its own carries it, so the main bot's token
// never sits on a server abroad.
//
// The exit itself may be the sick one: before any message it checks a neutral address abroad
// (no way out at all) and a neutral one in RU (no path to RU — then the Hub's own monitor
// reports the exit) and stays silent when either fails.

const (
	HubWatchScriptPath  = "/opt/heyearth/bin/heyearth-hubwatch.sh"
	HubWatchEnvPath     = "/etc/heyearth/hubwatch.env" // BOT_TOKEN, CHAT_ID — mode 0600
	HubWatchStatePath   = "/var/lib/heyearth/hubwatch.state"
	HubWatchServicePath = "/etc/systemd/system/heyearth-hubwatch.service"
	HubWatchTimerPath   = "/etc/systemd/system/heyearth-hubwatch.timer"
	HubWatchTimerUnit   = "heyearth-hubwatch.timer"
	HubWatchServiceUnit = "heyearth-hubwatch.service"
	// HubWatchControlURL: a neutral address abroad that answers when the exit has a way out.
	HubWatchControlURL = "https://www.gstatic.com/generate_204"
	// HubWatchControlRUURL: a neutral address in RU; any HTTP answer from it means the exit
	// has a path to RU.
	HubWatchControlRUURL = "https://ya.ru/"
)

// HubWatchParams is what the watcher on one exit is rendered with.
type HubWatchParams struct {
	HubAddr    string // the Hub's public IP: the check goes to it whatever DNS says
	SiteHost   string // the site's name (SNI and Host of the health check)
	ControlURL string // a neutral https address abroad
	// ControlRUURL: a neutral https address in RU (any HTTP answer counts).
	ControlRUURL string
	ExitName     string // «выход 3» — named in the message instead of any address
	StaggerSec   int    // a pause before the message, different per exit
	// StatePath and EnvPath default to HubWatchStatePath and HubWatchEnvPath (tests set them).
	StatePath, EnvPath string
}

var (
	hubWatchHostRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)+$`)
	hubWatchURLRe  = regexp.MustCompile(`^https://[A-Za-z0-9._~:/?#\[\]@!$&()*+,;=%-]+$`)
	hubWatchPathRe = regexp.MustCompile(`^[A-Za-z0-9._/:~-]+$`)
)

// Validate refuses values that could not be put into the script as they are.
func (p HubWatchParams) Validate() error {
	switch {
	case net.ParseIP(p.HubAddr) == nil:
		return fmt.Errorf("адрес хаба %q не IP", p.HubAddr)
	case !hubWatchHostRe.MatchString(p.SiteHost):
		return fmt.Errorf("имя сайта %q не имя хоста", p.SiteHost)
	case !hubWatchURLRe.MatchString(p.ControlURL):
		return fmt.Errorf("контрольный адрес %q не https-адрес", p.ControlURL)
	case !hubWatchURLRe.MatchString(p.ControlRUURL):
		return fmt.Errorf("контрольный адрес в РФ %q не https-адрес", p.ControlRUURL)
	case p.ExitName == "" || strings.ContainsAny(p.ExitName, "'\"\\$`\n"):
		return fmt.Errorf("имя выхода %q не годится для сообщения", p.ExitName)
	case p.StaggerSec < 0 || p.StaggerSec > 45:
		return fmt.Errorf("пауза %d с вне 0..45", p.StaggerSec)
	}
	for _, path := range []string{p.StatePath, p.EnvPath} {
		if path != "" && !hubWatchPathRe.MatchString(path) {
			return fmt.Errorf("путь %q не годится", path)
		}
	}
	return nil
}

var (
	hubWatchTokenRe = regexp.MustCompile(`^[0-9]{3,20}:[A-Za-z0-9_-]{10,100}$`)
	hubWatchChatRe  = regexp.MustCompile(`^(-?[0-9]{1,20}|@[A-Za-z][A-Za-z0-9_]{3,31})$`)
)

// ValidateHubWatchCreds refuses a bot token or a chat that is not exactly one: both go into a
// file root sources on the exit, so nothing with a quote, a space or a `$` may get there. The
// messages never repeat the value.
func ValidateHubWatchCreds(botToken, chatID string) error {
	if !hubWatchTokenRe.MatchString(botToken) {
		return errors.New("токен бота-сторожа не похож на токен Telegram-бота (<число>:<буквы, цифры, _ и ->)")
	}
	if !hubWatchChatRe.MatchString(chatID) {
		return errors.New("чат тревог не число и не @имя канала")
	}
	return nil
}

// HubWatchEnv is hubwatch.env: the watcher bot's token and the chat (both must have passed
// ValidateHubWatchCreds). Mode 0600 on the exit.
func HubWatchEnv(botToken, chatID string) string {
	return fmt.Sprintf("BOT_TOKEN='%s'\nCHAT_ID='%s'\n", botToken, chatID)
}

// HubWatchUnits are the watcher's service and its once-a-minute timer.
func HubWatchUnits() (service, timer string) {
	service = "[Unit]\nDescription=HiEarth: look at the Hub from outside\nWants=network-online.target\nAfter=network-online.target\n\n" +
		"[Service]\nType=oneshot\nExecStart=" + HubWatchScriptPath + "\nTimeoutStartSec=180\nNice=10\n"
	timer = "[Unit]\nDescription=HiEarth: look at the Hub from outside, every minute\n\n" +
		"[Timer]\nOnCalendar=minutely\nAccuracySec=5s\n\n[Install]\nWantedBy=timers.target\n"
	return service, timer
}

// HubWatchScript renders the watcher (p must have passed Validate). One run:
//
//	the Hub answers → the failure count is 0; after an alert, the second good run in a row
//	  sends «снова доступен»;
//	it does not → the count grows; from the third failure in a row: both control addresses must
//	  answer (else silence, the count is kept), then a pause of StaggerSec and one more look at
//	  the Hub, then «Хаб недоступен снаружи» — repeated at most every 30 minutes.
//
// The worst run (two looks at the Hub of 20 s, two controls of 10 s, the pause of at most 45 s,
// the message 15 s) is 120 s: the unit's TimeoutStartSec is 180.
func HubWatchScript(p HubWatchParams) string {
	state, env := p.StatePath, p.EnvPath
	if state == "" {
		state = HubWatchStatePath
	}
	if env == "" {
		env = HubWatchEnvPath
	}
	return `#!/usr/bin/env bash
# Installed by the HiEarth Hub (job «watchdog»). DO NOT EDIT: the next install overwrites it.
# Looks at the Hub from outside once a minute; tells the owner's Telegram chat when it is gone.
set -u
HUB_ADDR='` + p.HubAddr + `'
SITE_HOST='` + p.SiteHost + `'
CONTROL_URL='` + p.ControlURL + `'
CONTROL_RU_URL='` + p.ControlRUURL + `'
EXIT_NAME='` + p.ExitName + `'
STAGGER_SEC=` + fmt.Sprint(p.StaggerSec) + `
STATE=` + state + `
ENV=` + env + `
FAIL_THRESHOLD=3
REPEAT_SEC=1800

fails=0; oks=0; alerted=0; last_alert=0
if [ -r "$STATE" ]; then . "$STATE"; fi
save() {
  mkdir -p "$(dirname "$STATE")"
  printf 'fails=%s\noks=%s\nalerted=%s\nlast_alert=%s\n' "$fails" "$oks" "$alerted" "$last_alert" > "$STATE.tmp" && mv -f "$STATE.tmp" "$STATE"
}
# The Hub answers when the site's health check opens on the address this watcher was installed
# with, or on the one the site's name resolves to now (the Hub's address may have been changed,
# or a change rolled back, since the install).
hub_ok() {
  curl -fsS -m 10 -o /dev/null --resolve "$SITE_HOST:443:$HUB_ADDR" "https://$SITE_HOST/api/v1/health" 2>/dev/null ||
    curl -fsS -m 10 -o /dev/null "https://$SITE_HOST/api/v1/health" 2>/dev/null
}
# The bot token goes to curl as a config on stdin (-K -), never as an argument: «ps» shows
# a process's arguments to every user of the exit. printf is a shell builtin (no process).
# Backslashes and quotes are escaped for the config with a quoted replacement: an unquoted
# backslash in a ${…//…/…} replacement behaves differently across bash versions. A line break
# would end the quoted value and start another curl option: it is written as \n (\r as \r).
send() {
  . "$ENV"
  local bs='\' t
  t=${1//"$bs"/"$bs$bs"}
  t=${t//\"/"$bs\""}
  t=${t//$'\n'/"${bs}n"}
  t=${t//$'\r'/"${bs}r"}
  printf 'url = "https://api.telegram.org/bot%s/sendMessage"\ndata-urlencode = "chat_id=%s"\ndata-urlencode = "text=%s"\n' "$BOT_TOKEN" "$CHAT_ID" "$t" |
    curl -fsS -m 15 -o /dev/null -K - 2>/dev/null
}

if hub_ok; then
  fails=0; oks=$((oks + 1))
  if [ "$alerted" = 1 ] && [ "$oks" -ge 2 ]; then
    if send "✅ Хаб снова доступен снаружи (взгляд: $EXIT_NAME)."; then alerted=0; fi
  fi
  save
  exit 0
fi
oks=0; fails=$((fails + 1)); save
if [ "$fails" -lt "$FAIL_THRESHOLD" ]; then exit 0; fi
now=$(date +%s)
if [ "$alerted" = 1 ] && [ $((now - last_alert)) -lt "$REPEAT_SEC" ]; then exit 0; fi
# The exit itself may be the sick one: without a neutral address abroad answering, say nothing.
if ! curl -fsS -m 10 -o /dev/null "$CONTROL_URL" 2>/dev/null; then exit 0; fi
# ...or it lost its way to RU (any HTTP answer from a neutral address there will do): the Hub's
# own monitor reports that, and the Hub is not what is down.
if ! curl -sS -m 10 -o /dev/null "$CONTROL_RU_URL" 2>/dev/null; then exit 0; fi
# Another exit may be looking too: wait this exit's turn, then look once more.
sleep "$STAGGER_SEC"
if hub_ok; then exit 0; fi
if send "🔴 Хаб недоступен снаружи (взгляд: $EXIT_NAME): $fails проверок подряд сайт не открылся по адресу хаба. Что делать, когда пульта нет: docs/runbooks/hub-down.md"; then
  alerted=1; last_alert=$(date +%s); save
fi
`
}
