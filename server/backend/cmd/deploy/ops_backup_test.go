package main

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"os/exec"
	"regexp"
	"sort"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"
)

// The scripts under test are executed by a real bash against stub functions that replace
// runuser / ssh / age / df / sleep. Everything happens in a mktemp directory created by the
// harness itself; nothing outside it is touched. Tests skip when there is no working bash.

const testAgeRecipient = "age1ql3z7hjy54pw3hyww5ayyfg7zqgvc7w3j2elw8zmrj2kg5sfn9aqmcac8p"

// runBash runs script the way production does: the remote login shell receives the single
// command line `bash -c '<shQuote(script)>'` and parses it. Here that command line is fed to an
// outer bash on stdin (no argv quoting or size limits of the host OS in the way), so a quoting
// bug in shQuote shows up as a failing script. Returns stdout+stderr and the exit status.
func runBash(t *testing.T, script string) (string, int) {
	t.Helper()
	return runBashStdin(t, "bash -c "+shQuote(script)+"\n")
}

// runBashStdin feeds script on stdin (no argv size limit) — used for syntax checks and by runBash.
func runBashStdin(t *testing.T, script string, args ...string) (string, int) {
	t.Helper()
	cmd := exec.Command("bash", args...)
	cmd.Stdin = strings.NewReader(script)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	exit := 0
	if err := cmd.Run(); err != nil {
		ee, ok := err.(*exec.ExitError)
		if !ok {
			t.Fatalf("run bash: %v", err)
		}
		exit = ee.ExitCode()
	}
	return out.String(), exit
}

func hasLine(out, line string) bool {
	for _, l := range strings.Split(out, "\n") {
		if strings.TrimRight(l, "\r") == line {
			return true
		}
	}
	return false
}

func lineWithPrefix(out, prefix string) string {
	for _, l := range strings.Split(out, "\n") {
		if strings.HasPrefix(l, prefix) {
			return strings.TrimRight(l, "\r")
		}
	}
	return ""
}

