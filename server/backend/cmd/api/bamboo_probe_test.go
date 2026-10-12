package main

import (
	"bytes"
	crand "crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestSalamanderVector(t *testing.T) {
	// Computed with Python hashlib.blake2b(digest_size=32).
	psk := []byte("test-psk")
	salt := []byte{1, 2, 3, 4, 5, 6, 7, 8}
	key := salamanderKey(psk, salt)
	if got, want := hex.EncodeToString(key[:]), "f35f34d5ed3755dc6677b5da8c502e5fd70f30efcf603e0baa03561e440e1f4d"; got != want {
		t.Fatalf("key %s, want %s", got, want)
	}
	out, err := salamanderObfuscate(bytes.NewReader(salt), psk, []byte("hello quic"))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := hex.EncodeToString(out), "01020304050607089b3a58b9821724a90f14"; got != want {
		t.Fatalf("obfuscated %s, want %s", got, want)
	}
	plain, err := salamanderDeobfuscate(psk, out)
	if err != nil || string(plain) != "hello quic" {
		t.Fatalf("deobfuscated %q, %v", plain, err)
	}
}

func TestSalamanderRoundtrip(t *testing.T) {
	// psk with spare capacity: deriving the key must not write the salt into it.
	psk := make([]byte, 8, 64)
	copy(psk, "test-psk")
	spare := psk[:cap(psk)]
	for i := len(psk); i < len(spare); i++ {
		spare[i] = 0xEE
	}
	before := bytes.Clone(spare)

	for _, n := range []int{1, 31, 32, 33, 1200} {
		in := make([]byte, n)
		crand.Read(in)
		out, err := salamanderObfuscate(crand.Reader, psk, in)
		if err != nil {
			t.Fatal(err)
		}
		if len(out) != salamanderSaltLen+n {
			t.Fatalf("n=%d: len %d", n, len(out))
		}
		got, err := salamanderDeobfuscate(psk, out)
		if err != nil || !bytes.Equal(got, in) {
			t.Fatalf("n=%d: roundtrip mismatch (err %v)", n, err)
		}
		if n == 1200 {
			if bytes.Equal(out[salamanderSaltLen:], in) {
				t.Fatal("payload not obfuscated")
			}
			if other, _ := salamanderDeobfuscate([]byte("other-psk"), out); bytes.Equal(other, in) {
				t.Fatal("wrong psk deobfuscated to the original")
			}
		}
	}
	if !bytes.Equal(spare, before) {
		t.Fatal("salamander wrote into the spare capacity of psk")
	}
	if _, err := salamanderObfuscate(bytes.NewReader([]byte{1, 2, 3}), psk, []byte("x")); err == nil {
		t.Fatal("short salt read must be an error")
	}
}

func TestSalamanderDeobfuscateShort(t *testing.T) {
	for n := 0; n <= salamanderSaltLen; n++ { // 8 bytes = salt only, rejected like Hysteria does
		if _, err := salamanderDeobfuscate([]byte("psk"), make([]byte, n)); err == nil {
			t.Fatalf("%d bytes: want error", n)
		}
	}
}

// failingReader returns an error at once: buildVNProbePacket must still produce a valid probe.
type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, errors.New("no entropy") }

func TestBuildVNProbePacket(t *testing.T) {
	readers := map[string]io.Reader{
		"crypto": crand.Reader,
		"ones":   bytes.NewReader(bytes.Repeat([]byte{0xFF}, 2000)),
		"zeros":  bytes.NewReader(make([]byte, 2000)),
		"broken": failingReader{},
	}
	for name, r := range readers {
		p := buildVNProbePacket(r)
		if len(p) != 1200 {
			t.Fatalf("%s: len %d", name, len(p))
		}
		if p[0]&0xC0 != 0xC0 {
			t.Fatalf("%s: first byte %#x lacks long header + fixed bit", name, p[0])
		}
		if v := binary.BigEndian.Uint32(p[1:5]); v != 0x1a2a3a4a {
			t.Fatalf("%s: version %#x", name, v)
		}
		if p[5] != 8 || p[14] != 8 {
			t.Fatalf("%s: cid lengths %d/%d", name, p[5], p[14])
		}
		if isVersionNegotiation(p) {
			t.Fatalf("%s: probe looks like VN", name)
		}
	}
	a, b := buildVNProbePacket(crand.Reader), buildVNProbePacket(crand.Reader)
	if bytes.Equal(a[6:14], b[6:14]) {
		t.Fatal("DCID is not random")
	}
}

