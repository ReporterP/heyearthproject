package main

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/heyearth/backend/internal/config"
	sshcrypto "github.com/heyearth/backend/internal/crypto"
	"github.com/heyearth/backend/internal/provision"
	_ "github.com/lib/pq"
	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

const (
	goVersion     = "1.22.4"
	goDownloadURL = "https://go.dev/dl/go" + goVersion + ".linux-amd64.tar.gz"
	goInstallPath = "/usr/local"
	appDir        = "/opt/heyearth/mainserver"
)

type deployConfig struct {
	repoRoot           string
	serverRoot         string
	backendRoot        string
	env                envMap
	siteDomain         string
	letsEncryptEmail   string
	host               string
	deployUser         string
	deployPassword     string
	deployPort         int
	hostKeyFingerprint string
}

type envMap map[string]string

func main() {
	mainMode := flag.Bool("main", false, "установить Главный сервер (Go, PostgreSQL, API, бот, Nginx)")
	updateMain := flag.Bool("update-main", false, "обновить код Главного сервера и provisioning assets (без фронтов)")
	restart := flag.Bool("restart", false, "перезапустить сервисы Главного сервера")
	site := flag.Bool("site", false, "обновить admin_web + Vue site + Nginx")
	adminWeb := flag.Bool("admin-web", false, "обновить только admin_web")
	vueSite := flag.Bool("vue-site", false, "обновить только Vue маркетинговый сайт")
	deploySite := flag.Bool("deploy-site", false, "обновить Vue маркетинговый сайт (алиас для --vue-site)")
	nginxOnly := flag.Bool("nginx", false, "обновить только nginx конфиг + SSL")
	setupXrayFlag := flag.Bool("setup-xray", false, "установить Xray (VLESS Reality) на главный сервер")
	streamPPFlag := flag.String("stream-proxy-protocol", "", "on|off: PROXY protocol на 443 stream (реальные IP клиентов для nginx/API); меняет только upstream'ы nginx stream (8443+10443 ↔ 8446+10444, xray слушает оба входа всегда), выполняется на Hub отдельным юнитом")
	forceFlag := flag.Bool("force", false, "с --setup-xray: заменить существующий /opt/heyearth/xray/config.json bootstrap-конфигом (на живом Hub отрежет пользователей до синхронизации API)")
	setupHy2Flag := flag.Bool("setup-hysteria2-hub", false, "установить Hysteria2 сервер на главный сервер (Express routing для premium-планов)")
	statusMode := flag.Bool("status", false, "проверить состояние сервисов и API на сервере")
	rotateCerts := flag.Bool("rotate-node-agent-certs", false, "перевыпустить на Hub клиентский mTLS-сертификат API от CA Hub (CA и сертификаты нод не меняются; сертификаты нод обновляет их настройка)")
	ensureHubProv := flag.Bool("ensure-hub-provisioning", false, "подготовить живой Hub к настройке нод (админка и --setup-exit-node): SSH_ENCRYPTION_KEY в api.env, ключ SSH Hub, node-agent, install-mtproxy.sh, собственный CA node-agent на Hub (прежний — как legacy до сведения exit); ничего не перезапускает")
	setupExitNode := flag.String("setup-exit-node", "", "настроить exit-ноду ядром провижининга (IP или IP:порт; порт игнорируется)")
	exitNodePassword := flag.String("exit-node-password", "", "SSH пароль root exit-ноды (иначе EXIT_NODE_SSH_PASSWORD или сохранённый в базе)")
	exitNodeSSHPort := flag.Int("exit-node-ssh-port", 22, "SSH порт exit-ноды (по умолчанию: сохранённый в базе, для новой — 22)")
	exitNodeName := flag.String("exit-node-name", "", "название новой exit-ноды (--setup-exit-node; по умолчанию «Выход <страна>»)")
	exitNodeCountry := flag.String("exit-node-country", "", "код страны новой exit-ноды, напр. DE (--setup-exit-node)")
	exitNodePlans := flag.String("exit-node-plans", "", "тарифы exit: all-paid | none | keep | 1,2,3 (по умолчанию: новой — all-paid, существующей — keep)")
	exitNodeMTProxy := flag.Bool("exit-node-mtproxy", true, "MTProxy на exit (--setup-exit-node; без флага выключатель ноды не меняется, у новой — включён)")
	sshCmd := flag.String("ssh", "", "выполнить SSH команду на сервере и вернуть результат")
	setupOpsFlag := flag.Bool("setup-ops", false, "эксплуатация Hub: чистка ufw, ежедневный бэкап (+копия на exit: --backup-target), fail2ban, journald, nginx stream timeout, защита xray API")
	backupTarget := flag.String("backup-target", "", "IP exit-ноды для копии бэкапа (--setup-ops); пароль: --exit-node-password или EXIT_NODE_SSH_PASSWORD")
	backupAgeRecipient := flag.String("backup-age-recipient", "", "age-публичный ключ (age1…), которым шифруется копия бэкапа на exit (--setup-ops); приватный ключ хранит владелец офлайн: age-keygen -o backup.key")
	flag.Parse()

	var mode string
	switch {
	case *ensureHubProv:
		mode = "ensure-hub-provisioning"
	case *rotateCerts:
		mode = "rotate-certs"
	case *setupExitNode != "":
		mode = "setup-exit-node"
	case *mainMode:
		mode = "main"
	case *updateMain:
		mode = "update-main"
	case *restart:
		mode = "restart"
	case *site:
		mode = "site"
	case *adminWeb:
		mode = "admin-web"
	case *vueSite || *deploySite:
		mode = "vue-site"
	case *nginxOnly:
		mode = "nginx"
	case *setupXrayFlag:
		mode = "setup-xray"
	case *setupHy2Flag:
		mode = "setup-hysteria2-hub"
	case *setupOpsFlag:
		mode = "setup-ops"
	case *streamPPFlag != "":
		mode = "stream-proxy-protocol"
	case *sshCmd != "":
		mode = "ssh"
	case *statusMode:
		mode = "status"
	case flag.NArg() == 1 && flag.Arg(0) == "fix-db":
		mode = "fix-db"
	case flag.NArg() >= 1 && flag.Arg(0) == "fix-db":
		mode = "fix-db"
	default:
		log.Fatal("Укажи один из флагов: --main, --update-main, --restart, --site, --admin-web, --vue-site, --deploy-site, --nginx, --setup-xray, --setup-hysteria2-hub, --setup-ops, --stream-proxy-protocol on|off, --ensure-hub-provisioning, --setup-exit-node, --status, --rotate-node-agent-certs. Или: fix-db")
	}

	repoRoot := findRepoRoot()
	serverRoot := filepath.Join(repoRoot, "server")
	backendRoot := filepath.Join(serverRoot, "backend")
	env := loadEnv(serverRoot, repoRoot)

	cfg := &deployConfig{
		repoRoot:           repoRoot,
		serverRoot:         serverRoot,
		backendRoot:        backendRoot,
		env:                env,
		siteDomain:         env.get("SITE_DOMAIN", "hiearth.ru"),
		letsEncryptEmail:   env.get("LETSENCRYPT_EMAIL", ""),
		host:               env.get("DEPLOY_HOST", ""),
		deployUser:         env.get("DEPLOY_USER", "root"),
		deployPassword:     env.get("DEPLOY_SSH_PASSWORD", ""),
		deployPort:         env.getInt("DEPLOY_PORT", 22),
		hostKeyFingerprint: env.get("DEPLOY_HOST_KEY_FINGERPRINT", ""),
	}

	validateLocalConfig(mode, cfg)

	addr := fmt.Sprintf("%s:%d", cfg.host, cfg.deployPort)
	client, err := sshDial(addr, cfg.deployUser, cfg.deployPassword, cfg.hostKeyFingerprint)
	if err != nil {
		log.Fatalf("ssh dial: %v", err)
	}
	defer client.Close()

	if mode == "ssh" {
		out, err := runSSH(client, *sshCmd, false)
		if err != nil {
			log.Fatalf("SSH error: %v", err)
		}
		fmt.Print(out)
		return
	}
	if mode == "status" {
		doStatus(client, cfg)
		return
	}
	if mode == "fix-db" {
		doFixDB(client, cfg)
		return
	}
	if mode == "setup-ops" {
		setupOpsHub(client, cfg, *backupTarget, *exitNodePassword, *exitNodeSSHPort, *backupAgeRecipient)
		return
	}
	if mode == "stream-proxy-protocol" {
		switchStreamProxyProtocol(client, cfg, *streamPPFlag)
		return
	}

	if mode == "ensure-hub-provisioning" {
		ensureHubProvisioning(client, cfg)
		return
	}
	if mode == "rotate-certs" {
		rotateHubClientCert(client)
		return
	}
	if mode == "setup-exit-node" {
		// Flags not given keep what the database has (port, MTProxy switch).
		set := map[string]bool{}
		flag.Visit(func(f *flag.Flag) { set[f.Name] = true })
		opts := exitCLIOptions{name: *exitNodeName, country: *exitNodeCountry, plans: *exitNodePlans, password: *exitNodePassword}
		if set["exit-node-ssh-port"] {
			opts.sshPort = *exitNodeSSHPort
		}
		if set["exit-node-mtproxy"] {
			opts.mtproxy = exitNodeMTProxy
		}
		setupExitNodeDeploy(*setupExitNode, client, cfg, opts)
		return
	}

	sftpClient, err := sftp.NewClient(client)
	if err != nil {
		log.Fatalf("sftp: %v", err)
	}
	defer sftpClient.Close()

	switch mode {
	case "main":
		optimizeNetworkStack(client)
		installPostgresOnServer(client)
		uploadNodeProvisioningAssets(sftpClient, client, cfg)
		doAPI(sftpClient, client, cfg)
		doAdminWeb(sftpClient, client, cfg)
		doVueSite(sftpClient, client, cfg)
		doNginx(sftpClient, client, cfg)
		doSSL(client, cfg)
		doNginx(sftpClient, client, cfg) // перезаписать конфиг с учётом SSL
		seedConnectionSettings(client, cfg)
		verifyServices(client)
	case "update-main":
		uploadNodeProvisioningAssets(sftpClient, client, cfg)
		doAPI(sftpClient, client, cfg)
		if cfg.letsEncryptEmail != "" {
			doSSL(client, cfg)
		}
		seedConnectionSettings(client, cfg)
		verifyServices(client)
	case "restart":
		restartServices(client)
		verifyServices(client)
	case "site":
		doAdminWeb(sftpClient, client, cfg)
		doVueSite(sftpClient, client, cfg)
		doNginx(sftpClient, client, cfg)
		doSSL(client, cfg)
		doNginx(sftpClient, client, cfg) // перезаписать конфиг с учётом SSL
	case "admin-web":
		doAdminWeb(sftpClient, client, cfg)
		_, _ = runSSH(client, "nginx -t && systemctl reload nginx", false)
	case "vue-site":
		doVueSite(sftpClient, client, cfg)
		_, _ = runSSH(client, "nginx -t && systemctl reload nginx", false)
	case "nginx":
		doNginx(sftpClient, client, cfg)
		doSSL(client, cfg)
		doNginx(sftpClient, client, cfg) // перезаписать с SSL
	case "setup-xray":
		setupXray(client, cfg, *forceFlag)
		doNginx(sftpClient, client, cfg)
		seedConnectionSettings(client, cfg)
		verifyServices(client)
	case "setup-hysteria2-hub":
		setupHysteria2Hub(client, cfg)
	}
}

