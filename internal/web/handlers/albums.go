package handlers

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/photocore/photocore/internal/auth"
	"github.com/photocore/photocore/internal/storage"
)

// ListAlbums возвращает список альбомов
func (h *Handlers) ListAlbums(w http.ResponseWriter, r *http.Request) {
	albums, err := h.store.ListAlbums()
	if err != nil {
		h.jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if h.wantsHTML(r) {
		data := h.baseData(r)
		data["Albums"] = albums
		h.render(w, "albums.html", data)
		return
	}

	h.jsonResponse(w, albums)
}

// GetAlbum возвращает альбом с медиа
func (h *Handlers) GetAlbum(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	album, err := h.store.GetAlbum(id)
	if err != nil {
		h.jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if album == nil {
		http.NotFound(w, r)
		return
	}

	media, err := h.store.GetAlbumMedia(id)
	if err != nil {
		h.jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if h.wantsHTML(r) {
		data := h.baseData(r)
		data["Album"] = album
		data["Media"] = media
		h.render(w, "album.html", data)
		return
	}

	h.jsonResponse(w, map[string]interface{}{
		"album": album,
		"media": media,
	})
}

// CreateAlbum создает новый альбом
func (h *Handlers) CreateAlbum(w http.ResponseWriter, r *http.Request) {
	role := auth.GetUserRole(r)
	if !auth.CanCreateAlbum(role) {
		h.jsonError(w, "Forbidden: недостаточно прав", http.StatusForbidden)
		return
	}

	var req struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}

	if r.Header.Get("Content-Type") == contentTypeJSON {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			h.jsonError(w, "Invalid JSON", http.StatusBadRequest)
			return
		}
	} else {
		req.Name = r.FormValue("name")
		req.Description = r.FormValue("description")
	}

	if req.Name == "" {
		h.jsonError(w, "Name is required", http.StatusBadRequest)
		return
	}

	album := &storage.Album{
		ID:          generateRandomID(),
		Name:        req.Name,
		Description: req.Description,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}

	if err := h.store.SaveAlbum(album); err != nil {
		h.jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	h.jsonResponse(w, album)
}

// UpdateAlbum обновляет альбом
func (h *Handlers) UpdateAlbum(w http.ResponseWriter, r *http.Request) {
	role := auth.GetUserRole(r)
	if !auth.CanEditAlbum(role) {
		h.jsonError(w, "Forbidden: недостаточно прав", http.StatusForbidden)
		return
	}

	id := chi.URLParam(r, "id")

	album, err := h.store.GetAlbum(id)
	if err != nil {
		h.jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if album == nil {
		http.NotFound(w, r)
		return
	}

	var req struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		CoverID     string `json:"cover_id"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.jsonError(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	if req.Name != "" {
		album.Name = req.Name
	}
	if req.Description != "" {
		album.Description = req.Description
	}
	if req.CoverID != "" {
		album.CoverID = req.CoverID
	}
	album.UpdatedAt = time.Now()

	if err := h.store.SaveAlbum(album); err != nil {
		h.jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	h.jsonResponse(w, album)
}

// DeleteAlbum удаляет альбом
func (h *Handlers) DeleteAlbum(w http.ResponseWriter, r *http.Request) {
	role := auth.GetUserRole(r)
	if !auth.CanDeleteAlbum(role) {
		h.jsonError(w, "Forbidden: недостаточно прав", http.StatusForbidden)
		return
	}

	id := chi.URLParam(r, "id")

	if err := h.store.DeleteAlbum(id); err != nil {
		h.jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	h.jsonResponse(w, map[string]string{"status": string(StatusDeleted)})
}

// AddToAlbum добавляет медиа в альбом
func (h *Handlers) AddToAlbum(w http.ResponseWriter, r *http.Request) {
	role := auth.GetUserRole(r)
	if !auth.CanEditAlbum(role) {
		h.jsonError(w, "Forbidden: недостаточно прав", http.StatusForbidden)
		return
	}

	id := chi.URLParam(r, "id")

	var req struct {
		MediaIDs []string `json:"media_ids"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.jsonError(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	if err := h.store.AddMediaToAlbum(id, req.MediaIDs); err != nil {
		h.jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	h.jsonResponse(w, map[string]string{"status": string(StatusAdded)})
}

// RemoveFromAlbum удаляет медиа из альбома
func (h *Handlers) RemoveFromAlbum(w http.ResponseWriter, r *http.Request) {
	role := auth.GetUserRole(r)
	if !auth.CanEditAlbum(role) {
		h.jsonError(w, "Forbidden: недостаточно прав", http.StatusForbidden)
		return
	}

	id := chi.URLParam(r, "id")

	var req struct {
		MediaIDs []string `json:"media_ids"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.jsonError(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	if err := h.store.RemoveMediaFromAlbum(id, req.MediaIDs); err != nil {
		h.jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	h.jsonResponse(w, map[string]string{"status": string(StatusRemoved)})
}
