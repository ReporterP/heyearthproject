package main

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"database/sql"
	"encoding/pem"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"math/big"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/heyearth/backend/internal/config"
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
	ansibleRoot        string
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
	streamPPFlag := flag.String("stream-proxy-protocol", "", "on|off: PROXY protocol на 443 stream (реальные IP клиентов для nginx/API); переключает nginx и xray Reality вместе, на Hub отдельным юнитом")
	forceFlag := flag.Bool("force", false, "с --setup-xray: заменить существующий /opt/heyearth/xray/config.json bootstrap-конфигом (на живом Hub отрежет пользователей до синхронизации API)")
	setupHy2Flag := flag.Bool("setup-hysteria2-hub", false, "установить Hysteria2 сервер на главный сервер (Express routing для premium-планов)")
	setupAwgFlag := flag.Bool("setup-awg", false, "установить AmneziaWG (Палма) на главный сервер — userspace VPN с CPS-обфускацией")
	statusMode := flag.Bool("status", false, "проверить состояние сервисов и API на сервере")
	backupMode := flag.Bool("backup", false, "создать полный бэкап (БД + certs + .env) на сервере")
	restoreMode := flag.Bool("restore", false, "восстановить из бэкапа")
	rotateCerts := flag.Bool("rotate-node-agent-certs", false, "перегенерировать листовые mTLS-сертификаты (client, server) без замены CA")
	setupExitNode := flag.String("setup-exit-node", "", "развернуть exit-ноду (IP:порт_туннеля, напр. 1.2.3.4:443)")
	exitNodePassword := flag.String("exit-node-password", "", "SSH пароль для exit-ноды")
	exitNodeSSHPort := flag.Int("exit-node-ssh-port", 22, "SSH порт exit-ноды")
	sshCmd := flag.String("ssh", "", "выполнить SSH команду на сервере и вернуть результат")
	setupOpsFlag := flag.Bool("setup-ops", false, "эксплуатация Hub: чистка ufw, ежедневный бэкап (+копия на exit: --backup-target), fail2ban, journald, nginx stream timeout, защита xray API")
	backupTarget := flag.String("backup-target", "", "IP exit-ноды для копии бэкапа (--setup-ops); пароль: --exit-node-password или EXIT_NODE_SSH_PASSWORD")
	backupAgeRecipient := flag.String("backup-age-recipient", "", "age-публичный ключ (age1…), которым шифруется копия бэкапа на exit (--setup-ops); приватный ключ хранит владелец офлайн: age-keygen -o backup.key")
	flag.Parse()

	var mode string
	switch {
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
	case *setupAwgFlag:
		mode = "setup-awg"
	case *setupOpsFlag:
		mode = "setup-ops"
	case *streamPPFlag != "":
		mode = "stream-proxy-protocol"
	case *sshCmd != "":
		mode = "ssh"
	case *statusMode:
		mode = "status"
	case *backupMode:
		mode = "backup"
	case *restoreMode:
		mode = "restore"
	case flag.NArg() == 1 && flag.Arg(0) == "fix-db":
		mode = "fix-db"
	case flag.NArg() >= 1 && flag.Arg(0) == "fix-db":
		mode = "fix-db"
	default:
		log.Fatal("Укажи один из флагов: --main, --update-main, --restart, --site, --admin-web, --vue-site, --deploy-site, --nginx, --setup-xray, --setup-hysteria2-hub, --setup-awg, --setup-ops, --stream-proxy-protocol on|off, --setup-exit-node, --status, --backup, --restore, --rotate-node-agent-certs. Или: fix-db")
	}

	repoRoot := findRepoRoot()
	serverRoot := filepath.Join(repoRoot, "server")
	backendRoot := filepath.Join(serverRoot, "backend")
	ansibleRoot := filepath.Join(serverRoot, "ansible")
	env := loadEnv(serverRoot, repoRoot)

	cfg := &deployConfig{
		repoRoot:           repoRoot,
		serverRoot:         serverRoot,
		backendRoot:        backendRoot,
		ansibleRoot:        ansibleRoot,
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
	if mode != "status" {
		ensureLocalMTLSAssets(cfg)
	}

	if mode == "rotate-certs" {
		rotateNodeAgentCerts(cfg)
		log.Println("Листовые сертификаты перегенерированы локально. Запусти --update-main для загрузки на сервер.")
		return
	}

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
	if mode == "backup" {
		doBackup(client, cfg)
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
		switchStreamProxyProtocol(client, cfg, *streamPPFlag, config.DefaultRealitySNI)
		return
	}

	if mode == "setup-exit-node" {
		setupExitNodeDeploy(*setupExitNode, *exitNodePassword, *exitNodeSSHPort, client, cfg)
		return
	}

	sftpClient, err := sftp.NewClient(client)
	if err != nil {
		log.Fatalf("sftp: %v", err)
	}
	defer sftpClient.Close()

	switch mode {
	case "main":
		installGoOnServer(client)
		optimizeNetworkStack(client)
		installPostgresOnServer(client)
		installAnsibleOnServer(client)
		uploadNodeProvisioningAssets(sftpClient, client, cfg)
		doAPI(sftpClient, client, cfg)
		doAdminWeb(sftpClient, client, cfg)
		doVueSite(sftpClient, client, cfg)
		doNginx(sftpClient, client, cfg)
		doSSL(client, cfg)
		doNginx(sftpClient, client, cfg) // перезаписать конфиг с учётом SSL
		seedConnectionSettings(client, cfg)
		setupAmneziaWGHub(client, cfg)
		verifyServices(client)
	case "update-main":
		// installAnsibleOnServer пропускаем — apt-get update может зависать
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
	case "setup-awg":
		setupAmneziaWGHub(client, cfg)
		setupAWGExitRoute(client, cfg)
	case "backup":
		doBackup(client, cfg)
	case "restore":
		doRestore(client, sftpClient, cfg)
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

// findFlutterCmd returns true if `fvm` or `flutter` is in PATH.
func findFlutterCmd() bool {
	if _, err := exec.LookPath("fvm"); err == nil {
		return true
	}
	if _, err := exec.LookPath("flutter"); err == nil {
		return true
	}
	return false
}

// flutterExec returns the flutter command parts. Prefers "fvm flutter" if fvm is available.
func flutterExec() (string, []string) {
	if _, err := exec.LookPath("fvm"); err == nil {
		return "fvm", []string{"flutter"}
	}
	return "flutter", nil
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

func sshDial(addr, user, password, expectedFingerprint string) (*ssh.Client, error) {
	return sshDialAlgos(addr, user, password, expectedFingerprint, nil)
}

// sshDialAlgos is sshDial with an optional list of accepted host key algorithms. A pinned
// fingerprint is only meaningful for one key type, so callers that pin an ed25519 fingerprint
// restrict the negotiation to ssh-ed25519 (otherwise the server may present its RSA key).
func sshDialAlgos(addr, user, password, expectedFingerprint string, hostKeyAlgorithms []string) (*ssh.Client, error) {
	var callback ssh.HostKeyCallback
	if strings.TrimSpace(expectedFingerprint) != "" {
		callback = func(hostname string, remoteAddr net.Addr, key ssh.PublicKey) error {
			got := ssh.FingerprintSHA256(key)
			if got != expectedFingerprint {
				return fmt.Errorf("unexpected host key fingerprint for %s: got %s", hostname, got)
			}
			return nil
		}
	} else {
		log.Println("DEPLOY_HOST_KEY_FINGERPRINT not set — accepting any host key (insecure, OK for initial deploy)")
		callback = ssh.InsecureIgnoreHostKey()
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
	_ = c.Remove(remote)
	dst, err := c.Create(remote)
	if err != nil {
		return err
	}
	defer dst.Close()
	_, err = io.Copy(dst, src)
	return err
}

func uploadFileContent(c *sftp.Client, content []byte, remote string) error {
	remote = normRemote(remote)
	if err := sftpMkdirAll(c, remoteDir(remote)); err != nil {
		return err
	}
	_ = c.Remove(remote)
	dst, err := c.Create(remote)
	if err != nil {
		return err
	}
	defer dst.Close()
	_, err = dst.Write(content)
	return err
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

func installGoOnServer(client *ssh.Client) {
	script := fmt.Sprintf(`set -e
if command -v go >/dev/null 2>&1 && go version | grep -q %s; then
  echo "Go already installed: $(go version)"
  exit 0
fi
cd /tmp
wget -q %s -O go.tar.gz 2>/dev/null || curl -fsSL %s -o go.tar.gz
rm -rf %s/go
tar -C %s -xzf go.tar.gz
echo 'export PATH=$PATH:%s/go/bin' >/etc/profile.d/go.sh
chmod 644 /etc/profile.d/go.sh
`, goVersion, goDownloadURL, goDownloadURL, goInstallPath, goInstallPath, goInstallPath)
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

func installAnsibleOnServer(client *ssh.Client) {
	script := `set -e
apt-get update -qq
apt-get install -y -qq software-properties-common
apt-get install -y -qq ansible sshpass
`
	_, _ = runSSH(client, script, true)
}

func ensureLocalMTLSAssets(cfg *deployConfig) {
	certDir := filepath.Join(cfg.backendRoot, "certs")
	required := []string{"ca-cert.pem", "ca-key.pem", "client-cert.pem", "client-key.pem", "server-cert.pem", "server-key.pem"}
	missing := false
	for _, name := range required {
		if _, err := os.Stat(filepath.Join(certDir, name)); err != nil {
			missing = true
			break
		}
	}
	if !missing {
		checkLeafCertExpiry(certDir)
		return
	}

	if err := os.MkdirAll(certDir, 0755); err != nil {
		log.Fatal(err)
	}

	caKey, err := rsa.GenerateKey(rand.Reader, 4096)
	if err != nil {
		log.Fatal(err)
	}
	caTemplate := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "HeYearth Node-Agent CA", Organization: []string{"HeYearth"}},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().AddDate(10, 0, 0),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	if err != nil {
		log.Fatal(err)
	}
	writePEM(filepath.Join(certDir, "ca-cert.pem"), "CERTIFICATE", caDER)
	writePrivateKey(filepath.Join(certDir, "ca-key.pem"), caKey)

	caCert, err := x509.ParseCertificate(caDER)
	if err != nil {
		log.Fatal(err)
	}

	generateSignedCert(certDir, "client", pkix.Name{CommonName: "main-api", Organization: []string{"HeYearth"}}, []string{"main-api"}, nil, x509.ExtKeyUsageClientAuth, caCert, caKey, 2)
	generateSignedCert(certDir, "server", pkix.Name{CommonName: "node-agent", Organization: []string{"HeYearth"}}, []string{"node-agent", "localhost"}, nil, x509.ExtKeyUsageServerAuth, caCert, caKey, 3)
}

const leafCertValidityYears = 1

func generateSignedCert(certDir, prefix string, subject pkix.Name, dnsNames []string, ipAddresses []net.IP, usage x509.ExtKeyUsage, caCert *x509.Certificate, caKey *rsa.PrivateKey, serial int64) {
	key, err := rsa.GenerateKey(rand.Reader, 4096)
	if err != nil {
		log.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(serial),
		Subject:      subject,
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().AddDate(leafCertValidityYears, 0, 0),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{usage},
		DNSNames:     dnsNames,
		IPAddresses:  ipAddresses,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, caCert, &key.PublicKey, caKey)
	if err != nil {
		log.Fatal(err)
	}
	writePEM(filepath.Join(certDir, prefix+"-cert.pem"), "CERTIFICATE", der)
	writePrivateKey(filepath.Join(certDir, prefix+"-key.pem"), key)
}

func checkLeafCertExpiry(certDir string) {
	certPath := filepath.Join(certDir, "client-cert.pem")
	pemData, err := os.ReadFile(certPath)
	if err != nil {
		return
	}
	block, _ := pem.Decode(pemData)
	if block == nil {
		return
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return
	}
	daysLeft := time.Until(cert.NotAfter).Hours() / 24
	if daysLeft < 30 {
		log.Printf("ВНИМАНИЕ: листовой сертификат client-cert.pem истекает через %.0f дней! Запусти --rotate-node-agent-certs", daysLeft)
	}
}

func loadCAFromDisk(certDir string) (*x509.Certificate, *rsa.PrivateKey) {
	caPEM, err := os.ReadFile(filepath.Join(certDir, "ca-cert.pem"))
	if err != nil {
		log.Fatalf("read CA cert: %v", err)
	}
	block, _ := pem.Decode(caPEM)
	if block == nil {
		log.Fatal("ca-cert.pem: no PEM block found")
	}
	caCert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		log.Fatalf("parse CA cert: %v", err)
	}

	keyPEM, err := os.ReadFile(filepath.Join(certDir, "ca-key.pem"))
	if err != nil {
		log.Fatalf("read CA key: %v", err)
	}
	keyBlock, _ := pem.Decode(keyPEM)
	if keyBlock == nil {
		log.Fatal("ca-key.pem: no PEM block found")
	}
	caKey, err := x509.ParsePKCS1PrivateKey(keyBlock.Bytes)
	if err != nil {
		log.Fatalf("parse CA key: %v", err)
	}
	return caCert, caKey
}

func rotateNodeAgentCerts(cfg *deployConfig) {
	certDir := filepath.Join(cfg.backendRoot, "certs")
	caCert, caKey := loadCAFromDisk(certDir)

	log.Println("Ротация: перегенерация листовых сертификатов (client, server) тем же Node-Agent CA…")
	serial := time.Now().Unix()
	generateSignedCert(certDir, "client", pkix.Name{CommonName: "main-api", Organization: []string{"HeYearth"}}, []string{"main-api"}, nil, x509.ExtKeyUsageClientAuth, caCert, caKey, serial)
	generateSignedCert(certDir, "server", pkix.Name{CommonName: "node-agent", Organization: []string{"HeYearth"}}, []string{"node-agent", "localhost"}, nil, x509.ExtKeyUsageServerAuth, caCert, caKey, serial+1)
	log.Println("Новые client-cert.pem, client-key.pem, server-cert.pem, server-key.pem сгенерированы.")
}

func writePEM(path, blockType string, der []byte) {
	f, err := os.Create(path)
	if err != nil {
		log.Fatal(err)
	}
	defer f.Close()
	if err := pem.Encode(f, &pem.Block{Type: blockType, Bytes: der}); err != nil {
		log.Fatal(err)
	}
}

func writePrivateKey(path string, key *rsa.PrivateKey) {
	writePEM(path, "RSA PRIVATE KEY", x509.MarshalPKCS1PrivateKey(key))
}

func uploadNodeProvisioningAssets(c *sftp.Client, client *ssh.Client, cfg *deployConfig) {
	_, _ = runSSH(client, "mkdir -p "+appDir+"/artifacts "+appDir+"/ansible "+appDir+"/certs", true)

	nodeAgentPath := buildGoBinary(cfg.backendRoot, "node-agent", "node-agent")
	if err := uploadFile(c, nodeAgentPath, appDir+"/artifacts/node-agent"); err != nil {
		log.Fatal(err)
	}

	for _, name := range []string{"ca-cert.pem", "server-cert.pem", "server-key.pem", "client-cert.pem", "client-key.pem"} {
		local := filepath.Join(cfg.backendRoot, "certs", name)
		if err := uploadFile(c, local, path.Join(appDir, "certs", name)); err != nil {
			log.Fatalf("upload cert %s: %v", name, err)
		}
	}

	if err := uploadDir(c, cfg.ansibleRoot, appDir+"/ansible"); err != nil {
		log.Fatalf("upload ansible: %v", err)
	}
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
	if err := uploadFileContent(c, []byte(serializeEnv(apiEnv)), appDir+"/api.env"); err != nil {
		log.Fatal(err)
	}
	// api.env carries the DB password, the JWT secret and the admin password.
	if _, err := runSSH(client, "chmod 600 "+appDir+"/api.env", true); err != nil {
		log.Fatal(err)
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

func doAdminWeb(c *sftp.Client, client *ssh.Client, cfg *deployConfig) {
	_, _ = runSSH(client, "rm -rf /var/www/heyearth && mkdir -p /var/www/heyearth", true)
	adminBuildDir := buildAdminWeb(cfg.repoRoot, cfg)
	if err := uploadDir(c, adminBuildDir, "/var/www/heyearth"); err != nil {
		log.Fatalf("upload admin_web build: %v", err)
	}
	_, _ = runSSH(client, "chmod -R 755 /var/www/heyearth", true)
	fmt.Println("[admin-web] uploaded to /var/www/heyearth")
}

func doVueSite(c *sftp.Client, client *ssh.Client, cfg *deployConfig) {
	_, _ = runSSH(client, "rm -rf /var/www/heyearth-site && mkdir -p /var/www/heyearth-site", true)
	vueBuildDir := buildSite(cfg.repoRoot, cfg)
	if err := uploadDir(c, vueBuildDir, "/var/www/heyearth-site"); err != nil {
		log.Fatalf("upload vue site build: %v", err)
	}
	_, _ = runSSH(client, "chmod -R 755 /var/www/heyearth-site", true)
	fmt.Println("[vue-site] uploaded to /var/www/heyearth-site")
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
		script := fmt.Sprintf("cd %s && rm -rf node_modules package-lock.json && unset VITE_API_BASE && /usr/bin/npm install && /usr/bin/npm run build",
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

	install := exec.Command(npmPath, "install")
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
	_, _ = runSSH(client, "mkdir -p "+appDir+"/releases", true)

	domain := cfg.siteDomain
	certPath := "/etc/letsencrypt/live/" + domain

	// Проверяем наличие SSL-сертификата
	hasCert, _ := runSSH(client, fmt.Sprintf("test -f %s/fullchain.pem && echo yes || echo no", certPath), false)
	ssl := strings.TrimSpace(hasCert) == "yes"

	// Проверяем наличие Xray — если установлен, используем stream для SNI routing на 443
	xrayActive := isXrayInstalled(client)

	var nginxConf string
	if ssl {
		nginxConf = buildNginxConfSSL(domain, appDir, certPath, xrayActive)
	} else {
		nginxConf = buildNginxConfHTTP(domain, appDir)
	}

	if err := uploadFileContent(c, []byte(nginxConf), "/etc/nginx/conf.d/heyearth.conf"); err != nil {
		log.Fatal(err)
	}

	// Если Xray активен — настраиваем nginx stream для SNI routing на 443
	if xrayActive && ssl {
		// The stream must match what xray's Reality inbound expects right now: with
		// acceptProxyProtocol a stream without PROXY would break every Reality handshake.
		ppOut, _ := runSSH(client, streamProxyLiveCheck, false)
		proxyProtocol := strings.TrimSpace(ppOut) == "on"
		fmt.Printf("nginx stream: PROXY protocol %s (по живому конфигу xray)\n", map[bool]string{true: "ВКЛ", false: "выкл"}[proxyProtocol])
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

	_, _ = runSSH(client, "rm -f /etc/nginx/sites-enabled/default && nginx -t && systemctl enable nginx && systemctl restart nginx", true)
}

func buildNginxConfHTTP(domain, appDir string) string {
	return fmt.Sprintf(`# ── Main site + API (%[1]s) ──────────────────────────────────────
server {
    listen 80;
    listen [::]:80;
    server_name %[1]s www.%[1]s;

    location /heyearth.v1.ConfigApi/ { grpc_pass 127.0.0.1:50051; grpc_set_header Host $host; }
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

    location /releases/ { alias %[2]s/releases/; add_header Cache-Control "public, max-age=3600"; try_files $uri =404; }

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

func buildNginxConfSSL(domain, appDir, certPath string, xrayActive bool) string {
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
    listen %[4]s ssl http2;
    listen [::]:%[4]s ssl http2;
%[5]s    server_name %[1]s www.%[1]s;

    ssl_certificate %[3]s/fullchain.pem;
    ssl_certificate_key %[3]s/privkey.pem;
    include /etc/letsencrypt/options-ssl-nginx.conf;
    ssl_dhparam /etc/letsencrypt/ssl-dhparams.pem;

    location /heyearth.v1.ConfigApi/ { grpc_pass 127.0.0.1:50051; grpc_set_header Host $host; }
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

    location /releases/ { alias %[2]s/releases/; add_header Cache-Control "public, max-age=3600"; try_files $uri =404; }

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
    listen %[4]s ssl http2;
    listen [::]:%[4]s ssl http2;
%[5]s    server_name admin.%[1]s;

    ssl_certificate %[3]s/fullchain.pem;
    ssl_certificate_key %[3]s/privkey.pem;
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
    listen %[4]s ssl http2;
    listen [::]:%[4]s ssl http2;
%[5]s    server_name site.%[1]s;

    ssl_certificate %[3]s/fullchain.pem;
    ssl_certificate_key %[3]s/privkey.pem;
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
`, domain, appDir, certPath, httpsPort, ppListen)
}

// buildNginxStreamConf создаёт конфиг nginx stream для SNI routing на порту 443:
// наши домены → nginx HTTPS (8443), остальное → Xray VLESS Reality (10443).
// proxyProtocol (режим --stream-proxy-protocol on): stream шлёт PROXY-заголовок с
// реальным адресом клиента, домены идут на PROXY-listener 127.0.0.1:8446, а inbound
// Reality обязан принимать PROXY (acceptProxyProtocol, app_settings.stream_proxy_protocol).
func buildNginxStreamConf(domain string, proxyProtocol bool) string {
	httpsUpstream, ppLine := "127.0.0.1:8443", ""
	if proxyProtocol {
		httpsUpstream, ppLine = "127.0.0.1:"+nginxProxyProtocolPort, "    "+streamProxyProtocolLine+"\n"
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
    server 127.0.0.1:10443;
}

server {
    listen 443 so_keepalive=60s:10s:3;
    listen [::]:443 so_keepalive=60s:10s:3;
    ssl_preread on;
%[3]s    # = xray connIdle 3600s; default 10m cut idle long-lived sessions behind the SNI router.
    proxy_timeout 3600s;
    proxy_pass $backend;
}
`, domain, httpsUpstream, ppLine)
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
  echo "[xray] Bootstrap-конфиг записан (VLESS:10443, SNI ` + config.DefaultRealitySNI + `; остальное генерирует heyearth-api)"
fi`

// isXrayInstalled проверяет наличие Xray на сервере
func isXrayInstalled(client *ssh.Client) bool {
	// Проверяем наличие бинарника Xray И наличие systemd-юнита.
	// Не проверяем is-active — Xray может быть в состоянии "activating" при старте,
	// но stream-конфиг nginx всё равно нужен для SNI routing.
	out, _ := runSSH(client, "test -f /opt/heyearth/xray/xray && systemctl is-enabled xray 2>/dev/null || echo no", false)
	return strings.TrimSpace(out) == "enabled"
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

# Вставляем основные настройки (всегда перезаписываем)
runuser -u postgres -- psql -d heyearth -c "
INSERT INTO app_settings (key, value, updated_at) VALUES
  ('main_server_host', '$MAIN_HOST', CURRENT_TIMESTAMP),
  ('vless_port', '443', CURRENT_TIMESTAMP)
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

	// Express (Hysteria2) — DB-driven runtime view: tunnel mode, bound exit,
	// config_version, obfs rotation age, active client count.
	fmt.Println("=== Express (Hysteria2) ===")
	expressOut, _ := runSSH(client, `# server status + UDP listener
SRV=$(systemctl is-active hysteria2-server 2>/dev/null || echo inactive)
LISTEN=$(ss -ulnp 2>/dev/null | grep ':50443' | head -1 | awk '{print $5}')
echo "  server: $SRV${LISTEN:+, UDP $LISTEN}"

# Tunnel mode + healthy exits
MODE=$(su - postgres -c "psql -d heyearth -tA -c \"SELECT COALESCE(value,'unknown') FROM app_settings WHERE key='hysteria2_tunnel_mode';\"" 2>/dev/null | head -1 | tr -d ' ')
N=$(su - postgres -c "psql -d heyearth -tA -c \"SELECT COUNT(*) FROM servers WHERE COALESCE(is_exit_node,FALSE)=TRUE AND COALESCE(lifecycle_state,'')='online' AND COALESCE(tunnel_status,'')='healthy';\"" 2>/dev/null | head -1 | tr -d ' ')
echo "  tunnel mode: ${MODE:-unknown} (${N:-0} healthy exits)"

# Best-RTT bound exit
BEST=$(su - postgres -c "psql -d heyearth -tA -c \"SELECT COALESCE(country_code,'??') || ' ' || ip_address || ' (RTT ' || COALESCE(last_rtt_ms::text,'?') || 'ms)' FROM servers WHERE COALESCE(is_exit_node,FALSE)=TRUE AND COALESCE(lifecycle_state,'')='online' AND COALESCE(tunnel_status,'')='healthy' ORDER BY COALESCE(last_rtt_ms,9999) ASC LIMIT 1;\"" 2>/dev/null | head -1)
echo "  bound exit: ${BEST:-none}"

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

func serializeEnv(values map[string]string) string {
	keys := []string{
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
	}
	var b strings.Builder
	for _, key := range keys {
		if value, ok := values[key]; ok && strings.TrimSpace(value) != "" {
			b.WriteString(key)
			b.WriteString("=")
			b.WriteString(value)
			b.WriteString("\n")
		}
	}
	return b.String()
}

func doBackup(client *ssh.Client, cfg *deployConfig) {
	timestamp := time.Now().Format("20060102-150405")
	backupDir := fmt.Sprintf("/opt/heyearth/backups/%s", timestamp)

	fmt.Printf("=== Creating backup at %s ===\n", backupDir)

	cmds := []string{
		fmt.Sprintf("mkdir -p %s", backupDir),
		fmt.Sprintf("sudo -u postgres pg_dump heyearth > %s/db.sql 2>/dev/null || pg_dump -h 127.0.0.1 -U heyearth heyearth > %s/db.sql", backupDir, backupDir),
		fmt.Sprintf("cp -r %s/certs %s/certs 2>/dev/null || true", appDir, backupDir),
		fmt.Sprintf("cp %s/.env %s/dot-env 2>/dev/null || true", appDir, backupDir),
		fmt.Sprintf("cp -r /var/www/heyearth %s/admin-web 2>/dev/null || true", backupDir),
		fmt.Sprintf("cp -r /var/www/heyearth-site %s/site 2>/dev/null || true", backupDir),
		fmt.Sprintf("cp /etc/nginx/conf.d/heyearth.conf %s/nginx.conf 2>/dev/null || true", backupDir),
		fmt.Sprintf("tar czf /opt/heyearth/backups/backup-%s.tar.gz -C /opt/heyearth/backups %s", timestamp, timestamp),
		fmt.Sprintf("rm -rf %s", backupDir),
		"ls -lah /opt/heyearth/backups/",
	}

	for _, cmd := range cmds {
		out, err := runSSH(client, cmd, false)
		if err != nil {
			log.Printf("  cmd failed: %s → %v", cmd, err)
		}
		if strings.TrimSpace(out) != "" {
			fmt.Println(out)
		}
	}

	fmt.Printf("=== Backup complete: /opt/heyearth/backups/backup-%s.tar.gz ===\n", timestamp)
}

func doRestore(client *ssh.Client, c *sftp.Client, cfg *deployConfig) {
	fmt.Println("=== Available backups ===")
	out, _ := runSSH(client, "ls -lt /opt/heyearth/backups/*.tar.gz 2>/dev/null | head -10", false)
	fmt.Println(out)

	fmt.Println("To restore, run on the server:")
	fmt.Println("  cd /opt/heyearth/backups")
	fmt.Println("  tar xzf backup-XXXXXXXX-XXXXXX.tar.gz")
	fmt.Println("  sudo -u postgres psql heyearth < XXXXXXXX-XXXXXX/db.sql")
	fmt.Println("  cp XXXXXXXX-XXXXXX/dot-env /opt/heyearth/mainserver/.env")
	fmt.Println("  cp -r XXXXXXXX-XXXXXX/certs /opt/heyearth/mainserver/certs")
	fmt.Println("  systemctl restart heyearth-api")
	fmt.Println("")
	fmt.Println("For migration to a new server:")
	fmt.Println("  1. Run --backup on the old server")
	fmt.Println("  2. scp /opt/heyearth/backups/backup-*.tar.gz newhost:/opt/heyearth/backups/")
	fmt.Println("  3. On the new server: run deploy --main first")
	fmt.Println("  4. Then restore the DB and config from the backup")
}

// exitNodeSetupScript renders the root provisioning script for a new exit node: Xray
// (SS+WS :443, VLESS Reality :10443) + Hysteria2 server (:50443). The hysteria server.yaml
// ends with config.ExitHysteriaACL and the xray routing starts with exitXrayGuardRules, so no
// tunnel egress reaches the exit's loopback, private neighbours or its own public IP — except
// mtprotoPort on the own address (the MTProxy full-tunnel clients reach through the tunnel).
// Pure, so tests can render it without SSH.
func exitNodeSetupScript(exitIP string, mtprotoPort int) string {
	return fmt.Sprintf(`set -e
mkdir -p /opt/heyearth/xray /opt/heyearth/state

# ── 1. Скачать Xray если нет ──
if [ ! -f /opt/heyearth/xray/xray ]; then
  apt-get update -qq
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

# ── 2. Сгенерировать/обновить ключи ──
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
  echo "[xray] Ключи сгенерированы"
else
  echo "[xray] Ключи уже существуют, проверяю полноту..."
  # Дополняем недостающие поля (uuid, ssPassword) если их нет
  python3 -c "
import json, subprocess, os
with open('$KEYS_FILE') as f:
    k = json.load(f)
changed = False
if 'uuid' not in k or not k['uuid']:
    k['uuid'] = open('/proc/sys/kernel/random/uuid').read().strip()
    changed = True
if 'ssPassword' not in k or not k['ssPassword']:
    import base64; k['ssPassword'] = base64.b64encode(os.urandom(16)).decode()
    changed = True
if changed:
    with open('$KEYS_FILE', 'w') as f:
        json.dump(k, f, indent=2)
    print('[xray] Ключи дополнены (uuid/ssPassword)')
else:
    print('[xray] Все поля присутствуют')
"
fi

# Читаем ключи
PRIV_KEY=$(python3 -c "import json; print(json.load(open('$KEYS_FILE'))['privateKey'])")
PUB_KEY=$(python3 -c "import json; print(json.load(open('$KEYS_FILE'))['publicKey'])")
SHORT_ID=$(python3 -c "import json; print(json.load(open('$KEYS_FILE'))['shortId'])")
UUID=$(python3 -c "import json; print(json.load(open('$KEYS_FILE'))['uuid'])")
SS_PASSWORD=$(python3 -c "import json; print(json.load(open('$KEYS_FILE')).get('ssPassword', ''))")

# ── 3. Остановить конфликтующие сервисы ──
systemctl stop ss-server 2>/dev/null || true
systemctl disable ss-server 2>/dev/null || true
echo "[xray] Конфликтующие сервисы остановлены"

# ── 4. Xray конфиг: SS+WebSocket (TCP:443) + VLESS Reality (10443) ──
cat > /opt/heyearth/xray/config.json <<XRAYEOF
{
  "log": {"loglevel": "warning"},
  "dns": {
    "servers": ["8.8.8.8", "1.1.1.1"],
    "queryStrategy": "UseIPv4",
    "disableCache": false
  },
  "inbounds": [
    {
      "tag": "shadowsocks-ws",
      "listen": "0.0.0.0",
      "port": 443,
      "protocol": "shadowsocks",
      "settings": {
        "method": "2022-blake3-aes-128-gcm",
        "password": "$SS_PASSWORD",
        "network": "tcp,udp"
      },
      "streamSettings": {
        "network": "ws",
        "wsSettings": {"path": "/stream"},
        "sockopt": {"tcpFastOpen": true, "tcpNoDelay": true, "tcpKeepAliveInterval": 30}
      },
      "sniffing": {"enabled": true, "destOverride": ["http", "tls", "quic"]}
    },
    {
      "tag": "vless-reality",
      "listen": "0.0.0.0",
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
          "show": false, "dest": "microsoft.com:443", "xver": 0,
          "serverNames": ["microsoft.com", "www.microsoft.com"],
          "privateKey": "$PRIV_KEY",
          "shortIds": ["$SHORT_ID"]
        }
      },
      "sniffing": {"enabled": true, "destOverride": ["http", "tls", "quic"]}
    }
  ],
  "outbounds": [
    {
      "protocol": "freedom", "tag": "direct",
      "settings": {"domainStrategy": "UseIPv4"},
      "streamSettings": {"sockopt": {"tcpFastOpen": true, "tcpNoDelay": true, "tcpKeepAliveInterval": 30}}
    },
    {"protocol": "blackhole", "tag": "block", "settings": {"response": {"type": "none"}}}
  ],
  "routing": {
    "domainStrategy": "IPIfNonMatch",
    "rules": [
      %s,
      {"type": "field", "protocol": ["bittorrent"], "outboundTag": "block"}
    ]
  },
  "policy": {"levels": {"0": {"handshake": 4, "connIdle": 300, "uplinkOnly": 2, "downlinkOnly": 5, "bufferSize": 512}}}
}
XRAYEOF
echo "[xray] Exit-node конфиг записан (SS+WS:443, VLESS:10443)"

# ── 4b. Xray systemd ──
cat > /etc/systemd/system/xray.service <<'SVCEOF'
[Unit]
Description=Xray Exit Node (HeYearth)
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
systemctl restart xray
sleep 1
echo "[xray] Сервис запущен"
systemctl status xray --no-pager -l 2>&1 | head -10 || true

# ── 5. Hysteria2 сервер (QUIC/UDP — нет head-of-line blocking) ──
echo "[hy2] Установка/обновление Hysteria2..."
%s
# Самоподписанный TLS-сертификат для Hysteria2
mkdir -p /opt/heyearth/certs
if [ ! -f /opt/heyearth/certs/hy2.crt ]; then
  openssl req -x509 -newkey ec -pkeyopt ec_paramgen_curve:prime256v1 \
    -days 3650 -nodes \
    -keyout /opt/heyearth/certs/hy2.key \
    -out /opt/heyearth/certs/hy2.crt \
    -subj "/CN=www.microsoft.com" 2>/dev/null
  echo "[hy2] TLS-сертификат сгенерирован"
else
  echo "[hy2] TLS-сертификат уже существует"
fi

mkdir -p /opt/heyearth/hysteria
cat > /opt/heyearth/hysteria/server.yaml <<'HYEOF'
listen: :50443

tls:
  cert: /opt/heyearth/certs/hy2.crt
  key: /opt/heyearth/certs/hy2.key

auth:
  type: password
  password: PLACEHOLDER_SS_PASSWORD

masquerade:
  type: proxy
  proxy:
    url: https://www.microsoft.com
    rewriteHost: true

quic:
  initStreamReceiveWindow: 8388608
  maxStreamReceiveWindow: 8388608
  initConnReceiveWindow: 20971520
  maxConnReceiveWindow: 20971520

bandwidth:
  up: 1 gbps
  down: 1 gbps

%s
HYEOF
# Подставляем пароль
sed -i "s|PLACEHOLDER_SS_PASSWORD|$SS_PASSWORD|g" /opt/heyearth/hysteria/server.yaml

cat > /etc/systemd/system/hysteria2.service <<'SVCEOF'
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
SVCEOF

systemctl daemon-reload
systemctl enable hysteria2
systemctl restart hysteria2
sleep 2
echo "[hy2] Hysteria2 сервис запущен"
systemctl status hysteria2 --no-pager | head -5 || true

# ── 5b. Port hopping: UDP %s → :50443 (клиенты Hub прыгают по этому диапазону) ──
%s
echo "[hy2] port hopping UDP %s → :50443 (nftables-hysteria)"

# ── 6. Firewall: все протоколы ──
if command -v ufw >/dev/null 2>&1; then
  ufw allow 22/tcp 2>/dev/null || true
  ufw allow 443/tcp 2>/dev/null || true
  ufw allow 443/udp 2>/dev/null || true
  ufw allow 10443/tcp 2>/dev/null || true
  ufw allow 50443/udp 2>/dev/null || true
  ufw allow %s/udp 2>/dev/null || true
  ufw --force enable 2>/dev/null || true
  echo "[fw] UFW правила добавлены"
else
  iptables -I INPUT -p tcp --dport 443 -j ACCEPT 2>/dev/null || true
  iptables -I INPUT -p udp --dport 443 -j ACCEPT 2>/dev/null || true
  iptables -I INPUT -p tcp --dport 10443 -j ACCEPT 2>/dev/null || true
  iptables -I INPUT -p udp --dport 50443 -j ACCEPT 2>/dev/null || true
  iptables -I INPUT -p udp --dport %s -j ACCEPT 2>/dev/null || true
  echo "[fw] iptables правила добавлены"
fi

# ── 7. Итоги ──
echo ""
echo "===== EXIT NODE CREDENTIALS ====="
echo "IP: %s"
echo "Tunnel Port: 50443 (Hysteria2 UDP)"
echo "Fallback SS+WS: 443 (TCP)"
echo "Fallback VLESS: 10443 (TCP)"
echo "Protocol: hysteria2"
echo "UUID: $UUID"
echo "Public Key: $PUB_KEY"
echo "Short ID: $SHORT_ID"
echo "SS Password: (не печатается; /opt/heyearth/state/reality-keys.json)"
echo "SNI: microsoft.com"
echo "================================="
`, exitXrayGuardRules(exitIP, mtprotoPort), config.HysteriaInstallScript(), strings.TrimSuffix(config.ExitHysteriaACL(exitIP, mtprotoPort), "\n"),
		config.ExitHopRange, strings.TrimSuffix(config.ExitPortHopScript, "\n"), config.ExitHopRange,
		strings.ReplaceAll(config.ExitHopRange, "-", ":"), strings.ReplaceAll(config.ExitHopRange, "-", ":"), exitIP)
}

// exitNodeSetupError says why exitNodeSetupScript did not complete: the script exited
// non-zero (runErr; it runs under set -e) and/or its stdout lacks the hysteria success line
// (config.HysteriaInstallError). nil only when both are clean.
func exitNodeSetupError(out string, runErr error) error {
	return errors.Join(runErr, config.HysteriaInstallError(out))
}

// hubHy2ClientScript renders the Hub-side script that runs a Hysteria2 client to the exit
// node as a local SOCKS5 on 127.0.0.1:1088+serverID, from the same templates heyearth-api uses
// (config.HubHysteriaClientYAML: port hopping across the exit's range, hopInterval, client-side
// ACL; config.HubHysteriaClientUnit). Files go through put_file (base64 payload: the tunnel
// password never touches the shell; client-<id>.yaml 0600) and the client is restarted only
// when a file changed or it is not running, so a re-run does not interrupt the tunnel. It
// installs the hysteria binary only if the Hub has none (config.HysteriaEnsureScript): an
// upgrade here would hit every Hub client on its next restart while other exits may still run
// an older server. Prints HUB_CLIENT_CHANGED=0|1. Pure, so tests can render it without SSH.
func hubHy2ClientScript(exitIP string, serverID int, auth string) string {
	id := strconv.Itoa(serverID)
	unit := "hysteria2-client-" + id
	port := strconv.Itoa(1088 + serverID)
	return `set -e
mkdir -p ` + config.HubHysteriaDir + `

# Hysteria2 binary: только если на Hub его нет (обновление Hub — deploy --setup-hysteria2-hub)
` + config.HysteriaEnsureScript() + opsPutFileFunc + `# Клиент к exit-ноде ` + exitIP + ` (SOCKS 127.0.0.1:` + port + `): общий шаблон с API (port hopping, ACL)
umask 077
` + putFileCalls([]opsFile{{config.HubHysteriaDir + "/client-" + id + ".yaml", "600", config.HubHysteriaClientYAML(serverID, exitIP, auth, 1088+serverID)}}) + `umask 022
` + putFileCalls([]opsFile{{"/etc/systemd/system/" + unit + ".service", "644", config.HubHysteriaClientUnit(serverID, exitIP)}}) + `systemctl daemon-reload
systemctl enable ` + unit + ` >/dev/null 2>&1
if [ "$PUT_CHANGED" = 1 ] || ! systemctl is-active --quiet ` + unit + `; then
  systemctl restart ` + unit + `
  sleep 2
  echo "[hy2] ` + unit + ` (пере)запущен"
else
  echo "[hy2] ` + unit + `: конфиг не изменился и клиент активен — без перезапуска"
fi
systemctl status ` + unit + ` --no-pager | head -5 || true
if ! systemctl is-active --quiet ` + unit + `; then
  echo "[hy2] ERROR: ` + unit + ` не активен (см. journalctl -u ` + unit + `)" >&2
  exit 1
fi
echo "Hysteria2 client for ` + exitIP + `: port ` + port + `"
echo "HUB_CLIENT_CHANGED=$PUT_CHANGED"
`
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

// dialExitNode connects to an exit by root password only with its ed25519 host key pinned
// (exitAccessFor: EXIT_NODE_HOST_KEY_FINGERPRINT and the exit's own password — never the Hub's).
func dialExitNode(addr string, access exitAccess) (*ssh.Client, error) {
	return sshDialAlgos(addr, "root", access.password, access.fingerprint, []string{ssh.KeyAlgoED25519})
}

// setupExitNodeDeploy разворачивает exit-ноду: устанавливает Xray, настраивает конфиг.
// Формат addr: "IP" или "IP:TUNNEL_PORT" (порт игнорируется).
func setupExitNodeDeploy(addr, password string, sshPort int, mainClient *ssh.Client, cfg *deployConfig) {
	exitIP, err := parseExitNodeAddr(addr)
	if err != nil {
		log.Fatalf("[exit-node] %v", err)
	}
	// Пароль — только самой exit-ноды (--exit-node-password / EXIT_NODE_SSH_PASSWORD), host key —
	// по EXIT_NODE_HOST_KEY_FINGERPRINT. Пароль Hub на exit не пробуется.
	access, err := exitAccessFor(cfg.env, password)
	if err != nil {
		log.Fatalf("[exit-node] %v\n[exit-node] EXIT_NODE_HOST_KEY_FINGERPRINT в server/.env должен быть отпечатком ed25519 host key именно этой exit-ноды (%s).", err, exitIP)
	}

	fmt.Printf("[exit-node] Подключение к %s:%d (host key закреплён)...\n", exitIP, sshPort)
	exitClient, err := dialExitNode(net.JoinHostPort(exitIP, fmt.Sprintf("%d", sshPort)), access)
	if err != nil {
		log.Fatalf("[exit-node] SSH: %v", err)
	}
	defer exitClient.Close()

	// Оптимизация сетевого стека (BBR, IPv6)
	fmt.Println("[exit-node] Оптимизация сетевого стека...")
	optimizeNetworkStack(exitClient)

	// Установка Xray и настройка как exit-node. MTProxy на собственном адресе exit должен
	// оставаться доступным через туннели (ACL hysteria и routing xray его пропускают).
	mtprotoPort := hubMTProtoPort(mainClient)
	fmt.Printf("[exit-node] Установка Xray... (MTProxy порт %d разрешён на собственном адресе)\n", mtprotoPort)
	script := exitNodeSetupScript(exitIP, mtprotoPort)

	// check=true: the script runs under set -e, so a non-zero exit means provisioning stopped
	// part-way (its stderr, e.g. "[hysteria] ERROR ...", is printed by runSSH). Either that or
	// a missing hysteria success line aborts before the exit is written to the DB and wired
	// into the Hub.
	out, runErr := runSSH(exitClient, script, true)
	fmt.Println(out)
	if err := exitNodeSetupError(out, runErr); err != nil {
		log.Fatalf("[exit-node] ОШИБКА: развёртывание %s не завершено: %v\n"+
			"[exit-node] Прерываю до записи в БД и настройки клиента на Hub. Исправьте причину и повторите --setup-exit-node (скрипт идемпотентен).", exitIP, err)
	}
	fmt.Println("[exit-node] Развёртывание завершено")

	// ── Автоматическое обновление credentials в БД основного сервера ──
	fmt.Println("[exit-node] Читаю credentials с exit-ноды...")
	credsScript := `python3 -c "
import json
k = json.load(open('/opt/heyearth/state/reality-keys.json'))
print(k['uuid'])
print(k['publicKey'])
print(k['shortId'])
print(k.get('ssPassword', ''))
"`
	credsOut, err := runSSH(exitClient, credsScript, true)
	if err != nil {
		fmt.Printf("[exit-node] Не удалось прочитать credentials: %v\n", err)
		fmt.Println("[exit-node] Добавьте данные в админку вручную")
		return
	}

	lines := strings.Split(strings.TrimSpace(credsOut), "\n")
	if len(lines) < 3 {
		fmt.Println("[exit-node] Неожиданный формат credentials, обновите вручную")
		return
	}
	tunnelUUID := strings.TrimSpace(lines[0])
	tunnelPubKey := strings.TrimSpace(lines[1])
	tunnelShortID := strings.TrimSpace(lines[2])
	tunnelSSPass := ""
	if len(lines) > 3 {
		tunnelSSPass = strings.TrimSpace(lines[3])
	}

	// DATABASE_URL lives in the Hub's api.env (db_password.go); server/.env is the fallback.
	dbURL, err := readHubDatabaseURL(mainClient)
	if err != nil || dbURL == "" {
		dbURL = cfg.env.get("DATABASE_URL", "")
	}
	if dbURL == "" {
		fmt.Println("[exit-node] ОШИБКА: DATABASE_URL не найден ни в api.env на Hub, ни в server/.env — нужен для записи exit-ноды в БД")
		return
	}

	fmt.Println("[exit-node] Обновляю запись в БД основного сервера...")
	want := exitTunnelCreds{UUID: tunnelUUID, PublicKey: tunnelPubKey, ShortID: tunnelShortID, SSPassword: tunnelSSPass,
		Protocol: "hysteria2", Port: 50443, SNI: "microsoft.com"}
	action := exitDBUpdate // unknown state: behave as before (write and resync)
	dbConn, closeDB, err := openDBTunnel(mainClient, dbURL)
	if err != nil {
		fmt.Printf("[exit-node] Ошибка подключения к БД через SSH-туннель: %v\n", err)
		fmt.Println("[exit-node] Добавьте данные в админку вручную")
	} else {
		defer closeDB()
		var cur exitTunnelCreds
		rerr := dbConn.QueryRow(`SELECT COALESCE(tunnel_uuid, ''), COALESCE(tunnel_public_key, ''), COALESCE(tunnel_short_id, ''),
			       COALESCE(tunnel_ss_password, ''), COALESCE(tunnel_protocol, ''), COALESCE(tunnel_port, 0), COALESCE(tunnel_sni, '')
			  FROM servers WHERE ip_address = $1 LIMIT 1`, exitIP).
			Scan(&cur.UUID, &cur.PublicKey, &cur.ShortID, &cur.SSPassword, &cur.Protocol, &cur.Port, &cur.SNI)
		if rerr != nil && !errors.Is(rerr, sql.ErrNoRows) {
			fmt.Printf("[exit-node] Ошибка чтения записи exit-ноды: %v\n", rerr)
		} else {
			action = decideExitDBAction(cur, rerr == nil, want)
		}
		switch action {
		case exitDBKeep:
			fmt.Println("[exit-node] credentials туннеля в БД не изменились — tunnel_status не сбрасываю, heyearth-api не перезапускаю")
		case exitDBUpdate:
			// The tunnel changed: the health monitor re-checks it (tunnel_status unknown) and xray resyncs.
			if _, err := dbConn.Exec(`
				UPDATE servers SET
				  tunnel_uuid = $1, tunnel_public_key = $2, tunnel_short_id = $3,
				  tunnel_ss_password = $4, tunnel_sni = 'microsoft.com',
				  tunnel_protocol = 'hysteria2', tunnel_port = 50443,
				  tunnel_status = 'unknown', status = COALESCE(NULLIF(status, ''), 'active')
				WHERE ip_address = $5`,
				tunnelUUID, tunnelPubKey, tunnelShortID, tunnelSSPass, exitIP); err != nil {
				fmt.Printf("[exit-node] Ошибка UPDATE: %v\n", err)
			} else {
				fmt.Println("[exit-node] БД обновлена (credentials туннеля изменились)")
			}
		case exitDBInsert:
			if _, err := dbConn.Exec(`
				INSERT INTO servers (name, ip_address, country_code, is_exit_node,
				                     tunnel_protocol, tunnel_port, tunnel_uuid, tunnel_public_key,
				                     tunnel_short_id, tunnel_ss_password, tunnel_sni, tunnel_status, status)
				SELECT 'Exit Node', $1, 'NL', true, 'hysteria2', 50443, $2, $3, $4, $5, 'microsoft.com', 'unknown', 'active'
				WHERE NOT EXISTS (SELECT 1 FROM servers WHERE ip_address = $1)`,
				exitIP, tunnelUUID, tunnelPubKey, tunnelShortID, tunnelSSPass); err != nil {
				fmt.Printf("[exit-node] Ошибка INSERT: %v\n", err)
			} else {
				fmt.Println("[exit-node] БД обновлена (новая exit-нода)")
			}
		}
	}

	// Проверяем статус Xray + Hysteria2 на exit-ноде
	fmt.Println("[exit-node] Проверяю сервисы на exit-ноде...")
	xrayCheck, _ := runSSH(exitClient, "systemctl is-active xray; systemctl is-active hysteria2; echo '---'; ss -tulnp | grep -E '443|10443|50443'", true)
	fmt.Println(xrayCheck)

	// ── Автонастройка Hysteria2 клиента на Hub ──
	fmt.Println("[exit-node] Настраиваю Hysteria2 клиент на Hub...")

	// ID сервера из БД задаёт порт клиента (1088+id) и имя юнита. Не угадываем: чужой id
	// перезаписал бы клиент другой exit-ноды.
	serverID := 0
	if dbConn != nil {
		var id int
		var status string
		row := dbConn.QueryRow(`SELECT id, COALESCE(status, '') FROM servers WHERE ip_address = $1 LIMIT 1`, exitIP)
		if row.Scan(&id, &status) == nil {
			serverID = id
			if status != "active" && status != "online" {
				fmt.Printf("[exit-node] статус exit-ноды в БД оставлен как есть: %q (повторный запуск не включает выключенную ноду; включить — в админке)\n", status)
			}
		}
	}
	if serverID <= 0 {
		log.Fatalf("[exit-node] ОШИБКА: не удалось узнать id exit-ноды %s в БД — клиент Hysteria2 на Hub не настраиваю (порт 1088+id). "+
			"Проверьте доступ к БД и запись в servers, затем повторите --setup-exit-node.", exitIP)
	}

	// Hysteria2 клиент на Hub: подключается к exit-ноде, даёт локальный SOCKS5
	hy2ClientScript := hubHy2ClientScript(exitIP, serverID, tunnelSSPass)

	// check=true (the script runs under set -e): a failed client setup must not end in "Готово".
	hy2Out, err := runSSHBash(mainClient, "hub hysteria client", hy2ClientScript, true)
	fmt.Println(hy2Out)
	if err != nil {
		log.Fatalf("[exit-node] ОШИБКА: клиент Hysteria2 на Hub для %s не настроен: %v\n"+
			"[exit-node] Exit-нода уже в БД; heyearth-api не перезапущен. Исправьте причину и повторите --setup-exit-node.", exitIP, err)
	}

	// With a single exit, an API restart takes xray's and Bamboo's exit away for 30–60 s while the
	// tunnel is re-checked: only when the tunnel credentials actually changed.
	if exitDBNeedsResync(action) {
		fmt.Println("[exit-node] Перезапускаю heyearth-api для пересинхронизации Xray...")
		_, _ = runSSH(mainClient, "systemctl restart heyearth-api", false)
	} else {
		fmt.Println("[exit-node] heyearth-api не перезапускаю: credentials туннеля не изменились")
	}

	fmt.Printf("\n[exit-node] ✅ Готово! Exit-нода %s полностью настроена.\n", exitIP)
	fmt.Printf("Протоколы: Hysteria2 (UDP:50443) + SS+WS (TCP:443) + VLESS (TCP:10443)\n")
	fmt.Printf("UUID: %s\nPublic Key: %s\nShort ID: %s\n", tunnelUUID, tunnelPubKey, tunnelShortID)
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
