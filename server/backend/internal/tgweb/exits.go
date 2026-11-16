package tgweb

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/proxy"

	"github.com/heyearth/backend/internal/biz"
	"github.com/heyearth/backend/internal/config"
	"github.com/heyearth/backend/internal/provision"
)

// The exits' side of the WEB proxy: which exits run the official MTProxy with the current secret
// (a note per exit, written by the exit step once it found the exit serving it), which of those
// the Hub reaches through the tunnel right now, and the forwarder's list built from both.

// ExitKey is the app_settings key of exit id's note: SecretTag of the secret it runs.
func ExitKey(id int) string { return "tg_webproxy_exit_" + strconv.Itoa(id) }

// SecretTag identifies a secret without revealing it.
func SecretTag(secret string) string {
	s := sha256.Sum256([]byte("tg-webproxy-exit:" + secret))
	return hex.EncodeToString(s[:8])
}

// Exit is one online exit and what is known of its MTProxy.
type Exit struct {
	ID        int    `json:"id"`
	Name      string `json:"name"`
	IP        string `json:"ip"`
	Installed bool   `json:"installed"` // its note says it runs the current secret
	Reachable bool   `json:"reachable"` // the Hub reached :3398 through the tunnel just now
	Path      string `json:"path"`      // the tunnel path that answered: hysteria | stls
}

// Seams (cmd/api sets StartExitJob and ExitJob; tests swap the rest).
var (
	onlineExits = func(ctx context.Context) ([]biz.ExitNodeTunnel, error) { return biz.ListOnlineExitNodesContext(ctx) }
	// StartExitJob starts a KindTGMTProxy job on exit id (the API's node runner).
	StartExitJob func(ctx context.Context, id int, kind string) (int64, error)
	// ExitJob reports the latest job of exit id: its id, status and error.
	ExitJob func(ctx context.Context, id int) (int64, string, string, error)
	// socksDial reaches addr through the Hub's local SOCKS port.
	socksDial = func(ctx context.Context, socks, addr string) (net.Conn, error) {
		d, err := proxy.SOCKS5("tcp", socks, nil, &net.Dialer{Timeout: 4 * time.Second})
		if err != nil {
			return nil, err
		}
		cd, ok := d.(proxy.ContextDialer)
		if !ok {
			return nil, errors.New("SOCKS5 без контекста")
		}
		return cd.DialContext(ctx, "tcp", addr)
	}
	exitJobWait = 25 * time.Minute
	exitJobPoll = 5 * time.Second
)

// reach tries the exit's MTProxy through the hysteria path, then the ShadowTLS one.
func reach(ctx context.Context, id int, ip string) (bool, string) {
	addr := net.JoinHostPort(ip, strconv.Itoa(config.TGMTProxyPort))
	for _, p := range []struct {
		port int
		name string
	}{{1088 + id, "hysteria"}, {2088 + id, "stls"}} {
		cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		c, err := socksDial(cctx, "127.0.0.1:"+strconv.Itoa(p.port), addr)
		cancel()
		if err == nil {
			c.Close()
			return true, p.name
		}
	}
	return false, ""
}

// Exits lists the online exits with their notes and, for the noted ones, a reachability check
// through the tunnel (in parallel).
func Exits(ctx context.Context, secret string) ([]Exit, error) {
	nodes, err := onlineExits(ctx)
	if err != nil {
		return nil, fmt.Errorf("выходы не прочитаны: %w", err)
	}
	tag := SecretTag(secret)
	out := make([]Exit, len(nodes))
	var wg sync.WaitGroup
	for i, n := range nodes {
		note, err := setting(ctx, ExitKey(n.ID))
		if err != nil {
			return nil, err
		}
		out[i] = Exit{ID: n.ID, Name: n.Name, IP: n.IPAddress, Installed: secret != "" && note == tag}
		if out[i].Installed && net.ParseIP(n.IPAddress) != nil {
			wg.Add(1)
			go func(e *Exit) {
				defer wg.Done()
				e.Reachable, e.Path = reach(ctx, e.ID, e.IP)
				noteReach(e.ID, e.Reachable)
			}(&out[i])
		}
	}
	wg.Wait()
	sort.Slice(out, func(a, b int) bool { return out[a].ID < out[b].ID })
	return out, nil
}

// keepFor: an installed exit stays in the forwarder's list this long after it last answered
// through the tunnel — one failed probe (a tunnel blip) must not take the WEB proxy down; the
// forwarder rests a failing path by itself.
const keepFor = 3 * time.Minute

var (
	okMu   sync.Mutex
	lastOK = map[int]time.Time{}
	now    = time.Now
)

func noteReach(id int, ok bool) {
	if !ok {
		return
	}
	okMu.Lock()
	lastOK[id] = now()
	okMu.Unlock()
}

func recentlyOK(id int) bool {
	okMu.Lock()
	defer okMu.Unlock()
	t, ok := lastOK[id]
	return ok && now().Sub(t) < keepFor
}

func served(exits []Exit) map[int]string {
	m := map[int]string{}
	for _, e := range exits {
		if e.Installed && (e.Reachable || recentlyOK(e.ID)) {
			m[e.ID] = e.IP
		}
	}
	return m
}

func names(exits []Exit, pick func(Exit) bool) string {
	var v []string
	for _, e := range exits {
		if pick(e) {
			v = append(v, "#"+strconv.Itoa(e.ID))
		}
	}
	return strings.Join(v, ", ")
}

// WriteBackends puts the forwarder's list on the Hub (only when it changed); returns how many
// exits it holds.
func WriteBackends(ctx context.Context, hub provision.Remote, exits []Exit) (int, error) {
	s := served(exits)
	if _, err := hub.Run(ctx, "install -d -m 0755 "+config.TGFwdDir); err != nil {
		return 0, err
	}
	_, err := hub.PutFile(ctx, config.TGFwdBackendsPath, config.TGFwdBackendsJSON(s), 0o644)
	return len(s), err
}

