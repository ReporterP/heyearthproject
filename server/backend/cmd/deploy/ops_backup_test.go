package main

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"os/exec"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// The scripts under test are executed by a real bash, through the same transport as production
// (runSSHBash: an outer shell runs `bash -c '<shQuote(script)>'`), against stub functions that
// replace runuser / ssh / sleep. Everything happens in a mktemp directory created by the
// harness itself; nothing outside it is touched. Tests skip when there is no working bash.

// runBash runs script the way production does: the remote login shell receives the single
// command line `bash -c '<shQuote(script)>'` and parses it. Here that command line is fed to an
// outer bash on stdin (no argv quoting or size limits of the host OS in the way), so a quoting
// bug in shQuote shows up as a failing script. Returns stdout+stderr and the exit status.
func runBash(t *testing.T, script string) (string, int) {
	t.Helper()
	return runBashStdin(t, "bash -c "+shQuote(script)+"\n")
}

// runBashStdin feeds script on stdin (no argv size limit) — used for syntax checks and for
// scripts that must be run with extra environment.
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
  case "$cmd" in
    pg_dump) if [ "$PGDUMP_FAIL" = 1 ]; then echo "pg_dump: stub failure" >&2; return 1; fi
             if [ "$PGDUMP_EMPTY" = 1 ]; then return 0; fi
             printf 'PGDMP-stub-%s' "$*" ;;
    psql)    if [ "$PSQL_FAIL" = 1 ]; then return 1; fi; echo "$USERS_STUB" ;;
    *) echo "STUB-BAD-RUNUSER $cmd" >&2; return 99 ;;
  esac
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
echo "STATE ssh_calls=$(cat "$T/ssh.args" 2>/dev/null | wc -l | tr -d ' ')"
echo "STATE ssh_args=$(sed -n 1p "$T/ssh.args" 2>/dev/null)"
newest=$(ls -1t "$B"/backup-*.tar.gz 2>/dev/null | sed -n 1p)
if [ -n "$newest" ]; then
  echo "STATE members=$(tar -tzf "$newest" | sort | tr '\n' ' ')"
  echo "STATE meta=$(tar -xzOf "$newest" meta.txt | grep users_count)"
  echo "STATE files=$(tar -xzOf "$newest" files.tar.gz | tar -tz | grep -v '/$' | sort | tr '\n' ' ')"
  echo "STATE dump=$(tar -xzOf "$newest" db.dump | head -c 30)"
  echo "STATE received_equal=$([ -f "$T/received" ] && cmp -s "$newest" "$T/received" && echo 1 || echo 0)"
fi
`

// backupHarness builds the bash program for one case: env file, fake config tree, stubs, an
// optional pre-step, then the script itself sourced in a subshell.
func backupHarness(vars map[string]string, pre string) string {
	base := map[string]string{
		"KEEP": "14", "TARGET_HOST": "203.0.113.7", "PGDUMP_FAIL": "0", "PGDUMP_EMPTY": "0",
		"PSQL_FAIL": "0", "USERS_STUB": "42", "SSH_MODE": "ok",
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
printf 'BACKUP_DIR=%s\nKEEP=%s\nTARGET_HOST=%s\nTARGET_PORT=22\nSSH_KEY=%s\nFILES_ROOT=%s\n' \
  "$T/backups" "$KEEP" "$TARGET_HOST" "$T/key" "$T/root" > "$T/backup.env"
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
			name: "success: archive with db, meta and config files, copied off-box and verified",
			want: []string{
				"STATE exit=0", "STATE archives=1", "STATE last_success=1", "STATE last_offsite=1", "STATE last_error=0",
				"STATE stray=0", "STATE ssh_calls=1", "STATE received_equal=1",
				"STATE members=db.dump files.tar.gz meta.txt ",
				"STATE meta=users_count=42",
				"STATE files=etc/letsencrypt/cert.pem etc/nginx/stream-enabled/heyearth opt/heyearth/mainserver/api.env opt/heyearth/mainserver/certs/ca-cert.pem opt/heyearth/state/reality-keys.json ",
			},
			wantSub: []string{"local: ", "users=42)", "offsite: 203.0.113.7 OK (sha256 ", "BatchMode=yes", "StrictHostKeyChecking=yes", "root@203.0.113.7 backup-"},
			notSub:  []string{"ERROR", "WARN", "STUB-BAD"},
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
			want: []string{"STATE exit=0", "STATE last_offsite=1", "STATE last_error=0", "STATE ssh_calls=3", "STATE received_equal=1"},
		},
		{
			name:    "no TARGET_HOST: local backup only, warning, exit 0, no ssh",
			vars:    map[string]string{"TARGET_HOST": ""},
			want:    []string{"STATE exit=0", "STATE archives=1", "STATE last_success=1", "STATE last_offsite=0", "STATE last_error=0", "STATE ssh_calls=0"},
			wantSub: []string{"WARN: no TARGET_HOST, offsite skipped"},
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

func TestBackupScriptRotation(t *testing.T) {
	if !haveBash() {
		t.Skip("no working bash")
	}
	pre := `for d in 01 02 03 04; do echo old > "$T/backups/backup-202601${d}-000000.tar.gz"; touch -d "2026-01-${d} 00:00:00" "$T/backups/backup-202601${d}-000000.tar.gz"; done
