//go:build ignore

// Быстрая проверка состояния сервера через SSH.
// go run check_server.go
package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
)

func main() {
	_, thisFile, _, _ := runtime.Caller(0)
	backendRoot := filepath.Dir(thisFile)
	serverRoot := filepath.Dir(backendRoot)
	envFile := filepath.Join(serverRoot, ".env")

	password := ""
	if data, err := os.ReadFile(envFile); err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "DEPLOY_SSH_PASSWORD=") {
				password = strings.TrimPrefix(line, "DEPLOY_SSH_PASSWORD=")
				break
			}
		}
	}
	if password == "" {
		log.Fatal("DEPLOY_SSH_PASSWORD not found")
	}

	config := &ssh.ClientConfig{
		User:            "root",
		Auth:            []ssh.AuthMethod{ssh.Password(password)},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         10 * time.Second,
	}
	client, err := ssh.Dial("tcp", "45.150.39.236:22", config)
	if err != nil {
		log.Fatalf("SSH: %v", err)
	}
	defer client.Close()

	cmds := []struct {
		label string
		cmd   string
	}{
		{"API статус", "systemctl is-active heyearth-api"},
		{"Xray статус", "systemctl is-active xray"},
		{"API логи (последние 30)", "journalctl -u heyearth-api -n 30 --no-pager 2>&1"},
		{"Xray config VLESS клиенты", `python3 -c "import json; c=json.load(open('/opt/heyearth/xray/config.json')); vless=[i for i in c['inbounds'] if i['protocol']=='vless'][0]; print(f'VLESS clients: {len(vless[\"settings\"][\"clients\"])}'); [print(f'  - {cl[\"id\"]}') for cl in vless['settings']['clients'][:10]]" 2>&1`},
		{"Xray config SOCKS5 аккаунты", `python3 -c "import json; c=json.load(open('/opt/heyearth/xray/config.json')); socks=[i for i in c['inbounds'] if i['protocol']=='socks'][0]; accs=socks['settings']['accounts']; print(f'SOCKS5 accounts: {len(accs)}'); [print(f'  - user={a[\"user\"]}') for a in accs[:10]]" 2>&1`},
		{"БД: vless_uuid колонка", `sudo -u postgres psql -d heyearth -tAc "SELECT column_name, data_type FROM information_schema.columns WHERE table_name='users' AND column_name='vless_uuid';" 2>&1`},
		{"БД: пользователи с UUID", `sudo -u postgres psql -d heyearth -tAc "SELECT user_id, vless_uuid::text FROM users WHERE vless_uuid IS NOT NULL LIMIT 5;" 2>&1`},
		{"Health check", "curl -s http://127.0.0.1:8000/api/v1/health"},
	}

	for _, c := range cmds {
		fmt.Printf("\n=== %s ===\n", c.label)
		session, err := client.NewSession()
		if err != nil {
			fmt.Printf("ERROR: %v\n", err)
			continue
		}
		out, _ := session.CombinedOutput(c.cmd)
		fmt.Println(strings.TrimSpace(string(out)))
		session.Close()
	}
}
