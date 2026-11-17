package main

// Hub SSH authentication for the deploy tool: the deploy key first, the root password as a
// fallback (until the owner turns password logins off on the Hub). Task t1gv.

import (
	"fmt"
	"os"
	"strings"

	"golang.org/x/crypto/ssh"
)

// loadDeployKey reads an unencrypted OpenSSH private key (DEPLOY_SSH_KEY_FILE). The file lives
// outside the repository (the repo is public), e.g. C:\Users\<you>\heyearth-secrets\hub-deploy-ed25519.
func loadDeployKey(path string) (ssh.Signer, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("DEPLOY_SSH_KEY_FILE: %w", err)
	}
	signer, err := ssh.ParsePrivateKey(data)
	if err != nil {
		return nil, fmt.Errorf("DEPLOY_SSH_KEY_FILE %s: %w (a passphrase-protected key is not supported)", path, err)
	}
	return signer, nil
}

// deployAuthMethods: the key (when DEPLOY_SSH_KEY_FILE is set) and then the password (when
// DEPLOY_SSH_PASSWORD is set). At least one is required.
func deployAuthMethods(keyFile, password string) ([]ssh.AuthMethod, error) {
	var auth []ssh.AuthMethod
	if keyFile = strings.TrimSpace(keyFile); keyFile != "" {
		signer, err := loadDeployKey(keyFile)
		if err != nil {
			return nil, err
		}
		auth = append(auth, ssh.PublicKeys(signer))
	}
	if password != "" {
		auth = append(auth, ssh.Password(password))
	}
	if len(auth) == 0 {
		return nil, fmt.Errorf("нужен DEPLOY_SSH_KEY_FILE (ключ) или DEPLOY_SSH_PASSWORD в server/.env")
	}
	return auth, nil
}

// sshDialHub connects to the Hub with the pinned host key and the deploy key / password.
func sshDialHub(cfg *deployConfig) (*ssh.Client, error) {
	if !fingerprintRe.MatchString(strings.TrimSpace(cfg.hostKeyFingerprint)) {
		return nil, fmt.Errorf("DEPLOY_HOST_KEY_FINGERPRINT в server/.env не задан или не вида SHA256:<43 символа> (%q) — "+
			"подключение к Hub без закреплённого ключа хоста запрещено", cfg.hostKeyFingerprint)
	}
	auth, err := deployAuthMethods(cfg.deployKeyFile, cfg.deployPassword)
	if err != nil {
		return nil, err
	}
	return sshDialAuth(fmt.Sprintf("%s:%d", cfg.host, cfg.deployPort), cfg.deployUser, auth, cfg.hostKeyFingerprint, nil)
}
