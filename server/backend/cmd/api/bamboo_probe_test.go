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

// ── salamander, QUIC, DNS, SOCKS headers ──

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

func TestVNMirrorsProbe(t *testing.T) {
	probe := buildVNProbePacket(crand.Reader)
	vn := testVNReply(probe)
	if !vnMirrorsProbe(vn, probe) {
		t.Fatal("a proper VN does not mirror the probe")
	}
	if vnMirrorsProbe(testVNReply(buildVNProbePacket(crand.Reader)), probe) {
		t.Fatal("a VN for another probe matched")
	}
	swapped := []byte{0x80, 0, 0, 0, 0, 8}
	swapped = append(append(append(swapped, probe[6:14]...), 8), probe[15:23]...) // CIDs not swapped back
	if vnMirrorsProbe(swapped, probe) {
		t.Fatal("unswapped CIDs matched")
	}
	for _, n := range []int{0, 6, 7, 14, 15, 22} {
		if vnMirrorsProbe(vn[:n], probe) {
			t.Fatalf("truncated to %d bytes matched", n)
		}
	}
	if vnMirrorsProbe([]byte{0x80, 0, 0, 0, 0, 200, 1, 2}, probe) {
		t.Fatal("DCID length beyond the packet matched")
	}
}

func TestDNSControlPacket(t *testing.T) {
	q := buildDNSQuery(0x1234, "example.com")
	want := "123401000001000000000000" + "076578616d706c6503636f6d00" + "00010001"
	if got := hex.EncodeToString(q); got != want {
		t.Fatalf("query %s, want %s", got, want)
	}
	if got := hex.EncodeToString(buildDNSQuery(0x1234, "example.com.")); got != want {
		t.Fatalf("trailing dot: %s", got)
	}
	resp := bytes.Clone(q)
	resp[2] |= 0x80
	cases := []struct {
		name string
		msg  []byte
		want bool
	}{
		{"response", resp, true},
		{"query echoed (QR=0)", q, false},
		{"other id", append([]byte{0x43, 0x21}, resp[2:]...), false},
		{"short", resp[:11], false},
	}
	for _, tc := range cases {
		if got := isDNSResponseTo(0x1234, tc.msg); got != tc.want {
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

func TestReplyFromTarget(t *testing.T) {
	cases := []struct {
		from, host string
		port       int
		want       bool
	}{
		{"138.16.181.225:41234", "138.16.181.225", 41234, true},
		{"[::ffff:138.16.181.225]:41234", "138.16.181.225", 41234, true}, // IPv4-mapped is the same address
		{"138.16.181.225:50443", "138.16.181.225", 41234, false},         // answered from the listener, not the hop port
		{"45.150.39.236:41234", "138.16.181.225", 41234, false},          // answered from another IP of the Hub
		{"hiearth.ru:41234", "138.16.181.225", 41234, false},
		{"1.2.3.4:41234", "hiearth.ru", 41234, true}, // a name: only the port is compared
		{"1.2.3.4:41235", "hiearth.ru", 41234, false},
		{"garbage", "hiearth.ru", 41234, false},
	}
	for _, tc := range cases {
		if got := replyFromTarget(tc.from, tc.host, tc.port); got != tc.want {
			t.Fatalf("from %s to %s:%d: %v, want %v", tc.from, tc.host, tc.port, got, tc.want)
		}
	}
}

func TestBambooEvidence(t *testing.T) {
	wrap := func(e error) error { return fmt.Errorf("%w: x", e) }
	order := []error{wrap(errBambooSocks), errors.New("bad host"), wrap(errBambooNoReply), wrap(errBambooWrongReply), wrap(errBambooWrongSource)}
	for i := 1; i < len(order); i++ {
		if !strongerEvidence(order[i], order[i-1]) || strongerEvidence(order[i-1], order[i]) {
			t.Fatalf("%v must beat %v", order[i], order[i-1])
		}
	}
	if !strongerEvidence(order[0], nil) {
		t.Fatal("anything beats no error yet")
	}
	if strongerEvidence(wrap(errBambooNoReply), wrap(errBambooNoReply)) {
		t.Fatal("among equals the first one is kept")
	}
}

// ── fake SOCKS5 with a UDP relay ──

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

// fakeReply is one datagram the relay sends back.
type fakeReply struct {
	from    string // source named in the SOCKS header; "" = where the request went
	payload []byte
}

// fakeHandler answers a datagram that association n (1-based) sent to dst.
type fakeHandler func(s *fakeBambooSOCKS, n int, dst string, payload []byte) []fakeReply

type fakeSOCKSOpts struct {
	handle    fakeHandler
	greeting  []byte                                 // answer to the greeting; nil = 05 00
	associate func(n int, relay *net.UDPAddr) []byte // ASSOCIATE reply; nil (or nil result) = success naming relay
	relayIPv6 bool                                   // the relay listens on [::1]
}

// fakeBambooSOCKS is a no-auth SOCKS5 server that only does UDP ASSOCIATE; its relay hands
// every datagram to opts.handle instead of forwarding it.
type fakeBambooSOCKS struct {
	port int
	opts fakeSOCKSOpts

	mu       sync.Mutex
	requests int              // ASSOCIATE requests seen
	perAssoc map[int][]string // destinations received, per association
	problems []string
}

func newFakeBambooSOCKS(t *testing.T, opts fakeSOCKSOpts) *fakeBambooSOCKS {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	s := &fakeBambooSOCKS{port: ln.Addr().(*net.TCPAddr).Port, opts: opts, perAssoc: map[int][]string{}}
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

// associateReply is a SOCKS5 reply naming ip:port (ATYP 1 or 4).
func associateReply(rep byte, ip net.IP, port int) []byte {
	b := []byte{5, rep, 0, 4}
	if ip4 := ip.To4(); ip4 != nil {
		b[3] = 1
		b = append(b, ip4...)
	} else {
		b = append(b, ip.To16()...)
	}
	return binary.BigEndian.AppendUint16(b, uint16(port))
}

func (s *fakeBambooSOCKS) serve(c net.Conn) {
	defer c.Close()
	var b [10]byte
	if _, err := io.ReadFull(c, b[:3]); err != nil || !bytes.Equal(b[:3], []byte{5, 1, 0}) {
		s.problem("greeting % x (%v)", b[:3], err)
		return
	}
	greeting := s.opts.greeting
	if greeting == nil {
		greeting = []byte{5, 0}
	}
	c.Write(greeting)
	if greeting[1] != 0 {
		return
	}
	if _, err := io.ReadFull(c, b[:10]); err != nil || !bytes.Equal(b[:4], []byte{5, 3, 0, 1}) {
		s.problem("associate request % x (%v)", b[:10], err)
		return
	}
	ip := net.IPv4(127, 0, 0, 1)
	if s.opts.relayIPv6 {
		ip = net.IPv6loopback
	}
	relay, err := net.ListenUDP("udp", &net.UDPAddr{IP: ip})
	if err != nil {
		s.problem("relay: %v", err)
		return
	}
	defer relay.Close()
	s.mu.Lock()
	s.requests++
	n := s.requests
	s.mu.Unlock()

	raddr := relay.LocalAddr().(*net.UDPAddr)
	var reply []byte
	if s.opts.associate != nil {
		reply = s.opts.associate(n, raddr)
	}
	if reply == nil {
		reply = associateReply(0, raddr.IP, raddr.Port)
	}
	c.Write(reply)
	if reply[1] != 0 {
		return
	}
	go s.relay(relay, n)
	_, _ = io.Copy(io.Discard, c) // the association lives while the control connection is open
}

func (s *fakeBambooSOCKS) relay(relay *net.UDPConn, n int) {
	buf := make([]byte, 4096)
	for {
		size, client, err := relay.ReadFromUDP(buf)
		if err != nil {
			return
		}
		dst, payload, err := parseSocksUDPDatagram(buf[:size])
		if err != nil {
			s.problem("datagram header: %v", err)
			continue
		}
		s.mu.Lock()
		s.perAssoc[n] = append(s.perAssoc[n], dst)
		s.mu.Unlock()
		for _, r := range s.opts.handle(s, n, dst, bytes.Clone(payload)) {
			from := r.from
			if from == "" {
				from = dst
			}
			host, portStr, _ := net.SplitHostPort(from)
			port, _ := strconv.Atoi(portStr)
			hdr, err := socksUDPHeader(host, port)
			if err != nil {
				s.problem("reply header for %s: %v", from, err)
				continue
			}
			relay.WriteToUDP(append(hdr, r.payload...), client)
		}
	}
}

// check asserts no protocol violations, the number of ASSOCIATE requests and that every
// association sent exactly one datagram (one destination per association, the Hub DNAT rule).
func (s *fakeBambooSOCKS) check(t *testing.T, wantRequests int) {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.problems) > 0 {
		t.Fatalf("fake SOCKS saw problems: %s", strings.Join(s.problems, "; "))
	}
	if s.requests != wantRequests {
		t.Fatalf("ASSOCIATE requests %d, want %d", s.requests, wantRequests)
	}
	for n, dsts := range s.perAssoc {
		if len(dsts) != 1 {
			t.Fatalf("association %d got %d datagrams: %v", n, len(dsts), dsts)
		}
	}
}

// vnServer plays the Hub's Hysteria: it checks that payload is a salamander-obfuscated VN
// probe to a hopping port and answers with reply(n, dst, probe).
func vnServer(psk []byte, reply func(n int, dst string, probe []byte) []fakeReply) fakeHandler {
	return func(s *fakeBambooSOCKS, n int, dst string, payload []byte) []fakeReply {
		_, portStr, _ := net.SplitHostPort(dst)
		if port, _ := strconv.Atoi(portStr); port < 30000 || port > 65000 {
			s.problem("destination port %d outside the hopping range", port)
		}
		probe, err := salamanderDeobfuscate(psk, payload)
		if err != nil {
			s.problem("deobfuscate: %v", err)
			return nil
		}
		if len(probe) != 1200 || probe[0]&0xC0 != 0xC0 || binary.BigEndian.Uint32(probe[1:5]) != 0x1a2a3a4a {
			s.problem("not a VN probe: %d bytes, % x", len(probe), probe[:min(len(probe), 8)])
			return nil
		}
		return reply(n, dst, probe)
	}
}

func obfuscated(psk, p []byte) []byte {
	out, _ := salamanderObfuscate(crand.Reader, psk, p)
	return out
}

// goodVN answers every probe with a proper VN from the destination.
func goodVN(psk []byte) func(int, string, []byte) []fakeReply {
	return func(_ int, _ string, probe []byte) []fakeReply {
		return []fakeReply{{payload: obfuscated(psk, testVNReply(probe))}}
	}
}

func silent(int, string, []byte) []fakeReply { return nil }

// fromPort replaces the port of dst.
func fromPort(dst string, delta int) string {
	host, portStr, _ := net.SplitHostPort(dst)
	port, _ := strconv.Atoi(portStr)
	return net.JoinHostPort(host, strconv.Itoa(port+delta))
}

// newHangingSOCKS accepts TCP connections and never answers.
func newHangingSOCKS(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var conns []net.Conn
	t.Cleanup(func() {
		ln.Close()
		mu.Lock()
		defer mu.Unlock()
		for _, c := range conns {
			c.Close()
		}
	})
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			conns = append(conns, c)
			mu.Unlock()
		}
	}()
	return ln.Addr().(*net.TCPAddr).Port
}

// ipv6LoopbackUDPWorks sends one datagram to itself over [::1]: some Windows setups (VPN/TUN
// drivers) accept the sockets but never deliver the datagram.
func ipv6LoopbackUDPWorks() error {
	c, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv6loopback})
	if err != nil {
		return err
	}
	defer c.Close()
	d, err := net.DialUDP("udp", nil, c.LocalAddr().(*net.UDPAddr))
	if err != nil {
		return err
	}
	defer d.Close()
	if _, err := d.Write([]byte("ping")); err != nil {
		return err
	}
	c.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
	_, _, err = c.ReadFromUDP(make([]byte, 16))
	return err
}

