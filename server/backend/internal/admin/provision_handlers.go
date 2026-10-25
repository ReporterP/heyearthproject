package admin

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/heyearth/backend/internal/biz"
	"github.com/heyearth/backend/internal/db"
	pbnode "github.com/heyearth/backend/internal/pb/node_agent_v1"
	"github.com/heyearth/backend/internal/provision"
	"github.com/lib/pq"
)

// Node provisioning API (phase 2): every change to a node is a job of internal/provision
// (persisted steps, resumable); SSH goes only to an owner-confirmed host key; nothing here
// restarts the API. Reads need servers.read, changes nodes.provision (routes.go).

// Provisioner runs node jobs (*provision.Runner, set by cmd/api).
type Provisioner interface {
	Start(ctx context.Context, serverID int, kind string) (int64, error)
	Resume(ctx context.Context, jobID int64) error
}

// NodeJobStore reads nodes and jobs (*provision.SQLStore, set by cmd/api). NodeMeta is the node
// without credentials (decisions that do not connect); LoadNode decrypts them (a connection).
type NodeJobStore interface {
	NodeMeta(ctx context.Context, serverID int) (provision.Node, error)
	LoadNode(ctx context.Context, serverID int) (provision.Node, error)
	JobView(ctx context.Context, jobID int64) (provision.JobView, error)
	ListJobs(ctx context.Context, serverID, limit int) ([]provision.JobView, error)
}

var (
	NodeProvisioner Provisioner
	NodeJobs        NodeJobStore
	// NodeProbe lists a node's host keys with handshake-only connections (no credentials sent).
	NodeProbe = provision.Probe
	// NodeDial connects with the owner-confirmed host key only.
	NodeDial = provision.DialNode
	// HubPublicKey returns the Hub's own SSH public key, creating it once (cmd/api).
	HubPublicKey func(ctx context.Context) (string, error)
	// HubOwnIPs are the Hub's public addresses (cmd/api: hubOwnIPs); a node cannot be the Hub.
	HubOwnIPs func() []string

	saveNodeOptions = saveNodeOptionsDB
)

const (
	// probeTimeout bounds a host key scan (one handshake per key type the node offers).
	probeTimeout = 30 * time.Second
	// NodeAgentProbeTimeout bounds ProbeNodeAgent on its own (the recipe's agent check allows the same).
	NodeAgentProbeTimeout = 20 * time.Second

	hostKeyFirstMsg    = "Сначала подтвердите отпечаток ключа сервера: без него Hub к серверу не подключается"
	notInitializedMsg  = "Провижининг не инициализирован"
	unknownPlanMsg     = "В plan_ids есть несуществующий тариф"
	confirmNameMsg     = "Введите название ноды точно — так подтверждается опасное действие"
	planIDsPositiveMsg = "plan_ids — номера тарифов (целые числа больше нуля)"
	nodeIPExistsMsg    = "Нода с этим IP уже есть"
	nodeUnreachableMsg = "Не удалось подключиться к серверу по SSH — подробности в журнале API"
	jobActiveMsg       = "Для этой ноды уже выполняется задача — дождитесь её окончания"
	// probeFailedMsg is the only answer to a failed probe: refused, filtered, not SSH, no
	// supported key all look the same, so the endpoint tells nothing about other ports or hosts.
	probeFailedMsg = "Не удалось получить ключ SSH сервера: проверьте IP-адрес и SSH-порт, что сервер включён и SSH на нём доступен"
)

func provisionReady(w http.ResponseWriter) bool {
	if NodeProvisioner == nil || NodeJobs == nil {
		writeAdminError(w, http.StatusServiceUnavailable, notInitializedMsg)
		return false
	}
	return true
}

func writeJobError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, provision.ErrJobActive):
		writeAdminError(w, http.StatusConflict, jobActiveMsg)
	case errors.Is(err, provision.ErrNodeNotFound):
		writeAdminError(w, http.StatusNotFound, "Нода не найдена")
	case errors.Is(err, provision.ErrMTProxyDisabled):
		writeAdminError(w, http.StatusConflict, err.Error())
	case errors.Is(err, provision.ErrJobNotResumable):
		writeAdminError(w, http.StatusConflict, err.Error())
	case errors.Is(err, provision.ErrHostKeyNotConfirmed):
		writeAdminError(w, http.StatusConflict, hostKeyFirstMsg)
	case provision.IsRefusal(err): // the node's state, the Hub's settings or assets: the owner's text
		writeAdminError(w, http.StatusConflict, err.Error())
	default: // database, network: never shown as is
		log.Printf("[nodes] job: %v", err)
		writeAdminError(w, http.StatusInternalServerError, "Не удалось запустить задачу")
	}
}

// writeNodeAccessError answers a failed SSH connection to a node.
func writeNodeAccessError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, provision.ErrNodeNotFound):
		writeAdminError(w, http.StatusNotFound, "Нода не найдена")
	case errors.Is(err, provision.ErrHostKeyNotConfirmed):
		writeAdminError(w, http.StatusConflict, hostKeyFirstMsg)
	case errors.Is(err, provision.ErrHostKeyMismatch):
		log.Printf("[nodes] ssh: %v", err)
		writeAdminError(w, http.StatusConflict, "Сервер предъявил не тот ключ, что подтверждён: он переустановлен или это не ваш сервер. "+
			"Проверьте подключение и подтвердите ключ заново в карточке ноды")
	case errors.Is(err, provision.ErrNoCredentials):
		log.Printf("[nodes] ssh: %v", err)
		writeAdminError(w, http.StatusConflict, "Нет доступа к серверу: пароль не расшифровывается или не подходит, а ключа Hub на сервере нет — задайте пароль заново")
	default: // refused, timed out, no SSH: one answer, details in the log (no port probing)
		log.Printf("[nodes] ssh: %v", err)
		writeAdminError(w, http.StatusBadGateway, nodeUnreachableMsg)
	}
}

