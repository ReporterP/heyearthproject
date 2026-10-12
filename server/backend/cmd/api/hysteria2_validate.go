package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// hysteria2ValidateTimeout is how long a candidate config must keep the server alive. Loading
// the TLS pair, the geoip file and the ACL takes well under a second; a config the server does
// not like makes it exit at once.
const hysteria2ValidateTimeout = 3 * time.Second

// hysteria2ProbeListen is the listen address of the throw-away probe: an ephemeral loopback port.
const hysteria2ProbeListen = "127.0.0.1:0"

// validateHysteria2Config checks a candidate server.yaml by starting a real hysteria server on a
// COPY of it and watching whether it survives hysteria2ValidateTimeout.
//
// The copy differs from the candidate in two lines only: the QUIC listener and the trafficStats
// listener are moved to ephemeral loopback ports (hysteria2ProbeListen). The probe therefore
// never collides with the live hysteria2-server holding :50443 / 127.0.0.1:7653, which is what
// the previous implementation had to explain away: it treated "address already in use" and even
// the early "server mode" log line as success, but hysteria prints that line before the config
// is built and binds the port before it parses the ACL, so a broken ACL passed. Now:
//
//   - the process is still running at the timeout → the config was accepted (the probe is
//     killed);
//   - the process exits earlier, whatever the reason → the config is rejected and the error
//     carries what hysteria printed (stdout and stderr, it writes its FATAL line to either).
//
// Hysteria v2.6.x has no --check flag, so start-and-watch is the only syntactic gate.
func validateHysteria2Config(path string) error {
	return validateHysteria2ConfigWith(path, runHysteria2Probe, hysteria2ValidateTimeout)
}

// hysteria2Prober starts the server on cfgPath and reports whether it was still running after
// timeout (alive) or what it printed when it exited earlier. err is a failure to start at all.
type hysteria2Prober func(cfgPath string, timeout time.Duration) (alive bool, output string, err error)

func validateHysteria2ConfigWith(path string, probe hysteria2Prober, timeout time.Duration) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read hysteria2 config: %w", err)
	}
	// 0600 next to the candidate: the copy holds the obfs password and the stats secret.
	f, err := os.CreateTemp(filepath.Dir(path), ".hysteria2-probe-*.yaml")
	if err != nil {
		return fmt.Errorf("create hysteria2 probe config: %w", err)
	}
	probePath := f.Name()
	defer os.Remove(probePath)
	if _, err := f.WriteString(hysteria2ProbeConfig(string(raw))); err != nil {
		f.Close()
		return fmt.Errorf("write hysteria2 probe config: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("write hysteria2 probe config: %w", err)
	}

	alive, output, err := probe(probePath, timeout)
	switch {
	case err != nil:
		return fmt.Errorf("start hysteria: %w", err)
	case alive:
		return nil
	default:
		return fmt.Errorf("hysteria rejected the config (exited within %v): %s", timeout, tailString(strings.TrimSpace(output), 2000))
	}
}

// hysteria2ProbeConfig returns cfg with the QUIC listener (top-level `listen:`) and the
// trafficStats listener (`listen:` inside the trafficStats block) replaced by
// hysteria2ProbeListen. Everything else, the ACL included, is left byte for byte as it was.
func hysteria2ProbeConfig(cfg string) string {
	lines := strings.Split(cfg, "\n")
	inStats := false
	topDone := false
	for i, l := range lines {
		trim := strings.TrimSpace(l)
		indented := strings.HasPrefix(l, " ") || strings.HasPrefix(l, "\t")
		switch {
		case !indented && trim != "" && !strings.HasPrefix(trim, "#"):
			inStats = strings.HasPrefix(trim, "trafficStats:")
			if !topDone && strings.HasPrefix(trim, "listen:") {
				lines[i] = "listen: " + hysteria2ProbeListen
				topDone = true
			}
		case inStats && strings.HasPrefix(trim, "listen:"):
			lines[i] = l[:len(l)-len(strings.TrimLeft(l, " \t"))] + "listen: " + hysteria2ProbeListen
		}
	}
	return strings.Join(lines, "\n")
}

func tailString(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return "…" + s[len(s)-n:]
}

// runHysteria2Probe is the production hysteria2Prober: `hysteria server --config <cfg>` under
// a deadline.
func runHysteria2Probe(cfgPath string, timeout time.Duration) (bool, string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "hysteria", "server", "--config", cfgPath, "--disable-update-check")
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Start(); err != nil {
		return false, "", err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case <-ctx.Done():
		// Still running at the deadline: accepted. Kill it and reap it.
		_ = cmd.Process.Kill()
		<-done
		return true, "", nil
	case werr := <-done:
		return false, fmt.Sprintf("%v: %s", werr, out.String()), nil
	}
}