// TestSocksAssociateHandshake drives the client side of the handshake over net.Pipe, so the
// reply parsing (incl. ATYP 4) is covered even where UDP over [::1] does not work.
func TestSocksAssociateHandshake(t *testing.T) {
	ok := func(atypAddr ...byte) []byte { return append([]byte{5, 0, 0}, atypAddr...) }
	cases := []struct {
		name     string
		greeting []byte
		reply    []byte // nil: the server hangs up after the request
		want     string // relay address, or "" for an error
		errPart  string
	}{
		{"ipv4", []byte{5, 0}, ok(1, 127, 0, 0, 1, 0x0f, 0xa0), "127.0.0.1:4000", ""},
		{"ipv6 (ATYP 4)", []byte{5, 0}, ok(append(append([]byte{4}, net.IPv6loopback...), 0x0f, 0xa0)...), "[::1]:4000", ""},
		{"0.0.0.0 → server address (pipe: 127.0.0.1)", []byte{5, 0}, ok(1, 0, 0, 0, 0, 0x0f, 0xa0), "127.0.0.1:4000", ""},
		{":: → server address (pipe: 127.0.0.1)", []byte{5, 0}, ok(append(append([]byte{4}, net.IPv6unspecified...), 0x0f, 0xa0)...), "127.0.0.1:4000", ""},
		{"method rejected 0xFF", []byte{5, 0xFF}, nil, "", "05 ff"},
		{"associate rejected", []byte{5, 0}, []byte{5, 7, 0, 1, 0, 0, 0, 0, 0, 0}, "", "код 7"},
		{"wrong version", []byte{5, 0}, []byte{4, 0, 0, 1, 0, 0, 0, 0, 0, 0}, "", "версия 4"},
		{"ATYP 3", []byte{5, 0}, ok(3, 9, 'l', 'o', 'c', 'a', 'l', 'h', 'o', 's', 't', 0x0f, 0xa0), "", "ATYP 3"},
		{"BND.PORT 0", []byte{5, 0}, ok(1, 127, 0, 0, 1, 0, 0), "", "порт"},
		{"truncated address", []byte{5, 0}, ok(1, 127, 0), "", "адрес реле"},
		{"hang-up after the request", []byte{5, 0}, nil, "", "ответ на UDP ASSOCIATE"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client, server := net.Pipe()
			defer client.Close()
			go func() {
				defer server.Close()
				buf := make([]byte, 10)
				if _, err := io.ReadFull(server, buf[:3]); err != nil {
					return
				}
				server.Write(tc.greeting)
				if tc.greeting[1] != 0 {
					return
				}
				if _, err := io.ReadFull(server, buf[:10]); err != nil {
					return
				}
				if tc.reply != nil {
					server.Write(tc.reply)
				}
			}()
			relay, err := socksAssociateHandshake(client, time.Now().Add(time.Second))
			if tc.want != "" {
				if err != nil || relay.String() != tc.want {
					t.Fatalf("relay %v, err %v; want %s", relay, err, tc.want)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.errPart) {
				t.Fatalf("err %v, want one mentioning %q", err, tc.errPart)
			}
		})
	}
}

