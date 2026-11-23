package main

// Daily Hub backup + encrypted off-box copy to an exit node:
// `deploy --setup-ops [--backup-target <exit-IP> --backup-age-recipient age1…]`.
// Runbook: docs/runbooks/restore-hub-backup.md.
//
// The off-box copy is encrypted on the Hub (age, to a public key whose private half the owner
// keeps offline) and uploaded with a dedicated ed25519 key that is allowed on the exit ONLY as a
// forced command (heyearth-backup-receive): a stolen Hub key can store capped, name-checked
// backup-*.tar.gz.age files, nothing else, and the exit never sees plaintext. No password is
// stored on the Hub (servers.ssh_password is AES-encrypted in the DB, a bash timer could not use
// it, and a plaintext copy would be the same root secret as the exit itself).

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"regexp"
	"strconv"
	"strings"

	"golang.org/x/crypto/ssh"
)

const (
	backupSSHKey           = "/root/.ssh/heyearth_backup_ed25519"
	backupKeyComment       = "heyearth-backup@hub"
	backupAgeRecipientFile = "/etc/heyearth/backup-age.pub"
)

// backupScript runs on the Hub from heyearth-backup.service. Two env hooks exist for the unit
// tests only and are unset in production: HEYEARTH_BACKUP_ENV (path of the env file) and
// FILES_ROOT (root under which the config paths are looked up, "/" by default).
//
// Order matters for the monitor (cmd/api/monitor.go counts regular non-empty
// backup-*.tar.gz files by mtime): the archive appears under its final name only after it has
// been written completely (.part + mv) and read back with tar -t. Local archives stay plain;
// only the copy that leaves the Hub is encrypted (backup-<ts>.tar.gz.age) — the checksum, the
// size and the receiver's name check all apply to the ciphertext.
//
// The free-space check runs before anything is written: a full disk fails the run with a clear
// last-error and no new archive, which the monitor turns into a backup-stale alert. Disk use is
// built to stay within that check (2x the last archive + 1 GiB): while the archive is built the
// dump, the config tarball and the .part coexist (about twice the archive); the intermediates
// are deleted the moment the final tar exists, so the encrypted stream is created next to the
// archive alone (again about twice the archive).
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
fail() { echo "$(date -Is) $*" > "$BACKUP_DIR/last-error"; echo "ERROR: $*" >&2; exit 1; }
LAST=$(ls -1t "$BACKUP_DIR"/backup-*.tar.gz 2>/dev/null | sed -n 1p || true)
LAST_SIZE=0; if [ -n "$LAST" ]; then LAST_SIZE=$(stat -c %s "$LAST"); fi
NEED=$((2 * LAST_SIZE + 1073741824))
AVAIL=$(df --output=avail -B1 "$BACKUP_DIR" | tail -n 1 | tr -d ' ' || true)
case "$AVAIL" in ''|*[!0-9]*) fail "cannot read the free space of $BACKUP_DIR" ;; esac
[ "$AVAIL" -ge "$NEED" ] || fail "not enough free space in $BACKUP_DIR: $AVAIL bytes free, $NEED needed (2x the last archive + 1 GiB)"
find "$BACKUP_DIR" -maxdepth 1 -name '.work-*' -mmin +120 -exec rm -rf {} + 2>/dev/null || true
WORK=$(mktemp -d "$BACKUP_DIR/.work-XXXXXX")
trap 'rm -rf "$WORK" "$ARCHIVE.part"' EXIT
USERS=$(runuser -u postgres -- psql -d heyearth -tAc "SELECT count(*) FROM users") || fail "users count failed"
case "$USERS" in ''|*[!0-9]*) fail "users count is not a number: '$USERS'" ;; esac
runuser -u postgres -- pg_dump -Fc -d heyearth > "$WORK/db.dump" || fail "pg_dump failed"
[ -s "$WORK/db.dump" ] || fail "pg_dump produced an empty dump"
printf 'created_at=%s\nusers_count=%s\nhost=%s\n' "$(date -Is)" "$USERS" "$(hostname)" > "$WORK/meta.txt"
PATHS=()
for p in opt/heyearth/state opt/heyearth/mainserver/api.env opt/heyearth/mainserver/.env opt/heyearth/mainserver/certs \
         opt/heyearth/hysteria etc/letsencrypt etc/nginx/nginx.conf etc/nginx/conf.d etc/nginx/stream.d etc/nginx/stream-enabled \
         etc/systemd/system/hysteria2-server.service.d; do
  if [ -e "$FILES_ROOT/$p" ]; then PATHS+=("$p"); fi
