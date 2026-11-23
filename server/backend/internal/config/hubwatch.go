package config

import (
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
// The exit itself may be the sick one (no way out, no path to RU): before any message it
// checks a neutral address abroad and stays silent when that fails too.

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
)

// HubWatchParams is what the watcher on one exit is rendered with.
type HubWatchParams struct {
	HubAddr    string // the Hub's public IP: the check goes to it whatever DNS says
	SiteHost   string // the site's name (SNI and Host of the health check)
	ControlURL string // a neutral https address abroad
	ExitName   string // «выход 3» — named in the message instead of any address
	StaggerSec int    // a pause before the message, different per exit
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

// HubWatchEnv is hubwatch.env: the watcher bot's token and the chat. Mode 0600 on the exit.
func HubWatchEnv(botToken, chatID string) string {
	return fmt.Sprintf("BOT_TOKEN='%s'\nCHAT_ID='%s'\n", botToken, chatID)
}

// HubWatchUnits are the watcher's service and its once-a-minute timer.
func HubWatchUnits() (service, timer string) {
	service = "[Unit]\nDescription=HiEarth: look at the Hub from outside\nWants=network-online.target\nAfter=network-online.target\n\n" +
		"[Service]\nType=oneshot\nExecStart=" + HubWatchScriptPath + "\nTimeoutStartSec=90\nNice=10\n"
	timer = "[Unit]\nDescription=HiEarth: look at the Hub from outside, every minute\n\n" +
		"[Timer]\nOnCalendar=minutely\nAccuracySec=5s\n\n[Install]\nWantedBy=timers.target\n"
	return service, timer
}

// HubWatchScript renders the watcher (p must have passed Validate). One run:
//
//	the Hub answers → the failure count is 0; after an alert, the second good run in a row
//	  sends «снова доступен»;
//	it does not → the count grows; from the third failure in a row: the control address must
//	  answer (else silence, the count is kept), then a pause of StaggerSec and one more look at
//	  the Hub, then «Хаб недоступен снаружи» — repeated at most every 30 minutes.
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
send() {
  . "$ENV"
  curl -fsS -m 15 -o /dev/null --data-urlencode "chat_id=$CHAT_ID" --data-urlencode "text=$1" "https://api.telegram.org/bot$BOT_TOKEN/sendMessage" 2>/dev/null
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
# Another exit may be looking too: wait this exit's turn, then look once more.
sleep "$STAGGER_SEC"
if hub_ok; then exit 0; fi
if send "🔴 Хаб недоступен снаружи (взгляд: $EXIT_NAME): $fails проверок подряд сайт не открылся по адресу хаба. Что делать, когда пульта нет: docs/runbooks/hub-down.md"; then
  alerted=1; last_alert=$(date +%s); save
fi
`
}
