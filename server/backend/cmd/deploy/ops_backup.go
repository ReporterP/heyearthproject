package main

// Daily Hub backup + off-box copy to an exit node: `deploy --setup-ops [--backup-target <exit-IP>]`.
// Runbook: docs/runbooks/restore-hub-backup.md.
//
// The off-box copy uses a dedicated ed25519 key that is allowed on the exit ONLY as a forced
// command (heyearth-backup-receive): a stolen key can store backup-*.tar.gz files, nothing else.
// No password is stored on the Hub (servers.ssh_password is AES-encrypted in the DB, a bash
// timer could not use it, and a plaintext copy would be the same root secret as the exit itself).

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net"
	"regexp"
	"strconv"
	"strings"

	"golang.org/x/crypto/ssh"
)

const (
	backupSSHKey     = "/root/.ssh/heyearth_backup_ed25519"
	backupKeyComment = "heyearth-backup@hub"
)

// backupScript runs on the Hub from heyearth-backup.service. Two env hooks exist for the unit
// tests only and are unset in production: HEYEARTH_BACKUP_ENV (path of the env file) and
// FILES_ROOT (root under which the config paths are looked up, "/" by default).
//
// Order matters for the monitor (cmd/api/monitor.go counts regular non-empty
// backup-*.tar.gz files by mtime): the archive appears under its final name only after it has
// been written completely (.part + mv) and read back with tar -t.
//
// users_count is taken right BEFORE pg_dump; registrations between the two make the dump a
// few users larger, which heyearth-backup-verify.sh tolerates (see there).
const backupScript = `#!/usr/bin/env bash
# Installed by deploy --setup-ops (cmd/deploy/ops_backup.go). DO NOT EDIT. Restore: docs/runbooks/restore-hub-backup.md
set -euo pipefail
umask 077
source "${HEYEARTH_BACKUP_ENV:-/etc/heyearth/backup.env}"
FILES_ROOT="${FILES_ROOT:-/}"; FILES_ROOT="${FILES_ROOT%/}"
TS=$(date +%Y%m%d-%H%M%S); ARCHIVE="$BACKUP_DIR/backup-$TS.tar.gz"
mkdir -p "$BACKUP_DIR"
find "$BACKUP_DIR" -maxdepth 1 -name '.work-*' -mmin +120 -exec rm -rf {} + 2>/dev/null || true
WORK=$(mktemp -d "$BACKUP_DIR/.work-XXXXXX")
trap 'rm -rf "$WORK" "$ARCHIVE.part"' EXIT
fail() { echo "$(date -Is) $*" > "$BACKUP_DIR/last-error"; echo "ERROR: $*" >&2; exit 1; }
USERS=$(runuser -u postgres -- psql -d heyearth -tAc "SELECT count(*) FROM users") || fail "users count failed"
case "$USERS" in ''|*[!0-9]*) fail "users count is not a number: '$USERS'" ;; esac
runuser -u postgres -- pg_dump -Fc -d heyearth > "$WORK/db.dump" || fail "pg_dump failed"
[ -s "$WORK/db.dump" ] || fail "pg_dump produced an empty dump"
printf 'created_at=%s\nusers_count=%s\nhost=%s\n' "$(date -Is)" "$USERS" "$(hostname)" > "$WORK/meta.txt"
PATHS=()
for p in opt/heyearth/state opt/heyearth/mainserver/api.env opt/heyearth/mainserver/.env opt/heyearth/mainserver/certs \
         opt/heyearth/hysteria etc/letsencrypt etc/nginx/nginx.conf etc/nginx/conf.d etc/nginx/stream.d etc/nginx/stream-enabled; do
  if [ -e "$FILES_ROOT/$p" ]; then PATHS+=("$p"); fi
done
[ "${#PATHS[@]}" -gt 0 ] || fail "none of the config paths exist"
rc=0; tar -C "${FILES_ROOT:-/}" -czf "$WORK/files.tar.gz" "${PATHS[@]}" || rc=$?
[ "$rc" -le 1 ] || fail "tar of config files failed (exit $rc)"
tar -C "$WORK" -czf "$ARCHIVE.part" db.dump meta.txt files.tar.gz || fail "final tar failed"
tar -tzf "$ARCHIVE.part" > /dev/null || fail "final archive is unreadable"
mv -f "$ARCHIVE.part" "$ARCHIVE"
ls -1t "$BACKUP_DIR"/backup-*.tar.gz | tail -n +$((KEEP + 1)) | xargs -r rm -f --
date -Is > "$BACKUP_DIR/last-success"
echo "local: $ARCHIVE ($(du -h "$ARCHIVE" | cut -f1), users=$USERS)"
if [ -z "${TARGET_HOST:-}" ]; then echo "WARN: no TARGET_HOST, offsite skipped" >&2; rm -f "$BACKUP_DIR/last-error"; exit 0; fi
SUM=$(sha256sum "$ARCHIVE" | cut -d' ' -f1)
for i in 1 2 3; do
  RESP=""
  if RESP=$(ssh -i "$SSH_KEY" -p "$TARGET_PORT" -o BatchMode=yes -o IdentitiesOnly=yes -o StrictHostKeyChecking=yes \
        -o ConnectTimeout=20 -o ServerAliveInterval=15 -o ServerAliveCountMax=4 \
        "root@$TARGET_HOST" "backup-$TS.tar.gz" < "$ARCHIVE" 2>"$WORK/ssh.err") && [ "$RESP" = "OK $SUM" ]; then
    date -Is > "$BACKUP_DIR/last-offsite"; rm -f "$BACKUP_DIR/last-error"
    echo "offsite: $TARGET_HOST OK (sha256 $SUM)"; exit 0
  fi
  echo "offsite attempt $i failed: ${RESP:-} $(cat "$WORK/ssh.err")" >&2
  if [ "$i" -lt 3 ]; then sleep 30; fi
done
fail "offsite copy to $TARGET_HOST failed after 3 attempts (local archive kept)"
`

