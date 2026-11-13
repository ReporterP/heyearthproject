package main

import (
	"io/fs"
	"strings"
	"testing"
	"testing/fstest"
	"time"
)

func TestParseCheckHostTCPResult(t *testing.T) {
	ru3 := []string{"ru1.node.check-host.net", "ru2.node.check-host.net", "ru3.node.check-host.net"}
	cases := []struct {
		name, body string
		requested  []string
		tally      checkHostTally
		verdict    string
	}{
		{"one node ok", `{"ru1.node.check-host.net":[{"time":0.05,"address":"203.0.113.225"}],"ru2.node.check-host.net":[{"error":"Connection timed out"}],"ru3.node.check-host.net":[{"error":"Connection timed out"}]}`,
			ru3, checkHostTally{OK: 1, Failed: 2}, "reachable"},
		{"all failed", `{"ru1.node.check-host.net":[{"error":"Connection timed out"}],"ru2.node.check-host.net":[{"error":"Connection refused"}]}`,
			ru3[:2], checkHostTally{Failed: 2}, "unreachable"},
		{"pending", `{"ru1.node.check-host.net":[{"error":"Connection timed out"}],"ru2.node.check-host.net":null}`,
			ru3[:2], checkHostTally{Failed: 1, Pending: 1}, "unknown"},
		{"empty", `{}`, ru3, checkHostTally{Pending: 3}, "unknown"},
		{"requested 3, response has only 2 fails", `{"ru1.node.check-host.net":[{"error":"Connection timed out"}],"ru2.node.check-host.net":[{"error":"Connection timed out"}]}`,
			ru3, checkHostTally{Failed: 2, Pending: 1}, "unknown"},
		{"extra unrequested failing node is ignored", `{"ru1.node.check-host.net":[{"time":0.05}],"ru9.node.check-host.net":[{"error":"Connection timed out"}]}`,
			ru3[:1], checkHostTally{OK: 1}, "reachable"},
		{"extra unrequested ok node cannot hide an outage", `{"ru1.node.check-host.net":[{"error":"Connection timed out"}],"de1.node.check-host.net":[{"time":0.02}]}`,
			ru3[:1], checkHostTally{Failed: 1}, "unreachable"},
		{"only unrequested nodes answered", `{"de1.node.check-host.net":[{"error":"Connection timed out"}]}`,
			ru3[:1], checkHostTally{Pending: 1}, "unknown"},
		{"duplicate requested node counts once", `{"ru1.node.check-host.net":[{"error":"Connection timed out"}]}`,
			[]string{"ru1.node.check-host.net", "ru1.node.check-host.net"}, checkHostTally{Failed: 1}, "unreachable"},
	}
	for _, tc := range cases {
		tally, err := parseCheckHostTCPResult([]byte(tc.body), tc.requested)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if tally != tc.tally {
			t.Fatalf("%s: tally %+v, want %+v", tc.name, tally, tc.tally)
		}
		if got := tally.verdict(); got != tc.verdict {
			t.Fatalf("%s: verdict %s (%+v), want %s", tc.name, got, tally, tc.verdict)
		}
	}
	if _, err := parseCheckHostTCPResult([]byte(`<html>rate limited</html>`), ru3); err == nil {
		t.Fatal("non-JSON body must be an error (not counted as a failure)")
	}
}

