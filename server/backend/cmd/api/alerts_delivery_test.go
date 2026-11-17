package main

import (
	"context"
	"encoding/binary"
	"errors"
	tgapi "github.com/heyearth/backend/internal/telegram"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/heyearth/backend/internal/biz"
)

// newFakeSocksConnect starts a minimal SOCKS5 server (no auth, CONNECT only) on 127.0.0.1 — the
// stand-in for an exit's hysteria2 client — and returns its port and the number of relayed
// connections. Liveness dials (connect + close) are simply dropped.
func newFakeSocksConnect(t *testing.T) (int, *atomic.Int32) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	var relayed atomic.Int32
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go serveFakeSocksConnect(c, &relayed)
		}
	}()
	return ln.Addr().(*net.TCPAddr).Port, &relayed
}

func serveFakeSocksConnect(c net.Conn, relayed *atomic.Int32) {
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(10 * time.Second))
	var hdr [2]byte
	if _, err := io.ReadFull(c, hdr[:]); err != nil {
		return
	}
	if _, err := io.ReadFull(c, make([]byte, hdr[1])); err != nil {
		return
	}
	if _, err := c.Write([]byte{5, 0}); err != nil {
		return
	}
	var req [4]byte
	if _, err := io.ReadFull(c, req[:]); err != nil || req[1] != 1 {
		return
	}
	var host string
	switch req[3] {
	case 1, 4:
		b := make([]byte, map[byte]int{1: 4, 4: 16}[req[3]])
		if _, err := io.ReadFull(c, b); err != nil {
			return
		}
		host = net.IP(b).String()
	case 3:
		var l [1]byte
		if _, err := io.ReadFull(c, l[:]); err != nil {
			return
		}
		b := make([]byte, l[0])
		if _, err := io.ReadFull(c, b); err != nil {
			return
		}
		host = string(b)
	default:
		return
	}
	var pb [2]byte
	if _, err := io.ReadFull(c, pb[:]); err != nil {
		return
	}
	target, err := net.Dial("tcp", net.JoinHostPort(host, strconv.Itoa(int(binary.BigEndian.Uint16(pb[:])))))
	if err != nil {
		_, _ = c.Write([]byte{5, 5, 0, 1, 0, 0, 0, 0, 0, 0})
		return
	}
	defer target.Close()
	if _, err := c.Write([]byte{5, 0, 0, 1, 0, 0, 0, 0, 0, 0}); err != nil {
		return
	}
	relayed.Add(1)
	go func() { _, _ = io.Copy(target, c) }()
	_, _ = io.Copy(c, target)
}

// deliveryWorld wires the real Telegram delivery path (credentials source → route → exit SOCKS
// → Bot API) to fakes: a Bot API server, one exit SOCKS, and switchable DB behaviour.
type deliveryWorld struct {
	tgHits    atomic.Int32
	tgTexts   chan string
	socksPort int
	relayed   *atomic.Int32
	dbMode    atomic.Value // "up" | "refusing" | "hung"
	release   chan struct{}
	exitReads atomic.Int32
	fs        *fakeSettings
}

func newDeliveryWorld(t *testing.T) *deliveryWorld {
	t.Helper()
	w := &deliveryWorld{tgTexts: make(chan string, 16), release: make(chan struct{})}
	w.dbMode.Store("up")
	t.Cleanup(func() { close(w.release) }) // let abandoned "hung" reads finish

	tg := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		w.tgHits.Add(1)
		w.tgTexts <- string(body)
		_, _ = rw.Write([]byte(`{"ok":true,"result":{}}`))
	}))
	t.Cleanup(tg.Close)
	w.socksPort, w.relayed = newFakeSocksConnect(t)

	oldBase, oldRoute, oldSrc, oldList, oldTimeout := telegramAPIBase, telegram, alertTelegram, listHealthyExitNodes, exitListDeliveryTimeout
	oldCache := exitSocksPortsCache.Load()
	t.Cleanup(func() {
		telegramAPIBase, telegram, alertTelegram, listHealthyExitNodes, exitListDeliveryTimeout = oldBase, oldRoute, oldSrc, oldList, oldTimeout
		exitSocksPortsCache.Store(oldCache)
	})
	telegramAPIBase = tg.URL
	exitSocksPortsCache.Store(nil)
	exitListDeliveryTimeout = 200 * time.Millisecond

	// db stands for PostgreSQL: refusing fails at once, hung blocks until the test ends
	db := func() error {
		switch w.dbMode.Load() {
		case "refusing":
			return errors.New("dial tcp 127.0.0.1:5432: connect: connection refused")
		case "hung":
			<-w.release
			return errors.New("read tcp 127.0.0.1:5432: i/o timeout")
		}
		return nil
	}
	listHealthyExitNodes = func() ([]biz.ExitNodeTunnel, error) {
		w.exitReads.Add(1)
		if err := db(); err != nil {
			return nil, err
		}
		return []biz.ExitNodeTunnel{{ID: w.socksPort - 1088, TunnelProtocol: "hysteria2"}}, nil
	}
	w.fs = &fakeSettings{values: map[string]string{alertTokenSetting: fakeBotToken, alertChatIDSetting: "-100"}}
	alertTelegram = newAlertTelegramSource(func(key string) (string, error) {
		if err := db(); err != nil {
			return "", err
		}
		return w.fs.get(key)
	})
	alertTelegram.timeout = 200 * time.Millisecond
	// api.telegram.org is not reachable directly from the Hub: only the exit path can work
	telegram = &tgapi.Route{
		Now: time.Now,
		Direct: func(context.Context, *tgapi.Request) ([]byte, error) {
			return nil, errors.New("dial tcp 149.154.167.220:443: i/o timeout")
		},
		ViaExit: telegramExitPath,
	}
	return w
}

