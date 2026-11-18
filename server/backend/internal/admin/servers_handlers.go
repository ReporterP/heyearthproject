package admin

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/heyearth/backend/internal/biz"
	"github.com/heyearth/backend/internal/config"
	"github.com/heyearth/backend/internal/crypto"
	"github.com/heyearth/backend/internal/db"
	pbnode "github.com/heyearth/backend/internal/pb/node_agent_v1"
	"github.com/heyearth/backend/internal/provision"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

const (
	// sshPasswordKeyMissingMsg: a password cannot be stored without SSH_ENCRYPTION_KEY (503 — the
	// Hub is not set up for it; the same answer for «Добавить ноду» and a password change).
	sshPasswordKeyMissingMsg = "На Hub не задан SSH_ENCRYPTION_KEY — пароль нельзя сохранить зашифрованно. Выполните go run ./cmd/deploy --ensure-hub-provisioning"
)

// serverLifecycleStates are the values of servers.lifecycle_state (migration 000040).
var serverLifecycleStates = map[string]bool{
	"pending": true, "provisioning": true, "online": true, "failed": true, "deprovisioning": true, "offline": true,
}

// serverListWhere builds the servers list filter: search (name/IP), status (node-agent
// availability), lifecycle_state and country (ISO alpha-2). A bad lifecycle state or country is a
// 400, never a silently ignored filter.
func serverListWhere(q url.Values) (string, []any, error) {
	where, args := "1=1", []any{}
	add := func(cond string, v any) {
		args = append(args, v)
		where += " AND " + fmt.Sprintf(cond, len(args))
	}
	if s := strings.TrimSpace(q.Get("search")); s != "" {
		add("(s.name ILIKE $%[1]d OR s.ip_address ILIKE $%[1]d)", "%"+s+"%")
	}
	if s := strings.TrimSpace(q.Get("status")); s != "" {
		add("s.status = $%d", s)
	}
	if s := strings.TrimSpace(q.Get("lifecycle_state")); s != "" {
		if !serverLifecycleStates[s] {
			return "", nil, fmt.Errorf("неизвестное состояние ноды %q", s)
		}
		add("s.lifecycle_state = $%d", s)
	}
	if s := strings.TrimSpace(q.Get("country")); s != "" {
		if len(s) != 2 || validateCountryCode(s) != nil {
			return "", nil, fmt.Errorf("страна — двухбуквенный код ISO, например NL")
		}
		add("UPPER(s.country_code) = $%d", strings.ToUpper(s))
	}
	return where, args, nil
}

// redactServer drops every secret before a server leaves the API (Review Focus 5): the SSH
// password, the exit's private Reality key, the tunnel UUID (the Hub's VLESS credential on the
// exit) and the Hysteria tunnel password. The browser only learns whether they are set; flags a
// loader already set from the stored columns (list SQL, loadServerByID) are kept.
func redactServer(s serverResponse) serverResponse {
	s.HasSSHPassword = s.HasSSHPassword || strings.TrimSpace(s.SSHPassword) != ""
	s.HasTunnelCreds = s.HasTunnelCreds || s.TunnelUUID != "" || s.TunnelSSPass != "" || s.TunnelPrivateKey != ""
	s.SSHPassword, s.TunnelUUID, s.TunnelPrivateKey, s.TunnelSSPass = "", "", "", ""
	return s
}

// validateServerUpdate: weight 1..10 (0 = keep), tunnel protocol hysteria2 or vless (the only ones
// the xray generator builds since phase 1).
func validateServerUpdate(req serverPayload) error {
	if req.LoadWeight != 0 && (req.LoadWeight < 1 || req.LoadWeight > 10) {
		return fmt.Errorf("приоритет должен быть от 1 до 10")
	}
	switch req.TunnelProtocol {
	case "", "hysteria2", "vless":
		return nil
	}
	return fmt.Errorf("протокол туннеля %q не поддерживается: только hysteria2 или vless", req.TunnelProtocol)
}

