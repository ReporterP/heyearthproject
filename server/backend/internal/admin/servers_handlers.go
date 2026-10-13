package admin

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/heyearth/backend/internal/biz"
	"github.com/heyearth/backend/internal/config"
	"github.com/heyearth/backend/internal/crypto"
	"github.com/heyearth/backend/internal/db"
	pbnode "github.com/heyearth/backend/internal/pb/node_agent_v1"
	"github.com/heyearth/backend/internal/shellquote"
	"golang.org/x/crypto/ssh"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

// provisionProgress хранит текущий прогресс провижининга серверов (in-memory)
var provisionProgress sync.Map // map[int]*ProvisionProgress

// goneHandler answers 410 Gone with a Russian explanation: endpoints phase 1 removed.
func goneHandler(msg string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) { writeAdminError(w, http.StatusGone, msg) }
}

const (
	addServerGoneMsg   = "Добавление сервера через старую форму отключено: она делала неполную настройку и перезапускала API. Новый exit добавляется мастером «Добавить ноду» (следующее обновление админки)."
	fileWriteGoneMsg   = "Запись файлов на серверах из админки отключена: ручная правка конфигов и ключей ломала туннели. Журналы доступны только для чтения."
	tunnelCredsGoneMsg = "Генерация ключей туннеля в браузере отключена: ключи создаются на самой ноде при настройке."
	setupGoneMsg       = "Настройка exit из старой формы отключена: она перезапускала API и не доделывала шаги. Новый exit настраивается мастером «Добавить ноду» (следующее обновление админки)."

	sshPasswordKeyMissingMsg = "Ключ шифрования SSH не настроен — пароль не сохранён"
)

