package main

// --path-matrix ID: the per-flow check of the path between the Hub and one reserve entry, run
// before that entry (or a protocol through it) becomes the only way for a group of users — Beads
// heyearthproject-ukzk, incident 2026-11-20: entry 6 passed every single-probe check while 40–60 %
// of its 5-tuples were dead.
//
// Three groups, each over many 5-tuples (fixed, distinct source ports):
//
//	hub → entry   TCP connect to the entry's 443 and 2443 from pathMatrixTuples source ports
//	entry → hub   the same from the entry to the Hub's 8447 (its nginx upstream), 443 and 2443
//	via exit      TLS with the Reality SNI through an exit abroad to the entry's 443 and 2443 —
//	              what a user sees, nginx retries included
//
// Raw tuple groups must reach pathMatrixRawPct, the user-visible ones pathMatrixUserPct; the last
// line is PATH_MATRIX verdict=PASS|FAIL. Read-only: nothing on the Hub or the entry changes.

import (
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/heyearth/backend/internal/biz"
	"golang.org/x/crypto/ssh"
)

const (
	pathMatrixTuples  = 50
	pathMatrixRawPct  = 95 // a raw 5-tuple group below this has a broken link on the way
	pathMatrixUserPct = 99 // what users get through the entry (TLS via an exit)
)

// pathMatrixProbePy: TCP connects from n fixed source ports; prints "<name> <ok>/<n> <pattern>".
const pathMatrixProbePy = `import socket, struct, sys, random
name, host, port, n = sys.argv[1], sys.argv[2], int(sys.argv[3]), int(sys.argv[4])
base = random.randint(20000, 60000 - n)
out = ""
for sp in range(base, base + n):
    s = socket.socket(); s.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
    s.setsockopt(socket.SOL_SOCKET, socket.SO_LINGER, struct.pack("ii", 1, 0)); s.settimeout(2)
    try:
        s.bind(("", sp)); s.connect((host, port)); out += "+"
    except Exception:
        out += "-"
    s.close()
print("%s %d/%d %s" % (name, out.count("+"), n, out))
`

// pathMatrixRun is the Hub-side job: $ENTRY, $HUB, $SNI, $SOCKS (an exit's local SOCKS port), $N.
func pathMatrixRun(id int, entry, hub, sni string, socks int) string {
	py := base64.StdEncoding.EncodeToString([]byte(pathMatrixProbePy))
	entryScript := fmt.Sprintf(`P=$(mktemp); trap 'rm -f "$P"' EXIT; printf '%%s' %[1]s | base64 -d > "$P"
for t in "entry→hub:8447 %[2]s 8447" "entry→hub:443 %[2]s 443" "entry→hub:2443 %[2]s 2443"; do set -- $t; echo "RAW $(python3 "$P" "$1" "$2" "$3" %[3]d)"; done
echo PATH_MATRIX_ENTRY_DONE
`, shQuote(py), hub, pathMatrixTuples)
	return nodeSSHFunc + fmt.Sprintf(`set -u
ENTRY=%[1]s; SNI=%[2]s; SOCKS=%[3]d; N=%[4]d
P=$(mktemp); trap 'rm -f "$P"' EXIT; printf '%%s' %[5]s | base64 -d > "$P"
echo "== hub → entry %[6]d"
for port in 443 2443; do echo "RAW $(python3 "$P" "hub→entry:$port" "$ENTRY" "$port" "$N")"; done
echo "== entry %[6]d → hub"
for try in $(seq 1 %[7]d); do
  out=$(nodessh %[6]d <<'HEYEARTH_NODE_EOF'
%[8]s
HEYEARTH_NODE_EOF
)
  if grep -q PATH_MATRIX_ENTRY_DONE <<<"$out"; then grep '^RAW ' <<<"$out"; break; fi
  echo "(SSH до входа: попытка $try не удалась)"
done
grep -q PATH_MATRIX_ENTRY_DONE <<<"${out:-}" || echo "RAW entry→hub:ssh 0/1 SSH до входа не удался"
echo "== via exit (SOCKS :$SOCKS) → entry %[6]d → hub"
for port in 443 2443; do
  ok=0
  for i in $(seq 1 "$N"); do
    curl -sk --socks5-hostname "127.0.0.1:$SOCKS" --connect-to "$SNI:443:$ENTRY:$port" "https://$SNI/" -o /dev/null --max-time 15 -w '%%{http_code}\n' &
    if [ $((i %% 10)) = 0 ]; then wait; fi
  done > /tmp/heyearth-pm-$port; wait
  ok=$(grep -vc '^000$' /tmp/heyearth-pm-$port); rm -f /tmp/heyearth-pm-$port
  echo "USER via-exit:$port $ok/$N"
done
`, shQuote(entry), shQuote(sni), socks, pathMatrixTuples, shQuote(py), id, relayStreamSSHTries, entryScript)
}