// serverRoutingChanged: does the edit (req after the fallback to stored values, weight resolved)
// change anything the generated xray/hysteria configs are built from? Name, SSH access and
// country do not; address, status, tunnel protocol/port/credentials/SNI and weight do.
func serverRoutingChanged(old serverResponse, req serverPayload, weight int) bool {
	return req.IPAddress != old.IPAddress || req.Status != old.Status ||
		req.TunnelProtocol != old.TunnelProtocol || req.TunnelPort != old.TunnelPort ||
		req.TunnelUUID != old.TunnelUUID || req.TunnelPublicKey != old.TunnelPublicKey ||
		req.TunnelShortID != old.TunnelShortID || req.TunnelSNI != old.TunnelSNI ||
		req.TunnelSSPass != old.TunnelSSPass || weight != old.LoadWeight
}

func GetServersHandler(w http.ResponseWriter, r *http.Request) {
	p := parsePagination(r)

	where, args, err := serverListWhere(r.URL.Query())
	if err != nil {
		writeAdminError(w, http.StatusBadRequest, err.Error())
		return
	}
	argN := len(args) + 1

	var total int
	if err := db.DB.QueryRow("SELECT COUNT(*) FROM servers s WHERE "+where, args...).Scan(&total); err != nil {
		writeAdminError(w, http.StatusInternalServerError, "Не удалось посчитать серверы")
		return
	}

	// Sortable columns whitelist (Phase 1 Task 3).
	// last_check is mapped to the underlying last_tunnel_check column.
	allowedSort := map[string]string{
		"id":              "s.id",
		"country_code":    "s.country_code",
		"status":          "s.status",
		"last_check":      "s.last_tunnel_check",
		"lifecycle_state": "s.lifecycle_state",
		"load_weight":     "s.load_weight",
	}
	sortCol := ""
	if v := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("sort"))); v != "" {
		if c, ok := allowedSort[v]; ok {
			sortCol = c
		}
	}
	dir := "DESC"
	if d := strings.ToLower(r.URL.Query().Get("dir")); d == "asc" {
		dir = "ASC"
	} else if d == "desc" {
		dir = "DESC"
	}
	orderBy := "COALESCE(s.updated_at, CURRENT_TIMESTAMP) DESC, s.id DESC"
	if sortCol != "" {
		orderBy = sortCol + " " + dir
	}

	query := fmt.Sprintf(`
		SELECT s.id, s.name, s.ip_address, s.ssh_port, s.ssh_user, s.country_code, s.status,
		       s.load_metrics, COALESCE(s.updated_at, CURRENT_TIMESTAMP),
		       s.metrics_synced_at,
		       s.is_exit_node, s.tunnel_status, s.tunnel_protocol, s.tunnel_port,
		       COALESCE(s.tunnel_error, ''), COALESCE(s.load_weight, 1), s.lifecycle_state, s.mtproto_status,
		       (COALESCE(TRIM(s.ssh_password), '') <> '' OR COALESCE(octet_length(s.ssh_password_enc), 0) > 0),
		       (COALESCE(s.tunnel_uuid, '') <> '' OR COALESCE(s.tunnel_ss_password, '') <> '' OR COALESCE(s.tunnel_private_key, '') <> '')
		FROM servers s
		WHERE %s
		ORDER BY %s
		LIMIT $%d OFFSET $%d
	`, where, orderBy, argN, argN+1)
	args = append(args, p.PageSize, p.Offset())

	rows, err := db.DB.Query(query, args...)
	if err != nil {
		writeAdminError(w, http.StatusInternalServerError, "Не удалось загрузить список серверов")
		return
	}
	defer rows.Close()

	var servers []serverResponse
	for rows.Next() {
		var s serverResponse
		var updatedAt time.Time
		var metricsSyncedAt sql.NullTime
		if err := rows.Scan(&s.ID, &s.Name, &s.IPAddress, &s.SSHPort, &s.SSHUser, &s.CountryCode, &s.Status, &s.LoadMetrics, &updatedAt, &metricsSyncedAt, &s.IsExitNode, &s.TunnelStatus, &s.TunnelProtocol, &s.TunnelPort, &s.TunnelError, &s.LoadWeight, &s.LifecycleState, &s.MTProtoStatus, &s.HasSSHPassword, &s.HasTunnelCreds); err != nil {
			writeAdminError(w, http.StatusInternalServerError, "Не удалось прочитать сервер из базы")
			return
		}
		if len(s.LoadMetrics) == 0 {
			s.LoadMetrics = json.RawMessage(`{}`)
		}
		s.UpdatedAt = updatedAt.Format(time.RFC3339)
		if metricsSyncedAt.Valid {
			s.MetricsSyncedAt = metricsSyncedAt.Time.Format(time.RFC3339)
		}
		// The list never reads the secrets themselves, only whether they are set (SQL above).
		servers = append(servers, redactServer(s))
	}
	writeAdminJSON(w, http.StatusOK, PagedResponse{Items: servers, Total: total, Page: p.Page, PageSize: p.PageSize})
}

