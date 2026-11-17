package admin

// «Проверить с телефона» (block 3M / M6): whether mobile networks let a node's address through.
// Mobile operators freeze TCP to most hosting subnets after the first packets, so the only real
// test is a phone on mobile data. POST starts a temporary responder on the node (plain HTTP,
// 30 minutes, then it stops itself and closes its firewall rule); the owner opens its link on the
// phone with Wi-Fi off; GET reads what really reached the phone: the responder logs, per
// download, the bytes the phone acknowledged (TCP_INFO bytes_acked — a write into the kernel
// buffer proves nothing) and the client's own report; the client IP tells the operator.

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/heyearth/backend/internal/config"
	"github.com/heyearth/backend/internal/db"
	"github.com/heyearth/backend/internal/provision"
	qrcode "github.com/skip2/go-qrcode"
)

const (
	mobileCheckDir      = "/opt/heyearth/mobile-check"
	mobileCheckUnit     = config.MobileCheckUnit
	mobileCheckLog      = "/var/log/" + mobileCheckUnit + "/check.log" // LogsDirectory of the unit
	mobileCheckBytes    = 2 << 20                                      // 2 MiB: far past the 5–16 KB where a frozen connection stops
	mobileCheckDuration = 30 * time.Minute
)

// mobileCheckResponder is the node side (python3 is on every supported Ubuntu/Debian). Paths are
// /<token>/ (page), /<token>/blob (2 MiB), /<token>/result (the page's own report). Each line of
// the log is one JSON event.
const mobileCheckResponder = `import http.server, json, os, socket, socketserver, struct, sys, time
PORT, TOKEN, LOG, SIZE = int(sys.argv[1]), sys.argv[2], sys.argv[3], int(sys.argv[4])
CHUNK = os.urandom(65536)
PAGE = """<!doctype html><html lang="ru"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>Проверка входа</title>
<style>body{font:17px/1.5 system-ui,sans-serif;margin:24px;background:#111;color:#eee}b{font-size:22px}</style></head><body>
<p><b id="s">Проверяю…</b></p><p id="d">Не закрывайте страницу 20–30 секунд. Wi-Fi должен быть выключен.</p>
<script>
var t0=Date.now(),got=0,done=false;
function fin(ok){if(done)return;done=true;var ms=Date.now()-t0;
document.getElementById('s').textContent=ok?'Готово: адрес проходит':'Не прошло: оборвалось на '+Math.round(got/1024)+' КБ';
document.getElementById('d').textContent='Скачано '+(got/1048576).toFixed(1)+' МБ за '+(ms/1000).toFixed(1)+' с. Результат уже виден в админке.';
var x=new XMLHttpRequest();x.open('POST','result');x.setRequestHeader('Content-Type','application/json');x.send(JSON.stringify({bytes:got,ms:ms,ok:ok}));}
var x=new XMLHttpRequest();x.open('GET','blob?'+Date.now());x.responseType='arraybuffer';
x.onprogress=function(e){got=e.loaded;};x.onload=function(){got=x.response?x.response.byteLength:got;fin(got>=SIZE);};
x.onerror=function(){fin(false);};x.timeout=30000;x.ontimeout=function(){fin(false);};x.send();
</script></body></html>""".replace("SIZE", str(SIZE)).encode()

open(LOG, "w").close()  # a new check starts with an empty log

def record(**kw):
    kw["at"] = round(time.time(), 1)
    with open(LOG, "a") as f:
        f.write(json.dumps(kw) + "\n")

def acked(sock):
    try:
        info = sock.getsockopt(socket.IPPROTO_TCP, socket.TCP_INFO, 232)
        return struct.unpack_from("Q", info, 120)[0]
    except OSError:
        return -1

class H(http.server.BaseHTTPRequestHandler):
    timeout = 40
    def log_message(self, *a):
        pass
    def path_is(self, tail):
        p = "/" + TOKEN + tail
        return self.path.split("?")[0] in (p, p + "/")
    def do_GET(self):
        ip = self.client_address[0]
        if self.path_is(""):
            self.send_response(200)
            self.send_header("Content-Type", "text/html; charset=utf-8")
            self.send_header("Content-Length", str(len(PAGE)))
            self.send_header("Cache-Control", "no-store")
            self.end_headers()
            self.wfile.write(PAGE)
            record(kind="page", ip=ip)
            return
        if self.path_is("/blob"):
            self.send_response(200)
            self.send_header("Content-Type", "application/octet-stream")
            self.send_header("Content-Length", str(SIZE))
            self.send_header("Cache-Control", "no-store")
            self.end_headers()
            base, t0, sent = acked(self.connection), time.time(), 0
            try:
                while sent < SIZE:
                    n = min(len(CHUNK), SIZE - sent)
                    self.wfile.write(CHUNK[:n])
                    sent += n
                self.wfile.flush()
            except OSError:
                pass
            last, still = -1, time.time()
            while True:  # what the phone really acknowledged, not what the kernel took
                a = acked(self.connection)
                if a < 0 or a - base >= SIZE:
                    break
                if a != last:
                    last, still = a, time.time()
                elif time.time() - still > 15:
                    break
                time.sleep(0.25)
            a = acked(self.connection)
            got = max(0, min(SIZE, a - base)) if a >= 0 else -1
            record(kind="blob", ip=ip, sent=sent, acked=got, secs=round(time.time() - t0, 1))
            return
        self.send_response(404)
        self.end_headers()
    def do_POST(self):
        if self.path_is("/result"):
            n = min(int(self.headers.get("Content-Length") or 0), 4096)
            try:
                d = json.loads(self.rfile.read(n) or b"{}")
            except ValueError:
                d = {}
            record(kind="report", ip=self.client_address[0], bytes=int(d.get("bytes") or 0), ms=int(d.get("ms") or 0))
            self.send_response(204)
            self.end_headers()
            return
        self.send_response(404)
        self.end_headers()

class S(socketserver.ThreadingMixIn, http.server.HTTPServer):
    daemon_threads = True
    allow_reuse_address = True

S(("0.0.0.0", PORT), H).serve_forever()
`