func assignments(vars map[string]string) string {
	keys := make([]string, 0, len(vars))
	for k := range vars {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var sb strings.Builder
	for _, k := range keys {
		fmt.Fprintf(&sb, "%s=%s\n", k, shQuote(vars[k]))
	}
	return sb.String()
}

func mergeVars(base, over map[string]string) map[string]string {
	m := map[string]string{}
	for k, v := range base {
		m[k] = v
	}
	for k, v := range over {
		m[k] = v
	}
	return m
}

// ───────────────────────── heyearth-backup.sh ─────────────────────────

const backupHarnessStubs = `
runuser() {
  shift 2; if [ "$1" = "--" ]; then shift; fi
  cmd="$1"; shift
  echo "$cmd" >> "$T/calls"
  case "$cmd" in
    pg_dump) if [ "$PGDUMP_FAIL" = 1 ]; then echo "pg_dump: stub failure" >&2; return 1; fi
             if [ "$PGDUMP_EMPTY" = 1 ]; then return 0; fi
             printf 'PGDMP-stub-%s' "$*" ;;
    psql)    if [ "$PSQL_FAIL" = 1 ]; then return 1; fi; echo "$USERS_STUB" ;;
    *) echo "STUB-BAD-RUNUSER $cmd" >&2; return 99 ;;
  esac
}
df() { printf 'Avail\n%s\n' "$DF_AVAIL"; }
age() {
  echo "$*" >> "$T/age.args"
  ls -A "$T"/backups/.work-*/ > "$T/work_at_age" 2>/dev/null
  if [ "$AGE_FAIL" = 1 ]; then echo "age: stub failure" >&2; return 1; fi
  out=""; in=""
  while [ $# -gt 0 ]; do
    case "$1" in -R) shift 2 ;; -o) out="$2"; shift 2 ;; *) in="$1"; shift ;; esac
  done
  { printf 'AGE-STUB\n'; cat "$in"; } > "$out"
}
ssh() {
  echo "$*" >> "$T/ssh.args"
  cat > "$T/received"
  n=$(wc -l < "$T/ssh.args" | tr -d ' ')
  case "$SSH_MODE" in
    ok)     echo "OK $(sha256sum "$T/received" | cut -d' ' -f1)" ;;
    badsum) echo "OK 0000000000" ;;
    fail)   echo "ssh: connect to host timed out" >&2; return 255 ;;
    flaky)  if [ "$n" -lt 3 ]; then echo "ssh: connection reset" >&2; return 255; fi
            echo "OK $(sha256sum "$T/received" | cut -d' ' -f1)" ;;
  esac
}
sleep() { :; }
`

const backupHarnessState = `
rc=$?
echo "STATE exit=$rc"
B="$T/backups"
echo "STATE archives=$(ls "$B" | grep -c '^backup-.*\.tar\.gz$')"
echo "STATE names=$(ls "$B" | grep '^backup-.*\.tar\.gz$' | sed 's/^backup-//; s/\.tar\.gz$//' | tr '\n' ' ')"
echo "STATE last_success=$([ -f "$B/last-success" ] && echo 1 || echo 0)"
echo "STATE last_offsite=$([ -f "$B/last-offsite" ] && echo 1 || echo 0)"
echo "STATE last_error=$([ -f "$B/last-error" ] && echo 1 || echo 0)"
echo "STATE last_error_text=$(cat "$B/last-error" 2>/dev/null | cut -d' ' -f2-)"
echo "STATE stray=$(ls -A "$B" | grep -cE '^\.work-|\.part$')"
echo "STATE pgdump_calls=$(grep -c '^pg_dump$' "$T/calls" 2>/dev/null)"
echo "STATE ssh_calls=$(cat "$T/ssh.args" 2>/dev/null | wc -l | tr -d ' ')"
echo "STATE ssh_args=$(sed -n 1p "$T/ssh.args" 2>/dev/null)"
echo "STATE ssh_last=$(tail -n 1 "$T/ssh.args" 2>/dev/null)"
echo "STATE age_args=$(sed -n 1p "$T/age.args" 2>/dev/null)"
echo "STATE work_at_age=$( [ -f "$T/work_at_age" ] && tr '\n' ' ' < "$T/work_at_age" )"
newest=$(ls -1t "$B"/backup-*.tar.gz 2>/dev/null | sed -n 1p)
if [ -n "$newest" ]; then
  echo "STATE members=$(tar -tzf "$newest" | sort | tr '\n' ' ')"
  echo "STATE meta=$(tar -xzOf "$newest" meta.txt | grep users_count)"
  echo "STATE files=$(tar -xzOf "$newest" files.tar.gz | tar -tz | grep -v '/$' | sort | tr '\n' ' ')"
  echo "STATE dump=$(tar -xzOf "$newest" db.dump | head -c 30)"
  if [ -f "$T/received" ]; then
    echo "STATE received_head=$(head -n 1 "$T/received")"
    echo "STATE received_is_plain_archive=$(cmp -s "$newest" "$T/received" && echo 1 || echo 0)"
    echo "STATE received_wraps_archive=$(tail -n +2 "$T/received" | cmp -s - "$newest" && echo 1 || echo 0)"
  fi
fi
`

// backupHarness builds the bash program for one case: env file, fake config tree, stubs, an
// optional pre-step, then the script itself sourced in a subshell.
func backupHarness(vars map[string]string, pre string) string {
	base := map[string]string{
		"KEEP": "14", "TARGET_HOST": "203.0.113.7", "PGDUMP_FAIL": "0", "PGDUMP_EMPTY": "0",
		"PSQL_FAIL": "0", "USERS_STUB": "42", "SSH_MODE": "ok", "AGE_FAIL": "0", "AGE_PUB": "1",
		"DF_AVAIL": "999999999999",
	}
	v := mergeVars(base, vars)
	return assignments(v) + `
T=$(mktemp -d); trap 'rm -rf "$T"' EXIT
mkdir -p "$T/backups" "$T/root/opt/heyearth/state" "$T/root/opt/heyearth/mainserver/certs" "$T/root/etc/letsencrypt" "$T/root/etc/nginx/stream-enabled"
echo reality > "$T/root/opt/heyearth/state/reality-keys.json"
echo jwt > "$T/root/opt/heyearth/mainserver/api.env"
echo ca > "$T/root/opt/heyearth/mainserver/certs/ca-cert.pem"
echo le > "$T/root/etc/letsencrypt/cert.pem"
echo sni > "$T/root/etc/nginx/stream-enabled/heyearth"
mkdir -p "$T/root/etc/systemd/system/hysteria2-server.service.d"
echo unit > "$T/root/etc/systemd/system/hysteria2-client-5.service"; echo unit > "$T/root/etc/systemd/system/hysteria2-client-express.service"
echo dropin > "$T/root/etc/systemd/system/hysteria2-server.service.d/override.conf"; echo other > "$T/root/etc/systemd/system/unrelated.service"
: > "$T/calls"
if [ "$AGE_PUB" = 1 ]; then echo "` + testAgeRecipient + `" > "$T/age.pub"; fi
printf 'BACKUP_DIR=%s\nKEEP=%s\nTARGET_HOST=%s\nTARGET_PORT=22\nSSH_KEY=%s\nAGE_RECIPIENT_FILE=%s\nFILES_ROOT=%s\n' \
  "$T/backups" "$KEEP" "$TARGET_HOST" "$T/key" "$T/age.pub" "$T/root" > "$T/backup.env"
export HEYEARTH_BACKUP_ENV="$T/backup.env"
` + backupHarnessStubs + pre + `
cat > "$T/backup.sh" <<'BACKUP_SCRIPT_EOF'
` + backupScript + `BACKUP_SCRIPT_EOF
( . "$T/backup.sh" )
` + backupHarnessState
}

func TestBackupScript(t *testing.T) {
	if !haveBash() {
		t.Skip("no working bash")
	}
	lastArchive := func(size int) string {
		return fmt.Sprintf(`head -c %d /dev/zero > "$T/backups/backup-20260101-000000.tar.gz"`+"\n", size)
	}
	cases := []struct {
		name     string
		vars     map[string]string
		pre      string
		wantExit int
		want     []string // exact lines
		wantSub  []string // substrings of the whole output
		notSub   []string
	}{
		{
			name: "success: plain local archive, encrypted copy shipped off-box and verified",
			want: []string{
				"STATE exit=0", "STATE archives=1", "STATE last_success=1", "STATE last_offsite=1", "STATE last_error=0",
				"STATE stray=0", "STATE ssh_calls=1", "STATE received_head=AGE-STUB", "STATE received_is_plain_archive=0", "STATE received_wraps_archive=1",
				// the dump, the config tarball and meta.txt are gone from the work dir before the encrypted stream is created
				"STATE work_at_age=",
				"STATE members=db.dump files.tar.gz meta.txt ",
				"STATE meta=users_count=42",
				// the Hub's tunnel client units and hysteria2-server drop-ins, nothing else from systemd
				"STATE files=etc/letsencrypt/cert.pem etc/nginx/stream-enabled/heyearth etc/systemd/system/hysteria2-client-5.service etc/systemd/system/hysteria2-client-express.service " +
					"etc/systemd/system/hysteria2-server.service.d/override.conf opt/heyearth/mainserver/api.env opt/heyearth/mainserver/certs/ca-cert.pem opt/heyearth/state/reality-keys.json ",
			},
			wantSub: []string{"local: ", "users=42)", "offsite: 203.0.113.7 OK (encrypted, sha256 ", "BatchMode=yes", "StrictHostKeyChecking=yes", "root@203.0.113.7 backup-"},
			notSub:  []string{"ERROR", "WARN", "STUB-BAD"},
		},
		{
			name: "no tunnel client units or drop-ins (a Hub without exits): the unmatched glob is skipped, the rest is archived",
			pre:  `rm -rf "$T/root/etc/systemd"` + "\n",
			want: []string{
				"STATE exit=0", "STATE archives=1", "STATE last_error=0",
				"STATE files=etc/letsencrypt/cert.pem etc/nginx/stream-enabled/heyearth opt/heyearth/mainserver/api.env opt/heyearth/mainserver/certs/ca-cert.pem opt/heyearth/state/reality-keys.json ",
			},
			notSub: []string{"ERROR", "STUB-BAD"},
		},
		{
			name: "pg_dump failure: no archive, last-error written, work dir cleaned, exit 1",
			vars: map[string]string{"PGDUMP_FAIL": "1"}, wantExit: 1,
			want: []string{
				"STATE exit=1", "STATE archives=0", "STATE last_success=0", "STATE last_offsite=0", "STATE last_error=1",
				"STATE last_error_text=pg_dump failed", "STATE stray=0", "STATE ssh_calls=0",
			},
			wantSub: []string{"ERROR: pg_dump failed"},
		},
		{
			name: "an empty dump is a failure, not a backup",
			vars: map[string]string{"PGDUMP_EMPTY": "1"}, wantExit: 1,
			want: []string{"STATE exit=1", "STATE archives=0", "STATE last_error=1", "STATE last_error_text=pg_dump produced an empty dump", "STATE stray=0"},
		},
		{
			name: "users count that is not a number fails before pg_dump",
			vars: map[string]string{"USERS_STUB": "ERROR: relation users does not exist"}, wantExit: 1,
			want: []string{"STATE exit=1", "STATE archives=0", "STATE last_error=1"},
		},
		{
			name: "psql failure is a failure",
			vars: map[string]string{"PSQL_FAIL": "1"}, wantExit: 1,
			want: []string{"STATE exit=1", "STATE archives=0", "STATE last_error_text=users count failed"},
		},
		{
			name: "off-box copy failing 3 times: exit 1, local archive kept, last-error set, last-offsite absent",
			vars: map[string]string{"SSH_MODE": "fail"}, wantExit: 1,
			want: []string{
				"STATE exit=1", "STATE archives=1", "STATE last_success=1", "STATE last_offsite=0", "STATE last_error=1",
				"STATE ssh_calls=3", "STATE last_error_text=offsite copy to 203.0.113.7 failed after 3 attempts (local archive kept)",
			},
			wantSub: []string{"offsite attempt 1 failed", "offsite attempt 3 failed"},
		},
		{
			name: "a wrong checksum from the receiver is a failed attempt",
			vars: map[string]string{"SSH_MODE": "badsum"}, wantExit: 1,
			want: []string{"STATE exit=1", "STATE archives=1", "STATE last_offsite=0", "STATE ssh_calls=3"},
		},
		{
			name: "two failed attempts then success: exit 0, last-offsite written, no last-error",
			vars: map[string]string{"SSH_MODE": "flaky"},
			want: []string{"STATE exit=0", "STATE last_offsite=1", "STATE last_error=0", "STATE ssh_calls=3", "STATE received_wraps_archive=1"},
		},
		{
			name:    "no TARGET_HOST: local backup only, warning, exit 0, no ssh, no age",
			vars:    map[string]string{"TARGET_HOST": "", "AGE_PUB": "0"},
			want:    []string{"STATE exit=0", "STATE archives=1", "STATE last_success=1", "STATE last_offsite=0", "STATE last_error=0", "STATE ssh_calls=0", "STATE age_args="},
			wantSub: []string{"WARN: no TARGET_HOST, offsite skipped"},
		},
		{
			name: "a target without an age recipient is never served plain: local archive kept, nothing sent",
			vars: map[string]string{"AGE_PUB": "0"}, wantExit: 1,
			want: []string{
				"STATE exit=1", "STATE archives=1", "STATE last_success=1", "STATE last_offsite=0", "STATE last_error=1", "STATE ssh_calls=0", "STATE age_args=",
				"STATE last_error_text=offsite copy needs an age recipient (AGE_RECIPIENT_FILE) — a plain archive is never sent off the Hub (local archive kept)",
			},
		},
		{
			name: "age failing: nothing is sent, local archive kept",
			vars: map[string]string{"AGE_FAIL": "1"}, wantExit: 1,
			want: []string{"STATE exit=1", "STATE archives=1", "STATE last_success=1", "STATE ssh_calls=0", "STATE last_error_text=age encryption failed (local archive kept)"},
		},
		{
			name: "free space below 1 GiB: fails before pg_dump with a clear message, no archive",
			vars: map[string]string{"DF_AVAIL": "1000"}, wantExit: 1,
			want: []string{
				"STATE exit=1", "STATE archives=0", "STATE last_success=0", "STATE last_error=1", "STATE pgdump_calls=0", "STATE stray=0",
				"STATE last_error_text=not enough free space in $T/backups: 1000 bytes free, 1073741824 needed (2x the last archive + 1 GiB)",
			},
		},
		{
			name: "free space needed is 2x the last archive + 1 GiB: one byte short fails",
			vars: map[string]string{"DF_AVAIL": "1073751823"}, pre: lastArchive(5000), wantExit: 1,
			want:    []string{"STATE exit=1", "STATE archives=1", "STATE last_success=0", "STATE pgdump_calls=0"},
			wantSub: []string{"1073751823 bytes free, 1073751824 needed"},
		},
		{
			name: "free space needed is 2x the last archive + 1 GiB: exactly enough passes",
			vars: map[string]string{"DF_AVAIL": "1073751824"}, pre: lastArchive(5000),
			want: []string{"STATE exit=0", "STATE archives=2", "STATE last_success=1", "STATE pgdump_calls=1"},
		},
		{
			name: "unreadable free space fails instead of guessing",
			vars: map[string]string{"DF_AVAIL": ""}, wantExit: 1,
			want:    []string{"STATE exit=1", "STATE archives=0", "STATE pgdump_calls=0"},
			wantSub: []string{"cannot read the free space of"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			out, exit := runBash(t, backupHarness(c.vars, c.pre))
			if exit != 0 {
				t.Fatalf("harness failed (exit %d), the script's status is reported as STATE exit:\n%s", exit, out)
			}
			if got := lineWithPrefix(out, "STATE exit="); got != fmt.Sprintf("STATE exit=%d", c.wantExit) {
				t.Fatalf("%s, want exit %d\n%s", got, c.wantExit, out)
			}
			for _, l := range c.want {
				if strings.Contains(l, "$T/") { // the temp dir is only known to the harness
					prefix, suffix, _ := strings.Cut(l, "$T/")
					p := lineWithPrefix(out, prefix)
					if p == "" || !strings.HasSuffix(p, suffix) {
						t.Errorf("missing line like %q in:\n%s", l, out)
					}
					continue
				}
				if !hasLine(out, l) {
					t.Errorf("missing line %q in:\n%s", l, out)
				}
			}
			for _, s := range c.wantSub {
				if !strings.Contains(out, s) {
					t.Errorf("output lacks %q:\n%s", s, out)
				}
			}
			for _, s := range c.notSub {
				if strings.Contains(out, s) {
					t.Errorf("output must not contain %q:\n%s", s, out)
				}
			}
		})
	}
}

func TestBackupScriptOffsiteCommand(t *testing.T) {
	if !haveBash() {
		t.Skip("no working bash")
	}
	out, _ := runBash(t, backupHarness(nil, "")+`echo "STATE cipher_size=$(wc -c < "$T/received" | tr -d ' ')"
echo "STATE cipher_sha=$(sha256sum "$T/received" | cut -d' ' -f1)"
`)
	last := lineWithPrefix(out, "STATE ssh_last=")
	m := regexp.MustCompile(`root@203\.0\.113\.7 (backup-\d{8}-\d{6}\.tar\.gz\.age) (\d+) ([0-9a-f]{64})$`).FindStringSubmatch(last)
	if m == nil {
		t.Fatalf("the remote command must be '<name>.tar.gz.age <size> <sha256>', got %q\n%s", last, out)
	}
	if !hasLine(out, "STATE cipher_size="+m[2]) || !hasLine(out, "STATE cipher_sha="+m[3]) {
		t.Errorf("the declared size and sha256 must be those of the ciphertext that was sent:\n%s", out)
	}
	if strings.Contains(last, ".tar.gz ") {
		t.Errorf("the plain archive name must not be sent: %q", last)
	}
	if age := lineWithPrefix(out, "STATE age_args="); !strings.Contains(age, "-R ") || !strings.Contains(age, "-o ") || !strings.Contains(age, m[1]) {
		t.Errorf("age must encrypt to the recipient file (-R) and write the ciphertext under the offsite name: %q", age)
	}
}

func TestBackupScriptRotation(t *testing.T) {
	if !haveBash() {
		t.Skip("no working bash")
	}
	pre := `for d in 01 02 03 04; do echo old > "$T/backups/backup-202601${d}-000000.tar.gz"; touch -d "2026-01-${d} 00:00:00" "$T/backups/backup-202601${d}-000000.tar.gz"; done
echo keepme > "$T/backups/notes.txt"
`
	out, _ := runBash(t, backupHarness(map[string]string{"KEEP": "3", "TARGET_HOST": "", "AGE_PUB": "0"}, pre)+`echo "STATE notes=$(cat "$T/backups/notes.txt")"`+"\n")
	names := strings.Fields(strings.TrimPrefix(lineWithPrefix(out, "STATE names="), "STATE names="))
	if len(names) != 3 || names[0] != "20260103-000000" || names[1] != "20260104-000000" ||
		!regexp.MustCompile(`^\d{8}-\d{6}$`).MatchString(names[2]) || names[2] <= "20260104-000000" {
		t.Fatalf("KEEP=3 must leave the two newest old archives and the new one, got %v\n%s", names, out)
	}
	if !hasLine(out, "STATE notes=keepme") {
		t.Fatalf("rotation touched a file that is not a backup archive:\n%s", out)
	}
}

// ───────────────────────── heyearth-backup-verify.sh ─────────────────────────

func verifyHarness(expected, restored string, restoreFail bool) string {
	fail := "0"
	if restoreFail {
		fail = "1"
	}
	return assignments(map[string]string{"EXPECTED": expected, "RESTORED": restored, "RESTORE_FAIL": fail}) + `
T=$(mktemp -d); W=$(mktemp -d); trap 'rm -rf "$T" "$W"' EXIT
echo "PGDMP" > "$W/db.dump"; printf 'created_at=now\nusers_count=%s\nhost=h\n' "$EXPECTED" > "$W/meta.txt"
tar -C "$W" -czf "$T/backup-20261012-040000.tar.gz" db.dump meta.txt
printf 'ciphertext' > "$T/backup-20261012-040000.tar.gz.age"
: > "$T/calls"
runuser() {
  shift 2; if [ "$1" = "--" ]; then shift; fi
  cmd="$1"; shift
  echo "$cmd $*" >> "$T/calls"
  case "$cmd" in
    dropdb|createdb) ;;
    pg_restore) cat > /dev/null; if [ "$RESTORE_FAIL" = 1 ]; then echo "pg_restore: stub error" >&2; return 1; fi ;;
    psql) case "$*" in
            *schema_migrations*) echo "51 dirty=f" ;;
            *) echo "$RESTORED" ;;
          esac ;;
  esac
}
cat > "$T/verify.sh" <<'VERIFY_SCRIPT_EOF'
` + backupVerifyScript + `VERIFY_SCRIPT_EOF
( . "$T/verify.sh" "${VERIFY_ARCHIVE:-$T/backup-20261012-040000.tar.gz}" )
rc=$?
echo "STATE exit=$rc"
echo "STATE dropdb_calls=$(grep -c '^dropdb' "$T/calls" 2>/dev/null)"
echo "STATE last_call=$(tail -1 "$T/calls" 2>/dev/null | cut -d' ' -f1)"
`
}

func TestBackupVerifyScript(t *testing.T) {
	if !haveBash() {
		t.Skip("no working bash")
	}
	cases := []struct {
		name               string
		expected, restored string
		restoreFail        bool
		wantExit           int
		want               string // substring
		notWant            string
	}{
		{"same count: OK", "42", "42", false, 0, "RESTORE-TEST: OK\n", "drift"},
		{"count differs by 2 of 100 (registrations while the backup ran): OK with a note", "100", "102", false, 0, "RESTORE-TEST: OK (users changed by 2 while the backup ran)", "MISMATCH"},
		{"count differs by 10 of 100: MISMATCH", "100", "110", false, 1, "RESTORE-TEST: MISMATCH", "RESTORE-TEST: OK"},
		{"1% tolerance scales with the table: 50 of 10000 is OK", "10000", "9950", false, 0, "RESTORE-TEST: OK (users changed by 50", "MISMATCH"},
		{"empty restored table is a MISMATCH", "42", "0", false, 1, "RESTORE-TEST: MISMATCH", "RESTORE-TEST: OK"},
		{"restored count that is not a number is a MISMATCH", "42", "psql: error", false, 1, "RESTORE-TEST: MISMATCH (users count is not a number)", "RESTORE-TEST: OK"},
		{"pg_restore failing aborts, the scratch DB is still dropped", "42", "42", true, 1, "STATE last_call=dropdb", "RESTORE-TEST: OK"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			out, exit := runBash(t, verifyHarness(c.expected, c.restored, c.restoreFail))
			if exit != 0 {
				t.Fatalf("harness exit %d:\n%s", exit, out)
			}
			if !hasLine(out, fmt.Sprintf("STATE exit=%d", c.wantExit)) {
				t.Errorf("want script exit %d:\n%s", c.wantExit, out)
			}
			if !strings.Contains(out, c.want) {
				t.Errorf("output lacks %q:\n%s", c.want, out)
			}
			if c.notWant != "" && strings.Contains(out, c.notWant) {
				t.Errorf("output must not contain %q:\n%s", c.notWant, out)
			}
			if !c.restoreFail && !strings.Contains(out, "schema=51 dirty=f") {
				t.Errorf("schema version missing:\n%s", out)
			}
			if !hasLine(out, "STATE dropdb_calls=2") && !c.restoreFail {
				t.Errorf("the scratch DB must be dropped before the restore and again on exit:\n%s", out)
			}
		})
	}

	t.Run("an encrypted off-box copy is refused with the way to decrypt it, no database is touched", func(t *testing.T) {
		t.Parallel()
		out, _ := runBash(t, assignments(map[string]string{"VERIFY_ARCHIVE": "/x/backup-20261012-040000.tar.gz.age"})+verifyHarness("42", "42", false))
		if !hasLine(out, "STATE exit=2") || !strings.Contains(out, "is encrypted: decrypt it first (age -d -i backup.key") || !hasLine(out, "STATE dropdb_calls=0") {
			t.Fatalf("output:\n%s", out)
		}
	})
}