func GetServerDetailHandler(w http.ResponseWriter, r *http.Request) {
	serverID, ok := parseLastPathInt(r.URL.Path)
	if !ok {
		writeAdminError(w, http.StatusBadRequest, "Неверный id сервера")
		return
	}
	server, err := loadServerByID(serverID, true)
	if err == sql.ErrNoRows {
		writeAdminError(w, http.StatusNotFound, "Сервер не найден")
		return
	}
	if err != nil {
		writeAdminError(w, http.StatusInternalServerError, "Не удалось загрузить сервер")
		return
	}
	if p, ok := biz.GetExitPaths(server.ID); ok {
		server.Paths = &p
	}
	writeAdminJSON(w, http.StatusOK, redactServer(server))
}

// validateCountryCode allows empty (for legacy rows) or exactly two ASCII
// letters (ISO 3166-1 alpha-2). Anything else is rejected so we don't end up
// with city names or free-form text in the column.
func validateCountryCode(code string) error {
	c := strings.TrimSpace(code)
	if c == "" {
		return nil
	}
	if len(c) != 2 {
		return fmt.Errorf("код страны — пусто или две латинские буквы ISO (например NL, LV)")
	}
	for _, r := range c {
		if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')) {
			return fmt.Errorf("код страны должен состоять только из латинских букв")
		}
	}
	return nil
}

// clearHostKeyQuery drops a node's host key and its confirmation (its address changed).
const clearHostKeyQuery = `UPDATE servers SET ssh_host_key = NULL, ssh_host_key_fp = NULL, ssh_host_key_confirmed_at = NULL, updated_at = NOW() WHERE id = $1`

// sameNodeAddr: the same host (IPs compared as addresses: "::ffff:1.2.3.4" is 1.2.3.4) and port.
func sameNodeAddr(ipA string, portA int, ipB string, portB int) bool {
	if portA != portB {
		return false
	}
	a, b := net.ParseIP(strings.TrimSpace(ipA)), net.ParseIP(strings.TrimSpace(ipB))
	if a == nil || b == nil {
		return strings.TrimSpace(ipA) == strings.TrimSpace(ipB)
	}
	return a.Equal(b)
}

