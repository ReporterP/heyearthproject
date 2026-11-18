package main

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/heyearth/backend/internal/biz"
)

// The cabinet's «Входы» (spec 08, QA 2026-11-18): a person sees their sign-ins and ends them,
// including on an account without a password, where a leaked HiEarth ID was a session nobody
// could close.
//
//	GET  /api/v1/user/sessions               → {items: [{id, source, created_at, current}]}
//	DELETE /api/v1/user/sessions/{id}        → close one of the caller's sessions
//	POST /api/v1/user/sessions/close-others  → close all but the caller's

func handleUserSessions(w http.ResponseWriter, r *http.Request) {
	setCORS(w, r)
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "GET only"})
		return
	}
	_, userDBID, ok := requireUser(w, r)
	if !ok {
		return
	}
	items, err := biz.ListUserSessions(r.Context(), userDBID, bearerToken(r))
	if err != nil {
		writeJSONUnavailable(w, "Список входов временно недоступен, попробуйте позже")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func handleUserSessionAction(w http.ResponseWriter, r *http.Request) {
	setCORS(w, r)
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}
	_, userDBID, ok := requireUser(w, r)
	if !ok {
		return
	}
	rest := strings.TrimPrefix(r.URL.Path, "/api/v1/user/sessions/")
	switch {
	case rest == "close-others" && r.Method == http.MethodPost:
		n, err := biz.CloseOtherUserSessions(r.Context(), userDBID, bearerToken(r))
		if err != nil {
			writeJSONUnavailable(w, "Не удалось закрыть входы, попробуйте позже")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"closed": n})
	case r.Method == http.MethodDelete:
		id, err := strconv.Atoi(rest)
		if err != nil || id <= 0 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Неверный вход"})
			return
		}
		found, err := biz.CloseUserSession(r.Context(), userDBID, id)
		if err != nil {
			writeJSONUnavailable(w, "Не удалось закрыть вход, попробуйте позже")
			return
		}
		if !found {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "Такого входа нет"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"closed": 1})
	default:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
	}
}
