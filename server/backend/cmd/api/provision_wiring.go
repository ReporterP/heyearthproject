package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/heyearth/backend/internal/admin"
	"github.com/heyearth/backend/internal/biz"
	"github.com/heyearth/backend/internal/config"
	"github.com/heyearth/backend/internal/crypto"
	"github.com/heyearth/backend/internal/db"
	"github.com/heyearth/backend/internal/provision"
)

// nodeStore is the API's provisioning store: the Hub's own key on disk, node passwords
// decrypted with SSH_ENCRYPTION_KEY.
var nodeStore = &provision.SQLStore{HubKeyPath: config.HubSSHKeyPath, Decrypt: crypto.Decrypt}

// Seams of the wiring (tests replace them).
var (
	exitHubIPs        = hubOwnIPs
	exitSetting       = biz.GetSettingOrDefault
	mtproxyScriptPath = "/opt/heyearth/scripts/install-mtproxy.sh"
	// relayHubHost reads main_server_host strictly: sql.ErrNoRows when absent, any other error a
	// failed read (never a default — a guessed DNAT target would forward users elsewhere).
	relayHubHost   = func(ctx context.Context) (string, error) { return biz.GetSettingContext(ctx, "main_server_host") }
	relayHubLookup = provision.DefaultHubLookup
)

// initProvisioning: jobs the previous process left running become interrupted (resumable with
// «Продолжить»), SSH_ENCRYPTION_KEY is checked (a critical alert without it) and legacy plaintext
// SSH passwords get encrypted, dead CLI runs are swept periodically, the admin handlers get the
// runner. It restarts nothing — the API never restarts itself.
func initProvisioning() {
	nodeStore.DB = db.DB
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if n, err := nodeStore.InterruptRunning(ctx); err != nil {
		log.Printf("[provision] не удалось пометить прерванные задачи: %v", err)
	} else if n > 0 {
		log.Printf("[provision] %d задач(и) прервано перезапуском API — их можно продолжить в карточке ноды", n)
	}
	if checkSSHEncryptionKey(crypto.Ready) {
		migrateLegacySSHPasswords(ctx)
		checkSSHCiphertexts(ctx)
	}
	nodeRunner = &provision.Runner{Store: nodeStore, Dial: provision.DialNode, Recipe: apiRecipe, Logf: log.Printf}
	go nodeRunner.SweepStale(context.Background(), provision.StaleSweepEvery)
	admin.NodeProvisioner = nodeRunner
	admin.NodeJobs = nodeStore
	admin.HubPublicKey = func(ctx context.Context) (string, error) { return provision.EnsureHubKey(ctx, provision.LocalRemote{}) }
	admin.HubOwnIPs = hubOwnIPs
}

// nodeRunner runs every node job of the API (admin handlers and the reconciler's MTProxy backfill).
var nodeRunner *provision.Runner

// sshKeyAlertKey: the critical alert while SSH_ENCRYPTION_KEY is missing or invalid.
const sshKeyAlertKey = "ssh-encryption-key"

// checkSSHEncryptionKey raises sshKeyAlertKey (critical) when ready fails — node passwords can be
// neither stored nor read, so adding and setting up nodes from the admin panel cannot work — and
// resolves it otherwise. The error names the variable, never the key (crypto.Ready).
func checkSSHEncryptionKey(ready func() error) bool {
	if err := ready(); err != nil {
		RaiseAlert(sshKeyAlertKey, AlertCritical, "На Hub не задан SSH_ENCRYPTION_KEY",
			"Пароли нод нельзя сохранить и прочитать — добавление и настройка нод из админки не работают, "+
				"открытые пароли SSH старых записей не зашифрованы. Выполните go run ./cmd/deploy --ensure-hub-provisioning "+
				"и перезапустите API деплоем. Ошибка: "+err.Error())
		log.Printf("[provision] SSH_ENCRYPTION_KEY: %v — пароли нод недоступны, миграция открытых паролей пропущена", err)
		return false
	}
	ResolveAlert(sshKeyAlertKey, "SSH_ENCRYPTION_KEY на месте")
	return true
}

// sshCiphertextsAlertKey: the warning while stored SSH passwords do not decrypt with the key the
// API runs with.
const sshCiphertextsAlertKey = "ssh-ciphertexts-unreadable"

// storedCiphertextsQuery: every stored encrypted SSH password.
const storedCiphertextsQuery = `SELECT id, ssh_password_enc FROM servers
 WHERE COALESCE(octet_length(ssh_password_enc), 0) > 0 ORDER BY id`

