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
	"runtime"
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
	updateMain := flag.Bool("update-main", false, "обновить код Главного сервера и provisioning assets (без фронтов)")
	restart := flag.Bool("restart", false, "перезапустить сервисы Главного сервера")
	site := flag.Bool("site", false, "обновить admin_web + Vue site + Nginx")
	adminWeb := flag.Bool("admin-web", false, "обновить только admin_web")
	vueSite := flag.Bool("vue-site", false, "обновить только Vue маркетинговый сайт")
	deploySite := flag.Bool("deploy-site", false, "обновить Vue маркетинговый сайт (алиас для --vue-site)")
	statusMode := flag.Bool("status", false, "проверить состояние сервисов и API на сервере")
	backupMode := flag.Bool("backup", false, "создать полный бэкап (БД + certs + .env) на сервере")
	restoreMode := flag.Bool("restore", false, "восстановить из бэкапа")
	rotateCerts := flag.Bool("rotate-node-agent-certs", false, "перегенерировать листовые mTLS-сертификаты (client, server) без замены CA")
	flag.Parse()

	var mode string
	switch {
	case *rotateCerts:
		mode = "rotate-certs"
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
		log.Fatal("Укажи один из флагов: --main, --update-main, --restart, --site, --admin-web, --vue-site, --deploy-site, --status, --backup, --restore, --rotate-node-agent-certs. Или: fix-db")
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
		doAdminWeb(sftpClient, client, cfg)
		doVueSite(sftpClient, client, cfg)
		doNginx(sftpClient, client, cfg)
		doSSL(client, cfg)
		verifyServices(client)
	case "update-main":
		installAnsibleOnServer(client)
		uploadNodeProvisioningAssets(sftpClient, client, cfg)
		doAPI(sftpClient, client, cfg)
		doBot(sftpClient, client, cfg)
		if cfg.letsEncryptEmail != "" {
			doSSL(client, cfg)
		}
		verifyServices(client)
	case "restart":
		restartServices(client)
		verifyServices(client)
	case "site":
		doAdminWeb(sftpClient, client, cfg)
		doVueSite(sftpClient, client, cfg)
		doNginx(sftpClient, client, cfg)
		doSSL(client, cfg)
	case "admin-web":
		doAdminWeb(sftpClient, client, cfg)
		_, _ = runSSH(client, "nginx -t && systemctl reload nginx", false)
	case "vue-site":
		doVueSite(sftpClient, client, cfg)
		_, _ = runSSH(client, "nginx -t && systemctl reload nginx", false)
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

	if mode == "main" || mode == "site" || mode == "admin-web" {
		if _, err := exec.LookPath("flutter"); err != nil {
			log.Fatal("flutter is required locally to build admin_web during deploy")
		}
	}
	if mode == "main" || mode == "site" || mode == "vue-site" {
		if _, err := exec.LookPath("npm"); err != nil {
			log.Fatal("npm is required locally to build the Vue site during deploy")
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

	build := exec.Command("flutter", "build", "web", "--release", "--no-tree-shake-icons", "--base-href=/admin/", "--dart-define=HEYEARTH_ADMIN_API_BASE=/api/admin")
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

	// Ensure postgres has a known password so the API can connect without peer auth issues.
	_, _ = runSSH(client, `sudo -u postgres psql -tAc "ALTER USER postgres PASSWORD 'HeYearthDB1';" 2>/dev/null || true`, false)

	dbURL := cfg.env.get("DATABASE_URL", "")
	if dbURL == "" || dbURL == "postgres://postgres@localhost:5432/heyearth?sslmode=disable" {
		dbURL = "postgres://postgres:HeYearthDB1@localhost:5432/heyearth?sslmode=disable"
	}

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

	publicURL := "https://" + strings.TrimRight(cfg.siteDomain, "/")
	if strings.HasPrefix(cfg.siteDomain, "http://") || strings.HasPrefix(cfg.siteDomain, "https://") {
		publicURL = strings.TrimRight(cfg.siteDomain, "/")
	}
	botEnv := map[string]string{
		"APP_DIR":              appDir,
		"TELEGRAM_BOT_TOKEN":   cfg.botToken,
		"HEYEARTH_PUBLIC_URL":  publicURL,
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

func doAdminWeb(c *sftp.Client, client *ssh.Client, cfg *deployConfig) {
	_, _ = runSSH(client, "mkdir -p /var/www/heyearth", true)
	adminBuildDir := buildAdminWeb(cfg.repoRoot)
	if err := uploadDir(c, adminBuildDir, "/var/www/heyearth"); err != nil {
		log.Fatalf("upload admin_web build: %v", err)
	}
	fmt.Println("[admin-web] uploaded to /var/www/heyearth")
}

func doVueSite(c *sftp.Client, client *ssh.Client, cfg *deployConfig) {
	_, _ = runSSH(client, "mkdir -p /var/www/heyearth-site", true)
	vueBuildDir := buildVueSite(cfg.repoRoot, cfg)
	if err := uploadDir(c, vueBuildDir, "/var/www/heyearth-site"); err != nil {
		log.Fatalf("upload vue site build: %v", err)
	}
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

func buildVueSite(repoRoot string, cfg *deployConfig) string {
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
		apiBase := strings.ReplaceAll(publicBaseURL(cfg), "'", "'\"'\"'")
		telegramBot := strings.TrimSpace(cfg.env.get("TELEGRAM_BOT_USERNAME", "heyearth_bot"))
		// Use /usr/bin/npm so we don't pick up Windows npm from PATH. Clean install avoids @tailwindcss/oxide "Cannot find native binding" (npm optional deps bug).
		script := fmt.Sprintf("cd %s && rm -rf node_modules package-lock.json && export VITE_API_BASE='%s' && export VITE_TELEGRAM_BOT_URL='https://t.me/%s' && /usr/bin/npm install && /usr/bin/npm run build",
			strings.ReplaceAll(wslPath, "'", "'\"'\"'"), apiBase, telegramBot)
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
		log.Fatal("npm is required locally to build the Vue site")
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
	build.Env = append(os.Environ(),
		"VITE_API_BASE="+publicBaseURL(cfg),
		"VITE_TELEGRAM_BOT_URL=https://t.me/"+strings.TrimSpace(cfg.env.get("TELEGRAM_BOT_USERNAME", "heyearth_bot")),
	)
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

	nginxConf := fmt.Sprintf(`server {
    listen 80 default_server;
    listen [::]:80 default_server;
    server_name %s www.%s _;

    add_header X-Robots-Tag "noindex, nofollow" always;

    location /heyearth.v1.ConfigApi/ {
        grpc_pass 127.0.0.1:50051;
        grpc_set_header Host $host;
    }

    location /heyearth.v1.ClientApi/ {
        grpc_pass 127.0.0.1:50051;
        grpc_set_header Host $host;
    }

    location /heyearth.v1.BotApi/ {
        grpc_pass 127.0.0.1:50051;
        grpc_set_header Host $host;
    }

    location = /robots.txt {
        default_type text/plain;
        return 200 "User-agent: *\nDisallow: /\n";
    }

    location /go/ {
        proxy_pass http://127.0.0.1:8000;
        proxy_http_version 1.1;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-Proto $scheme;
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

    location /admin/ {
        alias /var/www/heyearth/;
        try_files $uri $uri/ /admin/index.html;
    }
    location = /admin {
        return 301 /admin/;
    }

    location /promo/ {
        alias /var/www/heyearth-site/;
        try_files $uri $uri/ /promo/index.html;
    }
    location = /promo {
        return 301 /promo/;
    }

    location / {
        return 301 /admin/;
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
	// Ensure http2 is enabled on the SSL listener that certbot creates/modifies.
	// certbot adds "listen 443 ssl" (without http2); gRPC requires h2 in ALPN.
	// nginx >= 1.25: use "http2 on;" standalone directive.
	// nginx 1.24 and below: patch the listen line to "listen 443 ssl http2;".
	patchHTTP2 := `set -e
sed -i 's/listen \[\:\:\]\:443 ssl ipv6only=on;/listen [::]:443 ssl http2 ipv6only=on;/' /etc/nginx/conf.d/heyearth.conf
sed -i 's/listen \[\:\:\]\:443 ssl;/listen [::]:443 ssl http2;/' /etc/nginx/conf.d/heyearth.conf
sed -i 's/listen 443 ssl http2 http2;/listen 443 ssl http2;/' /etc/nginx/conf.d/heyearth.conf
sed -i 's/listen 443 ssl;/listen 443 ssl http2;/' /etc/nginx/conf.d/heyearth.conf
nginx -t && systemctl reload nginx && echo "nginx reloaded with http2"`
	_, _ = runSSH(client, patchHTTP2, false)
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
		"HEYEARTH_PUBLIC_URL",
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
	fmt.Println("  systemctl restart heyearth-api telegram-bot")
	fmt.Println("")
	fmt.Println("For migration to a new server:")
	fmt.Println("  1. Run --backup on the old server")
	fmt.Println("  2. scp /opt/heyearth/backups/backup-*.tar.gz newhost:/opt/heyearth/backups/")
	fmt.Println("  3. On the new server: run deploy --main first")
	fmt.Println("  4. Then restore the DB and config from the backup")
}
