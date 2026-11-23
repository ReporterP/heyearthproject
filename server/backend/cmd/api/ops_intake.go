package main

// The deploy tools' line of the ops feed (docs/prd/stability-operations.prd.md, part 4: the
// feed is the one place for everything that changes prod). quick_admin_web.go and cmd/deploy
// write «выкачена админка <sha>» or a --setup-* run through POST /internal/ops/event on the
// loopback auth-only listener (127.0.0.1:8001), from an SSH session on the Hub. The caller proves
// it is root on the Hub by the key in /opt/heyearth/state/ops-intake.key (0600 root), created by
// the API at start: the hysteria user may reach this listener (egress guard), the key file not.

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/heyearth/backend/internal/biz"
)

const (
	opsIntakePath     = "/internal/ops/event"
	opsIntakeBodySize = 4 << 10
)

// opsIntakeKey is the key the deploy tools send in X-Ops-Key; nil (no key file) refuses everyone.
var opsIntakeKey []byte

// ensureOpsIntakeKey reads the key file, creating it (64 hex characters, mode 0600) when absent.
func ensureOpsIntakeKey(path string) ([]byte, error) {
	if b, err := os.ReadFile(path); err == nil {
		if k := strings.TrimSpace(string(b)); len(k) >= 32 {
			return []byte(k), nil
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return nil, err
	}
	key := []byte(hex.EncodeToString(raw))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(key, '\n'), 0o600); err != nil {
		return nil, err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return nil, err
	}
	return key, nil
}

type opsIntakeBody struct {
	Kind    string `json:"kind"`
	Actor   string `json:"actor"`
	Target  string `json:"target"`
	Summary string `json:"summary"`
	Impact  string `json:"impact"`
	OK      *bool  `json:"ok"`
}

// handleOpsIntake — POST /internal/ops/event with X-Ops-Key: 403 without the key, 413 over
// 4 KB, 400 for a line the feed would not take; 200 when recorded.
func handleOpsIntake(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "POST only"})
		return
	}
	got := []byte(strings.TrimSpace(r.Header.Get("X-Ops-Key")))
	if len(opsIntakeKey) == 0 || len(got) == 0 || subtle.ConstantTimeCompare(got, opsIntakeKey) != 1 {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "forbidden"})
		return
	}
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, opsIntakeBodySize))
	if err != nil {
		writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"error": "body over 4 KB"})
		return
	}
	var b opsIntakeBody
	if err := json.Unmarshal(raw, &b); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad JSON"})
		return
	}
	e := biz.OpsEvent{Kind: b.Kind, Actor: strings.TrimSpace(b.Actor), Target: biz.ScrubOpsText(b.Target),
		Summary: biz.ScrubOpsText(b.Summary), Impact: b.Impact, OK: b.OK == nil || *b.OK}
	if e.Actor == "" {
		e.Actor = "deploy"
	}
	if err := biz.CheckOpsEvent(e); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	recordOps(e)
	writeJSON(w, http.StatusOK, map[string]string{"ok": "recorded"})
}