// ───────────────────────── heyearth-backup-receive (runs on the exit) ─────────────────────────

type receiverOpts struct {
	keepDays, perDate, max, min int64
	realFlock                   bool // use the host's flock instead of the stub
}

// receiverForTest swaps the fixed directory / retention / caps of the receiver for test values.
// The production text has no environment hooks: the receiver is a security boundary.
func receiverForTest(t *testing.T, o receiverOpts) string {
	t.Helper()
	const fixed = "DIR=/var/backups/heyearth-hub; KEEP_DAYS=8; PER_DATE=2; MAX_BYTES=4294967296; MIN_BYTES=104857600;"
	if !strings.Contains(backupReceiverScript, fixed) {
		t.Fatalf("receiver no longer contains %q — update the test", fixed)
	}
	return strings.Replace(renderBackupReceiver(), fixed,
		fmt.Sprintf(`DIR="$T/store"; KEEP_DAYS=%d; PER_DATE=%d; MAX_BYTES=%d; MIN_BYTES=%d;`, o.keepDays, o.perDate, o.max, o.min), 1)
}

// receiverHarness defines: n <date-expr> -> an offsite name for that date, cmdline <name> <file>
// -> the forced-command line for that payload, recv <command> <stdin-file> -> runs the receiver.
// flock is a stub (exit 1 while $T/busy exists) unless the test asks for the real one: not every
// host has util-linux's flock (the Windows dev machine does not).
func receiverHarness(t *testing.T, o receiverOpts, body string) string {
	flockSetup := `mkdir -p "$T/bin"
printf '#!/bin/sh\n[ -e "$T/busy" ] && exit 1\nexit 0\n' > "$T/bin/flock"; chmod +x "$T/bin/flock"
export PATH="$T/bin:$PATH"
`
	if o.realFlock {
		flockSetup = `command -v flock >/dev/null 2>&1 || { echo SKIP-NO-FLOCK; exit 0; }
`
	}
	return `
T=$(mktemp -d); trap 'rm -rf "$T"' EXIT
cat > "$T/receive.sh" <<'RECEIVER_EOF'
` + receiverForTest(t, o) + `RECEIVER_EOF
export T
` + flockSetup + `head -c 400 /dev/zero | tr '\0' 'x' > "$T/blob"
head -c 500 /dev/zero | tr '\0' 'y' > "$T/blob500"
head -c 3000 /dev/zero | tr '\0' 'z' > "$T/blob3000"
n() { date -d "$1" +backup-%Y%m%d-%H%M%S.tar.gz.age; }
cmdline() { printf '%s %s %s' "$1" "$(wc -c < "$2" | tr -d ' ')" "$(sha256sum "$2" | cut -d' ' -f1)"; }
recv() { SSH_ORIGINAL_COMMAND="$1" bash "$T/receive.sh" < "$2"; }
stored() { ls "$T/store" 2>/dev/null | wc -l | tr -d ' '; }
` + body
}