// mobileCheckStartScript starts the responder for secs seconds on the first free port of
// 80/8080/8880 and prints "PORT=<n>": a throwaway user (DynamicUser) with only the right to bind
// the port, a firewall rule only while ufw is active (removed when the unit stops). A previous
// run is stopped first.
func mobileCheckStartScript(token string, secs int) string {
	return fmt.Sprintf(`set -e
command -v python3 >/dev/null || { echo "на сервере нет python3"; exit 3; }
systemctl stop %[1]s 2>/dev/null || true
PORT=""
for p in 80 8080 8880; do
  if ! ss -Hltn "sport = :$p" | grep -q .; then PORT=$p; break; fi
done
[ -n "$PORT" ] || { echo "порты 80, 8080 и 8880 заняты"; exit 3; }
STOP=()
if command -v ufw >/dev/null && ufw status | grep -q "Status: active"; then
  ufw allow "$PORT/tcp" comment %[1]s >/dev/null
  STOP=(--property="ExecStopPost=+/usr/sbin/ufw delete allow $PORT/tcp")
fi
systemd-run --unit=%[1]s --collect --quiet "${STOP[@]}" \
  -p DynamicUser=yes -p AmbientCapabilities=CAP_NET_BIND_SERVICE -p CapabilityBoundingSet=CAP_NET_BIND_SERVICE \
  -p NoNewPrivileges=yes -p ProtectSystem=strict -p ProtectHome=yes -p PrivateTmp=yes -p LogsDirectory=%[1]s \
  /usr/bin/timeout %[4]d /usr/bin/python3 %[2]s/check.py "$PORT" %[5]s %[3]s %[6]d
sleep 1
systemctl is-active --quiet %[1]s || { echo "тестовая страница не запустилась"; journalctl -u %[1]s -n 5 --no-pager; exit 3; }
echo "PORT=$PORT"
`, mobileCheckUnit, mobileCheckDir, mobileCheckLog, secs, token, mobileCheckBytes)
}

