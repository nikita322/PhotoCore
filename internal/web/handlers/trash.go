package handlers

import (
	"net/http"
	"os"
	"sort"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/photocore/photocore/internal/auth"
	"github.com/photocore/photocore/internal/logger"
	"github.com/photocore/photocore/internal/storage"
)

// TrashPage отображает страницу корзины
func (h *Handlers) TrashPage(w http.ResponseWriter, r *http.Request) {
	role := auth.GetUserRole(r)
	if !auth.CanEdit(role) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	trashMedia, err := h.store.ListTrashMedia()
	if err != nil {
		http.Error(w, "Internal error", http.StatusInternalServerError)
		return
	}

	sort.Slice(trashMedia, func(i, j int) bool {
		if trashMedia[i].DeletedAt == nil || trashMedia[j].DeletedAt == nil {
			return false
		}
		return trashMedia[i].DeletedAt.After(*trashMedia[j].DeletedAt)
	})

	type TrashItem struct {
		*storage.Media
		DaysRemaining  int
		DeletedDaysAgo int
	}

	var items []TrashItem
	for _, m := range trashMedia {
		daysAgo := 0
		remaining := storage.TrashRetentionDays
		if m.DeletedAt != nil {
			daysAgo = int(time.Since(*m.DeletedAt).Hours() / 24)
			remaining = storage.TrashRetentionDays - daysAgo
			if remaining < 0 {
				remaining = 0
			}
		}
		items = append(items, TrashItem{
			Media:          m,
			DaysRemaining:  remaining,
			DeletedDaysAgo: daysAgo,
		})
	}

	data := h.baseData(r)
	data["TrashItems"] = items
	data["TrashCount"] = len(items)
	h.render(w, "trash.html", data)
}

// MoveToTrash перемещает медиа в корзину (soft delete)
func (h *Handlers) MoveToTrash(w http.ResponseWriter, r *http.Request) {
	role := auth.GetUserRole(r)
	if !auth.CanDeleteMedia(role) {
		h.jsonError(w, "Forbidden: недостаточно прав", http.StatusForbidden)
		return
	}

	id := chi.URLParam(r, "id")

	if err := h.store.SoftDeleteMedia(id); err != nil {
		h.jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	h.cache.DeleteMedia(id)
	h.cache.Clear()

	h.jsonResponse(w, map[string]interface{}{
		"status":  StatusMovedToTrash,
		"message": "Файл перемещён в корзину",
	})
}

// RestoreFromTrash восстанавливает медиа из корзины
func (h *Handlers) RestoreFromTrash(w http.ResponseWriter, r *http.Request) {
	role := auth.GetUserRole(r)
	if !auth.CanEdit(role) {
		h.jsonError(w, "Forbidden: недостаточно прав", http.StatusForbidden)
		return
	}

	id := chi.URLParam(r, "id")

	if err := h.store.RestoreMedia(id); err != nil {
		h.jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	h.cache.DeleteMedia(id)
	h.cache.Clear()

	h.jsonResponse(w, map[string]interface{}{
		"status":  StatusRestored,
		"message": "Файл восстановлен",
	})
}

// PermanentDelete окончательно удаляет медиа
func (h *Handlers) PermanentDelete(w http.ResponseWriter, r *http.Request) {
	role := auth.GetUserRole(r)
	if !auth.CanDeleteMedia(role) {
		h.jsonError(w, "Forbidden: недостаточно прав", http.StatusForbidden)
		return
	}

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

	if err := os.Remove(media.Path); err != nil && !os.IsNotExist(err) {
		logger.InfoLog.Printf("Warning: failed to delete file %s: %v", media.Path, err)
	}

	h.thumbGen.DeleteThumbnails(id)

	if err := h.store.DeleteMedia(id); err != nil {
		h.jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	h.cache.DeleteMedia(id)
	h.cache.Clear()

	h.jsonResponse(w, map[string]interface{}{
		"status":  StatusDeleted,
		"message": "Файл удалён окончательно",
	})
}

// EmptyTrash очищает всю корзину
func (h *Handlers) EmptyTrash(w http.ResponseWriter, r *http.Request) {
	role := auth.GetUserRole(r)
	if !auth.CanDeleteMedia(role) {
		h.jsonError(w, "Forbidden: недостаточно прав", http.StatusForbidden)
		return
	}

	trashMedia, err := h.store.ListTrashMedia()
	if err != nil {
		h.jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	var deleted int
	for _, m := range trashMedia {
		if err := os.Remove(m.Path); err != nil && !os.IsNotExist(err) {
			logger.InfoLog.Printf("Warning: failed to delete file %s: %v", m.Path, err)
		}

		h.thumbGen.DeleteThumbnails(m.ID)

		if err := h.store.DeleteMedia(m.ID); err != nil {
			logger.InfoLog.Printf("Error deleting media %s: %v", m.ID, err)
			continue
		}
		deleted++
	}

	h.cache.Clear()

	h.jsonResponse(w, map[string]interface{}{
		"status":  StatusEmptied,
		"deleted": deleted,
		"message": "Корзина очищена",
	})
}

// TrashStats возвращает статистику корзины
func (h *Handlers) TrashStats(w http.ResponseWriter, r *http.Request) {
	count, totalSize, err := h.store.GetTrashStats()
	if err != nil {
		h.jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	h.jsonResponse(w, map[string]interface{}{
		"count":      count,
		"total_size": totalSize,
	})
}
