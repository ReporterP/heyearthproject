package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/binary"
	"math/big"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/blake2b"

	"github.com/heyearth/backend/internal/config"
)

// Entry 6 of the incident, RFC 5737 addresses.
func testTunnelPlan(t *testing.T) relayTunnelPlan {
	t.Helper()
	p, err := relayTunnelRender(6, 2, 22, "198.51.100.42", "203.0.113.10", "yandex.ru", "obfs-psk")
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// The files are exactly the relay recipe's (the same generators): a later reprovision of the
// entry finds nothing to change.
func TestRelayTunnelRender(t *testing.T) {
	p := testTunnelPlan(t)
	if p.hubTun.String() != "10.77.0.25" || p.relayTun.String() != "10.77.0.26" || p.dev != "he-r6" || p.hubUnitName != "heyearth-tunnel-r6.service" {
		t.Fatalf("names %+v", p)
	}
	nft, _ := config.RelayNftRulesetVia("203.0.113.10", "10.77.0.25")
	nginx, _ := config.RelayNginxConfVia("203.0.113.10", "10.77.0.25")
	trust, _ := config.HubRelayTrustFileVia("198.51.100.42", "10.77.0.26")
	if p.nft != nft || p.nginx != nginx || p.trust != trust || p.jail != config.RelayJail(22, "203.0.113.10") {
		t.Error("the plan's files differ from the recipe's")
	}
	if !strings.Contains(p.hubUnit, "ip tunnel add he-r6 mode gre local 203.0.113.10 remote 198.51.100.42 ttl 64") ||
		!strings.Contains(p.relayUnit, "ip tunnel add he-hub mode gre local 198.51.100.42 remote 203.0.113.10 ttl 64") {
		t.Errorf("units:\n%s\n%s", p.hubUnit, p.relayUnit)
	}
	if _, err := relayTunnelRender(0, 2, 22, "198.51.100.42", "203.0.113.10", "yandex.ru", ""); err == nil {
		t.Error("server id 0 accepted")
	}
	if _, err := relayTunnelRender(6, 2, 22, "198.51.100.42", "10.0.0.1", "yandex.ru", ""); err == nil {
		t.Error("a private Hub address accepted")
	}
}

func TestRelayTunnelScriptsParse(t *testing.T) {
	if !haveBash() {
		t.Skip("no bash")
	}
	p := testTunnelPlan(t)
	relayApply := relayTunnelVars(p, nil) + "RUN_ID=1 GUARD=900\nB64_unit=x B64_nft=x B64_nginx=x B64_jail=x\n" + relayTunnelRelayApplySh
	for name, s := range map[string]string{
		"check":         relayTunnelCheckRun(p),
		"apply":         relayTunnelApplyRun(p, "1", "MATRIX tcp=40/100 udp=55/100", "CHECK tls=40/40 http=10/10"),
		"relay-apply":   relayApply,
		"relay-net":     relayTunnelRelayNetSh(p),
		"relay-confirm": relayTunnelRelayConfirmSh,
		"relay-revert":  relayTunnelRelayRevertSh,
		"exit":          "TB=1 UB=2\n" + relayTunnelExitSh(p),
	} {
		if out, code := runBashStdin(t, s, "-n"); code != 0 {
			t.Errorf("%s: bash -n failed (%d): %s", name, code, out)
		}
	}
	// The secrets of the plan stay out of what the Hub logs: the obfs password is only inside
	// the base64 of the exit's script, never in a plain line.
	if strings.Contains(relayTunnelApplyRun(p, "1", "", ""), "obfs-psk") || strings.Contains(relayTunnelCheckRun(p), "obfs-psk") {
		t.Error("the obfs password is in the job's text in the clear")
	}
}

// The decision of the apply job, run by bash.
func TestRelayTunnelDecide(t *testing.T) {
	if !haveBash() {
		t.Skip("no bash")
	}
	for _, c := range []struct {
		before, after, self string
		ok                  bool
		why                 string
	}{
		{"MATRIX tcp=40/100 udp=55/100", "MATRIX tcp=100/100 udp=100/100", "CHECK tls=40/40 http=10/10", true, ""},
		{"MATRIX tcp=0/100 udp=0/100", "MATRIX tcp=99/100 udp=100/100", "CHECK tls=39/40 http=10/10", true, ""}, // 39/40 = 97 %
		{"MATRIX tcp=40/100 udp=55/100", "MATRIX tcp=98/100 udp=100/100", "CHECK tls=40/40 http=10/10", false, "TCP 2443 после 98%"},
		{"MATRIX tcp=40/100 udp=55/100", "MATRIX tcp=100/100 udp=90/100", "CHECK tls=40/40 http=10/10", false, "UDP после 90%"},
		{"MATRIX tcp=40/100 udp=0/0", "MATRIX tcp=100/100 udp=0/0", "CHECK tls=40/40 http=10/10", true, ""}, // no Bamboo: UDP skipped
		{"MATRIX tcp=40/100 udp=55/100", "MATRIX tcp=100/100 udp=100/100", "CHECK tls=30/40 http=10/10", false, "TLS :443 входа 75%"},
		{"MATRIX tcp=40/100 udp=55/100", "", "", false, "TCP 2443 после -1%"}, // nothing measured
	} {
		script := "MIN=99 TLSMIN=95\n" + relayTunnelDecideSh + "decide " + shQuote(c.before) + " " + shQuote(c.after) + " " + shQuote(c.self) + "\n"
		out, code := runBashStdin(t, script)
		out = strings.TrimSpace(out)
		if code != 0 || strings.HasPrefix(out, "OK") != c.ok || (c.why != "" && !strings.Contains(out, c.why)) {
			t.Errorf("%+v: %q (%d)", c, out, code)
		}
	}
}

func TestRelayTunnelParseCheck(t *testing.T) {
	l := "[relay-tunnel] x\n" + relayTunnelRoutesMark + "\ndefault via 198.51.100.1 dev eth0\n" + relayTunnelAddrsMark +
		"\n2: eth0    inet 198.51.100.42/24 scope global eth0\n" + relayTunnelNetEnd +
		"\nSELF CHECK tls=21/40 http=6/10\nRELAY_NET_DONE\nBEFORE MATRIX tcp=41/100 udp=52/100\nBEFORE failed tcp source ports: 42001\n"
	routes, addrs, self, m, err := relayTunnelParseCheck(l)
	if err != nil || !strings.Contains(routes, "default via") || !strings.Contains(addrs, "198.51.100.42/24") ||
		self != "CHECK tls=21/40 http=6/10" || m != "MATRIX tcp=41/100 udp=52/100" {
		t.Fatalf("%q %q %q %q %v", routes, addrs, self, m, err)
	}
	if _, _, _, _, err := relayTunnelParseCheck("RELAY_TUNNEL_FAIL SSH до входа 6 не удался\n"); err == nil {
		t.Error("a log without the entry's network accepted")
	}
}

// relay_matrix.py against a TLS listener and a Hysteria-like UDP responder (salamander + Version
// Negotiation, as cmd/api bamboo_probe.go expects it), all on 127.0.0.1.
func TestRelayMatrixPy(t *testing.T) {
	py := ""
	for _, c := range []string{"python3", "python"} {
		if p, err := exec.LookPath(c); err == nil && exec.Command(p, "-c", "import ssl, hashlib; hashlib.blake2b(b'', digest_size=32)").Run() == nil {
			py = p
			break
		}
	}
	if py == "" {
		t.Skip("no python3")
	}
	ln, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{Certificates: []tls.Certificate{selfSigned(t)}})
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
			go func() { _ = c.(*tls.Conn).Handshake(); c.Close() }()
		}
	}()
	psk := []byte("obfs-psk")
	ports := []int{30000, 30349} // relay_matrix.py: 30000 + i·349 for i = 0, 1
	for i, port := range ports {
		pc, err := net.ListenPacket("udp", "127.0.0.1:"+strconv.Itoa(port))
		if err != nil {
			t.Skipf("UDP %d busy: %v", port, err)
		}
		defer pc.Close()
		silent := i == 1 // the second hop port never answers: a failure must be counted
		go func() {
			buf := make([]byte, 2048)
			for {
				n, from, err := pc.ReadFrom(buf)
				if err != nil {
					return
				}
				if silent || n <= 8 {
					continue
				}
				in := salamanderTest(psk, buf[:8], buf[8:n])
				// Version Negotiation: long header, version 0, DCID = the probe's SCID, SCID = its DCID.
				vn := []byte{0x80, 0, 0, 0, 0, 8}
				vn = append(vn, in[15:23]...)
				vn = append(vn, 8)
				vn = append(vn, in[6:14]...)
				vn = binary.BigEndian.AppendUint32(vn, 1)
				salt := []byte("saltsalt")
				_, _ = pc.WriteTo(append(append([]byte{}, salt...), salamanderTest(psk, salt, vn)...), from)
			}
		}()
	}
	dir := t.TempDir()
	script := filepath.Join(dir, "m.py")
	if err := os.WriteFile(script, []byte(relayMatrixPy), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(py, "-I", script, "127.0.0.1", "example.org", "3", "2", "46001", "47001")
	cmd.Env = append(os.Environ(), "PSK="+string(psk), "TCP_PORT="+strconv.Itoa(ln.Addr().(*net.TCPAddr).Port))
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.Contains(string(out), "MATRIX tcp=3/3 udp=1/2") || !strings.Contains(string(out), "failed udp source ports: 47002") {
		t.Fatalf("matrix:\n%s", out)
	}
}

