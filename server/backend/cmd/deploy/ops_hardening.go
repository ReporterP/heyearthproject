package main

// Hub hardening — `deploy --setup-hardening` (also the last steps of --setup-ops): kernel
// network settings, security auto-updates, sshd limits and the per-source flood table (nft).
//
// The changes that can lock the operator out (sshd, the firewall) go through guardedApply: a
// transient systemd timer undoes them after guardSeconds unless a FRESH SSH login (new TCP
// connection, new authentication) proves the Hub is still reachable and cancels the timer.
// Runbook: docs/runbooks/ddos.md.

import (
	"fmt"
	"log"
	"regexp"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
)

// guardSeconds: how long a guarded change lives without a confirming login.
const guardSeconds = 300

// ───────────────────────── kernel network settings ─────────────────────────

// hardeningSysctl: SYN flood resistance (cookies + larger backlog + fewer SYN-ACK retries),
// a conntrack table sized for a flood (8 GB RAM: 512k entries ≈ 160 MB) and an established
// timeout of a day instead of five (xray / nginx drop idle sessions after an hour anyway).
// rp_filter stays as the host has it (2 = loose): the Hub answers from one address through
// another gateway, strict mode would drop that.
const hardeningSysctl = `# Installed by deploy --setup-hardening (cmd/deploy/ops_hardening.go)
net.ipv4.tcp_syncookies = 1
net.ipv4.tcp_max_syn_backlog = 8192
net.ipv4.tcp_synack_retries = 3
net.core.somaxconn = 8192
net.core.netdev_max_backlog = 16384
net.ipv4.tcp_rfc1337 = 1
net.ipv4.conf.all.accept_redirects = 0
net.ipv4.conf.default.accept_redirects = 0
net.ipv4.conf.all.send_redirects = 0
net.ipv4.conf.default.send_redirects = 0
net.ipv4.conf.all.accept_source_route = 0
net.ipv6.conf.all.accept_redirects = 0
net.ipv6.conf.default.accept_redirects = 0
net.ipv4.icmp_echo_ignore_broadcasts = 1
net.netfilter.nf_conntrack_max = 524288
net.netfilter.nf_conntrack_tcp_timeout_established = 86400
net.netfilter.nf_conntrack_tcp_timeout_time_wait = 60
`

// renderSysctlScript installs the sysctl file, loads nf_conntrack at boot before
// systemd-sysctl (else the conntrack keys are silently skipped after a reboot), sizes the
// conntrack hash and applies everything now.
func renderSysctlScript() string {
	return `set -eu
` + opsPutFileFunc + putFileCalls([]opsFile{
		{"/etc/sysctl.d/60-heyearth-net.conf", "644", hardeningSysctl},
		{"/etc/modules-load.d/heyearth-conntrack.conf", "644", "nf_conntrack\n"},
		{"/etc/modprobe.d/heyearth-conntrack.conf", "644", "options nf_conntrack hashsize=131072\n"},
	}) + `modprobe nf_conntrack
hs=/sys/module/nf_conntrack/parameters/hashsize
if [ -w "$hs" ] && [ "$(cat "$hs")" -lt 131072 ]; then echo 131072 > "$hs"; fi
sysctl -q -p /etc/sysctl.d/60-heyearth-net.conf
# The Reality private key and the master UUID: root only (claude-security F45/F49).
[ -f /opt/heyearth/state/reality-keys.json ] && chmod 600 /opt/heyearth/state/reality-keys.json
echo "[ops] sysctl: syn_backlog=$(sysctl -n net.ipv4.tcp_max_syn_backlog) synack_retries=$(sysctl -n net.ipv4.tcp_synack_retries) conntrack=$(sysctl -n net.netfilter.nf_conntrack_count)/$(sysctl -n net.netfilter.nf_conntrack_max) hash=$(cat "$hs")"
`
}

// ───────────────────────── security auto-updates ─────────────────────────

