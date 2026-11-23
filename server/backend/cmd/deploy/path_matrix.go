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
	"github.com/heyearth/backend/internal/config"
	"github.com/heyearth/backend/internal/provision"
	"golang.org/x/crypto/ssh"
)

// The groups, floors and the probe are shared with «Проверить путь» of the admin panel
// (internal/provision pathcheck.go): the CLI and the API judge a path the same way.
const (
	pathMatrixTuples  = provision.PathTuples
	pathMatrixRawPct  = provision.PathRawPct
	pathMatrixUserPct = provision.PathUserPct
	pathMatrixProbePy = provision.PathProbePy
)

// pathMatrixRun is the Hub-side job: $ENTRY, $HUB, $SNI, $SOCKS (an exit's local SOCKS port), $N.
// An entry on a GRE tunnel (he-r<id> on the Hub, he-hub on the entry; config.RelayTunnelAddrs)
// sends everything to the Hub through it: the raw groups then run between the tunnel addresses
// and count; the public addresses are only nginx's backup upstream, printed as BACKUP and left
// out of the verdict. The job prints EXPECT <groups> so a missing group fails the verdict.
func pathMatrixRun(id int, entry, hub, sni string, socks int, hubT, relayT string) string {
	py := base64.StdEncoding.EncodeToString([]byte(pathMatrixProbePy))
	entryScript := fmt.Sprintf(`P=$(mktemp); trap 'rm -f "$P"' EXIT; printf '%%s' %[1]s | base64 -d > "$P"
PUB=RAW; TUN=0; if ip link show he-hub >/dev/null 2>&1; then PUB=BACKUP; TUN=1; fi
for t in "entry→hub:8447 %[2]s 8447" "entry→hub:443 %[2]s 443" "entry→hub:2443 %[2]s 2443"; do set -- $t; echo "$PUB $(python3 "$P" "$1" "$2" "$3" %[3]d)"; done
if [ "$TUN" = 1 ]; then
  for port in 8447 443 2443; do echo "RAW $(python3 "$P" "tunnel-entry→hub:$port" %[4]s "$port" %[3]d)"; done
fi
echo PATH_MATRIX_ENTRY_DONE
`, shQuote(py), hub, pathMatrixTuples, shQuote(hubT))
	return nodeSSHFunc + fmt.Sprintf(`set -u
ENTRY=%[1]s; SNI=%[2]s; SOCKS=%[3]d; N=%[4]d; RELAYT=%[9]s
P=$(mktemp); trap 'rm -f "$P"' EXIT; printf '%%s' %[5]s | base64 -d > "$P"
if ip link show he-r%[6]d >/dev/null 2>&1; then
  PUB=BACKUP; TUN=1; echo "EXPECT 6"
  echo "== вход %[6]d на GRE-туннеле: публичный путь — запасной upstream nginx (BACKUP, в вердикт не идёт)"
else
  PUB=RAW; TUN=0; echo "EXPECT 7"
fi
echo "== hub → entry %[6]d"
for port in 443 2443; do echo "$PUB $(python3 "$P" "hub→entry:$port" "$ENTRY" "$port" "$N")"; done
if [ "$TUN" = 1 ]; then echo "RAW $(python3 "$P" "tunnel-hub→entry:443" "$RELAYT" 443 "$N")"; fi
echo "== entry %[6]d → hub"
for try in $(seq 1 %[7]d); do
  out=$(nodessh %[6]d <<'HEYEARTH_NODE_EOF'
%[8]s
HEYEARTH_NODE_EOF
)
  if grep -q PATH_MATRIX_ENTRY_DONE <<<"$out"; then grep -E '^(RAW|BACKUP) ' <<<"$out"; break; fi
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
`, shQuote(entry), shQuote(sni), socks, pathMatrixTuples, shQuote(py), id, relayStreamSSHTries, entryScript, shQuote(relayT))
}

// pathMatrixVerdict reads the RAW/USER lines of a run: every group must reach its floor, and the
// run must have as many groups as its EXPECT line says (7 without the line). BACKUP lines (the
// public path of an entry on a tunnel) are shown, never judged.
func pathMatrixVerdict(log string) (pass bool, failed []string) { return provision.PathVerdict(log) }

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
	hubT, relayT, err := config.RelayTunnelAddrs(id)
	if err != nil {
		return err
	}
	l, err := hubJob(client, "path-matrix", id, pathMatrixRun(id, entry, hub, sni, 1088+ex, hubT.String(), relayT.String()), 20*time.Minute)
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
