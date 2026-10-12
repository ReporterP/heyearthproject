#!/usr/bin/env bash
# Task 7 stand: checks `xray api` adu/rmu/adrules/inboundusercount semantics with the
# production xray binary on a throw-away instance (loopback ports 20085 api, 20443
# VLESS Reality, 20444 VLESS XHTTP, 20801/20802 test SOCKS, 20999 local HTTP).
# Does not touch the production xray. Run on the Hub as root; cleans up on exit.
set -u
XRAY=${XRAY:-/opt/heyearth/xray/xray}
DIR=/root/xray-stand
API=127.0.0.1:20085
SNI=${SNI:-yandex.ru}
URL=${URL:-https://www.gstatic.com/generate_204}
OKCODE=${OKCODE:-204}
SID=a1b2c3d4e5f60708
FAILS=0

cleanup() { systemctl stop xray-stand xray-stand-a xray-stand-b xray-stand-http >/dev/null 2>&1; rm -rf "$DIR"; }
trap cleanup EXIT
cleanup
mkdir -p "$DIR" && cd "$DIR" || exit 1

ok()  { echo "PASS  $1"; }
bad() { echo "FAIL  $1"; FAILS=$((FAILS + 1)); }
expect() { if [ "$2" = "$3" ]; then ok "$1 (= $3)"; else bad "$1: want '$2', got '$3'"; fi; }
api() { local sub=$1; shift; "$XRAY" api "$sub" --server=$API "$@" 2>&1; }
count() { api inboundusercount -tag="$1" | tr -d ' \n' | sed -n 's/.*"count":\([0-9]*\).*/\1/p' | grep . || echo 0; }
ruletags() { api lsrules | tr -d ' \n' | grep -o '"ruleTag":"[^"]*"' | cut -d'"' -f4 | paste -sd, -; }
probe() { curl -s -o /dev/null -m 8 -w '%{http_code}' -x "socks5h://127.0.0.1:$1" "$URL"; }

"$XRAY" version | head -1
KEYS=$("$XRAY" x25519)
PRIV=$(echo "$KEYS" | awk -F': ' '/^PrivateKey/{print $2}')
PUB=$(echo "$KEYS" | awk -F': ' '/^Password|^Public key/{print $2}')
UA=$("$XRAY" uuid)
UB=$("$XRAY" uuid)
CA='{"id":"'$UA'","flow":"xtls-rprx-vision","email":"a@stand","level":0}'
CB='{"id":"'$UB'","flow":"xtls-rprx-vision","email":"b@stand","level":1}'
CBX='{"id":"'$UB'","email":"b@stand","level":1}'

reality() { echo '{"show":false,"dest":"'$SNI':443","xver":0,"serverNames":["'$SNI'"],"privateKey":"'$PRIV'","shortIds":["'$SID'"]}'; }
in_tcp() { echo '{"tag":"vr-test","listen":"127.0.0.1","port":20443,"protocol":"vless","settings":{"clients":'"$1"',"decryption":"none"},"streamSettings":{"network":"tcp","security":"reality","realitySettings":'"$(reality)"'}}'; }
in_xhttp() { echo '{"tag":"vx-test","listen":"127.0.0.1","port":20444,"protocol":"vless","settings":{"clients":'"$1"',"decryption":"none"},"streamSettings":{"network":"xhttp","security":"reality","xhttpSettings":{"path":"/"},"realitySettings":'"$(reality)"'}}'; }
routing() { # $1 = JSON array of users of plan 1 (same rule shapes as production; no fallbackTag:
	# with fallbackTag random/roundRobin require an observatory, which the stand does not run)
	cat <<EOF
{"domainStrategy":"IPIfNonMatch",
 "balancers":[{"tag":"plan-1-balancer","selector":["direct"],"strategy":{"type":"random"}}],
 "rules":[
  {"type":"field","ruleTag":"api","inboundTag":["api-in"],"outboundTag":"api"},
  {"type":"field","ruleTag":"private","ip":["geoip:private"],"outboundTag":"block"},
  {"type":"field","ruleTag":"plan-1-users","user":$1,"balancerTag":"plan-1-balancer"},
  {"type":"field","ruleTag":"catch-all","network":"tcp,udp","outboundTag":"block"}]}
EOF
}
cat >server.json <<EOF
{"log":{"loglevel":"warning"},
 "api":{"tag":"api","services":["HandlerService","RoutingService","StatsService"]},
 "stats":{},
 "policy":{"levels":{"0":{"statsUserUplink":true,"statsUserDownlink":true},"1":{"statsUserUplink":true,"statsUserDownlink":true}}},
 "inbounds":[{"tag":"api-in","listen":"127.0.0.1","port":20085,"protocol":"dokodemo-door","settings":{"address":"127.0.0.1"}},
  $(in_tcp "[$CA]"), $(in_xhttp "[]")],
 "outbounds":[{"tag":"block","protocol":"blackhole"},{"tag":"direct","protocol":"freedom"},{"tag":"api","protocol":"freedom"}],
 "routing":$(routing '["a@stand"]')}
EOF
client() { # $1 = local SOCKS port, $2 = VLESS uuid
	cat >"client-$1.json" <<EOF
{"log":{"loglevel":"warning"},
 "inbounds":[{"listen":"127.0.0.1","port":$1,"protocol":"socks","settings":{"udp":false}}],
 "outbounds":[{"protocol":"vless","settings":{"vnext":[{"address":"127.0.0.1","port":20443,
   "users":[{"id":"$2","encryption":"none","flow":"xtls-rprx-vision"}]}]},
   "streamSettings":{"network":"tcp","security":"reality",
   "realitySettings":{"fingerprint":"chrome","serverName":"$SNI","publicKey":"$PUB","shortId":"$SID"}}}]}
EOF
}
client 20801 "$UA"
client 20802 "$UB"
"$XRAY" run -test -config server.json >/dev/null || { "$XRAY" run -test -config server.json; exit 1; }
systemd-run --quiet --collect --unit=xray-stand "$XRAY" run -config "$DIR/server.json"
systemd-run --quiet --collect --unit=xray-stand-a "$XRAY" run -config "$DIR/client-20801.json"
systemd-run --quiet --collect --unit=xray-stand-b "$XRAY" run -config "$DIR/client-20802.json"
sleep 3

echo "== S1 baseline"
expect "S1 user A (in plan-1-users) reaches the internet" "$OKCODE" "$(probe 20801)"
expect "S1 user B (unknown to inbound) is rejected" 000 "$(probe 20802)"
expect "S1 inboundusercount vr-test" 1 "$(count vr-test)"
expect "S1 inboundusercount vx-test (prints {})" 0 "$(count vx-test)"
expect "S1 lsrules order" "api,private,plan-1-users,catch-all" "$(ruletags)"

echo "== S2 adu: VLESS user with flow + level"
echo '{"inbounds":['"$(in_tcp "[$CB]")"']}' >adu-b.json
expect "S2 adu output" "Added 1 user(s) in total." "$(api adu adu-b.json | tail -1)"
expect "S2 inboundusercount vr-test" 2 "$(count vr-test)"
expect "S2 B accepted but not in plan rule -> catch-all block" 000 "$(probe 20802)"
journalctl -u xray-stand --no-pager -n 100 | grep "b@stand" | tail -1 | sed 's/^/      access: /'

echo "== S3 adu of an existing user"
expect "S3 duplicate add counted as 0" "Added 0 user(s) in total." "$(api adu adu-b.json | tail -1)"
expect "S3 inboundusercount unchanged" 2 "$(count vr-test)"

echo "== S4 adrules WITHOUT -append = replace all rules + balancers"
echo '{"routing":'"$(routing '["a@stand","b@stand"]')"'}' >rules-ab.json
T0=$(date +%s%N); api adrules rules-ab.json >/dev/null; RC=$?; T1=$(date +%s%N)
expect "S4 adrules exit code" 0 "$RC"
echo "      adrules took $(((T1 - T0) / 1000000)) ms"
expect "S4 order preserved" "api,private,plan-1-users,catch-all" "$(ruletags)"
expect "S4 user B (added by adu, flow vision) works via plan-1-balancer" "$OKCODE" "$(probe 20802)"
expect "S4 user A still works" "$OKCODE" "$(probe 20801)"
if api statsquery -pattern "user>>>b@stand>>>traffic>>>downlink" | grep -q '"value"'; then ok "S4 traffic of API-added user B is counted (statsquery)"; else bad "S4 no traffic stats for b@stand"; fi

echo "== S5 adrules -append: why per-tag replacement is NOT used"
echo '{"routing":{"rules":[{"type":"field","ruleTag":"plan-1-users","user":["a@stand","b@stand"],"balancerTag":"plan-1-balancer"}]}}' >plan1.json
OUT=$(api adrules -append plan1.json); RC=$?
if [ "$RC" -ne 0 ] && echo "$OUT" | grep -q "duplicate ruleTag"; then ok "S5 -append with existing ruleTag -> 'duplicate ruleTag' error"; else bad "S5 rc=$RC out=$OUT"; fi
api rmrules plan-1-users >/dev/null
api adrules -append plan1.json >/dev/null
expect "S5 rmrules + adrules -append lands AFTER catch-all" "api,private,catch-all,plan-1-users" "$(ruletags)"
expect "S5 ...so user B is blocked by catch-all" 000 "$(probe 20802)"
api adrules rules-ab.json >/dev/null
expect "S5 full replacement restores order" "api,private,plan-1-users,catch-all" "$(ruletags)"
expect "S5 user B works again" "$OKCODE" "$(probe 20802)"

echo "== S6 rmu: new connections rejected; what happens to a live one"
if command -v python3 >/dev/null; then
	head -c 100000000 /dev/zero >big.bin
	systemd-run --quiet --collect --unit=xray-stand-http --working-directory="$DIR" python3 -m http.server 20999 --bind 127.0.0.1
	sleep 1
	curl -s -o /dev/null -m 20 --limit-rate 1M -w '%{size_download}' -x socks5h://127.0.0.1:20802 http://127.0.0.1:20999/big.bin >dl.txt &
	DL=$!
	sleep 3
fi
expect "S6 rmu output" "Removed 1 user(s) in total." "$(api rmu -tag=vr-test -- b@stand | tail -1)"
expect "S6 inboundusercount vr-test" 1 "$(count vr-test)"
expect "S6 new connection of B rejected" 000 "$(probe 20802)"
if [ -n "${DL:-}" ]; then
	wait "$DL"
	BYTES=$(cat dl.txt)
	if [ "${BYTES:-0}" -gt 10000000 ]; then echo "INFO  S6 live download SURVIVED rmu: $BYTES bytes in 20 s"; else echo "INFO  S6 live download was CUT by rmu: $BYTES bytes"; fi
fi

echo "== S7 XHTTP inbound, client without flow"
echo '{"inbounds":['"$(in_xhttp "[$CBX]")"']}' >adu-bx.json
expect "S7 adu output" "Added 1 user(s) in total." "$(api adu adu-bx.json | tail -1)"
expect "S7 inboundusercount vx-test" 1 "$(count vx-test)"
expect "S7 rmu output" "Removed 1 user(s) in total." "$(api rmu -tag=vx-test -- b@stand | tail -1)"
expect "S7 inboundusercount vx-test back to 0" 0 "$(count vx-test)"

echo
if [ "$FAILS" -eq 0 ]; then echo "STAND RESULT: ALL PASS"; else echo "STAND RESULT: $FAILS FAIL(S)"; fi