echo keepme > "$T/backups/notes.txt"
`
	out, _ := runBash(t, backupHarness(map[string]string{"KEEP": "3", "TARGET_HOST": ""}, pre)+`echo "STATE notes=$(cat "$T/backups/notes.txt")"`+"\n")
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
( . "$T/verify.sh" "$T/backup-20261012-040000.tar.gz" )
rc=$?
echo "STATE exit=$rc"
echo "STATE dropdb_calls=$(grep -c '^dropdb' "$T/calls")"
echo "STATE last_call=$(tail -1 "$T/calls" | cut -d' ' -f1)"
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
}

// ───────────────────────── heyearth-backup-receive (runs on the exit) ─────────────────────────

// receiverForTest swaps the fixed directory / retention of the receiver for test values. The
// production text has no environment hooks: the receiver is a security boundary.
func receiverForTest(t *testing.T, keep int) string {
	t.Helper()
	const fixed = "DIR=/var/backups/heyearth-hub; KEEP=7;"
	if !strings.Contains(backupReceiverScript, fixed) {
		t.Fatalf("receiver no longer contains %q — update the test", fixed)
	}
	return strings.Replace(renderBackupReceiver(), fixed, fmt.Sprintf(`DIR="$T/store"; KEEP=%d;`, keep), 1)
}

func receiverHarness(t *testing.T, keep int, body string) string {
	return `
T=$(mktemp -d); W=$(mktemp -d); trap 'rm -rf "$T" "$W"' EXIT
cat > "$T/receive.sh" <<'RECEIVER_EOF'
` + receiverForTest(t, keep) + `RECEIVER_EOF
export T
echo "payload of a backup" > "$W/db.dump"; tar -C "$W" -czf "$T/good.tar.gz" db.dump
head -c 60 "$T/good.tar.gz" > "$T/truncated.tar.gz"
recv() { SSH_ORIGINAL_COMMAND="$1" bash "$T/receive.sh" < "$2"; }
` + body
}

func TestBackupReceiver(t *testing.T) {
	if !haveBash() {
		t.Skip("no working bash")
	}
	version := backupReceiverVersion()

	t.Run("ping answers with the receiver version and reads nothing", func(t *testing.T) {
		t.Parallel()
		out, _ := runBash(t, receiverHarness(t, 7, `recv ping /dev/null; echo "STATE exit=$?"; echo "STATE stored=$(ls -A "$T/store" 2>/dev/null | wc -l | tr -d ' ')"`))
		if !hasLine(out, "PONG "+version) || !hasLine(out, "STATE exit=0") || !hasLine(out, "STATE stored=0") {
			t.Fatalf("ping:\n%s", out)
		}
	})

	t.Run("a valid name stores the stream atomically and answers with its sha256", func(t *testing.T) {
		t.Parallel()
		out, _ := runBash(t, receiverHarness(t, 7, `recv backup-20261012-040000.tar.gz "$T/good.tar.gz"; echo "STATE exit=$?"
