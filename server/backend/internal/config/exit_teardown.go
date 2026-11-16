package config

import (
	"strconv"
	"strings"
)

// Taking an exit out of service (internal/provision DeprovisionRecipe) and its Telegram proxy.
//
// The units are the real ones on the exits (checked on exit 2, 2026-10-15; the backup tunnel since
// 2026-11-16): the services hysteria2, heyearth-stls (ShadowTLS, exit_stls.go), node-agent,
// mtproxy, and xray where the retired Reality path is still installed; the oneshots
// nftables-hysteria (port hop, loads ExitPortHopConfPath) and heyearth-exit-guard (the backup
// tunnel's kernel egress guard); the hand-made
// heyearth-agent-guard (node-agent :50052 from the Hub only). No hysteria2-server /
// hysteria-server unit exists; nftables.service is not ours (inactive on the exits) and is never
// touched. Every script tolerates a unit or table that does not exist.
//
// Deprovision parks the node: services stopped and disabled, the tunnel guard and the port hop
// removed, the tunnel password rotated — binaries, unit files and the Hub's SSH key stay, so
// «Настроить» can bring it back. The node-agent Hub-only guard stays too: a parked node keeps
// :50052 closed to the world. Deleting the record (DELETE /servers/{id}) never touches the node.

const (
	// ExitPortHopService / ExitPortHopTable: the boot unit and the nft table of ExitPortHopConf.
	ExitPortHopService = "nftables-hysteria"
	ExitPortHopTable   = "hysteria_porthopping"

	// The node-agent Hub-only guard (Beads cudk.77; made by hand on exit 2, 2026-10-15, now the
	// exit template ExitAgentGuardConf / ExitAgentGuardUnit, exit_agent_guard.go). Kept by deprovision.
	ExitAgentGuardService  = "heyearth-agent-guard"
	ExitAgentGuardTable    = "heyearth_agent_guard"
	ExitAgentGuardConfPath = "/etc/nftables-heyearth-agent.conf"

	// HubServerArchiveDir keeps a removed Hub client's config, unit and journal tail
	// (<id>-<unix time>/, mode 0700: the config carries the retired tunnel password).
	HubServerArchiveDir = "/opt/heyearth/state/server-archive"

	// MTProxy (mtg) on an exit, as cmd/deploy/install-mtproxy.sh installs it.
	MTProxyService       = "mtproxy"
	MTProxyInstallerPath = "/opt/heyearth/mtproxy/install-mtproxy.sh"
	MTProxyConfigPath    = "/opt/mtproxy/config.toml"
	MTProxyBinary        = "/usr/local/bin/mtg"

	systemdWantsDir     = "/etc/systemd/system/multi-user.target.wants"
	exitServiceUnitList = "hysteria2 " + ExitSTLSUnit + " xray node-agent " + MTProxyService
)

// ExitServiceUnits are the long-running services of an exit, stopped and disabled by deprovision.
func ExitServiceUnits() []string { return strings.Fields(exitServiceUnitList) }

// HubClientUnit is the Hub's hysteria client unit of exit id (HubHysteriaClientUnit).
func HubClientUnit(id int) string { return "hysteria2-client-" + strconv.Itoa(id) }

// HubClientConfigPath is that client's config (HubHysteriaClientYAML).
func HubClientConfigPath(id int) string {
	return HubHysteriaDir + "/client-" + strconv.Itoa(id) + ".yaml"
}

// unitOffFunc: unit_off UNIT stops and disables a unit (a missing one is fine) and removes a
// leftover enable link, so it neither runs nor starts at boot.
const unitOffFunc = `unit_off() {
  systemctl disable --now "$1" >/dev/null 2>&1 || true
  systemctl stop "$1" >/dev/null 2>&1 || true
  rm -f ` + systemdWantsDir + `/"$1".service
}
`