func validateLocalConfig(mode string, cfg *deployConfig) {
	if cfg.host == "" {
		log.Fatal("DEPLOY_HOST is required")
	}
	if cfg.deployPassword == "" {
		log.Fatal("DEPLOY_SSH_PASSWORD is required")
	}

	if mode == "main" || mode == "update-main" {
		for _, key := range []string{"ADMIN_USER", "ADMIN_PASS", "JWT_SECRET"} {
			if strings.TrimSpace(cfg.env.get(key, "")) == "" {
				log.Fatalf("%s is required for %s", key, mode)
			}
		}
	}

	if mode == "main" || mode == "site" || mode == "admin-web" || mode == "vue-site" {
		if _, err := exec.LookPath("npm"); err != nil {
			log.Fatal("npm is required locally to build admin_web / Vue site during deploy")
		}
	}
	if mode == "status" || mode == "fix-db" || mode == "ssh" {
		return
	}
}

func loadEnv(paths ...string) envMap {
	m := make(envMap)
	for _, base := range paths {
		if base == "" {
			continue
		}
		readEnvFile(m, filepath.Join(base, ".env"))
	}
	for _, e := range os.Environ() {
		if i := strings.Index(e, "="); i > 0 {
			m[e[:i]] = e[i+1:]
		}
	}
	return m
}

func readEnvFile(m envMap, path string) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if idx := strings.Index(line, "="); idx > 0 {
			key := strings.TrimSpace(line[:idx])
			value := strings.Trim(strings.TrimSpace(line[idx+1:]), "\"'")
			m[key] = value
		}
	}
}

func (m envMap) get(key, def string) string {
	if v, ok := m[key]; ok && strings.TrimSpace(v) != "" {
		return v
	}
	return def
}

func (m envMap) getInt(key string, def int) int {
	var n int
	if _, err := fmt.Sscanf(m.get(key, ""), "%d", &n); err == nil {
		return n
	}
	return def
}

func findRepoRoot() string {
	dir, _ := os.Getwd()
	for {
		if _, err := os.Stat(filepath.Join(dir, "server", "backend", "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	log.Fatal("Не удалось найти корень репозитория (ожидается server/backend/go.mod)")
	return ""
}

// sshDial connects to the Hub by password only with its host key pinned by
// DEPLOY_HOST_KEY_FINGERPRINT (the key Go negotiates by default — ECDSA on the Hub). Without a
// well-formed fingerprint nothing is dialled: the deploy password would go to whoever answers.
func sshDial(addr, user, password, expectedFingerprint string) (*ssh.Client, error) {
	if !fingerprintRe.MatchString(strings.TrimSpace(expectedFingerprint)) {
		return nil, fmt.Errorf("DEPLOY_HOST_KEY_FINGERPRINT в server/.env не задан или не вида SHA256:<43 символа> (%q) — "+
			"подключение к Hub без закреплённого ключа хоста запрещено: пароль ушёл бы любому, кто ответит на %s. "+
			"Отпечаток — в консоли хостера: ssh-keygen -lf /etc/ssh/ssh_host_ecdsa_key.pub -E sha256", expectedFingerprint, addr)
	}
	return sshDialAlgos(addr, user, password, expectedFingerprint, nil)
}

// sshDialAlgos is sshDial with an optional list of accepted host key algorithms. A pinned
// fingerprint is only meaningful for one key type, so callers that pin an ed25519 fingerprint
// restrict the negotiation to ssh-ed25519 (otherwise the server may present its RSA key). There
// is no unpinned mode.
func sshDialAlgos(addr, user, password, expectedFingerprint string, hostKeyAlgorithms []string) (*ssh.Client, error) {
	expectedFingerprint = strings.TrimSpace(expectedFingerprint)
	if !fingerprintRe.MatchString(expectedFingerprint) {
		return nil, fmt.Errorf("ssh %s: нужен закреплённый отпечаток ключа хоста SHA256:…, а не %q", addr, expectedFingerprint)
	}
	callback := func(hostname string, remoteAddr net.Addr, key ssh.PublicKey) error {
		got := ssh.FingerprintSHA256(key)
		if got != expectedFingerprint {
			return fmt.Errorf("unexpected host key fingerprint for %s: got %s", hostname, got)
		}
		return nil
	}

	cfg := &ssh.ClientConfig{
		User:              user,
		Auth:              []ssh.AuthMethod{ssh.Password(password)},
		HostKeyCallback:   callback,
		HostKeyAlgorithms: hostKeyAlgorithms,
		Timeout:           15 * time.Second,
	}
	client, err := ssh.Dial("tcp", addr, cfg)
	if err != nil {
		return nil, err
	}
	// SSH keepalive: prevent server from dropping idle connections during local operations
	go func() {
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			_, _, err := client.SendRequest("keepalive@openssh.com", true, nil)
			if err != nil {
				return
			}
		}
	}()
	return client, nil
}

// runSSH runs cmd through the remote login shell. With check=true a failure is returned as
// an error that carries a short label of the command, never the whole script.
func runSSH(client *ssh.Client, cmd string, check bool) (string, error) {
	return runSSHLabeled(client, cmd, cmdLabel(cmd), check)
}

// runSSHBash runs a (multi-line) script explicitly under bash instead of whatever the remote
// login shell is: `bash -c '<script>'`, so bash-isms like `set -o pipefail` are deterministic.
func runSSHBash(client *ssh.Client, label, script string, check bool) (string, error) {
	return runSSHLabeled(client, "bash -c "+shQuote(script), label, check)
}

// shQuote single-quotes s for a POSIX shell.
func shQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// cmdLabel is the first non-empty line of cmd, shortened, with a note when more follow.
func cmdLabel(cmd string) string {
	lines := strings.Split(strings.TrimSpace(cmd), "\n")
	label := strings.TrimSpace(lines[0])
	if r := []rune(label); len(r) > 80 {
		label = string(r[:80]) + "…"
	}
	if len(lines) > 1 {
		label += fmt.Sprintf(" …(+%d lines)", len(lines)-1)
	}
	return label
}

func runSSHLabeled(client *ssh.Client, cmd, label string, check bool) (string, error) {
	sess, err := client.NewSession()
	if err != nil {
		return "", err
	}
	defer sess.Close()

	var out, errOut bytes.Buffer
	sess.Stdout = &out
	sess.Stderr = &errOut
	err = sess.Run(cmd)
	outStr := out.String()
	if outStr != "" {
		fmt.Print(outStr)
	}
	if err != nil && errOut.Len() > 0 {
		fmt.Fprint(os.Stderr, errOut.String())
	}
	if check && err != nil {
		return outStr, fmt.Errorf("%s: %w", label, err)
	}
	return outStr, nil
}

func normRemote(path string) string {
	return "/" + strings.TrimPrefix(strings.ReplaceAll(path, "\\", "/"), "/")
}

func remoteDir(remote string) string {
	remote = normRemote(remote)
	idx := strings.LastIndex(remote, "/")
	if idx <= 0 {
		return ""
	}
	return remote[:idx]
}

func sftpMkdirAll(c *sftp.Client, path string) error {
	path = strings.TrimPrefix(filepath.ToSlash(path), "/")
	if path == "" {
		return nil
	}
	var cur string
	for _, part := range strings.Split(path, "/") {
		if part == "" {
			continue
		}
		if cur == "" {
			cur = "/" + part
		} else {
			cur += "/" + part
		}
		_ = c.Mkdir(cur)
	}
	return nil
}

func uploadFile(c *sftp.Client, local, remote string) error {
	src, err := os.Open(local)
	if err != nil {
		return err
	}
	defer src.Close()

	remote = normRemote(remote)
	if err := sftpMkdirAll(c, remoteDir(remote)); err != nil {
		return err
	}
	// Write next to the target and rename: a dropped SFTP session leaves the old file
	// whole (it used to remove it first and leave a truncated binary behind).
	tmp := fmt.Sprintf("%s.tmp-%d", remote, time.Now().UnixNano())
	dst, err := c.Create(tmp)
	if err != nil {
		return err
	}
	if _, err = io.Copy(dst, src); err != nil {
		dst.Close()
		_ = c.Remove(tmp)
		return err
	}
	if err := dst.Close(); err != nil {
		_ = c.Remove(tmp)
		return err
	}
	if err := c.PosixRename(tmp, remote); err != nil {
		_ = c.Remove(tmp)
		return err
	}
	return nil
}

func uploadFileContent(c *sftp.Client, content []byte, remote string) error {
	remote = normRemote(remote)
	if err := sftpMkdirAll(c, remoteDir(remote)); err != nil {
		return err
	}
	tmp := fmt.Sprintf("%s.tmp-%d", remote, time.Now().UnixNano())
	dst, err := c.Create(tmp)
	if err != nil {
		return err
	}
	if _, err = dst.Write(content); err != nil {
		dst.Close()
		_ = c.Remove(tmp)
		return err
	}
	if err := dst.Close(); err != nil {
		_ = c.Remove(tmp)
		return err
	}
	if err := c.PosixRename(tmp, remote); err != nil {
		_ = c.Remove(tmp)
		return err
	}
	return nil
}

func uploadDir(c *sftp.Client, localDir, remoteDir string) error {
	entries, err := os.ReadDir(localDir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		localPath := filepath.Join(localDir, entry.Name())
		remotePath := normRemote(filepath.Join(remoteDir, entry.Name()))
		if entry.IsDir() {
			if err := sftpMkdirAll(c, remotePath); err != nil {
				return err
			}
			if err := uploadDir(c, localPath, remotePath); err != nil {
				return err
			}
			continue
		}
		if err := uploadFile(c, localPath, remotePath); err != nil {
			return err
		}
	}
	return nil
}

func buildGoBinary(backendRoot, name, outName string) string {
	outPath := filepath.Join(backendRoot, ".build", outName)
	_ = os.MkdirAll(filepath.Dir(outPath), 0755)

	cmd := exec.Command("go", "build", "-o", outPath, "./cmd/"+name)
	cmd.Dir = backendRoot
	cmd.Env = append(os.Environ(), "GOOS=linux", "GOARCH=amd64")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		log.Fatalf("go build ./cmd/%s: %v", name, err)
	}
	return outPath
}

func buildAdminWeb(repoRoot string, cfg *deployConfig) string {
	adminWebRoot := filepath.Join(repoRoot, "admin_web")

	// React admin panel: npm ci + npm run build
	npmCi := exec.Command("npm", "ci")
	npmCi.Dir = adminWebRoot
	npmCi.Stdout = os.Stdout
	npmCi.Stderr = os.Stderr
	if err := npmCi.Run(); err != nil {
		log.Fatalf("npm ci in admin_web: %v", err)
	}

	build := exec.Command("npm", "run", "build")
	build.Dir = adminWebRoot
	build.Stdout = os.Stdout
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		log.Fatalf("npm run build in admin_web: %v", err)
	}

	return filepath.Join(adminWebRoot, "dist")
}