done
# The Hub's tunnel client units (hysteria2-client-<id>.service, -express) are written by exit
# provisioning only; no other deploy step recreates them.
for f in "$FILES_ROOT"/etc/systemd/system/hysteria2-client-*.service; do
  if [ -e "$f" ]; then PATHS+=("${f#"$FILES_ROOT"/}"); fi
done
[ "${#PATHS[@]}" -gt 0 ] || fail "none of the config paths exist"
rc=0; tar -C "${FILES_ROOT:-/}" -czf "$WORK/files.tar.gz" "${PATHS[@]}" || rc=$?
[ "$rc" -le 1 ] || fail "tar of config files failed (exit $rc)"
tar -C "$WORK" -czf "$ARCHIVE.part" db.dump meta.txt files.tar.gz || fail "final tar failed"
rm -f "$WORK/db.dump" "$WORK/meta.txt" "$WORK/files.tar.gz"
tar -tzf "$ARCHIVE.part" > /dev/null || fail "final archive is unreadable"
mv -f "$ARCHIVE.part" "$ARCHIVE"
ls -1t "$BACKUP_DIR"/backup-*.tar.gz | tail -n +$((KEEP + 1)) | xargs -r rm -f --
date -Is > "$BACKUP_DIR/last-success"
echo "local: $ARCHIVE ($(du -h "$ARCHIVE" | cut -f1), users=$USERS)"
if [ -z "${TARGET_HOST:-}" ]; then echo "WARN: no TARGET_HOST, offsite skipped" >&2; rm -f "$BACKUP_DIR/last-error"; exit 0; fi
[ -s "${AGE_RECIPIENT_FILE:-}" ] || fail "offsite copy needs an age recipient (AGE_RECIPIENT_FILE) — a plain archive is never sent off the Hub (local archive kept)"
command -v age >/dev/null 2>&1 || fail "age is not installed (local archive kept)"
OFFNAME="backup-$TS.tar.gz.age"
age -R "$AGE_RECIPIENT_FILE" -o "$WORK/$OFFNAME" "$ARCHIVE" || fail "age encryption failed (local archive kept)"
[ -s "$WORK/$OFFNAME" ] || fail "age produced an empty file (local archive kept)"
SUM=$(sha256sum "$WORK/$OFFNAME" | cut -d' ' -f1); SIZE=$(stat -c %s "$WORK/$OFFNAME")
for i in 1 2 3; do
  RESP=""
  if RESP=$(ssh -i "$SSH_KEY" -p "$TARGET_PORT" -o BatchMode=yes -o IdentitiesOnly=yes -o StrictHostKeyChecking=yes \
        -o ConnectTimeout=20 -o ServerAliveInterval=15 -o ServerAliveCountMax=4 \
        "root@$TARGET_HOST" "$OFFNAME $SIZE $SUM" < "$WORK/$OFFNAME" 2>"$WORK/ssh.err") && [ "$RESP" = "OK $SUM" ]; then
    date -Is > "$BACKUP_DIR/last-offsite"; rm -f "$BACKUP_DIR/last-error"
    echo "offsite: $TARGET_HOST OK (encrypted, sha256 $SUM)"; exit 0
  fi
  echo "offsite attempt $i failed: ${RESP:-} $(cat "$WORK/ssh.err")" >&2
  if [ "$i" -lt 3 ]; then sleep 30; fi
done
fail "offsite copy to $TARGET_HOST failed after 3 attempts (local archive kept)"
`

// backupVerifyScript restores a PLAIN local archive into the scratch DB heyearth_restore_test and
// compares the users count with meta.txt. Usage: heyearth-backup-verify.sh [archive] (default:
// newest local one). An off-box .age copy must be decrypted first (see the runbook).
//
// meta.txt was written a moment before pg_dump, so on a live system the restored count may be
// a little higher (new registrations) or lower (deleted accounts). A difference of at most
// max(3, 1%) is reported as OK with the drift; anything bigger is a MISMATCH.
const backupVerifyScript = `#!/usr/bin/env bash
# Installed by deploy --setup-ops (cmd/deploy/ops_backup.go). DO NOT EDIT. Restore: docs/runbooks/restore-hub-backup.md
set -euo pipefail
umask 077
ARCHIVE="${1:-$(ls -1t /opt/heyearth/backups/backup-*.tar.gz | sed -n 1p)}"
case "$ARCHIVE" in *.age) echo "$ARCHIVE is encrypted: decrypt it first (age -d -i backup.key -o backup.tar.gz $ARCHIVE), see docs/runbooks/restore-hub-backup.md" >&2; exit 2 ;; esac
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