// HubClientRemoveScript (Hub): stop and disable both tunnel clients of exit id — hysteria
// (HubClientUnit) and the ShadowTLS backup (HubSTLSUnit), a stray process of either killed too —
// move their configs and units into a fresh HubServerArchiveDir/<id>-<time> with the journal tails
// (only when there is something to archive), delete the .bak copies (they carry the tunnel
// secrets) and their restart, pending-binary and path-check markers, daemon-reload. Fails if a
// client still runs.
func HubClientRemoveScript(id int) string {
	n := strconv.Itoa(id)
	var b strings.Builder
	b.WriteString(opLine("hub-client-remove", n) + "set -e\n" + unitOffFunc +
		"HC_ARCHIVE=" + shQuote(HubServerArchiveDir+"/"+n) + "-$(date +%s)\n")
	for _, c := range []struct{ unit, cfg, proc string }{
		{HubClientUnit(id), HubClientConfigPath(id), `(^|[ /])client-` + n + `[.]yaml( |$)`},
		{HubSTLSUnit(id), HubSTLSConfigPath(id), `(^|[ /])` + HubSTLSUnit(id) + `[.]service/client[.]json( |$)`},
	} {
		b.WriteString("HC_UNIT=" + c.unit + "\nHC_CFG=" + shQuote(c.cfg) + "\nHC_FILE=" + shQuote(unitFilePath(c.unit)) + "\n" +
			`unit_off "$HC_UNIT"
pkill -9 -f ` + shQuote(c.proc) + ` 2>/dev/null || true
hc_files=()
for f in "$HC_CFG" "$HC_FILE"; do
  if [ -f "$f" ]; then hc_files+=("$f"); fi
done
if [ "${#hc_files[@]}" -gt 0 ]; then
  (umask 077 && mkdir -p "$HC_ARCHIVE")
  journalctl -u "$HC_UNIT" -n 100 --no-pager > "$HC_ARCHIVE/$HC_UNIT.journal.log" 2>&1 || true
  mv -f "${hc_files[@]}" "$HC_ARCHIVE/"
  echo "[hub] конфигурация и юнит $HC_UNIT перенесены в архив $HC_ARCHIVE"
fi
rm -f "$HC_CFG.bak" "$HC_FILE.bak" ` + shQuote(RestartMarker(c.unit)) + " " + shQuote(BinaryPendingMarker(c.unit)) + " " + shQuote(PathCheckMarker(c.unit)) + `
systemctl daemon-reload
systemctl reset-failed "$HC_UNIT" >/dev/null 2>&1 || true
if systemctl is-active --quiet "$HC_UNIT"; then
  echo "[hub] ERROR: $HC_UNIT всё ещё работает" >&2
  exit 1
fi
echo "[hub] клиент туннеля $HC_UNIT снят"
`)
	}
	return b.String()
}

// HubClientStateScript (Hub) prints UNIT, CONFIG (files present), ACTIVE and LINK (an enable
// link left) of exit id's tunnel clients (hysteria and the ShadowTLS backup), each 1 when either
// client has it, else 0.
func HubClientStateScript(id int) string {
	var units, cfgs, links []string
	for _, c := range []struct{ unit, cfg string }{{HubClientUnit(id), HubClientConfigPath(id)}, {HubSTLSUnit(id), HubSTLSConfigPath(id)}} {
		units = append(units, c.unit)
		cfgs = append(cfgs, c.cfg)
		links = append(links, systemdWantsDir+"/"+c.unit+".service")
	}
	either := func(test string, args []string, f func(string) string) string {
		conds := make([]string, len(args))
		for i, a := range args {
			conds[i] = test + " " + f(a)
		}
		return strings.Join(conds, " || ")
	}
	return opLine("hub-client-state", strconv.Itoa(id)) +
		"if " + either("[ -f", units, func(u string) string { return shQuote(unitFilePath(u)) + " ]" }) + "; then echo UNIT=1; else echo UNIT=0; fi\n" +
		"if " + either("[ -f", cfgs, func(c string) string { return shQuote(c) + " ]" }) + "; then echo CONFIG=1; else echo CONFIG=0; fi\n" +
		"if " + either("systemctl is-active --quiet", units, func(u string) string { return u }) + "; then echo ACTIVE=1; else echo ACTIVE=0; fi\n" +
		"if " + either("[ -e", links, func(w string) string { return shQuote(w) + " ] || [ -L " + shQuote(w) + " ]" }) + "; then echo LINK=1; else echo LINK=0; fi\n"
}