// optimizeNetworkStack включает BBR congestion control и отключает IPv6.
// BBR значительно улучшает пропускную способность, а отключение IPv6 исключает утечки маршрутов.
func optimizeNetworkStack(client *ssh.Client) {
	script := `set -e
# BBR congestion control
if ! sysctl net.ipv4.tcp_congestion_control 2>/dev/null | grep -q bbr; then
  echo "net.core.default_qdisc=fq" >> /etc/sysctl.conf
  echo "net.ipv4.tcp_congestion_control=bbr" >> /etc/sysctl.conf
  echo "[+] BBR enabled"
else
  echo "[=] BBR already enabled"
fi

# Отключение IPv6
if ! sysctl net.ipv6.conf.all.disable_ipv6 2>/dev/null | grep -q 1; then
  echo "net.ipv6.conf.all.disable_ipv6=1" >> /etc/sysctl.conf
  echo "net.ipv6.conf.default.disable_ipv6=1" >> /etc/sysctl.conf
  echo "net.ipv6.conf.lo.disable_ipv6=1" >> /etc/sysctl.conf
  echo "[+] IPv6 disabled"
else
  echo "[=] IPv6 already disabled"
fi

sysctl -p 2>/dev/null || true
echo "[OK] Network stack optimized"
`
	_, _ = runSSH(client, script, true)
}

func installPostgresOnServer(client *ssh.Client) {
	script := `set -e
apt-get update -qq
apt-get install -y -qq postgresql postgresql-contrib
systemctl enable postgresql
systemctl start postgresql
sudo -u postgres psql -tAc "SELECT 1 FROM pg_database WHERE datname='heyearth'" | grep -q 1 || sudo -u postgres createdb heyearth
`
	_, _ = runSSH(client, script, true)
}

// uploadNodeProvisioningAssets: node-agent and the MTProxy installer for the API's provisioning,
// and the node-agent CA, which is made on the Hub itself (ensureHubNodeCA): no
// certificate or key goes from this machine to the Hub any more (Beads cudk.77). A deploy only
// creates a CA on a Hub without one and renews the API's client certificate; replacing an old CA
// is --ensure-hub-provisioning.
func uploadNodeProvisioningAssets(c *sftp.Client, client *ssh.Client, cfg *deployConfig) {
	_, _ = runSSH(client, "mkdir -p "+appDir+"/artifacts "+hubCertsDir+" "+path.Dir(hubMTProxyScript), true)

	nodeAgentPath := buildGoBinary(cfg.backendRoot, "node-agent", "node-agent")
	if err := uploadFile(c, nodeAgentPath, hubNodeAgentArtifact); err != nil {
		log.Fatal(err)
	}
	if err := uploadFile(c, filepath.Join(cfg.backendRoot, "cmd", "deploy", "install-mtproxy.sh"), hubMTProxyScript); err != nil {
		log.Fatalf("upload install-mtproxy.sh: %v", err)
	}
	_, _ = runSSH(client, "chmod 755 "+hubMTProxyScript, true)

	ctx, hub := context.Background(), provision.NewSSHRemote(client)
	p, err := ensureHubNodeCA(ctx, hub, hubCertsDir, hubClock(ctx, hub)(), caKeep, false)
	reportHubCA(p, err, false)
}

func doAPI(c *sftp.Client, client *ssh.Client, cfg *deployConfig) {
	_, _ = runSSH(client, "mkdir -p "+appDir+"/migrations", true)

	apiPath := buildGoBinary(cfg.backendRoot, "api", "heyearth-api")
	if err := uploadFile(c, apiPath, appDir+"/heyearth-api"); err != nil {
		log.Fatal(err)
	}
	_, _ = runSSH(client, "chmod +x "+appDir+"/heyearth-api", true)

	migrationsDir := filepath.Join(cfg.backendRoot, "migrations")
	if err := uploadDir(c, migrationsDir, appDir+"/migrations"); err != nil {
		log.Fatalf("upload migrations: %v", err)
	}

	// The DB password lives only in the Hub's api.env: kept on every run, generated once on a
	// first install, never reset (see db_password.go).
	dbURL := resolveHubDatabaseURL(client, cfg)

	apiEnv := map[string]string{
		"APP_DIR":              appDir,
		"DATABASE_URL":         dbURL,
		"ADMIN_USER":           cfg.env.get("ADMIN_USER", ""),
		"ADMIN_PASS":           cfg.env.get("ADMIN_PASS", ""),
		"JWT_SECRET":           cfg.env.get("JWT_SECRET", ""),
		"SITE_DOMAIN":          cfg.siteDomain,
		"PUBLIC_BASE_URL":      publicBaseURL(cfg),
		"TLS_CLIENT_CERT_FILE": path.Join(appDir, "certs", "client-cert.pem"),
		"TLS_CLIENT_KEY_FILE":  path.Join(appDir, "certs", "client-key.pem"),
		"TLS_CA_FILE":          path.Join(appDir, "certs", "ca-cert.pem"),
	}
	// SSH_ENCRYPTION_KEY survives every deploy (api.env is rewritten from this whitelist); a Hub
	// without one gets it here only while no ciphertext exists. An api.env or a key state that
	// cannot be read stops the deploy before api.env is touched and the API restarted: without the
	// key the API could no longer decrypt node passwords whose plaintext copies it already emptied.
	hubEnv, _, err := readHubAPIEnv(client)
	if err != nil {
		log.Fatalf("[hub] чтение %s: %v — api.env не перезаписываю, API не перезапускаю (SSH_ENCRYPTION_KEY пропал бы)", hubAPIEnv, err)
	}
	sshKey, generated, err := apiEnvSSHKey(hubEnv, func(e string) (string, bool, error) { return resolveSSHEncryptionKey(client, cfg, e) })
	if err != nil {
		log.Fatalf("[hub] SSH_ENCRYPTION_KEY: %v — api.env не перезаписываю, API не перезапускаю", err)
	}
	apiEnv["SSH_ENCRYPTION_KEY"] = sshKey
	// api.env carries the DB password, the JWT secret, the admin password and the key: written on
	// the Hub (exact content, timestamped copy of the previous file, 0600, temp + mv), not over SFTP.
	content, kept := preserveUnmanagedEnv(serializeEnv(apiEnv), hubEnv)
	if len(kept) > 0 {
		fmt.Println("[hub] api.env: сохраняю ключи, которыми deploy не управляет: " + strings.Join(kept, ", "))
	}
	writeHubAPIEnv(client, content)
	if generated {
		fmt.Println("[hub] SSH_ENCRYPTION_KEY сгенерирован (зашифрованных паролей в БД не было)")
		keepKeyCopy(client)
	}

	service := fmt.Sprintf(`[Unit]
Description=HeYearth API
After=network-online.target postgresql.service
Wants=network-online.target

[Service]
Type=simple
User=root
WorkingDirectory=%s
EnvironmentFile=%s/api.env
ExecStart=%s/heyearth-api
Restart=always
RestartSec=5

[Install]
WantedBy=multi-user.target
`, appDir, appDir, appDir)
	if err := uploadFileContent(c, []byte(service), "/etc/systemd/system/heyearth-api.service"); err != nil {
		log.Fatal(err)
	}
	_, _ = runSSH(client, "systemctl daemon-reload && systemctl enable heyearth-api && systemctl restart heyearth-api", true)
}

// doBot removed — Telegram bot is no longer part of the project.

// doAdminWeb uploads next to the live build and swaps directories at the end: a dropped SFTP
// session leaves the working admin in place (the old order removed it first). When SFTP keeps
// dropping, `go run quick_admin_web.go` sends the build as one archive over an SSH pipe.
func doAdminWeb(c *sftp.Client, client *ssh.Client, cfg *deployConfig) {
	const live, next = "/var/www/heyearth", "/var/www/heyearth.next"
	_, _ = runSSH(client, "rm -rf "+next+" && mkdir -p "+next, true)
	adminBuildDir := buildAdminWeb(cfg.repoRoot, cfg)
	if err := uploadDir(c, adminBuildDir, next); err != nil {
		_, _ = runSSH(client, "rm -rf "+next, false)
		log.Fatalf("upload admin_web build: %v (the live admin is untouched; try: go run quick_admin_web.go)", err)
	}
	_, _ = runSSH(client, fmt.Sprintf("chmod -R 755 %[2]s && rm -rf %[1]s.old && { [ ! -d %[1]s ] || mv %[1]s %[1]s.old; } && mv %[2]s %[1]s", live, next), true)
	fmt.Println("[admin-web] uploaded to " + live)
}

// doVueSite builds the site FIRST, uploads it next to the live one and swaps the
// directories at the end, like doAdminWeb: a failed build or a dropped SFTP session leaves
// the working site in place (it used to delete the live site before building). When SFTP
// keeps dropping, `go run quick_admin_web.go -site` sends it as one archive.
func doVueSite(c *sftp.Client, client *ssh.Client, cfg *deployConfig) {
	const live, next = "/var/www/heyearth-site", "/var/www/heyearth-site.next"
	vueBuildDir := buildSite(cfg.repoRoot, cfg)
	if _, err := os.Stat(filepath.Join(vueBuildDir, "index.html")); err != nil {
		log.Fatalf("site build has no index.html: %v (the live site is untouched)", err)
	}
	_, _ = runSSH(client, "rm -rf "+next+" && mkdir -p "+next, true)
	if err := uploadDir(c, vueBuildDir, next); err != nil {
		_, _ = runSSH(client, "rm -rf "+next, false)
		log.Fatalf("upload site build: %v (the live site is untouched; try: go run quick_admin_web.go -site)", err)
	}
	_, _ = runSSH(client, fmt.Sprintf("chmod -R 755 %[2]s && rm -rf %[1]s.old && { [ ! -d %[1]s ] || mv %[1]s %[1]s.old; } && mv %[2]s %[1]s", live, next), true)
	fmt.Println("[vue-site] uploaded to " + live)
}

// uncToWSLPath converts a Windows UNC path (\\wsl.localhost\Distro\home\...) to WSL Linux path (/home/...).
func uncToWSLPath(unc string) string {
	normalized := filepath.ToSlash(unc)
	if i := strings.Index(normalized, "/home/"); i >= 0 {
		return normalized[i:]
	}
	return ""
}

func buildSite(repoRoot string, cfg *deployConfig) string {
	siteRoot := filepath.Join(repoRoot, "site")

	// When repo is on WSL (UNC on Windows, or /home/... on Linux), run npm inside a clean environment so we don't use Windows npm (which breaks on UNC in postinstall). Use WSL explicitly on Windows; on Linux (WSL) run via bash with explicit /usr/bin/npm to avoid Windows npm in PATH.
	useWSL := false
	var wslPath string
	if runtime.GOOS == "windows" && (strings.HasPrefix(siteRoot, "\\\\") || strings.HasPrefix(siteRoot, `\\`)) {
		wslPath = uncToWSLPath(siteRoot)
		if wslPath != "" {
			useWSL = true
		}
	}
	if runtime.GOOS == "linux" && strings.HasPrefix(filepath.ToSlash(siteRoot), "/home/") {
		wslPath = filepath.ToSlash(siteRoot)
		useWSL = true
	}
	if useWSL && wslPath != "" {
		// Unset VITE_API_BASE — nginx on site.hiearth.ru proxies /api/ to backend
		// npm ci: the locked versions, never a fresh resolve (package-lock.json stays).
		script := fmt.Sprintf("cd %s && unset VITE_API_BASE && /usr/bin/npm ci && /usr/bin/npm run build",
			strings.ReplaceAll(wslPath, "'", "'\"'\"'"))
		var cmd *exec.Cmd
		if runtime.GOOS == "windows" {
			cmd = exec.Command("wsl", "-e", "bash", "-c", script)
		} else {
			cmd = exec.Command("bash", "-c", script)
		}
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			log.Fatalf("npm install/build in site (via WSL/bash): %v", err)
		}
		return filepath.Join(siteRoot, "dist")
	}

	npmPath, err := exec.LookPath("npm")
	if err != nil {
		log.Fatal("npm is required locally to build the site")
	}

	install := exec.Command(npmPath, "ci")
	install.Dir = siteRoot
	install.Stdout = os.Stdout
	install.Stderr = os.Stderr
	if err := install.Run(); err != nil {
		log.Fatalf("npm install in site: %v", err)
	}

	build := exec.Command(npmPath, "run", "build")
	build.Dir = siteRoot
	// Remove VITE_API_BASE entirely — nginx on site.hiearth.ru proxies /api/ to backend.
	// Empty or absent VITE_API_BASE means relative paths (same-origin).
	build.Env = filterEnv(os.Environ(), "VITE_API_BASE")
	build.Stdout = os.Stdout
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		log.Fatalf("npm run build in site: %v", err)
	}

	return filepath.Join(siteRoot, "dist")
}

