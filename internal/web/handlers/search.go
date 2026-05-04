package handlers

import (
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/photocore/photocore/internal/storage"
)

// Search выполняет поиск медиа
func (h *Handlers) Search(w http.ResponseWriter, r *http.Request) {
	query := &storage.SearchQuery{
		Text:   r.URL.Query().Get("q"),
		Camera: r.URL.Query().Get("camera"),
	}

	if t := r.URL.Query().Get("type"); t != "" {
		query.Type = storage.MediaType(t)
	}

	if from := r.URL.Query().Get("from"); from != "" {
		if t, err := time.Parse(time.DateOnly, from); err == nil {
			query.DateFrom = &t
		}
	}
	if to := r.URL.Query().Get("to"); to != "" {
		if t, err := time.Parse(time.DateOnly, to); err == nil {
			query.DateTo = &t
		}
	}

	if tags := r.URL.Query().Get("tags"); tags != "" {
		query.Tags = strings.Split(tags, ",")
	}

	if fav := r.URL.Query().Get("favorite"); fav == hxRequestTrue {
		t := true
		query.IsFavorite = &t
	}

	if gps := r.URL.Query().Get("gps"); gps == hxRequestTrue {
		t := true
		query.HasGPS = &t
	}

	if limit := r.URL.Query().Get("limit"); limit != "" {
		if l, err := strconv.Atoi(limit); err == nil {
			query.Limit = l
		}
	}
	if offset := r.URL.Query().Get("offset"); offset != "" {
		if o, err := strconv.Atoi(offset); err == nil {
			query.Offset = o
		}
	}

	result, err := h.store.Search(query)
	if err != nil {
		h.jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	isHTMX := r.Header.Get(hxRequestHeader) == hxRequestTrue
	if isHTMX {
		h.renderPartial(w, "search_results.html", map[string]interface{}{
			"Media":      result.Media,
			"TotalCount": result.TotalCount,
			"HasMore":    result.HasMore,
			"Query":      query.Text,
		})
		return
	}

	h.jsonResponse(w, result)
}

// SearchPage отображает страницу поиска
func (h *Handlers) SearchPage(w http.ResponseWriter, r *http.Request) {
	tags, _ := h.store.ListAllTags()

	allMedia, _ := h.store.ListAllMedia()
	cameras := make(map[string]bool)
	for _, m := range allMedia {
		if m.Metadata.Camera != "" {
			cameras[m.Metadata.Camera] = true
		}
	}
	var cameraList []string
	for c := range cameras {
		cameraList = append(cameraList, c)
	}
	sort.Strings(cameraList)

	data := h.baseData(r)
	data["Tags"] = tags
	data["Cameras"] = cameraList
	h.render(w, "search.html", data)
}