func TestNewestBackupModTime(t *testing.T) {
	base := time.Date(2026, 10, 12, 3, 0, 0, 0, time.UTC)
	at := func(h int) time.Time { return base.Add(time.Duration(h) * time.Hour) }
	list := func(t *testing.T, fsys fstest.MapFS) []fs.DirEntry {
		t.Helper()
		entries, err := fs.ReadDir(fsys, ".")
		if err != nil {
			t.Fatal(err)
		}
		return entries
	}

	valid := fstest.MapFS{
		"backup-20261012-030000.tar.gz": {Data: []byte("old"), ModTime: at(0)},
		"backup-20261012-060000.tar.gz": {Data: []byte("new"), ModTime: at(3)},
		"backup-20261012-040000.tar.gz": {Data: []byte("mid"), ModTime: at(1)},
	}
	if got := newestBackupModTime(list(t, valid)); !got.Equal(at(3)) {
		t.Fatalf("newest valid file must win: got %v, want %v", got, at(3))
	}

	noise := fstest.MapFS{
		"backup-20261012-030000.tar.gz": {Data: []byte("ok"), ModTime: at(0)},
		// newer, but must not count:
		"20261012-090000":                      {Mode: fs.ModeDir | 0o755, ModTime: at(6)},  // doBackup temp dir
		"lost+found":                           {Mode: fs.ModeDir | 0o700, ModTime: at(7)},  // fs root of a mount
		"backup-20261012-100000.tar.gz.":       {Data: []byte("x"), ModTime: at(8)},         // wrong suffix
		"backup-20261012-110000.tar":           {Data: []byte("x"), ModTime: at(9)},         // wrong suffix
		"db-20261012-120000.tar.gz":            {Data: []byte("x"), ModTime: at(10)},        // wrong prefix
		"backup-20261012-130000.tar.gz.part":   {Data: []byte("x"), ModTime: at(11)},        // partial upload
		"backup-20261012-140000.tar.gz":        {Data: nil, ModTime: at(12)},                // empty file
		"backup-20261012-150000.tar.gz.d":      {Mode: fs.ModeDir | 0o755, ModTime: at(13)}, // dir with a lookalike name
		"nested":                               {Mode: fs.ModeDir | 0o755, ModTime: at(14)},
		"nested/backup-20261012-160000.tar.gz": {Data: []byte("x"), ModTime: at(15)}, // not in the top directory
	}
	if got := newestBackupModTime(list(t, noise)); !got.Equal(at(0)) {
		t.Fatalf("only the regular non-empty backup-*.tar.gz may count: got %v, want %v", got, at(0))
	}

	// A directory named exactly like a backup, and an empty file: nothing valid at all.
	none := fstest.MapFS{
		"backup-20261012-030000.tar.gz": {Mode: fs.ModeDir | 0o755, ModTime: at(0)},
		"backup-20261012-040000.tar.gz": {Data: []byte{}, ModTime: at(1)},
	}
	if got := newestBackupModTime(list(t, none)); !got.IsZero() {
		t.Fatalf("dir/empty file named like a backup must not count: got %v", got)
	}
	if got := newestBackupModTime(nil); !got.IsZero() {
		t.Fatalf("empty listing: got %v, want zero", got)
	}
}

func TestLoopProbeStreak(t *testing.T) {
	t0 := time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC)
	var s loopProbeStreak
	for i, want := range []int{1, 2, 3} {
		if got := s.record(t0.Add(time.Duration(i)*time.Minute), false); got != want {
			t.Fatalf("consecutive failure #%d: streak %d, want %d", i+1, got, want)
		}
	}
	if got := s.record(t0.Add(3*time.Minute), true); got != 0 {
		t.Fatalf("success must reset the streak: %d", got)
	}
	if got := s.record(t0.Add(4*time.Minute), false); got != 1 {
		t.Fatalf("failure after success: streak %d, want 1", got)
	}
	if got := s.record(t0.Add(5*time.Minute), false); got != 2 {
		t.Fatalf("streak %d, want 2", got)
	}
	// Probes skipped for longer than loopProbeStaleAfter: old failures no longer count.
	if got := s.record(t0.Add(5*time.Minute+loopProbeStaleAfter+time.Second), false); got != 1 {
		t.Fatalf("failure after a long gap must start a new streak: %d, want 1", got)
	}
	// A gap of exactly loopProbeStaleAfter is still "consecutive".
	last := t0.Add(5*time.Minute + loopProbeStaleAfter + time.Second)
	if got := s.record(last.Add(loopProbeStaleAfter), false); got != 2 {
		t.Fatalf("gap == stale limit keeps the streak: %d, want 2", got)
	}
}

func TestLoopProbeStreakRecovered(t *testing.T) {
	t0 := time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC)
	var s loopProbeStreak
	s.record(t0, false)
	s.record(t0.Add(time.Minute), true)
	if s.recovered() {
		t.Fatal("one success after a failure is not a recovery")
	}
	s.record(t0.Add(2*time.Minute), false)
	s.record(t0.Add(3*time.Minute), true)
	if s.recovered() {
		t.Fatal("a failure in between starts the count over")
	}
	s.record(t0.Add(4*time.Minute), true)
	if !s.recovered() {
		t.Fatal("two successes in a row are a recovery")
	}
	// a stale gap breaks the success streak as well
	var g loopProbeStreak
	g.record(t0, true)
	g.record(t0.Add(loopProbeStaleAfter+time.Second), true)
	if g.recovered() {
		t.Fatal("successes separated by a stale gap are not consecutive")
	}
}

