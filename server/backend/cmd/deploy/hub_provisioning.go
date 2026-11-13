package main

// --ensure-hub-provisioning puts on a LIVE Hub what node provisioning (admin panel and
// --setup-exit-node) needs, idempotently, and restarts nothing (Bamboo, xray and the API keep
// running; the API is restarted by the operator only when SSH_ENCRYPTION_KEY was just added):
//
//  1. SSH_ENCRYPTION_KEY in api.env — kept when present; otherwise taken from server/.env or, only
//     while the database holds no encrypted SSH password, generated once (32 random bytes,
//     base64, the format internal/crypto reads). api.env keeps every other line, its mode and
//     owner; the previous file is backed up next to it; the write is refused if api.env changed
//     since it was read. The key goes over SSH stdin only, never into an argv or the output.
//  2. The Hub's own SSH key (provision.EnsureHubKey: created once, on the Hub).
//  3. artifacts/node-agent and /opt/heyearth/scripts/install-mtproxy.sh (written only when different).
//  4. The node-agent CA made on the Hub (hub_ca.go) — the previous CA kept as legacy so the exits
//     not converged yet stay reachable. Replacing a CA waits for an API that serves the legacy
//     pair (otherwise the running API would lose those exits): the first run then says so, and
//     the run after the API deploy replaces it.

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/heyearth/backend/internal/provision"
	"golang.org/x/crypto/ssh"
)

const (
	hubNodeAgentArtifact = appDir + "/artifacts/node-agent"
	hubMTProxyScript     = "/opt/heyearth/scripts/install-mtproxy.sh"
	hubAPIBinary         = appDir + "/heyearth-api"
)

// validSSHKey: base64 (standard) of exactly 32 bytes — what internal/crypto accepts.
func validSSHKey(v string) bool {
	b, err := base64.StdEncoding.DecodeString(strings.TrimSpace(v))
	return err == nil && len(b) == 32
}

// errKeyUndecidable: ciphertexts exist but no usable key anywhere — a new key would orphan them.
var errKeyUndecidable = errors.New("в БД есть зашифрованные пароли нод, а годного SSH_ENCRYPTION_KEY нет ни в api.env на Hub, ни в server/.env — " +
	"новый ключ сделал бы их нечитаемыми; верните прежний ключ из бэкапа api.env (в api.env на Hub или в server/.env) и повторите")

// decideSSHEncryptionKey: the Hub's api.env value, else server/.env's, else — only while the
// database holds no ciphertext — a new key (generated=true). Never a new key over existing
// ciphertexts: they would become unreadable. Errors never quote a key.
func decideSSHEncryptionKey(hub, local string, dbHasCiphertext bool) (key string, generated bool, err error) {
	hub, local = strings.TrimSpace(hub), strings.TrimSpace(local)
	switch {
	case validSSHKey(hub):
		if validSSHKey(local) && local != hub {
			fmt.Println("[hub] WARN: SSH_ENCRYPTION_KEY в server/.env отличается от api.env на Hub — используется ключ Hub")
		}
		return hub, false, nil
	case validSSHKey(local):
		return local, false, nil
	case dbHasCiphertext:
		return "", false, errKeyUndecidable
	}
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", false, err
	}
	return base64.StdEncoding.EncodeToString(b), true, nil
}

// sshCiphertextCountScript counts stored SSH ciphertexts (psql as postgres, peer auth): 0 while
// the servers table does not exist yet (a fresh Hub before the API's first migrations).
const sshCiphertextCountScript = `set -euo pipefail
t=$(runuser -u postgres -- psql -d heyearth -tAc "SELECT to_regclass('public.servers') IS NOT NULL")
if [ "$t" != t ]; then echo 0; exit 0; fi
runuser -u postgres -- psql -d heyearth -tAc "SELECT count(*) FROM servers WHERE COALESCE(octet_length(ssh_password_enc), 0) > 0"
`

// parseCount reads the single non-negative number the count script prints.
func parseCount(out string) (int, error) {
	n, err := strconv.Atoi(strings.TrimSpace(out))
	if err != nil || n < 0 {
		return 0, fmt.Errorf("неожиданный ответ psql: %q", strings.TrimSpace(out))
	}
	return n, nil
}

// readHubAPIEnv returns api.env ("" and exists=false when the Hub has none yet).
func readHubAPIEnv(client *ssh.Client) (content string, exists bool, err error) {
	out, err := runSSHQuiet(client, "if [ -f "+hubAPIEnv+" ]; then echo EXISTS; cat "+hubAPIEnv+"; fi", "")
	if err != nil {
		return "", false, err
	}
	rest, ok := strings.CutPrefix(out, "EXISTS\n")
	return rest, ok, nil
}

