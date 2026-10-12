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