var defaultReceiver = receiverOpts{keepDays: 8, perDate: 2, max: 100000, min: 1}

func TestBackupReceiver(t *testing.T) {
	if !haveBash() {
		t.Skip("no working bash")
	}
	version := backupReceiverVersion()
	run := func(t *testing.T, o receiverOpts, body string) string {
		t.Helper()
		out, _ := runBash(t, receiverHarness(t, o, body))
		return out
	}
	want := func(t *testing.T, out string, lines ...string) {
		t.Helper()
		for _, l := range lines {
			if !hasLine(out, l) {
				t.Errorf("missing line %q in:\n%s", l, out)
			}
		}
	}

	t.Run("ping answers with the receiver version and reads nothing", func(t *testing.T) {
		t.Parallel()
		out := run(t, defaultReceiver, `recv ping /dev/null; echo "STATE exit=$?"; echo "STATE stored=$(stored)"`)
		want(t, out, "PONG "+version, "STATE exit=0", "STATE stored=0")
	})

	t.Run("a valid upload is stored atomically and answered with the sha256 of the ciphertext", func(t *testing.T) {
		t.Parallel()
		out := run(t, defaultReceiver, `name=$(n now); recv "$(cmdline "$name" "$T/blob")" "$T/blob"; echo "STATE exit=$?"
echo "STATE sum_ok=$([ "$(sha256sum "$T/store/$name" | cut -d' ' -f1)" = "$(sha256sum "$T/blob" | cut -d' ' -f1)" ] && echo 1 || echo 0)"
echo "STATE only_the_archive=$([ "$(ls "$T/store")" = "$name" ] && echo 1 || echo 0)"
echo "STATE sha=$(sha256sum "$T/blob" | cut -d' ' -f1)"`)
		want(t, out, "STATE exit=0", "STATE sum_ok=1", "STATE only_the_archive=1")
		sha := strings.TrimPrefix(lineWithPrefix(out, "STATE sha="), "STATE sha=")
		if !hasLine(out, "OK "+sha) {
			t.Fatalf("the answer must be 'OK <sha256>':\n%s", out)
		}
	})

	t.Run("anything but ping or a well-formed ciphertext upload command is rejected and stores nothing", func(t *testing.T) {
		t.Parallel()
		good := `$(date +backup-%Y%m%d-%H%M%S)`
		cmds := []string{
			`../evil.tar.gz.age 400 ` + strings.Repeat("a", 64),
			`backup-2026-1.tar.gz.age 400 ` + strings.Repeat("a", 64),
			good + `.tar.gz 400 ` + strings.Repeat("a", 64), // a plain name is no longer accepted
			good + `.tar.gz.age`, // no size / sha
			good + `.tar.gz.age 400`,
			good + `.tar.gz.age abc ` + strings.Repeat("a", 64),
			good + `.tar.gz.age 400 ` + strings.Repeat("A", 64),
			good + `.tar.gz.age 400 ` + strings.Repeat("a", 63),
			good + `.tar.gz.age 400 ` + strings.Repeat("a", 64) + ` extra`,
			good + `.tar.gz.age 400 ` + strings.Repeat("a", 64) + `; id`,
			good + `.tar.gz.age.part 400 ` + strings.Repeat("a", 64),
			`x/` + good + `.tar.gz.age 400 ` + strings.Repeat("a", 64),
			`ping x`, `scp -t /root`, `bash`, ``,
		}
		var sb strings.Builder
		for i, c := range cmds {
			fmt.Fprintf(&sb, "recv \"%s\" \"$T/blob\" >/dev/null 2>&1; echo \"STATE rc%d=$?\"\n", c, i)
		}
		sb.WriteString(`echo "STATE stored=$(stored)"; echo "STATE outside=$(ls "$T" | grep -c evil)"`)
		out := run(t, defaultReceiver, sb.String())
		for i, c := range cmds {
			if !hasLine(out, fmt.Sprintf("STATE rc%d=2", i)) {
				t.Errorf("command %q must be rejected with status 2:\n%s", c, out)
			}
		}
		want(t, out, "STATE stored=0", "STATE outside=0")
	})

	t.Run("the name's date must be real and within one calendar day of the receiver's own date", func(t *testing.T) {
		t.Parallel()
		out := run(t, defaultReceiver, `
f=$(n '+2 days');   recv "$(cmdline "$f" "$T/blob")" "$T/blob" >/dev/null 2>&1; echo "STATE future2d=$?"
f=$(n '+12 hours'); recv "$(cmdline "$f" "$T/blob")" "$T/blob" >/dev/null 2>&1; echo "STATE future12h=$?"
f=$(n '-1 days');   recv "$(cmdline "$f" "$T/blob")" "$T/blob" >/dev/null 2>&1; echo "STATE yesterday=$?"
f=$(n '-2 days');   recv "$(cmdline "$f" "$T/blob")" "$T/blob" >/dev/null 2>&1; echo "STATE older2d=$?"
f=$(n '-8 days');   recv "$(cmdline "$f" "$T/blob")" "$T/blob" >/dev/null 2>&1; echo "STATE older8d=$?"
f=$(n '-9 days');   recv "$(cmdline "$f" "$T/blob")" "$T/blob" >/dev/null 2>&1; echo "STATE older9d=$?"
for bad in backup-20261301-000000 backup-20260231-000000 backup-20261012-250000 backup-99991231-235959; do
  recv "$(cmdline "$bad.tar.gz.age" "$T/blob")" "$T/blob" >/dev/null 2>&1; echo "STATE bad[$bad]=$?"
done
echo "STATE stored=$(stored)"`)
		want(t, out, "STATE future2d=2", "STATE future12h=0", "STATE yesterday=0", "STATE older2d=2", "STATE older8d=2", "STATE older9d=2",
			"STATE bad[backup-20261301-000000]=2", "STATE bad[backup-20260231-000000]=2",
			"STATE bad[backup-20261012-250000]=2", "STATE bad[backup-99991231-235959]=2", "STATE stored=2")
	})

	t.Run("a declared size above MAX_BYTES is refused up front", func(t *testing.T) {
		t.Parallel()
		o := receiverOpts{keepDays: 8, perDate: 2, max: 1000, min: 1}
		out := run(t, o, `name=$(n now)
recv "$name 5000 $(sha256sum "$T/blob3000" | cut -d' ' -f1)" "$T/blob3000" >/dev/null 2>&1; echo "STATE declared_too_big=$? stored=$(stored)"
recv "$(cmdline "$name" "$T/blob500")" "$T/blob500" >/dev/null 2>&1; echo "STATE within_cap=$? stored=$(stored)"`)
		want(t, out, "STATE declared_too_big=4 stored=0", "STATE within_cap=0 stored=1")
	})

	t.Run("exactly the declared number of bytes is read: one more is refused, an endless stream cannot hang or fill the disk", func(t *testing.T) {
		t.Parallel()
		out := run(t, defaultReceiver, `name=$(n now); sum400=$(sha256sum "$T/blob" | cut -d' ' -f1)
recv "$name 400 $sum400" "$T/blob500" >/dev/null 2>&1;  echo "STATE one_more=$? stored=$(stored)"
recv "$name 500 $(sha256sum "$T/blob500" | cut -d' ' -f1)" "$T/blob3000" >/dev/null 2>&1; echo "STATE far_more=$? stored=$(stored)"
recv "$name 500 $sum400" "$T/blob" >/dev/null 2>&1;  echo "STATE too_little=$? stored=$(stored)"
recv "$name 400 $(printf '0%.0s' $(seq 64))" "$T/blob" >/dev/null 2>&1; echo "STATE wrong_sha=$? stored=$(stored)"
recv "$(cmdline "$name" "$T/blob")" "$T/blob" >/dev/null 2>&1;  echo "STATE exact=$? stored=$(stored)"`)
		want(t, out, "STATE one_more=3 stored=0", "STATE far_more=3 stored=0", "STATE too_little=3 stored=0", "STATE wrong_sha=3 stored=0", "STATE exact=0 stored=1")
	})

	t.Run("an endless stream is cut after SIZE+1 bytes, not after MAX_BYTES (here 1 TiB: reading that far would never finish)", func(t *testing.T) {
		t.Parallel()
		o := receiverOpts{keepDays: 8, perDate: 2, max: 1 << 40, min: 1}
		out := run(t, o, `name=$(n now); sum400=$(sha256sum "$T/blob" | cut -d' ' -f1)
yes | SSH_ORIGINAL_COMMAND="$name 500 $sum400" timeout 10 bash "$T/receive.sh" 2>&1 >/dev/null | tr -d '\0'; echo "STATE endless=${PIPESTATUS[1]} stored=$(stored)"`)
		want(t, out, "STATE endless=3 stored=0")
		if !strings.Contains(out, "more data than the declared 500 bytes") {
			t.Errorf("output:\n%s", out)
		}
	})

	t.Run("a re-upload of an existing name with different content is refused (EXISTS, exit 8) and the stored archive stays byte for byte", func(t *testing.T) {
		t.Parallel()
		out := run(t, defaultReceiver, `name=$(n now)
recv "$(cmdline "$name" "$T/blob")" "$T/blob" >/dev/null
before=$(sha256sum "$T/store/$name" | cut -d' ' -f1); mt=$(stat -c %Y "$T/store/$name")
# another payload under the same name: different sha and size, and the same size with a different sha
recv "$(cmdline "$name" "$T/blob500")" "$T/blob500" 2>&1; echo "STATE other_size=$?"
head -c 400 /dev/zero | tr '\0' 'q' > "$T/blob400b"
recv "$(cmdline "$name" "$T/blob400b")" "$T/blob400b" >/dev/null 2>&1; echo "STATE same_size=$?"
recv "$name 400 $(sha256sum "$T/blob" | cut -d' ' -f1)x" "$T/blob" >/dev/null 2>&1; echo "STATE malformed=$?"
echo "STATE untouched=$([ "$(sha256sum "$T/store/$name" | cut -d' ' -f1)" = "$before" ] && [ "$(stat -c %Y "$T/store/$name")" = "$mt" ] && echo 1 || echo 0) stored=$(stored)"`)
		want(t, out, "STATE other_size=8", "STATE same_size=8", "STATE malformed=2", "STATE untouched=1 stored=1")
		if !strings.Contains(out, "EXISTS") {
			t.Errorf("no EXISTS message:\n%s", out)
		}
	})

	t.Run("not enough free space for twice the biggest of (newest archive, declared size, minimum): rejected before anything is written", func(t *testing.T) {
		t.Parallel()
		o := receiverOpts{keepDays: 8, perDate: 2, max: 100000, min: 1 << 50}
		out := run(t, o, `name=$(n now); recv "$(cmdline "$name" "$T/blob")" "$T/blob" 2>&1; echo "STATE exit=$?"; echo "STATE stored=$(stored)"`)
		want(t, out, "STATE exit=5", "STATE stored=0")
		if !strings.Contains(out, "not enough free space") {
			t.Errorf("output:\n%s", out)
		}
	})

	t.Run("forged dates cannot push real archives out: a full date is REFUSED (FULL, exit 7), nothing stored is ever evicted", func(t *testing.T) {
		t.Parallel()
		out := run(t, defaultReceiver, `mkdir -p "$T/store"
# the real archives: one per day for the last five days, and today's 04:00 one; mtimes reversed on purpose
i=0
for d in '-5 days' '-4 days' '-3 days' '-2 days' '-1 days'; do
  i=$((i+1)); f=$(n "$d"); cp "$T/blob" "$T/store/$f"; touch -d "2026-10-0$((10-i)) 04:00:00" "$T/store/$f"; eval "real$i=$f"
done
today=$(date +%Y%m%d); yesterday=$(date -d yesterday +%Y%m%d); tomorrow=$(date -d tomorrow +%Y%m%d)
realtoday=backup-$today-040000.tar.gz.age; cp "$T/blob" "$T/store/$realtoday"
# the attacker: three uploads for every date inside the accepted window ...
for d in $yesterday $today $tomorrow; do
  rcs=""
  for h in 11 12 13; do
    recv "$(cmdline "backup-$d-${h}0000.tar.gz.age" "$T/blob")" "$T/blob" >/dev/null 2>&1; rcs="$rcs$? "
  done
  echo "STATE rcs[$d]=$rcs"
done
# ... and for dates outside it
recv "$(cmdline "$(n '-3 days')" "$T/blob")" "$T/blob" >/dev/null 2>&1; echo "STATE forged_old=$?"
recv "$(cmdline "$(n '+5 days')" "$T/blob")" "$T/blob" >/dev/null 2>&1; echo "STATE forged_future=$?"
for i in 1 2 3 4 5; do eval "f=\$real$i"; echo "STATE real$i=$([ -e "$T/store/$f" ] && echo 1 || echo 0)"; done
echo "STATE realtoday=$([ -e "$T/store/$realtoday" ] && echo 1 || echo 0)"
echo "STATE stored=$(stored)"
echo "STATE today_files=$(ls "$T/store" | grep "backup-$today" | sed "s/backup-$today-//; s/.tar.gz.age//" | tr '\n' ' ')"`)
		// yesterday and today already hold one real archive each: one more fits, then FULL; tomorrow takes two
		var yesterday, today, tomorrow string
		for _, l := range strings.Split(out, "\n") {
			if rest, ok := strings.CutPrefix(strings.TrimRight(l, "\r"), "STATE rcs["); ok {
				_, rcs, _ := strings.Cut(rest, "]=")
				switch {
				case yesterday == "":
					yesterday = rcs
				case today == "":
					today = rcs
				default:
					tomorrow = rcs
				}
			}
		}
		if yesterday != "0 7 7 " || today != "0 7 7 " || tomorrow != "0 0 7 " {
			t.Errorf("per-date results yesterday=%q today=%q tomorrow=%q (want '0 7 7 ', '0 7 7 ', '0 0 7 '):\n%s", yesterday, today, tomorrow, out)
		}
		want(t, out, "STATE forged_old=2", "STATE forged_future=2", "STATE real1=1", "STATE real2=1", "STATE real3=1", "STATE real4=1", "STATE real5=1", "STATE realtoday=1",
			"STATE today_files=040000 110000 ", "STATE stored=10")
	})

	t.Run("the Hub's own retry (same name, same sha256 and size) is answered OK without touching the file, even when its date is full", func(t *testing.T) {
		t.Parallel()
		out := run(t, defaultReceiver, `today=$(date +%Y%m%d)
for h in 01 02; do recv "$(cmdline "backup-$today-${h}0000.tar.gz.age" "$T/blob")" "$T/blob" >/dev/null 2>&1; echo "STATE first$h=$?"; done
f=$T/store/backup-$today-020000.tar.gz.age; mt=$(stat -c %Y "$f")
recv "$(cmdline "backup-$today-030000.tar.gz.age" "$T/blob")" "$T/blob" 2>&1; echo "STATE third=$?"
recv "$(cmdline "backup-$today-020000.tar.gz.age" "$T/blob")" /dev/null 2>&1; echo "STATE retry=$?"
echo "STATE untouched=$([ "$(stat -c %Y "$f")" = "$mt" ] && echo 1 || echo 0) stored=$(stored)"
echo "STATE sha=$(sha256sum "$T/blob" | cut -d' ' -f1)"`)
		want(t, out, "STATE first01=0", "STATE first02=0", "STATE third=7", "STATE retry=0", "STATE untouched=1 stored=2")
		sha := strings.TrimPrefix(lineWithPrefix(out, "STATE sha="), "STATE sha=")
		if !hasLine(out, "OK "+sha) || !strings.Contains(out, "FULL") {
			t.Errorf("the retry must be answered 'OK <sha256>' (without reading its stream) and the third archive refused with FULL:\n%s", out)
		}
	})

	t.Run("files are deleted by age only: older than KEEP_DAYS go, everything else stays however many there are per date", func(t *testing.T) {
		t.Parallel()
		out := run(t, defaultReceiver, `mkdir -p "$T/store"
old9=$(n '-9 days'); old8=$(n '-8 days'); d3a=backup-$(date -d '-3 days' +%Y%m%d)-010000.tar.gz.age; d3b=backup-$(date -d '-3 days' +%Y%m%d)-020000.tar.gz.age; d3c=backup-$(date -d '-3 days' +%Y%m%d)-030000.tar.gz.age
for f in $old9 $old8 $d3a $d3b $d3c; do cp "$T/blob" "$T/store/$f"; done
recv "$(cmdline "$(n now)" "$T/blob")" "$T/blob" >/dev/null 2>&1; echo "STATE exit=$?"
echo "STATE old9=$([ -e "$T/store/$old9" ] && echo 1 || echo 0) old8=$([ -e "$T/store/$old8" ] && echo 1 || echo 0)"
echo "STATE d3=$([ -e "$T/store/$d3a" ] && echo 1 || echo 0)$([ -e "$T/store/$d3b" ] && echo 1 || echo 0)$([ -e "$T/store/$d3c" ] && echo 1 || echo 0)"
echo "STATE stored=$(stored)"`)
		want(t, out, "STATE exit=0", "STATE old9=0 old8=1", "STATE d3=111", "STATE stored=5")
	})

	t.Run("a second upload while one is running is refused with BUSY (exit 6) and writes nothing", func(t *testing.T) {
		t.Parallel()
		out := run(t, defaultReceiver, `name=$(n now); touch "$T/busy"
recv "$(cmdline "$name" "$T/blob")" "$T/blob" 2>&1; echo "STATE busy=$? stored=$(stored)"
rm -f "$T/busy"
recv "$(cmdline "$name" "$T/blob")" "$T/blob" >/dev/null 2>&1; echo "STATE free=$? stored=$(stored)"`)
		want(t, out, "STATE busy=6 stored=0", "STATE free=0 stored=1")
		if !strings.Contains(out, "BUSY") {
			t.Errorf("no BUSY message:\n%s", out)
		}
	})

	t.Run("two real concurrent uploads: the second gets BUSY while the first is still streaming", func(t *testing.T) {
		t.Parallel()
		o := defaultReceiver
		o.realFlock = true
		out := run(t, o, `name=$(n now); other=$(n '-1 days')
( sleep 2; cat "$T/blob" ) | SSH_ORIGINAL_COMMAND="$(cmdline "$name" "$T/blob")" bash "$T/receive.sh" > "$T/first.out" 2>&1 &
sleep 0.8
recv "$(cmdline "$other" "$T/blob")" "$T/blob" > "$T/second.out" 2>&1; echo "STATE second=$?"
wait
echo "STATE first=$(cut -c1-3 "$T/first.out")"; echo "STATE second_msg=$(cat "$T/second.out")"; echo "STATE stored=$(stored)"`)
		if strings.Contains(out, "SKIP-NO-FLOCK") {
			t.Skip("no flock on this host (the stub-based BUSY test covers the receiver's reaction)")
		}
		want(t, out, "STATE second=6", "STATE first=OK ", "STATE stored=1")
		if !strings.Contains(out, "BUSY") {
			t.Errorf("no BUSY message:\n%s", out)
		}
	})
}