var fastBambooProber = bambooProber{socksHost: "127.0.0.1", attempts: 2, attemptTimeout: 300 * time.Millisecond, rand: crand.Reader}

// gaveUpInTime asserts the probe neither returned before trying both attempts nor overran them.
func gaveUpInTime(t *testing.T, elapsed time.Duration, waitedBoth bool) {
	t.Helper()
	limit := 2*fastBambooProber.attemptTimeout + time.Second
	if elapsed > limit {
		t.Fatalf("gave up after %v, limit %v", elapsed, limit)
	}
	if waitedBoth && elapsed < 2*fastBambooProber.attemptTimeout-50*time.Millisecond {
		t.Fatalf("gave up after %v: did not wait for both attempts", elapsed)
	}
}

func TestProbeBambooEndToEnd(t *testing.T) {
	psk := []byte("bamboo-obfs")
	const ipHost = "138.16.181.225"

	t.Run("vn reply, ipv4 host", func(t *testing.T) {
		s := newFakeBambooSOCKS(t, fakeSOCKSOpts{handle: vnServer(psk, goodVN(psk))})
		rtt, port, err := probeBamboo(s.port, ipHost, psk) // the production entry point
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
		s := newFakeBambooSOCKS(t, fakeSOCKSOpts{
			handle:    vnServer(psk, goodVN(psk)),
			associate: func(_ int, relay *net.UDPAddr) []byte { return associateReply(0, net.IPv4zero, relay.Port) },
		})
		if _, _, err := fastBambooProber.probe(s.port, "hiearth.ru", psk); err != nil {
			t.Fatal(err)
		}
		s.check(t, 1)
	})

	t.Run("relay on ipv6: ATYP 4 in the ASSOCIATE reply", func(t *testing.T) {
		if err := ipv6LoopbackUDPWorks(); err != nil {
			t.Skipf("UDP over [::1] does not work on this machine (%v); TestSocksAssociateHandshake covers ATYP 4", err)
		}
		s := newFakeBambooSOCKS(t, fakeSOCKSOpts{handle: vnServer(psk, goodVN(psk)), relayIPv6: true})
		if _, _, err := fastBambooProber.probe(s.port, ipHost, psk); err != nil {
			t.Fatal(err)
		}
		s.check(t, 1)
	})

	t.Run("garbage first, then the VN", func(t *testing.T) {
		s := newFakeBambooSOCKS(t, fakeSOCKSOpts{handle: vnServer(psk, func(_ int, _ string, probe []byte) []fakeReply {
			return []fakeReply{{payload: []byte{1, 2, 3}}, {payload: obfuscated(psk, testVNReply(probe))}}
		})})
		if _, _, err := fastBambooProber.probe(s.port, ipHost, psk); err != nil {
			t.Fatal(err)
		}
		s.check(t, 1)
	})

	t.Run("first datagram lost, retry succeeds", func(t *testing.T) {
		vn := goodVN(psk)
		s := newFakeBambooSOCKS(t, fakeSOCKSOpts{handle: vnServer(psk, func(n int, dst string, probe []byte) []fakeReply {
			if n == 1 {
				return nil
			}
			return vn(n, dst, probe)
		})})
		if _, _, err := fastBambooProber.probe(s.port, ipHost, psk); err != nil {
			t.Fatal(err)
		}
		s.check(t, 2)
	})

	t.Run("no reply", func(t *testing.T) {
		s := newFakeBambooSOCKS(t, fakeSOCKSOpts{handle: vnServer(psk, silent)})
		start := time.Now()
		_, port, err := fastBambooProber.probe(s.port, ipHost, psk)
		if !errors.Is(err, errBambooNoReply) {
			t.Fatalf("err %v, want no reply", err)
		}
		if !strings.Contains(err.Error(), ":"+strconv.Itoa(port)) {
			t.Fatalf("error %q does not name port %d", err, port)
		}
		gaveUpInTime(t, time.Since(start), true)
		s.check(t, 2)
	})

	evidence := []struct {
		name  string
		host  string
		reply func(n int, dst string, probe []byte) []fakeReply
		want  error
	}{
		{"obfuscated short-header packet", ipHost, func(int, string, []byte) []fakeReply {
			return []fakeReply{{payload: obfuscated(psk, append([]byte{0x40}, make([]byte, 40)...))}}
		}, errBambooWrongReply},
		{"vn obfuscated with another psk", ipHost, func(_ int, _ string, probe []byte) []fakeReply {
			return []fakeReply{{payload: obfuscated([]byte("other-psk"), testVNReply(probe))}}
		}, errBambooWrongReply},
		{"too short for salamander", ipHost, func(int, string, []byte) []fakeReply {
			return []fakeReply{{payload: []byte{1, 2, 3}}}
		}, errBambooWrongReply},
		{"vn for somebody else's CIDs", ipHost, func(int, string, []byte) []fakeReply {
			return []fakeReply{{payload: obfuscated(psk, testVNReply(buildVNProbePacket(crand.Reader)))}}
		}, errBambooWrongReply},
		{"vn from another port", ipHost, func(_ int, dst string, probe []byte) []fakeReply {
			return []fakeReply{{from: fromPort(dst, 1), payload: obfuscated(psk, testVNReply(probe))}}
		}, errBambooWrongSource},
		{"vn from the listener port 50443", ipHost, func(_ int, dst string, probe []byte) []fakeReply {
			host, port, _ := net.SplitHostPort(dst)
			listener := "50443"
			if port == listener { // the hop port itself may be 50443 (it is inside the range)
				listener = "50442"
			}
			return []fakeReply{{from: net.JoinHostPort(host, listener), payload: obfuscated(psk, testVNReply(probe))}}
		}, errBambooWrongSource},
		{"vn from another ip", ipHost, func(_ int, dst string, probe []byte) []fakeReply {
			_, port, _ := net.SplitHostPort(dst)
			return []fakeReply{{from: net.JoinHostPort("45.150.39.236", port), payload: obfuscated(psk, testVNReply(probe))}}
		}, errBambooWrongSource},
		{"domain host, vn from another port", "hiearth.ru", func(_ int, dst string, probe []byte) []fakeReply {
			return []fakeReply{{from: fromPort(dst, -1), payload: obfuscated(psk, testVNReply(probe))}}
		}, errBambooWrongSource},
		{"wrong source outranks garbage that came first", ipHost, func(_ int, dst string, probe []byte) []fakeReply {
			return []fakeReply{{payload: []byte{1, 2, 3}}, {from: fromPort(dst, 1), payload: obfuscated(psk, testVNReply(probe))}}
		}, errBambooWrongSource},
	}
	for _, tc := range evidence {
		t.Run(tc.name, func(t *testing.T) {
			s := newFakeBambooSOCKS(t, fakeSOCKSOpts{handle: vnServer(psk, tc.reply)})
			start := time.Now()
			_, _, err := fastBambooProber.probe(s.port, tc.host, psk)
			if !errors.Is(err, tc.want) {
				t.Fatalf("err %v, want %v", err, tc.want)
			}
			gaveUpInTime(t, time.Since(start), true) // a rejected datagram does not end the wait
			s.check(t, 2)
		})
	}

	t.Run("domain host: vn from any ip on the hop port is fine", func(t *testing.T) {
		s := newFakeBambooSOCKS(t, fakeSOCKSOpts{handle: vnServer(psk, func(_ int, dst string, probe []byte) []fakeReply {
			_, port, _ := net.SplitHostPort(dst)
			return []fakeReply{{from: net.JoinHostPort("138.16.181.225", port), payload: obfuscated(psk, testVNReply(probe))}}
		})})
		if _, _, err := fastBambooProber.probe(s.port, "hiearth.ru", psk); err != nil {
			t.Fatal(err)
		}
		s.check(t, 1)
	})

	t.Run("evidence outranks a later SOCKS failure", func(t *testing.T) {
		s := newFakeBambooSOCKS(t, fakeSOCKSOpts{
			handle: vnServer(psk, silent),
			associate: func(n int, relay *net.UDPAddr) []byte {
				if n == 2 {
					return associateReply(1, net.IPv4zero, 0) // general failure
				}
				return nil
			},
		})
		_, _, err := fastBambooProber.probe(s.port, ipHost, psk)
		if !errors.Is(err, errBambooNoReply) {
			t.Fatalf("err %v, want the no-reply of attempt 1", err)
		}
		s.check(t, 2)
	})

	socksFailures := []struct {
		name string
		opts fakeSOCKSOpts
		want string
	}{
		{"associate rejected", fakeSOCKSOpts{associate: func(int, *net.UDPAddr) []byte { return associateReply(7, net.IPv4zero, 0) }}, "код 7"},
		{"method rejected 0xFF", fakeSOCKSOpts{greeting: []byte{5, 0xFF}}, "05 ff"},
		{"relay as a domain name (ATYP 3)", fakeSOCKSOpts{associate: func(int, *net.UDPAddr) []byte {
			return []byte{5, 0, 0, 3, 9, 'l', 'o', 'c', 'a', 'l', 'h', 'o', 's', 't', 0x30, 0x39}
		}}, "ATYP 3"},
		{"BND.PORT 0", fakeSOCKSOpts{associate: func(int, *net.UDPAddr) []byte { return associateReply(0, net.IPv4(127, 0, 0, 1), 0) }}, "порт"},
	}
	for _, tc := range socksFailures {
		t.Run("socks: "+tc.name, func(t *testing.T) {
			tc.opts.handle = vnServer(psk, goodVN(psk))
			s := newFakeBambooSOCKS(t, tc.opts)
			_, _, err := fastBambooProber.probe(s.port, ipHost, psk)
			if !errors.Is(err, errBambooSocks) || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err %v, want SOCKS failure mentioning %q", err, tc.want)
			}
		})
	}

	t.Run("socks: port closed", func(t *testing.T) {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		port := ln.Addr().(*net.TCPAddr).Port
		ln.Close()
		start := time.Now()
		if _, _, err = fastBambooProber.probe(port, ipHost, psk); !errors.Is(err, errBambooSocks) {
			t.Fatalf("err %v, want SOCKS failure", err)
		}
		gaveUpInTime(t, time.Since(start), false)
	})

	t.Run("socks: accepts TCP and never answers", func(t *testing.T) {
		port := newHangingSOCKS(t)
		start := time.Now()
		if _, _, err := fastBambooProber.probe(port, ipHost, psk); !errors.Is(err, errBambooSocks) {
			t.Fatalf("err %v, want SOCKS failure", err)
		}
		gaveUpInTime(t, time.Since(start), true)
	})
}