// resolveSSHEncryptionKey applies decideSSHEncryptionKey against the live Hub.
func resolveSSHEncryptionKey(client *ssh.Client, cfg *deployConfig, hubEnv string) (string, bool, error) {
	return resolveSSHKeyWith(hubEnv, cfg.env.get("SSH_ENCRYPTION_KEY", ""), func() (int, error) {
		out, err := runSSHQuiet(client, "bash -c "+shQuote(sshCiphertextCountScript), "")
		if err != nil {
			return 0, fmt.Errorf("не удалось проверить зашифрованные пароли в БД: %w", err)
		}
		return parseCount(out)
	})
}

// resolveSSHKeyWith: a valid key on the Hub or in server/.env decides without the database (a
// psql hiccup never stops a deploy then); only without one are the ciphertexts counted.
func resolveSSHKeyWith(hubEnv, local string, count func() (int, error)) (string, bool, error) {
	hubKey := envFileValue(hubEnv, "SSH_ENCRYPTION_KEY")
	if validSSHKey(hubKey) || validSSHKey(local) {
		return decideSSHEncryptionKey(hubKey, local, true) // a valid key is returned before the ciphertext branch
	}
	n, err := count()
	if err != nil {
		return "", false, err
	}
	return decideSSHEncryptionKey(hubKey, local, n > 0)
}

// apiEnvSSHKey is the SSH_ENCRYPTION_KEY doAPI writes into api.env (rewritten from the
// whitelist): the Hub's key, server/.env's, or a new one on a Hub without ciphertexts. While
// ciphertexts exist without a usable key the current value is carried over as it is (nothing to
// lose, nothing to guess). Any other unknown — the ciphertext count unreadable — is an error:
// doAPI stops instead of writing api.env without the key (the API already emptied the plaintext
// SSH passwords: they would be lost for good).
func apiEnvSSHKey(hubEnv string, resolve func(hubEnv string) (string, bool, error)) (key string, generated bool, err error) {
	key, generated, err = resolve(hubEnv)
	switch {
	case errors.Is(err, errKeyUndecidable):
		fmt.Printf("[hub] WARN: %v — значение SSH_ENCRYPTION_KEY в api.env переношу как есть\n", err)
		return envFileValue(hubEnv, "SSH_ENCRYPTION_KEY"), false, nil
	case err != nil:
		return "", false, err
	}
	return key, generated, nil
}

// writeAPIEnvScript installs api.env from stdin on the Hub: only the exact content whose sha256
// is sum (a dropped session never leaves a truncated api.env), a timestamped copy of the previous
// file first, 0600, temp + mv. The content (secrets) travels on stdin only.
func writeAPIEnvScript(envPath, backup, sum string) string {
	return `set -euo pipefail
umask 077
f=` + shQuote(envPath) + `
b=` + shQuote(backup) + `
cat > "$f.tmp"
if ! echo "` + sum + `  $f.tmp" | sha256sum -c --status -; then
  rm -f "$f.tmp"
  echo "[hub] ERROR: api.env пришёл не целиком — прежний файл не тронут" >&2
  exit 1
fi
if [ -f "$f" ]; then cp -p "$f" "$b"; chmod 600 "$b"; fi
chmod 600 "$f.tmp"
mv -f "$f.tmp" "$f"
` + pruneEnvCopies + `echo "[hub] $f записан (прежний: $b)"
`
}

// writeHubAPIEnv writes api.env with writeAPIEnvScript; fatal on any error (before the API restart).
func writeHubAPIEnv(client *ssh.Client, content string) {
	sum := sha256.Sum256([]byte(content))
	backup := hubAPIEnv + ".bak-" + time.Now().Format("20060102-150405")
	out, err := runSSHQuiet(client, "bash -c "+shQuote(writeAPIEnvScript(hubAPIEnv, backup, hex.EncodeToString(sum[:]))), content)
	if err != nil {
		log.Fatalf("[hub] api.env не записан: %v — API не перезапускаю", err)
	}
	fmt.Print(out)
}

// keepEnvCopies: deploy's copies of api.env kept per kind (api.env.bak-<ts>, api.env.key-<ts>).
const keepEnvCopies = 5

// pruneEnvCopies (bash, $f = api.env) removes all but the newest keepEnvCopies api.env.bak-* and
// api.env.key-* (the timestamp sorts by time): they hold secrets, the DB password among them.
var pruneEnvCopies = `prune_env_copies() {
  local -a all old
  all=( "$1"-* )
  [ -e "${all[0]}" ] || return 0
  mapfile -t old < <(printf '%s\n' "${all[@]}" | sort -r | tail -n +` + strconv.Itoa(keepEnvCopies+1) + `)
  [ "${#old[@]}" -eq 0 ] || rm -f -- "${old[@]}"
}
prune_env_copies "$f.bak"
prune_env_copies "$f.key"
`