// ExitHysteriaConfigLegacy is the other place a hysteria server config may sit on an exit (the
// Hub's layout; exit 2 was found with its tunnel password there, 2026-10-26). The exit's unit
// (ExitHysteriaUnit) reads ExitHysteriaConfig; a copy here is never written by the recipes, but it
// carries the tunnel password all the same: deprovision rotates it with the other copy, and the
// keys step cross-checks it (internal/provision adoptLegacyKeys).
const ExitHysteriaConfigLegacy = "/etc/hysteria/server.yaml"

// rotateVars: the new password, its server.yaml line rendered in Go exactly as the templates do
// (ExitHysteriaServerYAML: yamlPassword — "null", "true", "123" become quoted strings), and the
// files that carry the tunnel password on an exit: both hysteria server configs and the keys.
func rotateVars(op, newPassword string) string {
	return opLine(op) + "NEW=" + shQuote(newPassword) + "\nRP_LINE=" + shQuote("  password: "+yamlPassword(newPassword)) +
		"\nRP_YAML=" + shQuote(ExitHysteriaConfig) + "\nRP_YAML2=" + shQuote(ExitHysteriaConfigLegacy) +
		"\nRP_KEYS=" + shQuote(ExitKeysPath) + "\n" +
		`RP_JSON_RE='^(.*"ssPassword"[[:space:]]*:[[:space:]]*")([^"]*)(".*)$'
`
}

// ExitRotatePasswordScript makes a retired exit refuse the old tunnel password: in both hysteria
// server configs (ExitHysteriaConfig and ExitHysteriaConfigLegacy, whichever exist: the auth
// "  password:" line becomes RP_LINE) and in reality-keys.json (ssPassword, read by a later
// «Настроить») it becomes newPassword, which nobody keeps — one copy left with the old password
// would also make a later «Настроить» find two different passwords on the node. Only the base64
// alphabet is accepted (a JSON string as is). Files are rewritten by bash itself (the password
// never reaches a command line), via temp file + mv with owner and mode kept; their .bak copies
// (old password) are deleted. A server config without the password line is an unknown format:
// nothing is changed (checked for both before any write) and the script fails. Never prints the
// password.
func ExitRotatePasswordScript(newPassword string) string {
	return rotateVars("rotate-password", newPassword) + `set -e
case "$NEW" in ""|*[!A-Za-z0-9+/=]*) echo "[exit] ERROR: новый пароль туннеля — не base64" >&2; exit 1 ;; esac
RP_TMP=""
trap 'rm -f "$RP_TMP"' EXIT
rp_line() { # $1 line, $2 yaml|json → the line with the new password; RP_HIT=1 when it carried one
  RP_HIT=0
  RP_OUT=$1
  if [ "$2" = yaml ]; then
    case "$1" in "  password: "*) RP_OUT=$RP_LINE; RP_HIT=1 ;; esac
  elif [[ $1 =~ $RP_JSON_RE ]]; then
    RP_OUT="${BASH_REMATCH[1]}$NEW${BASH_REMATCH[3]}"; RP_HIT=1
  fi
}
rp_count() {
  local l n=0
  while IFS= read -r l || [ -n "$l" ]; do rp_line "$l" "$2"; n=$((n + RP_HIT)); done < "$1"
  echo "$n"
}
rp_rewrite() {
  local l
  RP_TMP=$(mktemp "$1.XXXXXX")
  while IFS= read -r l || [ -n "$l" ]; do rp_line "$l" "$2"; printf '%s\n' "$RP_OUT"; done < "$1" > "$RP_TMP"
  chown --reference="$1" "$RP_TMP" 2>/dev/null || true
  chmod --reference="$1" "$RP_TMP"
  mv -f "$RP_TMP" "$1"
  RP_TMP=""
}
for y in "$RP_YAML" "$RP_YAML2"; do
  if [ -f "$y" ] && [ "$(rp_count "$y" yaml)" = 0 ]; then
    echo "[exit] ERROR: в $y нет строки «  password:» — формат неизвестен, пароль не меняю" >&2
    exit 1
  fi
done
rp_done=""
if [ -f "$RP_KEYS" ] && [ "$(rp_count "$RP_KEYS" json)" != 0 ]; then rp_rewrite "$RP_KEYS" json; rp_done="$rp_done reality-keys.json"; fi
for y in "$RP_YAML" "$RP_YAML2"; do
  if [ -f "$y" ]; then rp_rewrite "$y" yaml; rp_done="$rp_done $y"; fi
done
rm -f "$RP_YAML.bak" "$RP_YAML2.bak" "$RP_KEYS.bak"
if [ -n "$rp_done" ]; then
  echo "[exit] пароль туннеля заменён случайным, которого никто не хранит:$rp_done"
else
  echo "[exit] пароля туннеля на ноде нет — менять нечего"
fi
`
}

