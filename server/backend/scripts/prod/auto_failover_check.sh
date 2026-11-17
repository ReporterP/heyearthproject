# Runs ON the Hub: does the Happ JSON «⚡ Авто» survive a dead entry? (DDoS runbook, architecture layer)
#   go run ./cmd/deploy --ssh "$(cat scripts/prod/auto_failover_check.sh)"            # entry DEAD_ID=5 dead
#   go run ./cmd/deploy --ssh "DEAD_ID=6 $(cat scripts/prod/auto_failover_check.sh)"
# Takes the JSON subscription of the «Авто» test user (sub_auto_test_user, else the first active
# user), points the dead entry and the Hub's direct ways at unroutable TEST-NET addresses, runs a
# private xray with it (not the service, its own SOCKS on 127.0.0.1:18990) and fetches a 204 ten
# times. Healthy: 10/10 through the remaining entry. Prints codes only — no secrets.
set -u
DEAD_ID=${DEAD_ID:-5}
Q() { sudo -u postgres psql heyearth -tAc "$1"; }
XRAY=/opt/heyearth/xray/xray
HUB=$(Q "select value from app_settings where key='main_server_host'")
DEAD=$(Q "select ip_address from servers where id=$DEAD_ID")
TU=$(Q "select value from app_settings where key='sub_auto_test_user'")
T=$(Q "select subscribe_token from users where (user_id='$TU' or vless_uuid::text='$TU') and subscribe_token is not null limit 1")
[ -n "$T" ] || T=$(Q "select u.subscribe_token from users u where u.subscribe_token is not null and not u.is_blocked and exists (select 1 from user_subscriptions us where us.user_id=u.id and us.valid_until > now()) order by md5(u.subscribe_token::text) limit 1")
W=$(mktemp -d); trap 'pkill -f "$W/auto.json" 2>/dev/null; rm -rf "$W"' EXIT
curl -s -H "User-Agent: Happ/3.1 Android" "http://127.0.0.1:8000/api/v1/user/subscribe/$T?format=json" > "$W/sub.json"
python3 - "$W" "$HUB" "$DEAD" <<'PY'
import json, sys
w, hub, dead = sys.argv[1], sys.argv[2], sys.argv[3]
doc = json.load(open(w + "/sub.json"))
cfgs = doc if isinstance(doc, list) else [doc]
cfg = next((c for c in cfgs if "Авто" in str(c.get("remarks", ""))), cfgs[0])
s = json.dumps(cfg, ensure_ascii=False)
hosts = {hub, "hiearth.ru", "www.hiearth.ru"}
n_dead = s.count('"' + dead + '"')
s = s.replace('"' + dead + '"', '"192.0.2.1"')
n_hub = 0
for h in hosts:
    n_hub += s.count('"' + h + '"')
    s = s.replace('"' + h + '"', '"192.0.2.2"')
cfg = json.loads(s)
cfg["inbounds"] = [{"tag": "test-in", "listen": "127.0.0.1", "port": 18990, "protocol": "socks", "settings": {"udp": False}}]
cfg.pop("dns", None)
cfg["log"] = {"loglevel": "error"}
json.dump(cfg, open(w + "/auto.json", "w"))
print("config:", cfg.get("remarks", "?"), "| dead entry refs:", n_dead, "| hub refs:", n_hub, "| outbounds:", len(cfg.get("outbounds", [])))
PY
"$XRAY" run -c "$W/auto.json" >"$W/xray.log" 2>&1 &
sleep 25   # let the observatory probe every way
ok=0
for i in 1 2 3 4 5 6 7 8 9 10; do
  c=$(curl -s -o /dev/null -w '%{http_code}' --max-time 15 --socks5-hostname 127.0.0.1:18990 https://www.google.com/generate_204)
  [ "$c" = 204 ] && ok=$((ok+1))
done
echo "auto failover with entry $DEAD_ID and the Hub dead: $ok/10 OK"
grep -iE "failed to|error" "$W/xray.log" | grep -v "192.0.2" | tail -2 | cut -c1-160
