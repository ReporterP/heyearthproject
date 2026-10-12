package main

import (
	crand "crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net"
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

const (
	bambooAlertKey           = "bamboo-unreachable-external"
	bambooProbeFailThreshold = 3
	bambooHopPortMin         = 30000 // port-hopping range DNATed to :50443 (cmd/deploy hysteria2PortHopRule)
	bambooHopPortMax         = 65000
	bambooProbeVersion       = 0x1a2a3a4a // reserved 0x?a?a?a?a pattern (RFC 9000 §15): never supported
	bambooProbePacketLen     = 1200       // quic-go ignores unknown-version packets shorter than this
	salamanderSaltLen        = 8
)

var (
	bambooProbeState   loopProbeStreak // touched only by the loop-probe goroutine
	bambooProbeOKLog   logGate         // rate-limits the "probe OK" log line to once an hour
	bambooProbeSkipLog logGate         // rate-limits the "probe skipped" log line to once an hour
)

// Failure classes of probeBamboo. Only errBambooNoReply and errBambooWrongReply say anything
// about Bamboo itself; errBambooSocks is our side (the exit tunnel's local SOCKS5).
var (
	errBambooSocks      = errors.New("сбой SOCKS exit-туннеля")
	errBambooNoReply    = errors.New("нет ответа")
	errBambooWrongReply = errors.New("неверный ответ")
)

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

// socksAssociateHandshake returns the relay address from the ASSOCIATE reply. Only an IPv4
// BND.ADDR is supported; 0.0.0.0 means "the SOCKS server's own address".
func socksAssociateHandshake(ctrl net.Conn, deadline time.Time) (*net.UDPAddr, error) {
	if err := ctrl.SetDeadline(deadline); err != nil {
		return nil, err
	}
	if _, err := ctrl.Write([]byte{5, 1, 0}); err != nil { // VER, NMETHODS, METHOD no-auth
		return nil, fmt.Errorf("приветствие: %w", err)
	}
	var b [10]byte
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
	switch {
	case b[0] != 5:
		return nil, fmt.Errorf("ответ на UDP ASSOCIATE: версия %d", b[0])
	case b[1] != 0:
		return nil, fmt.Errorf("UDP ASSOCIATE отклонён: код %d", b[1])
	case b[3] != 1:
		return nil, fmt.Errorf("адрес реле с ATYP %d не поддерживается", b[3])
	}
	if _, err := io.ReadFull(ctrl, b[4:10]); err != nil {
		return nil, fmt.Errorf("адрес реле: %w", err)
	}
	relay := &net.UDPAddr{IP: net.IPv4(b[4], b[5], b[6], b[7]), Port: int(binary.BigEndian.Uint16(b[8:10]))}
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

// ── the probe ──

// bambooProber holds the knobs of probeBamboo; tests shrink the timeout.
type bambooProber struct {
	socksHost      string        // where the exit tunnel's SOCKS5 listens
	attempts       int           // UDP may lose a datagram: retry before calling it a failure
	attemptTimeout time.Duration // one attempt: SOCKS handshake + UDP round trip
	rand           io.Reader
}

var defaultBambooProber = bambooProber{socksHost: "127.0.0.1", attempts: 2, attemptTimeout: 5 * time.Second, rand: crand.Reader}

// probeBamboo checks that Bamboo answers on host from outside: through the exit SOCKS on
// 127.0.0.1:socksPort it sends a VN probe to a random hopping port and waits for Version
// Negotiation. port is the hopping port of the last attempt.
func probeBamboo(socksPort int, host string, psk []byte) (rtt time.Duration, port int, err error) {
	return defaultBambooProber.probe(socksPort, host, psk)
}

func (p bambooProber) probe(socksPort int, host string, psk []byte) (rtt time.Duration, port int, err error) {
	for range max(p.attempts, 1) {
		port = randomHopPort(p.rand)
		if rtt, err = p.attempt(socksPort, host, port, psk); err == nil {
			return rtt, port, nil
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
	deadline := time.Now().Add(p.attemptTimeout)
	target := net.JoinHostPort(host, strconv.Itoa(port))
	hdr, err := socksUDPHeader(host, port)
	if err != nil {
		return 0, fmt.Errorf("адрес %s: %w", target, err)
	}
	pkt, err := salamanderObfuscate(p.rand, psk, buildVNProbePacket(p.rand))
	if err != nil {
		return 0, err
	}
	socksAddr := net.JoinHostPort(p.socksHost, strconv.Itoa(socksPort))
	assoc, err := socksUDPAssociate(socksAddr, deadline)
	if err != nil {
		return 0, fmt.Errorf("%w %s: %v", errBambooSocks, socksAddr, err)
	}
	defer assoc.Close()

	start := time.Now()
	if _, err := assoc.udp.Write(append(hdr, pkt...)); err != nil {
		return 0, fmt.Errorf("%w %s: отправка в реле: %v", errBambooSocks, socksAddr, err)
	}
	buf := make([]byte, 2048)
	n, err := assoc.udp.Read(buf)
	if err != nil {
		var ne net.Error
		if errors.As(err, &ne) && ne.Timeout() {
			return 0, fmt.Errorf("%w от %s за %v", errBambooNoReply, target, p.attemptTimeout)
		}
		return 0, fmt.Errorf("%w %s: чтение из реле: %v", errBambooSocks, socksAddr, err)
	}
	rtt := time.Since(start)
	_, payload, err := parseSocksUDPDatagram(buf[:n])
	if err != nil {
		return 0, fmt.Errorf("%w от %s: %v", errBambooWrongReply, target, err)
	}
	plain, err := salamanderDeobfuscate(psk, payload)
	if err != nil {
		return 0, fmt.Errorf("%w от %s: %v", errBambooWrongReply, target, err)
	}
	if !isVersionNegotiation(plain) {
		return 0, fmt.Errorf("%w от %s: не QUIC Version Negotiation (%d байт, начало % x)",
			errBambooWrongReply, target, len(plain), plain[:min(len(plain), 8)])
	}
	return rtt, nil
}

func logBambooProbeSkipped(reason string) {
	if bambooProbeSkipLog.allow(time.Now(), time.Hour) {
		log.Printf("[monitor] Бамбук-проба пропущена: %s (повтор записи не чаще раза в час)", reason)
	}
}

func runBambooProbe() {
	if _, err := os.Stat(hysteria2ConfigPath); errors.Is(err, fs.ErrNotExist) {
		logBambooProbeSkipped("нет " + hysteria2ConfigPath + ", Бамбук не развёрнут")
		return
	}
	// Not biz.GetHysteria2ObfsPassword: it generates and stores a password as a side effect.
	psk := strings.TrimSpace(biz.GetSettingOrDefault("hysteria2_obfs_password", ""))
	if psk == "" {
		logBambooProbeSkipped("не задан hysteria2_obfs_password")
		return
	}
	socksPort, ok := pickExitSocksPort()
	if !ok {
		logBambooProbeSkipped("нет здорового exit с локальным SOCKS")
		return
	}
	host := monitorEntryHost()
	rtt, port, err := probeBamboo(socksPort, host, []byte(psk))
	if err == nil {
		bambooProbeState.record(time.Now(), true)
		if bambooProbeOKLog.allow(time.Now(), time.Hour) {
			log.Printf("[monitor] Бамбук-проба OK: %s UDP :%d ответил за %v (через exit :%d)", host, port, rtt.Round(time.Millisecond), socksPort)
		}
		ResolveAlert(bambooAlertKey, fmt.Sprintf("Бамбук снова отвечает снаружи: %s UDP :%d за %v (проба через exit :%d).",
			host, port, rtt.Round(time.Millisecond), socksPort))
		return
	}
	if errors.Is(err, errBambooSocks) { // the exit's SOCKS cannot relay UDP: says nothing about Bamboo
		logBambooProbeSkipped(err.Error())
		return
	}
	dialer, terr := exitSocksDialer(socksPort)
	if terr == nil {
		terr = socksDialCheck(dialer, "1.1.1.1:443")
	}
	if terr != nil { // a broken tunnel says nothing about Bamboo
		logBambooProbeSkipped(fmt.Sprintf("туннель exit :%d не работает: %v (проба: %v)", socksPort, terr, err))
		return
	}
	fails := bambooProbeState.record(time.Now(), false)
	log.Printf("[monitor] Бамбук-проба провалена (%d подряд): %s UDP :%d через exit :%d: %v", fails, host, port, socksPort, err)
	if fails >= bambooProbeFailThreshold {
		RaiseAlert(bambooAlertKey, AlertCritical, "Бамбук недоступен снаружи",
			fmt.Sprintf("%d проверок подряд Бамбук (Hysteria2 %s, UDP %d-%d → :50443) не ответил через exit :%d, туннель exit при этом работает.\n"+
				"Последняя попытка: порт %d, %v",
				fails, host, bambooHopPortMin, bambooHopPortMax, socksPort, port, err))
	}
}
