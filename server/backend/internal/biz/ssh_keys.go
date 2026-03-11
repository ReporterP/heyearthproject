package biz

import (
	"crypto/md5"
	"fmt"
	"strings"
	"time"

	"github.com/heyearth/backend/internal/db"
	"golang.org/x/crypto/ssh"
)

type SshKey struct {
	ID          int
	Name        string
	PublicKey   string
	PrivateKey  string
	Fingerprint string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

func ListSshKeys() ([]SshKey, error) {
	rows, err := db.DB.Query(`
		SELECT id, name, public_key, fingerprint, created_at, updated_at
		FROM ssh_keys
		ORDER BY name ASC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var keys []SshKey
	for rows.Next() {
		var k SshKey
		if err := rows.Scan(&k.ID, &k.Name, &k.PublicKey, &k.Fingerprint, &k.CreatedAt, &k.UpdatedAt); err != nil {
			return nil, err
		}
		keys = append(keys, k)
	}
	return keys, nil
}

func GetSshKeyByID(id int) (*SshKey, error) {
	var k SshKey
	err := db.DB.QueryRow(`
		SELECT id, name, public_key, private_key, fingerprint, created_at, updated_at
		FROM ssh_keys WHERE id = $1
	`, id).Scan(&k.ID, &k.Name, &k.PublicKey, &k.PrivateKey, &k.Fingerprint, &k.CreatedAt, &k.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &k, nil
}

func CreateSshKey(name, privateKeyPEM string) (*SshKey, error) {
	pubKey, fingerprint, err := derivePublicKeyInfo(privateKeyPEM)
	if err != nil {
		return nil, fmt.Errorf("invalid private key: %w", err)
	}

	var k SshKey
	err = db.DB.QueryRow(`
		INSERT INTO ssh_keys (name, public_key, private_key, fingerprint)
		VALUES ($1, $2, $3, $4)
		RETURNING id, name, public_key, fingerprint, created_at, updated_at
	`, strings.TrimSpace(name), pubKey, strings.TrimSpace(privateKeyPEM), fingerprint).
		Scan(&k.ID, &k.Name, &k.PublicKey, &k.Fingerprint, &k.CreatedAt, &k.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &k, nil
}

func UpdateSshKey(id int, name, privateKeyPEM string) (*SshKey, error) {
	pubKey, fingerprint, err := derivePublicKeyInfo(privateKeyPEM)
	if err != nil {
		return nil, fmt.Errorf("invalid private key: %w", err)
	}

	var k SshKey
	err = db.DB.QueryRow(`
		UPDATE ssh_keys
		SET name = $1, public_key = $2, private_key = $3, fingerprint = $4,
		    updated_at = CURRENT_TIMESTAMP
		WHERE id = $5
		RETURNING id, name, public_key, fingerprint, created_at, updated_at
	`, strings.TrimSpace(name), pubKey, strings.TrimSpace(privateKeyPEM), fingerprint, id).
		Scan(&k.ID, &k.Name, &k.PublicKey, &k.Fingerprint, &k.CreatedAt, &k.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &k, nil
}

func DeleteSshKey(id int) error {
	var count int
	if err := db.DB.QueryRow(`SELECT COUNT(*) FROM servers WHERE ssh_key_id = $1`, id).Scan(&count); err != nil {
		return err
	}
	if count > 0 {
		return fmt.Errorf("ssh key is used by %d server(s); unassign it first", count)
	}
	_, err := db.DB.Exec(`DELETE FROM ssh_keys WHERE id = $1`, id)
	return err
}

// derivePublicKeyInfo parses a PEM-encoded private key and returns the
// OpenSSH-format public key string and its MD5 fingerprint.
func derivePublicKeyInfo(privateKeyPEM string) (pubKeyStr, fingerprint string, err error) {
	signer, err := ssh.ParsePrivateKey([]byte(strings.TrimSpace(privateKeyPEM)))
	if err != nil {
		return "", "", err
	}
	pub := signer.PublicKey()
	pubKeyStr = strings.TrimSpace(string(ssh.MarshalAuthorizedKey(pub)))
	sum := md5.Sum(pub.Marshal())
	parts := make([]string, len(sum))
	for i, b := range sum {
		parts[i] = fmt.Sprintf("%02x", b)
	}
	fingerprint = "MD5:" + strings.Join(parts, ":")
	return pubKeyStr, fingerprint, nil
}