func TestIsVersionNegotiation(t *testing.T) {
	cases := []struct {
		name string
		p    []byte
		want bool
	}{
		{"vn", testVNReply(buildVNProbePacket(crand.Reader)), true},
		{"minimal vn", []byte{0x80, 0, 0, 0, 0, 0, 0}, true},
		{"vn, any low bits", []byte{0xFF, 0, 0, 0, 0, 0, 0}, true},
		{"too short", []byte{0x80, 0, 0, 0, 0, 0}, false},
		{"empty", nil, false},
		{"short header", []byte{0x40, 0, 0, 0, 0, 0, 0}, false},
		{"long header v1", []byte{0xC0, 0, 0, 0, 1, 0, 0}, false},
		{"our probe", buildVNProbePacket(crand.Reader), false},
	}
	for _, tc := range cases {
		if got := isVersionNegotiation(tc.p); got != tc.want {
			t.Fatalf("%s: %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestSocksUDPHeader(t *testing.T) {
	cases := []struct {
		host string
		port int
		hex  string
		from string
	}{
		{"138.16.181.225", 41234, "000000018a10b5e1a112", "138.16.181.225:41234"},
		{"hiearth.ru", 50443, "00000003" + "0a" + hex.EncodeToString([]byte("hiearth.ru")) + "c50b", "hiearth.ru:50443"},
		{"2001:db8::1", 443, "00000004" + "20010db8000000000000000000000001" + "01bb", "[2001:db8::1]:443"},
	}
	for _, tc := range cases {
		h, err := socksUDPHeader(tc.host, tc.port)
		if err != nil {
			t.Fatalf("%s: %v", tc.host, err)
		}
		if got := hex.EncodeToString(h); got != tc.hex {
			t.Fatalf("%s: header %s, want %s", tc.host, got, tc.hex)
		}
		from, payload, err := parseSocksUDPDatagram(append(h, "payload"...))
		if err != nil || from != tc.from || string(payload) != "payload" {
			t.Fatalf("%s: parsed %q %q %v", tc.host, from, payload, err)
		}
	}
	for _, bad := range []struct {
		host string
		port int
	}{{"", 443}, {strings.Repeat("a", 256), 443}, {"1.2.3.4", 0}, {"1.2.3.4", 65536}} {
		if _, err := socksUDPHeader(bad.host, bad.port); err == nil {
			t.Fatalf("%q:%d: want error", bad.host, bad.port)
		}
	}
	// an empty payload after a valid header is fine
	if from, payload, err := parseSocksUDPDatagram([]byte{0, 0, 0, 1, 127, 0, 0, 1, 0, 53}); err != nil || from != "127.0.0.1:53" || len(payload) != 0 {
		t.Fatalf("empty payload: %q %q %v", from, payload, err)
	}
	for name, p := range map[string][]byte{
		"short":            {0, 0, 0},
		"fragment":         {0, 0, 1, 1, 127, 0, 0, 1, 0, 53, 'x'},
		"unknown atyp":     {0, 0, 0, 9, 127, 0, 0, 1, 0, 53},
		"truncated ipv4":   {0, 0, 0, 1, 127, 0, 0, 1, 0},
		"truncated domain": {0, 0, 0, 3, 10, 'h', 'i', 0, 53},
		"no domain len":    {0, 0, 0, 3},
		"truncated ipv6":   {0, 0, 0, 4, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 0},
	} {
		if _, _, err := parseSocksUDPDatagram(p); err == nil {
			t.Fatalf("%s: want error", name)
		}
	}
}

// testVNReply builds the Version Negotiation packet a QUIC server sends for probe:
// DCID/SCID swapped, then the list of supported versions.
func testVNReply(probe []byte) []byte {
	dcid, scid := probe[6:14], probe[15:23]
	vn := []byte{0x80 | 0x2a, 0, 0, 0, 0, 8}
	vn = append(vn, scid...)
	vn = append(vn, 8)
	vn = append(vn, dcid...)
	return append(vn, 0, 0, 0, 1, 0x6b, 0x33, 0x43, 0xcf) // QUIC v1, v2
}

// fakeBambooSOCKS is a no-auth SOCKS5 server that only does UDP ASSOCIATE. Its UDP relay
// plays the Hub's Hysteria too: it checks the salamander-obfuscated probe and answers with
// whatever respond returns (nil = silence), wrapped in a SOCKS UDP header.
type fakeBambooSOCKS struct {
	t               *testing.T
	port            int
	psk             []byte
	respond         func(assoc int, probe []byte) []byte
	rejectAssociate bool
	bindUnspecified bool // BND.ADDR 0.0.0.0 in the ASSOCIATE reply

	mu       sync.Mutex
	assocs   int
	perAssoc map[int][]string // destinations received, per association
	problems []string
}

func newFakeBambooSOCKS(t *testing.T, psk []byte, respond func(assoc int, probe []byte) []byte) *fakeBambooSOCKS {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	s := &fakeBambooSOCKS{t: t, port: ln.Addr().(*net.TCPAddr).Port, psk: psk, respond: respond, perAssoc: map[int][]string{}}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go s.serve(c)
		}
	}()
	return s
}

func (s *fakeBambooSOCKS) problem(format string, args ...any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.problems = append(s.problems, fmt.Sprintf(format, args...))
}

func (s *fakeBambooSOCKS) serve(c net.Conn) {
	defer c.Close()
	var b [10]byte
	if _, err := io.ReadFull(c, b[:3]); err != nil || !bytes.Equal(b[:3], []byte{5, 1, 0}) {
		s.problem("greeting % x (%v)", b[:3], err)
		return
	}
	c.Write([]byte{5, 0})
	if _, err := io.ReadFull(c, b[:10]); err != nil || !bytes.Equal(b[:4], []byte{5, 3, 0, 1}) {
		s.problem("associate request % x (%v)", b[:10], err)
		return
	}
	if s.rejectAssociate {
		c.Write([]byte{5, 7, 0, 1, 0, 0, 0, 0, 0, 0}) // command not supported
		return
	}
	relay, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		s.problem("relay: %v", err)
		return
	}
	defer relay.Close()
	s.mu.Lock()
	s.assocs++
	assoc := s.assocs
	s.mu.Unlock()

	reply := []byte{5, 0, 0, 1, 127, 0, 0, 1, 0, 0}
	if s.bindUnspecified {
		copy(reply[4:8], []byte{0, 0, 0, 0})
	}
	binary.BigEndian.PutUint16(reply[8:], uint16(relay.LocalAddr().(*net.UDPAddr).Port))
	c.Write(reply)
	go s.relay(relay, assoc)
	_, _ = io.Copy(io.Discard, c) // the association lives while the control connection is open
}