// keyCopyScript copies api.env (now with the key) to copyPath, 0600: the copy made before the
// key was added has none.
func keyCopyScript(envPath, copyPath string) string {
	return "set -euo pipefail\numask 077\nf=" + shQuote(envPath) + "\ncp -p \"$f\" " + shQuote(copyPath) + "\nchmod 600 " + shQuote(copyPath) + "\n" + pruneEnvCopies
}

// keepKeyCopy runs keyCopyScript and tells the owner where the key is, to save it offline.
func keepKeyCopy(client *ssh.Client) {
	cp := hubAPIEnv + ".key-" + time.Now().Format("20060102-150405")
	if _, err := runSSHQuiet(client, "bash -c "+shQuote(keyCopyScript(hubAPIEnv, cp)), ""); err != nil {
		fmt.Printf("[hub] WARN: копия api.env с ключом не сделана: %v\n", err)
		cp = ""
	}
	fmt.Println("[hub] SSH_ENCRYPTION_KEY — строка SSH_ENCRYPTION_KEY= в " + hubAPIEnv + keyCopyNote(cp) +
		"; ежедневный бэкап Hub (heyearth-backup, --setup-ops) берёт api.env со следующего запуска. " +
		"Сохраните ключ офлайн (менеджер паролей) — без него зашифрованные пароли нод не восстановить:\n" +
		"    go run ./cmd/deploy --ssh \"grep '^SSH_ENCRYPTION_KEY=' " + hubAPIEnv + "\"")
}

func keyCopyNote(cp string) string {
	if cp == "" {
		return ""
	}
	return " и в копии " + cp
}

// writeSSHKeyScript replaces api.env's SSH_ENCRYPTION_KEY lines with the line read from stdin:
// only if api.env still has the sha256 it had when the decision was made; a backup first; every
// other line, the mode and the owner kept; temp + mv.
func writeSSHKeyScript(envPath, backup, sum string) string {
	return `set -euo pipefail
umask 077
f=` + shQuote(envPath) + `
b=` + shQuote(backup) + `
[ -f "$f" ] || { echo "[hub] ERROR: $f нет — сначала go run ./cmd/deploy --main" >&2; exit 1; }
echo "` + sum + `  $f" | sha256sum -c --status - || { echo "[hub] ERROR: $f изменился после чтения — ничего не записано, повторите команду" >&2; exit 1; }
cp -p "$f" "$b"
chmod 600 "$b"
{ grep -v '^SSH_ENCRYPTION_KEY=' "$f" || true; cat; } > "$f.tmp"
chmod --reference="$f" "$f.tmp"
chown --reference="$f" "$f.tmp" 2>/dev/null || true
mv -f "$f.tmp" "$f"
` + pruneEnvCopies + `echo "[hub] SSH_ENCRYPTION_KEY записан в $f (прежний файл: $b)"
`
}

// ensureSSHEncryptionKey makes sure api.env carries a valid key; true when it was written now
// (the API must be restarted to read it).
func ensureSSHEncryptionKey(client *ssh.Client, cfg *deployConfig) bool {
	hubEnv, exists, err := readHubAPIEnv(client)
	if err != nil {
		log.Fatalf("[hub] чтение %s: %v", hubAPIEnv, err)
	}
	if !exists {
		log.Fatalf("[hub] %s нет — Hub не установлен: сначала go run ./cmd/deploy --main", hubAPIEnv)
	}
	if validSSHKey(envFileValue(hubEnv, "SSH_ENCRYPTION_KEY")) {
		fmt.Println("[hub] SSH_ENCRYPTION_KEY в api.env на месте — не меняю")
		return false
	}
	key, generated, err := resolveSSHEncryptionKey(client, cfg, hubEnv)
	if err != nil {
		log.Fatalf("[hub] %v", err)
	}
	sum := sha256.Sum256([]byte(hubEnv))
	backup := hubAPIEnv + ".bak-" + time.Now().Format("20060102-150405")
	if out, err := runSSHQuiet(client, "bash -c "+shQuote(writeSSHKeyScript(hubAPIEnv, backup, hex.EncodeToString(sum[:]))), "SSH_ENCRYPTION_KEY="+key+"\n"); err != nil {
		log.Fatalf("[hub] SSH_ENCRYPTION_KEY → api.env: %v", err)
	} else {
		fmt.Print(out)
	}
	after, _, err := readHubAPIEnv(client)
	if err != nil || envFileValue(after, "SSH_ENCRYPTION_KEY") != key {
		log.Fatalf("[hub] SSH_ENCRYPTION_KEY не читается из api.env после записи (%v) — прежний файл: %s", err, backup)
	}
	if generated {
		fmt.Println("[hub] SSH_ENCRYPTION_KEY сгенерирован (зашифрованных паролей в БД не было)")
	} else {
		fmt.Println("[hub] SSH_ENCRYPTION_KEY взят из server/.env")
	}
	keepKeyCopy(client) // a copy WITH the key (офлайн-копию делает владелец)
	return true
}

