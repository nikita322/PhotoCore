package handlers

import (
	"fmt"
	"net/http"
	"os"

	"github.com/go-chi/chi/v5"
	"github.com/photocore/photocore/internal/logger"
	"github.com/photocore/photocore/internal/media"
)

// ViewMedia отображает отдельное медиа
func (h *Handlers) ViewMedia(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	m, found := h.cache.GetMedia(id)
	if !found {
		var err error
		m, err = h.store.GetMedia(id)
		if err != nil || m == nil {
			http.NotFound(w, r)
			return
		}
		h.cache.SetMedia(m)
	}

	isHTMX := r.Header.Get(hxRequestHeader) == hxRequestTrue

	data := h.baseData(r)
	data["Media"] = m
	data["IsHTMX"] = isHTMX

	if isHTMX {
		h.render(w, "viewer_content.html", data)
	} else {
		h.render(w, "viewer.html", data)
	}
}

// ServeMedia отдает оригинальный медиа-файл
func (h *Handlers) ServeMedia(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	m, found := h.cache.GetMedia(id)
	if !found {
		var err error
		m, err = h.store.GetMedia(id)
		if err != nil || m == nil {
			http.NotFound(w, r)
			return
		}
		h.cache.SetMedia(m)
	}

	if _, err := os.Stat(m.Path); os.IsNotExist(err) {
		http.NotFound(w, r)
		return
	}

	w.Header().Set("Content-Type", m.MimeType)
	http.ServeFile(w, r, m.Path)
}

// ServeThumbnail отдает превью
func (h *Handlers) ServeThumbnail(w http.ResponseWriter, r *http.Request) {
	h.serveThumbnailWithSize(w, r, media.ThumbnailSizeSmall)
}

// ServeThumbnailSize отдает превью указанного размера
func (h *Handlers) ServeThumbnailSize(w http.ResponseWriter, r *http.Request) {
	size := chi.URLParam(r, "size")
	if size != string(media.ThumbnailSizeSmall) && size != string(media.ThumbnailSizeMedium) && size != string(media.ThumbnailSizeLarge) {
		size = string(media.ThumbnailSizeSmall)
	}
	h.serveThumbnailWithSize(w, r, media.ThumbnailSize(size))
}

func (h *Handlers) serveThumbnailWithSize(w http.ResponseWriter, r *http.Request, size media.ThumbnailSize) {
	id := chi.URLParam(r, "id")

	m, found := h.cache.GetMedia(id)
	if !found {
		var err error
		m, err = h.store.GetMedia(id)
		if err != nil || m == nil {
			http.NotFound(w, r)
			return
		}
		h.cache.SetMedia(m)
	}

	thumbPath := h.thumbGen.GetThumbnailPath(id, size)
	if _, err := os.Stat(thumbPath); os.IsNotExist(err) {
		if hasFailed, errMsg := h.thumbService.HasFailed(id, size); hasFailed {
			logger.InfoLog.Printf("Thumbnail %s/%s permanently failed: %s", id[:16], size, errMsg)
			w.Header().Set("Cache-Control", "no-store, no-cache, must-revalidate, max-age=0")
			w.Header().Set("X-Thumbnail-Status", "failed")
			w.Header().Set("X-Thumbnail-Error", errMsg)
			http.Error(w, "Thumbnail generation failed: "+errMsg, http.StatusUnprocessableEntity)
			return
		}

		isProcessing := h.thumbService.IsProcessing(id, size)
		if !isProcessing {
			queued := h.thumbService.QueueThumbnail(id, size)
			if queued {
				logger.InfoLog.Printf("Thumbnail missing for %s/%s, queued for generation", id[:16], size)
			} else {
				logger.InfoLog.Printf("WARNING: Failed to queue thumbnail for %s/%s (pool full?)", id[:16], size)
			}
		} else {
			logger.InfoLog.Printf("Thumbnail for %s/%s is already in queue", id[:16], size)
		}

		w.Header().Set("Cache-Control", "no-store, no-cache, must-revalidate, max-age=0")
		w.Header().Set("Retry-After", "2")
		w.Header().Set("X-Thumbnail-Status", "processing")
		http.Error(w, "Thumbnail is being generated", http.StatusServiceUnavailable)
		return
	}

	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Cache-Control", fmt.Sprintf("public, max-age=%d", cacheControlDay))
	http.ServeFile(w, r, thumbPath)
}
