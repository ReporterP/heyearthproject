// Package mtprobe checks an MTProxy (mtg, fake-TLS mode) the way a Telegram client reaches it: it
// sends the fake-TLS ClientHello made for the proxy's secret and checks that the answer is mtg's
// ServerHello for that secret.
//
// A bare TCP connect proves nothing about mtg: the kernel completes the handshake into the
// listener's backlog whether or not mtg ever accepts it. On 13-14.11.2026 mtg 2.2.8 stopped
// accepting on all three exits while it kept running and listening; TCP probes stayed green for
// about 30 hours, until the backlog held 4096 connections. The handshake fails within seconds.
//
// The format is mtg's (mtglib/internal/tls/fake, identical in 2.2.8 and 2.3.0): the client random
// is HMAC-SHA256(key, ClientHello record with a zero random), XOR the Unix time in its last four
// bytes (little endian); mtg answers ServerHello + ChangeCipherSpec + one application-data record,
// and puts HMAC-SHA256(key, client random || that answer with a zero random) into the
// ServerHello's random.
package mtprobe

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"time"
)

const (
	recordHandshake    = 0x16
	recordCCS          = 0x14
	recordAppData      = 0x17
	handshakeClient    = 0x01
	handshakeServer    = 0x02
	randomLen          = 32
	randomOffset       = 1 + 2 + 2 + 1 + 3 + 2 // record header, handshake header, version
	maxRecordPayload   = 16384 + 256
	clientHelloPadding = 517 // a browser-sized ClientHello record, like Telegram's
)

// Secret is an mtg fake-TLS secret: the 16-byte key and the domain sent as SNI.
type Secret struct {
	Key  [16]byte
	Host string
}

// ParseSecret reads the hex form "ee" + 32 hex (key) + hex(domain), as biz.BuildMTProtoSecret and
// `mtg generate-secret --hex` make it.
func ParseSecret(s string) (Secret, error) {
	s = strings.TrimSpace(s)
	if len(s) < 2+32+2 || !strings.EqualFold(s[:2], "ee") {
		return Secret{}, errors.New("секрет MTProxy не в формате mtg (ee + 32 hex-символа + домен в hex)")
	}
	raw, err := hex.DecodeString(s[2:])
	if err != nil {
		return Secret{}, errors.New("секрет MTProxy не в формате mtg (ee + 32 hex-символа + домен в hex)")
	}
	var sec Secret
	copy(sec.Key[:], raw[:16])
	sec.Host = string(raw[16:])
	return sec, nil
}

// ErrNoAnswer: TCP connected, but nothing came back to the ClientHello before the deadline — mtg
// does not accept connections (the 13-14.11 failure), or it took the hello for a stranger's
// (other secret, clock off by more than 3 s) and fronts it to a site that does not answer.
var ErrNoAnswer = errors.New("TCP-соединение есть, но на ClientHello нет ответа: mtg не принимает подключения, либо секрет на ноде другой или часы расходятся больше чем на 3 с")

// ErrNotMTG: an answer came, but it is not mtg's ServerHello for this secret — mtg fronted the
// hello to a real site (the node runs another secret, or the clocks differ by more than 3 s), or
// something else listens on the port.
var ErrNotMTG = errors.New("ответ на ClientHello не от mtg с этим секретом")

// Handshake connects to addr with dial (nil: a net.Dialer) and runs the fake-TLS handshake for
// secret. nil means mtg accepted the connection, recognised the secret and answered. ctx bounds
// the whole probe (connect included): give it a deadline.
func Handshake(ctx context.Context, dial func(ctx context.Context, network, addr string) (net.Conn, error), addr string, secret Secret) error {
	if dial == nil {
		dial = (&net.Dialer{}).DialContext
	}
	conn, err := dial(ctx, "tcp", addr)
	if err != nil {
		return fmt.Errorf("TCP: %w", err)
	}
	defer conn.Close()
	if dl, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(dl)
	}
	stop := context.AfterFunc(ctx, func() { _ = conn.SetDeadline(time.Unix(1, 0)) })
	defer stop()

	hello, clientRandom, err := ClientHello(secret, time.Now())
	if err != nil {
		return err
	}
	if _, err := conn.Write(hello); err != nil {
		return fmt.Errorf("отправка ClientHello: %w", err)
	}
	answer, err := readAnswer(conn)
	if err != nil {
		var ne net.Error
		if errors.As(err, &ne) && ne.Timeout() && len(answer) == 0 {
			return ErrNoAnswer
		}
		return fmt.Errorf("%w: %v", ErrNotMTG, err)
	}
	if !VerifyServerHello(secret, clientRandom, answer) {
		return ErrNotMTG
	}
	return nil
}