// backupVerifyScript restores an archive into the scratch DB heyearth_restore_test and compares
// the users count with meta.txt. Usage: heyearth-backup-verify.sh [archive] (default: newest).
//
// meta.txt was written a moment before pg_dump, so on a live system the restored count may be
// a little higher (new registrations) or lower (deleted accounts). A difference of at most
// max(3, 1%) is reported as OK with the drift; anything bigger is a MISMATCH.
const backupVerifyScript = `#!/usr/bin/env bash
# Installed by deploy --setup-ops (cmd/deploy/ops_backup.go). DO NOT EDIT. Restore: docs/runbooks/restore-hub-backup.md
set -euo pipefail
umask 077
ARCHIVE="${1:-$(ls -1t /opt/heyearth/backups/backup-*.tar.gz | sed -n 1p)}"
WORK=$(mktemp -d)
cleanup() { runuser -u postgres -- dropdb --if-exists heyearth_restore_test || true; rm -rf "$WORK"; }
trap cleanup EXIT
tar -C "$WORK" -xzf "$ARCHIVE" db.dump meta.txt
EXPECTED=$(sed -n 's/^users_count=//p' "$WORK/meta.txt")
runuser -u postgres -- dropdb --if-exists heyearth_restore_test
runuser -u postgres -- createdb heyearth_restore_test
runuser -u postgres -- pg_restore --no-owner --no-acl --exit-on-error -d heyearth_restore_test < "$WORK/db.dump"
GOT=$(runuser -u postgres -- psql -d heyearth_restore_test -tAc "SELECT count(*) FROM users")
MIG=$(runuser -u postgres -- psql -d heyearth_restore_test -tAc "SELECT version || ' dirty=' || dirty FROM schema_migrations")
echo "archive=$ARCHIVE users_expected=$EXPECTED users_restored=$GOT schema=$MIG"
case "$EXPECTED$GOT" in ''|*[!0-9]*) echo "RESTORE-TEST: MISMATCH (users count is not a number)"; exit 1 ;; esac
DRIFT=$((GOT - EXPECTED)); if [ "$DRIFT" -lt 0 ]; then DRIFT=$((-DRIFT)); fi
TOL=$((EXPECTED / 100)); if [ "$TOL" -lt 3 ]; then TOL=3; fi
if [ "$DRIFT" -eq 0 ]; then echo "RESTORE-TEST: OK"
elif [ "$DRIFT" -le "$TOL" ]; then echo "RESTORE-TEST: OK (users changed by $DRIFT while the backup ran)"
else echo "RESTORE-TEST: MISMATCH"; exit 1; fi
`

