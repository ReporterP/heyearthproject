package mtprobe

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"io"
	"math/big"
	"net"
	"os"
	"slices"
	"testing"
	"time"
)

const testSecret = "ee00112233445566778899aabbccddeeff6578616d706c652e6f7267" // example.org

// fakeMTG is mtg's server side of the fake-TLS handshake (mtglib/internal/tls/fake: ReadClientHello,
// SendServerHello), reimplemented: it checks the record framing, the SNI, the client random's HMAC
// and the time like mtg and answers ServerHello + ChangeCipherSpec + application data with the
// HMAC in the ServerHello random. A hello it rejects is closed, as mtg's fronting to a dead site.
func fakeMTG(t *testing.T, sec Secret, skew time.Duration) string {
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
			go func() {
				defer c.Close()
				_ = c.SetDeadline(time.Now().Add(5 * time.Second))
				random, ok := mtgReadHello(c, sec, skew)
				if !ok {
					return
				}
				_, _ = c.Write(mtgServerHello(sec, random))
				_, _ = io.Copy(io.Discard, c)
			}()
		}
	}()
	return ln.Addr().String()
}

func mtgReadHello(r io.Reader, sec Secret, skew time.Duration) ([32]byte, bool) {
	var random [32]byte
	var h [5]byte
	if _, err := io.ReadFull(r, h[:]); err != nil || h[0] != 0x16 || h[1] != 3 || h[2] != 1 {
		return random, false
	}
	rec := make([]byte, 5+int(binary.BigEndian.Uint16(h[3:])))
	copy(rec, h[:])
	if _, err := io.ReadFull(r, rec[5:]); err != nil || rec[5] != 0x01 {
		return random, false
	}
	copy(random[:], rec[11:43])
	if !bytes.Contains(rec, append([]byte{0x00, byte(len(sec.Host))}, sec.Host...)) {
		return random, false // SNI
	}
	zeroed := slices.Clone(rec)
	clear(zeroed[11:43])
	mac := hmac.New(sha256.New, sec.Key[:])
	mac.Write(zeroed)
	d := mac.Sum(nil)
	for i := range 32 {
		d[i] ^= random[i]
	}
	if !bytes.Equal(d[:28], make([]byte, 28)) {
		return random, false
	}
	ts := time.Unix(int64(binary.LittleEndian.Uint32(d[28:])), 0)
	if time.Since(ts).Abs() > skew {
		return random, false
	}
	return random, true
}

func mtgServerHello(sec Secret, clientRandom [32]byte) []byte {
	var p bytes.Buffer
	body := []byte{0x03, 0x03}
	body = append(body, make([]byte, 32)...)
	body = append(body, 0, 0x13, 0x01, 0x00)
	hs := append([]byte{0x02, 0, byte(len(body) >> 8), byte(len(body))}, body...)
	p.Write([]byte{0x16, 0x03, 0x03, byte(len(hs) >> 8), byte(len(hs))})
	p.Write(hs)
	p.Write([]byte{0x14, 0x03, 0x03, 0x00, 0x01, 0x01})
	noise := make([]byte, 2600)
	_, _ = rand.Read(noise)
	p.Write([]byte{0x17, 0x03, 0x03, byte(len(noise) >> 8), byte(len(noise))})
	p.Write(noise)
	packet := p.Bytes()
	mac := hmac.New(sha256.New, sec.Key[:])
	mac.Write(clientRandom[:])
	mac.Write(packet)
	copy(packet[11:], mac.Sum(nil))
	return packet
}

func probe(t *testing.T, addr string, sec Secret, timeout time.Duration) error {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return Handshake(ctx, nil, addr, sec)
}

func TestParseSecret(t *testing.T) {
	sec, err := ParseSecret(testSecret)
	if err != nil || sec.Host != "example.org" || hex.EncodeToString(sec.Key[:]) != "00112233445566778899aabbccddeeff" {
		t.Fatalf("%+v %v", sec, err)
	}
	for _, bad := range []string{"", "dd00112233445566778899aabbccddeeff6578", "ee0011", "ee00112233445566778899aabbccddeeffzz"} {
		if _, err := ParseSecret(bad); err == nil {
			t.Errorf("%q parsed", bad)
		}
	}
}