const backupServiceUnit = "[Unit]\nDescription=HiEarth Hub backup (pg_dump + state + certs, encrypted offsite copy)\nWants=network-online.target\nAfter=network-online.target postgresql.service\n\n[Service]\nType=oneshot\nExecStart=/opt/heyearth/bin/heyearth-backup.sh\nNice=10\nIOSchedulingClass=idle\nTimeoutStartSec=30min\n"

// backupOnCalendar is 04:00 Moscow time (no DST there); the install script validates it with
// systemd-analyze before writing anything.
const backupOnCalendar = "*-*-* 04:00:00 Europe/Moscow"

const backupTimerUnit = "[Unit]\nDescription=Daily HiEarth Hub backup at 04:00 MSK\n\n[Timer]\nOnCalendar=" + backupOnCalendar + "\nPersistent=true\n\n[Install]\nWantedBy=timers.target\n"

// The restore drill (PRD A9): once a week the newest archive is restored into a scratch
// database. The outcome goes to backupVerifyStatusFile ("<unix time> <systemd service result>",
// "success" when the drill passed) for the API's monitor.
const (
	backupVerifyOnCalendar = "Sun *-*-* 05:00:00 Europe/Moscow"
	backupVerifyStatusFile = "/opt/heyearth/state/backup-verify.status"
)

// In a unit file %% is a literal %; systemd sets SERVICE_RESULT for ExecStopPost.
const backupVerifyServiceUnit = "[Unit]\nDescription=HiEarth Hub backup restore drill (newest archive into a scratch database)\nAfter=postgresql.service\n\n[Service]\nType=oneshot\nExecStart=/opt/heyearth/bin/heyearth-backup-verify.sh\nExecStopPost=/bin/sh -c 'echo \"$(date +%%s) $SERVICE_RESULT\" > " + backupVerifyStatusFile + "'\nNice=10\nIOSchedulingClass=idle\nTimeoutStartSec=30min\n"

const backupVerifyTimerUnit = "[Unit]\nDescription=Weekly HiEarth Hub backup restore drill, Sunday 05:00 MSK\n\n[Timer]\nOnCalendar=" + backupVerifyOnCalendar + "\nPersistent=true\n\n[Install]\nWantedBy=timers.target\n"

// backupVerifyUnitFiles are the drill's units; the script itself is installed with the backup.
var backupVerifyUnitFiles = []opsFile{
	{"/etc/systemd/system/heyearth-backup-verify.service", "644", backupVerifyServiceUnit},
	{"/etc/systemd/system/heyearth-backup-verify.timer", "644", backupVerifyTimerUnit},
}

// renderBackupVerifyTimerScript installs only the drill's timer on a Hub whose backup is set up
// already (deploy --setup-hygiene). It does not run the drill.
func renderBackupVerifyTimerScript() string {
	return `set -eu
[ -x /opt/heyearth/bin/heyearth-backup-verify.sh ] || { echo "[ops] ABORT: /opt/heyearth/bin/heyearth-backup-verify.sh is missing — run deploy --setup-ops first; drill timer not installed"; exit 1; }
systemd-analyze calendar "` + backupVerifyOnCalendar + `" >/dev/null 2>&1 || { echo "[ops] ABORT: this systemd cannot parse OnCalendar with a time zone — drill timer not installed"; exit 1; }
` + opsPutFileFunc + `mkdir -p /opt/heyearth/state
` + putFileCalls(backupVerifyUnitFiles) + `systemctl daemon-reload
systemctl enable --now heyearth-backup-verify.timer
systemctl list-timers heyearth-backup-verify.timer --no-pager
`
}

func renderBackupEnv(bc backupConfig) string {
	return fmt.Sprintf("BACKUP_DIR=/opt/heyearth/backups\nKEEP=14\nTARGET_HOST=%s\nTARGET_PORT=%d\nSSH_KEY=%s\nAGE_RECIPIENT=%s\nAGE_RECIPIENT_FILE=%s\n",
		bc.target, bc.port, backupSSHKey, bc.ageRecipient, backupAgeRecipientFile)
}