const backupServiceUnit = "[Unit]\nDescription=HiEarth Hub backup (pg_dump + state + certs, offsite copy)\nWants=network-online.target\nAfter=network-online.target postgresql.service\n\n[Service]\nType=oneshot\nExecStart=/opt/heyearth/bin/heyearth-backup.sh\nNice=10\nIOSchedulingClass=idle\nTimeoutStartSec=30min\n"

// backupOnCalendar is 04:00 Moscow time (no DST there); the install script validates it with
// systemd-analyze before writing anything.
const backupOnCalendar = "*-*-* 04:00:00 Europe/Moscow"

const backupTimerUnit = "[Unit]\nDescription=Daily HiEarth Hub backup at 04:00 MSK\n\n[Timer]\nOnCalendar=" + backupOnCalendar + "\nPersistent=true\n\n[Install]\nWantedBy=timers.target\n"

func renderBackupEnv(target string, port int) string {
	return fmt.Sprintf("BACKUP_DIR=/opt/heyearth/backups\nKEEP=14\nTARGET_HOST=%s\nTARGET_PORT=%d\nSSH_KEY=%s\n", target, port, backupSSHKey)
}

// renderBackupInstallScript installs the backup scripts, units and (once) the SSH key, then
// enables the timer. Tools the backup needs are checked BEFORE anything is written: a timer
// that cannot work must not be installed (monitoring would only notice 26 hours later).
func renderBackupInstallScript(target string, port int) string {
	files := []opsFile{
		{"/etc/heyearth/backup.env", "600", renderBackupEnv(target, port)},
		{"/opt/heyearth/bin/heyearth-backup.sh", "700", backupScript},
		{"/opt/heyearth/bin/heyearth-backup-verify.sh", "700", backupVerifyScript},
		{"/etc/systemd/system/heyearth-backup.service", "644", backupServiceUnit},
		{"/etc/systemd/system/heyearth-backup.timer", "644", backupTimerUnit},
	}
	return `set -eu
for t in pg_dump pg_restore psql runuser tar gzip sha256sum ssh ssh-keygen base64 systemctl systemd-analyze; do
  command -v "$t" >/dev/null 2>&1 || { echo "[ops] ABORT: $t is not installed — backup timer not installed"; exit 1; }
done
systemd-analyze calendar "` + backupOnCalendar + `" >/dev/null 2>&1 || { echo "[ops] ABORT: this systemd cannot parse OnCalendar with a time zone — backup timer not installed"; exit 1; }
` + opsPutFileFunc + `
mkdir -p /etc/heyearth /opt/heyearth/bin /opt/heyearth/backups /root/.ssh
chmod 700 /opt/heyearth/backups /root/.ssh
` + putFileCalls(files) + `K=` + backupSSHKey + `
if [ ! -f "$K" ]; then ssh-keygen -q -t ed25519 -N '' -C ` + backupKeyComment + ` -f "$K"; echo "[ops] generated $K"; fi
if [ ! -s "$K.pub" ]; then echo "$(ssh-keygen -y -f "$K") ` + backupKeyComment + `" > "$K.pub"; fi
systemctl daemon-reload
systemctl enable --now heyearth-backup.timer
systemctl list-timers heyearth-backup.timer --no-pager
`
}

func installBackupTimer(client *ssh.Client, target string, port int) error {
	return runOpsScript(client, "backup timer (setup-ops)", renderBackupInstallScript(target, port))
}