// storedCipher is one row of storedCiphertextsQuery.
type storedCipher struct {
	id  int
	enc []byte
}

// unreadableCiphertexts returns the ids whose ciphertext decrypt refuses.
func unreadableCiphertexts(rows []storedCipher, decrypt func([]byte) (string, error)) []int {
	var ids []int
	for _, r := range rows {
		if _, err := decrypt(r.enc); err != nil {
			ids = append(ids, r.id)
		}
	}
	return ids
}

// reportUnreadableCiphertexts raises sshCiphertextsAlertKey (warning) for the given server ids —
// a valid but DIFFERENT SSH_ENCRYPTION_KEY passes crypto.Ready, and nothing else would tell — and
// resolves it when there are none. Only ids and the count, never a ciphertext.
func reportUnreadableCiphertexts(ids []int) {
	if len(ids) == 0 {
		ResolveAlert(sshCiphertextsAlertKey, "Все сохранённые пароли SSH расшифровываются текущим SSH_ENCRYPTION_KEY")
		return
	}
	list := make([]string, len(ids))
	for i, id := range ids {
		list[i] = strconv.Itoa(id)
	}
	RaiseAlert(sshCiphertextsAlertKey, AlertWarn,
		fmt.Sprintf("Пароли SSH %d нод(ы) не расшифровываются ключом Hub", len(ids)),
		"Серверы "+strings.Join(list, ", ")+": сохранённый пароль SSH не расшифровывается текущим SSH_ENCRYPTION_KEY — "+
			"вероятно, ключ в api.env сменили. Ноды без ключа Hub не смогут подключиться по паролю (настройка, проверка, отключение). "+
			"Верните прежний ключ в api.env и перезапустите API деплоем либо введите пароли этих нод заново.")
	log.Printf("[provision] пароли SSH серверов %s не расшифровываются текущим SSH_ENCRYPTION_KEY", strings.Join(list, ", "))
}

// checkSSHCiphertexts counts, at API start, the stored SSH passwords the current key cannot read.
func checkSSHCiphertexts(ctx context.Context) {
	rows, err := db.DB.QueryContext(ctx, storedCiphertextsQuery)
	if err != nil {
		log.Printf("[provision] зашифрованные пароли SSH не прочитаны: %v", err)
		return
	}
	var all []storedCipher
	for rows.Next() {
		var r storedCipher
		if err := rows.Scan(&r.id, &r.enc); err != nil {
			log.Printf("[provision] зашифрованные пароли SSH: строка не прочитана: %v", err)
			continue
		}
		all = append(all, r)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		log.Printf("[provision] зашифрованные пароли SSH не прочитаны: %v", err)
		return
	}
	reportUnreadableCiphertexts(unreadableCiphertexts(all, crypto.Decrypt))
}

// legacyPasswordsQuery: rows that still hold a plaintext SSH password (ssh_password is NOT NULL,
// default the empty string = none; migration 000004).
const legacyPasswordsQuery = `SELECT id, ssh_password, ssh_password_enc FROM servers WHERE ssh_password <> ''`

// The migration's writes. ssh_password is NOT NULL: the plaintext is emptied (set to the empty
// string), never NULLed. Each write re-checks what the decision was made on — the plaintext read
// ($2) and, for the encryption, that there is still no ciphertext; for the clean-up, the very
// ciphertext that decrypted ($3) — so a password the owner changed meanwhile is never overwritten
// or lost.
const (
	encryptLegacyPasswordQuery = `UPDATE servers SET ssh_password_enc = $3, ssh_password = ''
 WHERE id = $1 AND ssh_password = $2 AND COALESCE(octet_length(ssh_password_enc), 0) = 0`
	clearLegacyPasswordQuery = `UPDATE servers SET ssh_password = ''
 WHERE id = $1 AND ssh_password = $2 AND ssh_password_enc = $3`
)

// legacyPassword is one row of legacyPasswordsQuery.
type legacyPassword struct {
	id    int
	plain string
	enc   []byte
}

// legacyPasswordAction: encrypt a plaintext-only password; drop the plaintext once a readable
// ciphertext exists; keep it while the ciphertext does not decrypt (it may be the only copy).
func legacyPasswordAction(enc []byte, decrypt func([]byte) (string, error)) string {
	if len(enc) == 0 {
		return "encrypt"
	}
	if _, err := decrypt(enc); err == nil {
		return "clear"
	}
	return "keep"
}

