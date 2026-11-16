# Local TCP forwarder to the Hub's sshd for when this PC cannot reach port 22 directly.
#
# Two ways out (pick the one that works on the current network):
#   --via-proxy 127.0.0.1:10809   HTTP CONNECT through Happ's local HTTP proxy to --target
#                                  (use the HOSTNAME, e.g. hiearth.ru:22: the Hub refuses its own
#                                  IP through the tunnel, a domain target is dialled from an exit)
#   --via-nic 192.168.1.131       dial --target from this physical NIC address, bypassing the
#                                  Happ TUN route (works when the network itself allows TCP 22)
#
# Then point the deploy tools at 127.0.0.1:<listen>: for cmd/deploy set DEPLOY_HOST=127.0.0.1
# (and DEPLOY_PORT=<listen> if it is not 22); for quick_deploy.go / quick_admin_web.go edit
# DEPLOY_HOST / DEPLOY_PORT only in the clean worktree's copy of server/.env, never the original.
# The Hub host key stays pinned (DEPLOY_HOST_KEY_FINGERPRINT), so the path needs no trust.
#
#   py -3 -I hubfwd.py --listen 2222 --target hiearth.ru:22 --via-proxy 127.0.0.1:10809
#   py -3 -I hubfwd.py --listen 2222 --target <hub-ip>:22 --via-nic <this-pc-lan-ip>
import argparse
import socket
import sys
import threading


def pipe(a, b):
    try:
        while True:
            d = a.recv(65536)
            if not d:
                break
            b.sendall(d)
    except OSError:
        pass
    finally:
        for s in (a, b):
            try:
                s.shutdown(socket.SHUT_RDWR)
            except OSError:
                pass


def dial(args):
    host, port = args.target.rsplit(":", 1)
    if args.via_proxy:
        ph, pp = args.via_proxy.rsplit(":", 1)
        u = socket.create_connection((ph, int(pp)), timeout=15)
        u.sendall(f"CONNECT {host}:{port} HTTP/1.1\r\nHost: {host}:{port}\r\n\r\n".encode())
        head = b""
        while b"\r\n\r\n" not in head:
            chunk = u.recv(1)
            if not chunk:
                raise OSError("proxy closed during CONNECT")
            head += chunk
        status = head.split(b"\r\n", 1)[0]
        if b" 200" not in status:
            raise OSError("proxy refused CONNECT: " + status.decode(errors="replace"))
    else:
        u = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
        if args.via_nic:
            u.bind((args.via_nic, 0))
        u.settimeout(15)
        u.connect((host, int(port)))
    u.settimeout(None)
    return u


def handle(c, args):
    try:
        u = dial(args)
    except OSError as e:
        print("upstream failed:", e, flush=True)
        c.close()
        return
    threading.Thread(target=pipe, args=(c, u), daemon=True).start()
    threading.Thread(target=pipe, args=(u, c), daemon=True).start()


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--listen", type=int, default=2222)
    ap.add_argument("--target", required=True, help="host:port of the Hub's sshd")
    g = ap.add_mutually_exclusive_group(required=True)
    g.add_argument("--via-proxy", help="HTTP proxy host:port for CONNECT (Happ: 127.0.0.1:10809)")
    g.add_argument("--via-nic", help="local physical NIC address to dial from")
    args = ap.parse_args()
    srv = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
    srv.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
    srv.bind(("127.0.0.1", args.listen))
    srv.listen(16)
    print(f"127.0.0.1:{args.listen} -> {args.target}", flush=True)
    while True:
        c, _ = srv.accept()
        threading.Thread(target=handle, args=(c, args), daemon=True).start()


if __name__ == "__main__":
    sys.exit(main())