// backupReceiverScript is the forced command of the Hub backup key, installed on the exit.
// sshd hands it the client's command line in SSH_ORIGINAL_COMMAND; the only accepted values
// are "ping" and a backup-YYYYmmdd-HHMMSS.tar.gz name. The stream is stored as <name>.part,
// checked with gzip -t (a connection that dropped midway must not leave a truncated "newest"
// archive that would push a good one out of the KEEP window) and only then renamed.
const backupReceiverScript = `#!/usr/bin/env bash
# Installed by Hub deploy --setup-ops (cmd/deploy/ops_backup.go). DO NOT EDIT.
# Forced command of the Hub backup key. Stores stdin as /var/backups/heyearth-hub/<name>, keeps the newest 7.
# receiver-version: __VERSION__
set -euo pipefail
umask 077
DIR=/var/backups/heyearth-hub; KEEP=7; NAME="${SSH_ORIGINAL_COMMAND:-}"
if [ "$NAME" = "ping" ]; then echo "PONG __VERSION__"; exit 0; fi
if ! [[ "$NAME" =~ ^backup-[0-9]{8}-[0-9]{6}\.tar\.gz$ ]]; then echo "rejected: $NAME" >&2; exit 2; fi
mkdir -p "$DIR"
PART="$DIR/$NAME.part"
trap 'rm -f "$PART"' EXIT
find "$DIR" -maxdepth 1 -name '*.part' -mmin +120 -delete 2>/dev/null || true
cat > "$PART"
gzip -t "$PART" 2>/dev/null || { echo "rejected: truncated or corrupt gzip stream" >&2; exit 3; }
mv -f "$PART" "$DIR/$NAME"
ls -1t "$DIR"/backup-*.tar.gz | tail -n +$((KEEP + 1)) | xargs -r rm -f --
echo "OK $(sha256sum "$DIR/$NAME" | cut -d' ' -f1)"
`

// backupReceiverVersion identifies the receiver text. The receiver answers "ping" with
// "PONG <version>", so a re-run of --setup-ops can tell "installed and current" (no exit
// password needed) from "missing or outdated" (reinstall through the exit root password).
func backupReceiverVersion() string {
	sum := sha256.Sum256([]byte(backupReceiverScript))
	return hex.EncodeToString(sum[:])[:12]
}

func renderBackupReceiver() string {
	return strings.ReplaceAll(backupReceiverScript, "__VERSION__", backupReceiverVersion())
}

var (
	backupPubKeyRe  = regexp.MustCompile(`^ssh-ed25519 [A-Za-z0-9+/]+={0,2} ` + regexp.QuoteMeta(backupKeyComment) + `$`)
	hostKeyFieldsRe = regexp.MustCompile(`^ssh-ed25519 [A-Za-z0-9+/]+={0,2}$`)
)

// backupAuthorizedKeysLine is the authorized_keys entry of the Hub key on the exit: forced
// command, no tty/forwarding. pub is validated first because the line is pasted into a script
// that runs as root on the exit.
func backupAuthorizedKeysLine(pub string) (string, error) {
	pub = strings.TrimSpace(pub)
	if !backupPubKeyRe.MatchString(pub) {
		return "", fmt.Errorf("the Hub backup public key has an unexpected format")
	}
	return `command="/usr/local/sbin/heyearth-backup-receive",restrict,no-pty,no-port-forwarding,no-agent-forwarding,no-X11-forwarding ` + pub, nil
}

// renderReceiverInstallScript runs on the exit: the forced-command script and the Hub key's
// authorized_keys entry (any older entry of the same key is replaced, other keys are kept).
func renderReceiverInstallScript(authLine string) string {
	return `set -eu
` + opsPutFileFunc + `
mkdir -p /var/backups/heyearth-hub /root/.ssh
chmod 700 /var/backups/heyearth-hub /root/.ssh
` + putFileCalls([]opsFile{{"/usr/local/sbin/heyearth-backup-receive", "755", renderBackupReceiver()}}) + `touch /root/.ssh/authorized_keys
{ grep -v '` + backupKeyComment + `' /root/.ssh/authorized_keys || true; printf '%s\n' '` + authLine + `'; } > /root/.ssh/authorized_keys.new
chmod 600 /root/.ssh/authorized_keys.new
mv -f /root/.ssh/authorized_keys.new /root/.ssh/authorized_keys
echo "[ops] exit: receiver and authorized_keys entry installed"
`
}

func knownHostsName(target string, port int) string {
	if port == 22 {
		return target
	}
	return fmt.Sprintf("[%s]:%d", target, port)
}

