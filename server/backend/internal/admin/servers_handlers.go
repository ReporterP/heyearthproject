package admin

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/heyearth/backend/internal/biz"
	"github.com/heyearth/backend/internal/config"
	"github.com/heyearth/backend/internal/db"
	pbnode "github.com/heyearth/backend/internal/pb/node_agent_v1"
	"golang.org/x/crypto/ssh"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

// provisionProgress хранит текущий прогресс провижининга серверов (in-memory)
var provisionProgress sync.Map // map[int]*ProvisionProgress

func GetServersHandler(w http.ResponseWriter, r *http.Request) {
	p := parsePagination(r)

	where := "1=1"
	args := []any{}
	argN := 1
	if p.Search != "" {
		where += fmt.Sprintf(" AND (s.name ILIKE $%d OR s.ip_address ILIKE $%d)", argN, argN)
		args = append(args, "%"+p.Search+"%")
		argN++
	}
	if p.Status != "" {
		where += fmt.Sprintf(" AND s.status = $%d", argN)
		args = append(args, p.Status)
		argN++
	}

	var total int
	if err := db.DB.QueryRow("SELECT COUNT(*) FROM servers s WHERE "+where, args...).Scan(&total); err != nil {
		writeAdminError(w, http.StatusInternalServerError, "Failed to count servers")
		return
	}

	query := fmt.Sprintf(`
		SELECT s.id, s.name, s.ip_address, s.ssh_port, s.ssh_user, s.country_code, s.status,
		       s.load_metrics, COALESCE(s.updated_at, CURRENT_TIMESTAMP),
		       s.ssh_key_id, COALESCE(sk.name, ''),
		       s.is_exit_node, s.tunnel_status, s.tunnel_protocol, s.tunnel_port
		FROM servers s
		LEFT JOIN ssh_keys sk ON sk.id = s.ssh_key_id
		WHERE %s
		ORDER BY COALESCE(s.updated_at, CURRENT_TIMESTAMP) DESC, s.id DESC
		LIMIT $%d OFFSET $%d
	`, where, argN, argN+1)
	args = append(args, p.PageSize, p.Offset())

	rows, err := db.DB.Query(query, args...)
	if err != nil {
		writeAdminError(w, http.StatusInternalServerError, "Failed to query servers")
		return
	}
	defer rows.Close()

	var servers []serverResponse
	for rows.Next() {
		var s serverResponse
		var updatedAt time.Time
		var sshKeyName string
		if err := rows.Scan(&s.ID, &s.Name, &s.IPAddress, &s.SSHPort, &s.SSHUser, &s.CountryCode, &s.Status, &s.LoadMetrics, &updatedAt, &s.SshKeyID, &sshKeyName, &s.IsExitNode, &s.TunnelStatus, &s.TunnelProtocol, &s.TunnelPort); err != nil {
			writeAdminError(w, http.StatusInternalServerError, "Failed to scan server")
			return
		}
		if len(s.LoadMetrics) == 0 {
			s.LoadMetrics = json.RawMessage(`{}`)
		}
		if sshKeyName != "" {
			s.SshKeyName = sshKeyName
		}
		s.GroupIDs, _ = listServerGroupIDs(s.ID)
		s.UpdatedAt = updatedAt.Format(time.RFC3339)
		servers = append(servers, s)
	}
	writeAdminJSON(w, http.StatusOK, PagedResponse{Items: servers, Total: total, Page: p.Page, PageSize: p.PageSize})
}

func GetServerDetailHandler(w http.ResponseWriter, r *http.Request) {
	serverID, ok := parseLastPathInt(r.URL.Path)
	if !ok {
		writeAdminError(w, http.StatusBadRequest, "Invalid server id")
		return
	}
	server, err := loadServerByID(serverID, true)
	if err == sql.ErrNoRows {
		writeAdminError(w, http.StatusNotFound, "Server not found")
		return
	}
	if err != nil {
		writeAdminError(w, http.StatusInternalServerError, "Failed to load server")
		return
	}
	writeAdminJSON(w, http.StatusOK, server)
}