// The Hub had both apt timers disabled (no update since January). Security pocket only (the
// stock 50unattended-upgrades origins), at 04:30 MSK (after the 04:00 backup, the quietest
// hour), no automatic reboot (a kernel update waits for the owner's reboot), services that use
// an updated library are restarted (needrestart 'a') — a few seconds of reconnects at night.
const (
	unattendedConf = `// Installed by deploy --setup-hardening (cmd/deploy/ops_hardening.go)
Unattended-Upgrade::Automatic-Reboot "false";
Unattended-Upgrade::Remove-Unused-Kernel-Packages "true";
Unattended-Upgrade::Remove-Unused-Dependencies "false";
Unattended-Upgrade::MinimalSteps "true";
APT::Periodic::Update-Package-Lists "1";
APT::Periodic::Unattended-Upgrade "1";
`
	aptUpgradeTimerDropIn = `# Installed by deploy --setup-hardening (cmd/deploy/ops_hardening.go)
[Timer]
OnCalendar=
OnCalendar=*-*-* 04:30:00 Europe/Moscow
RandomizedDelaySec=0
`
	needrestartConf = "# Installed by deploy --setup-hardening (cmd/deploy/ops_hardening.go)\n$nrconf{restart} = 'a';\n"
)

func renderUnattendedScript() string {
	return `set -eu
if ! dpkg -s unattended-upgrades >/dev/null 2>&1; then
  timeout 300 apt-get update -qq || true
  DEBIAN_FRONTEND=noninteractive timeout 600 apt-get install -y -qq unattended-upgrades
fi
` + opsPutFileFunc + putFileCalls([]opsFile{
		{"/etc/apt/apt.conf.d/52heyearth-unattended", "644", unattendedConf},
		{"/etc/systemd/system/apt-daily-upgrade.timer.d/heyearth.conf", "644", aptUpgradeTimerDropIn},
		{"/etc/needrestart/conf.d/heyearth.conf", "644", needrestartConf},
	}) + `systemctl daemon-reload
systemctl enable --now apt-daily.timer apt-daily-upgrade.timer >/dev/null 2>&1
for t in apt-daily.timer apt-daily-upgrade.timer; do
  [ "$(systemctl is-enabled "$t")" = enabled ] && systemctl is-active --quiet "$t" || { echo "[ops] FAILED: $t is not enabled+active"; exit 1; }
done
echo "[ops] auto-updates: next run $(systemctl list-timers apt-daily-upgrade.timer --no-legend 2>/dev/null | awk '{print $1, $2, $3}')"
echo "[ops] pending security updates: $(apt-get -s upgrade 2>/dev/null | grep -c '^Inst .*-security')"
[ -f /var/run/reboot-required ] && echo "[ops] NOTE: a reboot is required ($(tr '\n' ' ' < /var/run/reboot-required.pkgs 2>/dev/null | cut -c1-120))" || true
`
}

// ───────────────────────── sshd ─────────────────────────

// sshdHardeningConf is read before 50-cloud-init.conf (first value wins in sshd): fewer
// unauthenticated slots per bot (PerSourceMaxStartups) and a shorter grace time keep the
// brute-force noise (≈2400 failures a day) from filling MaxStartups and locking the owner out;
// no X11 / agent forwarding. Password login stays as it is until the owner turns it off
// (--ssh-password off, after the key login works from their own machine).
const sshdHardeningBase = `# Installed by deploy --setup-hardening (cmd/deploy/ops_hardening.go). Read before
# 50-cloud-init.conf: the first value of an option wins.
X11Forwarding no
AllowAgentForwarding no
LoginGraceTime 30
MaxAuthTries 4
MaxStartups 30:30:120
PerSourceMaxStartups 3
PerSourceNetBlockSize 32:128
ClientAliveInterval 60
ClientAliveCountMax 3
`

const sshdPasswordOff = `PasswordAuthentication no
KbdInteractiveAuthentication no
PermitRootLogin prohibit-password
`

const sshdDropInPath = "/etc/ssh/sshd_config.d/10-heyearth.conf"

func renderSSHDConf(passwordOff bool) string {
	if passwordOff {
		return sshdHardeningBase + sshdPasswordOff
	}
	return sshdHardeningBase
}

// renderSSHDApply writes the drop-in (old copy kept for the rollback), validates with sshd -t
// (restores and fails on error) and reloads sshd: existing sessions stay up.
func renderSSHDApply(conf string) string {
	return `set -eu
P=` + sshdDropInPath + `
B=/opt/heyearth/state/guard/sshd.prev
mkdir -p /opt/heyearth/state/guard; chmod 700 /opt/heyearth/state/guard
if [ -f "$P" ]; then cp -p "$P" "$B"; else rm -f "$B"; fi
` + opsPutFileFunc + putFileCalls([]opsFile{{sshdDropInPath, "644", conf}}) + `if ! sshd -t; then
  if [ -f "$B" ]; then cp -p "$B" "$P"; else rm -f "$P"; fi
  echo "[ops] FAILED: sshd -t rejected the drop-in — restored"; exit 1
fi
systemctl reload ssh
sshd -T 2>/dev/null | grep -iE '^(passwordauthentication|permitrootlogin|maxstartups|persourcemaxstartups|logingracetime|maxauthtries|x11forwarding) ' | sed 's/^/[ops] sshd: /'
`
}