func doNginx(c *sftp.Client, client *ssh.Client, cfg *deployConfig) {
	_, _ = runSSH(client, "apt-get update -qq && apt-get install -y -qq nginx", true)

	domain := cfg.siteDomain
	certPath := "/etc/letsencrypt/live/" + domain

	// Проверяем наличие SSL-сертификата и Xray (если установлен — stream для SNI routing на 443).
	// Ошибка проверки — не «нет»: иначе nginx получил бы конфиг без SSL / без stream (fail closed).
	ssl, err := parseYesNo(runSSHQuiet(client, "if [ -f "+certPath+"/fullchain.pem ]; then echo yes; else echo no; fi", ""))
	if err != nil {
		log.Fatalf("nginx: проверка сертификата %s: %v — конфиги nginx не трогаю", certPath, err)
	}
	xrayActive, err := isXrayInstalled(client)
	if err != nil {
		log.Fatalf("nginx: %v — конфиги nginx не трогаю", err)
	}

	// The stream keeps the live PROXY-protocol mode (deploy --stream-proxy-protocol). Determined
	// from the router file before anything is written; if it cannot be determined, nginx is not
	// touched at all (fail closed).
	proxyProtocol := false
	if xrayActive && ssl {
		if proxyProtocol, err = streamModeForDeploy(runSSHQuiet(client, streamRouterReadCmd, "")); err != nil {
			log.Fatalf("nginx: %v — конфиги nginx не трогаю. Приведите %s в порядок или выполните go run ./cmd/deploy --stream-proxy-protocol on|off",
				err, streamRouterFile)
		}
		fmt.Printf("nginx stream: PROXY protocol %s (сохраняю живой режим)\n", map[bool]string{true: "ВКЛ", false: "выкл"}[proxyProtocol])
	}

	var nginxConf string
	if ssl {
		nginxConf = buildNginxConfSSL(domain, certPath, xrayActive)
	} else {
		nginxConf = buildNginxConfHTTP(domain)
	}

	if err := uploadFileContent(c, []byte(nginxConf), "/etc/nginx/conf.d/heyearth.conf"); err != nil {
		log.Fatal(err)
	}

	// Если Xray активен — настраиваем nginx stream для SNI routing на 443
	if xrayActive && ssl {
		streamConf := buildNginxStreamConf(domain, proxyProtocol)
		_, _ = runSSH(client, "apt-get install -y -qq libnginx-mod-stream 2>/dev/null || true", true)
		_, _ = runSSH(client, "mkdir -p /etc/nginx/stream.d", true)
		if err := uploadFileContent(c, []byte(streamConf), "/etc/nginx/stream.d/heyearth.conf"); err != nil {
			log.Fatal(err)
		}
		// Добавить include stream.d в nginx.conf если ещё нет
		_, _ = runSSH(client, `grep -q 'include /etc/nginx/stream.d' /etc/nginx/nginx.conf || echo 'stream { include /etc/nginx/stream.d/*.conf; }' >> /etc/nginx/nginx.conf`, true)
		fmt.Println("nginx stream SNI routing настроен (443 → 8443/10443)")
		// worker_rlimit_nofile / worker_connections live in nginx.conf, which this tool does not generate:
		// apply the same idempotent patch as --setup-ops (nginx -t before reload, rollback on failure).
		if err := patchNginxStream(client); err != nil {
			fmt.Printf("WARN: nginx limits/timeouts patch: %v\n", err)
		}
	} else {
		// Xray не установлен — убираем stream-конфиг, чтобы не было конфликта портов
		// (conf.d слушает 443 напрямую, stream тоже хотел бы 443)
		_, _ = runSSH(client, "rm -f /etc/nginx/stream.d/heyearth.conf", true)
	}

	// reload, not restart: a restart drops every connection behind the 443 stream (Reality sessions).
	if _, err := runSSH(client, nginxApplyCmd, true); err != nil {
		log.Fatalf("nginx: nginx -t / reload не прошли: %v", err)
	}
}

func buildNginxConfHTTP(domain string) string {
	return fmt.Sprintf(`# ── Main site + API (%[1]s) ──────────────────────────────────────
server {
    listen 80;
    listen [::]:80;
    server_name %[1]s www.%[1]s;

    location /heyearth.v1.ClientApi/ { grpc_pass 127.0.0.1:50051; grpc_set_header Host $host; }
    location = /robots.txt { default_type text/plain; return 200 "User-agent: *\nDisallow: /api/\n"; }

    location /api/ {
        proxy_pass http://127.0.0.1:8000;
        proxy_http_version 1.1;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
        client_max_body_size 512m;
    }

    # geoip.dat for Happ-client smart-routing (Geoipurl in subscription deeplink).
    # alias to /etc/hysteria/geoip.dat (Loyalsoldier release, refreshed monthly).
    # www-data needs read access via setfacl — see cmd/deploy/hysteria2_hub.go.
    location = /static/geoip.dat {
        alias /etc/hysteria/geoip.dat;
        default_type application/octet-stream;
        add_header Cache-Control "public, max-age=86400" always;
        add_header X-Content-Type-Options "nosniff" always;
    }

    location / { root /var/www/heyearth-site; try_files $uri $uri/ /index.html; }
    location = /index.html { root /var/www/heyearth-site; add_header Cache-Control "no-cache, no-store, must-revalidate" always; }
}

# ── Admin panel (admin.%[1]s) ───────────────────────────────────
server {
    listen 80;
    listen [::]:80;
    server_name admin.%[1]s;

    location /api/admin/ {
        proxy_pass http://127.0.0.1:8000;
        proxy_http_version 1.1;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
        client_max_body_size 512m;
    }

    location / { root /var/www/heyearth; try_files $uri $uri/ /index.html; }
    location = /index.html { root /var/www/heyearth; add_header Cache-Control "no-cache, no-store, must-revalidate" always; }
}

# ── Redirect site.%[1]s → %[1]s ────────────────────────────────
server {
    listen 80;
    listen [::]:80;
    server_name site.%[1]s;
    return 301 $scheme://%[1]s$request_uri;
}

# ── Catch-all ───────────────────────────────────────────────────
server {
    listen 80 default_server;
    listen [::]:80 default_server;
    server_name _;
    return 301 http://%[1]s$request_uri;
}
`, domain, appDir)
}

func buildNginxConfSSL(domain, certPath string, xrayActive bool) string {
	// Когда Xray активен, порт 443 занят nginx stream для SNI routing.
	// HTTPS server blocks слушают на 8443 (внутренний), трафик приходит через stream.
	// Плюс listener 127.0.0.1:8446 с PROXY protocol — цель stream, пока включён
	// --stream-proxy-protocol (nginxProxyProtocolListen). Он есть всегда: без
	// PROXY-трафика он просто не используется, а 8443 остаётся обычным для прямых
	// локальных проверок (монитор сертификата в cmd/api).
	httpsPort := "443"
	ppListen := ""
	if xrayActive {
		httpsPort = "8443"
		ppListen = nginxProxyProtocolListen
	}

	return fmt.Sprintf(`# ── HTTP → HTTPS redirect ────────────────────────────────────────
server {
    listen 80;
    listen [::]:80;
    server_name %[1]s www.%[1]s admin.%[1]s site.%[1]s;
    return 301 https://$host$request_uri;
}

# ── Main site + API (%[1]s) — HTTPS ─────────────────────────────
server {
    listen %[3]s ssl http2;
    listen [::]:%[3]s ssl http2;
%[4]s    server_name %[1]s www.%[1]s;

    ssl_certificate %[2]s/fullchain.pem;
    ssl_certificate_key %[2]s/privkey.pem;
    include /etc/letsencrypt/options-ssl-nginx.conf;
    ssl_dhparam /etc/letsencrypt/ssl-dhparams.pem;

    location /heyearth.v1.ClientApi/ { grpc_pass 127.0.0.1:50051; grpc_set_header Host $host; }
    location = /robots.txt { default_type text/plain; return 200 "User-agent: *\nDisallow: /api/\n"; }

    location /api/ {
        proxy_pass http://127.0.0.1:8000;
        proxy_http_version 1.1;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
        client_max_body_size 512m;
    }

    # geoip.dat for Happ-client smart-routing (Geoipurl in subscription deeplink).
    # alias to /etc/hysteria/geoip.dat (Loyalsoldier release, refreshed monthly).
    # www-data needs read access via setfacl — see cmd/deploy/hysteria2_hub.go.
    location = /static/geoip.dat {
        alias /etc/hysteria/geoip.dat;
        default_type application/octet-stream;
        add_header Cache-Control "public, max-age=86400" always;
        add_header X-Content-Type-Options "nosniff" always;
    }

    location / { root /var/www/heyearth-site; try_files $uri $uri/ /index.html; }
    location = /index.html { root /var/www/heyearth-site; add_header Cache-Control "no-cache, no-store, must-revalidate" always; }
}

# ── Admin panel (admin.%[1]s) — HTTPS ───────────────────────────
server {
    listen %[3]s ssl http2;
    listen [::]:%[3]s ssl http2;
%[4]s    server_name admin.%[1]s;

    ssl_certificate %[2]s/fullchain.pem;
    ssl_certificate_key %[2]s/privkey.pem;
    include /etc/letsencrypt/options-ssl-nginx.conf;
    ssl_dhparam /etc/letsencrypt/ssl-dhparams.pem;

    location /api/admin/ {
        proxy_pass http://127.0.0.1:8000;
        proxy_http_version 1.1;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
        client_max_body_size 512m;
    }

    location / { root /var/www/heyearth; try_files $uri $uri/ /index.html; }
    location = /index.html { root /var/www/heyearth; add_header Cache-Control "no-cache, no-store, must-revalidate" always; }
}

# ── Redirect site.%[1]s → %[1]s (HTTPS) ────────────────────────
server {
    listen %[3]s ssl http2;
    listen [::]:%[3]s ssl http2;
%[4]s    server_name site.%[1]s;

    ssl_certificate %[2]s/fullchain.pem;
    ssl_certificate_key %[2]s/privkey.pem;
    include /etc/letsencrypt/options-ssl-nginx.conf;
    ssl_dhparam /etc/letsencrypt/ssl-dhparams.pem;

    return 301 https://%[1]s$request_uri;
}

# ── Catch-all HTTP ──────────────────────────────────────────────
server {
    listen 80 default_server;
    listen [::]:80 default_server;
    server_name _;
    return 301 https://%[1]s$request_uri;
}
`, domain, certPath, httpsPort, ppListen)
}