// migrateLegacySSHPasswords moves plaintext SSH passwords (servers.ssh_password, from before
// SSH_ENCRYPTION_KEY) into ssh_password_enc at API start — the provisioning store reads only the
// ciphertext. Idempotent: a migrated row no longer matches legacyPasswordsQuery. Logs server ids
// only, never a password.
func migrateLegacySSHPasswords(ctx context.Context) {
	rows, err := db.DB.QueryContext(ctx, legacyPasswordsQuery)
	if err != nil {
		log.Printf("[provision] открытые пароли SSH не прочитаны: %v", err)
		return
	}
	var todo []legacyPassword
	for rows.Next() {
		var r legacyPassword
		if err := rows.Scan(&r.id, &r.plain, &r.enc); err != nil {
			log.Printf("[provision] открытые пароли SSH: строка не прочитана: %v", err)
			continue
		}
		todo = append(todo, r)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		log.Printf("[provision] открытые пароли SSH не прочитаны: %v", err)
		return
	}
	exec := func(ctx context.Context, q string, args ...any) (int64, error) {
		res, err := db.DB.ExecContext(ctx, q, args...)
		if err != nil {
			return 0, err
		}
		return res.RowsAffected()
	}
	migrateLegacyPasswords(ctx, todo, exec, crypto.Encrypt, crypto.Decrypt, log.Printf)
}

// migrateLegacyPasswords applies legacyPasswordAction to each row with the guarded writes above.
// exec returns the rows a write changed (0: the row changed since it was read — left as it is).
func migrateLegacyPasswords(ctx context.Context, todo []legacyPassword,
	exec func(ctx context.Context, query string, args ...any) (int64, error),
	encrypt func(string) ([]byte, error), decrypt func([]byte) (string, error), logf func(string, ...any)) {
	for _, r := range todo {
		var query, done string
		var args []any
		switch legacyPasswordAction(r.enc, decrypt) {
		case "encrypt":
			enc, err := encrypt(r.plain)
			if err != nil || len(enc) == 0 {
				logf("[provision] сервер %d: не удалось зашифровать открытый пароль SSH (%v) — открытую копию не трогаю", r.id, err)
				continue
			}
			query, args, done = encryptLegacyPasswordQuery, []any{r.id, r.plain, enc}, "открытый пароль SSH зашифрован"
		case "clear":
			query, args, done = clearLegacyPasswordQuery, []any{r.id, r.plain, r.enc}, "открытая копия пароля SSH удалена (зашифрованный пароль читается)"
		default:
			logf("[provision] сервер %d: зашифрованный пароль не читается этим SSH_ENCRYPTION_KEY — открытую копию не трогаю", r.id)
			continue
		}
		switch n, err := exec(ctx, query, args...); {
		case err != nil:
			logf("[provision] сервер %d: пароль SSH не перенесён: %v", r.id, err)
		case n == 0:
			logf("[provision] сервер %d: строка изменилась после чтения — пароль SSH не тронут, повтор при следующем старте", r.id)
		default:
			logf("[provision] сервер %d: %s", r.id, done)
		}
	}
}

// apiHooks binds recipe side effects to the API's database: syncs go through biz.RequestSync
// (never an API restart), the agent check is the Hub's own mTLS call (admin.ProbeNodeAgent,
// bounded by admin.NodeAgentProbeTimeout).
func apiHooks() provision.Hooks { return nodeStore.Hooks(biz.RequestSync, admin.ProbeNodeAgent) }

// apiRecipe is the runner's recipe source: the Hub side runs locally (the API is on the Hub).
func apiRecipe(ctx context.Context, n provision.Node, kind string) (provision.Recipe, *provision.State, error) {
	st := provision.NewState(kind, n, provision.LocalRemote{}, apiHooks())
	// The options' errors name what the owner fixes on the Hub (addresses, assets): refusals.
	switch {
	case kind == provision.KindMTProxy: // MTProxy only: no Hub address list needed
		mt, err := apiMTProtoOptions(n)
		if err != nil {
			return nil, nil, provision.Refusal(err)
		}
		st.Exit.MTProto = mt
	case kind == provision.KindDeprovision: // taking a node out of service needs no options
	case n.Role == "exit":
		opts, err := apiExitOptions(n)
		if err != nil {
			return nil, nil, provision.Refusal(err)
		}
		st.Exit = opts
	case n.Role == "relay":
		opts, err := apiRelayOptions(ctx, n)
		if err != nil {
			return nil, nil, err // a refusal, or an internal error (the setting could not be read)
		}
		st.Relay = opts
	}
	rc, err := provision.RecipeFor(kind, n, st)
	return rc, st, err
}