// decodeOptional: an empty body is an empty object.
func decodeOptional(r *http.Request, v any) error {
	if err := json.NewDecoder(r.Body).Decode(v); err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	return nil
}

func validPlanIDs(ids []int) bool {
	for _, id := range ids {
		if id < 1 {
			return false
		}
	}
	return true
}

// isUniqueViolation: the write hit a unique index (servers: uq_servers_ip_address).
func isUniqueViolation(err error) bool {
	var pe *pq.Error
	return errors.As(err, &pe) && pe.Code == "23505"
}

// unknownPlan: a plan_servers insert hit the plans foreign key.
func unknownPlan(err error) bool {
	var pe *pq.Error
	return errors.As(err, &pe) && pe.Code == "23503"
}

// loadNodeMeta reads the node without credentials and answers 404 / 500 itself (a database
// error's text stays in the log).
func loadNodeMeta(w http.ResponseWriter, ctx context.Context, id int) (provision.Node, bool) {
	n, err := NodeJobs.NodeMeta(ctx, id)
	switch {
	case errors.Is(err, provision.ErrNodeNotFound):
		writeAdminError(w, http.StatusNotFound, "Нода не найдена")
		return n, false
	case err != nil:
		log.Printf("[nodes] node %d: %v", id, err)
		writeAdminError(w, http.StatusInternalServerError, "Не удалось загрузить ноду")
		return n, false
	}
	return n, true
}

// nodeHasActiveJob: the node's latest job is still queued or running.
func nodeHasActiveJob(ctx context.Context, id int) (bool, error) {
	jobs, err := NodeJobs.ListJobs(ctx, id, 1)
	if err != nil {
		return false, err
	}
	return len(jobs) > 0 && (jobs[0].Status == provision.JobRunning || jobs[0].Status == "queued"), nil
}

// refuseIfJobActive answers 409 (or 500) and returns true when a job runs on the node.
func refuseIfJobActive(w http.ResponseWriter, ctx context.Context, id int) bool {
	active, err := nodeHasActiveJob(ctx, id)
	if err != nil {
		log.Printf("[nodes] node %d jobs: %v", id, err)
		writeAdminError(w, http.StatusInternalServerError, "Не удалось проверить задачи ноды")
		return true
	}
	if active {
		writeAdminError(w, http.StatusConflict, jobActiveMsg)
		return true
	}
	return false
}

// blockedNodeNets: addresses that are never a node — "this network", CGNAT, IETF protocol
// assignments, benchmarking, reserved (with broadcast); IPv6 ranges that embed an IPv4 host or
// are for documentation. Non-global-unicast, loopback, private (RFC 1918, ULA), link-local,
// multicast and unspecified are refused by class.
var blockedNodeNets = func() []*net.IPNet {
	var out []*net.IPNet
	for _, c := range []string{"0.0.0.0/8", "100.64.0.0/10", "192.0.0.0/24", "198.18.0.0/15", "240.0.0.0/4",
		// IPv6: IPv4-compatible, NAT64, 6to4 (both map to an IPv4 host, possibly internal), documentation.
		"::/96", "64:ff9b::/96", "2002::/16", "2001:db8::/32"} {
		_, n, _ := net.ParseCIDR(c)
		out = append(out, n)
	}
	return out
}()

// validateNodeAddr is the one check of a node's address for every endpoint that connects to it
// (probe, create, a stored node's probe): a public unicast host that is not the Hub, port 1..65535
// (0 = 22). It keeps the probe from reaching the Hub's own or internal services.
func validateNodeAddr(ipText string, port int) (string, int, error) {
	ip := net.ParseIP(strings.TrimSpace(ipText))
	if ip == nil {
		return "", 0, errors.New("IP-адрес сервера некорректен")
	}
	if v4 := ip.To4(); v4 != nil {
		ip = v4
	}
	bad := !ip.IsGlobalUnicast() || ip.IsLoopback() || ip.IsPrivate() || ip.IsUnspecified() || ip.IsMulticast() ||
		ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsInterfaceLocalMulticast()
	for _, n := range blockedNodeNets {
		bad = bad || n.Contains(ip)
	}
	if bad {
		return "", 0, errors.New("IP-адрес сервера некорректен: нужен публичный адрес (не локальный, не частный, не CGNAT)")
	}
	if HubOwnIPs != nil {
		for _, h := range HubOwnIPs() {
			if hip := net.ParseIP(strings.TrimSpace(h)); hip != nil && hip.Equal(ip) {
				return "", 0, errors.New("это адрес самого Hub — нода должна быть отдельным сервером")
			}
		}
	}
	if port == 0 {
		port = 22
	}
	if port < 1 || port > 65535 {
		return "", 0, errors.New("SSH-порт должен быть от 1 до 65535")
	}
	return ip.String(), port, nil
}

// probeNode runs the probe, logging the real failure and answering probeFailedMsg (502).
func probeNode(w http.ResponseWriter, ctx context.Context, ip string, port int) (provision.ProbeResult, bool) {
	res, err := NodeProbe(ctx, ip, port)
	if err != nil {
		log.Printf("[nodes] probe %s:%d: %v", ip, port, err)
		writeAdminError(w, http.StatusBadGateway, probeFailedMsg)
		return res, false
	}
	return res, true
}

