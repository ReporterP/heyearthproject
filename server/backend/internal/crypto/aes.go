// Package crypto provides AES-256-GCM encryption for sensitive fields stored in DB.
package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
)

var errNoKey = errors.New("SSH_ENCRYPTION_KEY env not set — cannot encrypt SSH passwords")

// key returns the 32-byte AES key from env SSH_ENCRYPTION_KEY (base64-encoded).
func key() ([]byte, error) {
	raw := os.Getenv("SSH_ENCRYPTION_KEY")
	if raw == "" {
		return nil, errNoKey
	}
	k, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		return nil, fmt.Errorf("SSH_ENCRYPTION_KEY: base64 decode: %w", err)
	}
	if len(k) != 32 {
		return nil, fmt.Errorf("SSH_ENCRYPTION_KEY: must be 32 bytes (got %d)", len(k))
	}
	return k, nil
}

// Encrypt encrypts plaintext with AES-256-GCM.
// Returns nil if plaintext is empty.
func Encrypt(plaintext string) ([]byte, error) {
	if plaintext == "" {
		return nil, nil
	}
	k, err := key()
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(k)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err = io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	// Output: nonce || ciphertext+tag
	ciphertext := gcm.Seal(nonce, nonce, []byte(plaintext), nil)
	return ciphertext, nil
}

// Decrypt decrypts ciphertext produced by Encrypt.
// Returns empty string if ciphertext is nil/empty.
func Decrypt(ciphertext []byte) (string, error) {
	if len(ciphertext) == 0 {
		return "", nil
	}
	k, err := key()
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(k)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	ns := gcm.NonceSize()
	if len(ciphertext) < ns {
		return "", errors.New("crypto: ciphertext too short")
	}
	nonce, data := ciphertext[:ns], ciphertext[ns:]
	plaintext, err := gcm.Open(nil, nonce, data, nil)
	if err != nil {
		return "", fmt.Errorf("crypto: decrypt: %w", err)
	}
	return string(plaintext), nil
}