const sshdRollback = `P=` + sshdDropInPath + `; B=/opt/heyearth/state/guard/sshd.prev
if [ -f "$B" ]; then cp -p "$B" "$P"; else rm -f "$P"; fi
systemctl reload ssh || systemctl restart ssh
`

// ───────────────────────── per-source flood table (nft) ─────────────────────────

// ddosLimits are the per-source thresholds. Mode "observe" only counts (and logs a sample of)
// sources over them; "enforce" drops the excess. The *_lo meters always only count: they show
// how close normal traffic comes (calibration, see the runbook). Thresholds sit far above a
// mobile CGNAT address with many users behind it; the entries (relays) and exits — one address
// for many users — are exempt (allow sets, refreshed from the servers table every 5 minutes).
type ddosLimits struct {
	tcpRate, tcpBurst int // new TCP connections per second to 80/443/2443/8447
	tcpLo             int
	sshPerMin         int // new connections per minute to 22
	udpRate, udpBurst int // UDP packets per second to the Hysteria2 server (50443 + hop range)
	udpLo             int
}

var defaultDDoSLimits = ddosLimits{tcpRate: 100, tcpBurst: 400, tcpLo: 30, sshPerMin: 12, udpRate: 25000, udpBurst: 50000, udpLo: 8000}

var ddosModeRe = regexp.MustCompile(`^(observe|enforce)$`)

// renderDDoSNft renders /etc/heyearth/ddos.nft. The table is replaced atomically (add + delete +
// define in one nft -f transaction). It only ever drops or returns, never accepts, so ufw still
// decides everything else. Replies to the Hub's own connections (the tunnels to the exits) are
// never limited.
func renderDDoSNft(mode string, l ddosLimits) string {
	act := ""
	logTCP, logUDP, logSSH := "", "", ""
	if mode == "enforce" {
		act = " drop"
	} else {
		logTCP = ` limit rate 6/minute log prefix "he-ddos-tcp: " level info`
		logUDP = ` limit rate 6/minute log prefix "he-ddos-udp: " level info`
		logSSH = ` limit rate 6/minute log prefix "he-ddos-ssh: " level info`
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, `# Generated by deploy --setup-hardening (cmd/deploy/ops_hardening.go), mode %s. Do not edit:
# change the mode / thresholds in the deploy tool. Runbook: docs/runbooks/ddos.md.
add table inet heyearth_ddos
delete table inet heyearth_ddos
table inet heyearth_ddos {
	set allow4 { type ipv4_addr; flags interval; auto-merge; }
	set allow6 { type ipv6_addr; flags interval; auto-merge; }
`, mode)
	for _, s := range []string{"tcp", "tcp_lo", "ssh", "udp", "udp_lo"} {
		fmt.Fprintf(&sb, "\tset %[1]s4 { type ipv4_addr; size 262144; flags dynamic,timeout; timeout 2m; }\n\tset %[1]s6 { type ipv6_addr; size 262144; flags dynamic,timeout; timeout 2m; }\n", s)
	}
	for _, c := range []string{"over_tcp", "over_tcp_lo", "over_ssh", "over_udp", "over_udp_lo"} {
		fmt.Fprintf(&sb, "\tcounter %s { }\n", c)
	}
	// 8447 is the entries' listener (PROXY protocol): only servers may connect, in both modes.
	sb.WriteString(`	counter not_relay_8447 { }
	chain input {
		type filter hook input priority filter - 10; policy accept;
		iif "lo" return
		ip saddr @allow4 return
		ip6 saddr @allow6 return
		ct direction reply return
		tcp dport 8447 counter name not_relay_8447 drop
`)
	for _, fam := range []struct{ f, a string }{{"4", "ip saddr"}, {"6", "ip6 saddr"}} {
		fmt.Fprintf(&sb, "\t\ttcp dport 22 ct state new update @ssh%s { %s limit rate over %d/minute burst %d packets } counter name over_ssh%s%s\n", fam.f, fam.a, l.sshPerMin, l.sshPerMin, logSSH, act)
		fmt.Fprintf(&sb, "\t\ttcp dport { 80, 443, 2443, 8447 } ct state new update @tcp_lo%s { %s limit rate over %d/second burst %d packets } counter name over_tcp_lo\n", fam.f, fam.a, l.tcpLo, l.tcpLo*2)
		fmt.Fprintf(&sb, "\t\ttcp dport { 80, 443, 2443, 8447 } ct state new update @tcp%s { %s limit rate over %d/second burst %d packets } counter name over_tcp%s%s\n", fam.f, fam.a, l.tcpRate, l.tcpBurst, logTCP, act)
		fmt.Fprintf(&sb, "\t\tudp dport { 50443, 30000-65000 } update @udp_lo%s { %s limit rate over %d/second burst %d packets } counter name over_udp_lo\n", fam.f, fam.a, l.udpLo, l.udpLo*2)
		fmt.Fprintf(&sb, "\t\tudp dport { 50443, 30000-65000 } update @udp%s { %s limit rate over %d/second burst %d packets } counter name over_udp%s%s\n", fam.f, fam.a, l.udpRate, l.udpBurst, logUDP, act)
	}
	sb.WriteString("\t}\n}\n")
	return sb.String()
}

