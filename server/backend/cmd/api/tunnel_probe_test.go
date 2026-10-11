package main

import (
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"
)

// startTestSOCKS5 runs a minimal no-auth SOCKS5 CONNECT proxy (IPv4 targets) on loopback.
func startTestSOCKS5(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go serveTestSOCKS5(c)
		}
	}()
	return ln.Addr().String()
}

func serveTestSOCKS5(c net.Conn) {
	defer c.Close()
	b := make([]byte, 256)
	if _, err := io.ReadFull(c, b[:2]); err != nil { // VER NMETHODS
		return
	}
	if _, err := io.ReadFull(c, b[:b[1]]); err != nil { // METHODS
		return
	}
	c.Write([]byte{5, 0}) // method: no auth
	// request: VER CMD RSV ATYP; only CONNECT (1) to IPv4 (1) is needed here
	if _, err := io.ReadFull(c, b[:4]); err != nil || b[1] != 1 || b[3] != 1 {
		return
	}
	if _, err := io.ReadFull(c, b[:6]); err != nil { // addr + port
		return
	}
	up, err := net.Dial("tcp", net.JoinHostPort(net.IP(b[:4]).String(), strconv.Itoa(int(b[4])<<8|int(b[5]))))
	if err != nil {
		c.Write([]byte{5, 5, 0, 1, 0, 0, 0, 0, 0, 0})
		return
	}
	defer up.Close()
	c.Write([]byte{5, 0, 0, 1, 0, 0, 0, 0, 0, 0})
	go io.Copy(up, c)
	io.Copy(c, up)
}

func statusServer(t *testing.T, code int) string {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(code) }))
	t.Cleanup(srv.Close)
	return srv.URL
}

func TestProbeViaSOCKS5OK(t *testing.T) {
	lat, err := probeViaSOCKS5(startTestSOCKS5(t), statusServer(t, http.StatusNoContent)+"/generate_204", 2*time.Second)
	if err != nil || lat <= 0 {
		t.Fatalf("probe = (%v, %v), want (>0, nil)", lat, err)
	}
}

func TestProbeViaSOCKS5WrongStatus(t *testing.T) {
	// An error page / captive portal behind the tunnel is not a healthy exit.
	if _, err := probeViaSOCKS5(startTestSOCKS5(t), statusServer(t, http.StatusOK), 2*time.Second); err == nil {
		t.Fatal("status 200 accepted, want error")
	}
}

// C4: the local SOCKS port accepts TCP, but the tunnel behind it is dead.
func TestProbeViaSOCKS5DeadTunnel(t *testing.T) {
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
			go func() { time.Sleep(3 * time.Second); c.Close() }() // never answers
		}
	}()
	start := time.Now()
	if _, err := probeViaSOCKS5(ln.Addr().String(), "https://www.gstatic.com/generate_204", time.Second); err == nil {
		t.Fatal("dead tunnel reported healthy")
	}
	if d := time.Since(start); d > 2500*time.Millisecond {
		t.Fatalf("probe took %v, timeout not enforced", d)
	}
}