// dnsServer answers DNS queries to :53 with answer(dst, query) (nil = silence).
func dnsServer(answer func(dst string, query []byte) []byte) fakeHandler {
	return func(s *fakeBambooSOCKS, _ int, dst string, payload []byte) []fakeReply {
		if !strings.HasSuffix(dst, ":53") || len(payload) < 12 || binary.BigEndian.Uint16(payload[4:6]) != 1 {
			s.problem("not a DNS query to :53: %s % x", dst, payload)
			return nil
		}
		if resp := answer(dst, payload); resp != nil {
			return []fakeReply{{payload: resp}}
		}
		return nil
	}
}

func dnsAnswer(query []byte) []byte {
	resp := bytes.Clone(query)
	resp[2] |= 0x80
	return resp
}

func TestBambooControl(t *testing.T) {
	t.Run("first resolver answers", func(t *testing.T) {
		var dsts []string
		var mu sync.Mutex
		s := newFakeBambooSOCKS(t, fakeSOCKSOpts{handle: dnsServer(func(dst string, q []byte) []byte {
			mu.Lock()
			dsts = append(dsts, dst)
			mu.Unlock()
			return dnsAnswer(q)
		})})
		if err := fastBambooProber.control(s.port); err != nil {
			t.Fatal(err)
		}
		if len(dsts) != 1 || dsts[0] != "1.1.1.1:53" {
			t.Fatalf("queried %v", dsts)
		}
		s.check(t, 1)
	})

	t.Run("first resolver silent, second answers", func(t *testing.T) {
		s := newFakeBambooSOCKS(t, fakeSOCKSOpts{handle: dnsServer(func(dst string, q []byte) []byte {
			if dst == "8.8.8.8:53" {
				return dnsAnswer(q)
			}
			return nil
		})})
		if err := fastBambooProber.control(s.port); err != nil {
			t.Fatal(err)
		}
		s.check(t, 2)
	})

	failures := map[string]func(string, []byte) []byte{
		"silence":        func(string, []byte) []byte { return nil },
		"other id":       func(_ string, q []byte) []byte { r := dnsAnswer(q); r[0] ^= 0xFF; return r },
		"query echoed":   func(_ string, q []byte) []byte { return bytes.Clone(q) },
		"truncated junk": func(string, []byte) []byte { return []byte{1, 2} },
	}
	for name, answer := range failures {
		t.Run(name, func(t *testing.T) {
			s := newFakeBambooSOCKS(t, fakeSOCKSOpts{handle: dnsServer(answer)})
			start := time.Now()
			err := fastBambooProber.control(s.port)
			if err == nil || !strings.Contains(err.Error(), "1.1.1.1") || !strings.Contains(err.Error(), "8.8.8.8") {
				t.Fatalf("err %v, want a failure naming both resolvers", err)
			}
			gaveUpInTime(t, time.Since(start), true)
			s.check(t, 2)
		})
	}

	t.Run("socks hangs", func(t *testing.T) {
		port := newHangingSOCKS(t)
		start := time.Now()
		if err := fastBambooProber.control(port); err == nil {
			t.Fatal("want error")
		}
		gaveUpInTime(t, time.Since(start), true)
	})
}