// raiseWithin raises through a harness manager wired to the real delivery path and fails the
// test if the delivery worker does not come back within limit.
func raiseWithin(t *testing.T, h *alertHarness, limit time.Duration, key, title string) time.Duration {
	t.Helper()
	start := time.Now()
	done := make(chan struct{})
	go func() {
		h.m.Raise(key, AlertCritical, title, "d")
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(limit):
		t.Fatalf("delivery of %s is blocked on the DB (> %v)", key, limit)
	}
	return time.Since(start)
}

// I-2: with PostgreSQL refusing or hung, the db-unreachable alert still reaches Telegram through
// the exit known from the last good read, and the delivery worker is never stuck.
func TestAlertDeliveredViaCachedExitWhileDBDown(t *testing.T) {
	for _, mode := range []string{"refusing", "hung"} {
		t.Run(mode, func(t *testing.T) {
			w := newDeliveryWorld(t)
			warmAlertCaches() // API start: the DB answers, both caches are filled
			if c := exitSocksPortsCache.Load(); c == nil || len(*c) != 1 || (*c)[0] != w.socksPort {
				t.Fatalf("exit cache after warm-up: %v", c)
			}

			w.dbMode.Store(mode)
			h := newAlertHarness()
			h.m.send = sendTelegramAlert
			took := raiseWithin(t, h, 5*time.Second, dbAlertKey, "База данных недоступна")

			if len(h.events) != 1 || !h.events[0].Delivered {
				t.Fatalf("the alert must be delivered via the cached exit: %+v", h.events)
			}
			if w.tgHits.Load() != 1 || w.relayed.Load() < 1 {
				t.Fatalf("Bot API hits %d, SOCKS relays %d: the message must go through the exit", w.tgHits.Load(), w.relayed.Load())
			}
			if text := <-w.tgTexts; !strings.Contains(text, "База данных недоступна") || !strings.Contains(text, `"chat_id":"-100"`) {
				t.Fatalf("message: %s", text)
			}
			if w.exitReads.Load() < 2 {
				t.Fatalf("the delivery path must try the DB first (reads: %d)", w.exitReads.Load())
			}
			if took > 3*time.Second {
				t.Fatalf("delivery took %v: the DB reads are not bounded", took)
			}
		})
	}
}

// Without a cached exit list a DB failure is reported as such, quickly.
func TestDeliveryExitWithoutCacheReportsDB(t *testing.T) {
	w := newDeliveryWorld(t)
	w.dbMode.Store("hung")
	start := time.Now()
	_, err := pickDeliveryExitSocksPort()
	if err == nil || !strings.Contains(err.Error(), "список exit-нод из БД") || errors.Is(err, errNoLiveExit) {
		t.Fatalf("want the DB error, got %v", err)
	}
	if took := time.Since(start); took > 2*time.Second {
		t.Fatalf("a hung DB must not block the delivery path: %v", took)
	}
	err = telegramSendViaExit(fakeBotToken, "-100", "x")
	if err == nil || errors.Is(err, errNoExitRoute) || !strings.Contains(err.Error(), "exit для обхода не выбран") {
		t.Fatalf("telegramSendViaExit must name the DB, not 'no exit': %v", err)
	}
}

