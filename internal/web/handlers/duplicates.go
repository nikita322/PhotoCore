package handlers

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/photocore/photocore/internal/auth"
	"github.com/photocore/photocore/internal/storage"
)

// GetMediaInfo возвращает информацию о медиа в JSON
func (h *Handlers) GetMediaInfo(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	media, err := h.store.GetMedia(id)
	if err != nil {
		h.jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if media == nil {
		h.jsonError(w, "Media not found", http.StatusNotFound)
		return
	}

	h.jsonResponse(w, media)
}

// ReplaceDuplicate заменяет оригинал на дубликат
func (h *Handlers) ReplaceDuplicate(w http.ResponseWriter, r *http.Request) {
	role := auth.GetUserRole(r)
	if role != storage.RoleAdmin {
		h.jsonError(w, "Forbidden", http.StatusForbidden)
		return
	}

	var req struct {
		DuplicateID string `json:"duplicate_id"`
		OriginalID  string `json:"original_id"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.jsonError(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	duplicate, err := h.store.GetMedia(req.DuplicateID)
	if err != nil || duplicate == nil {
		h.jsonError(w, "Duplicate not found", http.StatusNotFound)
		return
	}

	original, err := h.store.GetMedia(req.OriginalID)
	if err != nil || original == nil {
		h.jsonError(w, "Original not found", http.StatusNotFound)
		return
	}

	now := time.Now()
	original.DuplicateOf = req.DuplicateID
	original.DeletedAt = &now
	if err := h.store.SaveMedia(original); err != nil {
		h.jsonError(w, "Failed to update original: "+err.Error(), http.StatusInternalServerError)
		return
	}

	duplicate.DuplicateOf = ""
	duplicate.DeletedAt = nil
	if err := h.store.SaveMedia(duplicate); err != nil {
		h.jsonError(w, "Failed to update duplicate: "+err.Error(), http.StatusInternalServerError)
		return
	}

	h.cache.Clear()

	h.jsonResponse(w, map[string]interface{}{
		"status":  StatusReplaced,
		"message": "Оригинал заменён на дубликат",
	})
}

// UnmarkDuplicate снимает статус дубликата с медиа
func (h *Handlers) UnmarkDuplicate(w http.ResponseWriter, r *http.Request) {
	role := auth.GetUserRole(r)
	if role != storage.RoleAdmin {
		h.jsonError(w, "Forbidden", http.StatusForbidden)
		return
	}

	var req struct {
		MediaID string `json:"media_id"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.jsonError(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	media, err := h.store.GetMedia(req.MediaID)
	if err != nil || media == nil {
		h.jsonError(w, "Media not found", http.StatusNotFound)
		return
	}

	media.DuplicateOf = ""
	media.DeletedAt = nil

	if err := h.store.SaveMedia(media); err != nil {
		h.jsonError(w, "Failed to update media: "+err.Error(), http.StatusInternalServerError)
		return
	}

	h.cache.Clear()

	h.jsonResponse(w, map[string]interface{}{
		"status":  StatusUnmarked,
		"message": "Статус дубликата снят",
	})
}
