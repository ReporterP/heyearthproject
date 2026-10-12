package main

import (
	"bytes"
	"context"
	crand "crypto/rand"
	"database/sql"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net"
	"net/netip"
	"os"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/blake2b"

	"github.com/heyearth/backend/internal/biz"
)

// ── (f) Bamboo probe: Hub → exit SOCKS (UDP ASSOCIATE) → Hysteria2 on the Hub's public IP ──
// On 2026-10-12 Bamboo was dead for 6.5 h for every external client (conntrack dropped the
// server's replies with EPERM) and nothing alerted, because every other probe is TCP.
//
// The probe sends a salamander-obfuscated 1200-byte QUIC long-header packet with an unsupported
// version to a random port of the hopping range; quic-go inside Hysteria answers with an
// obfuscated Version Negotiation packet. The reply takes exactly the user path (DNAT → :50443,
// conntrack, sendto), needs no Hysteria account and also proves the obfs password matches.
// It must come back from the very address and port the probe went to: clients behind a
// port-restricted or symmetric NAT drop anything else.

const (
	bambooAlertKey           = "bamboo-unreachable-external"
	bambooBlindAlertKey      = "bamboo-probe-blind"
	bambooProbeFailThreshold = 3
	bambooBlindAfter         = time.Hour       // deployed, but no verdict for this long → the probe is blind
	bambooLookupTimeout      = 5 * time.Second // resolving main_server_host on the Hub
	// bambooMaxExits bounds the exits tried in turn when the SOCKS of one cannot relay UDP. Worst
	// case of one run, ~45 s: lookup 5 s + two hanging exits 2×(2×5 s) + no reply on the third
	// 2×5 s + the DNS control 2×5 s.
	bambooMaxExits       = 3
	bambooHopPortMin     = 30000 // port-hopping range DNATed to :50443 (cmd/deploy hysteria2PortHopRule)
	bambooHopPortMax     = 65000
	bambooProbeVersion   = 0x1a2a3a4a // reserved 0x?a?a?a?a pattern (RFC 9000 §15): never supported
	bambooProbePacketLen = 1200       // quic-go ignores unknown-version packets shorter than this
	bambooControlName    = "example.com"
	salamanderSaltLen    = 8
)

// bambooControlResolvers are the targets of the UDP control, one per attempt: two operators,
// so one resolver unreachable from an exit does not blind the probe.
var bambooControlResolvers = []string{"1.1.1.1", "8.8.8.8"}

// Failure classes of a SOCKS UDP exchange. Only the last three say anything about the target;
// errBambooSocks is our side (the exit tunnel's local SOCKS5).
var (
	errBambooSocks       = errors.New("сбой SOCKS exit-туннеля")
	errBambooNoReply     = errors.New("нет ответа")
	errBambooWrongReply  = errors.New("неверный ответ")
	errBambooWrongSource = errors.New("ответ с чужого адреса")
)

// bambooEvidence ranks an error by how much it says about the target. Across datagrams and
// attempts the strongest error is reported (the first one among equals), so a SOCKS hiccup on
// the last attempt cannot turn real evidence into a skip.
func bambooEvidence(err error) int {
	switch {
	case errors.Is(err, errBambooWrongSource):
		return 4
	case errors.Is(err, errBambooWrongReply):
		return 3
	case errors.Is(err, errBambooNoReply):
		return 2
	case errors.Is(err, errBambooSocks):
		return 0
	default:
		return 1 // e.g. a malformed host: our config, but no exit can fix it
	}
}

func strongerEvidence(next, cur error) bool {
	return cur == nil || bambooEvidence(next) > bambooEvidence(cur)
}

// ── salamander (Hysteria extras/obfs/salamander.go) ──

// salamanderKey derives the per-packet XOR key BLAKE2b-256(psk || salt). It builds a fresh
// buffer: append(psk, salt...) could write into the spare capacity of the caller's psk.
func salamanderKey(psk, salt []byte) [blake2b.Size256]byte {
	buf := make([]byte, 0, len(psk)+len(salt))
	buf = append(append(buf, psk...), salt...)
	return blake2b.Sum256(buf)
}

func salamanderXOR(dst, src []byte, key *[blake2b.Size256]byte) {
	for i, b := range src {
		dst[i] = b ^ key[i%len(key)]
	}
}