func (s *fakeBambooSOCKS) relay(relay *net.UDPConn, assoc int) {
	buf := make([]byte, 4096)
	for {
		n, client, err := relay.ReadFromUDP(buf)
		if err != nil {
			return
		}
		dst, payload, err := parseSocksUDPDatagram(buf[:n])
		if err != nil {
			s.problem("datagram header: %v", err)
			continue
		}
		s.mu.Lock()
		s.perAssoc[assoc] = append(s.perAssoc[assoc], dst)
		s.mu.Unlock()
		host, portStr, _ := net.SplitHostPort(dst)
		port, _ := strconv.Atoi(portStr)
		if port < 30000 || port > 65000 {
			s.problem("destination port %d outside the hopping range", port)
		}
		probe, err := salamanderDeobfuscate(s.psk, payload)
		if err != nil {
			s.problem("deobfuscate: %v", err)
			continue
		}
		if len(probe) != 1200 || probe[0]&0xC0 != 0xC0 || binary.BigEndian.Uint32(probe[1:5]) != 0x1a2a3a4a {
			s.problem("not a VN probe: %d bytes, % x", len(probe), probe[:min(len(probe), 8)])
			continue
		}
		out := s.respond(assoc, probe)
		if out == nil {
			continue
		}
		hdr, err := socksUDPHeader(host, port)
		if err != nil {
			s.problem("reply header: %v", err)
			continue
		}
		relay.WriteToUDP(append(hdr, out...), client)
	}
}

func (s *fakeBambooSOCKS) check(t *testing.T, wantAssocs int) {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.problems) > 0 {
		t.Fatalf("fake SOCKS saw problems: %s", strings.Join(s.problems, "; "))
	}
	if s.assocs != wantAssocs {
		t.Fatalf("associations %d, want %d", s.assocs, wantAssocs)
	}
	for assoc, dsts := range s.perAssoc {
		if len(dsts) != 1 { // one destination port per association (the Hub's DNAT constraint)
			t.Fatalf("association %d got %d datagrams: %v", assoc, len(dsts), dsts)
		}
	}
}

func obfuscatedVN(psk []byte) func(int, []byte) []byte {
	return func(_ int, probe []byte) []byte {
		out, _ := salamanderObfuscate(crand.Reader, psk, testVNReply(probe))
		return out
	}
}