// renderBackupInstallScript installs the backup scripts, units and (once) the SSH key, then
// enables the timer. Everything the backup needs is checked or installed BEFORE anything is
// written: a timer that cannot work must not be installed (monitoring would only notice 26
// hours later). age is installed from apt (idempotent) only when an off-box copy is configured.
func renderBackupInstallScript(bc backupConfig) string {
	files := []opsFile{
		{"/etc/heyearth/backup.env", "600", renderBackupEnv(bc)},
		{"/opt/heyearth/bin/heyearth-backup.sh", "700", backupScript},
		{"/opt/heyearth/bin/heyearth-backup-verify.sh", "700", backupVerifyScript},
		{"/etc/systemd/system/heyearth-backup.service", "644", backupServiceUnit},
		{"/etc/systemd/system/heyearth-backup.timer", "644", backupTimerUnit},
	}
	files = append(files, backupVerifyUnitFiles...)
	ageInstall := ""
	if bc.ageRecipient != "" {
		files = append(files, opsFile{backupAgeRecipientFile, "644", bc.ageRecipient + "\n"})
		ageInstall = `if ! command -v age >/dev/null 2>&1; then
  timeout 300 apt-get update -qq || true
  DEBIAN_FRONTEND=noninteractive timeout 600 apt-get install -y -qq age || { echo "[ops] ABORT: cannot install age — backup timer not installed"; exit 1; }
fi
command -v age >/dev/null 2>&1 || { echo "[ops] ABORT: age is still missing — backup timer not installed"; exit 1; }
echo "[ops] age: $(age --version 2>&1 | head -n 1)"
`
	}
	return `set -eu
for t in pg_dump pg_restore psql runuser tar gzip sha256sum ssh ssh-keygen base64 systemctl systemd-analyze; do
  command -v "$t" >/dev/null 2>&1 || { echo "[ops] ABORT: $t is not installed — backup timer not installed"; exit 1; }
done
systemd-analyze calendar "` + backupOnCalendar + `" >/dev/null 2>&1 || { echo "[ops] ABORT: this systemd cannot parse OnCalendar with a time zone — backup timer not installed"; exit 1; }
systemd-analyze calendar "` + backupVerifyOnCalendar + `" >/dev/null 2>&1 || { echo "[ops] ABORT: this systemd cannot parse the drill's OnCalendar — backup timer not installed"; exit 1; }
` + ageInstall + opsPutFileFunc + `
mkdir -p /etc/heyearth /opt/heyearth/bin /opt/heyearth/backups /opt/heyearth/state /root/.ssh
chmod 700 /opt/heyearth/backups /root/.ssh
` + putFileCalls(files) + `K=` + backupSSHKey + `
if [ ! -f "$K" ]; then ssh-keygen -q -t ed25519 -N '' -C ` + backupKeyComment + ` -f "$K"; echo "[ops] generated $K"; fi
if [ ! -s "$K.pub" ]; then echo "$(ssh-keygen -y -f "$K") ` + backupKeyComment + `" > "$K.pub"; fi
systemctl daemon-reload
systemctl enable --now heyearth-backup.timer
systemctl enable --now heyearth-backup-verify.timer
systemctl list-timers heyearth-backup.timer heyearth-backup-verify.timer --no-pager
`
}

func installBackupTimer(client *ssh.Client, bc backupConfig) error {
	return runOpsScript(client, "backup timer (setup-ops)", renderBackupInstallScript(bc))
}