type checkHostRecorder struct{ events []string }

func (r *checkHostRecorder) raise(key string, level AlertLevel, _, _ string) {
	r.events = append(r.events, "raise "+key+" "+string(level))
}
func (r *checkHostRecorder) resolve(key, _ string) { r.events = append(r.events, "resolve "+key) }

func TestCheckHostVerdictStreaks(t *testing.T) {
	t0 := time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC)
	tally := checkHostTally{OK: 1, Failed: 2}
	run := func(st *loopProbeStreak, r *checkHostRecorder, at time.Duration, verdict string) string {
		r.events = nil
		checkHostVerdict(st, t0.Add(at), verdict, "hiearth.ru", "https://check-host.net/x", tally, r.raise, r.resolve)
		return strings.Join(r.events, ",")
	}
	const raise, resolve = "raise entry-unreachable-ru warn", "resolve entry-unreachable-ru"

	var st loopProbeStreak
	r := &checkHostRecorder{}
	if got := run(&st, r, 0, "unreachable"); got != "" {
		t.Fatalf("one failure: %q", got)
	}
	if got := run(&st, r, 15*time.Minute, "unknown"); got != "" {
		t.Fatalf("unknown is not counted: %q", got)
	}
	if got := run(&st, r, 30*time.Minute, "unreachable"); got != raise {
		t.Fatalf("two failures with one inconclusive run between must raise: %q", got)
	}
	if got := run(&st, r, 45*time.Minute, "reachable"); got != "" {
		t.Fatalf("a single reachable verdict must not resolve: %q", got)
	}
	if got := run(&st, r, 60*time.Minute, "reachable"); got != resolve {
		t.Fatalf("two reachable verdicts in a row must resolve: %q", got)
	}

	// M-4: failures hours apart (check-host unavailable in between) are not "consecutive"
	var stale loopProbeStreak
	run(&stale, r, 0, "unreachable")
	if got := run(&stale, r, checkHostStaleAfter+time.Minute, "unreachable"); got != "" {
		t.Fatalf("failures separated by more than %v must start over: %q", checkHostStaleAfter, got)
	}
	if got := run(&stale, r, checkHostStaleAfter+16*time.Minute, "unreachable"); got != raise {
		t.Fatalf("then two in a row raise again: %q", got)
	}
}

func TestLogGate(t *testing.T) {
	t0 := time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC)
	var g logGate
	if !g.allow(t0, time.Hour) {
		t.Fatal("first call must pass")
	}
	if g.allow(t0.Add(59*time.Minute), time.Hour) {
		t.Fatal("call inside the interval must be suppressed")
	}
	if !g.allow(t0.Add(time.Hour), time.Hour) {
		t.Fatal("call after the interval must pass")
	}
}

func TestCertExpiryAlert(t *testing.T) {
	now := time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC)

	if _, _, _, alert := certExpiryAlert("hiearth.ru", now.Add(30*24*time.Hour), now); alert {
		t.Fatal("30 days left must not alert")
	}
	if _, _, _, alert := certExpiryAlert("hiearth.ru", now.Add(certWarnDays*24*time.Hour), now); alert {
		t.Fatal("exactly certWarnDays left must not alert")
	}

	level, title, details, alert := certExpiryAlert("hiearth.ru", now.Add(5*24*time.Hour+time.Hour), now)
	if !alert || level != AlertWarn || !strings.Contains(details, "осталось 5 дн.") {
		t.Fatalf("5 days left: alert=%v level=%s details=%q", alert, level, details)
	}
	if title != "Сертификат сайта скоро истечёт" {
		t.Fatalf("expiring cert title: %q", title)
	}

	level, title, details, alert = certExpiryAlert("hiearth.ru", now.Add(-48*time.Hour), now)
	if !alert || level != AlertCritical {
		t.Fatalf("expired cert: alert=%v level=%s, want critical", alert, level)
	}
	if !strings.Contains(title, "истёк") || !strings.Contains(details, "истёк") || strings.Contains(details, "осталось") {
		t.Fatalf("expired cert needs its own wording: title=%q details=%q", title, details)
	}
	// Expired by a few seconds is still expired (and must not print "осталось -0 дн.").
	if level, _, details, alert = certExpiryAlert("hiearth.ru", now.Add(-time.Second), now); !alert || level != AlertCritical || strings.Contains(details, "осталось") {
		t.Fatalf("just expired: alert=%v level=%s details=%q", alert, level, details)
	}
}