// ddosAllowScript fills allow4/allow6 with the addresses of every server in the DB (exits and
// entries: one address for many users), the Hub's own addresses and the entries' GRE tunnel
// addresses (the far end of every he-r<id> device, internal/config relay_tunnel.go: through the
// tunnel an entry connects from it — :8447 would be dropped, its users limited as one source). A
// DB error keeps the current sets (an empty set would put the entries under the per-source limits).
const ddosAllowScript = `#!/bin/sh
# Installed by deploy --setup-hardening (cmd/deploy/ops_hardening.go): the flood table's allow sets
# and nginx's trusted list (ops_nginx_limits.go) — the addresses of every server + the Hub's own.
set -u
db=$(runuser -u postgres -- psql -d heyearth -tAc "SELECT DISTINCT ip_address FROM servers WHERE ip_address IS NOT NULL" 2>/dev/null) || { echo "heyearth-ddos-allow: DB unavailable, lists kept"; exit 0; }
own=$(ip -o addr show | awk '{print $4}' | cut -d/ -f1)
peers=$(ip -o -4 addr show | awk '$2 ~ /^he-r[0-9]+$/ && $5 == "peer" { sub(/\/.*/, "", $6); print $6 }')
if nft list table inet heyearth_ddos >/dev/null 2>&1; then
  v4=""; v6=""
  for ip in $(printf '%s\n' $db $own $peers | sort -u); do
    case "$ip" in
      *[!0-9a-fA-F:.]*) continue ;;
      *:*) v6="$v6${v6:+, }$ip" ;;
      *.*.*.*) v4="$v4${v4:+, }$ip" ;;
    esac
  done
  {
    echo "flush set inet heyearth_ddos allow4"
    [ -n "$v4" ] && echo "add element inet heyearth_ddos allow4 { $v4 }"
    echo "flush set inet heyearth_ddos allow6"
    [ -n "$v6" ] && echo "add element inet heyearth_ddos allow6 { $v6 }"
  } | nft -f -
fi
if [ -d /etc/nginx ] && [ -f /etc/nginx/conf.d/00-heyearth-limits.conf -o -f ` + nginxTrustedIPsPath + ` ]; then
` + nginxTrustedRefresh + `
fi
`

const (
	ddosUnit = `[Unit]
Description=HiEarth per-source flood limits (nft table inet heyearth_ddos)
After=network-pre.target postgresql.service
Wants=network-pre.target

[Service]
Type=oneshot
RemainAfterExit=yes
ExecStart=/usr/sbin/nft -f /etc/heyearth/ddos.nft
ExecStartPost=-/opt/heyearth/bin/heyearth-ddos-allow.sh
ExecStop=-/usr/sbin/nft delete table inet heyearth_ddos

[Install]
WantedBy=multi-user.target
`
	ddosAllowService = `[Unit]
Description=HiEarth flood table: refresh the allow sets from the servers table

[Service]
Type=oneshot
ExecStart=/opt/heyearth/bin/heyearth-ddos-allow.sh
`
	ddosAllowTimer = `[Unit]
Description=HiEarth flood table: refresh the allow sets every 5 minutes

[Timer]
OnBootSec=2min
OnUnitActiveSec=5min

[Install]
WantedBy=timers.target
`
)