echo "STATE sum_ok=$([ "$(sha256sum "$T/store/backup-20261012-040000.tar.gz" | cut -d' ' -f1)" = "$(sha256sum "$T/good.tar.gz" | cut -d' ' -f1)" ] && echo 1 || echo 0)"
echo "STATE files=$(ls -A "$T/store" | tr '\n' ' ')"`))
		if !hasLine(out, "STATE exit=0") || !hasLine(out, "STATE sum_ok=1") || !hasLine(out, "STATE files=backup-20261012-040000.tar.gz ") {
			t.Fatalf("store:\n%s", out)
		}
		if !regexp.MustCompile(`(?m)^OK [0-9a-f]{64}\r?$`).MatchString(out) {
			t.Fatalf("no 'OK <sha256>' answer:\n%s", out)
		}
	})

	t.Run("anything but ping or a backup-YYYYmmdd-HHMMSS.tar.gz name is rejected and stores nothing", func(t *testing.T) {
		t.Parallel()
		out, _ := runBash(t, receiverHarness(t, 7, `for n in '../evil.tar.gz' 'backup-2026-1.tar.gz' 'backup-20261012-040000.tar.gz; id' 'backup-20261012-040000.tar.gz.part' 'x/backup-20261012-040000.tar.gz' 'ping ' 'scp -t /root' 'bash' ''; do
  recv "$n" "$T/good.tar.gz" >/dev/null 2>&1; echo "STATE rc[$n]=$?"
done
echo "STATE stored=$(ls -A "$T/store" 2>/dev/null | wc -l | tr -d ' ')"; echo "STATE outside=$(ls "$T" | grep -c evil)"`))
		for _, n := range []string{"../evil.tar.gz", "backup-2026-1.tar.gz", "backup-20261012-040000.tar.gz; id", "backup-20261012-040000.tar.gz.part", "x/backup-20261012-040000.tar.gz", "ping ", "scp -t /root", "bash", ""} {
			if !hasLine(out, fmt.Sprintf("STATE rc[%s]=2", n)) {
				t.Errorf("name %q must be rejected with status 2:\n%s", n, out)
			}
		}
		if !hasLine(out, "STATE stored=0") || !hasLine(out, "STATE outside=0") {
			t.Errorf("a rejected name left something behind:\n%s", out)
		}
	})

	t.Run("a truncated gzip stream is rejected: no file, no .part", func(t *testing.T) {
		t.Parallel()
		out, _ := runBash(t, receiverHarness(t, 7, `recv backup-20261012-040000.tar.gz "$T/truncated.tar.gz" 2>&1; echo "STATE exit=$?"; echo "STATE stored=$(ls -A "$T/store" | wc -l | tr -d ' ')"`))
		if !hasLine(out, "STATE exit=3") || !hasLine(out, "STATE stored=0") || !strings.Contains(out, "rejected: truncated or corrupt gzip stream") {
			t.Fatalf("truncated:\n%s", out)
		}
	})

	t.Run("a corrupt re-upload does not replace the good archive of the same name", func(t *testing.T) {
		t.Parallel()
		out, _ := runBash(t, receiverHarness(t, 7, `recv backup-20261012-040000.tar.gz "$T/good.tar.gz" >/dev/null
recv backup-20261012-040000.tar.gz "$T/truncated.tar.gz" >/dev/null 2>&1; echo "STATE exit=$?"
echo "STATE still_good=$(gzip -t "$T/store/backup-20261012-040000.tar.gz" && echo 1 || echo 0)"`))
		if !hasLine(out, "STATE exit=3") || !hasLine(out, "STATE still_good=1") {
			t.Fatalf("re-upload:\n%s", out)
		}
	})

	t.Run("keeps the newest KEEP archives", func(t *testing.T) {
		t.Parallel()
		out, _ := runBash(t, receiverHarness(t, 3, `mkdir -p "$T/store"