// The exit-side installer, run against a temp directory: receiver installed, other keys kept,
// our own entry replaced (not duplicated), and an unreadable authorized_keys aborts untouched.
func TestReceiverInstallScript(t *testing.T) {
	if !haveBash() {
		t.Skip("no working bash")
	}
	const pub = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIOMqqnkVzrm0SdG6UOoqKLsabgH5C9okWi0dh2l9GKJl heyearth-backup@hub"
	auth, err := backupAuthorizedKeysLine(pub)
	if err != nil {
		t.Fatal(err)
	}
	paths := receiverPaths{receiver: "$T/sbin/heyearth-backup-receive", dir: "$T/store", sshDir: "$T/ssh"}
	harness := func(stubs, pre string) string {
		return `T=$(mktemp -d); trap 'rm -rf "$T"' EXIT
mkdir -p "$T/ssh"
printf 'ssh-ed25519 AAAAother1 admin@laptop\nssh-rsa AAAAother2 ci@runner\n' > "$T/ssh/authorized_keys"
` + pre + `
cat > "$T/install.sh" <<'INSTALL_EOF'
` + renderReceiverInstallScriptAt(paths, auth) + `INSTALL_EOF
cp "$T/ssh/authorized_keys" "$T/ak.before"
( flock() { :; }
` + stubs + `
. "$T/install.sh" )
echo "STATE exit=$?"
echo "STATE keys=$(grep -c . "$T/ssh/authorized_keys")"
echo "STATE ours=$(grep -c 'heyearth-backup@hub' "$T/ssh/authorized_keys")"
echo "STATE others=$(grep -c -e AAAAother1 -e AAAAother2 "$T/ssh/authorized_keys")"
echo "STATE unchanged=$(cmp -s "$T/ak.before" "$T/ssh/authorized_keys" && echo 1 || echo 0)"
echo "STATE new_left=$([ -e "$T/ssh/authorized_keys.new" ] && echo 1 || echo 0)"
echo "STATE receiver=$([ -s "$T/sbin/heyearth-backup-receive" ] && head -n 1 "$T/sbin/heyearth-backup-receive")"
echo "STATE forced=$(grep 'heyearth-backup@hub' "$T/ssh/authorized_keys" | cut -c1-59)"
echo "STATE store=$(ls "$T/store" 2>/dev/null | tr '\n' ' ')"
`
	}

	t.Run("installs the receiver and adds our entry next to the other keys; a second run does not duplicate it", func(t *testing.T) {
		t.Parallel()
		out, _ := runBash(t, harness("", `echo 'command="/old",restrict ssh-ed25519 AAAAstale heyearth-backup@hub' >> "$T/ssh/authorized_keys"`)+`
( flock() { :; }; . "$T/install.sh" ) >/dev/null; echo "STATE keys_after_second_run=$(grep -c . "$T/ssh/authorized_keys") ours_after_second_run=$(grep -c 'heyearth-backup@hub' "$T/ssh/authorized_keys")"`)
		for _, l := range []string{"STATE exit=0", "STATE keys=3", "STATE ours=1", "STATE others=2", "STATE new_left=0", "STATE receiver=#!/usr/bin/env bash",
			`STATE forced=command="/usr/local/sbin/heyearth-backup-receive",restrict,`, "STATE keys_after_second_run=3 ours_after_second_run=1"} {
			if !hasLine(out, l) {
				t.Errorf("missing %q:\n%s", l, out)
			}
		}
		if strings.Contains(out, "AAAAstale") {
			t.Errorf("the stale entry of our key must be replaced:\n%s", out)
		}
	})

	t.Run("an unreadable authorized_keys aborts and leaves it untouched (grep exit 2 must not shrink the file)", func(t *testing.T) {
		t.Parallel()
		out, _ := runBash(t, harness("grep() { return 2; }", ""))
		for _, l := range []string{"STATE exit=1", "STATE unchanged=1", "STATE new_left=0", "STATE ours=0", "STATE others=2"} {
			if !hasLine(out, l) {
				t.Errorf("missing %q:\n%s", l, out)
			}
		}
		if !strings.Contains(out, "ABORT: cannot read") || !strings.Contains(out, "(grep exit 2)") {
			t.Errorf("no abort message:\n%s", out)
		}
	})

	t.Run("legacy plaintext archives from an earlier receiver are deleted and counted; ciphertext and foreign files stay", func(t *testing.T) {
		t.Parallel()
		out, _ := runBash(t, harness("", `mkdir -p "$T/store"
echo a > "$T/store/backup-20260101-000000.tar.gz"; echo b > "$T/store/backup-20260102-000000.tar.gz"; echo c > "$T/store/backup-20260103-000000.tar.gz.part"
echo d > "$T/store/backup-20260104-000000.tar.gz.age"; echo e > "$T/store/backup-20260105-000000.tar.gz.age.part"; echo f > "$T/store/notes.txt"`)+`
( flock() { :; }; . "$T/install.sh" ) | grep legacy; echo "STATE store_after_second_run=$(ls "$T/store" | tr '\n' ' ')"`)
		for _, l := range []string{
			"STATE exit=0", "STATE store=backup-20260104-000000.tar.gz.age backup-20260105-000000.tar.gz.age.part notes.txt ",
			"STATE store_after_second_run=backup-20260104-000000.tar.gz.age backup-20260105-000000.tar.gz.age.part notes.txt ",
		} {
			if !hasLine(out, l) {
				t.Errorf("missing %q:\n%s", l, out)
			}
		}
		if !regexp.MustCompile(`removed 3 legacy plaintext archive\(s\) from .*/store`).MatchString(out) || !regexp.MustCompile(`removed 0 legacy plaintext archive\(s\)`).MatchString(out) {
			t.Errorf("the first run must report 3 removed legacy files, the second 0:\n%s", out)
		}
	})

	t.Run("a host without flock is refused before anything is written", func(t *testing.T) {
		t.Parallel()
		out, _ := runBash(t, harness(`command() { if [ "$1" = -v ] && [ "$2" = flock ]; then return 1; fi; builtin command "$@"; }`, ""))
		for _, l := range []string{"STATE exit=1", "STATE unchanged=1", "STATE receiver=", "STATE store="} {
			if !hasLine(out, l) {
				t.Errorf("missing %q:\n%s", l, out)
			}
		}
		if !strings.Contains(out, "ABORT: flock is not installed on the exit") {
			t.Errorf("no abort message:\n%s", out)
		}
	})

	t.Run("an authorized_keys without any line of ours (grep exit 1) is the normal first install", func(t *testing.T) {
		t.Parallel()
		out, _ := runBash(t, harness("", `: > "$T/ssh/authorized_keys"`))
		if !hasLine(out, "STATE exit=0") || !hasLine(out, "STATE keys=1") || !hasLine(out, "STATE ours=1") {
			t.Errorf("output:\n%s", out)
		}
	})
}