func TestHandshake(t *testing.T) {
	sec, _ := ParseSecret(testSecret)
	if err := probe(t, fakeMTG(t, sec, 3*time.Second), sec, 3*time.Second); err != nil {
		t.Fatalf("live mtg: %v", err)
	}

	// The node runs another secret (or the clocks differ): mtg fronts the hello; here the fronted
	// site is gone and the connection closes.
	other := sec
	other.Key[0] ^= 1
	if err := probe(t, fakeMTG(t, other, 3*time.Second), sec, 3*time.Second); !errors.Is(err, ErrNotMTG) {
		t.Fatalf("other secret: %v", err)
	}
	otherHost := sec
	otherHost.Host = "example.net"
	if err := probe(t, fakeMTG(t, otherHost, 3*time.Second), sec, 3*time.Second); !errors.Is(err, ErrNotMTG) {
		t.Fatalf("other domain: %v", err)
	}

	// The 13-14.11.2026 failure: the port listens, the kernel completes TCP, nobody accepts.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	start := time.Now()
	if err := probe(t, ln.Addr().String(), sec, 1500*time.Millisecond); !errors.Is(err, ErrNoAnswer) {
		t.Fatalf("listening, not accepting: %v", err)
	}
	if d := time.Since(start); d > 3*time.Second {
		t.Fatalf("the probe ignored its deadline: %s", d)
	}

	// Nothing listens: a TCP error.
	addr := ln.Addr().String()
	ln.Close()
	if err := probe(t, addr, sec, time.Second); err == nil || errors.Is(err, ErrNoAnswer) || errors.Is(err, ErrNotMTG) {
		t.Fatalf("closed port: %v", err)
	}
}

// A real TLS server on the port (the fronting site itself, nginx, …) answers the hello, but not
// with mtg's HMAC.
func TestHandshakeRealTLSServer(t *testing.T) {
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "example.org"}, DNSNames: []string{"example.org"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour)}
	der, _ := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	ln, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}})
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
			go func() { defer c.Close(); _ = c.(*tls.Conn).Handshake() }()
		}
	}()
	sec, _ := ParseSecret(testSecret)
	if err := probe(t, ln.Addr().String(), sec, 3*time.Second); !errors.Is(err, ErrNotMTG) {
		t.Fatalf("real TLS server: %v", err)
	}
}

// The clock check: a hello stamped outside mtg's 3 s tolerance is not answered by mtg.
func TestClientHelloTimestamp(t *testing.T) {
	sec, _ := ParseSecret(testSecret)
	for _, c := range []struct {
		age  time.Duration
		want bool
	}{{0, true}, {2 * time.Second, true}, {10 * time.Second, false}, {-10 * time.Second, false}} {
		rec, random, err := ClientHello(sec, time.Now().Add(-c.age))
		if err != nil {
			t.Fatal(err)
		}
		got, ok := mtgReadHello(bytes.NewReader(rec), sec, 3*time.Second)
		if ok != c.want || (ok && got != random) {
			t.Errorf("age %s: accepted=%v", c.age, ok)
		}
		if len(rec) != clientHelloPadding {
			t.Errorf("record of %d bytes, want %d", len(rec), clientHelloPadding)
		}
	}
	if _, _, err := ClientHello(Secret{}, time.Now()); err == nil {
		t.Error("a secret without a domain built a hello")
	}
}

// Against a real mtg: MTPROBE_ADDR=127.0.0.1:18443 MTPROBE_SECRET=ee… go test -run TestRealMTG.
// MTPROBE_EXPECT=fail flips the expectation (a wrong secret, a mtg that stopped accepting).
func TestRealMTG(t *testing.T) {
	addr, s := os.Getenv("MTPROBE_ADDR"), os.Getenv("MTPROBE_SECRET")
	if addr == "" || s == "" {
		t.Skip("MTPROBE_ADDR / MTPROBE_SECRET not set")
	}
	sec, err := ParseSecret(s)
	if err != nil {
		t.Fatal(err)
	}
	err = probe(t, addr, sec, 6*time.Second)
	t.Logf("probe %s: %v", addr, err)
	if want := os.Getenv("MTPROBE_EXPECT") != "fail"; (err == nil) != want {
		t.Fatalf("probe %s: %v (want ok=%v)", addr, err, want)
	}
}
