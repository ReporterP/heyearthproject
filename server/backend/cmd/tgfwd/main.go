// heyearth-tgfwd: the Telegram WEB proxy's backend on the Hub (phase T). The relay
// (tproxy-server) sends every MTProxy stream to 127.0.0.1:3398; this forwards it to the official
// MTProxy on an exit (Telegram is unreachable from the Hub itself), through the Hub→exit tunnel:
// the exit's hysteria SOCKS (127.0.0.1:1088+id) first, its ShadowTLS SOCKS (127.0.0.1:2088+id)
// when that fails (choked UDP). Exits are tried from a rotating start (load spread); a failed
// path rests for a while. The candidates come from backends.json, written by heyearth-api from
// the live exit state and re-read when it changes. With no candidate the listener is closed, so
// the relay's /readyz answers 503 (and the API's monitor raises its alert).
//
// The streams are MTProxy's obfuscated2: opaque here, nothing is logged about their content.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/net/proxy"
)

// Target is one exit's MTProxy and the local SOCKS ports that reach it, in order of preference.
type Target struct {
	Exit  int      `json:"exit"`
	Addr  string   `json:"addr"`
	Socks []string `json:"socks"`
}

type backends struct {
	Targets []Target `json:"targets"`
}

const (
	dialTimeout = 5 * time.Second
	restFor     = 30 * time.Second
)

type forwarder struct {
	mu      sync.Mutex
	targets []Target
	resting map[string]time.Time // socks|addr → until
	next    atomic.Uint64
	now     func() time.Time
	dial    func(ctx context.Context, socks, addr string) (net.Conn, error)
}

func socksDial(ctx context.Context, socks, addr string) (net.Conn, error) {
	d, err := proxy.SOCKS5("tcp", socks, nil, &net.Dialer{Timeout: dialTimeout})
	if err != nil {
		return nil, err
	}
	cd, ok := d.(proxy.ContextDialer)
	if !ok {
		return nil, errors.New("SOCKS5 dialer without context")
	}
	return cd.DialContext(ctx, "tcp", addr)
}

func (f *forwarder) set(t []Target) {
	f.mu.Lock()
	f.targets = t
	f.mu.Unlock()
}

func (f *forwarder) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.targets)
}

// paths lists every (socks, addr) in the order this stream tries them: exits from a rotating
// start, each exit's SOCKS ports in order; resting paths go last.
func (f *forwarder) paths() [][2]string {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := len(f.targets)
	if n == 0 {
		return nil
	}
	start := int(f.next.Add(1)-1) % n
	now := f.now()
	var fresh, rest [][2]string
	for i := range n {
		t := f.targets[(start+i)%n]
		for _, s := range t.Socks {
			p := [2]string{s, t.Addr}
			if until, ok := f.resting[s+"|"+t.Addr]; ok && now.Before(until) {
				rest = append(rest, p)
			} else {
				fresh = append(fresh, p)
			}
		}
	}
	return append(fresh, rest...)
}

func (f *forwarder) result(p [2]string, ok bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if ok {
		delete(f.resting, p[0]+"|"+p[1])
	} else {
		f.resting[p[0]+"|"+p[1]] = f.now().Add(restFor)
	}
}

// open dials the first path that answers.
func (f *forwarder) open(ctx context.Context) (net.Conn, error) {
	var last error
	for _, p := range f.paths() {
		dctx, cancel := context.WithTimeout(ctx, dialTimeout)
		c, err := f.dial(dctx, p[0], p[1])
		cancel()
		f.result(p, err == nil)
		if err == nil {
			return c, nil
		}
		last = err
	}
	if last == nil {
		last = errors.New("нет выходов с MTProxy")
	}
	return nil, last
}

func (f *forwarder) serve(c net.Conn) {
	defer c.Close()
	up, err := f.open(context.Background())
	if err != nil {
		log.Printf("[tgfwd] поток не открыт: %v", err)
		return
	}
	defer up.Close()
	done := make(chan struct{}, 2)
	pipe := func(dst, src net.Conn) {
		_, _ = io.Copy(dst, src)
		if tc, ok := dst.(interface{ CloseWrite() error }); ok {
			_ = tc.CloseWrite()
		} else {
			_ = dst.Close()
		}
		done <- struct{}{}
	}
	go pipe(up, c)
	go pipe(c, up)
	<-done
	<-done
}

// load reads backends.json; an unreadable or invalid file keeps what was loaded before.
func load(path string) ([]Target, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var bk backends
	if err := json.Unmarshal(b, &bk); err != nil {
		return nil, err
	}
	var out []Target
	for _, t := range bk.Targets {
		if _, _, err := net.SplitHostPort(t.Addr); err != nil || len(t.Socks) == 0 {
			continue
		}
		out = append(out, t)
	}
	return out, nil
}

// listener keeps the listening socket open exactly while there are targets.
type listener struct {
	addr string
	f    *forwarder
	mu   sync.Mutex
	ln   net.Listener
}

func (l *listener) sync() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	want := l.f.count() > 0
	switch {
	case want && l.ln == nil:
		ln, err := net.Listen("tcp", l.addr)
		if err != nil {
			return err
		}
		l.ln = ln
		go func() {
			for {
				c, err := ln.Accept()
				if err != nil {
					return
				}
				go l.f.serve(c)
			}
		}()
		log.Printf("[tgfwd] слушаю %s", l.addr)
	case !want && l.ln != nil:
		_ = l.ln.Close()
		l.ln = nil
		log.Printf("[tgfwd] выходов с MTProxy нет — %s закрыт", l.addr)
	}
	return nil
}

func main() {
	listen := flag.String("listen", "127.0.0.1:3398", "адрес для релея (только loopback)")
	path := flag.String("backends", "/etc/tproxy-server/backends.json", "список выходов (пишет heyearth-api)")
	flag.Parse()
	if h, _, err := net.SplitHostPort(*listen); err != nil || !net.ParseIP(h).IsLoopback() {
		log.Fatalf("[tgfwd] -listen %q: нужен адрес loopback", *listen)
	}
	f := &forwarder{resting: map[string]time.Time{}, now: time.Now, dial: socksDial}
	l := &listener{addr: *listen, f: f}
	var mod time.Time
	for {
		if st, err := os.Stat(*path); err == nil && !st.ModTime().Equal(mod) {
			if t, err := load(*path); err != nil {
				log.Printf("[tgfwd] %s не прочитан: %v — остаётся прежний список", *path, err)
			} else {
				mod = st.ModTime()
				f.set(t)
				log.Printf("[tgfwd] выходов с MTProxy: %d", len(t))
			}
		} else if err != nil && f.count() > 0 && errors.Is(err, os.ErrNotExist) {
			f.set(nil)
		}
		if err := l.sync(); err != nil {
			fmt.Fprintf(os.Stderr, "[tgfwd] %v\n", err)
		}
		time.Sleep(5 * time.Second)
	}
}