// nodeRemote opens the pinned SSH connection to a node (the caller closes it).
func nodeRemote(ctx context.Context, serverID int) (provision.Remote, error) {
	if NodeJobs == nil {
		return nil, errors.New("провижининг не инициализирован")
	}
	n, err := NodeJobs.LoadNode(ctx, serverID)
	if err != nil {
		return nil, err
	}
	return NodeDial(n)
}

// ProbeNodeAgent is one mTLS GetMetrics to a node's agent, bounded by NodeAgentProbeTimeout
// (cmd/api sets it as the recipe's Hooks.ProbeAgent).
func ProbeNodeAgent(ctx context.Context, ip string) error {
	ctx, cancel := context.WithTimeout(ctx, NodeAgentProbeTimeout)
	defer cancel()
	client, conn, err := connectToNodeAgent(ctx, ip)
	if err != nil {
		return err
	}
	defer conn.Close()
	_, err = client.GetMetrics(ctx, &pbnode.GetMetricsRequest{})
	return err
}

// ── host keys ──

// findHostKey returns the key with fingerprint fp among the keys a probe saw.
func findHostKey(res provision.ProbeResult, fp string) (provision.HostKey, bool) {
	for _, k := range res.Keys {
		if k.Fingerprint == fp {
			return k, true
		}
	}
	return provision.HostKey{}, false
}

// presentedKeys lists a probe's keys for an error message.
func presentedKeys(res provision.ProbeResult) string {
	var s []string
	for _, k := range res.Keys {
		s = append(s, k.Type+" "+k.Fingerprint)
	}
	if len(s) == 0 {
		return "ни одного ключа"
	}
	return strings.Join(s, ", ")
}

type probePayload struct {
	IPAddress string `json:"ip_address"`
	SSHPort   int    `json:"ssh_port"`
	ServerID  int    `json:"server_id"`
}

// storedKeyView is the node's stored host key next to a probe by server id.
type storedKeyView struct {
	Fingerprint string `json:"fingerprint"`
	KeyType     string `json:"key_type"`
	Confirmed   bool   `json:"confirmed"`
	Presented   bool   `json:"presented"` // the node presented this key in this probe
}

type probeResponse struct {
	provision.ProbeResult
	Stored *storedKeyView `json:"stored,omitempty"`
}

// ProbeNodeHandler — SSH key exchanges only: every host key the node offers (all types, so the
// owner can compare any of them with the provider's console); nothing stored, no password sent.
// {server_id} probes a known node and shows which key is stored.
func ProbeNodeHandler(w http.ResponseWriter, r *http.Request) {
	var req probePayload
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeAdminError(w, http.StatusBadRequest, "Неверное тело запроса")
		return
	}
	var stored *storedKeyView
	if req.ServerID > 0 {
		if !provisionReady(w) {
			return
		}
		n, ok := loadNodeMeta(w, r.Context(), req.ServerID)
		if !ok {
			return
		}
		req.IPAddress, req.SSHPort = n.IP, n.SSHPort
		if n.HostKeyFP != "" {
			stored = &storedKeyView{Fingerprint: n.HostKeyFP, KeyType: n.HostKeyType, Confirmed: n.HostKeyConfirmed}
		}
	}
	ip, port, err := validateNodeAddr(req.IPAddress, req.SSHPort)
	if err != nil {
		writeAdminError(w, http.StatusBadRequest, err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), probeTimeout)
	defer cancel()
	res, ok := probeNode(w, ctx, ip, port)
	if !ok {
		return
	}
	if stored != nil {
		k, ok := findHostKey(res, stored.Fingerprint)
		stored.Presented = ok
		if ok && stored.KeyType == "" {
			stored.KeyType = k.Type
		}
	}
	writeAdminJSON(w, http.StatusOK, probeResponse{ProbeResult: res, Stored: stored})
}

type createNodePayload struct {
	Name        string `json:"name"`
	Role        string `json:"role"`
	CountryCode string `json:"country_code"`
	IPAddress   string `json:"ip_address"`
	SSHPort     int    `json:"ssh_port"`
	SSHUser     string `json:"ssh_user"`
	SSHPassword string `json:"ssh_password"`
	HostKeyFP   string `json:"ssh_host_key_fp"`
}

const nodeIPExistsQuery = `SELECT EXISTS (SELECT 1 FROM servers WHERE ip_address = $1)`

// insertNodeQuery: a pending node whose host key (the blob of the key the owner chose, as the
// node presented it just now) is confirmed by this very request; the password only encrypted.
const insertNodeQuery = `
INSERT INTO servers (name, role, is_exit_node, ip_address, ssh_port, ssh_user, ssh_password_enc, country_code,
                     lifecycle_state, ssh_host_key, ssh_host_key_fp, ssh_host_key_confirmed_at, updated_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, 'pending', $9, $10, NOW(), NOW())
RETURNING id`

