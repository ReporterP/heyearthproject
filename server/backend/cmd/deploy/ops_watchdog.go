package main

// The Hub's pulse to an outside watchdog (docs/prd/stability-operations.prd.md, part 2): a
// systemd timer opens an address of a hosted service once a minute; when the pulse stops —
// the Hub is off, hung or cut from the network — that service raises the alarm through its
// own channels. The API sends a second pulse of its own (cmd/api/watchdog.go).
//
// WATCHDOG_PULSE_URL in server/.env is the Hub's pulse address. It is a secret: whoever knows
// it can keep the pulse alive in the Hub's place. On the Hub it lives in /etc/heyearth/pulse.env
// (mode 600) and nowhere else — not in a unit, not in the script, not in an output line.
//
// Installing it is impact class К0: three files, a timer, no service is touched.

import (
	"fmt"
	"log"
	"regexp"
	"strings"

	"golang.org/x/crypto/ssh"
)

const pulseScript = `#!/usr/bin/env bash
# Installed by deploy --setup-watchdog (cmd/deploy/ops_watchdog.go). DO NOT EDIT.
# The Hub's pulse: an outside service raises the alarm when it stops.
set -eu
. /etc/heyearth/pulse.env
exec curl -fsS -m 10 --retry 2 -o /dev/null "$PULSE_URL"
`

const pulseServiceUnit = "[Unit]\nDescription=HiEarth Hub pulse to the outside watchdog\nWants=network-online.target\nAfter=network-online.target\n\n[Service]\nType=oneshot\nExecStart=/opt/heyearth/bin/heyearth-pulse.sh\nTimeoutStartSec=45\n"

const pulseTimerUnit = "[Unit]\nDescription=HiEarth Hub pulse, every minute\n\n[Timer]\nOnCalendar=minutely\nAccuracySec=5s\n\n[Install]\nWantedBy=timers.target\n"

// pulseURLRe: an https address that goes into a single-quoted shell assignment as it is.
var pulseURLRe = regexp.MustCompile(`^https://[A-Za-z0-9.-]+\.[A-Za-z]{2,}(:[0-9]{1,5})?/[A-Za-z0-9._~:/?#@!&*+,;=%-]{1,300}$`)

func validatePulseURL(u string) error {
	if !pulseURLRe.MatchString(u) {
		return fmt.Errorf("WATCHDOG_PULSE_URL не похож на адрес пульса (https://хост/путь, без пробелов и кавычек)")
	}
	return nil
}

// renderPulseInstallScript installs the pulse for pulseURL (already validated); "" when there
// is no address — then nothing is installed. The script prints no address.
func renderPulseInstallScript(pulseURL string) string {
	if pulseURL == "" {
		return ""
	}
	files := []opsFile{
		{"/etc/heyearth/pulse.env", "600", "PULSE_URL='" + pulseURL + "'\n"},
		{"/opt/heyearth/bin/heyearth-pulse.sh", "700", pulseScript},
		{"/etc/systemd/system/heyearth-pulse.service", "644", pulseServiceUnit},
		{"/etc/systemd/system/heyearth-pulse.timer", "644", pulseTimerUnit},
	}
	return `set -eu
command -v curl >/dev/null 2>&1 || { echo "[ops] ABORT: curl is not installed — pulse not installed"; exit 1; }
` + opsPutFileFunc + `mkdir -p /etc/heyearth /opt/heyearth/bin
` + putFileCalls(files) + `systemd-analyze verify /etc/systemd/system/heyearth-pulse.service /etc/systemd/system/heyearth-pulse.timer
systemctl daemon-reload
systemctl enable --now heyearth-pulse.timer
if systemctl start heyearth-pulse.service; then
  echo "[ops] pulse: the first beat was accepted by the outside watchdog"
else
  echo "[ops] FAILED: the first beat did not get through (is the address right? can the Hub reach the service?)"; exit 1
fi
systemctl list-timers heyearth-pulse.timer --no-pager
`
}

// pulseStep is the pulse's install script for the address in server/.env; ok is false when no
// address is set — setup-ops then has no pulse step at all.
func pulseStep(pulseURL string) (script string, ok bool) {
	pulseURL = strings.TrimSpace(pulseURL)
	if pulseURL == "" {
		return "", false
	}
	return renderPulseInstallScript(pulseURL), true
}

// setupWatchdog installs the Hub's pulse (deploy --setup-watchdog).
func setupWatchdog(client *ssh.Client, cfg *deployConfig) {
	pulseURL := strings.TrimSpace(cfg.env.get("WATCHDOG_PULSE_URL", ""))
	if pulseURL == "" {
		log.Fatal("[watchdog] WATCHDOG_PULSE_URL не задан в server/.env: это адрес пульса хаба во внешнем стороже")
	}
	if err := validatePulseURL(pulseURL); err != nil {
		log.Fatalf("[watchdog] %v", err)
	}
	fmt.Println("[watchdog] К0: ставится таймер пульса, ни один сервис не перезапускается")
	if err := runOpsScript(client, "hub pulse (setup-watchdog)", renderPulseInstallScript(pulseURL)); err != nil {
		log.Fatalf("[watchdog] %v", err)
	}
	fmt.Println("[watchdog] setup-watchdog: OK")
}