// ExitPasswordCheckScript prints YAML=absent|new|old|none (the hysteria server configs: none
// there; only password lines equal to RP_LINE; another password; no password line — of the two
// copies the worse state counts: old, none, new, absent) and KEYS=absent|new|old|none
// (reality-keys.json ssPassword). Compares inside bash.
func ExitPasswordCheckScript(newPassword string) string {
	return rotateVars("password-check", newPassword) + `pc_state() { # $1 file, $2 yaml|json
  local l r=none
  [ -f "$1" ] || { echo absent; return; }
  while IFS= read -r l || [ -n "$l" ]; do
    if [ "$2" = yaml ]; then
      if [ "$l" = "$RP_LINE" ]; then [ "$r" = old ] || r=new
      else case "$l" in "  password: "*) r=old ;; esac
      fi
    elif [[ $l =~ $RP_JSON_RE ]]; then
      if [ "${BASH_REMATCH[2]}" = "$NEW" ]; then [ "$r" = old ] || r=new; else r=old; fi
    fi
  done < "$1"
  echo "$r"
}
pc_yaml() { # both copies: the worse state
  local a b r
  a=$(pc_state "$RP_YAML" yaml)
  b=$(pc_state "$RP_YAML2" yaml)
  for r in old none new absent; do
    if [ "$a" = "$r" ] || [ "$b" = "$r" ]; then echo "$r"; return; fi
  done
}
echo "YAML=$(pc_yaml)"
echo "KEYS=$(pc_state "$RP_KEYS" json)"
`
}

// ExitStopServicesScript stops and disables the exit's services (ExitServiceUnits); fails if
// one still runs.
const ExitStopServicesScript = `# hp:stop-services
` + unitOffFunc + `for u in ` + exitServiceUnitList + `; do unit_off "$u"; done
left=""
for u in ` + exitServiceUnitList + `; do
  if systemctl is-active --quiet "$u"; then left="$left $u"; fi
done
if [ -n "$left" ]; then
  echo "[exit] ERROR: всё ещё работают:$left" >&2
  exit 1
fi
echo "[exit] остановлены и выключены: ` + exitServiceUnitList + `"
`

// nftSetRemoveBody removes one of our nft rule sets: its boot unit (NS_UNIT, file NS_FILE) off,
// the table NS_TABLE deleted, the files (NS_CONF, NS_FILE and their .bak) gone; fails if the
// table is still loaded.
const nftSetRemoveBody = unitOffFunc + `unit_off "$NS_UNIT"
if command -v nft >/dev/null 2>&1; then nft delete table inet "$NS_TABLE" >/dev/null 2>&1 || true; fi
rm -f "$NS_CONF" "$NS_CONF.bak" "$NS_FILE" "$NS_FILE.bak"
systemctl daemon-reload >/dev/null 2>&1 || true
if command -v nft >/dev/null 2>&1 && nft list table inet "$NS_TABLE" >/dev/null 2>&1; then
  echo "[exit] ERROR: таблица inet $NS_TABLE осталась в nftables" >&2
  exit 1
fi
echo "[exit] сняты $NS_WHAT (table inet $NS_TABLE, $NS_UNIT)"
`

// ExitRemoveGuardScript removes the backup tunnel's kernel egress guard (ExitGuardConf/Unit) —
// never while the tunnel (or a not yet retired xray, the guard's previous subject) runs: it would
// run unguarded. The tunnel unit Requires= the guard unit, so without it the tunnel cannot be
// started by hand either.
const ExitRemoveGuardScript = `# hp:remove-guard
for u in ` + ExitSTLSUnit + ` xray; do
  if systemctl is-active --quiet "$u"; then
    echo "[exit] ERROR: $u ещё работает — защиту исходящих не снимаю" >&2
    exit 1
  fi
done
NS_UNIT=` + ExitGuardService + `
NS_TABLE=` + ExitGuardTable + `
NS_CONF=` + ExitGuardConfPath + `
NS_FILE=` + ExitGuardUnitPath + `
NS_WHAT='правила защиты исходящих запасного туннеля'
` + nftSetRemoveBody