// pathMatrixVerdict reads the RAW/USER lines of a run: every group must reach its floor.
func pathMatrixVerdict(log string) (pass bool, failed []string) {
	seen := 0
	for _, l := range strings.Split(log, "\n") {
		f := strings.Fields(l)
		if len(f) < 3 || (f[0] != "RAW" && f[0] != "USER") {
			continue
		}
		ok, n, found := strings.Cut(f[2], "/")
		a, err1 := strconv.Atoi(ok)
		b, err2 := strconv.Atoi(n)
		if !found || err1 != nil || err2 != nil || b == 0 {
			failed = append(failed, f[1]+" (не разобрано)")
			continue
		}
		seen++
		floor := pathMatrixRawPct
		if f[0] == "USER" {
			floor = pathMatrixUserPct
		}
		if 100*a < floor*b {
			failed = append(failed, fmt.Sprintf("%s %d/%d < %d%%", f[1], a, b, floor))
		}
	}
	return seen >= 7 && len(failed) == 0, failed
}

func pathMatrix(client *ssh.Client, id int) error {
	q := func(sql string) (string, error) {
		out, err := runSSHBash(client, "path-matrix db", `runuser -u postgres -- psql -d heyearth -tAc `+shQuote(sql), true)
		return strings.TrimSpace(out), err
	}
	row, err := q("select coalesce(role,'')||' '||coalesce(ip_address,'') from servers where id=" + strconv.Itoa(id))
	if err != nil {
		return err
	}
	role, entry, _ := strings.Cut(row, " ")
	if role != "relay" || entry == "" {
		return fmt.Errorf("сервер %d — не вход (%q)", id, row)
	}
	hub, err := q("select value from app_settings where key='main_server_host'")
	if err != nil {
		return err
	}
	sni, err := q("select coalesce((select value from app_settings where key='reality_sni_domain'),'')")
	if err != nil {
		return err
	}
	if sni == "" {
		sni = biz.DefaultRealitySNI
	}
	// An exit's local SOCKS on the Hub is 1088+id (hysteria2-client-<id>): the first live exit.
	exit, err := q("select coalesce(min(id),0) from servers where coalesce(role,'exit')='exit' and lifecycle_state='online'")
	if err != nil {
		return err
	}
	ex, _ := strconv.Atoi(exit)
	if ex == 0 {
		return fmt.Errorf("нет живого выхода для проверки «как у пользователя»")
	}
	l, err := hubJob(client, "path-matrix", id, pathMatrixRun(id, entry, hub, sni, 1088+ex), 20*time.Minute)
	if err != nil {
		return fmt.Errorf("вход %d: %w", id, err)
	}
	fmt.Print(l)
	pass, failed := pathMatrixVerdict(l)
	if !pass {
		fmt.Printf("PATH_MATRIX verdict=FAIL %s\n", strings.Join(failed, "; "))
		return fmt.Errorf("вход %d: путь не готов быть обязательным", id)
	}
	fmt.Println("PATH_MATRIX verdict=PASS")
	return nil
}