// buildNginxStreamConf создаёт конфиг nginx stream для SNI routing на порту 443:
// наши домены → nginx HTTPS (8443), остальное → Xray VLESS Reality (10443).
// proxyProtocol (режим --stream-proxy-protocol on): stream шлёт PROXY-заголовок с
// реальным адресом клиента, домены идут на PROXY-listener 127.0.0.1:8446, Reality — на
// зеркальный inbound 127.0.0.1:10444 с acceptProxyProtocol (оба есть всегда, см. stream_proxy.go).
func buildNginxStreamConf(domain string, proxyProtocol bool) string {
	httpsUpstream, xrayUpstream, ppLine := "127.0.0.1:8443", "127.0.0.1:"+xrayRealityPort, ""
	if proxyProtocol {
		httpsUpstream, xrayUpstream, ppLine = "127.0.0.1:"+nginxProxyProtocolPort, "127.0.0.1:"+xrayRealityPPPort, "    "+streamProxyProtocolLine+"\n"
	}
	return fmt.Sprintf(`# SNI-based routing: наши домены → nginx HTTPS, VPN-трафик → Xray
map $ssl_preread_server_name $backend {
    %[1]s          nginx_https;
    www.%[1]s      nginx_https;
    admin.%[1]s    nginx_https;
    site.%[1]s     nginx_https;
    default        xray_vless;
}

upstream nginx_https {
    server %[2]s;
}

upstream xray_vless {
    server %[3]s;
}

server {
    listen 443 so_keepalive=60s:10s:3;
    listen [::]:443 so_keepalive=60s:10s:3;
    ssl_preread on;
%[4]s    # = xray connIdle 3600s; default 10m cut idle long-lived sessions behind the SNI router.
    proxy_timeout 3600s;
    proxy_pass $backend;
}
`, domain, httpsUpstream, xrayUpstream, ppLine)
}

func doSSL(client *ssh.Client, cfg *deployConfig) {
	if strings.TrimSpace(cfg.letsEncryptEmail) == "" {
		return
	}
	// Устанавливаем certbot и получаем сертификат (certonly — без изменения nginx-конфига)
	cmd := fmt.Sprintf("apt-get install -y -qq certbot python3-certbot-nginx && certbot certonly --nginx -d %s -d www.%s -d admin.%s -d site.%s --non-interactive --agree-tos -m %s",
		cfg.siteDomain, cfg.siteDomain, cfg.siteDomain, cfg.siteDomain, cfg.letsEncryptEmail)
	_, _ = runSSH(client, cmd, false)
}

func restartServices(client *ssh.Client) {
	_, _ = runSSH(client, "systemctl stop telegram-bot 2>/dev/null; systemctl disable telegram-bot 2>/dev/null; true", false)
	_, _ = runSSH(client, "systemctl restart heyearth-api nginx", false)
	_, _ = runSSH(client, "systemctl restart xray 2>/dev/null || true", false)
}

func publicBaseURL(cfg *deployConfig) string {
	if configured := strings.TrimSpace(cfg.env.get("PUBLIC_BASE_URL", "")); configured != "" {
		return configured
	}
	scheme := "http"
	if strings.TrimSpace(cfg.letsEncryptEmail) != "" {
		scheme = "https"
	}
	return fmt.Sprintf("%s://%s", scheme, cfg.siteDomain)
}

func verifyServices(client *ssh.Client) {
	_, _ = runSSH(client, "systemctl is-active heyearth-api nginx", false)
	_, _ = runSSH(client, "systemctl is-active xray 2>/dev/null || echo 'xray: not installed'", false)
	_, _ = runSSH(client, "curl -s http://127.0.0.1:8000/api/v1/health || true", false)
}

// setupXray устанавливает Xray-core на Hub: бинарник, ключи Reality, bootstrap-конфиг (VLESS
// Reality на 127.0.0.1:10443, только если конфига ещё нет), юнит. Порт 443 обслуживается через
// nginx stream → SNI routing; полный конфиг генерирует heyearth-api.
func setupXray(client *ssh.Client, cfg *deployConfig, force bool) {
	fmt.Println("[xray] Установка Xray-core на сервер...")
	if force {
		fmt.Println("[xray] --force: существующий /opt/heyearth/xray/config.json будет заменён bootstrap-конфигом")
	}
	_, _ = runSSH(client, setupXrayScript(force), false)
	fmt.Println("[xray] Установка завершена")
}

// setupXrayScript renders the --setup-xray provisioning script: binary, Reality keys (kept when
// present), the BOOTSTRAP config (only when there is no config.json yet — on a live Hub that file
// is heyearth-api's generated config with every user; force=true replaces it anyway), the unit,
// firewall, nginx stream module. xray is restarted only when the bootstrap was written or it is
// not running. Pure, so tests can render it.
func setupXrayScript(force bool) string {
	forceVal := "0"
	if force {
		forceVal = "1"
	}
	return `set -e
mkdir -p /opt/heyearth/xray /opt/heyearth/state
FORCE=` + forceVal + `

# ── 1. Скачать Xray если нет ──
if [ ! -f /opt/heyearth/xray/xray ]; then
  apt-get install -y -qq wget unzip 2>/dev/null
  cd /tmp
  wget -q https://github.com/XTLS/Xray-core/releases/download/v25.1.1/Xray-linux-64.zip -O xray.zip
  unzip -o xray.zip -d /opt/heyearth/xray
  chmod +x /opt/heyearth/xray/xray
  rm -f xray.zip
  echo "[xray] Binary v25.1.1 установлен"
else
  echo "[xray] Binary уже существует: $(/opt/heyearth/xray/xray version 2>&1 | head -1)"
fi

# ── 2. Сгенерировать ключи если нет ──
KEYS_FILE="/opt/heyearth/state/reality-keys.json"
if [ ! -f "$KEYS_FILE" ]; then
  KEYS=$(/opt/heyearth/xray/xray x25519 2>&1)
  PRIV_KEY=$(echo "$KEYS" | grep 'Private key:' | awk '{print $3}')
  PUB_KEY=$(echo "$KEYS" | grep 'Public key:' | awk '{print $3}')
  SHORT_ID=$(head -c 8 /dev/urandom | xxd -p)
  UUID=$(cat /proc/sys/kernel/random/uuid)
  SS_PASSWORD=$(openssl rand -base64 16)

  cat > "$KEYS_FILE" <<KEYSEOF
{
  "privateKey": "$PRIV_KEY",
  "publicKey": "$PUB_KEY",
  "shortId": "$SHORT_ID",
  "uuid": "$UUID",
  "ssPassword": "$SS_PASSWORD"
}
KEYSEOF
  echo "[xray] Ключи сгенерированы: UUID=$UUID"
else
  echo "[xray] Ключи уже существуют"
  PRIV_KEY=$(python3 -c "import json; print(json.load(open('$KEYS_FILE'))['privateKey'])")
  PUB_KEY=$(python3 -c "import json; print(json.load(open('$KEYS_FILE'))['publicKey'])")
  SHORT_ID=$(python3 -c "import json; print(json.load(open('$KEYS_FILE'))['shortId'])")
  UUID=$(python3 -c "import json; print(json.load(open('$KEYS_FILE')).get('uuid', ''))" 2>/dev/null)
  SS_PASSWORD=$(python3 -c "import json; print(json.load(open('$KEYS_FILE')).get('ssPassword', ''))" 2>/dev/null)
  # Добавить UUID если нет
  if [ -z "$UUID" ]; then
    UUID=$(cat /proc/sys/kernel/random/uuid)
    python3 -c "
import json
d = json.load(open('$KEYS_FILE'))
d['uuid'] = '$UUID'
json.dump(d, open('$KEYS_FILE', 'w'), indent=2)
"
    echo "[xray] UUID добавлен: $UUID"
  fi
  # Добавить Shadowsocks пароль если нет
  if [ -z "$SS_PASSWORD" ]; then
    SS_PASSWORD=$(openssl rand -base64 16)
    python3 -c "
import json
d = json.load(open('$KEYS_FILE'))
d['ssPassword'] = '$SS_PASSWORD'
json.dump(d, open('$KEYS_FILE', 'w'), indent=2)
"
    echo "[xray] SS пароль добавлен"
  fi
fi

# ── 3. Bootstrap-конфиг Xray (только VLESS Reality) — только если конфига ещё нет ──
` + xrayBootstrapBlock + `
# ── 4. systemd-сервис ──
cat > /etc/systemd/system/xray.service <<'SVCEOF'
[Unit]
Description=Xray Service (VLESS Reality)
After=network.target
Wants=network-online.target

[Service]
Type=simple
User=root
WorkingDirectory=/opt/heyearth/xray
ExecStart=/opt/heyearth/xray/xray run -config /opt/heyearth/xray/config.json
Restart=always
RestartSec=5
LimitNOFILE=65535

[Install]
WantedBy=multi-user.target
SVCEOF

systemctl daemon-reload
systemctl enable xray
# A live Hub keeps its connections: restart only for a new bootstrap config or a stopped xray.
if [ "$XRAY_BOOTSTRAP_WROTE" = 1 ] || ! systemctl is-active --quiet xray; then
  systemctl restart xray
  sleep 1
  echo "[xray] Сервис (пере)запущен"
else
  echo "[xray] Сервис работает, конфиг не менялся — без перезапуска"
fi
systemctl status xray --no-pager -l 2>&1 | head -15 || true

# ── 5. Firewall: только порты со слушателем (spec 2026-10-11, часть 4) ──
# 443 → nginx stream (SNI → 8443 / 10443); 2443 → Xray XHTTP. 1080/8080/8388 удалены — не открывать.
if command -v ufw >/dev/null 2>&1; then
  for r in 22/tcp 80/tcp 443/tcp 2443/tcp; do ufw allow "$r" 2>/dev/null || true; done
  ufw --force enable 2>/dev/null || true
  ufw status numbered 2>/dev/null || true
else
  iptables -I INPUT -p tcp --dport 2443 -j ACCEPT 2>/dev/null || true
fi

# ── 6. nginx stream модуль ──
apt-get install -y -qq libnginx-mod-stream 2>/dev/null || true
echo "[xray] nginx stream модуль установлен"

# ── 7. Проверить что порты слушаются ──
echo "--- Проверка портов ---"
ss -tlnp | grep -E ':10443\s' || echo "ВНИМАНИЕ: Xray не слушает 10443!"
`
}