// backupReceiverScript is the forced command of the Hub backup key, installed on the exit.
// sshd hands it the client's command line in SSH_ORIGINAL_COMMAND; the only accepted values are
//
//	ping
//	backup-YYYYmmdd-HHMMSS.tar.gz.age <size> <sha256>
//
// What the receiver guarantees even if the Hub (and with it the key) is compromised:
//   - the name carries a real date within one calendar day of the receiver's own date
//     (yesterday, today, tomorrow — wide enough for clock and time-zone differences between
//     the Hub and the exit). An archive "from" any other day is refused, so a forged date can
//     neither push archives out of the retention window nor park files in it;
//   - a stored archive is never overwritten: a re-upload of an existing name is answered
//     "OK <sha256>" without touching the file when the declared sha256 and size are those of the
//     stored file (the Hub's own retry, idempotent), and refused otherwise (EXISTS, exit 8) —
//     a compromised Hub cannot replace yesterday's or today's good archive with garbage;
//   - at most PER_DATE archives per date. When a date is full the new upload is REFUSED
//     (FULL, exit 7) — never made room for by deleting something that is already stored. A
//     hostile Hub can thus fill the quota of three dates with garbage and block new backups,
//     but it cannot destroy a single stored archive;
//   - files are deleted by age only: names older than KEEP_DAYS days;
//   - one upload at a time (flock -n; a second one is refused with BUSY, exit 6);
//   - the declared size is at most MAX_BYTES and at most that many bytes (plus one, to notice an
//     excess) are read; the filesystem must have room for twice the biggest of (newest stored
//     archive, declared size, MIN_BYTES) before anything is written;
//   - the stream is stored as <name>.part and renamed only when size and sha256 are exactly what
//     was declared (the archive is ciphertext: integrity can be checked, the content cannot).
const backupReceiverScript = `#!/usr/bin/env bash
# Installed by Hub deploy --setup-ops (cmd/deploy/ops_backup.go). DO NOT EDIT.
# Forced command of the Hub backup key. Stores an age-encrypted archive as /var/backups/heyearth-hub/<name>:
# names within a day of today only, at most 2 per date (a full date refuses, never evicts), deleted by age (8 days) only.
# receiver-version: __VERSION__
set -euo pipefail
umask 077
DIR=/var/backups/heyearth-hub; KEEP_DAYS=8; PER_DATE=2; MAX_BYTES=4294967296; MIN_BYTES=104857600;
CMD="${SSH_ORIGINAL_COMMAND:-}"
reject() { echo "rejected: $1" >&2; exit "${2:-2}"; }
if [ "$CMD" = "ping" ]; then echo "PONG __VERSION__"; exit 0; fi
RE='^(backup-([0-9]{4})([0-9]{2})([0-9]{2})-([0-9]{2})([0-9]{2})([0-9]{2})\.tar\.gz\.age) ([0-9]{1,12}) ([0-9a-f]{64})$'
NAMERE='^backup-[0-9]{8}-[0-9]{6}\.tar\.gz\.age$'
[[ "$CMD" =~ $RE ]] || reject "command not recognised"
NAME="${BASH_REMATCH[1]}"; SIZE=$((10#${BASH_REMATCH[8]})); SUM="${BASH_REMATCH[9]}"
date -d "${BASH_REMATCH[2]}-${BASH_REMATCH[3]}-${BASH_REMATCH[4]} ${BASH_REMATCH[5]}:${BASH_REMATCH[6]}:${BASH_REMATCH[7]}" +%s >/dev/null 2>&1 || reject "the name does not carry a valid date"
D="${NAME:7:8}"
if [ "$D" -lt "$(date -d yesterday +%Y%m%d)" ] || [ "$D" -gt "$(date -d tomorrow +%Y%m%d)" ]; then reject "the date in the name is not within a day of today"; fi
CUTOFF=$(date -d "-$KEEP_DAYS days" +%Y%m%d)
if [ "$SIZE" -lt 1 ] || [ "$SIZE" -gt "$MAX_BYTES" ]; then reject "declared size $SIZE is outside 1..$MAX_BYTES" 4; fi
mkdir -p "$DIR"
exec 9>"$DIR/.lock"
flock -n 9 || reject "BUSY: another upload is in progress" 6
if [ -e "$DIR/$NAME" ]; then
  if [ "$(stat -c %s "$DIR/$NAME")" -eq "$SIZE" ] && [ "$(sha256sum "$DIR/$NAME" | cut -d' ' -f1)" = "$SUM" ]; then echo "OK $SUM"; exit 0; fi
  reject "EXISTS: $NAME is already stored with different content — a stored archive is never overwritten" 8
fi
STORED_THAT_DATE=$(ls -1 "$DIR" | grep -E "$NAMERE" | grep -c "^backup-$D-" || true)
if [ "$STORED_THAT_DATE" -ge "$PER_DATE" ]; then reject "FULL: $STORED_THAT_DATE archives are already stored for $D" 7; fi
LASTNAME=$(ls -1 "$DIR" | grep -E "$NAMERE" | sort | tail -n 1 || true)
BASE=$MIN_BYTES
if [ -n "$LASTNAME" ]; then LAST=$(stat -c %s "$DIR/$LASTNAME"); if [ "$LAST" -gt "$BASE" ]; then BASE=$LAST; fi; fi
if [ "$SIZE" -gt "$BASE" ]; then BASE=$SIZE; fi
AVAIL=$(df --output=avail -B1 "$DIR" | tail -n 1 | tr -d ' ' || true)
case "$AVAIL" in ''|*[!0-9]*) reject "cannot read the free space of $DIR" 5 ;; esac
[ "$AVAIL" -ge $((2 * BASE)) ] || reject "not enough free space: $AVAIL bytes free, $((2 * BASE)) needed" 5
PART="$DIR/$NAME.part"
trap 'rm -f "$PART"' EXIT
find "$DIR" -maxdepth 1 -name '*.part' -mmin +120 -delete 2>/dev/null || true
head -c $((SIZE + 1)) > "$PART"
GOT=$(stat -c %s "$PART")
[ "$GOT" -le "$SIZE" ] || reject "more data than the declared $SIZE bytes" 3
[ "$GOT" -eq "$SIZE" ] || reject "size mismatch: declared $SIZE, received $GOT" 3
[ "$(sha256sum "$PART" | cut -d' ' -f1)" = "$SUM" ] || reject "sha256 mismatch" 3
mv -f "$PART" "$DIR/$NAME"
for f in $(ls -1 "$DIR" | grep -E "$NAMERE" | sort); do
  if [ "${f:7:8}" -lt "$CUTOFF" ]; then rm -f -- "$DIR/$f"; fi
done
echo "OK $SUM"
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
	fingerprintRe   = regexp.MustCompile(`^SHA256:[A-Za-z0-9+/]{43}$`)
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

// receiverPaths are the locations the receiver install script writes to on the exit. Production
// uses prodReceiverPaths; the tests pass shell expressions under a temp directory.
type receiverPaths struct{ receiver, dir, sshDir string }

var prodReceiverPaths = receiverPaths{"/usr/local/sbin/heyearth-backup-receive", "/var/backups/heyearth-hub", "/root/.ssh"}

func renderReceiverInstallScript(authLine string) string {
	return renderReceiverInstallScriptAt(prodReceiverPaths, authLine)
}

// renderReceiverInstallScriptAt runs on the exit: the forced-command script, the Hub key's
// authorized_keys entry and the cleanup of legacy plaintext archives. Any older entry of the
// same key is replaced and other keys are kept; if authorized_keys cannot be read (grep exit
// status above 1) the script aborts and leaves the file untouched, instead of writing back a
// shortened file. The receiver needs flock (util-linux); a host without it is refused before
// anything is written.
//
// An earlier version of the receiver accepted plain backup-*.tar.gz uploads: those files (and
// their .part leftovers) are deleted and counted — the exit must only ever hold ciphertext.
func renderReceiverInstallScriptAt(p receiverPaths, authLine string) string {
	return `set -eu
