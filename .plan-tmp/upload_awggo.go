//go:build ignore

// One-off uploader: pushes the cross-compiled amneziawg-go (2.0) to the Hub
// via SSH pipe (same mechanism as quick_deploy.go). Uploads to a .new path;
// the swap + awg0 restart is done separately so we control timing.
package main

import (
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
)

func main() {
	password := ""
	if data, err := os.ReadFile("../server/.env"); err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "DEPLOY_SSH_PASSWORD=") {
				password = strings.TrimPrefix(line, "DEPLOY_SSH_PASSWORD=")
				break
			}
		}
	}
	if password == "" {
		log.Fatal("DEPLOY_SSH_PASSWORD not found in ../server/.env")
	}

	config := &ssh.ClientConfig{
		User:            "root",
		Auth:            []ssh.AuthMethod{ssh.Password(password)},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         10 * time.Second,
	}
	client, err := ssh.Dial("tcp", "45.150.39.236:22", config)
	if err != nil {
		log.Fatalf("SSH dial: %v", err)
	}
	defer client.Close()

	f, err := os.Open("awggo/amneziawg-go.linux")
	if err != nil {
		log.Fatalf("open binary: %v", err)
	}
	defer f.Close()

	session, err := client.NewSession()
	if err != nil {
		log.Fatalf("session: %v", err)
	}
	defer session.Close()
	session.Stdin = f
	start := time.Now()
	if err := session.Run("cat > /opt/heyearth/awg/amneziawg-go.new"); err != nil {
		log.Fatalf("upload: %v", err)
	}
	fmt.Printf("Uploaded amneziawg-go.new in %v\n", time.Since(start).Round(time.Millisecond))
}
