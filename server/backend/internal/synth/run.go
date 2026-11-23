package synth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/proxy"
)

// Runner starts the core for one probe and guarantees it does not outlive it.
type Runner struct {
	XrayBin string // the xray binary
	WorkDir string // each run gets <WorkDir>/<random>/ with c.json and pid; removed after it
	// AssetDir holds geoip.dat / geosite.dat (XRAY_LOCATION_ASSET); "" = the directory of XrayBin.
	AssetDir     string
	StartTimeout time.Duration // how long the core may take to open its SOCKS port (default 10 s)
	RunTimeout   time.Duration // the hard lifetime of the core, start included (default 60 s)
}

const (
	defaultStartTimeout = 10 * time.Second
	defaultRunTimeout   = 60 * time.Second
	coreOutputTail      = 2 << 10
)

// Probe runs the core with cfg (which must listen for SOCKS on 127.0.0.1:socksPort), opens
// every target through it and ends the core. The error is about the core itself (it could
// not be started); a target that did not open is a Result, not an error. The config file —
// it carries keys — and the process are gone when Probe returns, whatever happened.
func (r Runner) Probe(ctx context.Context, cfg map[string]any, socksPort int, targets []Target) ([]Result, error) {
	runTimeout := r.RunTimeout
	if runTimeout <= 0 {
		runTimeout = defaultRunTimeout
	}
	startTimeout := r.StartTimeout
	if startTimeout <= 0 {
		startTimeout = defaultStartTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, runTimeout)
	defer cancel()

	if err := os.MkdirAll(r.WorkDir, 0o700); err != nil {
		return nil, fmt.Errorf("work dir: %w", err)
	}
	dir, err := os.MkdirTemp(r.WorkDir, "run-")
	if err != nil {
		return nil, fmt.Errorf("work dir: %w", err)
	}
	defer os.RemoveAll(dir)
	body, err := json.Marshal(cfg)
	if err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}
	conf := filepath.Join(dir, "c.json") // xray tells the format by the extension
	if err := os.WriteFile(conf, body, 0o600); err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}

	asset := r.AssetDir
	if asset == "" {
		asset = filepath.Dir(r.XrayBin)
	}
	out := &tailBuffer{limit: coreOutputTail}
	cmd := exec.Command(r.XrayBin, "run", "-c", conf)
	cmd.Env = append(os.Environ(), "XRAY_LOCATION_ASSET="+asset)
	cmd.Stdout, cmd.Stderr = out, out
	ownProcessGroup(cmd)
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("xray did not start: %w", err)
	}
	exited := make(chan struct{})
	go func() { _ = cmd.Wait(); close(exited) }()
	// The hard lifetime: whatever the fetches do, the core dies at RunTimeout.
	lifetime := time.AfterFunc(runTimeout, func() { killProcess(cmd.Process.Pid) })
	defer func() {
		lifetime.Stop()
		killProcess(cmd.Process.Pid)
		select {
		case <-exited:
		case <-time.After(5 * time.Second):
		}
	}()
	_ = os.WriteFile(filepath.Join(dir, "pid"), []byte(strconv.Itoa(cmd.Process.Pid)), 0o600)

	addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(socksPort))
	if err := waitListening(ctx, addr, startTimeout, exited); err != nil {
		return nil, fmt.Errorf("xray did not start: %w: %s", err, out.String())
	}
	dialer, err := proxy.SOCKS5("tcp", addr, nil, &net.Dialer{Timeout: 5 * time.Second})
	if err != nil {
		return nil, fmt.Errorf("socks dialer: %w", err)
	}
	cd, ok := dialer.(proxy.ContextDialer)
	if !ok {
		return nil, errors.New("socks dialer has no DialContext")
	}
	results := make([]Result, 0, len(targets))
	for _, t := range targets {
		results = append(results, Fetch(ctx, cd, t))
	}
	return results, nil
}

// waitListening waits until addr accepts a connection, the core exits, or the time is up.
func waitListening(ctx context.Context, addr string, timeout time.Duration, exited <-chan struct{}) error {
	deadline := time.Now().Add(timeout)
	for {
		c, err := net.DialTimeout("tcp", addr, 200*time.Millisecond)
		if err == nil {
			c.Close()
			return nil
		}
		select {
		case <-exited:
			return errors.New("the core exited")
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("no SOCKS port after %s", timeout)
		}
	}
}

// ReapStale ends the cores a previous process left in WorkDir (the API was restarted in the
// middle of a run) and removes their directories. It returns how many processes it ended.
// A pid file is trusted only when that process really runs from WorkDir: the file may outlive
// its process, and the number may be somebody else's by then.
func (r Runner) ReapStale() int {
	entries, err := os.ReadDir(r.WorkDir)
	if err != nil {
		return 0
	}
	killed := 0
	for _, e := range entries {
		dir := filepath.Join(r.WorkDir, e.Name())
		if b, err := os.ReadFile(filepath.Join(dir, "pid")); err == nil {
			if pid, err := strconv.Atoi(strings.TrimSpace(string(b))); err == nil && pid > 1 && runsFrom(pid, r.WorkDir) {
				killProcess(pid)
				killed++
			}
		}
		_ = os.RemoveAll(dir)
	}
	return killed
}

// tailBuffer keeps the last limit bytes written to it.
type tailBuffer struct {
	mu    sync.Mutex
	limit int
	buf   []byte
}

func (b *tailBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.buf = append(b.buf, p...)
	if len(b.buf) > b.limit {
		b.buf = b.buf[len(b.buf)-b.limit:]
	}
	return len(p), nil
}

func (b *tailBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return strings.TrimSpace(string(b.buf))
}