// salamanderObfuscate returns salt || (in XOR key) with an 8-byte salt read from r.
func salamanderObfuscate(r io.Reader, psk, in []byte) ([]byte, error) {
	out := make([]byte, salamanderSaltLen+len(in))
	if _, err := io.ReadFull(r, out[:salamanderSaltLen]); err != nil {
		return nil, fmt.Errorf("salamander: соль: %w", err)
	}
	key := salamanderKey(psk, out[:salamanderSaltLen])
	salamanderXOR(out[salamanderSaltLen:], in, &key)
	return out, nil
}

// salamanderDeobfuscate reverses salamanderObfuscate. Like Hysteria, it rejects packets with
// no payload after the salt.
func salamanderDeobfuscate(psk, in []byte) ([]byte, error) {
	if len(in) <= salamanderSaltLen {
		return nil, fmt.Errorf("salamander: пакет %d байт, нужно больше %d", len(in), salamanderSaltLen)
	}
	key := salamanderKey(psk, in[:salamanderSaltLen])
	out := make([]byte, len(in)-salamanderSaltLen)
	salamanderXOR(out, in[salamanderSaltLen:], &key)
	return out, nil
}

// ── QUIC ──

// buildVNProbePacket returns a 1200-byte QUIC long-header packet with the unsupported version
// bambooProbeVersion, 8-byte DCID and SCID and random padding: what makes a QUIC server answer
// with Version Negotiation (RFC 9000 §6). A short read from r leaves zero bytes, which still
// form a valid probe.
func buildVNProbePacket(r io.Reader) []byte {
	p := make([]byte, bambooProbePacketLen)
	_, _ = io.ReadFull(r, p)
	p[0] = 0xC0 | p[0]&0x0F // long header + fixed bit
	binary.BigEndian.PutUint32(p[1:5], bambooProbeVersion)
	p[5] = 8  // DCID length, DCID = p[6:14]
	p[14] = 8 // SCID length, SCID = p[15:23]
	return p
}

// isVersionNegotiation reports whether p is a QUIC Version Negotiation packet: a long header
// with a zero version field (RFC 9000 §17.2.1).
func isVersionNegotiation(p []byte) bool {
	return len(p) >= 7 && p[0]&0x80 != 0 && binary.BigEndian.Uint32(p[1:5]) == 0
}

// vnMirrorsProbe reports whether the Version Negotiation packet vn answers probe (built by
// buildVNProbePacket): the server echoes the probe's SCID as DCID and its DCID as SCID.
func vnMirrorsProbe(vn, probe []byte) bool {
	if len(vn) < 7 {
		return false
	}
	dcidEnd := 6 + int(vn[5])
	if len(vn) < dcidEnd+1 {
		return false
	}
	scidEnd := dcidEnd + 1 + int(vn[dcidEnd])
	if len(vn) < scidEnd {
		return false
	}
	return bytes.Equal(vn[6:dcidEnd], probe[15:23]) && bytes.Equal(vn[dcidEnd+1:scidEnd], probe[6:14])
}

// ── DNS (UDP control) ──

// buildDNSQuery returns a recursive DNS query for the A record of name.
func buildDNSQuery(id uint16, name string) []byte {
	q := make([]byte, 12, 12+len(name)+6)
	binary.BigEndian.PutUint16(q[0:2], id)
	q[2] = 0x01 // RD
	q[5] = 1    // QDCOUNT
	for _, label := range strings.Split(strings.TrimSuffix(name, "."), ".") {
		q = append(append(q, byte(len(label))), label...)
	}
	return append(q, 0, 0, 1, 0, 1) // root label, QTYPE A, QCLASS IN
}

// isDNSResponseTo reports whether msg is a DNS response (QR bit) carrying our query id.
func isDNSResponseTo(id uint16, msg []byte) bool {
	return len(msg) >= 12 && binary.BigEndian.Uint16(msg[0:2]) == id && msg[2]&0x80 != 0
}

// ── minimal SOCKS5 UDP ASSOCIATE client (RFC 1928 §7; golang.org/x/net/proxy has no UDP) ──

// socksUDPAssoc is one UDP association. The relay drops it when the TCP control connection
// closes, so ctrl stays open for the whole exchange.
type socksUDPAssoc struct {
	ctrl net.Conn
	udp  *net.UDPConn // connected to the relay: datagrams from anyone else never reach us
}

