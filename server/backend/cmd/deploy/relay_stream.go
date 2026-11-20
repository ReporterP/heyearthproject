package main

// --relay-stream ID: converge one reserve entry's nginx stream to config.RelayNginxConf (TCP 443/80
// to the Hub, each connect retried on a new 5-tuple — incident 2026-11-20) without the provisioning
// job. The job ends with activate and would put an entry that is out of rotation back into the
// subscription; this mode touches nginx.conf only and never the entry's lifecycle.
//
// It runs on the entry through the Hub (nodessh: the Hub's key, the owner-confirmed host key),
// retrying the SSH session (the incident's transit drops some connections to the entry's port 22
// too). On the entry, before and after the change, the same check runs: TLS handshakes with the
// Reality SNI to its own 443 (nginx → Hub :8447 → Reality answers with the dest site's
// certificate: the whole relay → Hub path) and HTTP to its own 80. Auto-rollback: the previous
// nginx.conf is kept next to it and comes back when `nginx -t` fails, or when the check after the
// change is below relayStreamMinPct or worse than before. --relay-stream-check measures only.

import (
	"encoding/base64"
	"fmt"
	"net/netip"
	"strconv"
	"strings"
	"time"

	"github.com/heyearth/backend/internal/biz"
	"github.com/heyearth/backend/internal/config"
	"golang.org/x/crypto/ssh"
)

const (
	relayStreamTLSProbes = 40
	relayStreamHTTPProbe = 10
	relayStreamMinPct    = 90 // the after-check below this rolls back
	relayStreamSSHTries  = 15
)

// relayStreamCheckPy: probes in parallel, so the old 10 s connect timeout does not stretch the
// before-check; prints "CHECK tls=<ok>/<n> http=<ok>/<n>".
const relayStreamCheckPy = `import socket, ssl, sys, threading
sni, host = sys.argv[1], sys.argv[2]
ntls, nhttp = int(sys.argv[3]), int(sys.argv[4])
res = {"tls": 0, "http": 0}
lock = threading.Lock()
def tls():
    try:
        c = ssl.create_default_context(); c.check_hostname = False; c.verify_mode = ssl.CERT_NONE
        with socket.create_connection(("127.0.0.1", 443), timeout=15) as s:
            s.settimeout(15)
            with c.wrap_socket(s, server_hostname=sni):
                pass
        with lock: res["tls"] += 1
    except Exception:
        pass
def http():
    try:
        with socket.create_connection(("127.0.0.1", 80), timeout=15) as s:
            s.settimeout(15)
            s.sendall(("HEAD / HTTP/1.1\r\nHost: %s\r\nConnection: close\r\n\r\n" % host).encode())
            if s.recv(16).startswith(b"HTTP/"):
                with lock: res["http"] += 1
    except Exception:
        pass
ts = [threading.Thread(target=tls) for _ in range(ntls)] + [threading.Thread(target=http) for _ in range(nhttp)]
for t in ts: t.start()
for t in ts: t.join()
print("CHECK tls=%d/%d http=%d/%d" % (res["tls"], ntls, res["http"], nhttp))
`

// relayStreamEntrySh is the script run on the entry: $NEWB64 the new nginx.conf, $HUB the Hub
// address it must already point at, $SNI/$HOST for the check, $APPLY 0|1.
const relayStreamEntrySh = `set -u
CONF=${RELAY_NGINX_CONF:-/etc/nginx/nginx.conf}; PREV="$CONF.heyearth-prev"
NEW=$(mktemp); PY=$(mktemp); trap 'rm -f "$NEW" "$PY"' EXIT
printf '%%s' "$NEWB64" | base64 -d > "$NEW"; printf '%%s' "$CHECKB64" | base64 -d > "$PY"
head -n 1 "$CONF" | grep -qF 'config.RelayNginxConf' || { echo "RELAY_STREAM_FAIL $CONF написан не рецептом входа — не трогаю"; exit 1; }
if head -n 1 "$CONF" | grep -qF 'through the tunnel'; then echo "RELAY_STREAM_FAIL вход уже ходит на Hub через туннель — его nginx сводит --relay-tunnel, не трогаю"; exit 1; fi
head -n 1 "$CONF" | grep -qF "to the Hub $HUB." || { echo "RELAY_STREAM_FAIL $CONF ведёт не на Hub $HUB — не трогаю"; exit 1; }
check() { python3 "$PY" "$SNI" "$HOST" ` + "%d %d" + `; }
pct() { awk -v s="$1" 'BEGIN { split(s, a, /[=\/ ]/); print (a[4] > 0 ? int(100 * a[3] / a[4]) : 0) }'; } # "CHECK tls=21/40 …" → 52
before=$(check); echo "before: $before"
[ "$APPLY" = 1 ] || { echo "RELAY_STREAM_OK (только замер)"; exit 0; }
if cmp -s "$NEW" "$CONF"; then
  echo "nginx.conf уже новый"
else
  cp -p "$CONF" "$PREV"
  install -m 0644 "$NEW" "$CONF"
  if ! nginx -t -q 2>/tmp/heyearth-nginx-t; then cat /tmp/heyearth-nginx-t; cp -p "$PREV" "$CONF"; echo "RELAY_STREAM_FAIL nginx -t не прошёл — вернул прежний конфиг"; exit 1; fi
  systemctl reload nginx; sleep 2
  echo "nginx.conf заменён, nginx перечитал конфиг (прежний — $PREV)"
fi
after=$(check); echo "after: $after"
b=$(pct "$before"); a=$(pct "$after")
if [ "$a" -lt ` + "%d" + ` ] || [ "$((a + 5))" -lt "$b" ]; then # 5 points: one probe of 40 is noise
  if [ -f "$PREV" ] && ! cmp -s "$PREV" "$CONF"; then cp -p "$PREV" "$CONF"; nginx -t -q && systemctl reload nginx; echo "откат: вернул $PREV"; fi
  echo "RELAY_STREAM_FAIL после замены TLS $a%% (до: $b%%)"; exit 1
fi
echo "RELAY_STREAM_OK TLS до $b%% → после $a%%"
`

