package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestLimitPublicBodies(t *testing.T) {
	var read int64
	var readErr error
	h := limitPublicBodies(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		read, readErr = io.Copy(io.Discard, r.Body)
	}))
	big := strings.Repeat("x", publicBodyLimit+1)

	// A public endpoint stops reading past the limit (YooKassa webhook, sign-up, invoices...).
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/api/webhooks/yookassa", strings.NewReader(big)))
	if readErr == nil || read > publicBodyLimit {
		t.Fatalf("public path: read %d bytes, err %v; want an error at %d", read, readErr, publicBodyLimit)
	}

	// The admin API keeps its own limits (app uploads are up to 512 MB, behind admin auth).
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/api/admin/app-releases", strings.NewReader(big)))
	if readErr != nil || read != int64(len(big)) {
		t.Fatalf("admin path: read %d, err %v", read, readErr)
	}

	// A small public body passes untouched.
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(`{"a":1}`)))
	if readErr != nil || read != 7 {
		t.Fatalf("small body: read %d, err %v", read, readErr)
	}
}