// apiRelayOptions: what a relay recipe takes from the Hub. The DNAT target is main_server_host —
// the address users reach the Hub at today, which its replies come from (the relay's conntrack
// matches only replies from the address it sent to) — and never hubOwnIPs: that list also holds
// the Hub's other interface addresses, among them one filtered upstream. hubOwnIPs is only where
// SSH to the relay may come from (an active ufw without a rule for the SSH port).
func apiRelayOptions(ctx context.Context, n provision.Node) (provision.RelayOptions, error) {
	host, err := relayHubHost(ctx)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		host = "" // RelayOptionsFor refuses: no Hub address, no relay
	case err != nil:
		return provision.RelayOptions{}, fmt.Errorf("main_server_host не прочитан: %w", err)
	}
	o, err := provision.RelayOptionsFor(ctx, n.IP, n.SSHPort, host, relayHubLookup)
	if err != nil {
		return provision.RelayOptions{}, provision.Refusal(err)
	}
	o.HubIPs = exitHubIPs()
	return o, nil
}

// apiExitOptions: what an exit recipe takes from the Hub. HubIPs is ONE list for the node's
// firewall (SSH from the Hub) and its agent guard (TCP 50052 from the Hub only); without it no
// setup job is created. MTProxy options only when the owner did not switch it off; the MTProxy
// port is allowed in the ACL / guard rules either way.
func apiExitOptions(n provision.Node) (provision.ExitOptions, error) {
	hubIPs := exitHubIPs()
	if len(hubIPs) == 0 {
		return provision.ExitOptions{}, errors.New("публичные адреса Hub не определены (адреса интерфейсов и main_server_host в настройках) — " +
			"агент ноды открыл бы порт 50052 никому или Hub потерял бы с ним связь; задача не создана")
	}
	certs := filepath.Join(config.AppDir(), "certs")
	port := config.ParseMTProtoPort(exitSetting("mtproto_port", ""))
	o := provision.ExitOptions{
		HubIPs:      hubIPs,
		MTProtoPort: port,
		HubXray:     xrayUnitBinary,
		NodeAgent:   func() ([]byte, error) { return readHubFile(filepath.Join(config.AppDir(), "artifacts", "node-agent")) },
		CA: func() ([]byte, []byte, error) {
			cert, err := readHubFile(filepath.Join(certs, "ca-cert.pem"))
			if err != nil {
				return nil, nil, err
			}
			key, err := readHubFile(filepath.Join(certs, "ca-key.pem"))
			return cert, key, err
		},
	}
	mt, err := apiMTProtoOptions(n)
	if err != nil {
		return o, err
	}
	o.MTProto = mt
	return o, nil
}

// apiMTProtoOptions: the node's MTProxy from the Hub's settings (the port is the one apiExitOptions
// allows in the ACL / guard rules); nil when the owner switched MTProxy off for the node. The exit
// fronts its own domain (exitMTProtoDomain) with the shared key.
func apiMTProtoOptions(n provision.Node) (*provision.MTProtoOptions, error) {
	if n.MTProtoStatus == "disabled" {
		return nil, nil
	}
	script, err := readHubFile(mtproxyScriptPath)
	if err != nil {
		return nil, err
	}
	o := &provision.MTProtoOptions{Port: config.ParseMTProtoPort(exitSetting("mtproto_port", "")),
		Secret: strings.TrimSpace(exitSetting("mtproto_secret", "")),
		Domain: strings.TrimSpace(exitSetting("mtproto_fronting_domain", "")), Script: script}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := exitMTProtoDomainStep(ctx, n, o); err != nil {
		return nil, err
	}
	return o, nil
}

// exitMTProtoDomainStep is exitMTProtoDomain on the API's database (tests swap it).
var exitMTProtoDomainStep = func(ctx context.Context, n provision.Node, o *provision.MTProtoOptions) error {
	return exitMTProtoDomain(ctx, db.DB, n, o)
}