command -v flock >/dev/null 2>&1 || { echo "[ops] ABORT: flock is not installed on the exit — receiver not installed"; exit 1; }
` + opsPutFileFunc + `
mkdir -p ` + p.dir + ` ` + p.sshDir + `
chmod 700 ` + p.dir + ` ` + p.sshDir + `
` + putFileCalls([]opsFile{{p.receiver, "755", renderBackupReceiver()}}) + `AK=` + p.sshDir + `/authorized_keys
touch "$AK"
grep -v '` + backupKeyComment + `' "$AK" > "$AK.new" && rc=0 || rc=$?
if [ "$rc" -gt 1 ]; then rm -f "$AK.new"; echo "[ops] ABORT: cannot read $AK (grep exit $rc) — authorized_keys left untouched"; exit 1; fi
printf '%s\n' '` + authLine + `' >> "$AK.new"
chmod 600 "$AK.new"
mv -f "$AK.new" "$AK"
legacy=$(find ` + p.dir + ` -maxdepth 1 -type f \( -name 'backup-*.tar.gz' -o -name 'backup-*.tar.gz.part' \) | wc -l | tr -d ' ')
find ` + p.dir + ` -maxdepth 1 -type f \( -name 'backup-*.tar.gz' -o -name 'backup-*.tar.gz.part' \) -delete
echo "[ops] exit: removed $legacy legacy plaintext archive(s) from ` + p.dir + `"
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

// exitAccess is what reaching the exit by root password requires.
type exitAccess struct{ password, fingerprint string }

