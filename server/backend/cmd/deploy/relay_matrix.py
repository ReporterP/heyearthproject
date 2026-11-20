# The per-flow matrix of deploy --relay-tunnel (incident 2026-11-20), run on an exit node:
# connections to the reserve entry from many fixed source ports, so every one is its own
# 5-tuple on the entry -> Hub leg (the entry's DNAT and masquerade keep the source port).
#   TCP: a TLS handshake with the Reality SNI to <entry>:2443 (XHTTP «Дуб»: DNAT into the tunnel).
#   UDP: a salamander-obfuscated QUIC packet of an unsupported version to a hop port; Hysteria
#        (quic-go) on the Hub answers with Version Negotiation (cmd/api bamboo_probe.go).
# Usage: python3 relay_matrix.py <entry> <sni> <ntcp> <nudp> <tcp_base_port> <udp_base_port>
#        (env PSK = the obfs password; empty: UDP skipped)
# Prints: MATRIX tcp=<ok>/<n> udp=<ok>/<n> and the source ports that failed.
import hashlib
import os
import socket
import ssl
import struct
import sys
import threading

entry, sni = sys.argv[1], sys.argv[2]
ntcp, nudp = int(sys.argv[3]), int(sys.argv[4])
tcp_base, udp_base = int(sys.argv[5]), int(sys.argv[6])
psk = os.environ.get("PSK", "").encode()
tcp_port = int(os.environ.get("TCP_PORT", "2443"))  # tests only
if not psk:
    nudp = 0
lock = threading.Lock()
ok = {"tcp": 0, "udp": 0}
bad = {"tcp": [], "udp": []}
skipped = {"tcp": 0, "udp": 0}  # a source port this host cannot bind says nothing about the path
sem = threading.Semaphore(20)


def tcp_probe(i):
    src = tcp_base + i
    with sem:
        s = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
        try:
            s.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
            s.setsockopt(socket.SOL_SOCKET, socket.SO_LINGER, struct.pack("ii", 1, 0))  # RST: no TIME_WAIT
            try:
                s.bind(("0.0.0.0", src))
            except OSError:
                with lock:
                    skipped["tcp"] += 1
                return
            s.settimeout(6)
            s.connect((entry, tcp_port))
            c = ssl.create_default_context()
            c.check_hostname = False
            c.verify_mode = ssl.CERT_NONE
            t = c.wrap_socket(s, server_hostname=sni)
            t.close()
            with lock:
                ok["tcp"] += 1
            return
        except Exception:
            pass
        finally:
            try:
                s.close()
            except Exception:
                pass
        with lock:
            bad["tcp"].append(src)


def salamander(salt, data):
    key = hashlib.blake2b(psk + salt, digest_size=32).digest()
    return bytes(b ^ key[i % 32] for i, b in enumerate(data))


def udp_probe(i):
    src = udp_base + i
    port = 30000 + (i * 349) % 35001  # spread over the hop range 30000-65000
    with sem:
        s = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
        try:
            try:
                s.bind(("0.0.0.0", src))
            except OSError:
                with lock:
                    skipped["udp"] += 1
                return
            s.settimeout(4)
            p = bytearray(os.urandom(1200))
            p[0] = 0xC0 | (p[0] & 0x0F)
            p[1:5] = struct.pack(">I", 0x1A2A3A4A)
            p[5] = 8
            p[14] = 8
            dcid, scid = bytes(p[6:14]), bytes(p[15:23])
            salt = os.urandom(8)
            for _ in range(2):  # a datagram lost on the way is not a dead path
                s.sendto(salt + salamander(salt, bytes(p)), (entry, port))
                try:
                    data, frm = s.recvfrom(4096)
                except socket.timeout:
                    continue
                if frm[0] != entry or frm[1] != port or len(data) <= 8:
                    continue
                vn = salamander(data[:8], data[8:])
                if len(vn) < 7 or not vn[0] & 0x80 or vn[1:5] != b"\x00\x00\x00\x00":
                    continue
                d_end = 6 + vn[5]
                if len(vn) < d_end + 1:
                    continue
                s_end = d_end + 1 + vn[d_end]
                if vn[6:d_end] == scid and vn[d_end + 1:s_end] == dcid:
                    with lock:
                        ok["udp"] += 1
                    return
        except Exception:
            pass
        finally:
            s.close()
        with lock:
            bad["udp"].append(src)


ts = [threading.Thread(target=tcp_probe, args=(i,)) for i in range(ntcp)]
ts += [threading.Thread(target=udp_probe, args=(i,)) for i in range(nudp)]
for t in ts:
    t.start()
for t in ts:
    t.join()
print("MATRIX tcp=%d/%d udp=%d/%d" % (ok["tcp"], ntcp - skipped["tcp"], ok["udp"], nudp - skipped["udp"]))
if skipped["tcp"] or skipped["udp"]:
    print("skipped (source port busy on this host): tcp %d udp %d" % (skipped["tcp"], skipped["udp"]))
if bad["tcp"]:
    print("failed tcp source ports: " + " ".join(str(p) for p in sorted(bad["tcp"])))
if bad["udp"]:
    print("failed udp source ports: " + " ".join(str(p) for p in sorted(bad["udp"])))