// exitsCheck is tg_exits' verify: every online exit noted with the current secret, at least one
// reached through the tunnel, and the forwarder's list on the Hub saying exactly that.
func exitsCheck(secret string) func(ctx context.Context, r provision.Remote) (string, error) {
	return func(ctx context.Context, r provision.Remote) (string, error) {
		exits, err := Exits(ctx, secret)
		if err != nil {
			return "", err
		}
		if len(exits) == 0 {
			return "нет ни одного работающего выхода", nil
		}
		if miss := names(exits, func(e Exit) bool { return !e.Installed }); miss != "" {
			return "на выходах " + miss + " MTProxy WEB-прокси не стоит или со старым секретом", nil
		}
		if len(served(exits)) == 0 {
			return "MTProxy на выходах не отвечает через туннель — выходы ещё не переустановлены с разрешением порта " + strconv.Itoa(config.TGMTProxyPort) + "?", nil
		}
		cur, err := r.Run(ctx, "cat "+config.TGFwdBackendsPath+" 2>/dev/null || true")
		if err != nil {
			return "", err
		}
		if cur != string(config.TGFwdBackendsJSON(served(exits))) {
			return "список выходов перенаправителя устарел", nil
		}
		return "", nil
	}
}

// exitsApply is tg_exits' apply: a KindTGMTProxy job on every online exit not noted with the
// current secret (in parallel: different nodes), each waited for, then the forwarder's list.
// Exits that fail are reported; the step fails only when no exit serves.
func exitsApply(secret string) func(ctx context.Context, r provision.Remote, s *provision.State) error {
	return func(ctx context.Context, r provision.Remote, s *provision.State) error {
		if StartExitJob == nil || ExitJob == nil {
			return errors.New("задачи на выходах недоступны (провижининг не инициализирован)")
		}
		exits, err := Exits(ctx, secret)
		if err != nil {
			return err
		}
		type res struct {
			id  int
			msg string
		}
		var wg sync.WaitGroup
		results := make(chan res, len(exits))
		for _, e := range exits {
			if e.Installed {
				continue
			}
			wg.Add(1)
			go func(id int) {
				defer wg.Done()
				results <- res{id, runExitJob(ctx, id)}
			}(e.ID)
		}
		wg.Wait()
		close(results)
		for r := range results {
			s.Printf("выход #%d: %s", r.id, r.msg)
		}
		exits, err = Exits(ctx, secret)
		if err != nil {
			return err
		}
		n, err := WriteBackends(ctx, r, exits)
		if err != nil {
			return err
		}
		s.Printf("перенаправитель: выходов с MTProxy через туннель — %d (%s)", n, names(exits, func(e Exit) bool { return e.Installed && e.Reachable }))
		if miss := names(exits, func(e Exit) bool { return e.Installed && !e.Reachable }); miss != "" {
			s.Printf("MTProxy стоит, но через туннель не отвечает: %s — переустановите эти выходы (разрешение порта %d)", miss, config.TGMTProxyPort)
		}
		return nil
	}
}

// runExitJob starts the exit's job and waits for its end; a busy exit (another job) is waited for
// first. The result is one line for the step output.
func runExitJob(ctx context.Context, id int) string {
	deadline := time.Now().Add(exitJobWait)
	for {
		jobID, err := StartExitJob(ctx, id, provision.KindTGMTProxy)
		if err == nil {
			for time.Now().Before(deadline) {
				if err := sleepCtx(ctx, exitJobPoll); err != nil {
					return "прервано"
				}
				got, status, msg, err := ExitJob(ctx, id)
				if err != nil || got != jobID {
					continue
				}
				switch status {
				case provision.JobSucceeded:
					return "MTProxy установлен (задача " + strconv.FormatInt(jobID, 10) + ")"
				case provision.JobFailed, provision.JobInterrupted:
					return "задача " + strconv.FormatInt(jobID, 10) + " не удалась: " + msg
				}
			}
			return "задача " + strconv.FormatInt(jobID, 10) + " не закончилась за " + exitJobWait.String()
		}
		if !errors.Is(err, provision.ErrJobActive) || time.Now().After(deadline) {
			return "задача не запущена: " + err.Error()
		}
		if err := sleepCtx(ctx, exitJobPoll); err != nil {
			return "прервано"
		}
	}
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// ExitOptions is the exit step's options from the settings: nil while the WEB proxy is off or
// has no secret yet (then the exit recipes leave its MTProxy alone).
func ExitOptions(ctx context.Context, id int) (*provision.TGExitOptions, error) {
	s, err := Load(ctx)
	if err != nil {
		return nil, err
	}
	if !s.Enabled || !config.TGWebSecretRe.MatchString(s.Secret) {
		return nil, nil
	}
	tag := SecretTag(s.Secret)
	return &provision.TGExitOptions{Secret: s.Secret, Record: func(ctx context.Context) error {
		return setSetting(ctx, ExitKey(id), tag)
	}}, nil
}

// RefreshBackends rewrites the forwarder's list from the live exit state (the API's monitor,
// once a minute while the WEB proxy is on): an exit whose tunnel went down leaves it, one that
// came back returns. Returns how many exits serve.
func RefreshBackends(ctx context.Context, hub provision.Remote) (int, error) {
	s, err := Load(ctx)
	if err != nil || !s.Enabled || !config.TGWebSecretRe.MatchString(s.Secret) {
		return 0, err
	}
	exits, err := Exits(ctx, s.Secret)
	if err != nil {
		return 0, err
	}
	return WriteBackends(ctx, hub, exits)
}