func AddServerHandler(w http.ResponseWriter, r *http.Request) {
	var req serverPayload
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeAdminError(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	if strings.TrimSpace(req.Name) == "" || strings.TrimSpace(req.IPAddress) == "" {
		writeAdminError(w, http.StatusBadRequest, "name and ip_address are required")
		return
	}
	if req.SSHPort == 0 {
		req.SSHPort = 22
	}
	if strings.TrimSpace(req.SSHUser) == "" {
		req.SSHUser = "root"
	}
	statusValue := strings.TrimSpace(req.Status)
	if statusValue == "" {
		statusValue = "provisioning"
	}

	tx, err := db.DB.Begin()
	if err != nil {
		writeAdminError(w, http.StatusInternalServerError, "Failed to begin transaction")
		return
	}
	defer tx.Rollback()

	// Все серверы, добавляемые через админку — exit nodes
	isExitNode := true

	var serverID int
	err = tx.QueryRow(`
		INSERT INTO servers (name, ip_address, ssh_port, ssh_user, ssh_password, ssh_key_id, country_code, status,
		                     is_exit_node, tunnel_protocol, tunnel_port, tunnel_uuid, tunnel_public_key,
		                     tunnel_private_key, tunnel_short_id, tunnel_sni, tunnel_ss_method, tunnel_ss_password, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, CURRENT_TIMESTAMP)
		RETURNING id
	`, strings.TrimSpace(req.Name), strings.TrimSpace(req.IPAddress), req.SSHPort, strings.TrimSpace(req.SSHUser),
		req.SSHPassword, req.SshKeyID, strings.TrimSpace(req.CountryCode), statusValue,
		isExitNode, req.TunnelProtocol, req.TunnelPort, req.TunnelUUID, req.TunnelPublicKey,
		req.TunnelPrivateKey, req.TunnelShortID, req.TunnelSNI, req.TunnelSSMethod, req.TunnelSSPass).Scan(&serverID)
	if err != nil {
		writeAdminError(w, http.StatusInternalServerError, "Failed to insert server")
		return
	}
	if err := replaceServerGroupsTx(tx, serverID, req.GroupIDs); err != nil {
		writeAdminError(w, http.StatusInternalServerError, "Failed to assign server groups")
		return
	}
	if err := tx.Commit(); err != nil {
		writeAdminError(w, http.StatusInternalServerError, "Failed to save server")
		return
	}

	WriteAuditLog(r, "create_server", "server", fmt.Sprintf("%d", serverID), map[string]string{"name": req.Name, "ip": req.IPAddress})
	writeAdminJSON(w, http.StatusCreated, map[string]any{"id": serverID, "message": "Server added successfully"})
}

func UpdateServerHandler(w http.ResponseWriter, r *http.Request) {
	serverID, ok := parseLastPathInt(r.URL.Path)
	if !ok {
		writeAdminError(w, http.StatusBadRequest, "Invalid server id")
		return
	}
	var req serverPayload
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeAdminError(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	existing, err := loadServerByID(serverID, true)
	if err == sql.ErrNoRows {
		writeAdminError(w, http.StatusNotFound, "Server not found")
		return
	}
	if err != nil {
		writeAdminError(w, http.StatusInternalServerError, "Failed to load server")
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
	if strings.TrimSpace(req.SSHPassword) == "" {
		req.SSHPassword = existing.SSHPassword
	}
	if req.SshKeyID == nil {
		req.SshKeyID = existing.SshKeyID
	}
	if strings.TrimSpace(req.CountryCode) == "" {
		req.CountryCode = existing.CountryCode
	}
	if strings.TrimSpace(req.Status) == "" {
		req.Status = existing.Status
	}
	if req.GroupIDs == nil {
		req.GroupIDs = existing.GroupIDs
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

	tx, err := db.DB.Begin()
	if err != nil {
		writeAdminError(w, http.StatusInternalServerError, "Failed to begin transaction")
		return
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`
		UPDATE servers
		SET name = $1, ip_address = $2, ssh_port = $3, ssh_user = $4, ssh_password = $5,
		    ssh_key_id = $6, country_code = $7, status = $8,
		    is_exit_node = $9, tunnel_protocol = $10, tunnel_port = $11,
		    tunnel_uuid = $12, tunnel_public_key = $13, tunnel_private_key = $14,
		    tunnel_short_id = $15, tunnel_sni = $16, tunnel_ss_method = $17,
		    tunnel_ss_password = $18, updated_at = CURRENT_TIMESTAMP
		WHERE id = $19
	`, req.Name, req.IPAddress, req.SSHPort, req.SSHUser, req.SSHPassword, req.SshKeyID, req.CountryCode, req.Status,
		isExitNode, req.TunnelProtocol, req.TunnelPort,
		req.TunnelUUID, req.TunnelPublicKey, req.TunnelPrivateKey, req.TunnelShortID,
		req.TunnelSNI, req.TunnelSSMethod, req.TunnelSSPass, serverID); err != nil {
		writeAdminError(w, http.StatusInternalServerError, "Failed to update server")
		return
	}
	if err := replaceServerGroupsTx(tx, serverID, req.GroupIDs); err != nil {
		writeAdminError(w, http.StatusInternalServerError, "Failed to assign groups")
		return
	}
	if err := tx.Commit(); err != nil {
		writeAdminError(w, http.StatusInternalServerError, "Failed to save server")
		return
	}
	WriteAuditLog(r, "update_server", "server", fmt.Sprintf("%d", serverID), nil)
	writeAdminJSON(w, http.StatusOK, map[string]string{"message": "Server updated"})
}

func DeleteServerHandler(w http.ResponseWriter, r *http.Request) {
	serverID, ok := parseLastPathInt(r.URL.Path)
	if !ok {
		writeAdminError(w, http.StatusBadRequest, "Invalid server id")
		return
	}
	if _, err := db.DB.Exec(`DELETE FROM servers WHERE id = $1`, serverID); err != nil {
		writeAdminError(w, http.StatusInternalServerError, "Failed to delete server")
		return
	}
	WriteAuditLog(r, "delete_server", "server", fmt.Sprintf("%d", serverID), nil)
	writeAdminJSON(w, http.StatusOK, map[string]string{"message": "Server deleted"})
}

func ServerActionHandler(w http.ResponseWriter, r *http.Request) {
	path := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/admin/servers/"), "/")
	if !strings.HasSuffix(path, "/actions") {
		writeAdminError(w, http.StatusBadRequest, "Invalid server action path")
		return
	}
	serverIDText := strings.TrimSuffix(path, "/actions")
	serverIDText = strings.Trim(serverIDText, "/")
	serverID, err := strconv.Atoi(serverIDText)
	if err != nil {
		writeAdminError(w, http.StatusBadRequest, "Invalid server id")
		return
	}
	server, err := loadServerByID(serverID, true)
	if err == sql.ErrNoRows {
		writeAdminError(w, http.StatusNotFound, "Server not found")
		return
	}
	if err != nil {
		writeAdminError(w, http.StatusInternalServerError, "Failed to load server")
		return
	}

	var payload serverActionPayload
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		writeAdminError(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	switch strings.TrimSpace(payload.Action) {
	case "sync_metrics":
		if err := syncServerMetrics(server); err != nil {
			writeAdminError(w, http.StatusBadGateway, err.Error())
			return
		}
	case "restart_agent":
		if err := restartNodeAgent(server); err != nil {
			writeAdminError(w, http.StatusBadGateway, err.Error())
			return
		}
	case "setup_exit_node", "reprovision":
		// Проверка: не запущен ли уже провижининг для этого сервера
		if v, ok := provisionProgress.Load(serverID); ok {
			pp := v.(*ProvisionProgress)
			if !pp.Finished {
				writeAdminError(w, http.StatusConflict, "Provisioning already in progress")
				return
			}
		}
		// Запускаем асинхронно
		go runSetupExitNode(server)
		writeAdminJSON(w, http.StatusAccepted, map[string]string{"message": "Provisioning started"})
		return
	case "sync_tunnel":
		if err := syncTunnel(server); err != nil {
			writeAdminError(w, http.StatusBadGateway, err.Error())
			return
		}
	case "fetch_tunnel_creds":
		if err := fetchTunnelCreds(server); err != nil {
			writeAdminError(w, http.StatusBadGateway, err.Error())
			return
		}
	default:
		writeAdminError(w, http.StatusBadRequest, "Unsupported server action")
		return
	}
	writeAdminJSON(w, http.StatusOK, map[string]string{"message": "Server action completed"})
}

// ── Helpers ──────────────────────────────────────────────

func loadServerByID(serverID int, includePassword bool) (serverResponse, error) {
	var server serverResponse
	var updatedAt time.Time
	var sshKeyName string
	if includePassword {
		query := `
			SELECT s.id, s.name, s.ip_address, s.ssh_port, s.ssh_user, s.ssh_password,
			       s.country_code, s.status, s.load_metrics, COALESCE(s.updated_at, CURRENT_TIMESTAMP),
			       s.ssh_key_id, COALESCE(sk.name, ''),
			       s.is_exit_node, s.tunnel_status, s.tunnel_protocol, s.tunnel_port,
			       COALESCE(s.tunnel_uuid, ''), COALESCE(s.tunnel_public_key, ''),
			       COALESCE(s.tunnel_private_key, ''), COALESCE(s.tunnel_short_id, ''),
			       COALESCE(s.tunnel_sni, ''),
			       COALESCE(s.tunnel_ss_method, ''), COALESCE(s.tunnel_ss_password, '')
			FROM servers s
			LEFT JOIN ssh_keys sk ON sk.id = s.ssh_key_id
			WHERE s.id = $1
		`
		err := db.DB.QueryRow(query, serverID).Scan(
			&server.ID, &server.Name, &server.IPAddress, &server.SSHPort, &server.SSHUser,
			&server.SSHPassword, &server.CountryCode, &server.Status, &server.LoadMetrics,
			&updatedAt, &server.SshKeyID, &sshKeyName,
			&server.IsExitNode, &server.TunnelStatus, &server.TunnelProtocol, &server.TunnelPort,
			&server.TunnelUUID, &server.TunnelPublicKey, &server.TunnelPrivateKey,
			&server.TunnelShortID, &server.TunnelSNI,
			&server.TunnelSSMethod, &server.TunnelSSPass)
		if err != nil {
			return server, err
		}
	} else {
		query := `
			SELECT s.id, s.name, s.ip_address, s.ssh_port, s.ssh_user,
			       s.country_code, s.status, s.load_metrics, COALESCE(s.updated_at, CURRENT_TIMESTAMP),
			       s.ssh_key_id, COALESCE(sk.name, ''),
			       s.is_exit_node, s.tunnel_status, s.tunnel_protocol, s.tunnel_port,
			       COALESCE(s.tunnel_uuid, ''), COALESCE(s.tunnel_public_key, ''),
			       COALESCE(s.tunnel_private_key, ''), COALESCE(s.tunnel_short_id, ''),
			       COALESCE(s.tunnel_sni, ''),
			       COALESCE(s.tunnel_ss_method, ''), COALESCE(s.tunnel_ss_password, '')
			FROM servers s
			LEFT JOIN ssh_keys sk ON sk.id = s.ssh_key_id
			WHERE s.id = $1
		`
		err := db.DB.QueryRow(query, serverID).Scan(
			&server.ID, &server.Name, &server.IPAddress, &server.SSHPort, &server.SSHUser,
			&server.CountryCode, &server.Status, &server.LoadMetrics,
			&updatedAt, &server.SshKeyID, &sshKeyName,
			&server.IsExitNode, &server.TunnelStatus, &server.TunnelProtocol, &server.TunnelPort,
			&server.TunnelUUID, &server.TunnelPublicKey, &server.TunnelPrivateKey,
			&server.TunnelShortID, &server.TunnelSNI,
			&server.TunnelSSMethod, &server.TunnelSSPass)
		if err != nil {
			return server, err
		}
	}
	if sshKeyName != "" {
		server.SshKeyName = sshKeyName
	}
	server.GroupIDs, _ = listServerGroupIDs(server.ID)
	server.UpdatedAt = updatedAt.Format(time.RFC3339)
	if len(server.LoadMetrics) == 0 {
		server.LoadMetrics = json.RawMessage(`{}`)
	}
	return server, nil
}

func listServerGroupIDs(serverID int) ([]int, error) {
	rows, err := db.DB.Query(`SELECT group_id FROM server_groups WHERE server_id = $1 ORDER BY group_id ASC`, serverID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var groupIDs []int
	for rows.Next() {
		var groupID int
		if err := rows.Scan(&groupID); err != nil {
			return nil, err
		}
		groupIDs = append(groupIDs, groupID)
	}
	return groupIDs, rows.Err()
}

func replaceServerGroupsTx(tx *sql.Tx, serverID int, groupIDs []int) error {
	if _, err := tx.Exec(`DELETE FROM server_groups WHERE server_id = $1`, serverID); err != nil {
		return err
	}
	for _, groupID := range groupIDs {
		if _, err := tx.Exec(`INSERT INTO server_groups (server_id, group_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`, serverID, groupID); err != nil {
			return err
		}
	}
	return nil
}

// ── Server actions (sync, restart, reprovision) ──────────

func syncServerMetrics(server serverResponse) error {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	client, conn, err := connectToNodeAgent(ctx, server.IPAddress)
	if err != nil {
		return fmt.Errorf("connect node-agent: %w", err)
	}
	defer conn.Close()

	resp, err := client.GetMetrics(ctx, &pbnode.GetMetricsRequest{})
	if err != nil {
		return fmt.Errorf("get metrics: %w", err)
	}
	metricsBytes, _ := json.Marshal(map[string]any{
		"cpu_usage":          resp.GetCpuUsage(),
		"memory_usage":       resp.GetMemoryUsage(),
		"active_connections": resp.GetActiveConnections(),
		"synced_at":          time.Now().Format(time.RFC3339),
	})
	_, err = db.DB.Exec(`UPDATE servers SET load_metrics = $1::jsonb, status = 'online', updated_at = CURRENT_TIMESTAMP WHERE id = $2`, string(metricsBytes), server.ID)
	if err != nil {
		return err
	}

	// Если exit-нода вернула tunnel credentials — автоматически обновляем в БД
	// и переключаем протокол на shadowsocks (быстрее VLESS для Hub→Exit)
	if server.IsExitNode && resp.GetTunnelSsPassword() != "" {
		ssPass := resp.GetTunnelSsPassword()
		tunnelUUID := resp.GetTunnelUuid()
		pubKey := resp.GetTunnelPublicKey()
		shortID := resp.GetTunnelShortId()

		_, err = db.DB.Exec(`
			UPDATE servers SET
				tunnel_protocol = 'hysteria2',
				tunnel_port = 50443,
				tunnel_ss_method = '2022-blake3-aes-128-gcm',
				tunnel_ss_password = $1,
				tunnel_uuid = COALESCE(NULLIF($2, ''), tunnel_uuid),
				tunnel_public_key = COALESCE(NULLIF($3, ''), tunnel_public_key),
				tunnel_short_id = COALESCE(NULLIF($4, ''), tunnel_short_id),
				updated_at = CURRENT_TIMESTAMP
			WHERE id = $5
		`, ssPass, tunnelUUID, pubKey, shortID, server.ID)
		if err != nil {
			log.Printf("[sync-metrics] Ошибка обновления tunnel creds для сервера %d: %v", server.ID, err)
		} else {
			log.Printf("[sync-metrics] Сервер %d: tunnel credentials обновлены, протокол → shadowsocks:8388", server.ID)
		}
	}
	return nil
}

func restartNodeAgent(server serverResponse) error {
	if err := runSSHCommand(server, "systemctl restart node-agent"); err != nil {
		return fmt.Errorf("restart node-agent: %w", err)
	}
	if _, err := db.DB.Exec(`UPDATE servers SET status = 'online', updated_at = CURRENT_TIMESTAMP WHERE id = $1`, server.ID); err != nil {
		return err
	}
	return nil
}

// fetchTunnelCreds читает tunnel credentials с exit-ноды по SSH
// и автоматически переключает протокол на Shadowsocks
func fetchTunnelCreds(server serverResponse) error {
	if !server.IsExitNode {
		return fmt.Errorf("сервер не является exit-нодой")
	}
	// Читаем reality-keys.json с exit-ноды
	output, err := runSSHCommandOutput(server, "cat /opt/heyearth/state/reality-keys.json")
	if err != nil {
		return fmt.Errorf("чтение ключей: %w", err)
	}
	var keys struct {
		PrivateKey string `json:"privateKey"`
		PublicKey  string `json:"publicKey"`
		ShortID    string `json:"shortId"`
		UUID       string `json:"uuid"`
		SSPassword string `json:"ssPassword"`
	}
	if err := json.Unmarshal([]byte(output), &keys); err != nil {
		return fmt.Errorf("парсинг ключей: %w", err)
	}
	if keys.SSPassword == "" {
		return fmt.Errorf("SS пароль не найден в reality-keys.json")
	}

	// Обновляем в БД: переключаем на Shadowsocks
	_, err = db.DB.Exec(`
		UPDATE servers SET
			tunnel_protocol = 'shadowsocks',
			tunnel_port = 8388,
			tunnel_ss_method = '2022-blake3-aes-128-gcm',
			tunnel_ss_password = $1,
			tunnel_uuid = $2,
			tunnel_public_key = $3,
			tunnel_short_id = $4,
			tunnel_private_key = $5,
			updated_at = CURRENT_TIMESTAMP
		WHERE id = $6
	`, keys.SSPassword, keys.UUID, keys.PublicKey, keys.ShortID, keys.PrivateKey, server.ID)
	if err != nil {
		return fmt.Errorf("обновление БД: %w", err)
	}
	log.Printf("[fetch-creds] Сервер %d: tunnel credentials получены, протокол → shadowsocks:8388", server.ID)
	return nil
}

// runSSHCommandOutput выполняет SSH команду и возвращает stdout
func runSSHCommandOutput(server serverResponse, command string) (string, error) {
	authMethods, err := sshAuthMethods(server)
	if err != nil {
		return "", err
	}
	cfg := &ssh.ClientConfig{
		User:            server.SSHUser,
		Auth:            authMethods,
		HostKeyCallback: ssh.InsecureIgnoreHostKey(), //nolint:gosec
		Timeout:         30 * time.Second,
	}
	addr := net.JoinHostPort(server.IPAddress, fmt.Sprintf("%d", server.SSHPort))
	client, err := ssh.Dial("tcp", addr, cfg)
	if err != nil {
		return "", fmt.Errorf("ssh dial %s: %w", addr, err)
	}
	defer client.Close()

	sess, err := client.NewSession()
	if err != nil {
		return "", fmt.Errorf("ssh session: %w", err)
	}
	defer sess.Close()

	var outBuf, errBuf bytes.Buffer
	sess.Stdout = &outBuf
	sess.Stderr = &errBuf
	if err := sess.Run(command); err != nil {
		return "", fmt.Errorf("command %q: %v: %s", command, err, strings.TrimSpace(errBuf.String()))
	}
	return strings.TrimSpace(outBuf.String()), nil
}

func syncTunnel(server serverResponse) error {
	// Check tunnel health: try TCP connect to tunnel port on the exit node
	port := server.TunnelPort
	if port == 0 {
		port = 443
	}
	addr := fmt.Sprintf("%s:%d", server.IPAddress, port)
	conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
	status := "healthy"
	if err != nil {
		status = "unhealthy"
	} else {
		conn.Close()
	}
	// Update tunnel_status in DB
	if _, dbErr := db.DB.Exec(`UPDATE servers SET tunnel_status = $1, updated_at = CURRENT_TIMESTAMP WHERE id = $2`, status, server.ID); dbErr != nil {
		return fmt.Errorf("update tunnel status: %w", dbErr)
	}
	if err != nil {
		return fmt.Errorf("tunnel check failed (%s): %w", addr, err)
	}
	return nil
}

func reprovisionServer(server serverResponse) error {
	ansiblePlaybookPath, err := resolveExecutablePath("ansible-playbook", "/usr/bin/ansible-playbook", "/bin/ansible-playbook")
	if err != nil {
		return fmt.Errorf("ansible reprovision: %w", err)
	}
	playbook := filepath.Join(config.AppDir(), "ansible", "playbook.yml")

	args := []string{
		"-i", fmt.Sprintf("%s,", server.IPAddress),
		playbook,
		"-u", server.SSHUser,
		"-e", fmt.Sprintf("ansible_port=%d", server.SSHPort),
		"-e", fmt.Sprintf("ansible_host=%s", server.IPAddress),
		"-e", fmt.Sprintf("ss_public_host=%s", server.IPAddress),
	}

	env := append(os.Environ(),
		"ANSIBLE_HOST_KEY_CHECKING=False",
		"ANSIBLE_STDOUT_CALLBACK=minimal",
	)

	var keyTmpFile string
	if server.SshKeyID != nil && *server.SshKeyID > 0 {
		key, err := biz.GetSshKeyByID(*server.SshKeyID)
		if err != nil {
			return fmt.Errorf("load ssh key: %w", err)
		}
		f, err := os.CreateTemp("", "ansible-key-*.pem")
		if err != nil {
			return fmt.Errorf("create temp key file: %w", err)
		}
		keyTmpFile = f.Name()
		if _, err := f.WriteString(key.PrivateKey); err != nil {
			f.Close()
			os.Remove(keyTmpFile)
			return fmt.Errorf("write temp key file: %w", err)
		}
		f.Close()
		os.Chmod(keyTmpFile, 0600)
		defer os.Remove(keyTmpFile)
		args = append(args, "--private-key", keyTmpFile)
	} else if strings.TrimSpace(server.SSHPassword) != "" {
		args = append(args, "-e", fmt.Sprintf("ansible_password=%s", server.SSHPassword))
	} else {
		return fmt.Errorf("neither ssh_key_id nor ssh_password is set for reprovision")
	}

	cmd := exec.Command(ansiblePlaybookPath, args...)
	cmd.Env = env
	cmd.Dir = config.AppDir()
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("ansible reprovision: %v: %s", err, strings.TrimSpace(string(output)))
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
		HostKeyCallback: ssh.InsecureIgnoreHostKey(), //nolint:gosec
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

func resolveExecutablePath(name string, candidates ...string) (string, error) {
	if path, err := exec.LookPath(name); err == nil {
		return path, nil
	}
	for _, candidate := range candidates {
		if _, err := os.Stat(candidate); err == nil {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("%q executable not found; install it on the main server", name)
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
		writeAdminError(w, http.StatusBadRequest, "invalid server id")
		return
	}
	var ipAddress string
	var sshPort int
	err := db.DB.QueryRow(`SELECT ip_address, ssh_port FROM servers WHERE id = $1`, serverID).Scan(&ipAddress, &sshPort)
	if err != nil {
		writeAdminError(w, http.StatusNotFound, "server not found")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	agentClient, conn, err := connectToNodeAgent(ctx, ipAddress)
	if err != nil {
		writeAdminJSON(w, http.StatusOK, map[string]any{
			"error": fmt.Sprintf("cannot reach node-agent: %v", err),
		})
		return
	}
	defer conn.Close()

	metrics, err := agentClient.GetMetrics(ctx, &pbnode.GetMetricsRequest{})
	if err != nil {
		writeAdminJSON(w, http.StatusOK, map[string]any{
			"error": fmt.Sprintf("GetMetrics failed: %v", err),
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
		writeAdminError(w, http.StatusBadRequest, "invalid server id")
		return
	}
	service := r.URL.Query().Get("service")
	if service == "" {
		service = "node-agent"
	}
	tail := r.URL.Query().Get("tail")
	if tail == "" {
		tail = "100"
	}

	server, sshClient, err := sshToServer(serverID)
	if err != nil {
		writeAdminError(w, http.StatusInternalServerError, fmt.Sprintf("SSH failed: %v", err))
		return
	}
	_ = server
	defer sshClient.Close()

	session, err := sshClient.NewSession()
	if err != nil {
		writeAdminError(w, http.StatusInternalServerError, "SSH session failed")
		return
	}
	defer session.Close()

	cmd := fmt.Sprintf("journalctl -u %s --no-pager -n %s --output=short-iso 2>&1", service, tail)
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
		writeAdminError(w, http.StatusBadRequest, "invalid server id")
		return
	}
	dirPath := r.URL.Query().Get("path")
	if dirPath == "" {
		dirPath = "/opt/heyearth"
	}
	allowedPrefixes := []string{"/opt/heyearth", "/var/log", "/etc/shadowsocks-libev"}
	allowed := false
	for _, prefix := range allowedPrefixes {
		if strings.HasPrefix(dirPath, prefix) {
			allowed = true
			break
		}
	}
	if !allowed {
		writeAdminError(w, http.StatusForbidden, "path not allowed")
		return
	}

	_, sshClient, err := sshToServer(serverID)
	if err != nil {
		writeAdminError(w, http.StatusInternalServerError, fmt.Sprintf("SSH failed: %v", err))
		return
	}
	defer sshClient.Close()

	session, err := sshClient.NewSession()
	if err != nil {
		writeAdminError(w, http.StatusInternalServerError, "SSH session failed")
		return
	}
	defer session.Close()

	cmd := fmt.Sprintf("ls -la %s 2>&1", dirPath)
	output, _ := session.CombinedOutput(cmd)
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

func sshToServer(serverID int) (serverResponse, *ssh.Client, error) {
	var srv serverResponse
	err := db.DB.QueryRow(`
		SELECT id, ip_address, ssh_port, ssh_user, COALESCE(ssh_password, ''), ssh_key_id
		FROM servers WHERE id = $1
	`, serverID).Scan(&srv.ID, &srv.IPAddress, &srv.SSHPort, &srv.SSHUser, &srv.SSHPassword, &srv.SshKeyID)
	if err != nil {
		return srv, nil, fmt.Errorf("server not found: %w", err)
	}

	auths, err := sshAuthMethods(srv)
	if err != nil {
		return srv, nil, err
	}
	sshConfig := &ssh.ClientConfig{
		User:            srv.SSHUser,
		Auth:            auths,
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
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
		writeAdminError(w, http.StatusBadRequest, "Invalid server id")
		return
	}
	v, ok := provisionProgress.Load(serverID)
	if !ok {
		writeAdminJSON(w, http.StatusOK, map[string]interface{}{"server_id": serverID, "active": false})
		return
	}
	writeAdminJSON(w, http.StatusOK, v)
}

// ── Setup exit node (async, 7 steps) ──

func runSetupExitNode(server serverResponse) {
	steps := []ProvisionStep{
		{Name: "optimize_network", Description: "Оптимизация сетевого стека (BBR, TCP буферы)", Status: "pending"},
		{Name: "install_xray", Description: "Установка Xray (SS+WS:443, VLESS:10443)", Status: "pending"},
		{Name: "install_hysteria2", Description: "Установка Hysteria2 (UDP:50443)", Status: "pending"},
		{Name: "port_hopping", Description: "Настройка port hopping (nftables)", Status: "pending"},
		{Name: "configure_firewall", Description: "Настройка файрвола", Status: "pending"},
		{Name: "update_credentials", Description: "Обновление credentials в БД", Status: "pending"},
		{Name: "setup_hub_client", Description: "Настройка Hysteria2 клиента на Hub", Status: "pending"},
	}
	progress := &ProvisionProgress{
		ServerID: server.ID,
		Action:   "setup_exit_node",
		Steps:    steps,
		Started:  time.Now(),
	}
	provisionProgress.Store(server.ID, progress)

	setStep := func(idx int, status, errMsg string) {
		progress.Steps[idx].Status = status
		if errMsg != "" {
			progress.Steps[idx].Error = errMsg
		}
		provisionProgress.Store(server.ID, progress)
	}

	finishWithError := func(stepIdx int, err error) {
		setStep(stepIdx, "error", err.Error())
		progress.Finished = true
		provisionProgress.Store(server.ID, progress)
		log.Printf("[provision] Сервер %d (%s): ошибка на шаге %s: %v", server.ID, server.Name, progress.Steps[stepIdx].Name, err)
	}

	// SSH подключение к exit-ноде
	client, err := sshDialForServer(server)
	if err != nil {
		finishWithError(0, fmt.Errorf("SSH: %v", err))
		return
	}
	defer client.Close()

	runCmd := func(c *ssh.Client, cmd string) (string, error) {
		session, err := c.NewSession()
		if err != nil {
			return "", err
		}
		defer session.Close()
		var stdout, stderr bytes.Buffer
		session.Stdout = &stdout
		session.Stderr = &stderr
		if err := session.Run(cmd); err != nil {
			return stdout.String(), fmt.Errorf("%s: %s", err, stderr.String())
		}
		return stdout.String(), nil
	}

	// ── Шаг 1: BBR + TCP ──
	setStep(0, "running", "")
	_, err = runCmd(client, `cat > /etc/sysctl.d/99-bbr.conf << 'EOF'
net.core.default_qdisc = fq
net.ipv4.tcp_congestion_control = bbr
net.core.rmem_max = 16777216
net.core.wmem_max = 16777216
net.ipv4.tcp_rmem = 4096 131072 16777216
net.ipv4.tcp_wmem = 4096 131072 16777216
net.ipv4.tcp_fastopen = 3
net.ipv4.tcp_slow_start_after_idle = 0
net.ipv4.tcp_mtu_probing = 1
net.ipv6.conf.all.disable_ipv6 = 1
net.ipv6.conf.default.disable_ipv6 = 1
EOF
sysctl -p /etc/sysctl.d/99-bbr.conf 2>/dev/null || true`)
	if err != nil {
		finishWithError(0, err)
		return
	}
	setStep(0, "done", "")

	// ── Шаг 2: Xray ──
	setStep(1, "running", "")
	_, err = runCmd(client, `set -e
mkdir -p /opt/heyearth/xray /opt/heyearth/state
if [ ! -f /opt/heyearth/xray/xray ]; then
  apt-get update -qq 2>/dev/null
  apt-get install -y -qq wget unzip 2>/dev/null
  cd /tmp && wget -q https://github.com/XTLS/Xray-core/releases/download/v25.1.1/Xray-linux-64.zip -O xray.zip
  unzip -o xray.zip -d /opt/heyearth/xray && chmod +x /opt/heyearth/xray/xray && rm -f xray.zip
fi

# Генерируем ключи если нет
KEYS_FILE="/opt/heyearth/state/reality-keys.json"
if [ ! -f "$KEYS_FILE" ]; then
  KEYS=$(/opt/heyearth/xray/xray x25519 2>&1)
  PRIV_KEY=$(echo "$KEYS" | grep 'Private key:' | awk '{print $3}')
  PUB_KEY=$(echo "$KEYS" | grep 'Public key:' | awk '{print $3}')
  SHORT_ID=$(head -c 8 /dev/urandom | xxd -p)
  UUID=$(cat /proc/sys/kernel/random/uuid)
  SS_PASSWORD=$(openssl rand -base64 16)
  python3 -c "
import json
d={'privateKey':'$PRIV_KEY','publicKey':'$PUB_KEY','shortId':'$SHORT_ID','uuid':'$UUID','ssPassword':'$SS_PASSWORD'}
json.dump(d, open('$KEYS_FILE','w'), indent=2)
"
else
  python3 -c "
import json, os, base64
with open('$KEYS_FILE') as f: k=json.load(f)
changed=False
if 'uuid' not in k or not k['uuid']: k['uuid']=open('/proc/sys/kernel/random/uuid').read().strip(); changed=True
if 'ssPassword' not in k or not k['ssPassword']: k['ssPassword']=base64.b64encode(os.urandom(16)).decode(); changed=True
if changed: json.dump(k, open('$KEYS_FILE','w'), indent=2)
"
fi

# Читаем ключи
eval $(python3 -c "
import json
k=json.load(open('/opt/heyearth/state/reality-keys.json'))
print(f'PRIV_KEY={k[\"privateKey\"]}')
print(f'PUB_KEY={k[\"publicKey\"]}')
print(f'SHORT_ID={k[\"shortId\"]}')
print(f'UUID={k[\"uuid\"]}')
print(f'SS_PASSWORD={k.get(\"ssPassword\",\"\")}')
")

# Конфиг Xray: SS+WS на 443, VLESS Reality на 10443
cat > /opt/heyearth/xray/config.json << XEOF
{
  "log":{"loglevel":"warning"},
  "dns":{"servers":["8.8.8.8","1.1.1.1"],"queryStrategy":"UseIPv4","disableCache":false},
  "inbounds":[
    {"tag":"shadowsocks-ws","listen":"0.0.0.0","port":443,"protocol":"shadowsocks",
     "settings":{"method":"2022-blake3-aes-128-gcm","password":"$SS_PASSWORD","network":"tcp,udp"},
     "streamSettings":{"network":"ws","wsSettings":{"path":"/stream"},"sockopt":{"tcpFastOpen":true,"tcpNoDelay":true,"tcpKeepAliveInterval":30}},
     "sniffing":{"enabled":true,"destOverride":["http","tls","quic"]}},
    {"tag":"vless-reality","listen":"0.0.0.0","port":10443,"protocol":"vless",
     "settings":{"clients":[{"id":"$UUID","flow":"xtls-rprx-vision"}],"decryption":"none"},
     "streamSettings":{"network":"tcp","security":"reality","realitySettings":{"show":false,"dest":"microsoft.com:443","xver":0,"serverNames":["microsoft.com","www.microsoft.com"],"privateKey":"$PRIV_KEY","shortIds":["$SHORT_ID"]}},
     "sniffing":{"enabled":true,"destOverride":["http","tls","quic"]}}
  ],
  "outbounds":[
    {"protocol":"freedom","tag":"direct","settings":{"domainStrategy":"UseIPv4"},"streamSettings":{"sockopt":{"tcpFastOpen":true,"tcpNoDelay":true,"tcpKeepAliveInterval":30}}},
    {"protocol":"blackhole","tag":"block","settings":{"response":{"type":"none"}}}
  ],
  "routing":{"domainStrategy":"IPIfNonMatch","rules":[
    {"type":"field","ip":["geoip:private"],"outboundTag":"block"},
    {"type":"field","protocol":["bittorrent"],"outboundTag":"block"}
  ]},
  "policy":{"levels":{"0":{"handshake":4,"connIdle":300,"uplinkOnly":2,"downlinkOnly":5,"bufferSize":512}}}
}
XEOF

cat > /etc/systemd/system/xray.service << 'SEOF'
[Unit]
Description=Xray Exit Node (HeYearth)
After=network.target
[Service]
Type=simple
ExecStart=/opt/heyearth/xray/xray run -config /opt/heyearth/xray/config.json
Restart=always
RestartSec=5
LimitNOFILE=65535
[Install]
WantedBy=multi-user.target
SEOF
systemctl daemon-reload && systemctl enable xray && systemctl restart xray`)
	if err != nil {
		finishWithError(1, err)
		return
	}
	setStep(1, "done", "")

	// ── Шаг 3: Hysteria2 ──
	setStep(2, "running", "")
	_, err = runCmd(client, `set -e
if [ ! -f /usr/local/bin/hysteria ]; then
  curl -sL -o /usr/local/bin/hysteria https://github.com/apernet/hysteria/releases/download/app%2Fv2.6.1/hysteria-linux-amd64
  chmod +x /usr/local/bin/hysteria
fi
mkdir -p /opt/heyearth/certs /opt/heyearth/hysteria
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
HEOF
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
systemctl daemon-reload && systemctl enable hysteria2 && systemctl restart hysteria2`)
	if err != nil {
		finishWithError(2, err)
		return
	}
	setStep(2, "done", "")

	// ── Шаг 4: Port hopping ──
	setStep(3, "running", "")
	_, err = runCmd(client, `set -e
apt-get install -y -qq nftables 2>/dev/null || true
cat > /etc/nftables-hysteria.conf << 'NEOF'
table inet hysteria_porthopping {
  chain prerouting {
    type nat hook prerouting priority dstnat; policy accept;
    udp dport 20000-50000 counter redirect to :50443
  }
}
NEOF
nft -f /etc/nftables-hysteria.conf 2>/dev/null || true
cat > /etc/systemd/system/nftables-hysteria.service << 'SEOF'
[Unit]
Description=nftables port hopping for Hysteria2
After=network.target
[Service]
Type=oneshot
ExecStart=/usr/sbin/nft -f /etc/nftables-hysteria.conf
RemainAfterExit=yes
[Install]
WantedBy=multi-user.target
SEOF
systemctl daemon-reload && systemctl enable nftables-hysteria 2>/dev/null || true`)
	if err != nil {
		finishWithError(3, err)
		return
	}
	setStep(3, "done", "")

	// ── Шаг 5: Firewall ──
	setStep(4, "running", "")
	_, err = runCmd(client, `
if command -v ufw >/dev/null 2>&1; then
  ufw allow 22/tcp 2>/dev/null; ufw allow 443/tcp 2>/dev/null; ufw allow 443/udp 2>/dev/null
  ufw allow 10443/tcp 2>/dev/null; ufw allow 50443/udp 2>/dev/null
  ufw allow 20000:50000/udp 2>/dev/null; ufw --force enable 2>/dev/null
else
  iptables -I INPUT -p tcp --dport 443 -j ACCEPT 2>/dev/null
  iptables -I INPUT -p udp --dport 443 -j ACCEPT 2>/dev/null
  iptables -I INPUT -p tcp --dport 10443 -j ACCEPT 2>/dev/null
  iptables -I INPUT -p udp --dport 50443 -j ACCEPT 2>/dev/null
  iptables -I INPUT -p udp --dport 20000:50000 -j ACCEPT 2>/dev/null
fi
true`)
	if err != nil {
		finishWithError(4, err)
		return
	}
	setStep(4, "done", "")

	// ── Шаг 6: Credentials → DB ──
	setStep(5, "running", "")
	credsOut, err := runCmd(client, `python3 -c "
import json
k=json.load(open('/opt/heyearth/state/reality-keys.json'))
print(k.get('uuid',''))
print(k.get('publicKey',''))
print(k.get('shortId',''))
print(k.get('ssPassword',''))
print(k.get('privateKey',''))
"`)
	if err != nil {
		finishWithError(5, err)
		return
	}
	lines := strings.Split(strings.TrimSpace(credsOut), "\n")
	if len(lines) < 4 {
		finishWithError(5, fmt.Errorf("unexpected credentials format: %d lines", len(lines)))
		return
	}
	tunnelUUID := strings.TrimSpace(lines[0])
	tunnelPubKey := strings.TrimSpace(lines[1])
	tunnelShortID := strings.TrimSpace(lines[2])
	tunnelSSPass := strings.TrimSpace(lines[3])
	tunnelPrivKey := ""
	if len(lines) > 4 {
		tunnelPrivKey = strings.TrimSpace(lines[4])
	}

	_, err = db.DB.Exec(`UPDATE servers SET
		tunnel_uuid = $1, tunnel_public_key = $2, tunnel_short_id = $3,
		tunnel_ss_password = $4, tunnel_private_key = $5,
		tunnel_sni = 'microsoft.com', tunnel_protocol = 'hysteria2',
		tunnel_port = 50443, tunnel_status = 'unknown', status = 'active',
		updated_at = CURRENT_TIMESTAMP
		WHERE id = $6`,
		tunnelUUID, tunnelPubKey, tunnelShortID, tunnelSSPass, tunnelPrivKey, server.ID)
	if err != nil {
		finishWithError(5, err)
		return
	}
	setStep(5, "done", "")

	// ── Шаг 7: Hysteria2 клиент на Hub ──
	setStep(6, "running", "")
	hy2Port := 1088 + server.ID
	hy2ClientScript := fmt.Sprintf(`set -e
mkdir -p /opt/heyearth/hysteria
if [ ! -f /usr/local/bin/hysteria ]; then
  curl -sL -o /usr/local/bin/hysteria https://github.com/apernet/hysteria/releases/download/app%%2Fv2.6.1/hysteria-linux-amd64
  chmod +x /usr/local/bin/hysteria
fi
cat > /opt/heyearth/hysteria/client-%d.yaml << 'HEOF'
server: %s:20000-50000
auth: %s
tls:
  insecure: true
transport:
  udp:
    hopInterval: 30s
quic:
  initStreamReceiveWindow: 26843545
  maxStreamReceiveWindow: 26843545
  initConnReceiveWindow: 67108864
  maxConnReceiveWindow: 67108864
bandwidth:
  up: 100 mbps
  down: 100 mbps
socks5:
  listen: 127.0.0.1:%d
HEOF
cat > /etc/systemd/system/hysteria2-client-%d.service << SEOF
[Unit]
Description=Hysteria2 Client for %s (HeYearth)
After=network.target
[Service]
Type=simple
ExecStart=/usr/local/bin/hysteria client -c /opt/heyearth/hysteria/client-%d.yaml
Restart=always
RestartSec=3
LimitNOFILE=65535
[Install]
WantedBy=multi-user.target
SEOF
systemctl daemon-reload
systemctl enable hysteria2-client-%d
systemctl restart hysteria2-client-%d
`, server.ID, server.IPAddress, tunnelSSPass, hy2Port, server.ID, server.IPAddress, server.ID, server.ID, server.ID)

	// Выполняем на Hub (localhost) через exec.Command
	cmd := exec.Command("bash", "-c", hy2ClientScript)
	out, err := cmd.CombinedOutput()
	if err != nil {
		finishWithError(6, fmt.Errorf("%v: %s", err, string(out)))
		return
	}
	setStep(6, "done", "")

	// Перезапускаем API для пересинхронизации Xray
	exec.Command("systemctl", "restart", "heyearth-api").Run()

	progress.Finished = true
	provisionProgress.Store(server.ID, progress)
	log.Printf("[provision] Сервер %d (%s): провижининг завершён успешно", server.ID, server.Name)
}

// sshDialForServer создаёт SSH-соединение к серверу по его credentials из БД
func sshDialForServer(srv serverResponse) (*ssh.Client, error) {
	var auths []ssh.AuthMethod
	if srv.SSHPassword != "" {
		auths = append(auths, ssh.Password(srv.SSHPassword))
	}
	if srv.SshKeyID != nil && *srv.SshKeyID > 0 {
		var privKeyPEM string
		err := db.DB.QueryRow("SELECT private_key FROM ssh_keys WHERE id = $1", *srv.SshKeyID).Scan(&privKeyPEM)
		if err == nil {
			signer, err := ssh.ParsePrivateKey([]byte(privKeyPEM))
			if err == nil {
				auths = append(auths, ssh.PublicKeys(signer))
			}
		}
	}
	if len(auths) == 0 {
		return nil, fmt.Errorf("no SSH credentials for server %d", srv.ID)
	}
	sshConfig := &ssh.ClientConfig{
		User:            srv.SSHUser,
		Auth:            auths,
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         15 * time.Second,
	}
	addr := fmt.Sprintf("%s:%d", srv.IPAddress, srv.SSHPort)
	return ssh.Dial("tcp", addr, sshConfig)
}