// exitMTProtoDomain points o at the exit's own fronting domain (biz.ExitFrontingDomain) and its
// secret, recording the domain first: the deeplink hands out the secret of the recorded one. No
// domain found: o keeps the shared domain and the installer says it does not resolve to the node
// (an exit installs without MTProxy). A failed read or write of the record is an error.
func exitMTProtoDomain(ctx context.Context, q interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}, n provision.Node, o *provision.MTProtoOptions) error {
	stored, err := biz.ExitMTProtoDomain(ctx, q, n.ID)
	if err != nil {
		return fmt.Errorf("домен Telegram-прокси ноды %d: %w", n.ID, err)
	}
	domain, err := biz.ExitFrontingDomain(ctx, n.IP, stored, o.Domain)
	if err != nil {
		log.Printf("[mtproxy] нода %d: %v", n.ID, err)
		return nil
	}
	secret, err := biz.MTProtoSecretFor(o.Secret, domain)
	if err != nil {
		return nil // the installer's own check names the bad shared secret
	}
	if err := biz.SetExitMTProtoDomain(ctx, q, n.ID, domain); err != nil {
		return fmt.Errorf("домен Telegram-прокси ноды %d не записан: %w", n.ID, err)
	}
	o.Domain, o.Secret = domain, secret
	return nil
}

// readHubFile names the deploy step that puts a missing provisioning asset on the Hub.
func readHubFile(path string) ([]byte, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("на Hub нет %s — выполните go run ./cmd/deploy --ensure-hub-provisioning (%v)", path, err)
	}
	if err := checkArtifactSum(path, b); err != nil {
		return nil, err
	}
	return b, nil
}

// checkArtifactSum compares a Hub artifact with the sha256 written next to it by the deploy
// (cleanup plan B7): a cut-off upload must fail here, before an exit gets a broken binary.
// A file without the checksum (older deploys) is accepted.
func checkArtifactSum(path string, data []byte) error {
	want, err := os.ReadFile(path + ".sha256")
	if err != nil {
		return nil
	}
	sum := sha256.Sum256(data)
	f := strings.Fields(string(want))
	if got := hex.EncodeToString(sum[:]); len(f) == 0 || !strings.EqualFold(f[0], got) {
		return fmt.Errorf("файл %s на Hub повреждён (sha256 не совпадает с записанным при деплое) — выложите его заново: go run quick_deploy.go", path)
	}
	return nil
}

// mtproxyRetryAfter: the reconciler's backfill does not start another MTProxy job on a node
// whose latest job is an MTProxy job younger than this (a node whose install keeps failing gets a
// job every few hours, not every tick).
const mtproxyRetryAfter = 6 * time.Hour

// mtproxyBackfill is the ghost reconciler's MTProxy backfill: a provision.KindMTProxy job on the
// first node that takes one — the same job as «Переустановить Telegram-прокси», under the node's
// one-job lock, over the pinned connection. The recipe refuses (no job) a node that is not an
// online exit, has MTProxy switched off, an unconfirmed host key or bad MTProxy settings.
type mtproxyBackfill struct {
	start func(ctx context.Context, serverID int, kind string) (int64, error)
	jobs  func(ctx context.Context, serverID, limit int) ([]provision.JobView, error)
	now   func() time.Time
	logf  func(format string, a ...any)
}

// run starts at most one job; it returns the node it started one on (0: none).
func (b mtproxyBackfill) run(ctx context.Context, needing []int) int {
	for _, id := range needing {
		jobs, err := b.jobs(ctx, id, 1)
		if err != nil {
			b.logf("[ghost-reconciler] mtproxy %d: задачи не прочитаны: %v", id, err)
			continue
		}
		if len(jobs) > 0 && (jobs[0].Status == provision.JobRunning || jobs[0].Status == "queued") {
			continue // the node is busy; the next tick looks again
		}
		if len(jobs) > 0 && jobs[0].Kind == provision.KindMTProxy && b.now().Sub(jobs[0].CreatedAt) < mtproxyRetryAfter {
			b.logf("[ghost-reconciler] mtproxy %d: последняя попытка %s — следующая не раньше чем через %s", id,
				jobs[0].CreatedAt.Format(time.RFC3339), mtproxyRetryAfter)
			continue
		}
		jobID, err := b.start(ctx, id, provision.KindMTProxy)
		if err != nil {
			b.logf("[ghost-reconciler] mtproxy %d: пропущено: %v", id, err)
			continue
		}
		b.logf("[ghost-reconciler] mtproxy %d: задача %d запущена", id, jobID)
		return id
	}
	return 0
}
