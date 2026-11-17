# Runs ON the Hub: checks «Бамбук» (Hysteria2 server on the Hub) from OUTSIDE, from an exit node.
# Testing it from the Hub itself always fails (hairpin) — see CLAUDE.md, port hopping.
#   go run ./cmd/deploy --ssh "$(cat scripts/prod/bamboo_from_exit.sh)"
# Takes the hysteria2:// line of one active user's subscription (the same pick as
# snap_subscriptions.sh), starts a temporary hysteria client on exit EXIT_ID (default: the first
# online exit) against the Hub's public address on 50443 and on one port of the hop range, and
# fetches a 204 through it. Prints only codes and timings — no secrets.
set -u
Q() { sudo -u postgres psql heyearth -tAc "$1"; }
HUB=$(Q "select value from app_settings where key='main_server_host'" | head -1)
EXIT=$(Q "select ip_address from servers where role='exit' and lifecycle_state='online' order by id limit 1")
[ -n "${EXIT_ID:-}" ] && EXIT=$(Q "select ip_address from servers where id=${EXIT_ID}")
T=$(Q "select u.subscribe_token from users u where u.subscribe_token is not null and not u.is_blocked and exists (select 1 from user_subscriptions us where us.user_id = u.id and us.valid_until > now() + interval '2 days') order by md5(u.subscribe_token::text) limit 1")
URI=$(curl -s -H "User-Agent: v2rayN/6.0" "http://127.0.0.1:8000/api/v1/user/subscribe/$T" | base64 -d 2>/dev/null | grep -m1 '^hysteria2://')
[ -n "$URI" ] || { echo "bamboo: no hysteria2 line in the subscription"; exit 1; }
AUTH=$(printf '%s' "$URI" | sed -E 's#^hysteria2://([^@]+)@.*#\1#')
Q_=$(printf '%s' "$URI" | sed -E 's#^[^?]*\?##; s/#.*$//')
param() { printf '%s' "$Q_" | tr '&' '\n' | sed -n "s/^$1=//p" | head -1 | sed 's/%2[Ff]/\//g; s/%3[Dd]/=/g; s/%2[Bb]/+/g'; }
SNI=$(param sni); OBFS=$(param obfs-password)
KEY=/opt/heyearth/state/hub-ssh/id_ed25519
for PORT in 50443 41234; do
  CFG=$(printf 'server: %s:%s\nauth: %s\ntls:\n  sni: %s\nsocks5:\n  listen: 127.0.0.1:18089\n' "$HUB" "$PORT" "$AUTH" "$SNI")
  [ -n "$OBFS" ] && CFG=$(printf '%s\nobfs:\n  type: salamander\n  salamander:\n    password: %s\n' "$CFG" "$OBFS")
  R=$(printf '%s\n' "$CFG" | ssh -i "$KEY" -o BatchMode=yes -o ConnectTimeout=10 -o StrictHostKeyChecking=accept-new "root@$EXIT" '
    umask 077; cat > /tmp/he-bamboo.yaml
    (timeout 25 hysteria client -c /tmp/he-bamboo.yaml >/tmp/he-bamboo.log 2>&1 &)
    sleep 4
    curl -s -o /dev/null -w "%{http_code} %{time_total}s" --max-time 15 --socks5-hostname 127.0.0.1:18089 https://www.google.com/generate_204
    pkill -f he-bamboo.yaml; rm -f /tmp/he-bamboo.yaml /tmp/he-bamboo.log' 2>&1)
  echo "bamboo from exit, hub port $PORT: $R"
done