// mobileCheckReadScript prints the unit state and the log (bounded).
const mobileCheckReadScript = `systemctl is-active ` + mobileCheckUnit + ` 2>/dev/null || true
echo '---'
tail -n 400 ` + mobileCheckLog + ` 2>/dev/null || true`

type mobileCheckEvent struct {
	Kind  string  `json:"kind"`
	IP    string  `json:"ip"`
	Sent  int64   `json:"sent"`
	Acked int64   `json:"acked"`
	Secs  float64 `json:"secs"`
	Bytes int64   `json:"bytes"`
	MS    int64   `json:"ms"`
	At    float64 `json:"at"`
}

// MobileCheckResult is one phone (client address): its best download and the page's report.
type MobileCheckResult struct {
	IP        string    `json:"ip"`
	Operator  string    `json:"operator"`
	Delivered int64     `json:"delivered_bytes"` // acknowledged by the phone, best of its downloads
	Total     int64     `json:"total_bytes"`
	Seconds   float64   `json:"seconds"`
	OK        bool      `json:"ok"`
	Verdict   string    `json:"verdict"` // human text for the owner
	At        time.Time `json:"at"`
}

// parseMobileCheckLog folds the responder's events into one result per client address, newest
// activity first. A page view without any download is a result too: the page itself got stuck.
func parseMobileCheckLog(text string) []MobileCheckResult {
	byIP := map[string]*MobileCheckResult{}
	var order []string
	sc := bufio.NewScanner(strings.NewReader(text))
	for sc.Scan() {
		var e mobileCheckEvent
		if json.Unmarshal([]byte(strings.TrimSpace(sc.Text())), &e) != nil || e.IP == "" {
			continue
		}
		r := byIP[e.IP]
		if r == nil {
			r = &MobileCheckResult{IP: e.IP, Total: mobileCheckBytes, Delivered: -1}
			byIP[e.IP] = r
			order = append(order, e.IP)
		}
		if at := time.Unix(int64(e.At), 0).UTC(); at.After(r.At) {
			r.At = at
		}
		switch e.Kind {
		case "blob":
			if e.Acked > r.Delivered || (e.Acked == r.Delivered && e.Secs < r.Seconds) {
				r.Delivered, r.Seconds = e.Acked, e.Secs
			}
		case "report":
			if r.Delivered < 0 && e.Bytes > 0 { // acked unreadable: the phone's own count
				r.Delivered, r.Seconds = min(e.Bytes, mobileCheckBytes), float64(e.MS)/1000
			}
		}
	}
	out := make([]MobileCheckResult, 0, len(order))
	for _, ip := range order {
		r := *byIP[ip]
		r.OK = r.Delivered >= mobileCheckBytes
		switch {
		case r.OK:
			r.Verdict = fmt.Sprintf("✓ скачано %.1f МБ за %.1f с — адрес проходит", float64(r.Delivered)/(1<<20), r.Seconds)
		case r.Delivered < 0:
			r.Delivered = 0
			r.Verdict = "✗ страница открылась, но файл не начал скачиваться — адрес не проходит"
		default:
			r.Verdict = fmt.Sprintf("✗ оборвалось на %d КБ — адрес не проходит", r.Delivered/1024)
		}
		out = append(out, r)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].At.After(out[j].At) })
	return out
}

// mobileOperators: the ASNs of the big four (mobile and their mixed networks).
var mobileOperators = map[int]string{
	8359: "МТС", 30922: "МТС", 29497: "МТС", 28884: "МТС", 8580: "МТС",
	31133: "МегаФон", 25159: "МегаФон", 31163: "МегаФон", 31224: "МегаФон", 31195: "МегаФон", 31208: "МегаФон", 31213: "МегаФон", 31205: "МегаФон", 31261: "МегаФон",
	12958: "T2", 15378: "T2", 41330: "T2", 42437: "T2", 48190: "T2", 39374: "T2",
	3216: "Билайн", 16345: "Билайн", 8402: "Билайн",
}