// ── the decision layer ──

func TestDecideBamboo(t *testing.T) {
	wrap := func(e error) error { return fmt.Errorf("%w: x", e) }
	ctrl := errors.New("DNS через exit: нет ответа")
	cases := []struct {
		name         string
		probe, ctrl  error
		needsControl bool
		want         bambooVerdict
	}{
		{"ok", nil, nil, false, bambooOK},
		{"socks", wrap(errBambooSocks), nil, false, bambooSkip},
		{"no reply, control ok", wrap(errBambooNoReply), nil, true, bambooFail},
		{"no reply, control failed", wrap(errBambooNoReply), ctrl, true, bambooSkip},
		{"wrong reply ignores control", wrap(errBambooWrongReply), ctrl, false, bambooFail},
		{"wrong source ignores control", wrap(errBambooWrongSource), ctrl, false, bambooFail},
		{"unclassified, control ok", errors.New("адрес: пустой хост"), nil, true, bambooFail},
		{"unclassified, control failed", errors.New("адрес: пустой хост"), ctrl, true, bambooSkip},
	}
	for _, tc := range cases {
		if got := bambooNeedsControl(tc.probe); got != tc.needsControl {
			t.Fatalf("%s: needs control %v, want %v", tc.name, got, tc.needsControl)
		}
		if got := decideBamboo(tc.probe, tc.ctrl); got != tc.want {
			t.Fatalf("%s: verdict %v, want %v", tc.name, got, tc.want)
		}
	}
}