// ───────────────────────── backup.env resolution ─────────────────────────

func TestReadBackupEnvScript(t *testing.T) {
	if !haveBash() {
		t.Skip("no working bash")
	}
	run := func(setup string) (string, int) {
		return runBash(t, `T=$(mktemp -d); trap 'rm -rf "$T"' EXIT
export HEYEARTH_BACKUP_ENV="$T/backup.env"
`+setup+"\n"+strings.ReplaceAll(readBackupEnvScript, "/etc/heyearth/backup.env", `${HEYEARTH_BACKUP_ENV}`))
	}
	t.Run("absent file is ENV-ABSENT, exit 0", func(t *testing.T) {
		out, exit := run("")
		if exit != 0 || !hasLine(out, "ENV-ABSENT") {
			t.Fatalf("exit %d:\n%s", exit, out)
		}
	})
	t.Run("present file: the three keys and ENV-OK", func(t *testing.T) {
		out, exit := run(`printf 'BACKUP_DIR=/x\nTARGET_HOST=192.0.2.126\nTARGET_PORT=22\nAGE_RECIPIENT=` + testAgeRecipient + `\nSSH_KEY=/k\n' > "$T/backup.env"`)
		if exit != 0 || !hasLine(out, "ENV-OK") || !hasLine(out, "TARGET_HOST=192.0.2.126") || !hasLine(out, "AGE_RECIPIENT="+testAgeRecipient) || strings.Contains(out, "SSH_KEY") {
			t.Fatalf("exit %d:\n%s", exit, out)
		}
	})
	t.Run("present file without any of the keys (grep exit 1) is fine", func(t *testing.T) {
		out, exit := run(`echo KEEP=14 > "$T/backup.env"`)
		if exit != 0 || !hasLine(out, "ENV-OK") {
			t.Fatalf("exit %d:\n%s", exit, out)
		}
	})
	t.Run("a read error (grep exit 2) is a failure, never ENV-ABSENT", func(t *testing.T) {
		out, exit := run(`echo TARGET_HOST=1.2.3.4 > "$T/backup.env"; grep() { return 2; }`)
		if exit != 1 || hasLine(out, "ENV-ABSENT") || hasLine(out, "ENV-OK") || !strings.Contains(out, "cannot read") {
			t.Fatalf("exit %d:\n%s", exit, out)
		}
	})
}

func TestParseBackupEnv(t *testing.T) {
	ok := func(in string, want backupConfig) {
		t.Helper()
		got, err := parseBackupEnv(in)
		if err != nil || got != want {
			t.Errorf("parseBackupEnv(%q) = %+v, %v; want %+v", in, got, err, want)
		}
	}
	bad := func(in, why string) {
		t.Helper()
		if _, err := parseBackupEnv(in); err == nil {
			t.Errorf("parseBackupEnv(%q) must fail (%s)", in, why)
		}
	}
	ok("ENV-ABSENT\n", backupConfig{})
	ok("ENV-OK\n", backupConfig{})
	ok("TARGET_HOST=192.0.2.126\nTARGET_PORT=2222\nAGE_RECIPIENT="+testAgeRecipient+"\nENV-OK\n", backupConfig{"192.0.2.126", 2222, testAgeRecipient})
	ok("TARGET_HOST=\nTARGET_PORT=22\nAGE_RECIPIENT=\nENV-OK\r\n", backupConfig{"", 22, ""})
	bad("", "no state marker: the read did not work")
	bad("TARGET_HOST=192.0.2.126\n", "no state marker")
	bad("TARGET_HOST=$(reboot)\nENV-OK\n", "malformed host must not be treated as 'no target'")
	bad("TARGET_PORT=99999\nENV-OK\n", "malformed port")
	bad("AGE_RECIPIENT=age1short\nENV-OK\n", "malformed recipient")
}