// The monitors refresh the cache on every successful read; their own reads have no fallback.
func TestLiveExitSocksPortsRefreshesCacheWithoutFallback(t *testing.T) {
	w := newDeliveryWorld(t)
	ports, err := liveExitSocksPorts(3)
	if err != nil || len(ports) != 1 || ports[0] != w.socksPort {
		t.Fatalf("live ports %v, %v", ports, err)
	}
	if c := exitSocksPortsCache.Load(); c == nil || (*c)[0] != w.socksPort {
		t.Fatalf("a successful monitor read must refresh the cache: %v", c)
	}
	w.dbMode.Store("refusing")
	if _, err := liveExitSocksPorts(3); err == nil {
		t.Fatal("a probe must not run on a stale exit list")
	}
}

// Journal writes never hold up delivery: with the DB hung the writer blocks, the queue fills,
// further rows are dropped with a log line, and every alert is still delivered at once.
func TestAlertDeliveryNeverWaitsForJournal(t *testing.T) {
	release := make(chan struct{})
	var mu sync.Mutex
	var written []string
	j := newAlertJournal(func(ev alertEvent) {
		<-release
		mu.Lock()
		written = append(written, ev.Key)
		mu.Unlock()
	}, 2)
	var sent atomic.Int32
	m := &alertManager{
		now:      time.Now,
		send:     func(string) error { sent.Add(1); return nil },
		record:   j.record,
		cooldown: 30 * time.Minute,
		state:    map[string]*alertState{},
		loaded:   true,
	}
	m.startWorker(16)
	keys := []string{"a", "b", "c", "d", "e", "f"}
	for _, k := range keys {
		m.Raise(k, AlertWarn, "T", "")
	}
	flushed := make(chan struct{})
	go func() { m.flush(); close(flushed) }()
	select {
	case <-flushed:
	case <-time.After(3 * time.Second):
		t.Fatal("the delivery worker waits for the journal")
	}
	if sent.Load() != int32(len(keys)) {
		t.Fatalf("sent %d of %d", sent.Load(), len(keys))
	}
	dropped := j.dropped.Load()
	if dropped < 1 || dropped > int64(len(keys)) {
		t.Fatalf("with the writer stuck and a queue of 2, some rows must be dropped: %d", dropped)
	}

	close(release) // the DB is back: queued rows are written, in order
	want := len(keys) - int(dropped)
	deadline := time.Now().Add(3 * time.Second)
	for {
		mu.Lock()
		n := len(written)
		got := append([]string(nil), written...)
		mu.Unlock()
		if n == want {
			for i := 1; i < len(got); i++ {
				if got[i-1] >= got[i] {
					t.Fatalf("journal rows out of order: %v", got)
				}
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("written %v, want %d rows", got, want)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// M-3: a read that started before an admin's PUT must not put the old credentials back into
// the cache after afterWrite stored the new ones.
func TestAlertTelegramStaleReadCannotOverwriteWrite(t *testing.T) {
	fs := &fakeSettings{values: map[string]string{alertTokenSetting: fakeBotToken, alertChatIDSetting: "-100"}}
	var arm atomic.Bool
	reached, release := make(chan struct{}), make(chan struct{})
	src := newAlertTelegramSource(func(key string) (string, error) {
		v, err := fs.get(key) // the value as of the read
		if key == alertChatIDSetting && arm.CompareAndSwap(true, false) {
			close(reached)
			<-release // slow DB: the result arrives after the PUT
		}
		return v, err
	})
	src.timeout = 5 * time.Second
	if _, err := src.fresh(); err != nil { // cache: -100
		t.Fatal(err)
	}

	arm.Store(true)
	stale := make(chan alertTelegramConfig)
	go func() {
		cfg, _ := src.fresh() // reads -100, then stalls
		stale <- cfg
	}()
	<-reached
	if err := fs.set(alertChatIDSetting, "-200"); err != nil { // the admin's PUT
		t.Fatal(err)
	}
	src.afterWrite(alertSettingsUpdate{chatID: "-200", setChatID: true})
	close(release)
	if cfg := <-stale; cfg.chatID != "-100" {
		t.Fatalf("setup: the stale read must have seen the old value: %+v", cfg)
	}

	fs.down = true
	cfg, err := src.load()
	if err != nil || cfg.chatID != "-200" {
		t.Fatalf("the stale read overwrote the admin's write in the cache: %+v %v", cfg, err)
	}
}