// exitAccessFor is strict on purpose: the exit's root password goes over the wire only to a
// host whose ed25519 host key is pinned, and only a password meant for the exit is used — the
// Hub's deploy password is never tried there.
func exitAccessFor(env envMap, flagPassword string) (exitAccess, error) {
	pw := flagPassword
	if pw == "" {
		pw = env.get("EXIT_NODE_SSH_PASSWORD", "")
	}
	if pw == "" {
		return exitAccess{}, errors.New("reaching the exit by password needs the exit's own root password: " +
			"set EXIT_NODE_SSH_PASSWORD in server/.env or pass --exit-node-password (the Hub deploy password is never tried on the exit)")
	}
	fp := strings.TrimSpace(env.get("EXIT_NODE_HOST_KEY_FINGERPRINT", ""))
	if fp == "" {
		return exitAccess{}, errors.New("EXIT_NODE_HOST_KEY_FINGERPRINT is required whenever the exit is reached by password " +
			"(without it anybody on the path could pose as the exit and receive its root password). On the exit run " +
			"`ssh-keygen -lf /etc/ssh/ssh_host_ed25519_key.pub -E sha256` and put the SHA256:… value into server/.env")
	}
	if !fingerprintRe.MatchString(fp) {
		return exitAccess{}, fmt.Errorf("EXIT_NODE_HOST_KEY_FINGERPRINT %q must look like SHA256:<43 base64 characters> (the ed25519 host key fingerprint)", fp)
	}
	return exitAccess{password: pw, fingerprint: fp}, nil
}

// hostKeyMatchesFingerprint reports whether the "<type> <base64>" host key has the pinned
// SHA256 fingerprint.
func hostKeyMatchesFingerprint(keyType, keyB64, fingerprint string) bool {
	key, _, _, _, err := ssh.ParseAuthorizedKey([]byte(keyType + " " + keyB64))
	if err != nil {
		return false
	}
	return ssh.FingerprintSHA256(key) == fingerprint
}

// installOffsiteBackupKey makes the Hub backup key usable on the exit. If the key already
// answers the forced-command probe with the current receiver version (checked against the
// host key pinned in the Hub's known_hosts), nothing is touched and no exit password is needed,
// so a re-run survives a password rotation. Otherwise the receiver and the authorized_keys entry
// are installed over the exit's root password, with the exit's ed25519 host key pinned by
// EXIT_NODE_HOST_KEY_FINGERPRINT, and that host key is then pinned in the Hub's known_hosts.
func installOffsiteBackupKey(hub *ssh.Client, cfg *deployConfig, target, password string, port int) error {
	want := "PONG " + backupReceiverVersion()
	fmt.Printf("[ops] offsite: проверяю, работает ли ключ бэкапа на %s:%d (при первом запуске проба ожидаемо упадёт)\n", target, port)
	out, _ := runSSHBash(hub, "offsite probe", backupProbeCommand(target, port), false)
	if strings.Contains(out, want) {
		fmt.Printf("[ops] offsite backup key already installed on %s, receiver is current\n", target)
		fmt.Println("[ops] offsite backup key installed, probe: PONG")
		return nil
	}

	access, err := exitAccessFor(cfg.env, password)
	if err != nil {
		return err
	}
	pubOut, err := runSSHBash(hub, "read backup public key", "cat "+backupSSHKey+".pub", true)
	if err != nil {
		return fmt.Errorf("backup public key missing on the Hub (did the backup timer step fail?): %w", err)
	}
	authLine, err := backupAuthorizedKeysLine(pubOut)
	if err != nil {
		return err
	}
	exit, err := sshDialAlgos(net.JoinHostPort(target, strconv.Itoa(port)), "root", access.password, access.fingerprint, []string{ssh.KeyAlgoED25519})
	if err != nil {
		return fmt.Errorf("SSH to the backup target %s:%d (password + pinned host key): %w", target, port, err)
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
	if !hostKeyMatchesFingerprint(f[0], f[1], access.fingerprint) {
		return fmt.Errorf("the ed25519 host key file of %s does not match EXIT_NODE_HOST_KEY_FINGERPRINT — not pinning it", target)
	}
	probe, err := runSSHBash(hub, "pin exit host key + probe", renderKnownHostsScript(target, port, f[0], f[1]), true)
	if err != nil || !strings.Contains(probe, want) {
		return fmt.Errorf("offsite SSH probe failed (want %q): %v", want, err)
	}
	fmt.Println("[ops] offsite backup key installed, probe: PONG")
	return nil
}