func TestMergeBackupConfig(t *testing.T) {
	stored := backupConfig{"192.0.2.126", 2222, testAgeRecipient}
	other := "age1" + strings.Repeat("q", 58)
	cases := []struct {
		name       string
		stored     backupConfig
		target     string
		port       int
		recipient  string
		want       backupConfig
		wantErrHas string
	}{
		{"nothing stored, nothing given: local backups only", backupConfig{}, "", 22, "", backupConfig{port: 22}, ""},
		{"flags only", backupConfig{}, "192.0.2.126", 22, testAgeRecipient, backupConfig{"192.0.2.126", 22, testAgeRecipient}, ""},
		{"stored values are kept when no flags are given (a re-run never switches the copy off)", stored, "", 22, "", stored, ""},
		{"a flag target replaces the stored one and takes the flag port", stored, "5.6.7.8", 22, "", backupConfig{"5.6.7.8", 22, testAgeRecipient}, ""},
		{"a new recipient replaces the stored one", stored, "", 22, other, backupConfig{"192.0.2.126", 2222, other}, ""},
		{"a recipient alone (no target) is persisted", backupConfig{}, "", 22, testAgeRecipient, backupConfig{"", 22, testAgeRecipient}, ""},
		{"a target without any recipient is refused", backupConfig{}, "192.0.2.126", 22, "", backupConfig{}, "--backup-age-recipient"},
		{"a stored target without a stored recipient is refused too", backupConfig{"192.0.2.126", 22, ""}, "", 22, "", backupConfig{}, "--backup-age-recipient"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := mergeBackupConfig(c.stored, c.target, c.port, c.recipient)
			if c.wantErrHas != "" {
				if err == nil || !strings.Contains(err.Error(), c.wantErrHas) {
					t.Fatalf("want an error mentioning %q, got %v (%+v)", c.wantErrHas, err, got)
				}
				return
			}
			if err != nil || got != c.want {
				t.Fatalf("got %+v, %v; want %+v", got, err, c.want)
			}
		})
	}
}

func TestValidateBackupFlags(t *testing.T) {
	for _, tc := range []struct {
		target    string
		port      int
		recipient string
		ok        bool
	}{
		{"", 22, "", true}, {"192.0.2.126", 22, testAgeRecipient, true}, {"2001:db8::1", 2222, "", true},
		{"192.0.2.126; rm -rf /", 22, "", false}, {"host.example.com", 22, "", false}, {"1.2.3", 22, "", false},
		{"192.0.2.126", 0, "", false}, {"192.0.2.126", 70000, "", false}, {"", 0, "", false},
		{"", 22, "age1short", false}, {"", 22, "AGE-SECRET-KEY-1QQQQ", false}, {"", 22, testAgeRecipient + "x", false},
		{"", 22, strings.Replace(testAgeRecipient, "age1q", "age1b", 1), false}, // b is not in the bech32 alphabet
	} {
		if err := validateBackupFlags(tc.target, tc.port, tc.recipient); (err == nil) != tc.ok {
			t.Errorf("validateBackupFlags(%q, %d, %q) = %v, want ok=%v", tc.target, tc.port, tc.recipient, err, tc.ok)
		}
	}
}

// ───────────────────────── exit credentials ─────────────────────────

func TestExitAccessFor(t *testing.T) {
	const fp = "SHA256:nThbg6kXUpJWGl7E1IGOCspRomTxdCARLviKw6E5SY8"
	env := func(kv ...string) envMap {
		m := envMap{}
		for i := 0; i < len(kv); i += 2 {
			m[kv[i]] = kv[i+1]
		}
		return m
	}
	t.Run("password and fingerprint from the environment", func(t *testing.T) {
		a, err := exitAccessFor(env("EXIT_NODE_SSH_PASSWORD", "exit-pw", "EXIT_NODE_HOST_KEY_FINGERPRINT", fp), "")
		if err != nil || a.password != "exit-pw" || a.fingerprint != fp {
			t.Fatalf("%+v, %v", a, err)
		}
	})
	t.Run("the flag password wins over the environment", func(t *testing.T) {
		a, err := exitAccessFor(env("EXIT_NODE_SSH_PASSWORD", "env-pw", "EXIT_NODE_HOST_KEY_FINGERPRINT", fp), "flag-pw")
		if err != nil || a.password != "flag-pw" {
			t.Fatalf("%+v, %v", a, err)
		}
	})
	t.Run("the Hub deploy password is never a fallback", func(t *testing.T) {
		_, err := exitAccessFor(env("DEPLOY_SSH_PASSWORD", "hub-pw", "EXIT_NODE_HOST_KEY_FINGERPRINT", fp), "")
		if err == nil || !strings.Contains(err.Error(), "EXIT_NODE_SSH_PASSWORD") || !strings.Contains(err.Error(), "never tried on the exit") {
			t.Fatalf("want a clear error about the missing exit password, got %v", err)
		}
	})
	t.Run("the host key fingerprint is mandatory on the password path", func(t *testing.T) {
		_, err := exitAccessFor(env("EXIT_NODE_SSH_PASSWORD", "exit-pw"), "")
		if err == nil || !strings.Contains(err.Error(), "EXIT_NODE_HOST_KEY_FINGERPRINT is required") || !strings.Contains(err.Error(), "ssh-keygen -lf /etc/ssh/ssh_host_ed25519_key.pub") {
			t.Fatalf("want an error that says how to get the fingerprint, got %v", err)
		}
	})
	t.Run("a malformed fingerprint is refused", func(t *testing.T) {
		for _, bad := range []string{"MD5:aa:bb", "SHA256:short", "nThbg6kXUpJWGl7E1IGOCspRomTxdCARLviKw6E5SY8", "SHA256:" + strings.Repeat("A", 44)} {
			if _, err := exitAccessFor(env("EXIT_NODE_SSH_PASSWORD", "x", "EXIT_NODE_HOST_KEY_FINGERPRINT", bad), ""); err == nil {
				t.Errorf("accepted %q", bad)
			}
		}
	})
}

func TestHostKeyMatchesFingerprint(t *testing.T) {
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	sshPub, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	fields := strings.Fields(string(ssh.MarshalAuthorizedKey(sshPub)))
	fp := ssh.FingerprintSHA256(sshPub)
	if !fingerprintRe.MatchString(fp) {
		t.Fatalf("fingerprintRe does not accept a real fingerprint %q", fp)
	}
	if !hostKeyMatchesFingerprint(fields[0], fields[1], fp) {
		t.Errorf("the key must match its own fingerprint")
	}
	other, _, _ := ed25519.GenerateKey(rand.Reader)
	otherPub, _ := ssh.NewPublicKey(other)
	if hostKeyMatchesFingerprint(fields[0], fields[1], ssh.FingerprintSHA256(otherPub)) {
		t.Errorf("a different key's fingerprint must not match")
	}
	if hostKeyMatchesFingerprint("ssh-ed25519", "not-base64!!", fp) {
		t.Errorf("garbage must not match")
	}
}

// ───────────────────────── Go-side rendering ─────────────────────────

func TestBackupAuthorizedKeysLine(t *testing.T) {
	const pub = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIOMqqnkVzrm0SdG6UOoqKLsabgH5C9okWi0dh2l9GKJl heyearth-backup@hub"
	line, err := backupAuthorizedKeysLine(pub + "\n")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`command="/usr/local/sbin/heyearth-backup-receive"`, "restrict", "no-pty", "no-port-forwarding", "no-agent-forwarding", "no-X11-forwarding", pub} {
		if !strings.Contains(line, want) {
			t.Errorf("authorized_keys line lacks %q: %s", want, line)
		}
	}
	if !strings.HasSuffix(line, pub) || strings.Contains(line, "\n") {
		t.Errorf("the key must come last, on one line: %q", line)
	}
	// The line is pasted into a script that runs as root on the exit: nothing but a plain
	// ed25519 key with our comment may get through.
	for _, bad := range []string{
		"", "ssh-rsa AAAAB3 heyearth-backup@hub", pub + "'; reboot; echo '", "ssh-ed25519 AAAA'x heyearth-backup@hub",
		"ssh-ed25519 AAAA heyearth-backup@hub\nssh-ed25519 BBBB evil@host", "ssh-ed25519 AAAA other@host",
		"ssh-ed25519 AAAA$(id) heyearth-backup@hub",
	} {
		if _, err := backupAuthorizedKeysLine(bad); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
}

func TestReceiverVersion(t *testing.T) {
	v := backupReceiverVersion()
	if !regexp.MustCompile(`^[0-9a-f]{12}$`).MatchString(v) {
		t.Fatalf("version %q", v)
	}
	r := renderBackupReceiver()
	if strings.Contains(r, "__VERSION__") || strings.Count(r, v) != 2 {
		t.Fatalf("the version must replace both placeholders (comment and ping answer):\n%s", r)
	}
	if !strings.Contains(r, `echo "PONG `+v+`"`) {
		t.Fatalf("ping must answer PONG %s", v)
	}
}