// renderDDoSApply installs the units and the table. The previous table file is kept for the
// rollback; nft -c checks the new file before anything is loaded.
func renderDDoSApply(nftText string) string {
	return `set -eu
mkdir -p /opt/heyearth/state/guard /etc/heyearth /opt/heyearth/bin; chmod 700 /opt/heyearth/state/guard
if [ -f /etc/heyearth/ddos.nft ]; then cp -p /etc/heyearth/ddos.nft /opt/heyearth/state/guard/ddos.prev; else rm -f /opt/heyearth/state/guard/ddos.prev; fi
` + opsPutFileFunc + putFileCalls([]opsFile{
		{"/etc/heyearth/ddos.nft.new", "600", nftText},
		{"/opt/heyearth/bin/heyearth-ddos-allow.sh", "755", ddosAllowScript},
		{"/etc/systemd/system/heyearth-ddos.service", "644", ddosUnit},
		{"/etc/systemd/system/heyearth-ddos-allow.service", "644", ddosAllowService},
		{"/etc/systemd/system/heyearth-ddos-allow.timer", "644", ddosAllowTimer},
	}) + `nft -c -f /etc/heyearth/ddos.nft.new || { rm -f /etc/heyearth/ddos.nft.new; echo "[ops] FAILED: nft -c rejected the flood table"; exit 1; }
mv -f /etc/heyearth/ddos.nft.new /etc/heyearth/ddos.nft
systemctl daemon-reload
nft -f /etc/heyearth/ddos.nft
/opt/heyearth/bin/heyearth-ddos-allow.sh
systemctl enable heyearth-ddos.service >/dev/null 2>&1
systemctl is-active --quiet heyearth-ddos.service || systemctl start heyearth-ddos.service
systemctl enable --now heyearth-ddos-allow.timer >/dev/null 2>&1
echo "[ops] flood table: mode $(head -1 /etc/heyearth/ddos.nft | sed -n 's/.*, mode \([a-z]*\)\..*/\1/p'); allow4 $(nft list set inet heyearth_ddos allow4 | tr ',' '\n' | grep -cE '[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+') addresses"
`
}

const ddosRollback = `if [ -f /opt/heyearth/state/guard/ddos.prev ]; then
  cp -p /opt/heyearth/state/guard/ddos.prev /etc/heyearth/ddos.nft && nft -f /etc/heyearth/ddos.nft && /opt/heyearth/bin/heyearth-ddos-allow.sh
else
  nft delete table inet heyearth_ddos 2>/dev/null; systemctl disable heyearth-ddos.service 2>/dev/null; rm -f /etc/heyearth/ddos.nft
fi
`

// ───────────────────────── guarded apply ─────────────────────────

var guardNameRe = regexp.MustCompile(`^[a-z0-9-]+$`)

// guardedApply arms the rollback (a transient timer), runs apply, then proves with a fresh SSH
// login that the Hub is still reachable and disarms the timer. If apply fails, the rollback runs
// at once. If the fresh login fails, the timer stays armed and undoes the change by itself.
func guardedApply(client *ssh.Client, cfg *deployConfig, name, apply, rollback string) error {
	if !guardNameRe.MatchString(name) {
		return fmt.Errorf("bad guard name %q", name)
	}
	unit := "heyearth-guard-" + name
	script := "/opt/heyearth/state/guard/" + name + "-rollback.sh"
	arm := `set -eu
mkdir -p /opt/heyearth/state/guard; chmod 700 /opt/heyearth/state/guard
` + opsPutFileFunc + putFileCalls([]opsFile{{script, "700", "#!/bin/sh\n" + rollback}}) + fmt.Sprintf(`systemctl stop %[1]s.timer %[1]s.service >/dev/null 2>&1 || true
systemctl reset-failed %[1]s.timer %[1]s.service >/dev/null 2>&1 || true
systemd-run --quiet --unit=%[1]s --on-active=%[2]d /bin/sh %[3]s
echo "[guard] armed: %[1]s undoes the change in %[2]d s unless confirmed"
`, unit, guardSeconds, script)
	if err := runOpsScript(client, "guard "+name+": arm", arm); err != nil {
		return err
	}
	if err := runOpsScript(client, "guard "+name+": apply", apply); err != nil {
		_, _ = runSSHBash(client, "guard "+name+": immediate rollback", fmt.Sprintf("systemctl stop %[1]s.timer >/dev/null 2>&1; /bin/sh %[2]s", unit, script), false)
		return fmt.Errorf("%s: apply failed, rolled back: %w", name, err)
	}
	// Prove the Hub still takes NEW logins (the change must not lock the operator out).
	var fresh *ssh.Client
	var err error
	for i := 0; i < 4; i++ {
		if fresh, err = sshDialHub(cfg); err == nil {
			break
		}
		time.Sleep(time.Duration(5+5*i) * time.Second)
	}
	if err != nil {
		return fmt.Errorf("%s: a fresh SSH login failed after the change (%v) — the timer %s undoes it within %d s", name, err, unit, guardSeconds)
	}
	defer fresh.Close()
	if _, err := runSSHBash(fresh, "guard "+name+": confirm", fmt.Sprintf("systemctl stop %[1]s.timer >/dev/null 2>&1; systemctl reset-failed %[1]s.timer %[1]s.service >/dev/null 2>&1 || true; echo '[guard] confirmed by a fresh login: %[1]s disarmed'", unit), true); err != nil {
		return fmt.Errorf("%s: confirm failed: %w (the timer may still roll the change back)", name, err)
	}
	return nil
}