func relayStreamEntryScript(conf, hub, sni, host string, apply bool) string {
	a := "0"
	if apply {
		a = "1"
	}
	body := fmt.Sprintf(relayStreamEntrySh, relayStreamTLSProbes, relayStreamHTTPProbe, relayStreamMinPct)
	return "NEWB64=" + shQuote(base64.StdEncoding.EncodeToString([]byte(conf))) +
		" CHECKB64=" + shQuote(base64.StdEncoding.EncodeToString([]byte(relayStreamCheckPy))) +
		" HUB=" + shQuote(hub) + " SNI=" + shQuote(sni) + " HOST=" + shQuote(host) + " APPLY=" + a + "\n" + body
}

func relayStream(client *ssh.Client, id int, apply bool) error {
	q := func(sql string) (string, error) {
		out, err := runSSHBash(client, "relay-stream db", `runuser -u postgres -- psql -d heyearth -tAc `+shQuote(sql), true)
		return strings.TrimSpace(out), err
	}
	role, err := q("select coalesce(role,'') from servers where id=" + strconv.Itoa(id))
	if err != nil {
		return err
	}
	if role != "relay" {
		return fmt.Errorf("сервер %d — не вход (role %q)", id, role)
	}
	hub, err := q("select value from app_settings where key='main_server_host'")
	if err != nil {
		return err
	}
	if _, perr := netip.ParseAddr(hub); perr != nil {
		return fmt.Errorf("main_server_host %q — не адрес: рецепт входа разрешает имя сам, этот режим — нет", hub)
	}
	sni, err := q("select coalesce((select value from app_settings where key='reality_sni_domain'),'')")
	if err != nil {
		return err
	}
	if sni == "" {
		sni = biz.DefaultRealitySNI
	}
	conf, err := config.RelayNginxConf(hub)
	if err != nil {
		return err
	}
	entry := relayStreamEntryScript(conf, hub, sni, hub, apply)
	run := nodeSSHFunc + fmt.Sprintf(`for try in $(seq 1 %d); do
  out=$(nodessh %d <<'HEYEARTH_NODE_EOF'
%s
HEYEARTH_NODE_EOF
)
  if grep -q 'RELAY_STREAM_' <<<"$out"; then printf '%%s\n' "$out"; echo "(SSH: попытка $try)"; break; fi
  echo "[relay-stream] SSH до входа %d не удался (попытка $try), повтор"
done
`, relayStreamSSHTries, id, entry, id)
	launch := relayStreamLaunchScript(id, run)
	out, err := runSSHBash(client, fmt.Sprintf("relay-stream %d start", id), launch, true)
	if err != nil || !strings.Contains(out, "RELAY_STREAM_STARTED") {
		return fmt.Errorf("вход %d: запуск на Hub не начался: %v", id, err)
	}
	// Follow the log; the run itself does not depend on this session (relayStreamLaunchScript).
	logPath := relayStreamLogPath(id)
	fails := 0
	for deadline := time.Now().Add(relayStreamWait); time.Now().Before(deadline); {
		time.Sleep(5 * time.Second)
		l, err := runSSHBash(client, "relay-stream log", "cat "+logPath, true)
		if err != nil {
			if fails++; fails >= 6 {
				return fmt.Errorf("вход %d: связь с Hub потеряна, запуск продолжается там; лог: %s", id, logPath)
			}
			continue
		}
		fails = 0
		if !strings.Contains(l, "RELAY_STREAM_DONE") {
			continue
		}
		fmt.Print(l)
		if !strings.Contains(l, "RELAY_STREAM_OK") {
			return fmt.Errorf("вход %d: nginx stream не сведён (см. вывод выше)", id)
		}
		return nil
	}
	return fmt.Errorf("вход %d: за %s запуск не закончился; лог на Hub: %s", id, relayStreamWait, logPath)
}

const relayStreamWait = 20 * time.Minute

func relayStreamLogPath(id int) string {
	return fmt.Sprintf("/var/log/heyearth-relay-stream-%d.log", id)
}

// relayStreamLaunchScript starts run on the Hub detached from this SSH session (setsid, stdin
// closed): the owner's link to the Hub drops sessions, and a dropped session must not kill the
// change half-way (new nginx.conf in place, the check and the rollback not yet run). One run at a
// time: the lock is held by the detached run itself (fd 9 is inherited). The log ends with
// RELAY_STREAM_DONE.
func relayStreamLaunchScript(id int, run string) string {
	return fmt.Sprintf(`set -u
LOG=%[1]s; RUN=/root/.heyearth-relay-stream-%[2]d.sh
exec 9>/run/heyearth-relay-stream.lock
if ! flock -n 9; then echo "RELAY_STREAM_FAIL на Hub уже идёт другой --relay-stream"; exit 1; fi
printf '%%s' %[3]s | base64 -d > "$RUN"
: > "$LOG"
setsid bash -c 'bash "$1" > "$2" 2>&1; echo RELAY_STREAM_DONE >> "$2"; rm -f "$1"' _ "$RUN" "$LOG" </dev/null >/dev/null 2>&1 &
echo RELAY_STREAM_STARTED
`, shQuote(relayStreamLogPath(id)), id, shQuote(base64.StdEncoding.EncodeToString([]byte(run))))
}