// ClientHello builds the fake-TLS ClientHello record for secret at time now and returns it with
// its client random.
func ClientHello(secret Secret, now time.Time) (record []byte, clientRandom [randomLen]byte, err error) {
	if secret.Host == "" {
		return nil, clientRandom, errors.New("в секрете MTProxy нет домена")
	}
	rnd := func(n int) []byte {
		b := make([]byte, n)
		if _, e := rand.Read(b); e != nil && err == nil {
			err = e
		}
		return b
	}

	var body bytes.Buffer
	body.Write([]byte{0x03, 0x03})      // legacy_version TLS 1.2
	body.Write(make([]byte, randomLen)) // random: filled in below
	body.WriteByte(32)                  // session id
	body.Write(rnd(32))
	suites := []uint16{0x1301, 0x1302, 0x1303, 0xc02b, 0xc02f, 0xc02c, 0xc030, 0xcca9, 0xcca8}
	writeU16(&body, uint16(2*len(suites)))
	for _, s := range suites {
		writeU16(&body, s)
	}
	body.Write([]byte{0x01, 0x00}) // compression: null

	var ext bytes.Buffer
	host := []byte(secret.Host)
	sni := make([]byte, 0, 5+len(host))
	sni = binary.BigEndian.AppendUint16(sni, uint16(3+len(host))) // server_name_list length
	sni = append(sni, 0)                                          // host_name
	sni = binary.BigEndian.AppendUint16(sni, uint16(len(host)))
	sni = append(sni, host...)
	writeExt(&ext, 0x0000, sni)                                        // server_name
	writeExt(&ext, 0x000a, []byte{0x00, 0x04, 0x00, 0x1d, 0x00, 0x17}) // supported_groups: x25519, secp256r1
	writeExt(&ext, 0x000d, []byte{0x00, 0x08, 0x04, 0x03, 0x08, 0x04, 0x04, 0x01, 0x05, 0x03})
	writeExt(&ext, 0x002b, []byte{0x04, 0x03, 0x04, 0x03, 0x03}) // supported_versions: 1.3, 1.2
	writeExt(&ext, 0x002d, []byte{0x01, 0x01})                   // psk_key_exchange_modes
	keyShare := append([]byte{0x00, 0x24, 0x00, 0x1d, 0x00, 0x20}, rnd(32)...)
	writeExt(&ext, 0x0033, keyShare) // key_share: x25519
	if err != nil {
		return nil, clientRandom, fmt.Errorf("случайные байты: %w", err)
	}
	// padding (RFC 7685) up to a browser-sized record
	if pad := clientHelloPadding - (5 + 4 + body.Len() + 2 + ext.Len() + 4); pad > 0 {
		writeExt(&ext, 0x0015, make([]byte, pad))
	}
	writeU16(&body, uint16(ext.Len()))
	body.Write(ext.Bytes())

	rec := make([]byte, 0, 9+body.Len())
	rec = append(rec, recordHandshake, 0x03, 0x01)
	rec = binary.BigEndian.AppendUint16(rec, uint16(4+body.Len()))
	rec = append(rec, handshakeClient, byte(body.Len()>>16), byte(body.Len()>>8), byte(body.Len()))
	rec = append(rec, body.Bytes()...)

	mac := hmac.New(sha256.New, secret.Key[:])
	mac.Write(rec) // the random is still zero
	digest := mac.Sum(nil)
	var ts [4]byte
	binary.LittleEndian.PutUint32(ts[:], uint32(now.Unix()))
	for i := range 4 {
		digest[randomLen-4+i] ^= ts[i]
	}
	copy(clientRandom[:], digest)
	copy(rec[randomOffset:], digest)
	return rec, clientRandom, nil
}

// VerifyServerHello reports whether answer (mtg's first flight: ServerHello, ChangeCipherSpec,
// application data) carries the HMAC mtg puts into the ServerHello random for this secret and
// client random.
func VerifyServerHello(secret Secret, clientRandom [randomLen]byte, answer []byte) bool {
	if len(answer) < randomOffset+randomLen || answer[0] != recordHandshake || answer[5] != handshakeServer {
		return false
	}
	got := append([]byte(nil), answer[randomOffset:randomOffset+randomLen]...)
	zeroed := append([]byte(nil), answer...)
	clear(zeroed[randomOffset : randomOffset+randomLen])
	mac := hmac.New(sha256.New, secret.Key[:])
	mac.Write(clientRandom[:])
	mac.Write(zeroed)
	return hmac.Equal(got, mac.Sum(nil))
}

// readAnswer reads the three records of mtg's first flight: handshake, change_cipher_spec,
// application data. A server that sends something else fails here or in VerifyServerHello.
func readAnswer(r io.Reader) ([]byte, error) {
	var out []byte
	for i, want := range []byte{recordHandshake, recordCCS, recordAppData} {
		var h [5]byte
		if n, err := io.ReadFull(r, h[:]); err != nil {
			if i == 0 && n == 0 {
				return nil, err
			}
			return out, fmt.Errorf("запись %d ответа оборвана: %w", i+1, err)
		}
		if h[0] != want {
			return out, fmt.Errorf("запись %d ответа типа %#x, ожидалась %#x", i+1, h[0], want)
		}
		n := int(binary.BigEndian.Uint16(h[3:]))
		if n > maxRecordPayload {
			return out, fmt.Errorf("запись %d ответа длиной %d", i+1, n)
		}
		rec := make([]byte, 5+n)
		copy(rec, h[:])
		if _, err := io.ReadFull(r, rec[5:]); err != nil {
			return out, fmt.Errorf("запись %d ответа оборвана: %w", i+1, err)
		}
		out = append(out, rec...)
	}
	return out, nil
}

func writeU16(b *bytes.Buffer, v uint16) { _ = binary.Write(b, binary.BigEndian, v) }

func writeExt(b *bytes.Buffer, typ uint16, data []byte) {
	writeU16(b, typ)
	writeU16(b, uint16(len(data)))
	b.Write(data)
}
