package handlers

import "net/http"

// MapPage отображает страницу карты
func (h *Handlers) MapPage(w http.ResponseWriter, r *http.Request) {
	h.render(w, "map.html", h.baseData(r))
}

// GeoPoints возвращает точки с GPS координатами
func (h *Handlers) GeoPoints(w http.ResponseWriter, r *http.Request) {
	points, err := h.store.GetGeoPoints()
	if err != nil {
		h.jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	h.jsonResponse(w, points)
}
