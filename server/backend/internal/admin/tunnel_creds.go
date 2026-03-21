package admin

import (
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net/http"
)

// generateUUID4 генерирует UUID v4 без внешних зависимостей.
func generateUUID4() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant 10
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]), nil
}

// GenerateTunnelCredsHandler генерирует набор credentials для туннеля exit-node.
// GET /api/admin/servers/generate-tunnel-creds
func GenerateTunnelCredsHandler(w http.ResponseWriter, r *http.Request) {
	vlessUUID, err := generateUUID4()
	if err != nil {
		writeAdminError(w, http.StatusInternalServerError, "Failed to generate UUID")
		return
	}

	// x25519 key pair для Reality
	curve := ecdh.X25519()
	privKey, err := curve.GenerateKey(rand.Reader)
	if err != nil {
		writeAdminError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to generate x25519 key: %v", err))
		return
	}
	pubKey := privKey.PublicKey()

	privKeyB64 := base64.RawURLEncoding.EncodeToString(privKey.Bytes())
	pubKeyB64 := base64.RawURLEncoding.EncodeToString(pubKey.Bytes())

	// Short ID — 8 байт hex
	shortIDBytes := make([]byte, 8)
	if _, err := rand.Read(shortIDBytes); err != nil {
		writeAdminError(w, http.StatusInternalServerError, "Failed to generate short id")
		return
	}
	shortID := hex.EncodeToString(shortIDBytes)

	// Пароль для Shadowsocks (16 байт base64)
	ssPassBytes := make([]byte, 16)
	if _, err := rand.Read(ssPassBytes); err != nil {
		writeAdminError(w, http.StatusInternalServerError, "Failed to generate ss password")
		return
	}
	ssPassword := base64.StdEncoding.EncodeToString(ssPassBytes)

	writeAdminJSON(w, http.StatusOK, map[string]string{
		"vless_uuid":          vlessUUID,
		"reality_private_key": privKeyB64,
		"reality_public_key":  pubKeyB64,
		"reality_short_id":    shortID,
		"ss_password":         ssPassword,
	})
}