// putHubFile writes data to dst on the Hub over SSH stdin (temp + mv; no SFTP — it hangs), only
// when the content or mode differs.
func putHubFile(client *ssh.Client, dst, mode string, data []byte) {
	sum := sha256.Sum256(data)
	want := hex.EncodeToString(sum[:]) + " " + mode
	cur, _ := runSSHQuiet(client, "if [ -f "+shQuote(dst)+" ]; then echo \"$(sha256sum < "+shQuote(dst)+" | cut -d' ' -f1) $(stat -c %a "+shQuote(dst)+")\"; fi", "")
	if strings.TrimSpace(cur) == want {
		fmt.Printf("[hub] %s без изменений\n", dst)
		return
	}
	// The file, then its sha256 next to it (the API checks it before sending the file to a
	// node: a cut-off upload never reaches an exit).
	cmd := "set -e; umask 077; mkdir -p " + shQuote(path.Dir(dst)) + "; cat > " + shQuote(dst+".tmp") + "; chmod " + mode + " " + shQuote(dst+".tmp") +
		"; mv -f " + shQuote(dst+".tmp") + " " + shQuote(dst) +
		"; echo " + hex.EncodeToString(sum[:]) + " > " + shQuote(dst+".sha256.tmp") + "; chmod 644 " + shQuote(dst+".sha256.tmp") +
		"; mv -f " + shQuote(dst+".sha256.tmp") + " " + shQuote(dst+".sha256")
	if _, err := runSSHQuiet(client, cmd, string(data)); err != nil {
		log.Fatalf("[hub] запись %s: %v", dst, err)
	}
	fmt.Printf("[hub] %s записан (%d байт)\n", dst, len(data))
}

// apiServesLegacyCAScript prints yes when the RUNNING heyearth-api reads the legacy client pair
// (its loader names provision.LegacyClientCertFile): the image of its MainPID (procRoot/<pid>/exe)
// — a binary deployed but not restarted yet does not count. With the API stopped, the binary on
// disk is what starts. Anything unreadable: no.
func apiServesLegacyCAScript(procRoot, binary string) string {
	return `pid=$(systemctl show -p MainPID --value heyearth-api 2>/dev/null || true)
exe=` + shQuote(binary) + `
case "$pid" in ''|0|*[!0-9]*) ;; *) exe=` + shQuote(procRoot) + `/$pid/exe ;; esac
if grep -qaF ` + shQuote(provision.LegacyClientCertFile) + ` "$exe" 2>/dev/null; then echo yes; else echo no; fi
`
}

// apiServesLegacyCA: replacing the CA does not cut off agents of the old CA.
func apiServesLegacyCA(client *ssh.Client) bool {
	out, err := runSSHQuiet(client, "bash -c "+shQuote(apiServesLegacyCAScript("/proc", hubAPIBinary)), "")
	return err == nil && strings.TrimSpace(out) == "yes"
}

// legacyCleanupCommand: what the owner runs once every exit is converged — the previous CA and
// client pair, and the old shared node-agent certificate of the leaked CA (unused on the Hub).
func legacyCleanupCommand() string {
	files := []string{provision.LegacyCACertFile, provision.LegacyClientCertFile, provision.LegacyClientKeyFile, "server-cert.pem", "server-key.pem"}
	for i, f := range files {
		files[i] = hubCertsDir + "/" + f
	}
	return "go run ./cmd/deploy --ssh 'rm -f " + strings.Join(files, " ") + "'"
}