// xrayBootstrapBlock is step 3 of setupXrayScript: it writes the bootstrap config to $XRAY_CONF
// unless that file exists and FORCE != 1 (on a live Hub it is heyearth-api's generated config
// with every user; overwriting it would cut them all off until the next sync). Atomic: a
// .tmp.json (xray needs the extension) checked with `xray run -test`, then mv. Sets
// XRAY_BOOTSTRAP_WROTE=1 when it wrote. Needs UUID, PRIV_KEY, SHORT_ID. The Reality SNI is
// config.DefaultRealitySNI, shaped like cmd/api/xray_sync.go (dest <sni>:443, serverNames <sni>
// and www.<sni>); heyearth-api later replaces the whole file using app_settings.reality_sni_domain.
// Like the generated config it has both entry points of the 443 stream: vless-reality
// (127.0.0.1:10443) and its PROXY-protocol mirror vless-reality-pp (127.0.0.1:10444), so a stream
// in either mode (deploy --stream-proxy-protocol) reaches Reality from the first xray start.
// XRAY_CONF and XRAY_BIN are test hooks.
const xrayBootstrapBlock = `XRAY_CONF="${XRAY_CONF:-/opt/heyearth/xray/config.json}"
XRAY_BIN="${XRAY_BIN:-/opt/heyearth/xray/xray}"
XRAY_BOOTSTRAP_WROTE=0
if [ -f "$XRAY_CONF" ] && [ "${FORCE:-0}" != 1 ]; then
  echo "[xray] $XRAY_CONF уже есть (его генерирует heyearth-api) — bootstrap НЕ записываю (перезаписать: --setup-xray --force)"
else
  XRAY_TMP="${XRAY_CONF%.json}.tmp.json"
  cat > "$XRAY_TMP" <<XRAYEOF
{
  "log": {"loglevel": "warning"},
  "dns": {
    "servers": ["8.8.8.8", "1.1.1.1"]
  },
  "inbounds": [
    {
      "tag": "vless-reality",
      "listen": "127.0.0.1",
      "port": 10443,
      "protocol": "vless",
      "settings": {
        "clients": [{"id": "$UUID", "flow": "xtls-rprx-vision"}],
        "decryption": "none"
      },
      "streamSettings": {
        "network": "tcp",
        "security": "reality",
        "realitySettings": {
          "show": false,
          "dest": "` + config.DefaultRealitySNI + `:443",
          "xver": 0,
          "serverNames": ["` + config.DefaultRealitySNI + `", "www.` + config.DefaultRealitySNI + `"],
          "privateKey": "$PRIV_KEY",
          "shortIds": ["$SHORT_ID"]
        }
      },
      "sniffing": {"enabled": true, "destOverride": ["http", "tls", "quic"]}
    },
    {
      "tag": "` + xrayRealityPPTag + `",
      "listen": "127.0.0.1",
      "port": ` + xrayRealityPPPort + `,
      "protocol": "vless",
      "settings": {
        "clients": [{"id": "$UUID", "flow": "xtls-rprx-vision"}],
        "decryption": "none"
      },
      "streamSettings": {
        "network": "tcp",
        "security": "reality",
        "realitySettings": {
          "show": false,
          "dest": "` + config.DefaultRealitySNI + `:443",
          "xver": 0,
          "serverNames": ["` + config.DefaultRealitySNI + `", "www.` + config.DefaultRealitySNI + `"],
          "privateKey": "$PRIV_KEY",
          "shortIds": ["$SHORT_ID"]
        },
        "sockopt": {"acceptProxyProtocol": true}
      },
      "sniffing": {"enabled": true, "destOverride": ["http", "tls", "quic"]}
    }
  ],
  "outbounds": [
    {"protocol": "freedom", "tag": "direct", "settings": {"domainStrategy": "UseIP"}},
    {"protocol": "blackhole", "tag": "block", "settings": {"response": {"type": "none"}}}
  ],
  "routing": {
    "domainStrategy": "IPIfNonMatch",
    "rules": [
      {"type": "field", "ip": ["geoip:private"], "outboundTag": "block"},
      {"type": "field", "protocol": ["bittorrent"], "outboundTag": "block"}
    ]
  }
}
XRAYEOF
  if [ -x "$XRAY_BIN" ] && ! "$XRAY_BIN" run -test -config "$XRAY_TMP" >/dev/null 2>&1; then
    rm -f "$XRAY_TMP"
    echo "[xray] ОШИБКА: bootstrap-конфиг не прошёл xray run -test — $XRAY_CONF не тронут" >&2
    exit 1
  fi
  mv -f "$XRAY_TMP" "$XRAY_CONF"
  XRAY_BOOTSTRAP_WROTE=1
  echo "[xray] Bootstrap-конфиг записан (VLESS:10443 + PROXY-зеркало :10444, SNI ` + config.DefaultRealitySNI + `; остальное генерирует heyearth-api)"
fi`

// isXrayInstalled проверяет наличие Xray на сервере
func isXrayInstalled(client *ssh.Client) (bool, error) {
	// Проверяем наличие бинарника Xray И наличие systemd-юнита.
	// Не проверяем is-active — Xray может быть в состоянии "activating" при старте,
	// но stream-конфиг nginx всё равно нужен для SNI routing. Ошибка SSH — это ошибка, а не
	// «не установлен»: иначе doNginx удалил бы stream-конфиг (fail closed).
	installed, err := parseYesNo(runSSHQuiet(client, xrayInstalledCmd, ""))
	if err != nil {
		return false, fmt.Errorf("проверка xray: %w", err)
	}
	return installed, nil
}

// xrayInstalledCmd prints yes when the xray binary exists and its unit is enabled, else no.
const xrayInstalledCmd = "if [ -f /opt/heyearth/xray/xray ] && systemctl is-enabled --quiet xray 2>/dev/null; then echo yes; else echo no; fi"

// nginxApplyCmd validates and applies doNginx's config: reload when nginx runs (connections
// survive), start when it does not (fresh install).
const nginxApplyCmd = "rm -f /etc/nginx/sites-enabled/default && nginx -t && systemctl enable nginx && " +
	"if systemctl is-active --quiet nginx; then systemctl reload nginx; else systemctl start nginx; fi"

// parseYesNo reads the answer of a remote yes/no check; an SSH error or any other output is an
// error, never a "no".
func parseYesNo(out string, err error) (bool, error) {
	if err != nil {
		return false, err
	}
	switch strings.TrimSpace(out) {
	case "yes":
		return true, nil
	case "no":
		return false, nil
	}
	return false, fmt.Errorf("unexpected answer %q", strings.TrimSpace(out))
}

// seedConnectionSettings читает Xray-конфиг и reality-ключи с сервера,
// затем вставляет настройки подключения в app_settings через SQL.
func seedConnectionSettings(client *ssh.Client, cfg *deployConfig) {
	fmt.Println("[seed] Автозаполнение настроек подключения...")
	_, _ = runSSH(client, seedConnectionSettingsScript(cfg.host), false)
}

// seedConnectionSettingsScript читает reality-keys.json и xray config, затем вставляет в БД.
// psql идёт как postgres по peer auth: пароль БД (DATABASE_URL) в скрипт и argv не попадает.
func seedConnectionSettingsScript(host string) string {
	return fmt.Sprintf(`set -e
MAIN_HOST="%s"

# Читаем Reality-ключи
KEYS_FILE="/opt/heyearth/state/reality-keys.json"
if [ -f "$KEYS_FILE" ]; then
  PUB_KEY=$(python3 -c "import json; print(json.load(open('$KEYS_FILE'))['publicKey'])" 2>/dev/null || echo "")
  SHORT_ID=$(python3 -c "import json; print(json.load(open('$KEYS_FILE'))['shortId'])" 2>/dev/null || echo "")
else
  PUB_KEY=""
  SHORT_ID=""
fi

# Ищем VLESS UUID из xray конфига
XRAY_CONF="/opt/heyearth/xray/config.json"
if [ -f "$XRAY_CONF" ]; then
  VLESS_UUID=$(python3 -c "
import json
cfg = json.load(open('$XRAY_CONF'))
for inb in cfg.get('inbounds', []):
    if inb.get('protocol') == 'vless':
        for client in inb.get('settings', {}).get('clients', []):
            print(client.get('id', ''))
            break
        break
" 2>/dev/null || echo "")
else
  VLESS_UUID=""
fi

# Читаем SNI из xray конфига
if [ -f "$XRAY_CONF" ]; then
  REALITY_SNI=$(python3 -c "
import json
cfg = json.load(open('$XRAY_CONF'))
for inb in cfg.get('inbounds', []):
    sn = inb.get('streamSettings', {}).get('realitySettings', {}).get('serverNames', [])
    if sn:
        print(sn[0])
        break
" 2>/dev/null || echo "")
else
  REALITY_SNI=""
fi

echo "  main_server_host=$MAIN_HOST"
echo "  vless_uuid=$VLESS_UUID"
echo "  reality_public_key=$PUB_KEY"
echo "  reality_short_id=$SHORT_ID"
echo "  reality_sni=$REALITY_SNI"

# main_server_host — только если его ещё нет: его меняют в админке (домен вместо IP), а
# --update-main с DEPLOY_HOST (IP для SSH) не должен его перетирать.
runuser -u postgres -- psql -d heyearth -c "
INSERT INTO app_settings (key, value, updated_at) VALUES ('main_server_host', '$MAIN_HOST', CURRENT_TIMESTAMP)
ON CONFLICT (key) DO NOTHING;
INSERT INTO app_settings (key, value, updated_at) VALUES ('vless_port', '443', CURRENT_TIMESTAMP)
ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = CURRENT_TIMESTAMP;
" 2>/dev/null || true

# Вставляем ключи xray (перезаписываем)
if [ -n "$VLESS_UUID" ]; then
  runuser -u postgres -- psql -d heyearth -c "INSERT INTO app_settings (key, value, updated_at) VALUES ('vless_uuid', '$VLESS_UUID', CURRENT_TIMESTAMP) ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = CURRENT_TIMESTAMP;" 2>/dev/null || true
fi
if [ -n "$PUB_KEY" ]; then
  runuser -u postgres -- psql -d heyearth -c "INSERT INTO app_settings (key, value, updated_at) VALUES ('reality_public_key', '$PUB_KEY', CURRENT_TIMESTAMP) ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = CURRENT_TIMESTAMP;" 2>/dev/null || true
fi
if [ -n "$SHORT_ID" ]; then
  runuser -u postgres -- psql -d heyearth -c "INSERT INTO app_settings (key, value, updated_at) VALUES ('reality_short_id', '$SHORT_ID', CURRENT_TIMESTAMP) ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = CURRENT_TIMESTAMP;" 2>/dev/null || true
fi
# SNI — только если в БД его ещё нет: значением управляет auto-tuner/админка, а bootstrap-конфиг
# (--setup-xray после восстановления БД) не должен его перетирать.
if [ -n "$REALITY_SNI" ]; then
  runuser -u postgres -- psql -d heyearth -c "INSERT INTO app_settings (key, value, updated_at) VALUES ('reality_sni_domain', '$REALITY_SNI', CURRENT_TIMESTAMP) ON CONFLICT (key) DO NOTHING;" 2>/dev/null || true
fi

echo "[seed] Готово"
`, host)
}

