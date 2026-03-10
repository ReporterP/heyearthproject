package main

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"flag"
	"fmt"
	"io"
	"log"
	"math/big"
	"net"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"time"

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
	botToken           string
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
	updateMain := flag.Bool("update-main", false, "обновить код Главного сервера и provisioning assets")
	restart := flag.Bool("restart", false, "перезапустить сервисы Главного сервера")
	site := flag.Bool("site", false, "обновить только веб-слой и Nginx")
	statusMode := flag.Bool("status", false, "проверить состояние сервисов и API на сервере")
	flag.Parse()

	var mode string
	switch {
	case *mainMode:
		mode = "main"
	case *updateMain:
		mode = "update-main"
	case *restart:
		mode = "restart"
	case *site:
		mode = "site"
	case *statusMode:
		mode = "status"
	case flag.NArg() == 1 && flag.Arg(0) == "fix-db":
		mode = "fix-db"
	case flag.NArg() >= 1 && flag.Arg(0) == "fix-db":
		mode = "fix-db"
	default:
		log.Fatal("Укажи один из флагов: --main, --update-main, --restart, --site, --status. Или: fix-db")
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
		botToken:           env.get("TELEGRAM_BOT_TOKEN", ""),
		siteDomain:         env.get("SITE_DOMAIN", "heyearth.ru"),
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

	addr := fmt.Sprintf("%s:%d", cfg.host, cfg.deployPort)
	client, err := sshDial(addr, cfg.deployUser, cfg.deployPassword, cfg.hostKeyFingerprint)
	if err != nil {
		log.Fatalf("ssh dial: %v", err)
	}
	defer client.Close()

	if mode == "status" {
		doStatus(client, cfg)
		return
	}
	if mode == "fix-db" {
		doFixDB(client, cfg)
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
		installPostgresOnServer(client)
		installAnsibleOnServer(client)
		uploadNodeProvisioningAssets(sftpClient, client, cfg)
		doAPI(sftpClient, client, cfg)
		doBot(sftpClient, client, cfg)
		doSite(sftpClient, client, cfg)
		doSSL(client, cfg)
		verifyServices(client)
	case "update-main":
		installAnsibleOnServer(client)
		uploadNodeProvisioningAssets(sftpClient, client, cfg)
		doAPI(sftpClient, client, cfg)
		doBot(sftpClient, client, cfg)
		doSite(sftpClient, client, cfg)
		if cfg.letsEncryptEmail != "" {
			doSSL(client, cfg)
		}
		verifyServices(client)
	case "restart":
		restartServices(client)
		verifyServices(client)
	case "site":
		doSite(sftpClient, client, cfg)
		doSSL(client, cfg)
	}
}

