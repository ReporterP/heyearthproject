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
	client, err := ssh.Dial("tcp", host, config)
	if err != nil {
		log.Fatalf("SSH dial: %v", err)
	}
	defer client.Close()
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
	if out := runCmd(client, fmt.Sprintf("chmod +x %s && cp -p %s %s.bak 2>/dev/null; mv -f %s %s && echo SWAPPED", tmpPath, remotePath, remotePath, tmpPath, remotePath)); !strings.Contains(out, "SWAPPED") {
		runCmd(client, "rm -f "+tmpPath)
		log.Fatalf("swap binary failed: %s", strings.TrimSpace(out))
	}
	runCmd(client, "systemctl restart heyearth-api")
	time.Sleep(2 * time.Second)

	// Проверить здоровье
	out := runCmd(client, "systemctl is-active heyearth-api 2>/dev/null || echo FAILED")
	status := strings.TrimSpace(out)
	if status == "active" {
		fmt.Println("    heyearth-api: ACTIVE ✓")
	} else {
		fmt.Printf("    heyearth-api: %s ✗\n", status)
		fmt.Println("    Последние логи:")
		fmt.Println(runCmd(client, "journalctl -u heyearth-api -n 20 --no-pager 2>&1"))
	}

	// Проверить health endpoint
	healthOut := runCmd(client, "curl -s http://127.0.0.1:8000/api/v1/health 2>&1")
	fmt.Printf("    Health: %s\n", strings.TrimSpace(healthOut))

	// Проверить Xray sync
	xrayOut := runCmd(client, "systemctl is-active xray 2>/dev/null || echo not-installed")
	fmt.Printf("    Xray: %s\n", strings.TrimSpace(xrayOut))

	fmt.Println("\nДеплой завершён!")
	os.Remove(binaryPath)
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

	session.Stdin = strings.NewReader(string(data))
	return session.Run(fmt.Sprintf("cat > %s", remotePath))
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
