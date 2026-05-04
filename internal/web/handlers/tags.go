package handlers

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/photocore/photocore/internal/auth"
)

// ListTags возвращает все теги
func (h *Handlers) ListTags(w http.ResponseWriter, r *http.Request) {
	tags, err := h.store.ListAllTags()
	if err != nil {
		h.jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	h.jsonResponse(w, tags)
}

// AddTags добавляет теги к медиа
func (h *Handlers) AddTags(w http.ResponseWriter, r *http.Request) {
	role := auth.GetUserRole(r)
	if !auth.CanManageTags(role) {
		h.jsonError(w, "Forbidden: недостаточно прав", http.StatusForbidden)
		return
	}

	id := chi.URLParam(r, "id")

	var req struct {
		Tags []string `json:"tags"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.jsonError(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	if err := h.store.AddTagsToMedia(id, req.Tags); err != nil {
		h.jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	h.cache.DeleteMedia(id)

	h.jsonResponse(w, map[string]string{"status": string(StatusAdded)})
}

// RemoveTags удаляет теги с медиа
func (h *Handlers) RemoveTags(w http.ResponseWriter, r *http.Request) {
	role := auth.GetUserRole(r)
	if !auth.CanDeleteTags(role) {
		h.jsonError(w, "Forbidden: недостаточно прав", http.StatusForbidden)
		return
	}

	id := chi.URLParam(r, "id")

	var req struct {
		Tags []string `json:"tags"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.jsonError(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	if err := h.store.RemoveTagsFromMedia(id, req.Tags); err != nil {
		h.jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	h.cache.DeleteMedia(id)

	h.jsonResponse(w, map[string]string{"status": string(StatusRemoved)})
}

// MediaByTag возвращает медиа с определенным тегом
func (h *Handlers) MediaByTag(w http.ResponseWriter, r *http.Request) {
	tag := chi.URLParam(r, "tag")

	media, err := h.store.ListMediaByTag(tag)
	if err != nil {
		h.jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if h.wantsHTML(r) {
		h.renderPartial(w, "gallery_content.html", map[string]interface{}{
			"Media": media,
			"Tag":   tag,
		})
		return
	}

	h.jsonResponse(w, media)
}