func salamanderTest(psk, salt, in []byte) []byte {
	key := blake2b.Sum256(append(append([]byte{}, psk...), salt...))
	out := make([]byte, len(in))
	for i, b := range in {
		out[i] = b ^ key[i%len(key)]
	}
	return out
}

func selfSigned(t *testing.T) tls.Certificate {
	t.Helper()
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "example.org"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour)}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &k.PublicKey, k)
	if err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: k}
}

// TestRelayTunnelLabDump writes the apply job of entry 6 for the container lab
// (scratchpad tunlab-e2e.sh) when RELAY_TUNNEL_LAB_DIR is set.
func TestRelayTunnelLabDump(t *testing.T) {
	dir := os.Getenv("RELAY_TUNNEL_LAB_DIR")
	if dir == "" {
		t.Skip("RELAY_TUNNEL_LAB_DIR not set")
	}
	p := testTunnelPlan(t)
	old, _ := config.RelayNftRuleset(p.hub)
	oldNginx, _ := config.RelayNginxConf(p.hub)
	oldTrust, _ := config.HubRelayTrustFile(p.relay)
	for name, body := range map[string]string{
		"apply.sh": relayTunnelApplyRun(p, "77", "MATRIX tcp=40/100 udp=50/100", "CHECK tls=38/40 http=10/10"),
		"check.sh": relayTunnelCheckRun(p), "old.nft": old, "old.nginx": oldNginx, "old.trust": oldTrust,
		"old.jail": config.RelayJail(22, "203.0.113.99"), "new.nft": p.nft, "new.nginx": p.nginx, "new.trust": p.trust,
		"new.hubunit": p.hubUnit, "new.relayunit": p.relayUnit, "new.allow": ddosAllowScript,
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestRelayTunnelFlagSQL(t *testing.T) {
	if got := relayTunnelFlagSQL(6); got != "update servers set relay_tunnel = true, updated_at = now() where id = 6 and role = 'relay'" {
		t.Fatalf("flag SQL: %q", got)
	}
}