// ───────────────────────── entry point ─────────────────────────

// hardeningState reads what the Hub has now, so a re-run without flags keeps it: the flood
// table's mode (header of /etc/heyearth/ddos.nft) and whether the sshd drop-in turned password
// logins off.
func hardeningState(client *ssh.Client) (ddosMode string, passwordOff bool) {
	out, _ := runSSHBash(client, "hardening state", `head -1 /etc/heyearth/ddos.nft 2>/dev/null | sed -n 's/.*, mode \([a-z]*\)\..*/DDOS=\1/p'; grep -qx 'PasswordAuthentication no' `+sshdDropInPath+` 2>/dev/null && echo PWOFF=1 || true`, false)
	for _, l := range strings.Split(out, "\n") {
		l = strings.TrimSpace(l)
		if v, ok := strings.CutPrefix(l, "DDOS="); ok && ddosModeRe.MatchString(v) {
			ddosMode = v
		}
		if l == "PWOFF=1" {
			passwordOff = true
		}
	}
	return ddosMode, passwordOff
}

// setupHardening — deploy --setup-hardening [--ddos-mode observe|enforce] [--ssh-password on|off].
// Without the flags the Hub's current choices are kept (first run: observe, password on).
// Independent steps; a failing one is reported, the others still run; returns the failed ones.
func setupHardening(client *ssh.Client, cfg *deployConfig, ddosMode, sshPassword string) []string {
	curMode, curPwOff := hardeningState(client)
	if ddosMode == "" {
		ddosMode = curMode
	}
	if ddosMode == "" {
		ddosMode = "observe"
	}
	if !ddosModeRe.MatchString(ddosMode) {
		log.Fatalf("--ddos-mode must be observe or enforce, not %q", ddosMode)
	}
	sshPasswordOff := curPwOff
	switch sshPassword {
	case "":
	case "off":
		if strings.TrimSpace(cfg.deployKeyFile) == "" {
			log.Fatal("--ssh-password off needs DEPLOY_SSH_KEY_FILE: the deploy tool would lock itself out")
		}
		sshPasswordOff = true
	case "on":
		sshPasswordOff = false
	default:
		log.Fatalf("--ssh-password must be on or off, not %q", sshPassword)
	}
	var failed []string
	step := func(name string, run func() error) {
		fmt.Printf("[ops] -- %s\n", name)
		if err := run(); err != nil {
			fmt.Printf("[ops] FAILED %s: %v\n", name, err)
			failed = append(failed, name)
		}
	}
	step("sysctl (SYN flood, conntrack)", func() error { return runOpsScript(client, "sysctl (setup-hardening)", renderSysctlScript()) })
	step("security auto-updates", func() error { return runOpsScript(client, "auto-updates (setup-hardening)", renderUnattendedScript()) })
	step("sshd limits", func() error {
		return guardedApply(client, cfg, "sshd", renderSSHDApply(renderSSHDConf(sshPasswordOff)), sshdRollback)
	})
	step("flood table ("+ddosMode+")", func() error {
		return guardedApply(client, cfg, "ddos", renderDDoSApply(renderDDoSNft(ddosMode, defaultDDoSLimits)), ddosRollback)
	})
	step("nginx limits + scanner jail", func() error {
		return guardedApply(client, cfg, "nginx-limits", renderNginxLimitsApply(cfg.siteDomain), nginxLimitsRollback)
	})
	step("security headers", func() error {
		return guardedApply(client, cfg, "headers", renderHeadersApply(cfg.siteDomain), headersRollback)
	})
	return failed
}
