package handlers

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/photocore/photocore/internal/auth"
)

// GenerateAPIToken генерирует новый API токен для пользователя
func (h *Handlers) GenerateAPIToken(w http.ResponseWriter, r *http.Request) {
	session := auth.GetSession(r)
	if session == nil {
		h.jsonError(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	var req struct {
		DeviceName string `json:"device_name"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.jsonError(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if req.DeviceName == "" {
		req.DeviceName = defaultDeviceName
	}

	token, err := h.auth.GenerateAPIToken(session.UserID, session.Username, session.Role, req.DeviceName)
	if err != nil {
		h.jsonError(w, "Failed to generate token: "+err.Error(), http.StatusInternalServerError)
		return
	}

	h.jsonResponse(w, token)
}

// ListAPITokens возвращает список токенов пользователя
func (h *Handlers) ListAPITokens(w http.ResponseWriter, r *http.Request) {
	session := auth.GetSession(r)
	if session == nil {
		h.jsonError(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	tokens, err := h.store.ListUserAPITokens(session.UserID)
	if err != nil {
		h.jsonError(w, "Failed to list tokens: "+err.Error(), http.StatusInternalServerError)
		return
	}

	h.jsonResponse(w, tokens)
}

// RevokeAPIToken отзывает API токен
func (h *Handlers) RevokeAPIToken(w http.ResponseWriter, r *http.Request) {
	session := auth.GetSession(r)
	if session == nil {
		h.jsonError(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	token := chi.URLParam(r, "token")
	if token == "" {
		h.jsonError(w, "Token parameter required", http.StatusBadRequest)
		return
	}

	if !auth.IsAdmin(session.Role) {
		apiToken, err := h.store.GetAPIToken(token)
		if err != nil {
			h.jsonError(w, "Failed to get token: "+err.Error(), http.StatusInternalServerError)
			return
		}
		if apiToken == nil {
			h.jsonError(w, "Token not found", http.StatusNotFound)
			return
		}
		if apiToken.UserID != session.UserID {
			h.jsonError(w, "Forbidden", http.StatusForbidden)
			return
		}
	}

	if err := h.store.DeleteAPIToken(token); err != nil {
		h.jsonError(w, "Failed to revoke token: "+err.Error(), http.StatusInternalServerError)
		return
	}

	h.jsonResponse(w, map[string]string{"status": string(StatusRevoked)})
}
