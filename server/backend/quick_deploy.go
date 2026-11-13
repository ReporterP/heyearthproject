//go:build ignore

// Быстрый деплой через SSH pipe (обходит медленный SFTP).
// go run quick_deploy.go
package main

import (
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
)

const (
	user       = "root"
	appDir     = "/opt/heyearth/mainserver"
	binaryName = "heyearth-api"
)

func main() {
	// Найти корень проекта
	_, thisFile, _, _ := runtime.Caller(0)
	backendRoot := filepath.Dir(thisFile)
	serverRoot := filepath.Dir(backendRoot)
	envFile := filepath.Join(serverRoot, ".env")

	// Load host and password from .env — the Hub IP is not hardcoded
	// (it changed on 2026-10-10 after the old one was filtered upstream).
	password, deployHost, deployPort, hostKeyFP := "", "", "22", ""
	if data, err := os.ReadFile(envFile); err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			line = strings.TrimSpace(line)
			switch {
			case strings.HasPrefix(line, "DEPLOY_SSH_PASSWORD="):
				password = strings.TrimPrefix(line, "DEPLOY_SSH_PASSWORD=")
			case strings.HasPrefix(line, "DEPLOY_HOST="):
				deployHost = strings.TrimPrefix(line, "DEPLOY_HOST=")
			case strings.HasPrefix(line, "DEPLOY_PORT="):
				if p := strings.TrimPrefix(line, "DEPLOY_PORT="); p != "" {
					deployPort = p
				}
			case strings.HasPrefix(line, "DEPLOY_HOST_KEY_FINGERPRINT="):
				hostKeyFP = strings.TrimPrefix(line, "DEPLOY_HOST_KEY_FINGERPRINT=")
			}
		}
	}
	if password == "" {
		log.Fatal("DEPLOY_SSH_PASSWORD not found in server/.env")
	}
	if deployHost == "" {
		log.Fatal("DEPLOY_HOST not found in server/.env")
	}
	// The root password must never go to an unverified host: pin the Hub's host key.
	// Go's SSH client negotiates the ECDSA host key when the server has one (as the Hub does),
	// so pin THAT fingerprint: `ssh-keygen -lf /etc/ssh/ssh_host_ecdsa_key.pub` on the Hub
	// (read over an already-trusted session), the same value cmd/deploy uses.
	if !strings.HasPrefix(hostKeyFP, "SHA256:") {
		log.Fatal("DEPLOY_HOST_KEY_FINGERPRINT (SHA256:...) not found in server/.env — refusing to send the root password to an unverified host")
	}
	host := net.JoinHostPort(deployHost, deployPort)

	// 1. Собрать бинарник для Linux
	fmt.Println("[1/5] Сборка бинарника для Linux...")
	binaryPath := filepath.Join(backendRoot, binaryName+".bin")
	cmd := exec.Command("go", "build", "-o", binaryPath, "./cmd/api/")
	cmd.Dir = backendRoot
	cmd.Env = append(os.Environ(), "GOOS=linux", "GOARCH=amd64")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		log.Fatalf("build failed: %v", err)
	}
	binaryInfo, _ := os.Stat(binaryPath)
	fmt.Printf("    Binary: %s (%d MB)\n", binaryPath, binaryInfo.Size()/1024/1024)

	// 2. SSH подключение
	fmt.Println("[2/5] Подключение к серверу...")
	config := &ssh.ClientConfig{
		User: user,
		Auth: []ssh.AuthMethod{ssh.Password(password)},
		HostKeyCallback: func(_ string, _ net.Addr, key ssh.PublicKey) error {
			if got := ssh.FingerprintSHA256(key); got != hostKeyFP {
				return fmt.Errorf("host key mismatch: got %s, want %s (DEPLOY_HOST_KEY_FINGERPRINT)", got, hostKeyFP)
			}
			return nil
		},
		Timeout: 10 * time.Second,
	}
	client, err := dialRetry(host, config)
	if err != nil {
		log.Fatalf("SSH dial: %v", err)
	}
	defer func() { client.Close() }()
	fmt.Println("    Подключено.")

	// 3. Загрузить бинарник через SSH pipe — во временный файл рядом, на место кладёт mv.
	// Прямой `cat >` в рабочий путь давал "Text file busy" и полузаписанный бинарник при обрыве SSH.
	fmt.Println("[3/5] Загрузка бинарника через SSH pipe...")
	start := time.Now()
	remotePath := appDir + "/" + binaryName
	tmpPath := fmt.Sprintf("%s.new-%d", remotePath, time.Now().UnixNano())
	if err := uploadViaPipe(client, binaryPath, tmpPath); err != nil {
		runCmd(client, "rm -f "+tmpPath)
		log.Fatalf("upload binary: %v", err)
	}
	fmt.Printf("    Загружено за %v\n", time.Since(start).Round(time.Millisecond))

	// 4. Загрузить миграции. Любая ошибка — стоп до рестарта: API без миграции не стартует.
	fmt.Println("[4/5] Загрузка миграций...")
	migrationsDir := filepath.Join(backendRoot, "migrations")
	entries, err := os.ReadDir(migrationsDir)
	if err != nil {
		runCmd(client, "rm -f "+tmpPath)
		log.Fatalf("read migrations: %v", err)
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		localPath := filepath.Join(migrationsDir, entry.Name())
		remoteMigPath := appDir + "/migrations/" + entry.Name()
		data, err := os.ReadFile(localPath)
		if err != nil {
			runCmd(client, "rm -f "+tmpPath)
			log.Fatalf("read %s: %v", entry.Name(), err)
		}
		if err := uploadContentViaPipe(client, data, remoteMigPath); err != nil {
			runCmd(client, "rm -f "+tmpPath)
			log.Fatalf("upload %s: %v (сервис не перезапущен)", entry.Name(), err)
		}
	}
	fmt.Printf("    %d файлов миграций загружено\n", len(entries))

	// 5. Подменить бинарник атомарно и перезапустить
	fmt.Println("[5/5] Перезапуск сервиса...")
	if out := runCmd(client, fmt.Sprintf("chmod +x %[1]s && { [ ! -f %[2]s ] || cp -p %[2]s %[2]s.bak; } && mv -f %[1]s %[2]s && echo SWAPPED", tmpPath, remotePath)); !strings.Contains(out, "SWAPPED") {
		runCmd(client, "rm -f "+tmpPath)
		log.Fatalf("swap binary failed: %s", strings.TrimSpace(out))
	}
	runCmd(client, "systemctl restart heyearth-api")

	// The API applies migrations and warms up for 30-40 s. It must be active AND answer
	// /api/v1/health with 200 within healthWait, or the previous binary goes back and the
	// tool exits non-zero (it used to print the failure and exit 0).
	if !waitHealthy(&client, host, config, healthWait) {
		fmt.Println("    heyearth-api НЕ поднялся ✗ — последние логи:")
		fmt.Println(runCmd(client, "journalctl -u heyearth-api -n 30 --no-pager 2>&1 | tail -30"))
		fmt.Println("    Откат на предыдущий бинарник (.bak)...")
		out := runCmd(client, fmt.Sprintf("[ -f %[1]s.bak ] && cp -p %[1]s.bak %[1]s.rollback && mv -f %[1]s.rollback %[1]s && systemctl restart heyearth-api && echo ROLLED_BACK", remotePath))
		if strings.Contains(out, "ROLLED_BACK") && waitHealthy(&client, host, config, healthWait) {
			fmt.Println("    Откат выполнен, старая версия работает.")
		} else {
			fmt.Printf("    ОТКАТ НЕ УДАЛСЯ: %s\n", strings.TrimSpace(out))
		}
		os.Remove(binaryPath)
		os.Exit(1)
	}
	fmt.Println("    heyearth-api: ACTIVE ✓, health 200 ✓")

	// Проверить Xray sync
	xrayOut := runCmd(client, "systemctl is-active xray 2>/dev/null || echo not-installed")
	fmt.Printf("    Xray: %s\n", strings.TrimSpace(xrayOut))

	fmt.Println("\nДеплой завершён!")
	os.Remove(binaryPath)
}