func TestProbeBambooEndToEnd(t *testing.T) {
	psk := []byte("bamboo-obfs")
	fast := bambooProber{socksHost: "127.0.0.1", attempts: 2, attemptTimeout: 300 * time.Millisecond, rand: crand.Reader}

	t.Run("vn reply, ipv4 host", func(t *testing.T) {
		s := newFakeBambooSOCKS(t, psk, obfuscatedVN(psk))
		rtt, port, err := probeBamboo(s.port, "138.16.181.225", psk) // the production entry point
		if err != nil {
			t.Fatal(err)
		}
		// rtt may be 0 on loopback: Windows' monotonic clock is coarse.
		if rtt < 0 || rtt > defaultBambooProber.attemptTimeout || port < 30000 || port > 65000 {
			t.Fatalf("rtt %v port %d", rtt, port)
		}
		s.check(t, 1)
	})

	t.Run("vn reply, domain host, relay bound to 0.0.0.0", func(t *testing.T) {
		s := newFakeBambooSOCKS(t, psk, obfuscatedVN(psk))
		s.bindUnspecified = true
		if _, _, err := fast.probe(s.port, "hiearth.ru", psk); err != nil {
			t.Fatal(err)
		}
		s.check(t, 1)
	})

	t.Run("first datagram lost, retry succeeds", func(t *testing.T) {
		vn := obfuscatedVN(psk)
		s := newFakeBambooSOCKS(t, psk, func(assoc int, probe []byte) []byte {
			if assoc == 1 {
				return nil
			}
			return vn(assoc, probe)
		})
		if _, _, err := fast.probe(s.port, "138.16.181.225", psk); err != nil {
			t.Fatal(err)
		}
		s.check(t, 2)
	})

	t.Run("no reply", func(t *testing.T) {
		s := newFakeBambooSOCKS(t, psk, func(int, []byte) []byte { return nil })
		start := time.Now()
		_, port, err := fast.probe(s.port, "138.16.181.225", psk)
		elapsed := time.Since(start)
		if !errors.Is(err, errBambooNoReply) {
			t.Fatalf("err %v, want no reply", err)
		}
		if !strings.Contains(err.Error(), ":"+strconv.Itoa(port)) {
			t.Fatalf("error %q does not name port %d", err, port)
		}
		if limit := 2*fast.attemptTimeout + time.Second; elapsed > limit {
			t.Fatalf("gave up after %v, limit %v", elapsed, limit)
		}
		if elapsed < 2*fast.attemptTimeout-50*time.Millisecond {
			t.Fatalf("gave up after %v: did not wait for both attempts", elapsed)
		}
		s.check(t, 2)
	})

	garbage := map[string]func(int, []byte) []byte{
		"obfuscated short-header packet": func(int, []byte) []byte {
			out, _ := salamanderObfuscate(crand.Reader, psk, append([]byte{0x40}, make([]byte, 40)...))
			return out
		},
		"vn obfuscated with another psk": obfuscatedVN([]byte("other-psk")),
		"too short for salamander":       func(int, []byte) []byte { return []byte{1, 2, 3} },
	}
	for name, respond := range garbage {
		t.Run("wrong reply: "+name, func(t *testing.T) {
			s := newFakeBambooSOCKS(t, psk, respond)
			_, _, err := fast.probe(s.port, "138.16.181.225", psk)
			if !errors.Is(err, errBambooWrongReply) {
				t.Fatalf("err %v, want wrong reply", err)
			}
			s.check(t, 2)
		})
	}

	t.Run("associate rejected", func(t *testing.T) {
		s := newFakeBambooSOCKS(t, psk, obfuscatedVN(psk))
		s.rejectAssociate = true
		_, _, err := fast.probe(s.port, "138.16.181.225", psk)
		if !errors.Is(err, errBambooSocks) || !strings.Contains(err.Error(), "код 7") {
			t.Fatalf("err %v, want SOCKS failure with code 7", err)
		}
	})

	t.Run("socks port closed", func(t *testing.T) {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		port := ln.Addr().(*net.TCPAddr).Port
		ln.Close()
		start := time.Now()
		_, _, err = fast.probe(port, "138.16.181.225", psk)
		if !errors.Is(err, errBambooSocks) {
			t.Fatalf("err %v, want SOCKS failure", err)
		}
		if elapsed := time.Since(start); elapsed > 2*fast.attemptTimeout+time.Second {
			t.Fatalf("gave up after %v", elapsed)
		}
	})
}