func (a *socksUDPAssoc) Close() {
	_ = a.udp.Close()
	_ = a.ctrl.Close()
}

// socksUDPAssociate dials the SOCKS5 server, performs the no-auth greeting and UDP ASSOCIATE
// and opens a UDP socket to the relay. Every step is bounded by deadline.
func socksUDPAssociate(socksAddr string, deadline time.Time) (*socksUDPAssoc, error) {
	ctrl, err := (&net.Dialer{Deadline: deadline}).Dial("tcp", socksAddr)
	if err != nil {
		return nil, err
	}
	relay, err := socksAssociateHandshake(ctrl, deadline)
	if err != nil {
		_ = ctrl.Close()
		return nil, err
	}
	udp, err := net.DialUDP("udp", nil, relay)
	if err != nil {
		_ = ctrl.Close()
		return nil, fmt.Errorf("UDP к реле %s: %w", relay, err)
	}
	if err := udp.SetDeadline(deadline); err != nil {
		_ = udp.Close()
		_ = ctrl.Close()
		return nil, err
	}
	return &socksUDPAssoc{ctrl: ctrl, udp: udp}, nil
}

// socksAssociateHandshake returns the relay address from the ASSOCIATE reply. BND.ADDR may be
// IPv4 or IPv6; an unspecified one means "the SOCKS server's own address".
func socksAssociateHandshake(ctrl net.Conn, deadline time.Time) (*net.UDPAddr, error) {
	if err := ctrl.SetDeadline(deadline); err != nil {
		return nil, err
	}
	if _, err := ctrl.Write([]byte{5, 1, 0}); err != nil { // VER, NMETHODS, METHOD no-auth
		return nil, fmt.Errorf("приветствие: %w", err)
	}
	var b [4]byte
	if _, err := io.ReadFull(ctrl, b[:2]); err != nil {
		return nil, fmt.Errorf("ответ на приветствие: %w", err)
	}
	if b[0] != 5 || b[1] != 0 {
		return nil, fmt.Errorf("сервер не принял SOCKS5 без пароля: % x", b[:2])
	}
	// VER, CMD UDP ASSOCIATE, RSV, ATYP IPv4, 0.0.0.0:0 ("our UDP address is not known yet").
	if _, err := ctrl.Write([]byte{5, 3, 0, 1, 0, 0, 0, 0, 0, 0}); err != nil {
		return nil, fmt.Errorf("запрос UDP ASSOCIATE: %w", err)
	}
	if _, err := io.ReadFull(ctrl, b[:4]); err != nil {
		return nil, fmt.Errorf("ответ на UDP ASSOCIATE: %w", err)
	}
	var ipLen int
	switch {
	case b[0] != 5:
		return nil, fmt.Errorf("ответ на UDP ASSOCIATE: версия %d", b[0])
	case b[1] != 0:
		return nil, fmt.Errorf("UDP ASSOCIATE отклонён: код %d", b[1])
	case b[3] == 1:
		ipLen = net.IPv4len
	case b[3] == 4:
		ipLen = net.IPv6len
	default:
		return nil, fmt.Errorf("адрес реле с ATYP %d не поддерживается", b[3])
	}
	addr := make([]byte, ipLen+2)
	if _, err := io.ReadFull(ctrl, addr); err != nil {
		return nil, fmt.Errorf("адрес реле: %w", err)
	}
	relay := &net.UDPAddr{IP: net.IP(addr[:ipLen]), Port: int(binary.BigEndian.Uint16(addr[ipLen:]))}
	if relay.Port == 0 {
		return nil, errors.New("реле с портом 0")
	}
	if relay.IP.IsUnspecified() {
		relay.IP = net.IPv4(127, 0, 0, 1)
		if ta, ok := ctrl.RemoteAddr().(*net.TCPAddr); ok {
			relay.IP = ta.IP
		}
	}
	return relay, nil
}