// bambooHarness drives a bambooMonitor with scripted probe/control results and a fake clock.
type bambooHarness struct {
	now          time.Time
	deployed     bool
	psk          string
	ports        []int
	probeErr     map[int]error // by exit SOCKS port; missing = success
	controlErr   error
	probed       []int // exit ports probed in the last step
	controlPorts []int // exit ports controlled in the last step
	events       []string
	details      map[string]string
	logs         []string
	m            *bambooMonitor
}

func newBambooHarness() *bambooHarness {
	h := &bambooHarness{
		now:      time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC),
		deployed: true,
		psk:      "obfs",
		ports:    []int{1090},
		probeErr: map[int]error{},
		details:  map[string]string{},
	}
	h.m = &bambooMonitor{
		now:       func() time.Time { return h.now },
		deployed:  func() bool { return h.deployed },
		psk:       func() string { return h.psk },
		host:      func() string { return "138.16.181.225" },
		exitPorts: func() []int { return h.ports },
		probe: func(port int, _ string, psk []byte) (time.Duration, int, error) {
			if string(psk) != h.psk {
				panic("psk not passed through")
			}
			h.probed = append(h.probed, port)
			return 135 * time.Millisecond, 41234, h.probeErr[port]
		},
		control: func(port int) error {
			h.controlPorts = append(h.controlPorts, port)
			return h.controlErr
		},
		raise: func(key string, level AlertLevel, _, details string) {
			h.events = append(h.events, fmt.Sprintf("raise %s %s", key, level))
			h.details[key] = details
		},
		resolve: func(key, _ string) { h.events = append(h.events, "resolve "+key) },
		logf:    func(format string, args ...any) { h.logs = append(h.logs, fmt.Sprintf(format, args...)) },
	}
	return h
}