func payloadFrom(t *testing.T, script, path string) string {
	t.Helper()
	i := strings.Index(script, "put_file "+path+" ")
	if i < 0 {
		t.Fatalf("no put_file for %s", path)
	}
	m := regexp.MustCompile(`'([A-Za-z0-9+/=]*)'`).FindStringSubmatch(script[i:])
	got, err := base64.StdEncoding.DecodeString(m[1])
	if err != nil {
		t.Fatalf("payload of %s: %v", path, err)
	}
	return string(got)
}

func TestRenderBackupInstallScript(t *testing.T) {
	bc := backupConfig{"192.0.2.126", 2222, testAgeRecipient}
	s := renderBackupInstallScript(bc)
	for path, content := range map[string]string{
		"/etc/heyearth/backup.env":                    renderBackupEnv(bc),
		"/opt/heyearth/bin/heyearth-backup.sh":        backupScript,
		"/opt/heyearth/bin/heyearth-backup-verify.sh": backupVerifyScript,
		"/etc/systemd/system/heyearth-backup.service": backupServiceUnit,
		"/etc/systemd/system/heyearth-backup.timer":   backupTimerUnit,
		"/etc/heyearth/backup-age.pub":                testAgeRecipient + "\n",
	} {
		if got := payloadFrom(t, s, path); got != content {
			t.Errorf("payload of %s does not round-trip", path)
		}
	}
	env := renderBackupEnv(bc)
	for _, want := range []string{"TARGET_HOST=192.0.2.126\n", "TARGET_PORT=2222\n", "KEEP=14\n", "SSH_KEY=/root/.ssh/heyearth_backup_ed25519\n",
		"BACKUP_DIR=/opt/heyearth/backups\n", "AGE_RECIPIENT=" + testAgeRecipient + "\n", "AGE_RECIPIENT_FILE=/etc/heyearth/backup-age.pub\n"} {
		if !strings.Contains(env, want) {
			t.Errorf("backup.env lacks %q", want)
		}
	}
	// everything that can fail is checked or installed before the first write
	first := strings.Index(s, "put_file /etc/heyearth/backup.env")
	for _, marker := range []string{"is not installed", `systemd-analyze calendar "` + backupOnCalendar + `"`, "apt-get install -y -qq age", "cannot install age"} {
		if i := strings.Index(s, marker); i < 0 || first < 0 || i > first {
			t.Errorf("%q must come before the first put_file", marker)
		}
	}
	if !strings.Contains(backupTimerUnit, "OnCalendar=*-*-* 04:00:00 Europe/Moscow") || !strings.Contains(backupTimerUnit, "Persistent=true") {
		t.Errorf("timer: %q", backupTimerUnit)
	}
	// local-only configuration: no age package, no recipient file
	local := renderBackupInstallScript(backupConfig{port: 22})
	if strings.Contains(local, "apt-get") || strings.Contains(local, "backup-age.pub") {
		t.Errorf("a local-only install must not touch age")
	}
	if got := payloadFrom(t, local, "/etc/heyearth/backup.env"); !strings.Contains(got, "TARGET_HOST=\n") || !strings.Contains(got, "AGE_RECIPIENT=\n") {
		t.Errorf("local-only env: %q", got)
	}
}

// The install script must refuse to touch the host when a tool is missing or age cannot be
// installed — run with the checks failing and every mutating command replaced by a trap, so the
// test cannot write to /etc even when it runs as root on a Linux CI host.
func TestBackupInstallAbortsBeforeWriting(t *testing.T) {
	if !haveBash() {
		t.Skip("no working bash")
	}
	trap := `for f in mkdir chmod systemctl ssh-keygen mktemp mv cp tee; do eval "$f() { echo UNEXPECTED-$f; exit 99; }"; done
`
	t.Run("a missing tool", func(t *testing.T) {
		guard := `command() { if [ "$1" = -v ] && [ "$2" = pg_dump ]; then return 1; fi; builtin command "$@"; }` + "\n" + trap
		out, exit := runBash(t, guard+renderBackupInstallScript(backupConfig{"192.0.2.126", 22, testAgeRecipient}))
		if exit != 1 || !strings.Contains(out, "[ops] ABORT: pg_dump is not installed — backup timer not installed") || strings.Contains(out, "UNEXPECTED") {
			t.Fatalf("exit %d:\n%s", exit, out)
		}
	})
	t.Run("age cannot be installed", func(t *testing.T) {
		guard := `command() { if [ "$1" = -v ] && [ "$2" = age ]; then return 1; fi; return 0; }
timeout() { shift; "$@"; }
apt-get() { echo "apt: stub failure" >&2; return 100; }
systemd-analyze() { return 0; }
` + trap
		out, exit := runBash(t, guard+renderBackupInstallScript(backupConfig{"192.0.2.126", 22, testAgeRecipient}))
		if exit != 1 || !strings.Contains(out, "[ops] ABORT: cannot install age — backup timer not installed") || strings.Contains(out, "UNEXPECTED") {
			t.Fatalf("exit %d:\n%s", exit, out)
		}
	})
	t.Run("a systemd that cannot parse the time zone", func(t *testing.T) {
		guard := `command() { return 0; }
systemd-analyze() { return 1; }
` + trap
		out, exit := runBash(t, guard+renderBackupInstallScript(backupConfig{port: 22}))
		if exit != 1 || !strings.Contains(out, "cannot parse OnCalendar with a time zone") || strings.Contains(out, "UNEXPECTED") {
			t.Fatalf("exit %d:\n%s", exit, out)
		}
	})
}

func TestPutFile(t *testing.T) {
	if !haveBash() {
		t.Skip("no working bash")
	}
	enc := func(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }
	script := `set -eu
T=$(mktemp -d); trap 'rm -rf "$T"' EXIT
` + opsPutFileFunc + `
put_file "$T/sub/dir/f.conf" 644 '` + enc("one\n") + `'; echo "STATE first=$PUT_CHANGED $(cat "$T/sub/dir/f.conf")"
PUT_CHANGED=0
put_file "$T/sub/dir/f.conf" 644 '` + enc("one\n") + `'; echo "STATE same=$PUT_CHANGED"
put_file "$T/sub/dir/f.conf" 644 '` + enc("two\n") + `'; echo "STATE changed=$PUT_CHANGED $(cat "$T/sub/dir/f.conf")"
echo "STATE leftovers=$(ls -A "$T/sub/dir" | grep -c -v '^f.conf$' || true)"
if put_file "$T/sub/dir/bad" 600 '!!!not base64!!!'; then echo "STATE bad=accepted"; else echo "STATE bad=rejected"; fi
echo "STATE leftovers_after_bad=$(ls -A "$T/sub/dir" | grep -c -v '^f.conf$' || true)"
`
	out, exit := runBash(t, script)
	if exit != 0 {
		t.Fatalf("exit %d:\n%s", exit, out)
	}
	for _, l := range []string{"STATE first=1 one", "STATE same=0", "STATE changed=1 two", "STATE leftovers=0", "STATE bad=rejected", "STATE leftovers_after_bad=0"} {
		if !hasLine(out, l) {
			t.Errorf("missing %q:\n%s", l, out)
		}
	}
	if !strings.Contains(out, "[ops] unchanged") {
		t.Errorf("an identical re-write must say unchanged:\n%s", out)
	}
}

// Every rendered script must at least parse under bash.
func TestOpsScriptsParse(t *testing.T) {
	if !haveBash() {
		t.Skip("no working bash")
	}
	auth, err := backupAuthorizedKeysLine("ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIOMqqnkVzrm0SdG6UOoqKLsabgH5C9okWi0dh2l9GKJl heyearth-backup@hub")
	if err != nil {
		t.Fatal(err)
	}
	scripts := map[string]string{
		"backup.sh":            backupScript,
		"backup-verify.sh":     backupVerifyScript,
		"receiver":             renderBackupReceiver(),
		"backup install":       renderBackupInstallScript(backupConfig{"192.0.2.126", 22, testAgeRecipient}),
		"backup install local": renderBackupInstallScript(backupConfig{port: 22}),
		"receiver install":     renderReceiverInstallScript(auth),
		"known_hosts + probe":  renderKnownHostsScript("192.0.2.126", 2222, "ssh-ed25519", "AAAAC3NzaC1lZDI1NTE5AAAAIOMqqnkVzrm0SdG6UOoqKLsabgH5C9okWi0dh2l9GKJl"),
		"journald + fail2ban":  renderHostHygieneScript(22, "192.0.2.126"),
		"nginx patch":          renderNginxStreamPatchScript(),
		"egress guard":         renderEgressGuardScript(),
		"egress guard install": renderEgressGuardInstallScript(),
		"read backup.env":      readBackupEnvScript,
		"prune firewall":       pruneFirewallScript,
	}
	for name, s := range scripts {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if out, exit := runBashStdin(t, s, "-n"); exit != 0 {
				t.Fatalf("bash -n failed (exit %d): %s", exit, out)
			}
		})
	}
	// The scripts are shipped through `bash -c <quoted>`: the quoted form must not exceed what a
	// single argument may carry (128 KiB on Linux).
	for name, s := range scripts {
		if n := len(shQuote(s)); n > 100_000 {
			t.Errorf("%s: %d bytes as one argument", name, n)
		}
	}
}

func TestKnownHostsName(t *testing.T) {
	if got := knownHostsName("192.0.2.126", 22); got != "192.0.2.126" {
		t.Errorf("port 22: %q", got)
	}
	if got := knownHostsName("192.0.2.126", 2222); got != "[192.0.2.126]:2222" {
		t.Errorf("port 2222: %q", got)
	}
	cmd := backupProbeCommand("192.0.2.126", 2222)
	for _, want := range []string{"-i /root/.ssh/heyearth_backup_ed25519", "-p 2222", "BatchMode=yes", "StrictHostKeyChecking=yes", "root@192.0.2.126 ping"} {
		if !strings.Contains(cmd, want) {
			t.Errorf("probe command lacks %q: %s", want, cmd)
		}
	}
}