// socksUDPHeader builds the SOCKS5 UDP request header RSV RSV FRAG ATYP DST.ADDR DST.PORT.
// An IP literal goes as ATYP 1 (IPv4) or 4 (IPv6); anything else as a domain name (ATYP 3),
// which the exit resolves.
func socksUDPHeader(host string, port int) ([]byte, error) {
	if port <= 0 || port > 65535 {
		return nil, fmt.Errorf("порт %d вне диапазона", port)
	}
	h := []byte{0, 0, 0}
	ip := net.ParseIP(host)
	switch {
	case ip != nil && ip.To4() != nil:
		h = append(append(h, 1), ip.To4()...)
	case ip != nil:
		h = append(append(h, 4), ip.To16()...)
	case host == "" || len(host) > 255:
		return nil, fmt.Errorf("недопустимое имя хоста %q", host)
	default:
		h = append(append(h, 3, byte(len(host))), host...)
	}
	return binary.BigEndian.AppendUint16(h, uint16(port)), nil
}

// parseSocksUDPDatagram strips the SOCKS5 UDP header from a relayed datagram and returns the
// address it names and the payload. Fragments (FRAG != 0) are not supported.
func parseSocksUDPDatagram(p []byte) (from string, payload []byte, err error) {
	if len(p) < 4 {
		return "", nil, fmt.Errorf("датаграмма %d байт короче заголовка SOCKS", len(p))
	}
	if p[2] != 0 {
		return "", nil, fmt.Errorf("фрагмент SOCKS (FRAG=%d) не поддерживается", p[2])
	}
	var host string
	var end int // offset of DST.PORT
	switch p[3] {
	case 1:
		end = 4 + net.IPv4len
		if len(p) >= end {
			host = net.IP(p[4:end]).String()
		}
	case 3:
		if len(p) < 5 {
			return "", nil, errors.New("обрезанный заголовок SOCKS")
		}
		end = 5 + int(p[4])
		if len(p) >= end {
			host = string(p[5:end])
		}
	case 4:
		end = 4 + net.IPv6len
		if len(p) >= end {
			host = net.IP(p[4:end]).String()
		}
	default:
		return "", nil, fmt.Errorf("неизвестный ATYP %d в заголовке SOCKS", p[3])
	}
	if len(p) < end+2 {
		return "", nil, errors.New("обрезанный заголовок SOCKS")
	}
	port := binary.BigEndian.Uint16(p[end : end+2])
	return net.JoinHostPort(host, strconv.Itoa(int(port))), p[end+2:], nil
}

// socksUDPExchange sends payload to host:port through a fresh association on socksAddr and reads
// relayed datagrams until accept takes one or timeout passes. A rejected datagram does not end
// the wait; if nothing is accepted, the strongest rejection is returned instead of "no reply".
// A datagram whose SOCKS header is malformed is the relay's garbage, not evidence about the
// target: it is only counted in the "no reply" message.
func socksUDPExchange(socksAddr, host string, port int, payload []byte, timeout time.Duration,
	accept func(from string, data []byte) error) (time.Duration, error) {
	target := net.JoinHostPort(host, strconv.Itoa(port))
	hdr, err := socksUDPHeader(host, port)
	if err != nil {
		return 0, fmt.Errorf("адрес %s: %w", target, err)
	}
	assoc, err := socksUDPAssociate(socksAddr, time.Now().Add(timeout))
	if err != nil {
		return 0, fmt.Errorf("%w %s: %v", errBambooSocks, socksAddr, err)
	}
	defer assoc.Close()

	start := time.Now()
	if _, err := assoc.udp.Write(append(hdr, payload...)); err != nil {
		return 0, fmt.Errorf("%w %s: отправка в реле: %v", errBambooSocks, socksAddr, err)
	}
	var rejected error
	malformed := 0
	buf := make([]byte, 64<<10)
	for {
		n, err := assoc.udp.Read(buf)
		if err != nil {
			var ne net.Error
			switch {
			case rejected != nil:
				return 0, rejected
			case errors.As(err, &ne) && ne.Timeout() && malformed > 0:
				return 0, fmt.Errorf("%w от %s за %v (реле прислало датаграмм с битым заголовком SOCKS: %d)",
					errBambooNoReply, target, timeout, malformed)
			case errors.As(err, &ne) && ne.Timeout():
				return 0, fmt.Errorf("%w от %s за %v", errBambooNoReply, target, timeout)
			default:
				return 0, fmt.Errorf("%w %s: чтение из реле: %v", errBambooSocks, socksAddr, err)
			}
		}
		rtt := time.Since(start)
		from, data, err := parseSocksUDPDatagram(buf[:n])
		if err != nil {
			malformed++
			continue
		}
		if err = accept(from, data); err == nil {
			return rtt, nil
		}
		if strongerEvidence(err, rejected) {
			rejected = err
		}
	}
}