// CreateNodeHandler stores a pending node: the host key the owner confirmed (checked against
// what the node presents now), the root password encrypted (no key — no node).
func CreateNodeHandler(w http.ResponseWriter, r *http.Request) {
	var req createNodePayload
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeAdminError(w, http.StatusBadRequest, "Неверное тело запроса")
		return
	}
	ip, port, err := validateNodeAddr(req.IPAddress, req.SSHPort)
	if err != nil {
		writeAdminError(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.Role == "" {
		req.Role = "exit"
	}
	if req.Role != "exit" {
		writeAdminError(w, http.StatusBadRequest, "Резервные входы в РФ добавляются в следующем обновлении")
		return
	}
	country := strings.ToUpper(strings.TrimSpace(req.CountryCode))
	if country == "" {
		writeAdminError(w, http.StatusBadRequest, "Укажите страну сервера — две латинские буквы ISO, например NL")
		return
	}
	if err := validateCountryCode(country); err != nil {
		writeAdminError(w, http.StatusBadRequest, err.Error())
		return
	}
	user := strings.TrimSpace(req.SSHUser)
	if user == "" {
		user = "root"
	}
	fp := strings.TrimSpace(req.HostKeyFP)
	if !provision.ValidFingerprint(fp) {
		writeAdminError(w, http.StatusBadRequest, "Сначала проверьте подключение и подтвердите отпечаток ключа сервера (SHA256:…)")
		return
	}
	if strings.TrimSpace(req.SSHPassword) == "" {
		writeAdminError(w, http.StatusBadRequest, "Нужен пароль root: он хранится зашифрованно и нужен, пока на сервер не поставлен ключ Hub")
		return
	}
	enc, err := encryptSSHPassword(req.SSHPassword)
	if err != nil {
		writeAdminError(w, http.StatusServiceUnavailable, "На Hub не задан SSH_ENCRYPTION_KEY — пароль нельзя сохранить зашифрованно. Выполните go run ./cmd/deploy --ensure-hub-provisioning")
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = "Выход " + country
	}
	// A fast answer before the probe; the unique index (uq_servers_ip_address) decides a race.
	var exists bool
	if err := db.DB.QueryRowContext(r.Context(), nodeIPExistsQuery, ip).Scan(&exists); err != nil {
		writeAdminError(w, http.StatusInternalServerError, "Не удалось проверить список серверов")
		return
	}
	if exists {
		writeAdminError(w, http.StatusConflict, nodeIPExistsMsg)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), probeTimeout)
	defer cancel()
	res, ok := probeNode(w, ctx, ip, port)
	if !ok {
		return
	}
	key, ok := findHostKey(res, fp)
	if !ok {
		writeAdminError(w, http.StatusConflict, fmt.Sprintf("Сервер сейчас предъявляет другие ключи (%s), а подтверждён %s. Проверьте подключение заново и сверьте отпечаток с панелью хостера.", presentedKeys(res), fp))
		return
	}
	var id int
	if err := db.DB.QueryRowContext(r.Context(), insertNodeQuery, name, req.Role, req.Role == "exit", ip, port, user,
		enc, country, key.Key, key.Fingerprint).Scan(&id); err != nil {
		if isUniqueViolation(err) {
			writeAdminError(w, http.StatusConflict, nodeIPExistsMsg)
			return
		}
		writeAdminError(w, http.StatusInternalServerError, "Не удалось сохранить сервер")
		return
	}
	WriteAuditLog(r, "create_node", "server", strconv.Itoa(id), map[string]string{"name": name, "ip": ip, "fingerprint": fp, "key_type": key.Type})
	writeAdminJSON(w, http.StatusCreated, map[string]any{"id": id})
}

const confirmHostKeyQuery = `UPDATE servers SET ssh_host_key = $2, ssh_host_key_fp = $3, ssh_host_key_confirmed_at = NOW(), updated_at = NOW() WHERE id = $1`