for d in 01 02 03 04 05; do cp "$T/good.tar.gz" "$T/store/backup-202610${d}-040000.tar.gz"; touch -d "2026-10-${d} 04:00:00" "$T/store/backup-202610${d}-040000.tar.gz"; done
recv backup-20261012-040000.tar.gz "$T/good.tar.gz" >/dev/null; echo "STATE exit=$?"
echo "STATE kept=$(ls "$T/store" | sed 's/backup-//; s/-040000.tar.gz//' | tr '\n' ' ')"`))
		if !hasLine(out, "STATE exit=0") || !hasLine(out, "STATE kept=20261004 20261005 20261012 ") {
			t.Fatalf("rotation:\n%s", out)
		}
	})
}

// ───────────────────────── Go-side rendering ─────────────────────────

func TestValidateBackupTarget(t *testing.T) {
	for _, tc := range []struct {
		target string
		port   int
		ok     bool
	}{
		{"", 22, true}, {"89.124.89.126", 22, true}, {"2001:db8::1", 2222, true},
		{"89.124.89.126; rm -rf /", 22, false}, {"host.example.com", 22, false}, {"1.2.3", 22, false},
		{"89.124.89.126", 0, false}, {"89.124.89.126", 70000, false}, {"", 0, false},
	} {
		if err := validateBackupTarget(tc.target, tc.port); (err == nil) != tc.ok {
			t.Errorf("validateBackupTarget(%q, %d) = %v, want ok=%v", tc.target, tc.port, err, tc.ok)
		}
	}
}

func TestParseBackupEnvTarget(t *testing.T) {
	for _, tc := range []struct {
		in         string
		wantTarget string
		wantPort   int
	}{
		{"TARGET_HOST=89.124.89.126\nTARGET_PORT=2222\n", "89.124.89.126", 2222},
		{"TARGET_HOST=89.124.89.126\n", "89.124.89.126", 22},
		{"", "", 22},
		{"TARGET_HOST=\nTARGET_PORT=22\n", "", 22},
		{"TARGET_HOST=$(reboot)\nTARGET_PORT=99999\n", "", 22},
	} {
		gotT, gotP := parseBackupEnvTarget(tc.in, 22)
		if gotT != tc.wantTarget || gotP != tc.wantPort {
			t.Errorf("parseBackupEnvTarget(%q) = %q, %d; want %q, %d", tc.in, gotT, gotP, tc.wantTarget, tc.wantPort)
		}
	}
}

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

func TestRenderBackupInstallScript(t *testing.T) {
	s := renderBackupInstallScript("89.124.89.126", 2222)
	// payloads round-trip through base64
	payloads := map[string]string{
		"/etc/heyearth/backup.env":                    renderBackupEnv("89.124.89.126", 2222),
		"/opt/heyearth/bin/heyearth-backup.sh":        backupScript,
		"/opt/heyearth/bin/heyearth-backup-verify.sh": backupVerifyScript,
		"/etc/systemd/system/heyearth-backup.service": backupServiceUnit,
		"/etc/systemd/system/heyearth-backup.timer":   backupTimerUnit,
	}
	for path, content := range payloads {
		want := fmt.Sprintf("put_file %s ", path)
		i := strings.Index(s, want)
		if i < 0 {
			t.Errorf("no put_file for %s", path)
			continue
		}
		m := regexp.MustCompile(`'([A-Za-z0-9+/=]*)'`).FindStringSubmatch(s[i:])
		got, err := base64.StdEncoding.DecodeString(m[1])
		if err != nil || string(got) != content {
			t.Errorf("payload of %s does not round-trip (%v)", path, err)
		}
	}
	env := renderBackupEnv("89.124.89.126", 2222)
	for _, want := range []string{"TARGET_HOST=89.124.89.126\n", "TARGET_PORT=2222\n", "KEEP=14\n", "SSH_KEY=/root/.ssh/heyearth_backup_ed25519\n", "BACKUP_DIR=/opt/heyearth/backups\n"} {
		if !strings.Contains(env, want) {
			t.Errorf("backup.env lacks %q", want)
		}
	}
	// the calendar expression of the timer is validated by systemd itself before any write
	if a, b := strings.Index(s, `systemd-analyze calendar "`+backupOnCalendar+`"`), strings.Index(s, "put_file /etc/heyearth/backup.env"); a < 0 || b < 0 || a > b {
		t.Errorf("the OnCalendar check must come before the first put_file")
	}
	// the tool check precedes every write
	if a, b := strings.Index(s, "is not installed"), strings.Index(s, "put_file /etc/heyearth/backup.env"); a < 0 || b < 0 || a > b {
		t.Errorf("the prerequisite check must come before the first put_file")
	}
	if !strings.Contains(backupTimerUnit, "OnCalendar=*-*-* 04:00:00 Europe/Moscow") || !strings.Contains(backupTimerUnit, "Persistent=true") {
		t.Errorf("timer: %q", backupTimerUnit)
	}
}

// The install script must refuse to touch the host when a tool is missing — run with the tool
// check failing and every mutating command replaced by a trap, so the test cannot write to /etc
// even when it runs as root on a Linux CI host.
func TestBackupInstallAbortsBeforeWritingWhenAToolIsMissing(t *testing.T) {
	if !haveBash() {
		t.Skip("no working bash")
	}
	guard := `