// replyFromTarget reports whether a reply the relay attributes to from came from host:port. The
// port must match; the IP only when host is an IP literal (a name may resolve to any address).
func replyFromTarget(from, host string, port int) bool {
	fromHost, fromPort, err := net.SplitHostPort(from)
	if err != nil || fromPort != strconv.Itoa(port) {
		return false
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.Equal(net.ParseIP(fromHost))
	}
	return true
}

// ── the probe ──

// bambooProber holds the knobs of probeBamboo; tests shrink the timeout.
type bambooProber struct {
	socksHost      string        // where the exit tunnel's SOCKS5 listens
	attempts       int           // UDP may lose a datagram: retry before calling it a failure
	attemptTimeout time.Duration // one attempt: SOCKS handshake + waiting for the reply
	rand           io.Reader
}

var defaultBambooProber = bambooProber{socksHost: "127.0.0.1", attempts: 2, attemptTimeout: 5 * time.Second, rand: crand.Reader}

// probeBamboo checks that Bamboo answers on host from outside: through the exit SOCKS on
// 127.0.0.1:socksPort it sends a VN probe to a random hopping port and waits for Version
// Negotiation from that very port. port is the hopping port the returned error refers to.
func probeBamboo(socksPort int, host string, psk []byte) (rtt time.Duration, port int, err error) {
	return defaultBambooProber.probe(socksPort, host, psk)
}

func (p bambooProber) socksAddr(socksPort int) string {
	return net.JoinHostPort(p.socksHost, strconv.Itoa(socksPort))
}

func (p bambooProber) probe(socksPort int, host string, psk []byte) (rtt time.Duration, port int, err error) {
	for range max(p.attempts, 1) {
		hop := randomHopPort(p.rand)
		r, aerr := p.attempt(socksPort, host, hop, psk)
		if aerr == nil {
			return r, hop, nil
		}
		if strongerEvidence(aerr, err) {
			port, err = hop, aerr
		}
	}
	return 0, port, err
}

func randomHopPort(r io.Reader) int {
	var b [4]byte
	_, _ = io.ReadFull(r, b[:])
	return bambooHopPortMin + int(binary.BigEndian.Uint32(b[:])%(bambooHopPortMax-bambooHopPortMin+1))
}

// attempt uses a fresh association: a second destination port from the same relay source
// port clashes in the Hub's DNAT and never gets a reply.
func (p bambooProber) attempt(socksPort int, host string, port int, psk []byte) (time.Duration, error) {
	target := net.JoinHostPort(host, strconv.Itoa(port))
	probe := buildVNProbePacket(p.rand)
	pkt, err := salamanderObfuscate(p.rand, psk, probe)
	if err != nil {
		return 0, err
	}
	return socksUDPExchange(p.socksAddr(socksPort), host, port, pkt, p.attemptTimeout, func(from string, data []byte) error {
		plain, err := salamanderDeobfuscate(psk, data)
		switch {
		case err != nil:
			return fmt.Errorf("%w от %s: %v", errBambooWrongReply, target, err)
		case !isVersionNegotiation(plain):
			return fmt.Errorf("%w от %s: не QUIC Version Negotiation (%d байт, начало % x)",
				errBambooWrongReply, target, len(plain), plain[:min(len(plain), 8)])
		case !vnMirrorsProbe(plain, probe):
			return fmt.Errorf("%w от %s: Version Negotiation не на нашу пробу (Connection ID не совпадают)", errBambooWrongReply, target)
		case !replyFromTarget(from, host, port):
			return fmt.Errorf("%w: проба ушла на %s, ответ пришёл с %s", errBambooWrongSource, target, from)
		}
		return nil
	})
}

// control checks the exit tunnel's UDP path on its own: a DNS query through a fresh
// association must come back with our id. A failure means the exit lost UDP, not Bamboo.
func (p bambooProber) control(socksPort int) error {
	var errs []string
	for _, resolver := range bambooControlResolvers {
		var b [2]byte
		_, _ = io.ReadFull(p.rand, b[:])
		id := binary.BigEndian.Uint16(b[:])
		_, err := socksUDPExchange(p.socksAddr(socksPort), resolver, 53, buildDNSQuery(id, bambooControlName), p.attemptTimeout,
			func(_ string, msg []byte) error {
				if !isDNSResponseTo(id, msg) {
					return fmt.Errorf("%w от %s:53: не ответ DNS на наш запрос", errBambooWrongReply, resolver)
				}
				return nil
			})
		if err == nil {
			return nil
		}
		errs = append(errs, err.Error())
	}
	return fmt.Errorf("DNS через exit :%d: %s", socksPort, strings.Join(errs, "; "))
}