// reportHubCA prints what ensureHubNodeCA did; fatal=false only warns on an error (deploys go on).
func reportHubCA(p hubCAPlan, err error, fatal bool) {
	switch {
	case err != nil && fatal:
		log.Fatalf("[ca] %v", err)
	case err != nil:
		fmt.Printf("[ca] WARN: CA node-agent на Hub не проверен: %v\n", err)
		return
	}
	if p.keepLegacyCA || p.keepLegacyClient {
		fmt.Printf("[ca] прежний CA сохранён как %s (+ прежняя клиентская пара: %v)\n", provision.LegacyCACertFile, p.keepLegacyClient)
	}
	switch {
	case p.newCA:
		fmt.Println("[ca] на Hub создан собственный CA node-agent (ключ не покидал Hub) и клиентский сертификат API")
	case p.newClient:
		fmt.Println("[ca] клиентский сертификат API перевыпущен CA Hub")
	case p.notOurs:
	default:
		fmt.Println("[ca] CA node-agent на Hub в порядке — без изменений")
	}
	if p.note != "" {
		fmt.Println("[ca] " + p.note)
	}
}

// ensureHubProvisioning — see the file comment. Restarts nothing.
func ensureHubProvisioning(client *ssh.Client, cfg *deployConfig) {
	ctx := context.Background()
	hub := provision.NewSSHRemote(client)

	fmt.Println("[hub] 1/4 SSH_ENCRYPTION_KEY в api.env")
	keyWritten := ensureSSHEncryptionKey(client, cfg)

	fmt.Println("[hub] 2/4 ключ SSH Hub для нод")
	pub, err := provision.EnsureHubKey(ctx, hub)
	if err != nil {
		log.Fatalf("[hub] %v", err)
	}
	fmt.Printf("[hub] ключ Hub: %s (%s)\n", provision.HubKeyFingerprint(pub), pub)

	fmt.Println("[hub] 3/4 node-agent и установщик MTProxy")
	agent, err := os.ReadFile(buildGoBinary(cfg.backendRoot, "node-agent", "node-agent"))
	if err != nil {
		log.Fatal(err)
	}
	putHubFile(client, hubNodeAgentArtifact, "755", agent)
	mt, err := os.ReadFile(filepath.Join(cfg.backendRoot, "cmd", "deploy", "install-mtproxy.sh"))
	if err != nil {
		log.Fatal(err)
	}
	putHubFile(client, hubMTProxyScript, "755", mt)

	fmt.Println("[hub] 4/4 CA node-agent на Hub")
	mode := caRotate
	if !apiServesLegacyCA(client) {
		mode = caKeep
	}
	p, err := ensureHubNodeCA(ctx, hub, hubCertsDir, hubClock(ctx, hub)(), mode, false)
	reportHubCA(p, err, true)
	if p.notOurs {
		fmt.Println("[ca] CA пока прежний: API на Hub ещё не умеет держать прежнюю клиентскую пару (exit без сведения потеряли бы связь с Hub). " +
			"Выкатите API этой ветки (deploy_api.sh), затем повторите go run ./cmd/deploy --ensure-hub-provisioning — CA будет заменён без обрыва связи с агентами.")
	}
	if f, err := readHubCAFiles(ctx, hub, hubCertsDir); err == nil && len(f.legacyCA) > 0 {
		fmt.Println("[ca] прежний CA ещё доверен Hub (" + provision.LegacyCACertFile + "). Сведите каждый exit (карточка ноды → «Переустановить» или --setup-exit-node), " +
			"проверьте метрики нод в админке, затем удалите прежние файлы (и общий сертификат агента прежнего CA):\n    " + legacyCleanupCommand() + "\n" +
			"[ca] Пока они лежат, НЕ откатывайте API ниже 22fb979: прежний API берёт один клиентский сертификат и один CA и потеряет exit без сведения.")
	}

	fmt.Println("[hub] готово: Бамбук, xray и API не перезапускались.")
	if keyWritten {
		fmt.Println("[hub] Чтобы API прочитал новый SSH_ENCRYPTION_KEY, перезапустите только его (деплоем API или так):\n" +
			"    go run ./cmd/deploy --ssh 'systemctl restart heyearth-api'\n" +
			"  (не --restart: он перезапускает и xray). После старта API зашифрует открытые пароли SSH старых записей.")
	}
}

// rotateHubClientCert (--rotate-node-agent-certs): a new client certificate for the API from the
// Hub's CA. Node certificates are per node and renewed by the exit recipe; the CA is replaced only
// by --ensure-hub-provisioning.
func rotateHubClientCert(client *ssh.Client) {
	ctx, hub := context.Background(), provision.NewSSHRemote(client)
	p, err := ensureHubNodeCA(ctx, hub, hubCertsDir, hubClock(ctx, hub)(), caKeep, true)
	reportHubCA(p, err, true)
	if p.notOurs {
		log.Fatal("[ca] клиентский сертификат не перевыпущен: CA на Hub ещё прежний — сначала go run ./cmd/deploy --ensure-hub-provisioning")
	}
}