// removedServerActions answer 410: they broke the tunnel (fetch_tunnel_creds switched it to
// shadowsocks:8388; sync_tunnel TCP-dialled the UDP port and marked a working exit unhealthy;
// sync_metrics overwrote the protocol) or restarted the API itself mid-provisioning.
var removedServerActions = map[string]string{
	"fetch_tunnel_creds": "Действие «Получить ключи туннеля» удалено: оно переключало туннель на Shadowsocks и отключало exit. Для проверки используйте «Проверить туннель».",
	"sync_tunnel":        "Действие «Синхронизировать туннель» удалено: оно помечало рабочий exit недоступным. Для проверки используйте «Проверить туннель».",
	"sync_metrics":       "Действие «Синхронизировать метрики» удалено: оно перезаписывало протокол туннеля. Метрики обновляются автоматически.",
	"setup_exit_node":    setupGoneMsg,
	"reprovision":        setupGoneMsg,
}

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
		       s.ssh_key_id, COALESCE(sk.name, ''),
		       s.is_exit_node, s.tunnel_status, s.tunnel_protocol, s.tunnel_port,
		       COALESCE(s.tunnel_error, ''), COALESCE(s.load_weight, 1), s.lifecycle_state, s.mtproto_status,
		       (COALESCE(TRIM(s.ssh_password), '') <> '' OR COALESCE(octet_length(s.ssh_password_enc), 0) > 0),
		       (COALESCE(s.tunnel_uuid, '') <> '' OR COALESCE(s.tunnel_ss_password, '') <> '' OR COALESCE(s.tunnel_private_key, '') <> '')
		FROM servers s
		LEFT JOIN ssh_keys sk ON sk.id = s.ssh_key_id
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
		var sshKeyName string
		if err := rows.Scan(&s.ID, &s.Name, &s.IPAddress, &s.SSHPort, &s.SSHUser, &s.CountryCode, &s.Status, &s.LoadMetrics, &updatedAt, &metricsSyncedAt, &s.SshKeyID, &sshKeyName, &s.IsExitNode, &s.TunnelStatus, &s.TunnelProtocol, &s.TunnelPort, &s.TunnelError, &s.LoadWeight, &s.LifecycleState, &s.MTProtoStatus, &s.HasSSHPassword, &s.HasTunnelCreds); err != nil {
			writeAdminError(w, http.StatusInternalServerError, "Не удалось прочитать сервер из базы")
			return
		}
		if len(s.LoadMetrics) == 0 {
			s.LoadMetrics = json.RawMessage(`{}`)
		}
		if sshKeyName != "" {
			s.SshKeyName = sshKeyName
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

// AddServerHandler: the old form is gone (phase 1); the node wizard adds POST /servers in phase 2.
func AddServerHandler(w http.ResponseWriter, r *http.Request) {
	writeAdminError(w, http.StatusGone, addServerGoneMsg)
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
	if strings.TrimSpace(req.SSHUser) == "" {
		req.SSHUser = existing.SSHUser
	}
	if req.SshKeyID == nil {
		req.SshKeyID = existing.SshKeyID
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

	// Все серверы — exit nodes
	isExitNode := true
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
			writeAdminError(w, http.StatusInternalServerError, sshPasswordKeyMissingMsg)
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
		    ssh_key_id = $5, country_code = $6, status = $7,
		    is_exit_node = $8, tunnel_protocol = $9, tunnel_port = $10,
		    tunnel_uuid = $11, tunnel_public_key = $12, tunnel_private_key = $13,
		    tunnel_short_id = $14, tunnel_sni = $15, tunnel_ss_method = $16,
		    tunnel_ss_password = $17, load_weight = $18, updated_at = CURRENT_TIMESTAMP
		WHERE id = $19
	`, req.Name, req.IPAddress, req.SSHPort, req.SSHUser,
		req.SshKeyID, req.CountryCode, req.Status,
		isExitNode, req.TunnelProtocol, req.TunnelPort,
		req.TunnelUUID, req.TunnelPublicKey, req.TunnelPrivateKey, req.TunnelShortID,
		req.TunnelSNI, req.TunnelSSMethod, req.TunnelSSPass, weight, serverID); err != nil {
		writeAdminError(w, http.StatusInternalServerError, "Не удалось обновить сервер")
		return
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
	WriteAuditLog(r, "update_server", "server", fmt.Sprintf("%d", serverID), nil)
	if serverRoutingChanged(existing, req, weight) {
		biz.RequestSync() // the exit's address, paths and weight live in the xray/hysteria configs
	}
	writeAdminJSON(w, http.StatusOK, map[string]string{"message": "Server updated"})
}

func DeleteServerHandler(w http.ResponseWriter, r *http.Request) {
	serverID, ok := parseLastPathInt(r.URL.Path)
	if !ok {
		writeAdminError(w, http.StatusBadRequest, "Неверный id сервера")
		return
	}
	// Reject if another provision/deprovision is already in flight for this server.
	if v, ok := provisionProgress.Load(serverID); ok {
		pp := v.(*ProvisionProgress)
		if !pp.Finished {
			writeAdminError(w, http.StatusConflict, "Операция с этим сервером уже выполняется")
			return
		}
	}
	WriteAuditLog(r, "delete_server", "server", fmt.Sprintf("%d", serverID), nil)
	// Async hard deprovision: hub-side cleanup + best-effort exit teardown +
	// archive + DELETE FROM servers. Progress visible via /actions/progress.
	go runDeprovision(serverID, biz.DeprovisionHard)
	writeAdminJSON(w, http.StatusAccepted, map[string]string{
		"message":   "Deprovisioning started",
		"server_id": fmt.Sprintf("%d", serverID),
	})
}

// runDeprovision wraps biz.Deprovision with the same ProvisionProgress reporting
// pattern as runReinstallMTProto, so the existing /actions/progress endpoint
// keeps working unchanged for the frontend.
func runDeprovision(serverID int, mode biz.DeprovisionMode) {
	steps := []ProvisionStep{
		{Name: "transition_deprovisioning", Description: "Перевод в deprovisioning", Status: "pending"},
		{Name: "subscription_bump", Description: "Bump subscription_version", Status: "pending"},
		{Name: "drain", Description: "Drain 3 sec", Status: "pending"},
		{Name: "hub_stop", Description: "Остановка hysteria2-client-N", Status: "pending"},
		{Name: "archive", Description: "Архивирование .yaml + .service", Status: "pending"},
		{Name: "remove_unit", Description: "Удаление systemd-юнита", Status: "pending"},
		{Name: "exit_teardown", Description: "Лучшая попытка остановки на exit", Status: "pending"},
		{Name: "delete_row", Description: "Удаление записи из БД (hard)", Status: "pending"},
		{Name: "transition_offline", Description: "Переход в offline (soft)", Status: "pending"},
		{Name: "done", Description: "Готово", Status: "pending"},
	}
	progress := &ProvisionProgress{
		ServerID: serverID,
		Action:   fmt.Sprintf("deprovision_%s", string(mode)),
		Steps:    steps,
		Started:  time.Now(),
	}
	provisionProgress.Store(serverID, progress)

	stepIdx := map[string]int{}
	for i, s := range steps {
		stepIdx[s.Name] = i
	}
	ch := make(chan biz.ProvisionEvent, 32)

	doneCh := make(chan struct{})
	go func() {
		defer close(doneCh)
		for ev := range ch {
			idx, ok := stepIdx[ev.Stage]
			if !ok {
				continue
			}
			progress.Steps[idx].Status = ev.Status
			if ev.Message != "" {
				progress.Steps[idx].Error = ev.Message
			}
			provisionProgress.Store(serverID, progress)
		}
	}()

	err := biz.Deprovision(serverID, mode, ch)
	close(ch)
	<-doneCh

	if err != nil {
		log.Printf("[deprovision] server %d (%s): error: %v", serverID, mode, err)
	} else {
		log.Printf("[deprovision] server %d (%s): completed", serverID, mode)
	}
	progress.Finished = true
	provisionProgress.Store(serverID, progress)
}

// runHubProvisionOnly is the async wrapper for biz.HubProvisionExisting.
// Used by the admin "Provision" button when the exit-side has already been set up
// (e.g. server row was created with credentials but hub-side wasn't synced, OR
// after a soft deprovision when re-enabling the same server).
func runHubProvisionOnly(serverID int) {
	steps := []ProvisionStep{
		{Name: "load_server", Description: "Загрузка данных сервера", Status: "pending"},
		{Name: "write_config", Description: "Создание client-N.yaml", Status: "pending"},
		{Name: "install_unit", Description: "Установка systemd-юнита", Status: "pending"},
		{Name: "start_unit", Description: "Запуск туннеля", Status: "pending"},
		{Name: "subscription_bump", Description: "Bump subscription_version", Status: "pending"},
		{Name: "transition_online", Description: "Переход в online", Status: "pending"},
		{Name: "done", Description: "Готово", Status: "pending"},
	}
	progress := &ProvisionProgress{
		ServerID: serverID,
		Action:   "hub_provision",
		Steps:    steps,
		Started:  time.Now(),
	}
	provisionProgress.Store(serverID, progress)

	stepIdx := map[string]int{}
	for i, s := range steps {
		stepIdx[s.Name] = i
	}
	ch := make(chan biz.ProvisionEvent, 32)
	doneCh := make(chan struct{})
	go func() {
		defer close(doneCh)
		for ev := range ch {
			idx, ok := stepIdx[ev.Stage]
			if !ok {
				continue
			}
			progress.Steps[idx].Status = ev.Status
			if ev.Message != "" {
				progress.Steps[idx].Error = ev.Message
			}
			provisionProgress.Store(serverID, progress)
		}
	}()

	err := biz.HubProvisionExisting(serverID, ch)
	close(ch)
	<-doneCh

	if err != nil {
		log.Printf("[hub-provision] server %d: %v", serverID, err)
	} else {
		log.Printf("[hub-provision] server %d: ok", serverID)
	}
	progress.Finished = true
	provisionProgress.Store(serverID, progress)
}

func ServerActionHandler(w http.ResponseWriter, r *http.Request) {
	path := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/admin/servers/"), "/")
	if !strings.HasSuffix(path, "/actions") {
		writeAdminError(w, http.StatusBadRequest, "Неверный путь действия с сервером")
		return
	}
	serverIDText := strings.TrimSuffix(path, "/actions")
	serverIDText = strings.Trim(serverIDText, "/")
	serverID, err := strconv.Atoi(serverIDText)
	if err != nil {
		writeAdminError(w, http.StatusBadRequest, "Неверный id сервера")
		return
	}

	var payload serverActionPayload
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		writeAdminError(w, http.StatusBadRequest, "Неверное тело запроса")
		return
	}
	// Removed actions are refused before the database or a node is touched.
	action := strings.TrimSpace(payload.Action)
	if msg, gone := removedServerActions[action]; gone {
		writeAdminError(w, http.StatusGone, msg)
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

	switch action {
	case "restart_agent":
		if err := restartNodeAgent(server); err != nil {
			writeAdminError(w, http.StatusBadGateway, err.Error())
			return
		}
	case "reinstall_mtproxy":
		if v, ok := provisionProgress.Load(serverID); ok {
			pp := v.(*ProvisionProgress)
			if !pp.Finished {
				writeAdminError(w, http.StatusConflict, "Операция с этим сервером уже выполняется")
				return
			}
		}
		go runReinstallMTProto(server)
		writeAdminJSON(w, http.StatusAccepted, map[string]string{"message": "MTProto reinstall started"})
		return
	case "deprovision_soft":
		if v, ok := provisionProgress.Load(serverID); ok {
			pp := v.(*ProvisionProgress)
			if !pp.Finished {
				writeAdminError(w, http.StatusConflict, "Операция с этим сервером уже выполняется")
				return
			}
		}
		go runDeprovision(serverID, biz.DeprovisionSoft)
		writeAdminJSON(w, http.StatusAccepted, map[string]string{"message": "Deprovisioning (soft) started"})
		return
	case "deprovision_hard":
		if v, ok := provisionProgress.Load(serverID); ok {
			pp := v.(*ProvisionProgress)
			if !pp.Finished {
				writeAdminError(w, http.StatusConflict, "Операция с этим сервером уже выполняется")
				return
			}
		}
		go runDeprovision(serverID, biz.DeprovisionHard)
		writeAdminJSON(w, http.StatusAccepted, map[string]string{"message": "Deprovisioning (hard) started"})
		return
	case "hub_provision":
		if v, ok := provisionProgress.Load(serverID); ok {
			pp := v.(*ProvisionProgress)
			if !pp.Finished {
				writeAdminError(w, http.StatusConflict, "Операция с этим сервером уже выполняется")
				return
			}
		}
		go runHubProvisionOnly(serverID)
		writeAdminJSON(w, http.StatusAccepted, map[string]string{"message": "Hub provisioning started"})
		return
	default:
		writeAdminError(w, http.StatusBadRequest, "Неизвестное действие с сервером")
		return
	}
	writeAdminJSON(w, http.StatusOK, map[string]string{"message": "Server action completed"})
}

// ── Helpers ──────────────────────────────────────────────

func loadServerByID(serverID int, includePassword bool) (serverResponse, error) {
	var server serverResponse
	var updatedAt time.Time
	var metricsSyncedAt sql.NullTime
	var sshKeyName string
	if includePassword {
		query := `
			SELECT s.id, s.name, s.ip_address, s.ssh_port, s.ssh_user,
			       COALESCE(s.ssh_password, ''), s.ssh_password_enc,
			       s.country_code, s.status, s.load_metrics, COALESCE(s.updated_at, CURRENT_TIMESTAMP),
			       s.metrics_synced_at,
			       s.ssh_key_id, COALESCE(sk.name, ''),
			       s.is_exit_node, s.tunnel_status, s.tunnel_protocol, s.tunnel_port,
			       COALESCE(s.tunnel_uuid, ''), COALESCE(s.tunnel_public_key, ''),
			       COALESCE(s.tunnel_private_key, ''), COALESCE(s.tunnel_short_id, ''),
			       COALESCE(s.tunnel_sni, ''),
			       COALESCE(s.tunnel_ss_method, ''), COALESCE(s.tunnel_ss_password, ''),
			       COALESCE(s.tunnel_error, ''), s.lifecycle_state, s.mtproto_status, COALESCE(s.load_weight, 1)
			FROM servers s
			LEFT JOIN ssh_keys sk ON sk.id = s.ssh_key_id
			WHERE s.id = $1
		`
		var sshPwEnc []byte
		err := db.DB.QueryRow(query, serverID).Scan(
			&server.ID, &server.Name, &server.IPAddress, &server.SSHPort, &server.SSHUser,
			&server.SSHPassword, &sshPwEnc,
			&server.CountryCode, &server.Status, &server.LoadMetrics,
			&updatedAt, &metricsSyncedAt, &server.SshKeyID, &sshKeyName,
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
			       s.ssh_key_id, COALESCE(sk.name, ''),
			       s.is_exit_node, s.tunnel_status, s.tunnel_protocol, s.tunnel_port,
			       COALESCE(s.tunnel_uuid, ''), COALESCE(s.tunnel_public_key, ''),
			       COALESCE(s.tunnel_private_key, ''), COALESCE(s.tunnel_short_id, ''),
			       COALESCE(s.tunnel_sni, ''),
			       COALESCE(s.tunnel_ss_method, ''), COALESCE(s.tunnel_ss_password, ''),
			       COALESCE(s.tunnel_error, ''), s.lifecycle_state, s.mtproto_status, COALESCE(s.load_weight, 1)
			FROM servers s
			LEFT JOIN ssh_keys sk ON sk.id = s.ssh_key_id
			WHERE s.id = $1
		`
		err := db.DB.QueryRow(query, serverID).Scan(
			&server.ID, &server.Name, &server.IPAddress, &server.SSHPort, &server.SSHUser,
			&server.CountryCode, &server.Status, &server.LoadMetrics,
			&updatedAt, &metricsSyncedAt, &server.SshKeyID, &sshKeyName,
			&server.IsExitNode, &server.TunnelStatus, &server.TunnelProtocol, &server.TunnelPort,
			&server.TunnelUUID, &server.TunnelPublicKey, &server.TunnelPrivateKey,
			&server.TunnelShortID, &server.TunnelSNI,
			&server.TunnelSSMethod, &server.TunnelSSPass, &server.TunnelError, &server.LifecycleState, &server.MTProtoStatus, &server.LoadWeight)
		if err != nil {
			return server, err
		}
	}
	if sshKeyName != "" {
		server.SshKeyName = sshKeyName
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

// ── Server actions (restart) ─────────────────────────────

func restartNodeAgent(server serverResponse) error {
	if err := runSSHCommand(server, "systemctl restart node-agent"); err != nil {
		return fmt.Errorf("restart node-agent: %w", err)
	}
	if _, err := db.DB.Exec(`UPDATE servers SET status = 'online', updated_at = CURRENT_TIMESTAMP WHERE id = $1`, server.ID); err != nil {
		return err
	}
	return nil
}

// ── SSH ──────────────────────────────────────────────────

// runSSHCommand connects to the server via SSH (key-based if ssh_key_id is set,
// password-based otherwise) and runs a single command.
func runSSHCommand(server serverResponse, command string) error {
	authMethods, err := sshAuthMethods(server)
	if err != nil {
		return err
	}
	cfg := &ssh.ClientConfig{
		User:            server.SSHUser,
		Auth:            authMethods,
		HostKeyCallback: toFUHostKeyCallback(server.ID),
		Timeout:         30 * time.Second,
	}
	addr := net.JoinHostPort(server.IPAddress, fmt.Sprintf("%d", server.SSHPort))
	client, err := ssh.Dial("tcp", addr, cfg)
	if err != nil {
		return fmt.Errorf("ssh dial %s: %w", addr, err)
	}
	defer client.Close()

	sess, err := client.NewSession()
	if err != nil {
		return fmt.Errorf("ssh session: %w", err)
	}
	defer sess.Close()

	var errBuf bytes.Buffer
	sess.Stderr = &errBuf
	if err := sess.Run(command); err != nil {
		return fmt.Errorf("command %q: %v: %s", command, err, strings.TrimSpace(errBuf.String()))
	}
	return nil
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

func sshAuthMethods(server serverResponse) ([]ssh.AuthMethod, error) {
	if server.SshKeyID != nil && *server.SshKeyID > 0 {
		key, err := biz.GetSshKeyByID(*server.SshKeyID)
		if err != nil {
			return nil, fmt.Errorf("load ssh key %d: %w", *server.SshKeyID, err)
		}
		signer, err := ssh.ParsePrivateKey([]byte(key.PrivateKey))
		if err != nil {
			return nil, fmt.Errorf("parse ssh key: %w", err)
		}
		return []ssh.AuthMethod{ssh.PublicKeys(signer)}, nil
	}
	pw := strings.TrimSpace(server.SSHPassword)
	if pw == "" {
		return nil, fmt.Errorf("neither ssh_key_id nor ssh_password is set for this server")
	}
	return []ssh.AuthMethod{ssh.Password(pw)}, nil
}

// ── mTLS connection to node-agent ────────────────────────

func loadClientTLSCredentials() (credentials.TransportCredentials, error) {
	certFile := resolveRuntimePath("TLS_CLIENT_CERT_FILE", "client-cert.pem")
	keyFile := resolveRuntimePath("TLS_CLIENT_KEY_FILE", "client-key.pem")
	caFile := resolveRuntimePath("TLS_CA_FILE", "ca-cert.pem")

	clientCert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return nil, fmt.Errorf("load client key pair: %w", err)
	}
	pemServerCA, err := os.ReadFile(caFile)
	if err != nil {
		return nil, fmt.Errorf("read CA file: %w", err)
	}
	certPool := x509.NewCertPool()
	if !certPool.AppendCertsFromPEM(pemServerCA) {
		return nil, fmt.Errorf("append CA certs")
	}
	return credentials.NewTLS(&tls.Config{
		Certificates: []tls.Certificate{clientCert},
		RootCAs:      certPool,
		ServerName:   "node-agent",
		MinVersion:   tls.VersionTLS13,
	}), nil
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

// GetServerMetricsHandler fetches live metrics from node-agent.
func GetServerMetricsHandler(w http.ResponseWriter, r *http.Request) {
	serverID, ok := parseLastPathSegmentBeforeFinal(r.URL.Path, "metrics")
	if !ok {
		writeAdminError(w, http.StatusBadRequest, "Неверный id сервера")
		return
	}
	var ipAddress string
	var sshPort int
	err := db.DB.QueryRow(`SELECT ip_address, ssh_port FROM servers WHERE id = $1`, serverID).Scan(&ipAddress, &sshPort)
	if err != nil {
		writeAdminError(w, http.StatusNotFound, "Сервер не найден")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	agentClient, conn, err := connectToNodeAgent(ctx, ipAddress)
	if err != nil {
		writeAdminJSON(w, http.StatusOK, map[string]any{
			"error": fmt.Sprintf("node-agent недоступен: %v", err),
		})
		return
	}
	defer conn.Close()

	metrics, err := agentClient.GetMetrics(ctx, &pbnode.GetMetricsRequest{})
	if err != nil {
		writeAdminJSON(w, http.StatusOK, map[string]any{
			"error": fmt.Sprintf("не удалось получить метрики: %v", err),
		})
		return
	}

	writeAdminJSON(w, http.StatusOK, map[string]any{
		"cpu_usage":          metrics.CpuUsage,
		"memory_usage":       metrics.MemoryUsage,
		"active_connections": metrics.ActiveConnections,
	})
}

// GetServerLogsHandler fetches recent service logs via SSH.
func GetServerLogsHandler(w http.ResponseWriter, r *http.Request) {
	serverID, ok := parseLastPathSegmentBeforeFinal(r.URL.Path, "logs")
	if !ok {
		writeAdminError(w, http.StatusBadRequest, "Неверный id сервера")
		return
	}
	service := r.URL.Query().Get("service")
	allowedServices := map[string]bool{
		"node-agent": true, "xray": true, "hysteria2": true,
		"nginx": true, "heyearth-api": true,
	}
	if !allowedServices[service] {
		service = "node-agent"
	}
	tailStr := r.URL.Query().Get("tail")
	tailN, err := strconv.Atoi(tailStr)
	if err != nil || tailN < 1 || tailN > 5000 {
		tailN = 100
	}

	server, sshClient, err := sshToServer(serverID)
	if err != nil {
		writeAdminError(w, http.StatusInternalServerError, fmt.Sprintf("SSH-подключение не удалось: %v", err))
		return
	}
	_ = server
	defer sshClient.Close()

	session, err := sshClient.NewSession()
	if err != nil {
		writeAdminError(w, http.StatusInternalServerError, "Не удалось открыть SSH-сессию")
		return
	}
	defer session.Close()

	cmd := fmt.Sprintf("journalctl -u %s --no-pager -n %d --output=short-iso 2>&1", service, tailN)
	output, err := session.CombinedOutput(cmd)
	if err != nil {
		writeAdminJSON(w, http.StatusOK, map[string]string{
			"logs":  string(output),
			"error": err.Error(),
		})
		return
	}
	writeAdminJSON(w, http.StatusOK, map[string]string{"logs": string(output)})
}

// ServerFileBrowserHandler lists files at a given path on the server.
func ServerFileBrowserHandler(w http.ResponseWriter, r *http.Request) {
	serverID, ok := parseLastPathSegmentBeforeFinal(r.URL.Path, "files")
	if !ok {
		writeAdminError(w, http.StatusBadRequest, "Неверный id сервера")
		return
	}
	dirPath := r.URL.Query().Get("path")
	if dirPath == "" {
		dirPath = "/opt/heyearth"
	}
	// Node paths are POSIX: path.Clean, not filepath.Clean (which turns "/" into "\" on Windows).
	dirPath = path.Clean(dirPath)
	if !isAllowedListPath(dirPath) {
		writeAdminError(w, http.StatusForbidden, listForbiddenMsg+" (латиница, цифры, . _ / @ + -)")
		return
	}

	_, sshClient, err := sshToServer(serverID)
	if err != nil {
		writeAdminError(w, http.StatusInternalServerError, fmt.Sprintf("SSH-подключение не удалось: %v", err))
		return
	}
	defer sshClient.Close()

	session, err := sshClient.NewSession()
	if err != nil {
		writeAdminError(w, http.StatusInternalServerError, "Не удалось открыть SSH-сессию")
		return
	}
	defer session.Close()

	output, err := session.CombinedOutput(listDirCommand(dirPath))
	if err != nil && writeNodePathError(w, err, listForbiddenMsg) {
		return // a symlink out of the allowed directories, or nothing there
	}
	entries := parseLsOutput(string(output))
	writeAdminJSON(w, http.StatusOK, map[string]any{"path": dirPath, "entries": entries})
}

// parseLsOutput parses `ls -la` output into structured file entries.
func parseLsOutput(output string) []map[string]any {
	var entries []map[string]any
	lines := strings.Split(strings.TrimSpace(output), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "total ") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 9 {
			continue
		}
		perms := fields[0]
		sizeStr := fields[4]
		name := strings.Join(fields[8:], " ")
		if name == "." || name == ".." {
			continue
		}
		isDir := len(perms) > 0 && perms[0] == 'd'
		size, _ := strconv.ParseInt(sizeStr, 10, 64)
		entries = append(entries, map[string]any{
			"name":   name,
			"is_dir": isDir,
			"size":   size,
			"perms":  perms,
		})
	}
	if entries == nil {
		entries = []map[string]any{}
	}
	return entries
}

// ServerFileReadHandler reads file content from a server via SSH.
// GET /api/admin/servers/{id}/file-content?path=/var/log/syslog (logs only)
func ServerFileReadHandler(w http.ResponseWriter, r *http.Request) {
	serverID, ok := parseLastPathSegmentBeforeFinal(r.URL.Path, "file-content")
	if !ok {
		writeAdminError(w, http.StatusBadRequest, "Неверный id сервера")
		return
	}
	filePath := r.URL.Query().Get("path")
	if filePath == "" {
		writeAdminError(w, http.StatusBadRequest, "Не указан путь")
		return
	}
	filePath = path.Clean(filePath)
	if !isAllowedReadPath(filePath) {
		writeAdminError(w, http.StatusForbidden, logReadForbiddenMsg)
		return
	}

	_, sshClient, err := sshToServer(serverID)
	if err != nil {
		writeAdminError(w, http.StatusInternalServerError, fmt.Sprintf("SSH-подключение не удалось: %v", err))
		return
	}
	defer sshClient.Close()

	session, err := sshClient.NewSession()
	if err != nil {
		writeAdminError(w, http.StatusInternalServerError, "Не удалось открыть SSH-сессию")
		return
	}
	defer session.Close()

	output, err := session.CombinedOutput(logReadCommand(filePath))
	if err != nil {
		if writeNodePathError(w, err, logReadForbiddenMsg) {
			return // a symlink out of /var/log, or no such log
		}
		writeAdminError(w, http.StatusInternalServerError, fmt.Sprintf("Не удалось прочитать файл: %s", strings.TrimSpace(string(output))))
		return
	}
	writeAdminJSON(w, http.StatusOK, map[string]any{"path": filePath, "content": string(output)})
}

const (
	logReadForbiddenMsg = "Можно читать только журналы в /var/log"
	listForbiddenMsg    = "Просматривать можно только /opt/heyearth и /var/log"
)

// nodePathRe: the only characters a node path may carry. Paths go into a root shell on the node,
// so anything a shell could expand, split or quote ($, `, ', ", spaces, ;, |, *, \) is refused
// outright, on top of the single-quoting in the commands.
var nodePathRe = regexp.MustCompile(`^[A-Za-z0-9._/@+-]+$`)

// safeNodePath: an absolute, already clean POSIX path of safe characters with no ".." segment.
func safeNodePath(p string) bool {
	if !strings.HasPrefix(p, "/") || p != path.Clean(p) || !nodePathRe.MatchString(p) {
		return false
	}
	for _, seg := range strings.Split(p, "/") {
		if seg == ".." {
			return false
		}
	}
	return true
}

func underDir(p, dir string) bool { return p == dir || strings.HasPrefix(p, dir+"/") }

// isAllowedReadPath: only logs. Configs and reality-keys.json on a node carry private keys and
// tunnel passwords, which never leave the Hub (Review Focus 5).
func isAllowedReadPath(p string) bool { return safeNodePath(p) && underDir(p, "/var/log") }

// isAllowedListPath: directory listings (names only) under /opt/heyearth and /var/log.
func isAllowedListPath(p string) bool {
	return safeNodePath(p) && (underDir(p, "/opt/heyearth") || underDir(p, "/var/log"))
}

// Exit statuses of the node path commands: the path resolves (symlinks followed on the node)
// outside the allowed directories, or it does not exist.
const (
	nodePathOutsideExit = 3
	nodePathMissingExit = 4
)

const nodePathMissingMsg = "Файл не найден"

// resolvedNodePathScript resolves p on the node (readlink -f), refuses a target outside the
// case patterns (exit nodePathOutsideExit) and a missing one (exit nodePathMissingExit), then
// runs action with the resolved path in "$p". p must already pass safeNodePath.
func resolvedNodePathScript(p, patterns, action string) string {
	outside, missing := strconv.Itoa(nodePathOutsideExit), strconv.Itoa(nodePathMissingExit)
	return "p=$(readlink -f -- " + shellquote.Quote(p) + ") || exit " + missing +
		`; case "$p" in ` + patterns + ") ;; *) exit " + outside + ";; esac" +
		`; [ -e "$p" ] || exit ` + missing + "; " + action
}

// listDirCommand lists a directory on the node (names only) if it resolves into /opt/heyearth
// or /var/log.
func listDirCommand(dir string) string {
	return resolvedNodePathScript(dir, "/opt/heyearth|/opt/heyearth/*|/var/log|/var/log/*", `ls -la -- "$p" 2>&1`)
}

// logReadCommand reads up to 1 MiB of a log on the node. Symlinks are resolved there first: a
// link in /var/log pointing at a config or a key is refused.
func logReadCommand(p string) string {
	return resolvedNodePathScript(p, "/var/log/*", `head -c 1048576 -- "$p"`)
}

// nodePathExitStatus maps an exit status of the node path commands to the HTTP answer; ok is
// false for any other failure.
func nodePathExitStatus(exit int, forbiddenMsg string) (code int, msg string, ok bool) {
	switch exit {
	case nodePathOutsideExit:
		return http.StatusForbidden, forbiddenMsg, true
	case nodePathMissingExit:
		return http.StatusNotFound, nodePathMissingMsg, true
	}
	return 0, "", false
}

// writeNodePathError answers a node path command's verdict (exit 3/4); false when err is not one.
func writeNodePathError(w http.ResponseWriter, err error, forbiddenMsg string) bool {
	var exitErr *ssh.ExitError
	if !errors.As(err, &exitErr) {
		return false
	}
	code, msg, ok := nodePathExitStatus(exitErr.ExitStatus(), forbiddenMsg)
	if ok {
		writeAdminError(w, code, msg)
	}
	return ok
}

func sshToServer(serverID int) (serverResponse, *ssh.Client, error) {
	var srv serverResponse
	var sshPwEnc []byte
	err := db.DB.QueryRow(`
		SELECT id, ip_address, ssh_port, ssh_user, COALESCE(ssh_password, ''), ssh_password_enc, ssh_key_id
		FROM servers WHERE id = $1
	`, serverID).Scan(&srv.ID, &srv.IPAddress, &srv.SSHPort, &srv.SSHUser, &srv.SSHPassword, &sshPwEnc, &srv.SshKeyID)
	if err != nil {
		return srv, nil, fmt.Errorf("server not found: %w", err)
	}
	if len(sshPwEnc) > 0 {
		if dec, decErr := crypto.Decrypt(sshPwEnc); decErr == nil {
			srv.SSHPassword = dec
		}
	}

	auths, err := sshAuthMethods(srv)
	if err != nil {
		return srv, nil, err
	}
	sshConfig := &ssh.ClientConfig{
		User:            srv.SSHUser,
		Auth:            auths,
		HostKeyCallback: toFUHostKeyCallback(srv.ID),
		Timeout:         10 * time.Second,
	}
	addr := fmt.Sprintf("%s:%d", srv.IPAddress, srv.SSHPort)
	client, err := ssh.Dial("tcp", addr, sshConfig)
	if err != nil {
		return srv, nil, fmt.Errorf("ssh dial: %w", err)
	}
	return srv, client, nil
}

// ── Provision progress endpoint ──

func ServerActionProgressHandler(w http.ResponseWriter, r *http.Request) {
	// Извлекаем server ID из пути: /api/admin/servers/{id}/actions/progress
	path := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/admin/servers/"), "/")
	serverIDText := strings.SplitN(path, "/", 2)[0]
	serverID, err := strconv.Atoi(serverIDText)
	if err != nil {
		writeAdminError(w, http.StatusBadRequest, "Неверный id сервера")
		return
	}
	v, ok := provisionProgress.Load(serverID)
	if !ok {
		writeAdminJSON(w, http.StatusOK, map[string]interface{}{"server_id": serverID, "active": false})
		return
	}
	writeAdminJSON(w, http.StatusOK, v)
}

// ── Exit hysteria scripts (the old setup is gone; phase 2 moves these to internal/config) ──

// exitHysteria2ServerScript renders the exit-side hysteria step of the old exit setup (run as
// root on the exit): install/upgrade the hysteria binary, write server.yaml (ending with the shared
// config.ExitHysteriaACL block for exitIP, with the exit's MTProxy port allowed on its own
// address) + hysteria2.service, restart.
// Plain concatenation, not a format string: the install script carries a literal %2F.
func exitHysteria2ServerScript(exitIP string, mtprotoPort int) string {
	if net.ParseIP(strings.TrimSpace(exitIP)) == nil {
		log.Printf("[provision] exit address %q is not an IP address: hysteria ACL written without the own-address reject", exitIP)
	}
	return "set -e\n" + config.HysteriaInstallScript() + `mkdir -p /opt/heyearth/certs /opt/heyearth/hysteria
if [ ! -f /opt/heyearth/certs/hy2.crt ]; then
  openssl req -x509 -newkey ec -pkeyopt ec_paramgen_curve:prime256v1 -days 3650 -nodes \
    -keyout /opt/heyearth/certs/hy2.key -out /opt/heyearth/certs/hy2.crt -subj "/CN=www.microsoft.com" 2>/dev/null
fi
SS_PASSWORD=$(python3 -c "import json; print(json.load(open('/opt/heyearth/state/reality-keys.json')).get('ssPassword',''))")
cat > /opt/heyearth/hysteria/server.yaml << HEOF
listen: :50443
tls:
  cert: /opt/heyearth/certs/hy2.crt
  key: /opt/heyearth/certs/hy2.key
auth:
  type: password
  password: $SS_PASSWORD
masquerade:
  type: proxy
  proxy:
    url: https://www.bing.com
    rewriteHost: true
quic:
  initStreamReceiveWindow: 26843545
  maxStreamReceiveWindow: 26843545
  initConnReceiveWindow: 67108864
  maxConnReceiveWindow: 67108864
bandwidth:
  up: 100 mbps
  down: 100 mbps
` + config.ExitHysteriaACL(exitIP, mtprotoPort) + `HEOF
cat > /etc/systemd/system/hysteria2.service << 'SEOF'
[Unit]
Description=Hysteria2 VPN Server (HeYearth)
After=network.target
[Service]
Type=simple
ExecStart=/usr/local/bin/hysteria server -c /opt/heyearth/hysteria/server.yaml
Restart=always
RestartSec=3
LimitNOFILE=65535
[Install]
WantedBy=multi-user.target
SEOF
systemctl daemon-reload && systemctl enable hysteria2 && systemctl restart hysteria2`
}

// hubHy2ClientSetupScript renders the Hub-side client step of the old exit setup (run locally on
// the Hub via bash -c): install the hysteria binary only if the Hub has none (config.HysteriaEnsureScript;
// an upgrade here would hit every Hub client on its next restart while other exits may still
// run an older server), then enable + restart the per-exit client unit. serviceName is
// server-derived ("hysteria2-client-<id>"), never user input.
func hubHy2ClientSetupScript(serviceName string) string {
	return fmt.Sprintf(`set -e
%s
systemctl daemon-reload
systemctl enable %s
systemctl restart %s
`, config.HysteriaEnsureScript(), serviceName, serviceName)
}

// runReinstallMTProto re-runs the install-mtproxy.sh on the exit's VPS.
// Reuses provisionProgress with a single-step progress for UI continuity.
func runReinstallMTProto(server serverResponse) {
	progress := &ProvisionProgress{
		ServerID: server.ID,
		Action:   "reinstall_mtproxy",
		Steps: []ProvisionStep{
			{Name: "install_mtproxy", Description: "Переустановка MTProto-прокси", Status: "running"},
		},
		Started: time.Now(),
	}
	provisionProgress.Store(server.ID, progress)
	finalize := func(status, msg string) {
		progress.Steps[0].Status = status
		if msg != "" {
			progress.Steps[0].Error = msg
		}
		progress.Finished = true
		provisionProgress.Store(server.ID, progress)
	}

	if err := biz.MarkMTProtoStatus(server.ID, "installing"); err != nil {
		log.Printf("[reinstall-mtproxy] mark installing %d: %v", server.ID, err)
	}
	scriptBytes, scriptErr := os.ReadFile("/opt/heyearth/scripts/install-mtproxy.sh")
	if scriptErr != nil {
		scriptBytes, scriptErr = os.ReadFile("server/backend/cmd/deploy/install-mtproxy.sh")
	}
	if scriptErr != nil {
		_ = biz.MarkMTProtoStatus(server.ID, "unhealthy")
		finalize("error", "install-mtproxy.sh not found on hub: "+scriptErr.Error())
		return
	}
	sshPort := server.SSHPort
	if sshPort == 0 {
		sshPort = 22
	}
	sshUser := server.SSHUser
	if sshUser == "" {
		sshUser = "root"
	}
	if instErr := biz.InstallMTProtoOnExit(server.ID, server.IPAddress, sshUser, server.SSHPassword, sshPort, scriptBytes); instErr != nil {
		_ = biz.MarkMTProtoStatus(server.ID, "unhealthy")
		finalize("error", instErr.Error())
		return
	}
	_ = biz.MarkMTProtoStatus(server.ID, "healthy")
	biz.BumpSubscriptionVersion()
	finalize("done", "")
	log.Printf("[reinstall-mtproxy] %d (%s): success", server.ID, server.Name)
}
