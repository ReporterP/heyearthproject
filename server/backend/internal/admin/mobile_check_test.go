package admin

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseMobileCheckLog(t *testing.T) {
	log := strings.Join([]string{
		`{"kind":"page","ip":"91.79.35.101","at":1762950000}`,
		`{"kind":"blob","ip":"91.79.35.101","sent":2097152,"acked":14336,"secs":15.3,"at":1762950016}`, // frozen at 14 KB
		`{"kind":"page","ip":"94.25.171.3","at":1762950100}`,
		`{"kind":"blob","ip":"94.25.171.3","sent":2097152,"acked":2097152,"secs":1.4,"at":1762950102}`,
		`{"kind":"report","ip":"94.25.171.3","bytes":2097152,"ms":1400,"at":1762950102}`,
		`{"kind":"page","ip":"176.59.1.2","at":1762950200}`, // the page loaded, the download never started
		`not json`,
		`{"kind":"blob","at":1762950300}`, // no address: ignored
	}, "\n")
	got := parseMobileCheckLog(log)
	if len(got) != 3 {
		t.Fatalf("results %+v", got)
	}
	byIP := map[string]MobileCheckResult{}
	for _, r := range got {
		byIP[r.IP] = r
	}
	if r := byIP["94.25.171.3"]; !r.OK || r.Delivered != mobileCheckBytes || !strings.Contains(r.Verdict, "проходит") || strings.HasPrefix(r.Verdict, "✗") {
		t.Errorf("full download: %+v", r)
	}
	if r := byIP["91.79.35.101"]; r.OK || r.Delivered != 14336 || !strings.Contains(r.Verdict, "оборвалось на 14 КБ") {
		t.Errorf("frozen: %+v", r)
	}
	if r := byIP["176.59.1.2"]; r.OK || r.Delivered != 0 || !strings.Contains(r.Verdict, "не начал") {
		t.Errorf("page only: %+v", r)
	}
	if got[0].IP != "176.59.1.2" { // newest activity first
		t.Errorf("order: %s first", got[0].IP)
	}
	// The kernel took everything (sent) but the phone acknowledged little: acked decides.
	if r := parseMobileCheckLog(`{"kind":"blob","ip":"1.2.3.4","sent":2097152,"acked":5000,"secs":15,"at":1}`); r[0].OK {
		t.Error("written into the socket buffer counted as delivered")
	}
	// acked unreadable (-1): the phone's own report is used.
	r := parseMobileCheckLog(`{"kind":"blob","ip":"1.2.3.4","sent":2097152,"acked":-1,"at":1}` + "\n" + `{"kind":"report","ip":"1.2.3.4","bytes":2097152,"ms":2000,"at":2}`)
	if !r[0].OK || r[0].Seconds != 2 {
		t.Errorf("report fallback: %+v", r[0])
	}
}

func TestOperatorOf(t *testing.T) {
	for asn, want := range map[int]string{8359: "МТС", 25159: "МегаФон", 12958: "T2", 16345: "Билайн", 0: ""} {
		if got := operatorOf(asn, "X"); got != want {
			t.Errorf("AS%d: %q, want %q", asn, got, want)
		}
	}
	if got := operatorOf(48282, "VDSINA-AS - Hosting technology LTD, RU"); got != "AS48282 VDSINA-AS - Hosting technology LTD" {
		t.Errorf("other: %q", got)
	}
}

func TestMobileCheckStartScript(t *testing.T) {
	s := mobileCheckStartScript("abcd1234", 1800)
	for _, want := range []string{"for p in 80 8080 8880", "DynamicUser=yes", "AmbientCapabilities=CAP_NET_BIND_SERVICE",
		"ExecStopPost=+/usr/sbin/ufw delete allow $PORT/tcp", `"${STOP[@]}"`, "/usr/bin/timeout 1800", "check.py \"$PORT\" abcd1234 " + mobileCheckLog + " 2097152",
		"LogsDirectory=" + mobileCheckUnit, `echo "PORT=$PORT"`} {
		if !strings.Contains(s, want) {
			t.Errorf("start script lacks %q:\n%s", want, s)
		}
	}
	if strings.Contains(mobileCheckReadScript, "rm ") {
		t.Error("the read script must not delete anything")
	}
}

// The responder is valid Python (compiled when a python3 is around; skipped otherwise).
func TestMobileCheckResponderCompiles(t *testing.T) {
	py, err := exec.LookPath("python3")
	if err != nil || strings.Contains(py, "WindowsApps") {
		if py, err = exec.LookPath("py"); err != nil {
			t.Skip("no python")
		}
	}
	f := filepath.Join(t.TempDir(), "check.py")
	if err := os.WriteFile(f, []byte(mobileCheckResponder), 0o644); err != nil {
		t.Fatal(err)
	}
	args := []string{"-m", "py_compile", f}
	if filepath.Base(py) == "py" || filepath.Base(py) == "py.exe" {
		args = append([]string{"-3"}, args...)
	}
	if out, err := exec.Command(py, args...).CombinedOutput(); err != nil {
		t.Fatalf("py_compile: %v\n%s", err, out)
	}
}

func TestMobileCheckQR(t *testing.T) {
	if q := mobileCheckQR("http://203.0.113.50/abcd1234/"); !strings.HasPrefix(q, "data:image/png;base64,") || len(q) < 200 {
		t.Fatalf("qr %.60q", q)
	}
}