// ── the decision layer ──

type bambooVerdict int

const (
	bambooSkip bambooVerdict = iota // the probe could not test Bamboo
	bambooFail
	bambooOK
)

// bambooNeedsControl reports whether probeErr could be the exit's fault: only a missing reply
// (or an unclassified error) can. Any reply at all proves the exit relays UDP both ways.
func bambooNeedsControl(probeErr error) bool {
	return probeErr != nil && !errors.Is(probeErr, errBambooSocks) &&
		!errors.Is(probeErr, errBambooWrongReply) && !errors.Is(probeErr, errBambooWrongSource)
}

// decideBamboo turns a probe result and, when bambooNeedsControl, the UDP control result into
// a verdict. controlErr is ignored when no control was needed.
func decideBamboo(probeErr, controlErr error) bambooVerdict {
	switch {
	case probeErr == nil:
		return bambooOK
	case errors.Is(probeErr, errBambooSocks):
		return bambooSkip
	case bambooNeedsControl(probeErr) && controlErr != nil:
		return bambooSkip
	default:
		return bambooFail
	}
}

// bambooMonitor runs the probe once a minute and turns results into alerts. Every side effect
// is a field so tests can drive it. Used only by the loop-probe goroutine.
type bambooMonitor struct {
	now       func() time.Time
	deployed  func() bool            // /etc/hysteria/server.yaml exists
	psk       func() (string, error) // "" = not set; an error = could not read (DB down)
	host      func() (string, error) // main_server_host
	lookupIP  func(host string) (netip.Addr, error)
	exitPorts func() []int // live exit SOCKS ports, preferred first
	probe     func(socksPort int, host string, psk []byte) (time.Duration, int, error)
	control   func(socksPort int) error
	raise     func(key string, level AlertLevel, title, details string)
	resolve   func(key, message string)
	logf      func(format string, args ...any)

	streak     loopProbeStreak // with its own stale window, bambooBlindAfter
	blindSince time.Time       // start of the current stretch without a verdict while deployed; zero = not deployed
	okLog      logGate
	lastSkip   string               // class of the previous skip since the last verdict
	skipLogged map[string]time.Time // per skip class: when it was last logged
}

var bambooMon = &bambooMonitor{
	now: time.Now,
	deployed: func() bool {
		_, err := os.Stat(hysteria2ConfigPath)
		return !errors.Is(err, fs.ErrNotExist)
	},
	// Not biz.GetHysteria2ObfsPassword: it generates and stores a password as a side effect.
	psk:       func() (string, error) { return settingOrEmpty(biz.GetSetting, "hysteria2_obfs_password") },
	host:      bambooEntryHost,
	lookupIP:  lookupProbeIP,
	exitPorts: func() []int { return liveExitSocksPorts(bambooMaxExits) },
	probe:     probeBamboo,
	control:   func(socksPort int) error { return defaultBambooProber.control(socksPort) },
	raise:     RaiseAlert,
	resolve:   ResolveAlert,
	logf:      log.Printf,
}

func runBambooProbe() { bambooMon.run() }

// settingOrEmpty reads an app setting: a missing row is "", any other error is returned as is.
// GetSettingOrDefault cannot be used here: it turns a DB outage into "not set", and "obfs
// password not set" resolves the alerts (a false recovery in the middle of an outage).
func settingOrEmpty(get func(key string) (string, error), key string) (string, error) {
	v, err := get(key)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(v), nil
}

// bambooEntryHost is monitorEntryHost that reports DB errors instead of using the default.
func bambooEntryHost() (string, error) {
	host, err := settingOrEmpty(biz.GetSetting, "main_server_host")
	if err == nil && host == "" {
		host = "hiearth.ru"
	}
	return host, err
}