// ExitRemoveHopScript removes the port hop (ExitPortHopConf / ExitPortHopUnit).
const ExitRemoveHopScript = `# hp:remove-hop
NS_UNIT=` + ExitPortHopService + `
NS_TABLE=` + ExitPortHopTable + `
NS_CONF=` + ExitPortHopConfPath + `
NS_FILE=` + ExitPortHopUnitPath + `
NS_WHAT='правила прыжков портов'
` + nftSetRemoveBody

// ExitTeardownStateScript prints, for the exit services and both oneshots, ACTIVE_<unit> and
// ENABLED_<unit> (0|1), and GUARD_TABLE/GUARD_FILES, HOP_TABLE/HOP_FILES (0|1).
const ExitTeardownStateScript = `# hp:teardown-state
for u in ` + exitServiceUnitList + ` ` + ExitGuardService + ` ` + ExitPortHopService + `; do
  if systemctl is-active --quiet "$u"; then echo "ACTIVE_$u=1"; else echo "ACTIVE_$u=0"; fi
  case "$(systemctl is-enabled "$u" 2>/dev/null)" in enabled|enabled-runtime) echo "ENABLED_$u=1" ;; *) echo "ENABLED_$u=0" ;; esac
done
ts_table() { if command -v nft >/dev/null 2>&1 && nft list table inet "$1" >/dev/null 2>&1; then echo 1; else echo 0; fi; }
ts_files() { local f; for f in "$@"; do if [ -e "$f" ]; then echo 1; return; fi; done; echo 0; }
echo "GUARD_TABLE=$(ts_table ` + ExitGuardTable + `)"
echo "GUARD_FILES=$(ts_files ` + ExitGuardConfPath + ` ` + ExitGuardUnitPath + `)"
echo "HOP_TABLE=$(ts_table ` + ExitPortHopTable + `)"
echo "HOP_FILES=$(ts_files ` + ExitPortHopConfPath + ` ` + ExitPortHopUnitPath + `)"
`

// MTProxyRunScript runs install-mtproxy.sh at path (idempotent; port, secret and domain are
// validated by the caller) with $1..$3 = port, secret, domain. The installer is sourced in a
// subshell instead of `bash path args`: the secret is shared by every exit and must not sit in a
// process's argv for the minutes a first build takes. Its exit ends only the subshell and fails
// the script.
func MTProxyRunScript(path string, port int, secret, domain string) string {
	return opLine("mtproxy-install") + "set -e\nMT_SCRIPT=" + shQuote(path) + "\nset -- " + shQuote(strconv.Itoa(port)) + " " +
		shQuote(secret) + " " + shQuote(domain) + "\n( . \"$MT_SCRIPT\" )\n"
}

// MTProxyCheckScript prints ACTIVE (mtproxy.service), LISTEN (mtg on TCP port), SECRET
// (MTProxyConfigPath has exactly this secret; compared inside bash), each 0|1, and VERSION (mtg's).
func MTProxyCheckScript(port int, secret string) string {
	return opLine("mtproxy-check") + "S=" + shQuote(secret) + "\n" +
		"if systemctl is-active --quiet " + MTProxyService + "; then echo ACTIVE=1; else echo ACTIVE=0; fi\n" +
		"if ss -Htlnp 'sport = :" + strconv.Itoa(port) + "' 2>/dev/null | grep -q '\"mtg\"'; then echo LISTEN=1; else echo LISTEN=0; fi\n" +
		"mc=0\nif [ -r " + MTProxyConfigPath + " ]; then\n" +
		"  while IFS= read -r l || [ -n \"$l\" ]; do if [ \"$l\" = \"secret = \\\"$S\\\"\" ]; then mc=1; fi; done < " + MTProxyConfigPath + "\nfi\n" +
		"echo \"SECRET=$mc\"\n" +
		"echo \"VERSION=$(" + MTProxyBinary + " --version 2>/dev/null | awk 'NR==1{print $1}')\"\n"
}
