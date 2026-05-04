package handlers

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/photocore/photocore/internal/auth"
	"github.com/photocore/photocore/internal/logger"
	"github.com/photocore/photocore/internal/storage"
)

// AdminPage отображает страницу администрирования
func (h *Handlers) AdminPage(w http.ResponseWriter, r *http.Request) {
	role := auth.GetUserRole(r)
	if !auth.CanManageUsers(role) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	users, err := h.store.ListUsers()
	if err != nil {
		logger.ErrorLog.Printf("Failed to list users: %v", err)
		http.Error(w, "Internal error", http.StatusInternalServerError)
		return
	}

	data := h.baseData(r)
	data["Users"] = users
	h.render(w, "admin.html", data)
}

// ListUsers возвращает список пользователей (API)
func (h *Handlers) ListUsers(w http.ResponseWriter, r *http.Request) {
	role := auth.GetUserRole(r)
	if !auth.CanManageUsers(role) {
		h.jsonError(w, "Forbidden", http.StatusForbidden)
		return
	}

	users, err := h.store.ListUsers()
	if err != nil {
		h.jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	type safeUser struct {
		ID          string       `json:"id"`
		Username    string       `json:"username"`
		DisplayName string       `json:"display_name"`
		Role        storage.Role `json:"role"`
		CreatedAt   string       `json:"created_at"`
		LastLogin   string       `json:"last_login"`
	}
	var safeUsers []safeUser
	for _, u := range users {
		safeUsers = append(safeUsers, safeUser{
			ID:          u.ID,
			Username:    u.Username,
			DisplayName: u.DisplayName,
			Role:        u.Role,
			CreatedAt:   u.CreatedAt.Format(time.DateTime),
			LastLogin:   u.LastLogin.Format(time.DateTime),
		})
	}

	h.jsonResponse(w, safeUsers)
}

// CreateUser создает нового пользователя
func (h *Handlers) CreateUser(w http.ResponseWriter, r *http.Request) {
	role := auth.GetUserRole(r)
	if !auth.CanManageUsers(role) {
		h.jsonError(w, "Forbidden", http.StatusForbidden)
		return
	}

	var req struct {
		Username    string       `json:"username"`
		DisplayName string       `json:"display_name"`
		Password    string       `json:"password"`
		Role        storage.Role `json:"role"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.jsonError(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	if req.Username == "" || req.Password == "" {
		h.jsonError(w, "Username and password are required", http.StatusBadRequest)
		return
	}

	if req.Role != storage.RoleAdmin && req.Role != storage.RoleEditor && req.Role != storage.RoleViewer {
		req.Role = storage.RoleViewer
	}

	existing, _ := h.store.GetUser(req.Username)
	if existing != nil {
		h.jsonError(w, "User already exists", http.StatusConflict)
		return
	}

	hash, err := h.auth.HashPassword(req.Password)
	if err != nil {
		h.jsonError(w, "Failed to hash password", http.StatusInternalServerError)
		return
	}

	user := &storage.User{
		ID:           generateRandomID(),
		Username:     req.Username,
		DisplayName:  req.DisplayName,
		PasswordHash: hash,
		Role:         req.Role,
		CreatedAt:    time.Now(),
	}

	if user.DisplayName == "" {
		user.DisplayName = user.Username
	}

	if err := h.store.SaveUser(user); err != nil {
		h.jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	h.jsonResponse(w, map[string]interface{}{
		"id":       user.ID,
		"username": user.Username,
		"role":     user.Role,
	})
}

// UpdateUser обновляет пользователя
func (h *Handlers) UpdateUser(w http.ResponseWriter, r *http.Request) {
	role := auth.GetUserRole(r)
	if !auth.CanManageUsers(role) {
		h.jsonError(w, "Forbidden", http.StatusForbidden)
		return
	}

	username := chi.URLParam(r, "username")

	user, err := h.store.GetUser(username)
	if err != nil {
		h.jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if user == nil {
		h.jsonError(w, "User not found", http.StatusNotFound)
		return
	}

	var req struct {
		DisplayName string       `json:"display_name"`
		Password    string       `json:"password"`
		Role        storage.Role `json:"role"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.jsonError(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	if req.DisplayName != "" {
		user.DisplayName = req.DisplayName
	}

	if req.Role != "" {
		if req.Role == storage.RoleAdmin || req.Role == storage.RoleEditor || req.Role == storage.RoleViewer {
			user.Role = req.Role
		}
	}

	if req.Password != "" {
		hash, err := h.auth.HashPassword(req.Password)
		if err != nil {
			h.jsonError(w, "Failed to hash password", http.StatusInternalServerError)
			return
		}
		user.PasswordHash = hash
	}

	if err := h.store.SaveUser(user); err != nil {
		h.jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	h.jsonResponse(w, map[string]string{"status": string(StatusUpdated)})
}

// DeleteUser удаляет пользователя
func (h *Handlers) DeleteUser(w http.ResponseWriter, r *http.Request) {
	role := auth.GetUserRole(r)
	if !auth.CanManageUsers(role) {
		h.jsonError(w, "Forbidden", http.StatusForbidden)
		return
	}

	username := chi.URLParam(r, "username")

	session := auth.GetSession(r)
	if session != nil && session.Username == username {
		h.jsonError(w, "Cannot delete yourself", http.StatusBadRequest)
		return
	}

	if err := h.store.DeleteUser(username); err != nil {
		h.jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	h.jsonResponse(w, map[string]string{"status": string(StatusDeleted)})
}
