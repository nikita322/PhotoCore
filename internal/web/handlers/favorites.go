package handlers

import (
	"net/http"
	"sort"

	"github.com/go-chi/chi/v5"
	"github.com/photocore/photocore/internal/auth"
)

// ToggleFavorite переключает статус избранного для текущего пользователя
func (h *Handlers) ToggleFavorite(w http.ResponseWriter, r *http.Request) {
	userID := auth.GetUserID(r)
	if userID == "" {
		h.jsonError(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	id := chi.URLParam(r, "id")

	isFavorite, err := h.store.ToggleUserFavorite(userID, id)
	if err != nil {
		h.jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	h.jsonResponse(w, map[string]interface{}{
		"id":          id,
		"is_favorite": isFavorite,
	})
}

// ListFavorites возвращает избранные медиа текущего пользователя
func (h *Handlers) ListFavorites(w http.ResponseWriter, r *http.Request) {
	userID := auth.GetUserID(r)
	if userID == "" {
		h.jsonError(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	media, err := h.store.ListUserFavorites(userID)
	if err != nil {
		h.jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	sort.Slice(media, func(i, j int) bool {
		return media[i].TakenAt.After(media[j].TakenAt)
	})

	if h.wantsHTML(r) {
		data := h.baseData(r)
		data["Media"] = media
		h.render(w, "favorites.html", data)
		return
	}

	h.jsonResponse(w, media)
}