// asnLookup maps IPs to "AS<n> <name>" (Team Cymru whois, TCP 43). Swapped in tests.
var asnLookup = func(ctx context.Context, ips []string) (map[string]int, map[int]string, error) {
	d := net.Dialer{Timeout: 5 * time.Second}
	conn, err := d.DialContext(ctx, "tcp", "whois.cymru.com:43")
	if err != nil {
		return nil, nil, err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
	if _, err := fmt.Fprintf(conn, "begin\nverbose\n%s\nend\n", strings.Join(ips, "\n")); err != nil {
		return nil, nil, err
	}
	asn, names := map[string]int{}, map[int]string{}
	sc := bufio.NewScanner(conn)
	for sc.Scan() {
		f := strings.Split(sc.Text(), "|")
		if len(f) < 7 {
			continue
		}
		n, err := strconv.Atoi(strings.TrimSpace(f[0]))
		if err != nil {
			continue
		}
		asn[strings.TrimSpace(f[1])] = n
		names[n] = strings.TrimSpace(f[6])
	}
	return asn, names, nil
}

// operatorOf: the operator's name for the big four, "AS<n> <whois name>" otherwise, "" unknown.
func operatorOf(asn int, name string) string {
	if op, ok := mobileOperators[asn]; ok {
		return op
	}
	if asn == 0 {
		return ""
	}
	if i := strings.Index(name, ","); i > 0 {
		name = name[:i]
	}
	return strings.TrimSpace(fmt.Sprintf("AS%d %s", asn, name))
}

func newMobileCheckToken() (string, error) {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// mobileCheckState: the running check of each node (in memory: a restart only loses the link,
// the responder still stops by itself).
type mobileCheckState struct {
	URL     string    `json:"url"`
	Expires time.Time `json:"expires_at"`
}

// mobileCheckQR: the link as a PNG data URL for the phone's camera ("" when it cannot be drawn).
func mobileCheckQR(url string) string {
	png, err := qrcode.Encode(url, qrcode.Medium, 240)
	if err != nil {
		return ""
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(png)
}

var (
	mobileChecksMu sync.Mutex
	mobileChecks   = map[int]mobileCheckState{}
)

const mobileCheckWifiHint = "Откройте ссылку на телефоне с выключенным Wi-Fi (только мобильный интернет) и подождите 20–30 секунд. Лучше проверить с SIM разных операторов."

func mobileCheckNode(w http.ResponseWriter, r *http.Request) (provision.Node, provision.Remote, bool) {
	id, ok := parseLastPathSegmentBeforeFinal(r.URL.Path, "mobile-check")
	if !ok {
		writeAdminError(w, http.StatusBadRequest, "Неверный id сервера")
		return provision.Node{}, nil, false
	}
	if !provisionReady(w) {
		return provision.Node{}, nil, false
	}
	n, err := NodeJobs.LoadNode(r.Context(), id)
	if err != nil {
		writeAdminError(w, http.StatusNotFound, "Нода не найдена")
		return provision.Node{}, nil, false
	}
	if !n.HostKeyConfirmed {
		writeAdminError(w, http.StatusConflict, hostKeyFirstMsg)
		return provision.Node{}, nil, false
	}
	rem, err := NodeDial(n)
	if err != nil {
		writeNodeAccessError(w, err)
		return provision.Node{}, nil, false
	}
	return n, rem, true
}

// StartMobileCheckHandler (POST …/{id}/mobile-check) starts the responder and returns its link.
func StartMobileCheckHandler(w http.ResponseWriter, r *http.Request) {
	n, rem, ok := mobileCheckNode(w, r)
	if !ok {
		return
	}
	token, err := newMobileCheckToken()
	if err != nil {
		writeAdminError(w, http.StatusInternalServerError, "Не удалось начать проверку")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	if _, err := rem.PutFile(ctx, mobileCheckDir+"/check.py", []byte(mobileCheckResponder), 0o644); err != nil {
		log.Printf("[mobile-check] node %d: %v", n.ID, err)
		writeAdminError(w, http.StatusBadGateway, "Не удалось подготовить проверку на сервере — проверьте SSH-доступ")
		return
	}
	out, err := rem.Run(ctx, mobileCheckStartScript(token, int(mobileCheckDuration.Seconds())))
	port := ""
	for _, line := range strings.Split(out, "\n") {
		if p, ok := strings.CutPrefix(strings.TrimSpace(line), "PORT="); ok {
			port = p
		}
	}
	if err != nil || port == "" {
		log.Printf("[mobile-check] node %d: %v: %s", n.ID, err, out)
		msg := "Тестовая страница не запустилась"
		if t := strings.TrimSpace(out); t != "" && len(t) < 200 {
			msg += ": " + t
		}
		writeAdminError(w, http.StatusBadGateway, msg)
		return
	}
	host := n.IP
	if port != "80" {
		host = net.JoinHostPort(n.IP, port)
	}
	st := mobileCheckState{URL: "http://" + host + "/" + token + "/", Expires: time.Now().Add(mobileCheckDuration)}
	mobileChecksMu.Lock()
	mobileChecks[n.ID] = st
	mobileChecksMu.Unlock()
	WriteAuditLog(r, "node_mobile_check", "server", strconv.Itoa(n.ID), map[string]string{"port": port})
	writeAdminJSON(w, http.StatusOK, map[string]any{"url": st.URL, "qr": mobileCheckQR(st.URL), "expires_at": st.Expires, "hint": mobileCheckWifiHint})
}

// GetMobileCheckHandler (GET …/{id}/mobile-check): whether the page still runs and what reached
// each phone so far.
func GetMobileCheckHandler(w http.ResponseWriter, r *http.Request) {
	n, rem, ok := mobileCheckNode(w, r)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	out, err := rem.Run(ctx, mobileCheckReadScript)
	if err != nil {
		log.Printf("[mobile-check] node %d read: %v", n.ID, err)
		writeAdminError(w, http.StatusBadGateway, "Не удалось прочитать результат с сервера")
		return
	}
	state, logText, _ := strings.Cut(out, "---")
	results := parseMobileCheckLog(logText)
	if len(results) > 0 {
		ips := make([]string, len(results))
		for i, res := range results {
			ips[i] = res.IP
		}
		if asn, names, err := asnLookup(ctx, ips); err == nil {
			for i := range results {
				results[i].Operator = operatorOf(asn[results[i].IP], names[asn[results[i].IP]])
			}
		} else {
			log.Printf("[mobile-check] operator lookup: %v", err)
		}
	}
	for _, res := range results {
		if res.OK {
			rememberMobileOK(ctx, n.ID, res.At)
			break
		}
	}
	mobileChecksMu.Lock()
	st := mobileChecks[n.ID]
	mobileChecksMu.Unlock()
	active := strings.TrimSpace(state) == "active"
	resp := map[string]any{"active": active, "results": results, "hint": mobileCheckWifiHint}
	if active && st.URL != "" {
		resp["url"], resp["qr"], resp["expires_at"] = st.URL, mobileCheckQR(st.URL), st.Expires
	}
	writeAdminJSON(w, http.StatusOK, resp)
}

// MobileOKKey: app_settings key holding when a phone last got through entry id (RFC 3339).
func MobileOKKey(id int) string { return "entry_mobile_ok_" + strconv.Itoa(id) }

// rememberMobileOK keeps the latest good phone check of an entry (the «Запуск» checklist asks
// «проверен с телефона» without the owner re-running it).
var rememberMobileOK = func(ctx context.Context, id int, at time.Time) {
	if at.IsZero() {
		at = time.Now()
	}
	if _, err := db.DB.ExecContext(ctx, `
		INSERT INTO app_settings (key, value, updated_at) VALUES ($1, $2, NOW())
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = NOW()
		WHERE app_settings.value < EXCLUDED.value`, MobileOKKey(id), at.UTC().Format(time.RFC3339)); err != nil {
		log.Printf("[mobile-check] remember node %d: %v", id, err)
	}
}
