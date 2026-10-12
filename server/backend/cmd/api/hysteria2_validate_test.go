package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const sampleHysteria2Config = `# /etc/hysteria/server.yaml
listen: :50443

tls:
  cert: /etc/letsencrypt/live/hiearth.ru/fullchain.pem
  key:  /etc/letsencrypt/live/hiearth.ru/privkey.pem

auth:
  type: http
  http:
    url: http://127.0.0.1:8000/internal/hysteria2/auth
    insecure: true

trafficStats:
  listen: 127.0.0.1:7653
  secret: s3cret

outbounds:
  - name: via_exit
    type: socks5
    socks5:
      addr: 127.0.0.1:1090

acl:
  geoip: /etc/hysteria/geoip.dat
  inline:
    - reject(geoip:private)
    - direct(geoip:ru)
    - via_exit(all)
`

func TestHysteria2ProbeConfig(t *testing.T) {
	got := hysteria2ProbeConfig(sampleHysteria2Config)
	// exactly the two listeners move to an ephemeral loopback port ...
	want := strings.Replace(sampleHysteria2Config, "listen: :50443", "listen: 127.0.0.1:0", 1)
	want = strings.Replace(want, "  listen: 127.0.0.1:7653", "  listen: 127.0.0.1:0", 1)
	if got != want {
		t.Fatalf("probe config:\n--- got\n%s\n--- want\n%s", got, want)
	}
	// ... and nothing else, the ACL above all: the probe must parse the very same rules
	for _, keep := range []string{"- reject(geoip:private)", "- direct(geoip:ru)", "url: http://127.0.0.1:8000/internal/hysteria2/auth", "addr: 127.0.0.1:1090", "secret: s3cret"} {
		if !strings.Contains(got, keep) {
			t.Errorf("probe config lost %q", keep)
		}
	}
	if strings.Contains(got, ":50443") || strings.Contains(got, ":7653") {
		t.Errorf("a live port is still bound by the probe:\n%s", got)
	}
	// idempotent, and a config without trafficStats is fine
	if again := hysteria2ProbeConfig(got); again != got {
		t.Errorf("not idempotent")
	}
	bare := "listen: :50443\nacl:\n  inline:\n    - direct(all)\n"
	if got := hysteria2ProbeConfig(bare); got != "listen: 127.0.0.1:0\nacl:\n  inline:\n    - direct(all)\n" {
		t.Errorf("config without trafficStats: %q", got)
	}
	// a `listen:` nested in some other block is not touched
	nested := "listen: :50443\nother:\n  listen: 10.0.0.1:1\n"
	if got := hysteria2ProbeConfig(nested); !strings.Contains(got, "  listen: 10.0.0.1:1") {
		t.Errorf("a listen outside the QUIC / trafficStats blocks was rewritten: %q", got)
	}
}

func TestValidateHysteria2Config(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "server.yaml.tmp")
	if err := os.WriteFile(cfg, []byte(sampleHysteria2Config), 0o600); err != nil {
		t.Fatal(err)
	}
	const timeout = 3 * time.Second

	type seen struct {
		path    string
		content string
		timeout time.Duration
	}
	run := func(p hysteria2Prober) (error, seen) {
		var s seen
		err := validateHysteria2ConfigWith(cfg, func(path string, to time.Duration) (bool, string, error) {
			b, _ := os.ReadFile(path)
			s = seen{path: path, content: string(b), timeout: to}
			return p(path, to)
		}, timeout)
		return err, s
	}

	t.Run("still running at the timeout: accepted, and the probe never touches the live ports", func(t *testing.T) {
		err, s := run(func(string, time.Duration) (bool, string, error) { return true, "", nil })
		if err != nil {
			t.Fatalf("a surviving server must be accepted: %v", err)
		}
		if s.path == cfg || s.timeout != timeout {
			t.Errorf("the probe must run on a copy (%s) with the given timeout (%v)", s.path, s.timeout)
		}
		if !strings.Contains(s.content, "listen: 127.0.0.1:0") || strings.Contains(s.content, ":50443") || strings.Contains(s.content, ":7653") {
			t.Errorf("the copy must bind ephemeral loopback ports only:\n%s", s.content)
		}
		if _, statErr := os.Stat(s.path); !os.IsNotExist(statErr) {
			t.Errorf("the probe copy (it holds secrets) must be removed")
		}
		if b, _ := os.ReadFile(cfg); string(b) != sampleHysteria2Config {
			t.Errorf("the candidate itself must stay untouched")
		}
	})

	t.Run("an early exit is a rejection and carries what hysteria printed, whatever it printed", func(t *testing.T) {
		for _, out := range []string{
			"exit status 1: ERROR failed to load server config {\"error\": \"invalid config: acl.inline: invalid rule\"}",
			// the old code treated both of these as success:
			"INFO server mode\nFATAL failed to parse ACL: unknown outbound \"via_exti\"",
			"FATAL listen udp :50443: bind: address already in use",
		} {
			err, _ := run(func(string, time.Duration) (bool, string, error) { return false, out, nil })
			if err == nil || !strings.Contains(err.Error(), out) {
				t.Errorf("early exit with %q must be rejected with the output in the error, got %v", out, err)
			}
		}
	})

	t.Run("a failure to start is an error", func(t *testing.T) {
		err, _ := run(func(string, time.Duration) (bool, string, error) {
			return false, "", errors.New("executable file not found")
		})
		if err == nil || !strings.Contains(err.Error(), "executable file not found") {
			t.Fatalf("got %v", err)
		}
	})

	t.Run("a very long output is cut to its tail", func(t *testing.T) {
		long := strings.Repeat("x", 5000) + "THE-REAL-ERROR"
		err, _ := run(func(string, time.Duration) (bool, string, error) { return false, long, nil })
		if err == nil || !strings.Contains(err.Error(), "THE-REAL-ERROR") || len(err.Error()) > 2300 {
			t.Fatalf("len=%d err=%v", len(err.Error()), err)
		}
	})

	t.Run("a missing candidate is an error", func(t *testing.T) {
		if err := validateHysteria2ConfigWith(filepath.Join(dir, "nope.yaml"), func(string, time.Duration) (bool, string, error) { return true, "", nil }, timeout); err == nil {
			t.Fatal("expected an error")
		}
	})
}
