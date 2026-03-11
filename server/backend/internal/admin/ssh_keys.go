package admin

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/heyearth/backend/internal/biz"
)

type sshKeyResponse struct {
	ID          int    `json:"id"`
	Name        string `json:"name"`
	PublicKey   string `json:"public_key"`
	Fingerprint string `json:"fingerprint"`
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`
}

type sshKeyPayload struct {
	Name       string `json:"name"`
	PrivateKey string `json:"private_key"`
}

func sshKeyToResponse(k biz.SshKey) sshKeyResponse {
	return sshKeyResponse{
		ID:          k.ID,
		Name:        k.Name,
		PublicKey:   k.PublicKey,
		Fingerprint: k.Fingerprint,
		CreatedAt:   k.CreatedAt.Format(time.RFC3339),
		UpdatedAt:   k.UpdatedAt.Format(time.RFC3339),
	}
}

func ListSshKeysHandler(w http.ResponseWriter, r *http.Request) {
	keys, err := biz.ListSshKeys()
	if err != nil {
		writeAdminError(w, http.StatusInternalServerError, "Failed to list SSH keys")
		return
	}
	resp := make([]sshKeyResponse, 0, len(keys))
	for _, k := range keys {
		resp = append(resp, sshKeyToResponse(k))
	}
	writeAdminJSON(w, http.StatusOK, resp)
}

func CreateSshKeyHandler(w http.ResponseWriter, r *http.Request) {
	var payload sshKeyPayload
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		writeAdminError(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	if strings.TrimSpace(payload.Name) == "" {
		writeAdminError(w, http.StatusBadRequest, "name is required")
		return
	}
	if strings.TrimSpace(payload.PrivateKey) == "" {
		writeAdminError(w, http.StatusBadRequest, "private_key is required")
		return
	}

	k, err := biz.CreateSshKey(payload.Name, payload.PrivateKey)
	if err != nil {
		writeAdminError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeAdminJSON(w, http.StatusCreated, sshKeyToResponse(*k))
}

func UpdateSshKeyHandler(w http.ResponseWriter, r *http.Request) {
	keyID, ok := parseLastPathInt(r.URL.Path)
	if !ok {
		writeAdminError(w, http.StatusBadRequest, "Invalid key id")
		return
	}

	var payload sshKeyPayload
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		writeAdminError(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	if strings.TrimSpace(payload.Name) == "" {
		writeAdminError(w, http.StatusBadRequest, "name is required")
		return
	}
	if strings.TrimSpace(payload.PrivateKey) == "" {
		writeAdminError(w, http.StatusBadRequest, "private_key is required")
		return
	}

	k, err := biz.UpdateSshKey(keyID, payload.Name, payload.PrivateKey)
	if err != nil {
		writeAdminError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeAdminJSON(w, http.StatusOK, sshKeyToResponse(*k))
}

func DeleteSshKeyHandler(w http.ResponseWriter, r *http.Request) {
	keyID, ok := parseLastPathInt(r.URL.Path)
	if !ok {
		writeAdminError(w, http.StatusBadRequest, "Invalid key id")
		return
	}

	if err := biz.DeleteSshKey(keyID); err != nil {
		writeAdminError(w, http.StatusConflict, err.Error())
		return
	}
	writeAdminJSON(w, http.StatusOK, map[string]string{"message": "SSH key deleted"})
}
