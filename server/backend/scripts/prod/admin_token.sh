# Runs ON the Hub, prepended to a check script: logs in to the admin API with the bootstrap admin
# from api.env (read on the server, never printed) and leaves the JWT in $TOK and the base in $API.
# Example: go run ./cmd/deploy --ssh "$(cat scripts/prod/admin_token.sh); curl -s -H \"Authorization: Bearer \$TOK\" \$API/servers | head -c 300"
ENV=/opt/heyearth/mainserver/api.env; API=http://127.0.0.1:8000/api/admin
TOK=$(API="$API" U="$(grep '^ADMIN_USER=' "$ENV" | cut -d= -f2-)" P="$(grep '^ADMIN_PASS=' "$ENV" | cut -d= -f2-)" python3 -c '
import json, os, urllib.request
body = json.dumps({"username": os.environ["U"], "password": os.environ["P"]}).encode()
req = urllib.request.Request(os.environ["API"] + "/login", data=body, headers={"Content-Type": "application/json"})
print(json.load(urllib.request.urlopen(req))["token"])')
