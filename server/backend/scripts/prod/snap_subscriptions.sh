# Runs ON the Hub (send it with: go run ./cmd/deploy --ssh "$(cat scripts/prod/snap_subscriptions.sh)").
# Prints one line per (user, client): token prefix, client, sha256 prefix of the subscription body,
# and the Profile-Update-Interval header. Take it before and after a deploy and diff the two:
# a deploy that is not meant to change subscriptions must give identical lines.
# The same 3 users are picked every time (ordered by md5 of the token). No secrets are printed.
Q() { sudo -u postgres psql heyearth -tAc "$1"; }
for t in $(Q "select subscribe_token from users where subscribe_token is not null order by md5(subscribe_token::text) limit 3"); do
  for ua in "Happ/3.1 Android" "clash-verge/2.0"; do
    q=""; [ "$ua" = "clash-verge/2.0" ] && q="?format=clash"
    h=$(curl -s -H "User-Agent: $ua" "http://127.0.0.1:8000/api/v1/user/subscribe/$t$q" | sha256sum | cut -c1-16)
    i=$(curl -s -o /dev/null -D - -H "User-Agent: $ua" "http://127.0.0.1:8000/api/v1/user/subscribe/$t$q" | grep -i "profile-update-interval" | tr -d '\r')
    echo "${t:0:8} ${ua%%/*} $h $i"
  done
done