func doStatus(client *ssh.Client, cfg *deployConfig) {
	fmt.Println("=== Состояние сервисов ===")
	out, _ := runSSH(client, "systemctl is-active heyearth-api nginx 2>/dev/null; echo '---'; systemctl status heyearth-api --no-pager -l 2>&1 | tail -18", false)
	fmt.Println(out)

	// Xray
	fmt.Println("=== Xray ===")
	xrayOut, _ := runSSH(client, `systemctl is-active xray 2>/dev/null || echo 'NOT RUNNING'
if [ -f /opt/heyearth/xray/config.json ]; then
  python3 -c "
import json, sys
c=json.load(open('/opt/heyearth/xray/config.json'))
rs=c['inbounds'][0].get('streamSettings',{}).get('realitySettings',{})
print('  dest:', rs.get('dest'))
print('  serverNames:', rs.get('serverNames'))
print('  shortIds:', rs.get('shortIds'))
print('  clients:', len(c['inbounds'][0]['settings']['clients']))
print('  inbounds:', len(c['inbounds']))
print('  outbounds:', len(c['outbounds']))
" 2>&1
else
  echo 'Xray config not found'
fi
echo '--- ports ---'
ss -tlnp 2>/dev/null | grep -E ':(443|2443|10443) ' || echo 'no matching ports'`, false)
	fmt.Println(xrayOut)

	// Hysteria2 (Express)
	fmt.Println("=== Hysteria2 (Express) ===")
	hy2Out, _ := runSSH(client, `if command -v hysteria >/dev/null 2>&1; then
  echo "binary: $(hysteria version 2>/dev/null | awk '$1=="Version:"{print $2}') (pinned: `+config.HysteriaVersion+`)"
else
  echo "binary: NOT INSTALLED"
fi
systemctl is-active hysteria2-server 2>/dev/null || echo "service: not running"
if [ -f /etc/hysteria/server.yaml ]; then
  echo "config: present ($(wc -l < /etc/hysteria/server.yaml) lines)"
else
  echo "config: absent"
fi
echo '--- UDP listener ---'
ss -ulnp 2>/dev/null | grep ':50443 ' || echo 'no UDP listener on :50443'
echo '--- iptables nat (port hopping 30000:65000 -> :50443, dst-type LOCAL) ---'
iptables -t nat -S PREROUTING 2>/dev/null | grep -E -- '--to-destination :50443|--to-ports 50443' || echo 'no port-hop DNAT rule found'
echo '--- auth backend probe (auth-only listener 127.0.0.1:`+egressHyAuthPort+`) ---'
grep -E '^ *url:' /etc/hysteria/server.yaml 2>/dev/null | head -1 | sed 's/^ */server.yaml auth /' || true
HY_AUTH_CODE=$(curl -s -m 5 -o /dev/null -w '%{http_code}' -X POST `+egressHyAuthURL+` -d '{"auth":"_probe_"}' 2>/dev/null || true)
if [ -n "$HY_AUTH_CODE" ] && [ "$HY_AUTH_CODE" != 000 ]; then
  echo "`+egressHyAuthURL+`: http=$HY_AUTH_CODE (отвечает)"
else
  echo "`+egressHyAuthURL+`: НЕ отвечает — API без слушателя 8001 (нужен 4ab930e+); старый путь на 8000:"
  curl -s -m 5 -w ' http=%{http_code}' -X POST http://127.0.0.1:`+egressAPIPort+`/internal/hysteria2/auth -d '{"auth":"_probe_"}' 2>&1 | head -2
fi`, false)
	fmt.Println(hy2Out)

	// Express (Hysteria2) — DB-driven runtime view: Bamboo's exit, config_version, obfs rotation age, active clients.
	fmt.Println("=== Express (Hysteria2) ===")
	expressOut, _ := runSSH(client, `# server status + UDP listener
SRV=$(systemctl is-active hysteria2-server 2>/dev/null || echo inactive)
LISTEN=$(ss -ulnp 2>/dev/null | grep ':50443' | head -1 | awk '{print $5}')
echo "  server: $SRV${LISTEN:+, UDP $LISTEN}"

# Bamboo's exit (biz.ExpressExit) + healthy exits
N=$(su - postgres -c "psql -d heyearth -tA -c \"SELECT COUNT(*) FROM servers WHERE COALESCE(is_exit_node,FALSE)=TRUE AND COALESCE(lifecycle_state,'')='online' AND COALESCE(tunnel_status,'')='healthy';\"" 2>/dev/null | head -1 | tr -d ' ')
BEST=$(su - postgres -c "psql -d heyearth -tA -c \"SELECT '#' || id || ' ' || COALESCE(country_code,'??') || ' ' || ip_address || ' (RTT ' || COALESCE(last_rtt_ms::text,'?') || 'ms, since ' || COALESCE((SELECT value FROM app_settings WHERE key='hysteria2_bound_exit_since'),'?') || ')' FROM servers WHERE id = (SELECT NULLIF(value,'')::int FROM app_settings WHERE key='hysteria2_bound_exit_id');\"" 2>/dev/null | head -1)
echo "  bamboo exit: ${BEST:-none} (${N:-0} healthy exits)"

# config version
VER=$(su - postgres -c "psql -d heyearth -tA -c \"SELECT COALESCE(value,'?') FROM app_settings WHERE key='hysteria2_config_version';\"" 2>/dev/null | head -1 | tr -d ' ')
echo "  config_version: ${VER:-?}"

# obfs rotation age
ROT=$(su - postgres -c "psql -d heyearth -tA -c \"SELECT COALESCE(value,'never') FROM app_settings WHERE key='hysteria2_obfs_rotated_at';\"" 2>/dev/null | head -1 | tr -d ' ')
echo "  obfs rotation: ${ROT:-never}"

# active clients via stats API
SECRET=$(su - postgres -c "psql -d heyearth -tA -c \"SELECT COALESCE(value,'') FROM app_settings WHERE key='hysteria2_stats_secret';\"" 2>/dev/null | head -1 | tr -d ' ')
CLIENTS=$(curl -s -m 1 -H "Authorization: ${SECRET}" http://127.0.0.1:7653/traffic 2>/dev/null | python3 -c "import sys,json; d=json.load(sys.stdin); print(len(d))" 2>/dev/null)
echo "  active clients: ${CLIENTS:-?}"`, false)
	fmt.Println(expressOut)

	// PROXY-protocol mode of the 443 stream and the four loopback entry points
	fmt.Println("=== 443 stream: PROXY protocol ===")
	ppOut, ppErr := runSSHQuiet(client, streamStatusCmd, "")
	for _, l := range streamStatusReport(ppOut, ppErr) {
		fmt.Println(l)
	}

	// nginx stream
	fmt.Println("=== Nginx stream config ===")
	nginxOut, _ := runSSH(client, "cat /etc/nginx/stream.d/heyearth.conf 2>/dev/null || echo 'NO STREAM CONFIG'", false)
	fmt.Println(nginxOut)

	fmt.Println("=== Логи heyearth-api (последние 25 строк) ===")
	logsOut, _ := runSSH(client, "journalctl -u heyearth-api -n 25 --no-pager 2>&1", false)
	fmt.Println(logsOut)
	fmt.Println("=== API health (127.0.0.1:8000) ===")
	healthOut, _ := runSSH(client, "curl -s -w '\\nHTTP_CODE:%{http_code}' http://127.0.0.1:8000/api/v1/health", false)
	fmt.Println(healthOut)
	fmt.Println("=== API admin login (ожидается JSON) ===")
	loginOut, _ := runSSH(client, "curl -s -w '\\nHTTP_CODE:%{http_code}' -X POST http://127.0.0.1:8000/api/admin/login -H 'Content-Type: application/json' -d '{\"username\":\"x\",\"password\":\"y\"}'", false)
	fmt.Println(loginOut)
	if strings.Contains(loginOut, "<html") || strings.Contains(loginOut, "<!DOCTYPE") {
		fmt.Println("\n[!] Ответ API содержит HTML вместо JSON — возможно, nginx отдаёт ошибку или не проксирует на backend.")
	} else if strings.Contains(loginOut, "token") || strings.Contains(loginOut, "error") || strings.Contains(loginOut, "message") {
		fmt.Println("\n[OK] API отвечает JSON.")
	}
}

func doFixDB(client *ssh.Client, cfg *deployConfig) {
	fmt.Println("Проверка PostgreSQL...")
	out, _ := runSSH(client, "systemctl start postgresql 2>&1; systemctl enable postgresql 2>&1; true", false)
	if strings.Contains(out, "not found") {
		fmt.Println("PostgreSQL не установлен. Устанавливаю...")
		installPostgresOnServer(client)
	} else {
		fmt.Println(out)
	}
	// На свежем Ubuntu postgres требует пароль для TCP. Пароль задаётся только если у роли его
	// ещё нет (тогда он случайный и попадает в api.env); существующий не сбрасывается.
	fmt.Println("Настройка доступа к БД и перезапуск heyearth-api...")
	dbURL := resolveHubDatabaseURL(client, cfg)
	if err := writeHubDatabaseURL(client, dbURL); err != nil {
		log.Fatalf("[db] не удалось записать DATABASE_URL в %s: %v", hubAPIEnv, err)
	}
	_, _ = runSSH(client, "systemctl restart heyearth-api; sleep 3", false)
	fmt.Println("Проверка:")
	doStatus(client, cfg)
}

// filterEnv removes all entries with the given key from an environment slice.
func filterEnv(env []string, removeKey string) []string {
	prefix := removeKey + "="
	var out []string
	for _, e := range env {
		if !strings.HasPrefix(e, prefix) {
			out = append(out, e)
		}
	}
	return out
}

// managedEnvKeys are the api.env keys deploy writes (serializeEnv); every other key of the live
// api.env is the owner's and is kept as it is (preserveUnmanagedEnv).
var managedEnvKeys = []string{
	"APP_DIR",
	"DATABASE_URL",
	"ADMIN_USER",
	"ADMIN_PASS",
	"JWT_SECRET",
	"SITE_DOMAIN",
	"PUBLIC_BASE_URL",
	"TLS_CLIENT_CERT_FILE",
	"TLS_CLIENT_KEY_FILE",
	"TLS_CA_FILE",
	"SSH_ENCRYPTION_KEY",
}

var envLineRe = regexp.MustCompile(`^([A-Za-z_][A-Za-z0-9_]*)=`)

// preserveUnmanagedEnv appends to env (serializeEnv's output) the lines of the live api.env whose
// keys deploy does not manage (CORS_ALLOWED_ORIGINS, YOOKASSA_WEBHOOK_ALLOW_LOCAL, …), unchanged
// and in their order, and returns those key names (for a note; never the values). Comments, blank
// and malformed lines are not carried; managed keys come only from deploy.
func preserveUnmanagedEnv(env, live string) (string, []string) {
	var b strings.Builder
	b.WriteString(env)
	var names []string
	for _, line := range strings.Split(live, "\n") {
		line = strings.TrimRight(line, "\r")
		m := envLineRe.FindStringSubmatch(line)
		if m == nil || slices.Contains(managedEnvKeys, m[1]) {
			continue
		}
		b.WriteString(line + "\n")
		if !slices.Contains(names, m[1]) {
			names = append(names, m[1])
		}
	}
	return b.String(), names
}

func serializeEnv(values map[string]string) string {
	var b strings.Builder
	for _, key := range managedEnvKeys {
		if value, ok := values[key]; ok && strings.TrimSpace(value) != "" {
			b.WriteString(key)
			b.WriteString("=")
			b.WriteString(value)
			b.WriteString("\n")
		}
	}
	return b.String()
}

// parseExitNodeAddr validates --setup-exit-node: an IP address, optionally with a port (the
// port is ignored — the exit always uses 443/10443/50443). The IP ends up in root shell scripts
// on the exit and the Hub, so anything else (a hostname, shell metacharacters) is refused.
func parseExitNodeAddr(addr string) (string, error) {
	addr = strings.TrimSpace(addr)
	host := addr
	if net.ParseIP(addr) == nil {
		h, _, err := net.SplitHostPort(addr)
		if err != nil {
			return "", fmt.Errorf("--setup-exit-node %q: ожидается IP или IP:порт", addr)
		}
		host = h
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return "", fmt.Errorf("--setup-exit-node %q: %q не IP-адрес", addr, host)
	}
	return ip.String(), nil
}