// step advances the clock by d and runs the monitor once.
func (h *bambooHarness) step(d time.Duration) *bambooHarness {
	h.now = h.now.Add(d)
	h.events, h.probed, h.controlPorts, h.logs = nil, nil, nil, nil
	h.m.run()
	return h
}

func (h *bambooHarness) has(event string) bool {
	for _, e := range h.events {
		if e == event {
			return true
		}
	}
	return false
}

func (h *bambooHarness) raised() bool {
	for _, e := range h.events {
		if strings.HasPrefix(e, "raise ") {
			return true
		}
	}
	return false
}

const (
	raiseDown    = "raise " + bambooAlertKey + " critical"
	resolveDown  = "resolve " + bambooAlertKey
	raiseBlind   = "raise " + bambooBlindAlertKey + " warn"
	resolveBlind = "resolve " + bambooBlindAlertKey
)

func TestBambooMonitorThresholdAndRecovery(t *testing.T) {
	h := newBambooHarness()
	h.probeErr[1090] = fmt.Errorf("%w от 138.16.181.225:41234 за 5s", errBambooNoReply)
	for i := 1; i <= 2; i++ {
		if h.step(time.Minute); h.raised() {
			t.Fatalf("raised after %d failures: %v", i, h.events)
		}
		if !h.has(resolveBlind) {
			t.Fatalf("a counted failure is a verdict and must resolve the blind alert: %v", h.events)
		}
	}
	if h.step(time.Minute); !h.has(raiseDown) {
		t.Fatalf("3rd failure must raise: %v", h.events)
	}
	d := h.details[bambooAlertKey]
	for _, want := range []string{"3 проверок подряд", "138.16.181.225", "41234", "exit :1090", "нет ответа", "hysteria2-server"} {
		if !strings.Contains(d, want) {
			t.Fatalf("details lack %q:\n%s", want, d)
		}
	}
	if len(h.controlPorts) != 1 || h.controlPorts[0] != 1090 {
		t.Fatalf("no reply must be checked by the control on the same exit: %v", h.controlPorts)
	}
	delete(h.probeErr, 1090)
	if h.step(time.Minute); !h.has(resolveDown) || !h.has(resolveBlind) || h.raised() {
		t.Fatalf("success must resolve both alerts: %v", h.events)
	}
	if len(h.controlPorts) != 0 {
		t.Fatal("success needs no control")
	}
}

func TestBambooMonitorPerErrorKind(t *testing.T) {
	wrap := func(e error) error { return fmt.Errorf("%w: x", e) }
	ctrlDown := errors.New("DNS через exit :1090: нет ответа")
	cases := []struct {
		name        string
		probe, ctrl error
		raise       bool
		controls    int    // control calls per step
		detail      string // must appear in the alert details
	}{
		{"socks: skip, no control", wrap(errBambooSocks), nil, false, 0, ""},
		{"no reply, control ok: fail", wrap(errBambooNoReply), nil, true, 1, "hysteria2-server"},
		{"no reply, control failed: skip", wrap(errBambooNoReply), ctrlDown, false, 1, ""},
		{"wrong reply: fail without control", wrap(errBambooWrongReply), ctrlDown, true, 0, "server.yaml"},
		{"wrong source: fail without control", wrap(errBambooWrongSource), ctrlDown, true, 0, "NAT"},
		{"unclassified, control ok: fail", errors.New("адрес: пустой хост"), nil, true, 1, "пустой хост"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newBambooHarness()
			h.probeErr[1090], h.controlErr = tc.probe, tc.ctrl
			for i := 0; i < 3; i++ {
				h.step(time.Minute)
				if len(h.controlPorts) != tc.controls {
					t.Fatalf("control calls %d, want %d", len(h.controlPorts), tc.controls)
				}
			}
			if h.has(raiseDown) != tc.raise {
				t.Fatalf("raise %v, want %v: %v", h.has(raiseDown), tc.raise, h.events)
			}
			if tc.detail != "" && !strings.Contains(h.details[bambooAlertKey], tc.detail) {
				t.Fatalf("details lack %q:\n%s", tc.detail, h.details[bambooAlertKey])
			}
		})
	}
}

func TestBambooMonitorSkipsDoNotBreakTheStreak(t *testing.T) {
	h := newBambooHarness()
	h.probeErr[1090] = fmt.Errorf("%w: x", errBambooNoReply)
	h.step(time.Minute).step(time.Minute)
	h.controlErr = errors.New("exit lost UDP")
	if h.step(time.Minute); h.raised() {
		t.Fatalf("a failed control is a skip, not a failure: %v", h.events)
	}
	h.controlErr = nil
	if h.step(time.Minute); !h.has(raiseDown) {
		t.Fatalf("3rd counted failure (with a skip in between) must raise: %v", h.events)
	}
}