// healthWait bounds the wait for the restarted API.
const healthWait = 90 * time.Second

// dialRetry dials the Hub up to 4 times: the SSH link to it drops now and then.
func dialRetry(host string, config *ssh.ClientConfig) (*ssh.Client, error) {
	var last error
	for i := 0; i < 4; i++ {
		c, err := ssh.Dial("tcp", host, config)
		if err == nil {
			return c, nil
		}
		last = err
		fmt.Printf("    SSH: %v — повтор через 5 с\n", err)
		time.Sleep(5 * time.Second)
	}
	return nil, last
}

// waitHealthy polls the service state and the health endpoint until both are good or
// the time is up; a dropped SSH session is redialled.
func waitHealthy(client **ssh.Client, host string, config *ssh.ClientConfig, within time.Duration) bool {
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		time.Sleep(5 * time.Second)
		out := runCmd(*client, "systemctl is-active heyearth-api 2>/dev/null; curl -s -o /dev/null -w '%{http_code}' --max-time 5 http://127.0.0.1:8000/api/v1/health")
		if strings.HasPrefix(out, "ERROR:") {
			if c, err := dialRetry(host, config); err == nil {
				(*client).Close()
				*client = c
			}
			continue
		}
		f := strings.Fields(out)
		if len(f) == 2 && f[0] == "active" && f[1] == "200" {
			return true
		}
	}
	return false
}

func uploadViaPipe(client *ssh.Client, localPath, remotePath string) error {
	f, err := os.Open(localPath)
	if err != nil {
		return err
	}
	defer f.Close()

	session, err := client.NewSession()
	if err != nil {
		return err
	}
	defer session.Close()

	session.Stdin = f
	return session.Run(fmt.Sprintf("cat > %s", remotePath))
}

func uploadContentViaPipe(client *ssh.Client, data []byte, remotePath string) error {
	session, err := client.NewSession()
	if err != nil {
		return err
	}
	defer session.Close()

	// Next to the target, then mv: a dropped session never leaves a half-written file.
	session.Stdin = strings.NewReader(string(data))
	tmp := fmt.Sprintf("%s.tmp-%d", remotePath, time.Now().UnixNano())
	return session.Run(fmt.Sprintf("cat > %[1]s && mv -f %[1]s %[2]s || { rm -f %[1]s; exit 1; }", tmp, remotePath))
}

func runCmd(client *ssh.Client, command string) string {
	session, err := client.NewSession()
	if err != nil {
		return fmt.Sprintf("ERROR: %v", err)
	}
	defer session.Close()
	out, err := session.CombinedOutput(command)
	if err != nil && len(out) == 0 {
		return fmt.Sprintf("ERROR: %v", err)
	}
	return string(out)
}

// Заглушки для совместимости с зависимостями
var _ = net.Dial
var _ = io.Copy