// backupProbeCommand is run on the Hub: the forced command answers "PONG <version>".
func backupProbeCommand(target string, port int) string {
	return fmt.Sprintf("ssh -i %s -p %d -o BatchMode=yes -o IdentitiesOnly=yes -o StrictHostKeyChecking=yes -o ConnectTimeout=20 root@%s ping 2>&1 || true",
		backupSSHKey, port, target)
}

// renderKnownHostsScript pins the exit's ed25519 host key on the Hub (replacing an older entry
// for the same host) and probes the forced command.
func renderKnownHostsScript(target string, port int, hostKeyType, hostKeyB64 string) string {
	known := knownHostsName(target, port)
	return fmt.Sprintf(`set -eu
mkdir -p /root/.ssh && chmod 700 /root/.ssh && touch /root/.ssh/known_hosts
ssh-keygen -R '%[1]s' -f /root/.ssh/known_hosts >/dev/null 2>&1 || true
rm -f /root/.ssh/known_hosts.old
echo '%[1]s %[2]s %[3]s' >> /root/.ssh/known_hosts
`, known, hostKeyType, hostKeyB64) + backupProbeCommand(target, port) + "\n"
}

// installOffsiteBackupKey makes the Hub backup key usable on the exit. If the key already
// answers the forced-command probe with the current receiver version, nothing is touched and
// no exit password is needed (so a re-run survives a password rotation). Otherwise the receiver
// and the authorized_keys entry are installed over the exit's root password
// (--exit-node-password / EXIT_NODE_SSH_PASSWORD / the Hub deploy password, like setup-exit-node)
// and the exit's ed25519 host key is pinned in the Hub's known_hosts.
func installOffsiteBackupKey(hub *ssh.Client, cfg *deployConfig, target, password string, port int) error {
	want := "PONG " + backupReceiverVersion()
	fmt.Printf("[ops] offsite: проверяю, работает ли ключ бэкапа на %s:%d (при первом запуске проба ожидаемо упадёт)\n", target, port)
	out, _ := runSSHBash(hub, "offsite probe", backupProbeCommand(target, port), false)
	if strings.Contains(out, want) {
		fmt.Printf("[ops] offsite backup key already installed on %s, receiver is current\n", target)
		fmt.Println("[ops] offsite backup key installed, probe: PONG")
		return nil
	}

	pubOut, err := runSSHBash(hub, "read backup public key", "cat "+backupSSHKey+".pub", true)
	if err != nil {
		return fmt.Errorf("backup public key missing on the Hub (did the backup timer step fail?): %w", err)
	}
	authLine, err := backupAuthorizedKeysLine(pubOut)
	if err != nil {
		return err
	}
	if password == "" {
		password = cfg.env.get("EXIT_NODE_SSH_PASSWORD", cfg.deployPassword)
	}
	exit, err := sshDial(net.JoinHostPort(target, strconv.Itoa(port)), "root", password, cfg.env.get("EXIT_NODE_HOST_KEY_FINGERPRINT", ""))
	if err != nil {
		return fmt.Errorf("SSH to the backup target %s:%d (set EXIT_NODE_SSH_PASSWORD or --exit-node-password): %w", target, port, err)
	}
	defer exit.Close()
	if _, err := runSSHBash(exit, "install receiver (exit)", renderReceiverInstallScript(authLine), true); err != nil {
		return fmt.Errorf("install the receiver on %s: %w", target, err)
	}
	hostKey, err := runSSHBash(exit, "read exit host key", "cat /etc/ssh/ssh_host_ed25519_key.pub", true)
	f := strings.Fields(hostKey)
	if err != nil || len(f) < 2 || !hostKeyFieldsRe.MatchString(f[0]+" "+f[1]) {
		return fmt.Errorf("cannot read the ed25519 host key of %s: %v", target, err)
	}
	probe, err := runSSHBash(hub, "pin exit host key + probe", renderKnownHostsScript(target, port, f[0], f[1]), true)
	if err != nil || !strings.Contains(probe, want) {
		return fmt.Errorf("offsite SSH probe failed (want %q): %v", want, err)
	}
	fmt.Println("[ops] offsite backup key installed, probe: PONG")
	return nil
}