// lookupProbeIP resolves host on the Hub (not on the exit), preferring IPv4: the probe then
// targets an IP literal and the reply source is checked by address, not only by port.
func lookupProbeIP(host string) (netip.Addr, error) {
	ctx, cancel := context.WithTimeout(context.Background(), bambooLookupTimeout)
	defer cancel()
	addrs, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
	if err != nil {
		return netip.Addr{}, err
	}
	ip, ok := preferIPv4(addrs)
	if !ok {
		return netip.Addr{}, fmt.Errorf("у %s нет адресов", host)
	}
	return ip, nil
}

func preferIPv4(addrs []netip.Addr) (netip.Addr, bool) {
	for _, a := range addrs {
		if a.Unmap().Is4() {
			return a.Unmap(), true
		}
	}
	for _, a := range addrs {
		if a.IsValid() {
			return a, true
		}
	}
	return netip.Addr{}, false
}

func (m *bambooMonitor) run() {
	now := m.now()
	if !m.deployed() {
		m.notDeployed(now, "not-deployed", "нет "+hysteria2ConfigPath+", Бамбук не развёрнут")
		return
	}
	if m.blindSince.IsZero() { // before any DB read: a DB outage must not stop the blind clock
		m.blindSince = now
	}
	psk, err := m.psk()
	if err != nil { // unknown, not "not set": keep the streak and the alerts as they are
		m.skip(now, "db", fmt.Sprintf("не удалось прочитать hysteria2_obfs_password: %v", err))
		return
	}
	if psk == "" {
		m.notDeployed(now, "no-psk", "не задан hysteria2_obfs_password")
		return
	}
	host, err := m.host()
	if err != nil {
		m.skip(now, "db", fmt.Sprintf("не удалось прочитать main_server_host: %v", err))
		return
	}
	target, label := host, host
	if _, perr := netip.ParseAddr(host); perr != nil {
		ip, err := m.lookupIP(host)
		if err != nil {
			m.skip(now, "dns", fmt.Sprintf("не удалось разрешить %s на Hub: %v", host, err))
			return
		}
		target, label = ip.String(), fmt.Sprintf("%s (%s)", host, ip)
	}
	ports := m.exitPorts()
	if len(ports) == 0 {
		m.skip(now, "no-exit", "нет здорового exit с локальным SOCKS")
		return
	}
	var (
		rtt       time.Duration
		hop       int
		socksPort int
	)
	for _, socksPort = range ports { // a SOCKS that cannot relay UDP is our problem: try the next exit
		if rtt, hop, err = m.probe(socksPort, target, []byte(psk)); !errors.Is(err, errBambooSocks) {
			break
		}
	}
	var controlErr error
	if bambooNeedsControl(err) {
		controlErr = m.control(socksPort)
	}
	switch decideBamboo(err, controlErr) {
	case bambooOK:
		m.verdict(now)
		m.streak.recordWithin(now, true, bambooBlindAfter)
		if m.okLog.allow(now, time.Hour) {
			m.logf("[monitor] Бамбук-проба OK: %s UDP :%d ответил за %v (через exit :%d)", label, hop, rtt.Round(time.Millisecond), socksPort)
		}
		m.resolve(bambooAlertKey, fmt.Sprintf("Бамбук снова отвечает снаружи: %s UDP :%d за %v (проба через exit :%d).",
			label, hop, rtt.Round(time.Millisecond), socksPort))
	case bambooFail:
		m.verdict(now)
		// The stale window equals bambooBlindAfter: counted failures separated by skips for less
		// than that still add up, and a longer gap raises the blind alert instead, so failures
		// diluted by skips can never stay silent.
		fails := m.streak.recordWithin(now, false, bambooBlindAfter)
		level, title, details := bambooFailAlert(fails, label, hop, socksPort, err)
		if level == AlertWarn {
			m.logf("[monitor] Бамбук-проба не выполнилась (%d подряд), ошибка на стороне Hub: %v", fails, err)
		} else {
			m.logf("[monitor] Бамбук-проба провалена (%d подряд): %s UDP :%d через exit :%d: %v", fails, label, hop, socksPort, err)
		}
		if fails >= bambooProbeFailThreshold {
			m.raise(bambooAlertKey, level, title, details)
		}
	default:
		if errors.Is(err, errBambooSocks) {
			m.skip(now, "socks", fmt.Sprintf("SOCKS exit не передаёт UDP (порты %v): %v", ports, err))
		} else {
			m.skip(now, "control", fmt.Sprintf("UDP через exit :%d не работает (%v), проба: %v", socksPort, controlErr, err))
		}
	}
}

