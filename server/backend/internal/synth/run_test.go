package synth

import (
	"context"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// The process tests run a stand-in "core" (testdata/stubcore): socks, hold or nolisten by
// SYNTH_STUB, alive exactly while its SYNTH_STUB_LIVE port answers.

var stubBuild struct {
	once sync.Once
	dir  string
	path string
	err  error
}

func TestMain(m *testing.M) {
	code := m.Run()
	if stubBuild.dir != "" {
		os.RemoveAll(stubBuild.dir)
	}
	os.Exit(code)
}

// stubCore builds the stand-in core once per test binary. SYNTH_STUB_BIN names a ready binary
// instead (a run where the go tool is not at hand, e.g. the cross-compiled Linux run).
func stubCore(t *testing.T) string {
	t.Helper()
	stubBuild.once.Do(func() {
		if bin := os.Getenv("SYNTH_STUB_BIN"); bin != "" {
			stubBuild.path = bin
			return
		}
		dir, err := os.MkdirTemp("", "synth-stub")
		if err != nil {
			stubBuild.err = err
			return
		}
		stubBuild.dir = dir
		bin := filepath.Join(dir, "stubcore")
		if runtime.GOOS == "windows" {
			bin += ".exe"
		}
		if out, err := exec.Command("go", "build", "-o", bin, "./testdata/stubcore").CombinedOutput(); err != nil {
			stubBuild.err = &buildError{err, string(out)}
			return
		}
		stubBuild.path = bin
	})
	if stubBuild.err != nil {
		t.Fatalf("build the stand-in core: %v", stubBuild.err)
	}
	return stubBuild.path
}

type buildError struct {
	err error
	out string
}

func (e *buildError) Error() string { return e.err.Error() + ": " + e.out }

func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

func portOpen(port int) bool {
	c, err := net.DialTimeout("tcp", "127.0.0.1:"+strconv.Itoa(port), 300*time.Millisecond)
	if err != nil {
		return false
	}
	c.Close()
	return true
}

// waitClosed waits for the process behind the liveness port to be gone.
func waitClosed(t *testing.T, port int, what string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for portOpen(port) {
		if time.Now().After(deadline) {
			t.Fatalf("%s: the core process is still alive", what)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func dirEntries(t *testing.T, dir string) []string {
	t.Helper()
	es, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range es {
		names = append(names, e.Name())
	}
	return names
}

// stubRunner prepares a Runner over the stand-in core in the given mode.
func stubRunner(t *testing.T, mode string) (r Runner, socksPort, livePort int, cfg map[string]any) {
	t.Helper()
	socksPort, livePort = freePort(t), freePort(t)
	t.Setenv("SYNTH_STUB", mode)
	t.Setenv("SYNTH_STUB_LIVE", strconv.Itoa(livePort))
	cfg, err := ForWay(fixture(t)[0], "p-hub-tcp", socksPort, nil)
	if err != nil {
		t.Fatal(err)
	}
	return Runner{XrayBin: stubCore(t), WorkDir: t.TempDir(), StartTimeout: 5 * time.Second, RunTimeout: 20 * time.Second}, socksPort, livePort, cfg
}

func TestProbeFetchesThroughCore(t *testing.T) {
	r, port, live, cfg := stubRunner(t, "socks")
	srv := serveBytes(t, 200, 70000)
	results, err := r.Probe(context.Background(), cfg, port, []Target{
		{Name: "bulk", URL: srv.URL, MinBytes: 65536},
		{Name: "ru", URL: srv.URL},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 || !results[0].OK || results[0].Bytes != 70000 || !results[1].OK {
		t.Fatalf("results = %+v, want both targets fetched through the core", results)
	}
	waitClosed(t, live, "after a finished probe")
	if left := dirEntries(t, r.WorkDir); len(left) != 0 {
		t.Errorf("the work dir keeps %v after the probe", left)
	}
}

// Review Focus 2: a frozen path must not leave a core behind — the process has a hard lifetime.
func TestProbeKillsOnTimeout(t *testing.T) {
	r, port, live, cfg := stubRunner(t, "hold")
	r.RunTimeout = 300 * time.Millisecond
	start := time.Now()
	results, err := r.Probe(context.Background(), cfg, port, []Target{{Name: "bulk", URL: "http://203.0.113.9/", MinBytes: 65536}})
	if err != nil {
		t.Fatal(err)
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Errorf("Probe returned after %s with RunTimeout 300ms", d)
	}
	if len(results) != 1 || results[0].OK || results[0].Err == "" {
		t.Fatalf("results = %+v, want one failed target", results)
	}
	waitClosed(t, live, "after RunTimeout")
	if left := dirEntries(t, r.WorkDir); len(left) != 0 {
		t.Errorf("the work dir keeps %v after the probe", left)
	}
}

func TestProbeStartTimeout(t *testing.T) {
	r, port, live, cfg := stubRunner(t, "nolisten")
	r.StartTimeout = 400 * time.Millisecond
	_, err := r.Probe(context.Background(), cfg, port, []Target{{Name: "ru", URL: "http://203.0.113.9/"}})
	if err == nil || !strings.Contains(err.Error(), "xray did not start") {
		t.Fatalf("err = %v, want «xray did not start»", err)
	}
	waitClosed(t, live, "after a failed start")
	if left := dirEntries(t, r.WorkDir); len(left) != 0 {
		t.Errorf("the work dir keeps %v after a failed start", left)
	}
}

// The config carries the test user's keys: it must be readable by the owner only and must
// have the .json extension xray needs.
func TestProbeConfigFile(t *testing.T) {
	r, port, _, cfg := stubRunner(t, "hold")
	r.RunTimeout = 1500 * time.Millisecond
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = r.Probe(context.Background(), cfg, port, []Target{{Name: "bulk", URL: "http://203.0.113.9/", MinBytes: 1}})
	}()
	var conf string
	deadline := time.Now().Add(3 * time.Second)
	for conf == "" && time.Now().Before(deadline) {
		matches, _ := filepath.Glob(filepath.Join(r.WorkDir, "*", "c.json"))
		if len(matches) == 1 {
			conf = matches[0]
		}
		time.Sleep(20 * time.Millisecond)
	}
	if conf == "" {
		t.Fatal("no <work dir>/<run>/c.json while the probe runs")
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(conf), "pid")); err != nil {
		t.Errorf("no pid file next to the config: %v", err)
	}
	if runtime.GOOS != "windows" {
		st, err := os.Stat(conf)
		if err != nil {
			t.Fatal(err)
		}
		if st.Mode().Perm()&0o077 != 0 {
			t.Errorf("config mode %v: it carries keys, the owner only", st.Mode().Perm())
		}
	}
	<-done
}

// Review Focus 2: cores left by a previous API process are ended at start. Only a process
// that really runs from this work dir is touched — a pid file may outlive its process, and
// the number may belong to somebody else by then.
func TestReapStale(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("stale cores are told by /proc/<pid>/cmdline: Linux only (the Hub)")
	}
	r, _, live, _ := stubRunner(t, "nolisten")
	run := filepath.Join(r.WorkDir, "abc")
	if err := os.MkdirAll(run, 0o700); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(r.XrayBin, "run", "-c", filepath.Join(run, "c.json"))
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	go cmd.Wait()
	if err := os.WriteFile(filepath.Join(run, "pid"), []byte(strconv.Itoa(cmd.Process.Pid)), 0o600); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 100 && !portOpen(live); i++ {
		time.Sleep(20 * time.Millisecond)
	}
	// a pid file that names a live process which is NOT ours (this test itself)
	foreign := filepath.Join(r.WorkDir, "old")
	if err := os.MkdirAll(foreign, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(foreign, "pid"), []byte(strconv.Itoa(os.Getpid())), 0o600); err != nil {
		t.Fatal(err)
	}

	if n := r.ReapStale(); n != 1 {
		t.Fatalf("ReapStale() = %d, want 1 (the stale core; the foreign process is not counted)", n)
	}
	waitClosed(t, live, "after ReapStale")
	if left := dirEntries(t, r.WorkDir); len(left) != 0 {
		t.Errorf("the work dir keeps %v after the reap", left)
	}
}

// On any OS the leftovers of a previous process are cleared, and a missing work dir is fine.
func TestReapStaleClearsDirs(t *testing.T) {
	r := Runner{XrayBin: "xray", WorkDir: filepath.Join(t.TempDir(), "synth")}
	if n := r.ReapStale(); n != 0 {
		t.Fatalf("no work dir yet: ReapStale() = %d", n)
	}
	gone := filepath.Join(r.WorkDir, "gone")
	if err := os.MkdirAll(gone, 0o700); err != nil {
		t.Fatal(err)
	}
	// a pid nobody has: the largest value a pid can take
	if err := os.WriteFile(filepath.Join(gone, "pid"), []byte("4194303"), 0o600); err != nil {
		t.Fatal(err)
	}
	if n := r.ReapStale(); n != 0 {
		t.Fatalf("a dead pid: ReapStale() = %d, want 0", n)
	}
	if left := dirEntries(t, r.WorkDir); len(left) != 0 {
		t.Errorf("the work dir keeps %v", left)
	}
}

// Review finding: when something else already listens on the SOCKS port (a core a crashed run
// left behind, any other process), the new core cannot bind — and the fetches would go through
// that stranger and be stored as the lines' results. The probe must refuse instead.
func TestProbeRefusesBusyPort(t *testing.T) {
	r, _, live, _ := stubRunner(t, "socks")
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			c.Close()
		}
	}()
	busy := ln.Addr().(*net.TCPAddr).Port
	cfg, err := ForWay(fixture(t)[0], "p-hub-tcp", busy, nil)
	if err != nil {
		t.Fatal(err)
	}
	srv := serveBytes(t, 200, 70000)
	results, err := r.Probe(context.Background(), cfg, busy, []Target{{Name: "bulk", URL: srv.URL, MinBytes: 65536}})
	if err == nil {
		t.Fatalf("results = %+v, no error: the probe went through whatever listens on the port", results)
	}
	if !strings.Contains(err.Error(), "busy") {
		t.Errorf("err = %v, want it to say the port is busy", err)
	}
	waitClosed(t, live, "after a refused probe")
}

// A dial that succeeds says nothing when the core is already gone: something else answered.
func TestWaitListeningCoreExited(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			c.Close()
		}
	}()
	exited := make(chan struct{})
	close(exited)
	if err := waitListening(context.Background(), ln.Addr().String(), time.Second, exited); err == nil {
		t.Fatal("waitListening took a stranger's port for the core that had exited")
	}
}