// ConfirmHostKeyHandler confirms one of the keys the node presents right now (any type: exit 2's
// migrated key is ECDSA); its blob is stored too, so DialNode negotiates exactly that key.
func ConfirmHostKeyHandler(w http.ResponseWriter, r *http.Request) {
	id, ok := parseLastPathSegmentBeforeFinal(r.URL.Path, "host-key")
	if !ok {
		writeAdminError(w, http.StatusBadRequest, "Неверный id сервера")
		return
	}
	var req struct {
		Fingerprint string `json:"fingerprint"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || !provision.ValidFingerprint(strings.TrimSpace(req.Fingerprint)) {
		writeAdminError(w, http.StatusBadRequest, "Отпечаток должен выглядеть как SHA256:… (ssh-keygen -lf)")
		return
	}
	if !provisionReady(w) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), probeTimeout)
	defer cancel()
	n, ok := loadNodeMeta(w, ctx, id)
	if !ok {
		return
	}
	ip, port, err := validateNodeAddr(n.IP, n.SSHPort)
	if err != nil {
		writeAdminError(w, http.StatusBadRequest, err.Error())
		return
	}
	live, ok := probeNode(w, ctx, ip, port)
	if !ok {
		return
	}
	fp := strings.TrimSpace(req.Fingerprint)
	key, ok := findHostKey(live, fp)
	if !ok {
		writeAdminError(w, http.StatusConflict, fmt.Sprintf("Сервер сейчас предъявляет другие ключи (%s). Сверьте с панелью хостера.", presentedKeys(live)))
		return
	}
	if _, err := db.DB.ExecContext(ctx, confirmHostKeyQuery, id, key.Key, key.Fingerprint); err != nil {
		writeAdminError(w, http.StatusInternalServerError, "Не удалось сохранить отпечаток")
		return
	}
	WriteAuditLog(r, "confirm_host_key", "server", strconv.Itoa(id), map[string]string{"fingerprint": fp, "key_type": key.Type})
	writeAdminJSON(w, http.StatusOK, map[string]any{"fingerprint": fp, "key_type": key.Type, "confirmed": true})
}

// PreflightNodeHandler runs the read-only checks synchronously (the recipe's first step).
func PreflightNodeHandler(w http.ResponseWriter, r *http.Request) {
	id, ok := parseLastPathSegmentBeforeFinal(r.URL.Path, "preflight")
	if !ok {
		writeAdminError(w, http.StatusBadRequest, "Неверный id сервера")
		return
	}
	if !provisionReady(w) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	rm, err := nodeRemote(ctx, id)
	if err != nil {
		writeNodeAccessError(w, err)
		return
	}
	defer provision.Close(rm)
	checks, err := provision.Preflight(ctx, rm, time.Now())
	if err != nil {
		writeAdminError(w, http.StatusBadGateway, "Проверки не выполнились: "+err.Error())
		return
	}
	all := true
	for _, c := range checks {
		all = all && c.OK
	}
	writeAdminJSON(w, http.StatusOK, map[string]any{"ok": all, "checks": checks})
}

type provisionPayload struct {
	MTProxy    *bool  `json:"mtproxy"`
	PlanIDs    *[]int `json:"plan_ids"`
	LoadWeight *int   `json:"load_weight"`
}

const (
	mtproxyEnableQuery  = `UPDATE servers SET mtproto_status = 'unknown', updated_at = NOW() WHERE id = $1 AND mtproto_status = 'disabled'`
	mtproxyDisableQuery = `UPDATE servers SET mtproto_status = 'disabled', mtproto_installed_at = NULL, updated_at = NOW() WHERE id = $1`
	loadWeightQuery     = `UPDATE servers SET load_weight = $2, updated_at = NOW() WHERE id = $1`
)

// saveNodeOptionsDB stores the wizard's choices before the job (they must survive a resume, and
// the runner reads mtproto_status when it loads the node: MTProxy on turns 'disabled' back into
// 'unknown' so the recipe installs it; off makes it 'disabled' — no MTProxy options, the
// reconciler skips the node).
func saveNodeOptionsDB(ctx context.Context, id int, o provisionPayload) error {
	if o.PlanIDs != nil {
		if err := provision.BindPlans(ctx, db.DB, id, *o.PlanIDs); err != nil {
			return err
		}
	}
	if o.LoadWeight != nil {
		if _, err := db.DB.ExecContext(ctx, loadWeightQuery, id, *o.LoadWeight); err != nil {
			return err
		}
	}
	if o.MTProxy != nil {
		q := mtproxyEnableQuery
		if !*o.MTProxy {
			q = mtproxyDisableQuery
		}
		if _, err := db.DB.ExecContext(ctx, q, id); err != nil {
			return err
		}
	}
	return nil
}

// ProvisionNodeHandler starts «Настроить» (provision) or, for a working node, «Переустановить».
func ProvisionNodeHandler(w http.ResponseWriter, r *http.Request) {
	id, ok := parseLastPathSegmentBeforeFinal(r.URL.Path, "provision")
	if !ok {
		writeAdminError(w, http.StatusBadRequest, "Неверный id сервера")
		return
	}
	var req provisionPayload
	if err := decodeOptional(r, &req); err != nil {
		writeAdminError(w, http.StatusBadRequest, "Неверное тело запроса")
		return
	}
	if req.LoadWeight != nil && (*req.LoadWeight < 1 || *req.LoadWeight > 10) {
		writeAdminError(w, http.StatusBadRequest, "Приоритет — от 1 до 10")
		return
	}
	if req.PlanIDs != nil && !validPlanIDs(*req.PlanIDs) {
		writeAdminError(w, http.StatusBadRequest, planIDsPositiveMsg)
		return
	}
	if !provisionReady(w) {
		return
	}
	n, ok := loadNodeMeta(w, r.Context(), id)
	if !ok {
		return
	}
	if !n.HostKeyConfirmed {
		writeAdminError(w, http.StatusConflict, hostKeyFirstMsg)
		return
	}
	// A running job keeps the node: 409 before the options are touched or a sync is requested
	// (Runner.Start's unique index still decides a race).
	if refuseIfJobActive(w, r.Context(), id) {
		return
	}
	kind := provision.KindProvision
	if n.Lifecycle == "online" {
		kind = provision.KindReprovision
	}
	if err := saveNodeOptions(r.Context(), id, req); err != nil {
		if unknownPlan(err) {
			writeAdminError(w, http.StatusBadRequest, unknownPlanMsg)
			return
		}
		log.Printf("[nodes] node %d options: %v", id, err)
		writeAdminError(w, http.StatusInternalServerError, "Не удалось сохранить настройки ноды")
		return
	}
	if n.Lifecycle == "online" && (req.PlanIDs != nil || req.LoadWeight != nil) {
		biz.RequestSync() // a working exit's plans / priority live in the xray config
	}
	startJob(w, r, id, kind)
}

func startJob(w http.ResponseWriter, r *http.Request, id int, kind string) {
	jobID, err := NodeProvisioner.Start(r.Context(), id, kind)
	if err != nil {
		writeJobError(w, err)
		return
	}
	WriteAuditLog(r, "node_job_"+kind, "server", strconv.Itoa(id), map[string]any{"job_id": jobID})
	writeAdminJSON(w, http.StatusAccepted, map[string]any{"job_id": jobID, "kind": kind})
}

// VerifyNodeHandler starts «Проверить»: Verify of every step, nothing changes on the node.
func VerifyNodeHandler(w http.ResponseWriter, r *http.Request) {
	id, ok := parseLastPathSegmentBeforeFinal(r.URL.Path, "verify")
	if !ok {
		writeAdminError(w, http.StatusBadRequest, "Неверный id сервера")
		return
	}
	if !provisionReady(w) {
		return
	}
	n, ok := loadNodeMeta(w, r.Context(), id)
	if !ok {
		return
	}
	if !n.HostKeyConfirmed {
		writeAdminError(w, http.StatusConflict, hostKeyFirstMsg)
		return
	}
	startJob(w, r, id, provision.KindVerify)
}

type confirmPayload struct {
	ConfirmName string `json:"confirm_name"`
}

// confirmedNode loads the node (no credentials) and checks the typed name (ConfirmDanger in the
// UI, enforced here too); suffix "" = the id is the last path segment.
func confirmedNode(w http.ResponseWriter, r *http.Request, suffix string) (provision.Node, bool) {
	var id int
	var ok bool
	if suffix == "" {
		id, ok = parseLastPathInt(r.URL.Path)
	} else {
		id, ok = parseLastPathSegmentBeforeFinal(r.URL.Path, suffix)
	}
	var req confirmPayload
	if !ok || decodeOptional(r, &req) != nil {
		writeAdminError(w, http.StatusBadRequest, "Неверный запрос")
		return provision.Node{}, false
	}
	if !provisionReady(w) {
		return provision.Node{}, false
	}
	n, ok := loadNodeMeta(w, r.Context(), id)
	if !ok {
		return provision.Node{}, false
	}
	if strings.TrimSpace(req.ConfirmName) != n.Name {
		writeAdminError(w, http.StatusBadRequest, confirmNameMsg)
		return provision.Node{}, false
	}
	return n, true
}

// DeprovisionNodeHandler starts «Отключить»: out of routing first, then the Hub side, then the
// node (best effort); the row stays offline. An unconfirmed key does not block it.
func DeprovisionNodeHandler(w http.ResponseWriter, r *http.Request) {
	if n, ok := confirmedNode(w, r, "deprovision"); ok {
		startJob(w, r, n.ID, provision.KindDeprovision)
	}
}

const deleteNodeQuery = `DELETE FROM servers WHERE id = $1 AND lifecycle_state IN ('pending', 'offline')
   AND NOT EXISTS (SELECT 1 FROM provision_jobs WHERE server_id = $1 AND status IN ('queued', 'running'))`

// failedNodeDeleteMsg: a failed setup may have left the Hub's tunnel client (hysteria2-client-<id>,
// its path marker) and services on the node; «Отключить» (the deprovision job, which works from
// failed) removes the Hub side and parks the node, persisted and resumable — then the row goes.
const failedNodeDeleteMsg = "Сначала отключите ноду («Отключить»): после неудачной настройки на Hub мог остаться клиент туннеля, " +
	"а на сервере — запущенные сервисы; отключение их уберёт, затем запись можно удалить"

// DeleteServerHandler removes the row of a node with nothing left behind: pending (never set up)
// or offline (taken out by the deprovision job); its jobs go with it (ON DELETE CASCADE). A failed
// node is refused (failedNodeDeleteMsg) rather than cleaned up here: the deprovision job already
// does the Hub side and the node side, step by step and resumable — a second, synchronous copy of
// the Hub-side cleanup in this handler could only be less complete. Hub-only: the node itself is
// not touched (it keeps its agent guard).
func DeleteServerHandler(w http.ResponseWriter, r *http.Request) {
	n, ok := confirmedNode(w, r, "")
	if !ok {
		return
	}
	switch n.Lifecycle {
	case "pending", "offline":
	case "failed":
		writeAdminError(w, http.StatusConflict, failedNodeDeleteMsg)
		return
	default:
		writeAdminError(w, http.StatusConflict, "Удалить можно только отключённую или ещё не настроенную ноду — сначала «Отключить»")
		return
	}
	res, err := db.DB.ExecContext(r.Context(), deleteNodeQuery, n.ID)
	if err != nil {
		writeAdminError(w, http.StatusInternalServerError, "Не удалось удалить сервер")
		return
	}
	if cnt, _ := res.RowsAffected(); cnt == 0 {
		writeAdminError(w, http.StatusConflict, "Нода изменилась или для неё выполняется задача — обновите страницу; удалить можно только отключённую или ещё не настроенную ноду")
		return
	}
	biz.BumpSubscriptionVersion()
	WriteAuditLog(r, "delete_server", "server", strconv.Itoa(n.ID), nil)
	writeAdminJSON(w, http.StatusOK, map[string]string{"message": "Нода удалена"})
}

type routingPayload struct {
	PlanIDs         *[]int `json:"plan_ids"`
	LoadWeight      int    `json:"load_weight"`
	FallbackEnabled *bool  `json:"tunnel_fallback_enabled"`
}

const routingQuery = `UPDATE servers SET load_weight = $2, tunnel_fallback_enabled = COALESCE($3, tunnel_fallback_enabled), updated_at = NOW() WHERE id = $1`

// NodeRoutingHandler sets which plans route through the node, its priority and the TCP fallback switch.
func NodeRoutingHandler(w http.ResponseWriter, r *http.Request) {
	id, ok := parseLastPathSegmentBeforeFinal(r.URL.Path, "routing")
	var req routingPayload
	if !ok || json.NewDecoder(r.Body).Decode(&req) != nil {
		writeAdminError(w, http.StatusBadRequest, "Неверный запрос")
		return
	}
	if req.LoadWeight < 1 || req.LoadWeight > 10 || req.PlanIDs == nil {
		writeAdminError(w, http.StatusBadRequest, "Нужны plan_ids (пустой список — только общий пул) и приоритет от 1 до 10")
		return
	}
	if !validPlanIDs(*req.PlanIDs) {
		writeAdminError(w, http.StatusBadRequest, planIDsPositiveMsg)
		return
	}
	res, err := db.DB.ExecContext(r.Context(), routingQuery, id, req.LoadWeight, req.FallbackEnabled)
	if err != nil {
		writeAdminError(w, http.StatusInternalServerError, "Не удалось обновить ноду")
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		writeAdminError(w, http.StatusNotFound, "Нода не найдена")
		return
	}
	if err := provision.BindPlans(r.Context(), db.DB, id, *req.PlanIDs); err != nil {
		if unknownPlan(err) {
			writeAdminError(w, http.StatusBadRequest, unknownPlanMsg)
			return
		}
		writeAdminError(w, http.StatusInternalServerError, "Не удалось сохранить тарифы ноды")
		return
	}
	biz.RequestSync()
	WriteAuditLog(r, "node_routing", "server", strconv.Itoa(id), req)
	writeAdminJSON(w, http.StatusOK, map[string]string{"message": "Сохранено — маршрутизация обновится в течение нескольких секунд"})
}

// GetJobHandler: one job with its steps (output tails redacted by the runner before they were stored).
func GetJobHandler(w http.ResponseWriter, r *http.Request) {
	id, ok := parseLastPathInt(r.URL.Path)
	if !ok {
		writeAdminError(w, http.StatusBadRequest, "Неверный id задачи")
		return
	}
	if !provisionReady(w) {
		return
	}
	j, err := NodeJobs.JobView(r.Context(), int64(id))
	if errors.Is(err, sql.ErrNoRows) {
		writeAdminError(w, http.StatusNotFound, "Задача не найдена")
		return
	}
	if err != nil {
		writeAdminError(w, http.StatusInternalServerError, "Не удалось загрузить задачу")
		return
	}
	writeAdminJSON(w, http.StatusOK, j)
}

// ListJobsHandler: the node's latest jobs, newest first, without steps.
func ListJobsHandler(w http.ResponseWriter, r *http.Request) {
	sid, err := strconv.Atoi(r.URL.Query().Get("server_id"))
	if err != nil || sid < 1 {
		writeAdminError(w, http.StatusBadRequest, "server_id обязателен")
		return
	}
	if !provisionReady(w) {
		return
	}
	jobs, err := NodeJobs.ListJobs(r.Context(), sid, 20)
	if err != nil {
		writeAdminError(w, http.StatusInternalServerError, "Не удалось загрузить задачи")
		return
	}
	if jobs == nil {
		jobs = []provision.JobView{}
	}
	writeAdminJSON(w, http.StatusOK, map[string]any{"items": jobs})
}

// ResumeJobHandler: «Продолжить» an interrupted or failed job from its first step.
func ResumeJobHandler(w http.ResponseWriter, r *http.Request) {
	id, ok := parseLastPathSegmentBeforeFinal(r.URL.Path, "resume")
	if !ok {
		writeAdminError(w, http.StatusBadRequest, "Неверный id задачи")
		return
	}
	if !provisionReady(w) {
		return
	}
	j, err := NodeJobs.JobView(r.Context(), int64(id))
	if errors.Is(err, sql.ErrNoRows) {
		writeAdminError(w, http.StatusNotFound, "Задача не найдена")
		return
	}
	if err != nil {
		log.Printf("[nodes] job %d: %v", id, err)
		writeAdminError(w, http.StatusInternalServerError, "Не удалось загрузить задачу")
		return
	}
	// A job that connects would only fail again on its first node step without a confirmed key.
	if j.Kind != provision.KindDeprovision {
		n, ok := loadNodeMeta(w, r.Context(), j.ServerID)
		if !ok {
			return
		}
		if !n.HostKeyConfirmed {
			writeAdminError(w, http.StatusConflict, hostKeyFirstMsg)
			return
		}
	}
	if err := NodeProvisioner.Resume(r.Context(), int64(id)); err != nil {
		writeJobError(w, err)
		return
	}
	WriteAuditLog(r, "node_job_resume", "provision_job", strconv.Itoa(id), nil)
	writeAdminJSON(w, http.StatusAccepted, map[string]any{"job_id": id})
}

// HubKeyHandler: the Hub's public SSH key (to pre-install on a node) — never the private one.
func HubKeyHandler(w http.ResponseWriter, r *http.Request) {
	if HubPublicKey == nil {
		writeAdminError(w, http.StatusServiceUnavailable, notInitializedMsg)
		return
	}
	pub, err := HubPublicKey(r.Context())
	if err != nil {
		writeAdminError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeAdminJSON(w, http.StatusOK, map[string]string{"public_key": pub, "fingerprint": provision.HubKeyFingerprint(pub)})
}

// listNodesQuery: the «Ноды» page. Public columns only (ssh_host_key is a host's PUBLIC key,
// read to name its type); the path columns are the latest result of the jobs' path checks —
// path_hy 'skipped' = the main tunnel did not answer while the TCP path decided (not an error).
const listNodesQuery = `
SELECT s.id, COALESCE(s.name, ''), s.role, COALESCE(s.country_code, ''), s.ip_address, s.lifecycle_state,
       COALESCE(s.load_weight, 1), COALESCE(s.tunnel_status, 'unknown'), s.tunnel_fallback_enabled, s.mtproto_status,
       COALESCE(s.ssh_host_key_fp, ''), COALESCE(s.ssh_host_key, ''), s.ssh_host_key_confirmed_at IS NOT NULL,
       COALESCE((SELECT array_agg(ps.plan_id ORDER BY ps.plan_id) FROM plan_servers ps WHERE ps.server_id = s.id), '{}'),
       lj.id, lj.kind, lj.status, hy.status, hy.finished_at, tcp.status, tcp.finished_at
  FROM servers s
  LEFT JOIN LATERAL (SELECT id, kind, status FROM provision_jobs WHERE server_id = s.id ORDER BY id DESC LIMIT 1) lj ON TRUE
  LEFT JOIN LATERAL (SELECT st.status, st.finished_at FROM provision_steps st JOIN provision_jobs j ON j.id = st.job_id
       WHERE j.server_id = s.id AND st.name = 'path_hy' AND st.status IN ('ok', 'done', 'skipped', 'failed', 'drift', 'rolled_back')
       ORDER BY st.finished_at DESC NULLS LAST, st.id DESC LIMIT 1) hy ON TRUE
  LEFT JOIN LATERAL (SELECT st.status, st.finished_at FROM provision_steps st JOIN provision_jobs j ON j.id = st.job_id
       WHERE j.server_id = s.id AND st.name = 'path_tcp' AND st.status IN ('ok', 'done', 'failed', 'drift', 'rolled_back')
       ORDER BY st.finished_at DESC NULLS LAST, st.id DESC LIMIT 1) tcp ON TRUE
 WHERE ($1::text = '' OR s.role = $1::text)
 ORDER BY s.id`

// Path sources: the live tunnel monitor (phase 1) or the last job's path check.
const (
	pathSourceMonitor = "monitor"
	pathSourceJob     = "job"
)

// nodePath is one tunnel path of a node. Status: from the monitor healthy|unhealthy|unknown|absent;
// from a job ok|done|failed|drift|rolled_back, and for hy also skipped (the main tunnel did not
// answer, the exit went online on its TCP path — a warning, not an error); "" = never checked.
type nodePath struct {
	Status    string     `json:"status"`
	Source    string     `json:"source"`
	CheckedAt *time.Time `json:"checked_at"`
	RTTms     int        `json:"rtt_ms,omitempty"`
	Error     string     `json:"error,omitempty"`
}

type nodeJobRef struct {
	ID     int64  `json:"id"`
	Kind   string `json:"kind"`
	Status string `json:"status"`
}

type nodeHostKey struct {
	Fingerprint string `json:"fingerprint"`
	KeyType     string `json:"key_type"`
	Confirmed   bool   `json:"confirmed"`
}

type nodeItem struct {
	ID              int         `json:"id"`
	Name            string      `json:"name"`
	Role            string      `json:"role"`
	CountryCode     string      `json:"country_code"`
	IPAddress       string      `json:"ip_address"`
	Lifecycle       string      `json:"lifecycle_state"`
	LoadWeight      int         `json:"load_weight"`
	TunnelStatus    string      `json:"tunnel_status"`
	FallbackEnabled bool        `json:"tunnel_fallback_enabled"`
	MTProtoStatus   string      `json:"mtproto_status"`
	HostKey         nodeHostKey `json:"host_key"`
	PlanIDs         []int       `json:"plan_ids"`
	LastJob         *nodeJobRef `json:"last_job"`
	Paths           struct {
		Hy  nodePath `json:"hy"`
		TCP nodePath `json:"tcp"`
	} `json:"paths"`
}

func jobPath(status sql.NullString, at sql.NullTime) nodePath {
	if !status.Valid {
		return nodePath{}
	}
	p := nodePath{Status: status.String, Source: pathSourceJob}
	if at.Valid {
		t := at.Time
		p.CheckedAt = &t
	}
	return p
}

// ListNodesHandler — the «Ноды» page: truthful state, both tunnel paths, never a secret.
func ListNodesHandler(w http.ResponseWriter, r *http.Request) {
	role := r.URL.Query().Get("role")
	if role != "" && role != "exit" && role != "relay" {
		writeAdminError(w, http.StatusBadRequest, "role: exit или relay")
		return
	}
	rows, err := db.DB.QueryContext(r.Context(), listNodesQuery, role)
	if err != nil {
		writeAdminError(w, http.StatusInternalServerError, "Не удалось загрузить ноды")
		return
	}
	defer rows.Close()
	items := []nodeItem{}
	for rows.Next() {
		var it nodeItem
		var hostKeyBlob string
		var plans pq.Int64Array
		var jobID sql.NullInt64
		var jobKind, jobStatus, hySt, tcpSt sql.NullString
		var hyAt, tcpAt sql.NullTime
		if err := rows.Scan(&it.ID, &it.Name, &it.Role, &it.CountryCode, &it.IPAddress, &it.Lifecycle,
			&it.LoadWeight, &it.TunnelStatus, &it.FallbackEnabled, &it.MTProtoStatus,
			&it.HostKey.Fingerprint, &hostKeyBlob, &it.HostKey.Confirmed,
			&plans, &jobID, &jobKind, &jobStatus, &hySt, &hyAt, &tcpSt, &tcpAt); err != nil {
			writeAdminError(w, http.StatusInternalServerError, "Не удалось прочитать ноду")
			return
		}
		it.HostKey.KeyType = provision.StoredHostKeyType(hostKeyBlob, it.HostKey.Fingerprint)
		it.PlanIDs = make([]int, len(plans))
		for i, p := range plans {
			it.PlanIDs[i] = int(p)
		}
		if jobID.Valid {
			it.LastJob = &nodeJobRef{ID: jobID.Int64, Kind: jobKind.String, Status: jobStatus.String}
		}
		it.Paths.Hy, it.Paths.TCP = jobPath(hySt, hyAt), jobPath(tcpSt, tcpAt)
		// The live tunnel monitor (biz.GetExitPaths) beats the last job's path check; the job's
		// result is shown only until the monitor has seen the exit.
		if p, ok := biz.GetExitPaths(it.ID); ok && it.Role == "exit" {
			at := p.CheckedAt
			it.Paths.Hy = nodePath{Status: p.Hy, Source: pathSourceMonitor, CheckedAt: &at, RTTms: p.HyRTTms, Error: p.HyError}
			it.Paths.TCP = nodePath{Status: p.VR, Source: pathSourceMonitor, CheckedAt: &at, RTTms: p.VRRTTms, Error: p.VRError}
		}
		items = append(items, it)
	}
	if err := rows.Err(); err != nil {
		writeAdminError(w, http.StatusInternalServerError, "Не удалось загрузить ноды")
		return
	}
	writeAdminJSON(w, http.StatusOK, map[string]any{"items": items, "total": len(items)})
}