func validateLocalConfig(mode string, cfg *deployConfig) {
	if cfg.host == "" {
		log.Fatal("DEPLOY_HOST is required")
	}
	if cfg.deployPassword == "" {
		log.Fatal("DEPLOY_SSH_PASSWORD is required")
	}
	if cfg.hostKeyFingerprint == "" {
		log.Fatal("DEPLOY_HOST_KEY_FINGERPRINT is required for secure SSH deploys")
	}

	if mode == "main" || mode == "update-main" {
		for _, key := range []string{"ADMIN_USER", "ADMIN_PASS", "JWT_SECRET", "TELEGRAM_BOT_TOKEN"} {
			if strings.TrimSpace(cfg.env.get(key, "")) == "" {
				log.Fatalf("%s is required for %s", key, mode)
			}
		}
	}

	if mode == "main" || mode == "update-main" || mode == "site" {
		if _, err := exec.LookPath("flutter"); err != nil {
			log.Fatal("flutter is required locally to build admin_web during deploy")
		}
	}
	if mode == "status" || mode == "fix-db" {
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

func sshDial(addr, user, password, expectedFingerprint string) (*ssh.Client, error) {
	callback := func(hostname string, remoteAddr net.Addr, key ssh.PublicKey) error {
		got := ssh.FingerprintSHA256(key)
		if got != expectedFingerprint {
			return fmt.Errorf("unexpected host key fingerprint for %s: got %s", hostname, got)
		}
		return nil
	}

	cfg := &ssh.ClientConfig{
		User:            user,
		Auth:            []ssh.AuthMethod{ssh.Password(password)},
		HostKeyCallback: callback,
		Timeout:         15 * time.Second,
	}
	return ssh.Dial("tcp", addr, cfg)
}

func runSSH(client *ssh.Client, cmd string, check bool) (string, error) {
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
		return outStr, fmt.Errorf("%s: %w", cmd, err)
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

func buildAdminWeb(repoRoot string) string {
	adminWebRoot := filepath.Join(repoRoot, "admin_web")

	pubGet := exec.Command("flutter", "pub", "get")
	pubGet.Dir = adminWebRoot
	pubGet.Stdout = os.Stdout
	pubGet.Stderr = os.Stderr
	if err := pubGet.Run(); err != nil {
		log.Fatalf("flutter pub get in admin_web: %v", err)
	}

	build := exec.Command("flutter", "build", "web", "--release", "--dart-define=HEYEARTH_ADMIN_API_BASE=/api/admin")
	build.Dir = adminWebRoot
	build.Stdout = os.Stdout
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		log.Fatalf("flutter build web in admin_web: %v", err)
	}

	return filepath.Join(adminWebRoot, "build", "web")
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
		Subject:               pkix.Name{CommonName: "HeYearth Root CA", Organization: []string{"HeYearth"}},
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

func generateSignedCert(certDir, prefix string, subject pkix.Name, dnsNames []string, ipAddresses []net.IP, usage x509.ExtKeyUsage, caCert *x509.Certificate, caKey *rsa.PrivateKey, serial int64) {
	key, err := rsa.GenerateKey(rand.Reader, 4096)
	if err != nil {
		log.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(serial),
		Subject:      subject,
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().AddDate(5, 0, 0),
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

	apiEnv := map[string]string{
		"APP_DIR":              appDir,
		"DATABASE_URL":         cfg.env.get("DATABASE_URL", "postgres://postgres@localhost:5432/heyearth?sslmode=disable"),
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

func doBot(c *sftp.Client, client *ssh.Client, cfg *deployConfig) {
	botPath := buildGoBinary(cfg.backendRoot, "bot", "heyearth-bot")
	if err := uploadFile(c, botPath, appDir+"/heyearth-bot"); err != nil {
		log.Fatal(err)
	}
	_, _ = runSSH(client, "chmod +x "+appDir+"/heyearth-bot", true)

	botEnv := map[string]string{
		"APP_DIR":            appDir,
		"TELEGRAM_BOT_TOKEN": cfg.botToken,
	}
	if err := uploadFileContent(c, []byte(serializeEnv(botEnv)), appDir+"/bot.env"); err != nil {
		log.Fatal(err)
	}

	service := fmt.Sprintf(`[Unit]
Description=HeYearth Telegram Bot
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=root
WorkingDirectory=%s
EnvironmentFile=%s/bot.env
ExecStart=%s/heyearth-bot
Restart=always
RestartSec=5

[Install]
WantedBy=multi-user.target
`, appDir, appDir, appDir)
	if err := uploadFileContent(c, []byte(service), "/etc/systemd/system/telegram-bot.service"); err != nil {
		log.Fatal(err)
	}
	_, _ = runSSH(client, "systemctl daemon-reload && systemctl enable telegram-bot && systemctl restart telegram-bot", true)
}

func doSite(c *sftp.Client, client *ssh.Client, cfg *deployConfig) {
	_, _ = runSSH(client, "apt-get update -qq && apt-get install -y -qq nginx", true)
	_, _ = runSSH(client, "mkdir -p /var/www/heyearth "+appDir+"/releases", true)

	adminBuildDir := buildAdminWeb(cfg.repoRoot)
	if err := uploadDir(c, adminBuildDir, "/var/www/heyearth"); err != nil {
		log.Fatalf("upload admin_web build: %v", err)
	}

	nginxConf := fmt.Sprintf(`server {
    listen 80 default_server;
    listen [::]:80 default_server;
    server_name %s www.%s _;
    root /var/www/heyearth;
    index index.html;

    location /heyearth.v1.ConfigApi/ {
        grpc_pass 127.0.0.1:50051;
    }

    location /api/ {
        proxy_pass http://127.0.0.1:8000;
        proxy_http_version 1.1;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
        client_max_body_size 512m;
    }

    location /releases/ {
        alias %s/releases/;
        add_header Cache-Control "public, max-age=3600";
        try_files $uri =404;
    }

    location / {
        try_files $uri $uri/ /index.html;
    }
}
`, cfg.siteDomain, cfg.siteDomain, appDir)
	if err := uploadFileContent(c, []byte(nginxConf), "/etc/nginx/conf.d/heyearth.conf"); err != nil {
		log.Fatal(err)
	}
	_, _ = runSSH(client, "rm -f /etc/nginx/sites-enabled/default && nginx -t && systemctl enable nginx && systemctl restart nginx", true)
}

func doSSL(client *ssh.Client, cfg *deployConfig) {
	if strings.TrimSpace(cfg.letsEncryptEmail) == "" {
		return
	}
	cmd := fmt.Sprintf("apt-get install -y -qq certbot python3-certbot-nginx && certbot --nginx -d %s -d www.%s --non-interactive --agree-tos -m %s --redirect",
		cfg.siteDomain, cfg.siteDomain, cfg.letsEncryptEmail)
	_, _ = runSSH(client, cmd, false)
}

func restartServices(client *ssh.Client) {
	_, _ = runSSH(client, "systemctl restart heyearth-api telegram-bot nginx", false)
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
	_, _ = runSSH(client, "systemctl is-active heyearth-api telegram-bot nginx", false)
	_, _ = runSSH(client, "curl -s http://127.0.0.1:8000/api/v1/health || true", false)
}

func doStatus(client *ssh.Client, cfg *deployConfig) {
	fmt.Println("=== Состояние сервисов ===")
	out, _ := runSSH(client, "systemctl is-active heyearth-api telegram-bot nginx 2>/dev/null; echo '---'; systemctl status heyearth-api --no-pager -l 2>&1 | tail -18", false)
	fmt.Println(out)
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
	// На свежем Ubuntu postgres по умолчанию требует пароль для TCP; задаём и прописываем в api.env
	fmt.Println("Настройка доступа к БД и перезапуск heyearth-api...")
	script := `set -e
sudo -u postgres psql -tAc "ALTER USER postgres PASSWORD 'HeYearthDB1';" 2>/dev/null || true
grep -q "postgres:HeYearthDB1@" /opt/heyearth/mainserver/api.env || sed -i 's#postgres://postgres@localhost:5432#postgres://postgres:HeYearthDB1@localhost:5432#' /opt/heyearth/mainserver/api.env
systemctl restart heyearth-api
sleep 3
`
	_, _ = runSSH(client, script, false)
	fmt.Println("Проверка:")
	doStatus(client, cfg)
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
		"TELEGRAM_BOT_TOKEN",
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
