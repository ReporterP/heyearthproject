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
	password, deployHost, deployPort := "", "", "22"
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
			}
		}
	}
	if password == "" {
		log.Fatal("DEPLOY_SSH_PASSWORD not found in server/.env")
	}
	if deployHost == "" {
		log.Fatal("DEPLOY_HOST not found in server/.env")
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
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         10 * time.Second,
	}
	client, err := ssh.Dial("tcp", host, config)
	if err != nil {
		log.Fatalf("SSH dial: %v", err)
	}
	defer client.Close()
	fmt.Println("    Подключено.")

	// 3. Загрузить бинарник через SSH pipe
	fmt.Println("[3/5] Загрузка бинарника через SSH pipe...")
	start := time.Now()
	remotePath := appDir + "/" + binaryName
	if err := uploadViaPipe(client, binaryPath, remotePath); err != nil {
		log.Fatalf("upload binary: %v", err)
	}
	fmt.Printf("    Загружено за %v\n", time.Since(start).Round(time.Millisecond))

	// 4. Загрузить миграции
	fmt.Println("[4/5] Загрузка миграций...")
	migrationsDir := filepath.Join(backendRoot, "migrations")
	entries, _ := os.ReadDir(migrationsDir)
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		localPath := filepath.Join(migrationsDir, entry.Name())
		remoteMigPath := appDir + "/migrations/" + entry.Name()
		data, err := os.ReadFile(localPath)
		if err != nil {
			log.Printf("  skip %s: %v", entry.Name(), err)
			continue
		}
		if err := uploadContentViaPipe(client, data, remoteMigPath); err != nil {
			log.Printf("  upload %s: %v", entry.Name(), err)
		}
	}
	fmt.Printf("    %d файлов миграций загружено\n", len(entries))

	// 5. Рестарт сервиса
	fmt.Println("[5/5] Перезапуск сервиса...")
	runCmd(client, "chmod +x "+remotePath)
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