func TestBambooMonitorTriesNextExitOnSocksFailure(t *testing.T) {
	h := newBambooHarness()
	h.ports = []int{1090, 1091}
	h.probeErr[1090] = fmt.Errorf("%w: x", errBambooSocks)
	if h.step(time.Minute); fmt.Sprint(h.probed) != "[1090 1091]" || !h.has(resolveDown) {
		t.Fatalf("probed %v, events %v", h.probed, h.events)
	}
	h.probeErr[1091] = fmt.Errorf("%w: x", errBambooNoReply)
	h.step(time.Minute)
	if fmt.Sprint(h.controlPorts) != "[1091]" {
		t.Fatalf("control must run on the exit that gave the evidence: %v", h.controlPorts)
	}
	h.probeErr[1090] = nil
	if h.step(time.Minute); fmt.Sprint(h.probed) != "[1090]" {
		t.Fatalf("a working first exit is enough: %v", h.probed)
	}
}

func TestBambooMonitorBlind(t *testing.T) {
	h := newBambooHarness()
	h.ports = nil // no exit from the very start: the blind clock starts at the first run
	h.step(0)
	for elapsed := 10 * time.Minute; elapsed <= time.Hour; elapsed += 10 * time.Minute {
		if h.step(10 * time.Minute); h.raised() {
			t.Fatalf("raised after %v: %v", elapsed, h.events)
		}
	}
	if h.step(time.Minute); !h.has(raiseBlind) || h.has(raiseDown) {
		t.Fatalf("blind for 61m must raise only the blind warning: %v", h.events)
	}
	if d := h.details[bambooBlindAlertKey]; !strings.Contains(d, "нет здорового exit") {
		t.Fatalf("blind details must name the reason:\n%s", d)
	}

	h.ports = []int{1090}
	if h.step(time.Minute); !h.has(resolveBlind) {
		t.Fatalf("a verdict must resolve the blind alert: %v", h.events)
	}

	// the exit's SOCKS refuses UDP ASSOCIATE for over an hour
	h.probeErr[1090] = fmt.Errorf("%w 127.0.0.1:1090: UDP ASSOCIATE отклонён: код 7", errBambooSocks)
	for i := 0; i < 60; i++ {
		if h.step(time.Minute); h.raised() {
			t.Fatalf("raised after %d skipped minutes: %v", i+1, h.events)
		}
	}
	if h.step(time.Minute); !h.has(raiseBlind) || !strings.Contains(h.details[bambooBlindAlertKey], "ASSOCIATE") {
		t.Fatalf("SOCKS refusing UDP for 61m must raise blind: %v\n%s", h.events, h.details[bambooBlindAlertKey])
	}

	// a failed control also counts as blind
	h2 := newBambooHarness()
	h2.probeErr[1090] = fmt.Errorf("%w: x", errBambooNoReply)
	h2.controlErr = errors.New("exit lost UDP")
	h2.step(0)
	if h2.step(61 * time.Minute); !h2.has(raiseBlind) || h2.has(raiseDown) {
		t.Fatalf("failed control for 61m must raise blind only: %v", h2.events)
	}
}

func TestBambooMonitorNotDeployed(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(h *bambooHarness)
	}{
		{"no server.yaml", func(h *bambooHarness) { h.deployed = false }},
		{"no obfs password", func(h *bambooHarness) { h.psk = "" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newBambooHarness()
			h.probeErr[1090] = fmt.Errorf("%w: x", errBambooNoReply)
			h.step(time.Minute).step(time.Minute).step(time.Minute)
			if !h.has(raiseDown) {
				t.Fatalf("setup: alert not raised: %v", h.events)
			}
			tc.mutate(h)
			if h.step(time.Minute); !h.has(resolveDown) || !h.has(resolveBlind) || len(h.probed) != 0 {
				t.Fatalf("undeployed must resolve both alerts and not probe: %v, probed %v", h.events, h.probed)
			}
			for i := 0; i < 5; i++ { // hours without Bamboo: never blind
				if h.step(time.Hour); h.raised() {
					t.Fatalf("raised while not deployed: %v", h.events)
				}
			}
			// back again, but with no exit: the blind clock starts over instead of firing at once
			h.deployed, h.psk, h.ports = true, "obfs", nil
			if h.step(time.Minute); h.raised() {
				t.Fatalf("blind fired right after redeploy: %v", h.events)
			}
		})
	}
}

func TestBambooMonitorSkipLog(t *testing.T) {
	h := newBambooHarness()
	h.ports = nil
	logged := func() bool { return len(h.logs) == 1 && strings.Contains(h.logs[0], "пропущена") }
	if h.step(0); !logged() {
		t.Fatalf("first skip must be logged: %v", h.logs)
	}
	if h.step(time.Minute); len(h.logs) != 0 {
		t.Fatalf("same reason within the hour: %v", h.logs)
	}
	h.ports = []int{1090}
	h.probeErr[1090] = fmt.Errorf("%w: x", errBambooSocks)
	if h.step(time.Minute); !logged() || !strings.Contains(h.logs[0], "SOCKS") {
		t.Fatalf("a new reason must be logged at once: %v", h.logs)
	}
	if h.step(time.Minute); len(h.logs) != 0 {
		t.Fatalf("same reason within the hour: %v", h.logs)
	}
	h.ports = nil
	if h.step(time.Minute); !logged() {
		t.Fatalf("the reason changed back: %v", h.logs)
	}
	if h.step(30 * time.Minute); len(h.logs) != 0 {
		t.Fatalf("same reason within the hour: %v", h.logs)
	}
	if h.step(31 * time.Minute); !logged() {
		t.Fatalf("the same reason after an hour must be logged again: %v", h.logs)
	}
}
