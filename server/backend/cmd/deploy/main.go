// Деплой Shadowsocks + Telegram-бот (Go) + API (Go) + сайт на VPS.
// Запуск из корня проекта: go run ./cmd/deploy [--fresh|--update|--restart|--site]
// При --fresh на сервер устанавливается Go.
package main

import (
	"bufio"
	"bytes"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

const (
	goVersion    = "1.22.4"
	goDownloadURL = "https://go.dev/dl/go" + goVersion + ".linux-amd64.tar.gz"
	goInstallPath = "/usr/local"
	appDir        = "/opt/vpn-bot"
)

func main() {
	fresh := flag.Bool("fresh", false, "новый сервер: полная установка + установка Go")
	update := flag.Bool("update", false, "обновить файлы и перезапустить сервисы")
	restart := flag.Bool("restart", false, "только перезапуск сервисов")
	site := flag.Bool("site", false, "только сайт и SSL")
	flag.Parse()

	var mode string
	switch {
	case *fresh:
		mode = "fresh"
	case *update:
		mode = "update"
	case *restart:
		mode = "restart"
	case *site:
		mode = "site"
	default:
		log.Fatal("Укажи один из флагов: --fresh, --update, --restart, --site")
	}

	projectRoot := findProjectRoot()
	env := loadEnv(projectRoot)
	host := env.get("DEPLOY_HOST", "155.212.225.172")
	user := env.get("DEPLOY_USER", "root")
	password := env.get("DEPLOY_SSH_PASSWORD", "")
	port := env.getInt("DEPLOY_PORT", 22)
	botToken := env.get("TELEGRAM_BOT_TOKEN", "")
	siteDomain := env.get("SITE_DOMAIN", "heyearth.ru")
	letsEncryptEmail := env.get("LETSENCRYPT_EMAIL", "")

	if password == "" {
		log.Fatal("Задай DEPLOY_SSH_PASSWORD в .env (скопируй .env.example в .env)")
	}
	if (mode == "fresh" || mode == "update") && botToken == "" {
		botToken = readBotTokenFromFile(projectRoot)
		if botToken == "" {
			log.Fatal("Задай TELEGRAM_BOT_TOKEN в .env или в bot/.env")
		}
	}

	addr := fmt.Sprintf("%s:%d", host, port)
	fmt.Printf("Подключение к %s@%s ...\n", user, addr)

	client, err := sshDial(addr, user, password)
	if err != nil {
		log.Fatalf("Ошибка подключения: %v\nПроверь: хост, порт SSH, пароль в .env", err)
	}
	defer client.Close()

	sftpClient, err := sftp.NewClient(client)
	if err != nil {
		log.Fatalf("SFTP: %v", err)
	}
	defer sftpClient.Close()

	cfg := &deployConfig{
		projectRoot:      projectRoot,
		botToken:         botToken,
		siteDomain:       siteDomain,
		letsEncryptEmail: letsEncryptEmail,
	}

	switch mode {
	case "fresh":
		uploadHelperScripts(sftpClient, client, cfg)
		installGoOnServer(client)
		doBot(sftpClient, client, cfg)
		doShadowsocks(sftpClient, client, cfg)
		doAPI(sftpClient, client, cfg)
		doSite(sftpClient, client, cfg)
		doSSL(client, cfg)
		verifyServices(client, cfg)
	case "update":
		uploadHelperScripts(sftpClient, client, cfg)
		doShadowsocks(sftpClient, client, cfg)
		doBot(sftpClient, client, cfg)
		doAPI(sftpClient, client, cfg)
		runSSH(client, "mkdir -p /var/www/heyearth", false)
		uploadFile(sftpClient, filepath.Join(cfg.projectRoot, "site", "index.html"), "/var/www/heyearth/index.html")
		if cfg.letsEncryptEmail != "" {
			doSSL(client, cfg)
		}
		runSSH(client, "systemctl restart telegram-bot; systemctl restart heyearth-api 2>/dev/null; systemctl reload nginx 2>/dev/null; echo OK", false)
		verifyServices(client, cfg)
	case "restart":
		runSSH(client, "systemctl restart telegram-bot 2>/dev/null || true", false)
		runSSH(client, "systemctl restart heyearth-api 2>/dev/null || true", false)
		runSSH(client, "systemctl reload nginx 2>/dev/null || true", false)
		runSSH(client, "systemctl status telegram-bot nginx heyearth-api --no-pager 2>/dev/null || true", false)
	case "site":
		doSite(sftpClient, client, cfg)
		doSSL(client, cfg)
	}

	fmt.Println("\nГотово.")
	if mode == "site" || mode == "fresh" {
		if letsEncryptEmail != "" {
			fmt.Printf("Сайт: https://%s\n", siteDomain)
		} else {
			fmt.Printf("Сайт: http://%s\n", siteDomain)
			fmt.Println("Внимание: HTTPS не настроен. Приложение по умолчанию использует https:// — задай LETSENCRYPT_EMAIL в .env и выполни из корня серверного проекта: python scripts/deploy.py --site")
		}
	}
}

type deployConfig struct {
	projectRoot      string
	botToken         string
	siteDomain       string
	letsEncryptEmail string
}

type envMap map[string]string

func loadEnv(projectRoot string) envMap {
	m := make(envMap)
	f, err := os.Open(filepath.Join(projectRoot, ".env"))
	if err != nil {
		return m
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if i := strings.Index(line, "="); i > 0 {
			k := strings.TrimSpace(line[:i])
			v := strings.TrimSpace(line[i+1:])
			v = strings.Trim(v, "\"'")
			m[k] = v
		}
	}
	for _, e := range os.Environ() {
		if i := strings.Index(e, "="); i > 0 {
			m[e[:i]] = e[i+1:]
		}
	}
	return m
}

func (m envMap) get(key, def string) string {
	if v, ok := m[key]; ok && v != "" {
		return v
	}
	return def
}

func (m envMap) getInt(key string, def int) int {
	s := m.get(key, "")
	if s == "" {
		return def
	}
	var n int
	if _, err := fmt.Sscanf(s, "%d", &n); err == nil {
		return n
	}
	return def
}

func readBotTokenFromFile(projectRoot string) string {
	f, err := os.Open(filepath.Join(projectRoot, "bot", ".env"))
	if err != nil {
		return ""
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if strings.HasPrefix(line, "TELEGRAM_BOT_TOKEN=") {
			v := strings.TrimSpace(line[len("TELEGRAM_BOT_TOKEN="):])
			return strings.Trim(v, "\"'")
		}
	}
	return ""
}

func findProjectRoot() string {
	dir, _ := os.Getwd()
	for {
		if dir == "" || dir == filepath.VolumeName(dir)+string(filepath.Separator) {
			break
		}
		if _, err := os.Stat(filepath.Join(dir, "server")); err == nil {
			if _, err := os.Stat(filepath.Join(dir, "backend")); err == nil {
				return dir
			}
		}
		dir = filepath.Dir(dir)
	}
	root, _ := os.Getwd()
	return root
}

func sshDial(addr, user, password string) (*ssh.Client, error) {
	cfg := &ssh.ClientConfig{
		User: user,
		Auth: []ssh.AuthMethod{ssh.Password(password)},
	}
	cfg.SetDefaults()
	cfg.HostKeyCallback = ssh.InsecureIgnoreHostKey()
	return ssh.Dial("tcp", addr, cfg)
}

func runSSH(client *ssh.Client, cmd string, check bool) (string, error) {
	if len(cmd) > 80 {
		fmt.Printf("  $ %s...\n", cmd[:80])
	} else {
		fmt.Printf("  $ %s\n", cmd)
	}
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
	errStr := errOut.String()
	if outStr != "" {
		fmt.Print(outStr)
	}
	if err != nil && errStr != "" {
		fmt.Fprint(os.Stderr, errStr)
	}
	if check && err != nil {
		return outStr, fmt.Errorf("exit: %w", err)
	}
	return outStr, nil
}

func sftpMkdirAll(c *sftp.Client, path string) error {
	path = strings.TrimPrefix(filepath.ToSlash(path), "/")
	if path == "" {
		return nil
	}
	parts := strings.Split(path, "/")
	var cur string
	for _, p := range parts {
		if p == "" {
			continue
		}
		if cur == "" {
			cur = "/" + p
		} else {
			cur = cur + "/" + p
		}
		_ = c.Mkdir(cur) // игнорируем ошибку (каталог может уже существовать)
	}
	return nil
}

func uploadFile(c *sftp.Client, local, remote string) error {
	f, err := os.Open(local)
	if err != nil {
		return fmt.Errorf("open local %s: %w", local, err)
	}
	defer f.Close()
	remote = normRemote(remote)
	dir := remoteDir(remote)
	if dir != "" {
		sftpMkdirAll(c, dir)
	}
	_ = c.Remove(remote) // перезапись: удалить, если есть
	dst, err := c.Create(remote)
	if err != nil {
		return fmt.Errorf("sftp create %s: %w", remote, err)
	}
	defer dst.Close()
	_, err = io.Copy(dst, f)
	return err
}

func normRemote(path string) string {
	return "/" + strings.TrimPrefix(strings.ReplaceAll(path, "\\", "/"), "/")
}

func remoteDir(remote string) string {
	remote = normRemote(remote)
	i := strings.LastIndex(remote, "/")
	if i <= 0 {
		return ""
	}
	return remote[:i]
}

func uploadFileContent(c *sftp.Client, content []byte, remote string) error {
	remote = normRemote(remote)
	dir := remoteDir(remote)
	if dir != "" {
		sftpMkdirAll(c, dir)
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

func uploadFileLF(c *sftp.Client, localPath, remote string) error {
	data, err := os.ReadFile(localPath)
	if err != nil {
		return err
	}
	text := string(data)
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	return uploadFileContent(c, []byte(text), remote)
}

func buildGoBinary(projectRoot, name, outName string) (string, error) {
	backendDir := filepath.Join(projectRoot, "backend")
	env := os.Environ()
	env = append(env, "GOOS=linux", "GOARCH=amd64")
	cmd := exec.Command("go", "build", "-o", outName, "./cmd/"+name)
	cmd.Dir = backendDir
	cmd.Env = env
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("go build ./cmd/%s: %w", name, err)
	}
	return filepath.Join(backendDir, outName), nil
}

func installGoOnServer(client *ssh.Client) {
	fmt.Println("\n--- Установка Go на сервер ---")
	goBin := goInstallPath + "/go/bin"
	script := fmt.Sprintf(`set -e
if command -v go >/dev/null 2>&1 && go version | grep -q go1; then
  echo "Go уже установлен: $(go version)"
  exit 0
fi
echo "Устанавливаю Go %s..."
cd /tmp
wget -q %s -O go.tar.gz 2>/dev/null || curl -sL %s -o go.tar.gz
rm -rf %s/go
tar -C %s -xzf go.tar.gz
echo 'export PATH=$PATH:%s' > /etc/profile.d/go.sh
chmod 644 /etc/profile.d/go.sh
export PATH=$PATH:%s
go version
echo "Go установлен."
`, goVersion, goDownloadURL, goDownloadURL, goInstallPath, goInstallPath, goBin, goBin)
	runSSH(client, script, false)
}

func uploadHelperScripts(c *sftp.Client, client *ssh.Client, cfg *deployConfig) {
	fmt.Println("\n--- Скрипты в /root/ ---")
	for _, name := range []string{"ss-diagnose.sh", "setup-shadowsocks.sh"} {
		p := filepath.Join(cfg.projectRoot, "server", name)
		if _, err := os.Stat(p); err == nil {
			if err := uploadFileLF(c, p, "/root/"+name); err != nil {
				log.Printf("upload %s: %v", name, err)
			}
			runSSH(client, "chmod +x /root/"+name, false)
		}
	}
}

func doBot(c *sftp.Client, client *ssh.Client, cfg *deployConfig) {
	fmt.Println("\n--- Бот (Go): сборка и загрузка ---")
	runSSH(client, "mkdir -p "+appDir, false)
	binPath, err := buildGoBinary(cfg.projectRoot, "bot", "heyearth-bot")
	if err != nil {
		log.Fatal(err)
	}
	if err := uploadFile(c, binPath, appDir+"/heyearth-bot"); err != nil {
		log.Fatal(err)
	}
	runSSH(client, "chmod +x "+appDir+"/heyearth-bot", false)
	addSS := filepath.Join(cfg.projectRoot, "server", "add-ss-user.sh")
	if _, err := os.Stat(addSS); err == nil {
		uploadFileLF(c, addSS, appDir+"/add-ss-user.sh")
		runSSH(client, "chmod +x "+appDir+"/add-ss-user.sh", false)
	}
	uploadFileContent(c, []byte("TELEGRAM_BOT_TOKEN="+cfg.botToken+"\n"), appDir+"/.env")
	svc := fmt.Sprintf(`[Unit]
Description=Telegram VPN Bot (Shadowsocks)
After=network.target

[Service]
Type=simple
User=root
WorkingDirectory=%s
ExecStart=%s/heyearth-bot
Restart=always
RestartSec=5
Environment=TELEGRAM_BOT_TOKEN=%s
Environment=APP_DIR=%s
Environment=SS_HOST=%s

[Install]
WantedBy=multi-user.target
`, appDir, appDir, cfg.botToken, appDir, cfg.siteDomain)
	uploadFileContent(c, []byte(svc), "/tmp/telegram-bot.service")
	runSSH(client, "mv /tmp/telegram-bot.service /etc/systemd/system/ && systemctl daemon-reload", true)
	runSSH(client, "systemctl enable telegram-bot && systemctl restart telegram-bot", true)
	runSSH(client, "sleep 2 && systemctl status telegram-bot --no-pager", false)
	runSSH(client, "journalctl -u telegram-bot -n 20 --no-pager", false)
}

func doShadowsocks(c *sftp.Client, client *ssh.Client, cfg *deployConfig) {
	out, _ := runSSH(client, "test -f /etc/systemd/system/shadowsocks-libev@.service && echo ok", false)
	if strings.Contains(out, "ok") {
		fmt.Println("\nShadowsocks (per-user) уже настроен, пропускаем.")
		return
	}
	fmt.Println("\n--- Shadowsocks: установка (персональные ссылки) ---")
	setupPath := filepath.Join(cfg.projectRoot, "server", "setup-shadowsocks.sh")
	if _, err := os.Stat(setupPath); err == nil {
		uploadFileLF(c, setupPath, "/root/setup-shadowsocks.sh")
		runSSH(client, "chmod +x /root/setup-shadowsocks.sh", false)
	}
	runSSH(client, "SS_HOST="+cfg.siteDomain+" DEBIAN_FRONTEND=noninteractive /root/setup-shadowsocks.sh", false)
}

func doAPI(c *sftp.Client, client *ssh.Client, cfg *deployConfig) {
	fmt.Println("\n--- API (Go) ---")
	runSSH(client, "mkdir -p "+appDir, false)
	// Скрипт нужен API для выдачи SS-ссылок — заливаем всегда
	addSS := filepath.Join(cfg.projectRoot, "server", "add-ss-user.sh")
	if _, err := os.Stat(addSS); err == nil {
		if err := uploadFileLF(c, addSS, appDir+"/add-ss-user.sh"); err != nil {
			log.Printf("  предупреждение: upload add-ss-user.sh: %v", err)
		} else {
			runSSH(client, "chmod +x "+appDir+"/add-ss-user.sh", false)
		}
	}
	// Обновить go.sum перед сборкой (grpc/protobuf и т.д.)
	backendDir := filepath.Join(cfg.projectRoot, "backend")
	tidy := exec.Command("go", "mod", "tidy")
	tidy.Dir = backendDir
	tidy.Stdout = os.Stdout
	tidy.Stderr = os.Stderr
	if err := tidy.Run(); err != nil {
		log.Printf("[!] go mod tidy завершился с ошибкой: %v", err)
	}
	binPath, err := buildGoBinary(cfg.projectRoot, "api", "heyearth-api")
	if err != nil {
		log.Fatal(err)
	}
	if err := uploadFile(c, binPath, appDir+"/heyearth-api"); err != nil {
		log.Fatal(err)
	}
	runSSH(client, "chmod +x "+appDir+"/heyearth-api", false)
	svc := fmt.Sprintf(`[Unit]
Description=HeYearth VPN API
After=network.target network-online.target
Wants=network-online.target

[Service]
Type=simple
User=root
WorkingDirectory=%s
Environment=APP_DIR=%s
Environment=SS_HOST=%s
EnvironmentFile=-/opt/vpn-bot/app-update.env
# gRPC :50051, REST + Swagger :8000
ExecStart=%s/heyearth-api
Restart=always
RestartSec=5

[Install]
WantedBy=multi-user.target
`, appDir, appDir, cfg.siteDomain, appDir)
	uploadFileContent(c, []byte(svc), "/tmp/heyearth-api.service")
	runSSH(client, "mv /tmp/heyearth-api.service /etc/systemd/system/ && systemctl daemon-reload", true)
	runSSH(client, "systemctl enable heyearth-api", true)
	runSSH(client, "systemctl restart heyearth-api", false)
	runSSH(client, "sleep 3 && systemctl is-active heyearth-api", false)
	out, _ := runSSH(client, "curl -s -o /dev/null -w '%{http_code}' http://127.0.0.1:8000/api/v1/health 2>/dev/null || echo 000", false)
	if !strings.Contains(out, "200") {
		fmt.Println("\n  [ОШИБКА] REST gateway (8000) не отвечает. Логи heyearth-api:")
		runSSH(client, "journalctl -u heyearth-api -n 50 --no-pager 2>/dev/null", false)
		fmt.Println("  Проверь на сервере: ls -la /opt/vpn-bot/heyearth-api /opt/vpn-bot/add-ss-user.sh")
	}
}

func doSite(c *sftp.Client, client *ssh.Client, cfg *deployConfig) {
	fmt.Println("\n--- Сайт ---")
	// Открыть порты до запуска nginx, чтобы API был доступен с интернета без VPN
	runSSH(client, "ufw allow 22/tcp 2>/dev/null; ufw allow 80/tcp 2>/dev/null; ufw allow 443/tcp 2>/dev/null; true", false)
	runSSH(client, "mkdir -p /var/www/heyearth", false)
	uploadFile(c, filepath.Join(cfg.projectRoot, "site", "index.html"), "/var/www/heyearth/index.html")
	runSSH(client, "apt-get install -y -qq nginx 2>/dev/null || true", false)
	// default_server — ответ по IP и любому Host (доступ без VPN по IP или домену)
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
        proxy_pass http://127.0.0.1:8000/api/;
        proxy_http_version 1.1;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
    }
    location /releases/ {
        alias /opt/vpn-bot/releases/;
        add_header Cache-Control "public, max-age=3600";
    }
    location / { try_files $uri $uri/ =404; }
}
`, cfg.siteDomain, cfg.siteDomain)
	uploadFileContent(c, []byte(nginxConf), "/tmp/heyearth.conf")
	runSSH(client, "mkdir -p /etc/nginx/conf.d && mv /tmp/heyearth.conf /etc/nginx/conf.d/heyearth.conf", false)
	// убрать дубликат default_server (sites-enabled/default), иначе nginx -t падает
	runSSH(client, "rm -f /etc/nginx/sites-enabled/default 2>/dev/null; true", false)
	runSSH(client, "nginx -t", true)
	runSSH(client, "systemctl enable nginx 2>/dev/null; systemctl restart nginx 2>/dev/null || systemctl start nginx 2>/dev/null", false)
	runSSH(client, "sleep 1 && systemctl is-active nginx", false)
}

func doSSL(client *ssh.Client, cfg *deployConfig) {
	if cfg.letsEncryptEmail == "" {
		fmt.Println("\n[!] HTTPS не настроен: в .env задай LETSENCRYPT_EMAIL=твой@email.ru и выполни из корня серверного проекта: python scripts/deploy.py --site")
		return
	}
	fmt.Println("\n--- HTTPS (Let's Encrypt) ---")
	runSSH(client, "ufw allow 80/tcp 2>/dev/null; ufw allow 443/tcp 2>/dev/null; ufw status 2>/dev/null || true", false)
	runSSH(client, "apt-get install -y -qq certbot python3-certbot-nginx 2>/dev/null || true", false)
	out, _ := runSSH(client, fmt.Sprintf("certbot --nginx -d %s -d www.%s --non-interactive --agree-tos -m %s --redirect 2>&1",
		cfg.siteDomain, cfg.siteDomain, cfg.letsEncryptEmail), false)
	if out != "" && (strings.Contains(out, "Congratulations") || strings.Contains(out, "Successfully")) {
		fmt.Println("  [OK] Сертификат получен, HTTPS включён.")
		// Certbot перезаписывает конфиг и убирает default_server — без него запросы по IP/без VPN могут не попадать в хост
		runSSH(client, `sed -i 's/\(listen [^;]*\)\;/\1 default_server;/g' /etc/nginx/conf.d/heyearth.conf 2>/dev/null; sed -i 's/default_server default_server/default_server/g' /etc/nginx/conf.d/heyearth.conf; nginx -t && systemctl reload nginx`, false)
	} else if out != "" && (strings.Contains(out, "failed") || strings.Contains(out, "Error")) {
		fmt.Println("  [!] Certbot не смог выдать сертификат. Проверь: 1) DNS домена указывает на IP сервера, 2) порты 80 и 443 открыты снаружи.")
		fmt.Println("  Вывод certbot:", out)
	}
}

// verifyServices проверяет, что API и nginx работают, порт 443 открыт; при необходимости перезапускает.
func verifyServices(client *ssh.Client, cfg *deployConfig) {
	fmt.Println("\n--- Проверка сервисов ---")
	// 1) API должен быть запущен
	runSSH(client, "systemctl is-active --quiet heyearth-api || (systemctl start heyearth-api && sleep 1)", false)
	out, _ := runSSH(client, "systemctl is-active heyearth-api 2>/dev/null || true", false)
	if !strings.Contains(out, "active") {
		fmt.Println("  [!] heyearth-api не active, перезапускаю...")
		runSSH(client, "systemctl restart heyearth-api; sleep 2", false)
	}
	runSSH(client, "systemctl status heyearth-api --no-pager -l 2>/dev/null || true", false)

	// 2) Проверка: REST gateway (8000) и gRPC (50051)
	out, _ = runSSH(client, "curl -s -o /dev/null -w '%{http_code}' http://127.0.0.1:8000/api/v1/health 2>/dev/null || echo 000", false)
	if !strings.Contains(out, "200") {
		fmt.Println("  [!] REST gateway :8000 не отвечает. Проверь journalctl -u heyearth-api -n 50")
	} else {
		fmt.Println("  [OK] REST gateway :8000")
	}
	runSSH(client, "ss -tlnp 2>/dev/null | grep -q ':50051 ' && echo '  [OK] gRPC :50051' || echo '  [!] gRPC :50051 не слушает'", false)

	// 3) Nginx: конфиг, grpc_pass и proxy_pass
	runSSH(client, "nginx -t 2>&1", false)
	runSSH(client, "systemctl is-active --quiet nginx || (systemctl start nginx && echo 'nginx started')", false)
	runSSH(client, "grep -q 'grpc_pass.*50051' /etc/nginx/conf.d/heyearth.conf 2>/dev/null && echo '  [OK] nginx grpc_pass :50051' || true", false)
	runSSH(client, "grep -q 'proxy_pass.*8000' /etc/nginx/conf.d/heyearth.conf 2>/dev/null && echo '  [OK] nginx proxy_pass /api/ :8000' || true", false)
	runSSH(client, "systemctl status nginx --no-pager 2>/dev/null || true", false)

	// 4) Порт 443 открыт в ufw (если ufw включён)
	runSSH(client, "ufw status 2>/dev/null | grep -q 'Status: active' && (ufw status | grep -q 443 && echo '  [OK] ufw: 443 открыт' || (ufw allow 443/tcp 2>/dev/null; ufw status | grep 443)) || true", false)

	// 5) Итог
	out, _ = runSSH(client, "curl -s -o /dev/null -w '%{http_code}' http://127.0.0.1:8000/api/v1/health 2>/dev/null || echo fail", false)
	if strings.Contains(out, "200") {
		fmt.Println("\n  Итог: gRPC :50051 + REST gateway :8000. Приложение: https://" + cfg.siteDomain + "/api/v1/...")
	} else {
		fmt.Println("\n  Итог: REST gateway не отвечает. Выполни на сервере: journalctl -u heyearth-api -n 50")
	}
}
