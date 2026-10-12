package config

import (
	"fmt"
	"strings"
)

// HysteriaVersion — the single pinned apernet/hysteria release for every install path
// (cmd/deploy/hysteria2_hub.go, cmd/deploy/main.go setupExitNodeDeploy,
// internal/admin/servers_handlers.go). Upgrade exits (servers) before the Hub:
// 2.8.2+ clients lose UDP against older servers. That is why only exit-side paths and
// the Hub's own deploy --setup-hysteria2-hub upgrade (HysteriaInstallScript); Hub-side
// steps of exit provisioning only fill in a missing binary (HysteriaEnsureScript).
const HysteriaVersion = "v2.13.0"

// HysteriaDownloadURL — linux-amd64 asset; the tag "app/vX.Y.Z" has its slash encoded.
func HysteriaDownloadURL() string {
	return "https://github.com/apernet/hysteria/releases/download/app%2F" + HysteriaVersion + "/hysteria-linux-amd64"
}

// Output lines of HysteriaInstallScript that HysteriaInstallError looks for.
const (
	hysteriaInstalledLine = "[hysteria] installed " + HysteriaVersion + " "
	hysteriaCurrentLine   = "[hysteria] " + HysteriaVersion + " already installed"
	hysteriaErrorLine     = "[hysteria] ERROR"
)

// HysteriaInstallScript — idempotent shell: installs/upgrades /usr/local/bin/hysteria to
// HysteriaVersion. The download must report exactly HysteriaVersion before it replaces
// anything; the previous binary is kept as hysteria.bak (written via a temp file + mv, so
// a running .bak never hits ETXTBSY). On failure it prints "[hysteria] ERROR" to stderr,
// leaves the installed binary untouched and returns non-zero. Safe under set -eo pipefail.
// Does not restart units.
func HysteriaInstallScript() string {
	return `HY_WANT="` + HysteriaVersion + `"
HY_CUR=$(/usr/local/bin/hysteria version 2>/dev/null | awk '$1=="Version:"{print $2}') || HY_CUR=""
if [ "$HY_CUR" != "$HY_WANT" ]; then
  if curl -fsSL --connect-timeout 15 --max-time 300 --retry 3 --retry-delay 2 -o /usr/local/bin/hysteria.new "` + HysteriaDownloadURL() + `" \
    && chmod +x /usr/local/bin/hysteria.new \
    && [ "$(/usr/local/bin/hysteria.new version 2>/dev/null | awk '$1=="Version:"{print $2}')" = "$HY_WANT" ] \
    && { [ ! -x /usr/local/bin/hysteria ] || { cp -p /usr/local/bin/hysteria /usr/local/bin/hysteria.bak.tmp && mv -f /usr/local/bin/hysteria.bak.tmp /usr/local/bin/hysteria.bak; }; } \
    && mv -f /usr/local/bin/hysteria.new /usr/local/bin/hysteria; then
    echo "[hysteria] installed $HY_WANT (previous: ${HY_CUR:-none} -> /usr/local/bin/hysteria.bak)"
  else
    rm -f /usr/local/bin/hysteria.new /usr/local/bin/hysteria.bak.tmp
    echo "[hysteria] ERROR: download/verify of $HY_WANT failed, binary unchanged" >&2
    false
  fi
else
  echo "[hysteria] $HY_WANT already installed"
fi
`
}

// HysteriaEnsureScript — for the Hub-side steps of exit provisioning: installs
// HysteriaVersion only when /usr/local/bin/hysteria is absent and never touches an existing
// binary. The Hub's server and its clients to every exit share that binary and pick it up on
// their next restart, so upgrading it here would put a new client in front of exits that may
// still run an older server (2.8.2+ clients lose UDP against older servers). The Hub is
// upgraded on purpose, after all exits, by deploy --setup-hysteria2-hub.
func HysteriaEnsureScript() string {
	return `if [ -x /usr/local/bin/hysteria ]; then
  echo "[hysteria] keeping existing Hub binary (upgrade: deploy --setup-hysteria2-hub)"
else
` + HysteriaInstallScript() + `fi
`
}

// HysteriaInstallError checks the captured output of a script that ran HysteriaInstallScript.
// It returns an error when the output carries an "[hysteria] ERROR" line, or when it lacks
// both success lines for HysteriaVersion (e.g. set -e stopped the script before or inside
// the install). The ERROR line goes to stderr, so callers that capture stdout only must also
// check the script's exit status; the missing success line still catches that case.
func HysteriaInstallError(out string) error {
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, hysteriaErrorLine) {
			return fmt.Errorf("hysteria install failed: %s", strings.TrimSpace(line))
		}
	}
	if !strings.Contains(out, hysteriaInstalledLine) && !strings.Contains(out, hysteriaCurrentLine) {
		return fmt.Errorf("hysteria install did not report %s (no %q / %q line)",
			HysteriaVersion, strings.TrimSpace(hysteriaInstalledLine), hysteriaCurrentLine)
	}
	return nil
}
