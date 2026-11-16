package main

import (
	"context"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func newF(targets []Target, dial func(ctx context.Context, socks, addr string) (net.Conn, error)) *forwarder {
	f := &forwarder{resting: map[string]time.Time{}, now: time.Now, dial: dial}
	f.set(targets)
	return f
}

var two = []Target{
	{Exit: 2, Addr: "198.51.100.2:3398", Socks: []string{"127.0.0.1:1090", "127.0.0.1:2090"}},
	{Exit: 3, Addr: "198.51.100.3:3398", Socks: []string{"127.0.0.1:1091", "127.0.0.1:2091"}},
}

// hysteria fails (choked UDP) → the same exit's ShadowTLS path is used.
func TestFallsBackToSecondPath(t *testing.T) {
	var tried []string
	f := newF(two[:1], func(_ context.Context, socks, addr string) (net.Conn, error) {
		tried = append(tried, socks)
		if socks == "127.0.0.1:1090" {
			return nil, errors.New("refused")
		}
		a, _ := net.Pipe()
		return a, nil
	})
	if _, err := f.open(context.Background()); err != nil {
		t.Fatal(err)
	}
	if strings.Join(tried, ",") != "127.0.0.1:1090,127.0.0.1:2090" {
		t.Fatalf("tried %v", tried)
	}
	// The failed path rests: the next stream starts with the working one.
	tried = nil
	if _, err := f.open(context.Background()); err != nil || tried[0] != "127.0.0.1:2090" {
		t.Fatalf("resting path not moved last: %v %v", tried, err)
	}
}

func TestRotatesAndFailsOver(t *testing.T) {
	first := map[string]int{}
	var firstOfOpen string
	f := newF(two, func(_ context.Context, socks, addr string) (net.Conn, error) {
		if firstOfOpen == "" {
			firstOfOpen = addr
		}
		a, _ := net.Pipe()
		return a, nil
	})
	for range 4 {
		firstOfOpen = ""
		if _, err := f.open(context.Background()); err != nil {
			t.Fatal(err)
		}
		first[firstOfOpen]++
	}
	if first["198.51.100.2:3398"] != 2 || first["198.51.100.3:3398"] != 2 {
		t.Fatalf("no rotation: %v", first)
	}
	// Exit 3 down: every stream still opens (on exit 2).
	f3 := newF(two, func(_ context.Context, _, addr string) (net.Conn, error) {
		if strings.HasPrefix(addr, "198.51.100.3") {
			return nil, errors.New("exit 3 down")
		}
		a, _ := net.Pipe()
		return a, nil
	})
	for range 4 {
		if _, err := f3.open(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	f2 := newF(two, func(context.Context, string, string) (net.Conn, error) { return nil, errors.New("all down") })
	if _, err := f2.open(context.Background()); err == nil {
		t.Fatal("no path, yet a stream")
	}
}

func TestLoadSkipsBadTargets(t *testing.T) {
	p := filepath.Join(t.TempDir(), "b.json")
	os.WriteFile(p, []byte(`{"targets":[{"exit":2,"addr":"198.51.100.2:3398","socks":["127.0.0.1:1090"]},{"exit":3,"addr":"nope","socks":["x"]},{"exit":4,"addr":"198.51.100.4:3398","socks":[]}]}`), 0o600)
	got, err := load(p)
	if err != nil || len(got) != 1 || got[0].Exit != 2 {
		t.Fatalf("%+v %v", got, err)
	}
	os.WriteFile(p, []byte(`{broken`), 0o600)
	if _, err := load(p); err == nil {
		t.Fatal("broken JSON accepted")
	}
}

// The socket is open exactly while there is a target, and a stream is spliced both ways.
func TestListenerFollowsTargetsAndSplices(t *testing.T) {
	up, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer up.Close()
	go func() {
		c, err := up.Accept()
		if err == nil {
			io.Copy(c, c) // echo
			c.Close()
		}
	}()
	f := newF(nil, func(ctx context.Context, _, addr string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, "tcp", addr)
	})
	free, _ := net.Listen("tcp", "127.0.0.1:0")
	addr := free.Addr().String()
	free.Close()
	l := &listener{addr: addr, f: f}
	if err := l.sync(); err != nil {
		t.Fatal(err)
	}
	if c, err := net.DialTimeout("tcp", addr, time.Second); err == nil {
		c.Close()
		t.Fatal("listening without targets")
	}
	f.set([]Target{{Exit: 2, Addr: up.Addr().String(), Socks: []string{"direct"}}})
	if err := l.sync(); err != nil {
		t.Fatal(err)
	}
	c, err := net.DialTimeout("tcp", addr, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	c.Write([]byte("ping"))
	buf := make([]byte, 4)
	c.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := io.ReadFull(c, buf); err != nil || string(buf) != "ping" {
		t.Fatalf("splice: %q %v", buf, err)
	}
	c.Close()
	f.set(nil)
	l.sync()
	if c, err := net.DialTimeout("tcp", addr, time.Second); err == nil {
		c.Close()
		t.Fatal("still listening after the targets went")
	}
}
