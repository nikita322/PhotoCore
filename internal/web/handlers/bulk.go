package handlers

import (
	"archive/zip"
	"encoding/json"
	"io"
	"net/http"
	"os"

	"github.com/photocore/photocore/internal/auth"
	"github.com/photocore/photocore/internal/logger"
)

// BulkFavorite устанавливает избранное для нескольких медиа
func (h *Handlers) BulkFavorite(w http.ResponseWriter, r *http.Request) {
	var req struct {
		MediaIDs   []string `json:"media_ids"`
		IsFavorite bool     `json:"is_favorite"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.jsonError(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	if err := h.store.BulkSetFavorite(req.MediaIDs, req.IsFavorite); err != nil {
		h.jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	for _, id := range req.MediaIDs {
		h.cache.DeleteMedia(id)
	}

	h.jsonResponse(w, map[string]interface{}{
		"status": StatusUpdated,
		"count":  len(req.MediaIDs),
	})
}

// BulkAddTags добавляет теги к нескольким медиа
func (h *Handlers) BulkAddTags(w http.ResponseWriter, r *http.Request) {
	role := auth.GetUserRole(r)
	if !auth.CanManageTags(role) {
		h.jsonError(w, "Forbidden: недостаточно прав", http.StatusForbidden)
		return
	}

	var req struct {
		MediaIDs []string `json:"media_ids"`
		Tags     []string `json:"tags"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.jsonError(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	if err := h.store.BulkAddTags(req.MediaIDs, req.Tags); err != nil {
		h.jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	for _, id := range req.MediaIDs {
		h.cache.DeleteMedia(id)
	}

	h.jsonResponse(w, map[string]interface{}{
		"status": StatusUpdated,
		"count":  len(req.MediaIDs),
	})
}

// BulkAddToAlbum добавляет медиа в альбом
func (h *Handlers) BulkAddToAlbum(w http.ResponseWriter, r *http.Request) {
	role := auth.GetUserRole(r)
	if !auth.CanEditAlbum(role) {
		h.jsonError(w, "Forbidden: недостаточно прав", http.StatusForbidden)
		return
	}

	var req struct {
		MediaIDs []string `json:"media_ids"`
		AlbumID  string   `json:"album_id"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.jsonError(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	if err := h.store.AddMediaToAlbum(req.AlbumID, req.MediaIDs); err != nil {
		h.jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	h.jsonResponse(w, map[string]interface{}{
		"status": StatusAdded,
		"count":  len(req.MediaIDs),
	})
}

// BulkDelete удаляет несколько медиа
func (h *Handlers) BulkDelete(w http.ResponseWriter, r *http.Request) {
	role := auth.GetUserRole(r)
	if !auth.CanDeleteMedia(role) {
		h.jsonError(w, "Forbidden: недостаточно прав", http.StatusForbidden)
		return
	}

	var req struct {
		MediaIDs []string `json:"media_ids"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.jsonError(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	for _, id := range req.MediaIDs {
		h.cache.DeleteMedia(id)
		h.thumbGen.DeleteThumbnails(id)
	}

	if err := h.store.BulkDelete(req.MediaIDs); err != nil {
		h.jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	h.cache.Clear()

	h.jsonResponse(w, map[string]interface{}{
		"status": StatusDeleted,
		"count":  len(req.MediaIDs),
	})
}

// BulkDownload скачивает несколько файлов в ZIP
func (h *Handlers) BulkDownload(w http.ResponseWriter, r *http.Request) {
	var req struct {
		MediaIDs []string `json:"media_ids"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.jsonError(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	media, err := h.store.GetMediaByIDs(req.MediaIDs)
	if err != nil {
		h.jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", contentDisposition)

	zipWriter := zip.NewWriter(w)
	defer zipWriter.Close()

	for _, m := range media {
		file, err := os.Open(m.Path)
		if err != nil {
			continue
		}

		writer, err := zipWriter.Create(m.Filename)
		if err != nil {
			file.Close()
			continue
		}

		io.Copy(writer, file)
		file.Close()
	}
}

// BulkMoveToTrash перемещает несколько медиа в корзину
func (h *Handlers) BulkMoveToTrash(w http.ResponseWriter, r *http.Request) {
	role := auth.GetUserRole(r)
	if !auth.CanDeleteMedia(role) {
		h.jsonError(w, "Forbidden: недостаточно прав", http.StatusForbidden)
		return
	}

	var req struct {
		MediaIDs []string `json:"media_ids"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.jsonError(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	var moved int
	for _, id := range req.MediaIDs {
		if err := h.store.SoftDeleteMedia(id); err != nil {
			logger.InfoLog.Printf("Error moving media %s to trash: %v", id, err)
			continue
		}
		h.cache.DeleteMedia(id)
		moved++
	}

	h.cache.Clear()

	h.jsonResponse(w, map[string]interface{}{
		"status": StatusMovedToTrash,
		"count":  moved,
	})
}

// BulkRestore восстанавливает несколько медиа из корзины
func (h *Handlers) BulkRestore(w http.ResponseWriter, r *http.Request) {
	role := auth.GetUserRole(r)
	if !auth.CanEdit(role) {
		h.jsonError(w, "Forbidden: недостаточно прав", http.StatusForbidden)
		return
	}

	var req struct {
		MediaIDs []string `json:"media_ids"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.jsonError(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	var restored int
	for _, id := range req.MediaIDs {
		if err := h.store.RestoreMedia(id); err != nil {
			logger.InfoLog.Printf("Error restoring media %s: %v", id, err)
			continue
		}
		h.cache.DeleteMedia(id)
		restored++
	}

	h.cache.Clear()

	h.jsonResponse(w, map[string]interface{}{
		"status": StatusRestored,
		"count":  restored,
	})
}