func UpdateServerHandler(w http.ResponseWriter, r *http.Request) {
	serverID, ok := parseLastPathInt(r.URL.Path)
	if !ok {
		writeAdminError(w, http.StatusBadRequest, "Неверный id сервера")
		return
	}
	var req serverPayload
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeAdminError(w, http.StatusBadRequest, "Неверное тело запроса")
		return
	}
	if err := validateServerUpdate(req); err != nil {
		writeAdminError(w, http.StatusBadRequest, err.Error())
		return
	}
	// The stored SSH password is not needed: it is never rewritten from its decrypted value.
	existing, err := loadServerByID(serverID, false)
	if err == sql.ErrNoRows {
		writeAdminError(w, http.StatusNotFound, "Сервер не найден")
		return
	}
	if err != nil {
		writeAdminError(w, http.StatusInternalServerError, "Не удалось загрузить сервер")
		return
	}

	if strings.TrimSpace(req.Name) == "" {
		req.Name = existing.Name
	}
	if strings.TrimSpace(req.IPAddress) == "" {
		req.IPAddress = existing.IPAddress
	}
	if req.SSHPort == 0 {
		req.SSHPort = existing.SSHPort
	}
	// A changed address is checked and normalized like a new node's (validateNodeAddr), and drops
	// the host key confirmation: another machine may answer there, so the owner confirms again.
	addrChanged := false
	if sameNodeAddr(existing.IPAddress, existing.SSHPort, req.IPAddress, req.SSHPort) {
		req.IPAddress, req.SSHPort = existing.IPAddress, existing.SSHPort
	} else {
		ip, port, err := validateNodeAddr(req.IPAddress, req.SSHPort)
		if err != nil {
			writeAdminError(w, http.StatusBadRequest, err.Error())
			return
		}
		req.IPAddress, req.SSHPort, addrChanged = ip, port, true
		// Repointing a node is the provisioning power (where the Hub sends tunnels and the stored
		// root password), not a plain edit (security audit F23).
		if claims, _ := ClaimsFromContext(r.Context()); claims == nil || !claimsAllow(claims, "nodes.provision") {
			writeAdminError(w, http.StatusForbidden, "Адрес ноды меняет администратор с правом «Настройка нод»")
			return
		}
	}
	if strings.TrimSpace(req.SSHUser) == "" {
		req.SSHUser = existing.SSHUser
	}
	if strings.TrimSpace(req.CountryCode) == "" {
		req.CountryCode = existing.CountryCode
	} else {
		if err := validateCountryCode(req.CountryCode); err != nil {
			writeAdminError(w, http.StatusBadRequest, err.Error())
			return
		}
		req.CountryCode = strings.ToUpper(strings.TrimSpace(req.CountryCode))
	}
	if strings.TrimSpace(req.Status) == "" {
		req.Status = existing.Status
	}

	// The role decides is_exit_node (CHECK servers_relay_not_exit): keep the stored value.
	isExitNode := existing.IsExitNode
	if !isExitNode && addrChanged {
		if err := validateRelaySSHPort(req.SSHPort); err != nil {
			writeAdminError(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	if req.TunnelProtocol == "" {
		req.TunnelProtocol = existing.TunnelProtocol
	}
	if req.TunnelPort == 0 {
		req.TunnelPort = existing.TunnelPort
	}
	if req.TunnelUUID == "" {
		req.TunnelUUID = existing.TunnelUUID
	}
	if req.TunnelPublicKey == "" {
		req.TunnelPublicKey = existing.TunnelPublicKey
	}
	if req.TunnelPrivateKey == "" {
		req.TunnelPrivateKey = existing.TunnelPrivateKey
	}
	if req.TunnelShortID == "" {
		req.TunnelShortID = existing.TunnelShortID
	}
	if req.TunnelSNI == "" {
		req.TunnelSNI = existing.TunnelSNI
	}
	if req.TunnelSSMethod == "" {
		req.TunnelSSMethod = existing.TunnelSSMethod
	}
	if req.TunnelSSPass == "" {
		req.TunnelSSPass = existing.TunnelSSPass
	}
	weight := existing.LoadWeight
	if req.LoadWeight != 0 {
		weight = req.LoadWeight
	}

	// The SSH password columns change only when the request carries a new password; it is
	// encrypted before anything is written, and without a working key nothing is saved.
	var newSSHPasswordEnc []byte
	if strings.TrimSpace(req.SSHPassword) != "" {
		enc, err := encryptSSHPassword(req.SSHPassword)
		if err != nil {
			log.Printf("[servers] server %d: SSH password not saved: %v", serverID, err)
			writeAdminError(w, http.StatusServiceUnavailable, sshPasswordKeyMissingMsg)
			return
		}
		newSSHPasswordEnc = enc
	}

	tx, err := db.DB.Begin()
	if err != nil {
		writeAdminError(w, http.StatusInternalServerError, "Не удалось начать транзакцию")
		return
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`
		UPDATE servers
		SET name = $1, ip_address = $2, ssh_port = $3, ssh_user = $4,
		    country_code = $5, status = $6,
		    is_exit_node = $7, tunnel_protocol = $8, tunnel_port = $9,
		    tunnel_uuid = $10, tunnel_public_key = $11, tunnel_private_key = $12,
		    tunnel_short_id = $13, tunnel_sni = $14, tunnel_ss_method = $15,
		    tunnel_ss_password = $16, load_weight = $17, updated_at = CURRENT_TIMESTAMP
		WHERE id = $18
	`, req.Name, req.IPAddress, req.SSHPort, req.SSHUser,
		req.CountryCode, req.Status,
		isExitNode, req.TunnelProtocol, req.TunnelPort,
		req.TunnelUUID, req.TunnelPublicKey, req.TunnelPrivateKey, req.TunnelShortID,
		req.TunnelSNI, req.TunnelSSMethod, req.TunnelSSPass, weight, serverID); err != nil {
		if isUniqueViolation(err) {
			writeAdminError(w, http.StatusConflict, nodeIPExistsMsg)
			return
		}
		writeAdminError(w, http.StatusInternalServerError, "Не удалось обновить сервер")
		return
	}
	if addrChanged {
		if _, err := tx.Exec(clearHostKeyQuery, serverID); err != nil {
			writeAdminError(w, http.StatusInternalServerError, "Не удалось обновить сервер")
			return
		}
		// The stored root password belongs to the old machine: it must not follow the node to
		// a new address (audit F50); the Hub key, or a password entered now, is used from here on.
		if newSSHPasswordEnc == nil {
			if _, err := tx.Exec(`UPDATE servers SET ssh_password = '', ssh_password_enc = NULL WHERE id = $1`, serverID); err != nil {
				writeAdminError(w, http.StatusInternalServerError, "Не удалось обновить сервер")
				return
			}
		}
	}
	if newSSHPasswordEnc != nil {
		// ssh_password is NOT NULL: the plaintext column is emptied, never set to NULL.
		if _, err := tx.Exec(`UPDATE servers SET ssh_password = '', ssh_password_enc = $1 WHERE id = $2`,
			newSSHPasswordEnc, serverID); err != nil {
			writeAdminError(w, http.StatusInternalServerError, "Не удалось обновить сервер")
			return
		}
	}
	if err := tx.Commit(); err != nil {
		writeAdminError(w, http.StatusInternalServerError, "Не удалось сохранить сервер")
		return
	}
	WriteAuditLog(r, "update_server", "server", fmt.Sprintf("%d", serverID), map[string]bool{"address_changed": addrChanged})
	if serverRoutingChanged(existing, req, weight) {
		biz.RequestSync() // the exit's address, paths and weight live in the xray/hysteria configs
	}
	msg := "Server updated"
	if addrChanged {
		msg = "Сохранено. Адрес или SSH-порт изменились — подтвердите ключ сервера заново, иначе Hub к нему не подключится"
	}
	writeAdminJSON(w, http.StatusOK, map[string]string{"message": msg})
}

// ServerActionHandler: restarting node-agent over the pinned SSH connection (refused while a job
// works on the node). Every other change to a node is a job (provision_handlers.go).
func ServerActionHandler(w http.ResponseWriter, r *http.Request) {
	id, ok := parseLastPathSegmentBeforeFinal(r.URL.Path, "actions")
	if !ok {
		writeAdminError(w, http.StatusBadRequest, "Неверный id сервера")
		return
	}
	var payload serverActionPayload
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		writeAdminError(w, http.StatusBadRequest, "Неверное тело запроса")
		return
	}
	switch strings.TrimSpace(payload.Action) {
	case "restart_agent":
		if !provisionReady(w) {
			return
		}
		// Not while a job works on the node (it may be restarting or replacing node-agent itself).
		if refuseIfJobActive(w, r.Context(), id) {
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()
		rm, err := nodeRemote(ctx, id)
		if err != nil {
			writeNodeAccessError(w, err)
			return
		}
		defer provision.Close(rm)
		if _, err := rm.Run(ctx, "systemctl restart node-agent"); err != nil {
			writeAdminError(w, http.StatusBadGateway, "node-agent не перезапущен: "+err.Error())
			return
		}
		WriteAuditLog(r, "restart_agent", "server", strconv.Itoa(id), nil)
		writeAdminJSON(w, http.StatusOK, map[string]string{"message": "node-agent перезапущен"})
	default:
		writeAdminError(w, http.StatusBadRequest, "Неизвестное действие: «Настроить», «Проверить», «Отключить» — задачи в карточке сервера")
	}
}

// ── Helpers ──────────────────────────────────────────────

func loadServerByID(serverID int, includePassword bool) (serverResponse, error) {
	var server serverResponse
	var updatedAt time.Time
	var metricsSyncedAt sql.NullTime
	if includePassword {
		query := `
			SELECT s.id, s.name, s.ip_address, s.ssh_port, s.ssh_user,
			       COALESCE(s.ssh_password, ''), s.ssh_password_enc,
			       s.country_code, s.status, s.load_metrics, COALESCE(s.updated_at, CURRENT_TIMESTAMP),
			       s.metrics_synced_at,
			       s.is_exit_node, s.tunnel_status, s.tunnel_protocol, s.tunnel_port,
			       COALESCE(s.tunnel_uuid, ''), COALESCE(s.tunnel_public_key, ''),
			       COALESCE(s.tunnel_private_key, ''), COALESCE(s.tunnel_short_id, ''),
			       COALESCE(s.tunnel_sni, ''),
			       COALESCE(s.tunnel_ss_method, ''), COALESCE(s.tunnel_ss_password, ''),
			       COALESCE(s.tunnel_error, ''), s.lifecycle_state, s.mtproto_status, COALESCE(s.load_weight, 1)
			FROM servers s
			WHERE s.id = $1
		`
		var sshPwEnc []byte
		err := db.DB.QueryRow(query, serverID).Scan(
			&server.ID, &server.Name, &server.IPAddress, &server.SSHPort, &server.SSHUser,
			&server.SSHPassword, &sshPwEnc,
			&server.CountryCode, &server.Status, &server.LoadMetrics,
			&updatedAt, &metricsSyncedAt,
			&server.IsExitNode, &server.TunnelStatus, &server.TunnelProtocol, &server.TunnelPort,
			&server.TunnelUUID, &server.TunnelPublicKey, &server.TunnelPrivateKey,
			&server.TunnelShortID, &server.TunnelSNI,
			&server.TunnelSSMethod, &server.TunnelSSPass, &server.TunnelError, &server.LifecycleState, &server.MTProtoStatus, &server.LoadWeight)
		if err != nil {
			return server, err
		}
		// Set means stored, as in the list: a cipher, or a legacy plaintext.
		server.HasSSHPassword = len(sshPwEnc) > 0 || strings.TrimSpace(server.SSHPassword) != ""
		// Prefer decrypted password if available.
		if len(sshPwEnc) > 0 {
			if dec, err := crypto.Decrypt(sshPwEnc); err == nil {
				server.SSHPassword = dec
			}
		}
	} else {
		query := `
			SELECT s.id, s.name, s.ip_address, s.ssh_port, s.ssh_user,
			       s.country_code, s.status, s.load_metrics, COALESCE(s.updated_at, CURRENT_TIMESTAMP),
			       s.metrics_synced_at,
			       s.is_exit_node, s.tunnel_status, s.tunnel_protocol, s.tunnel_port,
			       COALESCE(s.tunnel_uuid, ''), COALESCE(s.tunnel_public_key, ''),
			       COALESCE(s.tunnel_private_key, ''), COALESCE(s.tunnel_short_id, ''),
			       COALESCE(s.tunnel_sni, ''),
			       COALESCE(s.tunnel_ss_method, ''), COALESCE(s.tunnel_ss_password, ''),
			       COALESCE(s.tunnel_error, ''), s.lifecycle_state, s.mtproto_status, COALESCE(s.load_weight, 1)
			FROM servers s
			WHERE s.id = $1
		`
		err := db.DB.QueryRow(query, serverID).Scan(
			&server.ID, &server.Name, &server.IPAddress, &server.SSHPort, &server.SSHUser,
			&server.CountryCode, &server.Status, &server.LoadMetrics,
			&updatedAt, &metricsSyncedAt,
			&server.IsExitNode, &server.TunnelStatus, &server.TunnelProtocol, &server.TunnelPort,
			&server.TunnelUUID, &server.TunnelPublicKey, &server.TunnelPrivateKey,
			&server.TunnelShortID, &server.TunnelSNI,
			&server.TunnelSSMethod, &server.TunnelSSPass, &server.TunnelError, &server.LifecycleState, &server.MTProtoStatus, &server.LoadWeight)
		if err != nil {
			return server, err
		}
	}
	server.UpdatedAt = updatedAt.Format(time.RFC3339)
	if metricsSyncedAt.Valid {
		server.MetricsSyncedAt = metricsSyncedAt.Time.Format(time.RFC3339)
	}
	if len(server.LoadMetrics) == 0 {
		server.LoadMetrics = json.RawMessage(`{}`)
	}
	return server, nil
}

// encryptSSHPassword encrypts a non-empty SSH password with AES-256-GCM (SSH_ENCRYPTION_KEY).
// A missing or invalid key is an error: the password is never stored in plaintext.
func encryptSSHPassword(password string) ([]byte, error) {
	if strings.TrimSpace(password) == "" {
		return nil, fmt.Errorf("empty SSH password")
	}
	ciphertext, err := crypto.Encrypt(password)
	if err != nil {
		return nil, err
	}
	if len(ciphertext) == 0 {
		return nil, fmt.Errorf("encryption returned no ciphertext")
	}
	return ciphertext, nil
}

// ── mTLS connection to node-agent ────────────────────────

// loadClientTLSCredentials: the Hub's client pair and CA, plus — while the node-agent CA is being
// replaced — the legacy pair and CA next to them (provision.NodeAgentClientTLS), so agents not
// yet converged stay reachable.
func loadClientTLSCredentials() (credentials.TransportCredentials, error) {
	cfg, err := provision.NodeAgentClientTLS(resolveRuntimePath("TLS_CLIENT_CERT_FILE", "client-cert.pem"),
		resolveRuntimePath("TLS_CLIENT_KEY_FILE", "client-key.pem"), resolveRuntimePath("TLS_CA_FILE", "ca-cert.pem"))
	if err != nil {
		return nil, err
	}
	return credentials.NewTLS(cfg), nil
}

func resolveRuntimePath(envKey string, certFileName string) string {
	if value := strings.TrimSpace(os.Getenv(envKey)); value != "" {
		return filepath.Clean(value)
	}
	return filepath.Join(config.AppDir(), "certs", certFileName)
}

func connectToNodeAgent(ctx context.Context, ip string) (pbnode.NodeAgentClient, *grpc.ClientConn, error) {
	tlsCredentials, err := loadClientTLSCredentials()
	if err != nil {
		return nil, nil, err
	}
	address := fmt.Sprintf("%s:50052", ip)
	conn, err := grpc.DialContext(ctx, address, grpc.WithTransportCredentials(tlsCredentials))
	if err != nil {
		return nil, nil, fmt.Errorf("dial node-agent %s: %w", address, err)
	}
	return pbnode.NewNodeAgentClient(conn), conn, nil
}

// nodeLogServices are the units each role really runs (C5): an exit's recipe installs node-agent,
// xray, hysteria2 and optionally the Telegram proxy; a reserve entry runs nginx (stream to the
// Hub) and heyearth-relay (its nft forwarding).
var nodeLogServices = map[string][]string{
	"exit":  {"node-agent", "xray", "hysteria2", config.MTProxyService},
	"relay": {"nginx", "heyearth-relay"},
}

// nodeLogService: the asked unit if the server's role runs it, else "" (default: the first).
func nodeLogService(role, asked string) string {
	units, ok := nodeLogServices[role]
	if !ok {
		units = nodeLogServices["exit"]
	}
	if asked == "" {
		return units[0]
	}
	if slices.Contains(units, asked) {
		return asked
	}
	return ""
}

// GetServerLogsHandler reads a node unit's journal over the pinned SSH connection.
func GetServerLogsHandler(w http.ResponseWriter, r *http.Request) {
	serverID, ok := parseLastPathSegmentBeforeFinal(r.URL.Path, "logs")
	if !ok {
		writeAdminError(w, http.StatusBadRequest, "Неверный id сервера")
		return
	}
	var role string
	if err := db.DB.QueryRowContext(r.Context(), `SELECT COALESCE(role, 'exit') FROM servers WHERE id = $1`, serverID).Scan(&role); err != nil {
		writeAdminError(w, http.StatusNotFound, "Сервер не найден")
		return
	}
	service := nodeLogService(role, r.URL.Query().Get("service"))
	if service == "" {
		writeAdminError(w, http.StatusBadRequest, "На этом сервере нет такой службы")
		return
	}
	tailN, err := strconv.Atoi(r.URL.Query().Get("tail"))
	if err != nil || tailN < 1 || tailN > 5000 {
		tailN = 100
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	rm, err := nodeRemote(ctx, serverID)
	if err != nil {
		writeNodeAccessError(w, err)
		return
	}
	defer provision.Close(rm)
	out, err := rm.Run(ctx, fmt.Sprintf("journalctl -u %s --no-pager -n %d --output=short-iso 2>&1", service, tailN))
	resp := map[string]string{"logs": out}
	if err != nil {
		resp["error"] = err.Error()
	}
	writeAdminJSON(w, http.StatusOK, resp)
}