func TestSafelyRecoversPanicAndContinues(t *testing.T) {
	ran := false
	safely("panicking step", func() { panic("boom") })
	safely("next step", func() { ran = true })
	if !ran {
		t.Fatal("a panic in one step must not prevent the next one")
	}
}

func TestOffsiteConfigured(t *testing.T) {
	for _, tc := range []struct {
		env  string
		want bool
	}{
		{"BACKUP_DIR=/x\nKEEP=14\nTARGET_HOST=192.0.2.126\nTARGET_PORT=22\n", true},
		{"TARGET_HOST=192.0.2.126", true},
		{"  TARGET_HOST=2001:db8::1  \n", true},
		{"BACKUP_DIR=/x\nTARGET_HOST=\nTARGET_PORT=22\n", false},
		{"TARGET_HOST=   \n", false},
		{"BACKUP_DIR=/x\nKEEP=14\n", false},
		{"", false},
		{"# TARGET_HOST=1.2.3.4\n", false},
		{"XTARGET_HOST=1.2.3.4\n", false},
	} {
		if got := offsiteConfigured(tc.env); got != tc.want {
			t.Errorf("offsiteConfigured(%q) = %v, want %v", tc.env, got, tc.want)
		}
	}
}

func TestDecideOffsiteBackup(t *testing.T) {
	now := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	h := func(back int) time.Time { return now.Add(-time.Duration(back) * time.Hour) }
	var never time.Time
	cases := []struct {
		name       string
		configured bool
		stamp, env time.Time
		want       offsiteVerdict
		wantAge    time.Duration
	}{
		{"offsite not configured: nothing to watch, however old the stamp", false, h(500), h(600), offsiteNotConfigured, 0},
		{"fresh copy", true, h(3), h(2000), offsiteFresh, 3 * time.Hour},
		{"exactly at the threshold is still fine", true, h(26), h(2000), offsiteFresh, 26 * time.Hour},
		{"a minute over the threshold is stale", true, h(26).Add(-time.Minute), h(2000), offsiteStale, 26*time.Hour + time.Minute},
		{"stale for days", true, h(100), h(2000), offsiteStale, 100 * time.Hour},
		{"configured long ago, no copy ever: stale", true, never, h(48), offsiteStale, 48 * time.Hour},
		{"configured a moment ago, no copy yet: grace period, not stale", true, never, h(1), offsiteFresh, time.Hour},
		{"re-pointed at another exit after the last copy: the period restarts", true, h(40), h(2), offsiteFresh, 2 * time.Hour},
		{"re-pointed long ago and still no new copy: stale from the re-pointing", true, h(90), h(30), offsiteStale, 30 * time.Hour},
		{"backup.env older than the stamp: the stamp counts", true, h(5), h(300), offsiteFresh, 5 * time.Hour},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, age := decideOffsiteBackup(c.configured, c.stamp, c.env, now)
			if got != c.want || age != c.wantAge {
				t.Fatalf("got (%v, %v), want (%v, %v)", got, age, c.want, c.wantAge)
			}
		})
	}
	if offsiteMaxAge != backupMaxAge {
		t.Errorf("the offsite threshold must equal the local backup threshold (26 h), got %v", offsiteMaxAge)
	}
}

func TestCheckHostVerdictForRelayKey(t *testing.T) {
	t0 := time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC)
	var st loopProbeStreak
	r := &checkHostRecorder{}
	for i, v := range []string{"unreachable", "unreachable", "reachable", "reachable"} {
		checkHostVerdictFor(&st, t0.Add(time.Duration(i)*15*time.Minute), v, "relay-5-unreachable-ru", "Резервный вход недоступен из РФ",
			"203.0.113.50", "https://check-host.net/x", checkHostTally{Failed: 3}, r.raise, r.resolve)
	}
	if got := strings.Join(r.events, ","); got != "raise relay-5-unreachable-ru warn,resolve relay-5-unreachable-ru" {
		t.Fatalf("events %q", got)
	}
}