command() { if [ "$1" = -v ] && [ "$2" = pg_dump ]; then return 1; fi; builtin command "$@"; }
for f in mkdir chmod systemctl ssh-keygen mktemp mv cp tee; do eval "$f() { echo UNEXPECTED-$f \"\$@\"; exit 99; }"; done
`
	out, exit := runBash(t, guard+renderBackupInstallScript("89.124.89.126", 22))
	if exit != 1 || !strings.Contains(out, "[ops] ABORT: pg_dump is not installed — backup timer not installed") || strings.Contains(out, "UNEXPECTED") {
		t.Fatalf("exit %d:\n%s", exit, out)
	}
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
		"backup.sh":              backupScript,
		"backup-verify.sh":       backupVerifyScript,
		"receiver":               renderBackupReceiver(),
		"backup install":         renderBackupInstallScript("89.124.89.126", 22),
		"receiver install":       renderReceiverInstallScript(auth),
		"known_hosts + probe":    renderKnownHostsScript("89.124.89.126", 2222, "ssh-ed25519", "AAAAC3NzaC1lZDI1NTE5AAAAIOMqqnkVzrm0SdG6UOoqKLsabgH5C9okWi0dh2l9GKJl"),
		"journald + fail2ban":    renderHostHygieneScript(22, "89.124.89.126"),
		"nginx stream patch":     renderNginxStreamPatchScript(),
		"xray api guard":         xrayAPIGuardScript,
		"xray api guard install": renderXrayAPIGuardInstallScript(),
		"prune firewall":         pruneFirewallScript,
	}
	for name, s := range scripts {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if out, exit := runBashStdin(t, s, "-n"); exit != 0 {
				t.Fatalf("bash -n failed (exit %d): %s", exit, out)
			}
		})
	}
	// The exit-side scripts and the Hub scripts are shipped through `bash -c <quoted>`: the
	// quoted form must not exceed what a single argument may carry (128 KiB on Linux).
	for name, s := range scripts {
		if n := len(shQuote(s)); n > 100_000 {
			t.Errorf("%s: %d bytes as one argument", name, n)
		}
	}
}

func TestKnownHostsName(t *testing.T) {
	if got := knownHostsName("89.124.89.126", 22); got != "89.124.89.126" {
		t.Errorf("port 22: %q", got)
	}
	if got := knownHostsName("89.124.89.126", 2222); got != "[89.124.89.126]:2222" {
		t.Errorf("port 2222: %q", got)
	}
	cmd := backupProbeCommand("89.124.89.126", 2222)
	for _, want := range []string{"-i /root/.ssh/heyearth_backup_ed25519", "-p 2222", "BatchMode=yes", "StrictHostKeyChecking=yes", "root@89.124.89.126 ping"} {
		if !strings.Contains(cmd, want) {
			t.Errorf("probe command lacks %q: %s", want, cmd)
		}
	}
}