// setupExitNodeDeploy — the emergency path: the same provisioning core as the admin panel
// (internal/provision, jobs owned by the CLI runner), run from this machine. The exit is reached
// by its root password only with its host key pinned (EXIT_NODE_HOST_KEY_FINGERPRINT or the key
// confirmed in the database — presented by the exit right now), the Hub over this deploy session,
// the database through an SSH tunnel. The node-agent CA key stays on the Hub: node certificates
// are signed there. Nothing outside the recipe is restarted: the API picks the exit up at its
// periodic sync (≤ 5 min). A new exit is written 'pending' (as the admin wizard does) and the job
// takes it to online — or failed, resumable with «Продолжить» or by running this again.
func setupExitNodeDeploy(addr string, mainClient *ssh.Client, cfg *deployConfig, opts exitCLIOptions) {
	exitIP, err := parseExitNodeAddr(addr)
	if err != nil {
		log.Fatalf("[exit-node] %v", err)
	}
	if err := config.ValidateExitIP(exitIP); err != nil {
		log.Fatalf("[exit-node] адрес не подходит для выхода: %v", err)
	}
	password, envFP, err := exitNodeCredentials(cfg.env, opts.password)
	if err != nil {
		log.Fatalf("[exit-node] %v", err)
	}
	ctx := context.Background()
	hub := provision.NewSSHRemote(mainClient)

	// 1. The Hub is prepared (--ensure-hub-provisioning): the key for node passwords, its own CA.
	hubEnv, exists, err := readHubAPIEnv(mainClient)
	if err != nil || !exists {
		log.Fatalf("[exit-node] %s на Hub не прочитан (%v)", hubAPIEnv, err)
	}
	key := envFileValue(hubEnv, "SSH_ENCRYPTION_KEY")
	if !validSSHKey(key) {
		log.Fatal("[exit-node] в api.env на Hub нет SSH_ENCRYPTION_KEY — сначала go run ./cmd/deploy --ensure-hub-provisioning")
	}
	if err := os.Setenv("SSH_ENCRYPTION_KEY", key); err != nil {
		log.Fatal(err)
	}
	// Node certificates are dated and checked by the Hub's clock (the Hub's API verifies them).
	now := hubClock(ctx, hub)
	caFiles, err := readHubCAFiles(ctx, hub, hubCertsDir)
	if err != nil {
		log.Fatalf("[exit-node] CA на Hub: %v", err)
	}
	if _, err := hubCAUsable(caFiles, now()); err != nil {
		log.Fatalf("[exit-node] CA node-agent на Hub не готов (%v) — сначала go run ./cmd/deploy --ensure-hub-provisioning", err)
	}

	// 2. The database and the exit's row as it is now.
	db, closeDB, err := openDBTunnel(mainClient, envFileValue(hubEnv, "DATABASE_URL"))
	if err != nil {
		log.Fatalf("[exit-node] БД через SSH-туннель: %v", err)
	}
	defer closeDB()
	cur, err := loadExitRow(ctx, db, exitIP, sshcrypto.Decrypt)
	if err != nil {
		log.Fatalf("[exit-node] %v", err)
	}
	if password == "" && !cur.HasPassword {
		log.Fatal("[exit-node] нужен пароль root самой exit-ноды (--exit-node-password или EXIT_NODE_SSH_PASSWORD): " +
			"в базе его нет или он не расшифровывается ключом Hub")
	}

	// 3. The host key the owner confirmed, presented by the exit right now (key exchanges only,
	// no password is sent before the key matched).
	port := opts.sshPort
	if port == 0 {
		port = 22
		if cur.Found {
			port = cur.SSHPort
		}
	}
	probe, err := provision.Probe(ctx, exitIP, port)
	if err != nil {
		log.Fatalf("[exit-node] %v", err)
	}
	hostKey, err := chooseExitHostKey(cur, envFP, probe)
	if err != nil {
		log.Fatalf("[exit-node] %v", err)
	}
	fmt.Printf("[exit-node] %s:%d, ключ хоста %s %s\n", exitIP, port, hostKey.Type, hostKey.Fingerprint)
	if password != "" { // a new password is stored only once it logs in (with that key pinned)
		r, err := provision.DialNode(provision.Node{IP: exitIP, SSHPort: port, SSHUser: "root", HostKeyFP: hostKey.Fingerprint,
			HostKeyType: hostKey.Type, HostKeyConfirmed: true, Password: password})
		if err != nil {
			log.Fatalf("[exit-node] вход на exit с этим паролем не удался: %v", err)
		}
		provision.Close(r)
	}

	// 4. Everything the job takes, checked before the row is written: a refused recipe leaves no row.
	hubIPs, err := cliHubIPs(ctx, mainClient, db, cfg.host)
	if err != nil {
		log.Fatalf("[exit-node] %v", err)
	}
	eo, err := cliExitOptions(ctx, mainClient, hub, db, hubIPs)
	if err != nil {
		log.Fatalf("[exit-node] %v", err)
	}
	kind, lifecycle, role := provision.KindProvision, "pending", "exit"
	if cur.Found {
		lifecycle, role = cur.Lifecycle, cur.Role
	}
	if lifecycle == "online" {
		kind = provision.KindReprovision
	}
	dry := provision.NewState(kind, provision.Node{ID: max(cur.ID, 1), Role: role, IP: exitIP, Lifecycle: lifecycle}, hub, provision.Hooks{})
	dry.Exit = eo
	if _, err := provision.RecipeFor(kind, dry.Node, dry); err != nil {
		log.Fatalf("[exit-node] %v", err)
	}
	spec := opts.plans
	if spec == "" {
		spec = map[bool]string{false: "all-paid", true: "keep"}[cur.Found]
	}
	planKind, planIDs, err := parsePlanSpec(spec)
	if err != nil {
		log.Fatalf("[exit-node] %v", err)
	}
	if planKind == "all-paid" {
		if planIDs, err = provision.PaidPlanIDs(ctx, db); err != nil {
			log.Fatalf("[exit-node] тарифы: %v", err)
		}
	}

	// 5. The row, its plans and MTProxy switch.
	id, err := ensureExitRow(ctx, db, exitRow{IP: exitIP, Password: password, Key: hostKey, Name: opts.name, Country: opts.country, SSHPort: opts.sshPort}, cur)
	if err != nil {
		log.Fatalf("[exit-node] %v", err)
	}
	stranded := func() {
		if !cur.Found {
			fmt.Printf("[exit-node] запись id %d в базе — «ожидает настройки» (pending): повторите команду (продолжит) или удалите её в админке\n", id)
		}
	}
	if planKind != "keep" {
		if err := provision.BindPlans(ctx, db, id, planIDs); err != nil {
			stranded()
			log.Fatalf("[exit-node] тарифы: %v", err)
		}
	}
	if opts.mtproxy != nil {
		if _, err := db.ExecContext(ctx, exitMTProxySwitchQuery, id, *opts.mtproxy); err != nil {
			stranded()
			log.Fatalf("[exit-node] MTProxy: %v", err)
		}
	}

	// 6. The job: the provisioning core under the CLI runner (synchronous here).
	store := &provision.SQLStore{DB: db, Decrypt: sshcrypto.Decrypt, JobRunner: provision.RunnerCLI}
	runner := &provision.Runner{Store: store, Dial: provision.DialNode, Go: func(f func()) { f() }, Logf: log.Printf,
		Recipe: func(ctx context.Context, n provision.Node, kind string) (provision.Recipe, *provision.State, error) {
			st := provision.NewState(kind, n, hub, store.Hooks(func() {
				fmt.Println("[exit-node] Xray и монитор туннелей на Hub подхватят изменения при периодической синхронизации API (≤ 5 мин)")
			}, cliProbeAgent(hub, hubCertsDir))) // the Hub's own mTLS check of the agent
			st.Exit, st.Now = eo, now
			if n.MTProtoStatus == "disabled" { // switched off by the owner: no MTProxy step
				st.Exit.MTProto = nil
			}
			rc, err := provision.RecipeFor(kind, n, st)
			return rc, st, err
		}}
	jobID, err := runner.Start(ctx, id, kind)
	if err != nil {
		stranded()
		log.Fatalf("[exit-node] задача не запущена: %v", err)
	}
	job, err := store.JobView(ctx, jobID)
	if err != nil {
		log.Fatalf("[exit-node] задача %d: %v", jobID, err)
	}
	for _, s := range job.Steps {
		fmt.Printf("[exit-node] %-11s %s\n", s.Status, s.Title)
		if s.Status == provision.StepFailed || s.Status == provision.StepRolledBack {
			fmt.Println("    " + strings.ReplaceAll(s.OutputTail, "\n", "\n    "))
		}
	}
	if job.Status != provision.JobSucceeded {
		log.Fatalf("[exit-node] задача %d (%s): %s — исправьте причину и повторите команду или нажмите «Продолжить» в карточке ноды (шаги идемпотентны)",
			jobID, job.Status, job.Error)
	}
	fmt.Printf("[exit-node] готово: %s (id %d) — основной туннель и запасной TCP проверены\n", exitIP, id)
}

// openDBTunnel creates an SSH port-forward from a random local port to the
// PostgreSQL server on the Hub (127.0.0.1:5432). It returns a *sql.DB connected
// through the tunnel and a cleanup function.
//
// The DATABASE_URL host/port is replaced with the local tunnel endpoint so that
// parameterized queries run without touching psql or constructing SQL strings.
func openDBTunnel(sshClient *ssh.Client, dbURL string) (*sql.DB, func(), error) {
	// Listen on a random local port.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, nil, fmt.Errorf("openDBTunnel listen: %w", err)
	}
	localAddr := ln.Addr().String()

	// Forward accepted connections to Hub's local PostgreSQL.
	go func() {
		for {
			local, err := ln.Accept()
			if err != nil {
				return // listener closed
			}
			go func(local net.Conn) {
				defer local.Close()
				remote, err := sshClient.Dial("tcp", "127.0.0.1:5432")
				if err != nil {
					return
				}
				defer remote.Close()
				done := make(chan struct{}, 2)
				go func() { io.Copy(remote, local); done <- struct{}{} }()
				go func() { io.Copy(local, remote); done <- struct{}{} }()
				<-done
			}(local)
		}
	}()

	// Rewrite DATABASE_URL host to our tunnel endpoint.
	u, err := url.Parse(dbURL)
	if err != nil {
		ln.Close()
		return nil, nil, fmt.Errorf("openDBTunnel parse url: %w", err)
	}
	u.Host = localAddr
	// Disable SSL — connection is over the SSH tunnel.
	q := u.Query()
	q.Set("sslmode", "disable")
	u.RawQuery = q.Encode()

	db, err := sql.Open("postgres", u.String())
	if err != nil {
		ln.Close()
		return nil, nil, fmt.Errorf("openDBTunnel sql.Open: %w", err)
	}
	if err := db.Ping(); err != nil {
		db.Close()
		ln.Close()
		return nil, nil, fmt.Errorf("openDBTunnel ping: %w", err)
	}

	cleanup := func() {
		db.Close()
		ln.Close()
	}
	return db, cleanup, nil
}