// verdict notes that the probe really tested Bamboo (success or a counted failure).
func (m *bambooMonitor) verdict(now time.Time) {
	m.blindSince = now
	m.lastSkip = "" // the next skip is news again, even for the same reason
	m.resolve(bambooBlindAlertKey, "Внешняя проба Бамбука снова даёт результат.")
}

// skip records a run without a verdict while Bamboo is deployed; after bambooBlindAfter of
// those the probe itself is reported as blind.
func (m *bambooMonitor) skip(now time.Time, class, reason string) {
	m.logSkip(now, class, reason)
	if blind := now.Sub(m.blindSince); blind > bambooBlindAfter {
		m.raise(bambooBlindAlertKey, AlertWarn, "Бамбук не проверяется",
			fmt.Sprintf("Бамбук развёрнут, но внешняя проба уже %v не даёт результата (ни успеха, ни засчитанного провала): его падение не будет замечено.\nПоследняя причина: %s",
				blind.Round(time.Minute), reason))
	}
}

// notDeployed closes whatever the probe raised: there is nothing to check any more.
func (m *bambooMonitor) notDeployed(now time.Time, class, reason string) {
	m.blindSince = time.Time{} // the blind clock starts over once Bamboo is back
	msg := "Бамбук больше не проверяется: " + reason + "."
	m.resolve(bambooAlertKey, msg)
	m.resolve(bambooBlindAlertKey, msg)
	m.logSkip(now, class, reason)
}

// logSkip logs a skip when its class differs from the previous skip (or a verdict came in
// between) and repeats the same class at most once an hour.
func (m *bambooMonitor) logSkip(now time.Time, class, reason string) {
	if m.skipLogged == nil {
		m.skipLogged = map[string]time.Time{}
	}
	if class != m.lastSkip || now.Sub(m.skipLogged[class]) >= time.Hour {
		m.skipLogged[class] = now
		m.logf("[monitor] Бамбук-проба пропущена: %s (та же причина пишется не чаще раза в час)", reason)
	}
	m.lastSkip = class
}

// bambooFailAlert words a counted failure; the hint depends on what the probe saw. An
// unclassified error is the probe's own failure on the Hub, not Bamboo's answer: warn level.
func bambooFailAlert(fails int, label string, hop, socksPort int, err error) (level AlertLevel, title, details string) {
	level, title = AlertCritical, "Бамбук недоступен снаружи"
	var what, hint string
	switch {
	case errors.Is(err, errBambooWrongSource):
		what = "ответил не с того адреса или порта, на который ушла проба"
		hint = "Клиенты за NAT такие ответы отбрасывают (тот же класс, что сбой 2026-10-12). " +
			"Проверьте порт-хоппинг: iptables -t nat -S PREROUTING (нужен DNAT --to-destination :50443, не REDIRECT)."
	case errors.Is(err, errBambooWrongReply):
		what = "ответил не QUIC Version Negotiation на нашу пробу"
		hint = "Вероятно, пароль obfs в БД (hysteria2_obfs_password) не совпадает с /etc/hysteria/server.yaml: " +
			"hysteria2_sync не перезаписывает server.yaml, пока нет активных Express-планов."
	case errors.Is(err, errBambooNoReply):
		what = "не ответил, хотя контрольный DNS-запрос через тот же exit прошёл"
		hint = "Проверьте systemctl status hysteria2-server, iptables -t nat -S PREROUTING и совпадение пароля obfs " +
			"в БД (hysteria2_obfs_password) и /etc/hysteria/server.yaml."
	default:
		level, title = AlertWarn, "Проба Бамбука не выполняется"
		return level, title, fmt.Sprintf("%d проверок подряд проба Бамбука (%s) не смогла выполниться из-за ошибки на стороне Hub, "+
			"это не ответ Бамбука: %v\nПроверьте main_server_host в настройках и журнал: journalctl -u heyearth-api | grep Бамбук.",
			fails, label, err)
	}
	return level, title, fmt.Sprintf("%d проверок подряд Бамбук (Hysteria2 %s, UDP %d-%d → :50443) %s (exit :%d).\nПоследняя попытка: порт %d, %v\n%s",
		fails, label, bambooHopPortMin, bambooHopPortMax, what, socksPort, hop, err, hint)
}
