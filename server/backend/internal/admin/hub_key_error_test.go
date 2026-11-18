package admin

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The node wizard shows the hub-key error to the owner: a plain sentence, not a process's raw
// output with paths («exit status 1: mkdir: cannot creat…»).
func TestHubKeyErrorIsNotRawProcessOutput(t *testing.T) {
	prev := HubPublicKey
	t.Cleanup(func() { HubPublicKey = prev })
	HubPublicKey = func(context.Context) (string, error) {
		return "", errors.New("ключ Hub: exit status 1: mkdir: cannot create directory '/opt/heyearth/state/hub-ssh': Permission denied")
	}
	rec := httptest.NewRecorder()
	HubKeyHandler(rec, httptest.NewRequest(http.MethodGet, "/api/admin/ssh-keys/hub", nil))
	body := rec.Body.String()
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status %d", rec.Code)
	}
	for _, leak := range []string{"exit status", "mkdir", "/opt/"} {
		if strings.Contains(body, leak) {
			t.Errorf("the answer carries %q: %s", leak, body)
		}
	}
}
