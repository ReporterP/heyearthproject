package tgweb

import (
	"bufio"
	"context"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/heyearth/backend/internal/config"
	"github.com/heyearth/backend/internal/provision"
)

// Status is what the admin card shows about the running WEB proxy.
type Status struct {
	CheckedAt     time.Time         `json:"checked_at"`
	Healthz       bool              `json:"healthz"`
	Readyz        bool              `json:"readyz"`
	ReadyzError   string            `json:"readyz_error"`
	Services      map[string]string `json:"services"`
	Metrics       map[string]int64  `json:"metrics"`
	MiddleEnd5Min *int              `json:"middle_end_disconnects_5m"`
	Cert          CertView          `json:"cert"`
	DNS           *DNSView          `json:"dns"`
}

// CertView: the host's certificate as stored by certbot.
type CertView struct {
	Present  bool       `json:"present"`
	NotAfter *time.Time `json:"not_after"`
	DaysLeft int        `json:"days_left"`
	Error    string     `json:"error"`
}

var adminClient = &http.Client{Timeout: 4 * time.Second}

// adminGet asks the relay's admin listener (loopback only).
var adminGet = func(ctx context.Context, path string) (int, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+config.TGWebAdminListen+path, nil)
	if err != nil {
		return 0, "", err
	}
	resp, err := adminClient.Do(req)
	if err != nil {
		return 0, "", err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	return resp.StatusCode, string(b), nil
}

// Ready: GET /readyz answers 200 (the relay up and its MTProxy reachable).
func Ready(ctx context.Context) (bool, string) {
	code, body, err := adminGet(ctx, "/readyz")
	switch {
	case err != nil:
		return false, "релей не отвечает: " + err.Error()
	case code != http.StatusOK:
		return false, strconv.Itoa(code) + " " + strings.TrimSpace(body)
	}
	return true, ""
}

// parseMetrics reads the relay's text metrics (tproxy_<name> <value>), names without the prefix.
func parseMetrics(body string) map[string]int64 {
	out := map[string]int64{}
	sc := bufio.NewScanner(strings.NewReader(body))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) != 2 || !strings.HasPrefix(f[0], "tproxy_") {
			continue
		}
		if n, err := strconv.ParseInt(f[1], 10, 64); err == nil {
			out[strings.TrimPrefix(f[0], "tproxy_")] = n
		}
	}
	return out
}

// certInfo reads /etc/letsencrypt/live/<host>/fullchain.pem (the API runs as root on the Hub).
var readCert = func(host string) ([]byte, error) {
	return os.ReadFile("/etc/letsencrypt/live/" + host + "/fullchain.pem")
}

func certInfo(host string, now time.Time) CertView {
	b, err := readCert(host)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return CertView{}
		}
		return CertView{Error: err.Error()}
	}
	blk, _ := pem.Decode(b)
	if blk == nil {
		return CertView{Present: true, Error: "сертификат не читается"}
	}
	c, err := x509.ParseCertificate(blk.Bytes)
	if err != nil {
		return CertView{Present: true, Error: err.Error()}
	}
	na := c.NotAfter.UTC()
	return CertView{Present: true, NotAfter: &na, DaysLeft: int(na.Sub(now).Hours() / 24)}
}

// statusScript: the units' states and MTProxy's middle-end disconnects in the last 5 minutes.
var statusScript = `for u in tproxy-server mtproxy refresh-mtproxy-config.timer; do printf 'UNIT %s %s\n' "$u" "$(systemctl is-active "$u" 2>/dev/null || true)"; done
printf 'ME %s\n' "$(journalctl -u mtproxy --since -5min --no-pager -o cat 2>/dev/null | grep -c 'Disconnected from RPC Middle-End' || true)"
`

// Check gathers the status (each part best effort: what cannot be read stays empty / nil).
func Check(ctx context.Context, hub provision.Remote, host string) Status {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	st := Status{CheckedAt: time.Now().UTC(), Services: map[string]string{}}
	if code, _, err := adminGet(ctx, "/healthz"); err == nil && code == http.StatusOK {
		st.Healthz = true
	}
	st.Readyz, st.ReadyzError = Ready(ctx)
	if code, body, err := adminGet(ctx, "/metrics"); err == nil && code == http.StatusOK {
		st.Metrics = parseMetrics(body)
	}
	if hub != nil {
		if out, err := hub.Run(ctx, statusScript); err == nil {
			for _, l := range strings.Split(out, "\n") {
				f := strings.Fields(l)
				switch {
				case len(f) >= 2 && f[0] == "UNIT":
					st.Services[f[1]] = strings.Join(f[2:], " ")
				case len(f) == 2 && f[0] == "ME":
					if n, err := strconv.Atoi(f[1]); err == nil {
						st.MiddleEnd5Min = &n
					}
				}
			}
		}
	}
	st.Cert = certInfo(host, time.Now())
	if v, err := CheckDNS(ctx, host); err == nil {
		st.DNS = &v
	}
	return st
}
