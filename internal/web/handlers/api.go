package handlers

import "net/http"

// StartScan запускает сканирование
func (h *Handlers) StartScan(w http.ResponseWriter, r *http.Request) {
	if err := h.scanner.Start(); err != nil {
		h.jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}

	h.cache.Clear()

	h.jsonResponse(w, map[string]interface{}{
		"status":  StatusStarted,
		"message": "Сканирование запущено",
	})
}

// ScanProgress возвращает прогресс сканирования
func (h *Handlers) ScanProgress(w http.ResponseWriter, r *http.Request) {
	progress := h.scanner.Progress()
	h.jsonResponse(w, progress)
}

// Stats возвращает статистику галереи
func (h *Handlers) Stats(w http.ResponseWriter, r *http.Request) {
	stats, found := h.cache.GetStats()
	if !found {
		var err error
		stats, err = h.store.GetStats()
		if err != nil {
			h.jsonError(w, err.Error(), http.StatusInternalServerError)
			return
		}
		h.cache.SetStats(stats)
	}

	h.jsonResponse(w, stats)
}

// QueueStats возвращает статистику очереди задач
func (h *Handlers) QueueStats(w http.ResponseWriter, r *http.Request) {
	stats := h.workerPool.Stats()
	h.jsonResponse(w, map[string]interface{}{
		"total_tasks":     stats.TotalTasks,
		"completed_tasks": stats.CompletedTasks,
		"failed_tasks":    stats.FailedTasks,
		"queued_tasks":    stats.QueuedTasks,
		"active_workers":  stats.ActiveWorkers,
		"queue_length":    h.workerPool.QueueLength(),
		"processing":      h.thumbService.ProcessingCount(),
	})
}

// CacheStats возвращает статистику кэша
func (h *Handlers) CacheStats(w http.ResponseWriter, r *http.Request) {
	stats := h.cache.Stats()
	h.jsonResponse(w, stats)
}

// GenerateThumbnails запускает генерацию превью
func (h *Handlers) GenerateThumbnails(w http.ResponseWriter, r *http.Request) {
	if err := h.thumbService.PregenerateThumbnails(); err != nil {
		h.jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	h.jsonResponse(w, map[string]interface{}{
		"status":  StatusStarted,
		"message": "Генерация превью запущена",
	})
}
