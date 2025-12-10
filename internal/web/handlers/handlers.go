package handlers

import (
	"archive/zip"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"html/template"
	"io"
	"log"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/photocore/photocore/internal/auth"
	"github.com/photocore/photocore/internal/cache"
	"github.com/photocore/photocore/internal/config"
	"github.com/photocore/photocore/internal/media"
	"github.com/photocore/photocore/internal/scanner"
	"github.com/photocore/photocore/internal/storage"
	"github.com/photocore/photocore/internal/worker"
)

// Handlers содержит все HTTP-обработчики
type Handlers struct {
	cfg          *config.Config
	store        *storage.Store
	scanner      *scanner.Scanner
	thumbGen     *media.ThumbnailGenerator
	auth         *auth.Auth
	templates    *template.Template
	cache        *cache.MediaCache
	workerPool   *worker.Pool
	thumbService *worker.ThumbnailService
}

// NewHandlers создает новый экземпляр обработчиков
func NewHandlers(
	cfg *config.Config,
	store *storage.Store,
	scanner *scanner.Scanner,
	thumbGen *media.ThumbnailGenerator,
	auth *auth.Auth,
	templates *template.Template,
	mediaCache *cache.MediaCache,
	workerPool *worker.Pool,
	thumbService *worker.ThumbnailService,
) *Handlers {
	return &Handlers{
		cfg:          cfg,
		store:        store,
		scanner:      scanner,
		thumbGen:     thumbGen,
		auth:         auth,
		templates:    templates,
		cache:        mediaCache,
		workerPool:   workerPool,
		thumbService: thumbService,
	}
}

// === Страницы ===

// Index перенаправляет на галерею
func (h *Handlers) Index(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, "/gallery", http.StatusFound)
}

// LoginPage отображает страницу входа
func (h *Handlers) LoginPage(w http.ResponseWriter, r *http.Request) {
	h.render(w, "login.html", nil)
}

// Login обрабатывает вход пользователя
func (h *Handlers) Login(w http.ResponseWriter, r *http.Request) {
	username := r.FormValue("username")
	password := r.FormValue("password")

	session, err := h.auth.Login(username, password)
	if err != nil {
		h.render(w, "login.html", map[string]interface{}{
			"Error": "Неверное имя пользователя или пароль",
		})
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name:     "session",
		Value:    session.ID,
		Path:     "/",
		MaxAge:   h.cfg.Auth.SessionMaxAge,
		HttpOnly: true,
		Secure:   r.TLS != nil,
		SameSite: http.SameSiteLaxMode,
	})

	http.Redirect(w, r, "/gallery", http.StatusFound)
}

// Logout выполняет выход пользователя
func (h *Handlers) Logout(w http.ResponseWriter, r *http.Request) {
	cookie, err := r.Cookie("session")
	if err == nil {
		h.auth.Logout(cookie.Value)
	}

	http.SetCookie(w, &http.Cookie{
		Name:   "session",
		Value:  "",
		Path:   "/",
		MaxAge: -1,
	})

	http.Redirect(w, r, "/login", http.StatusFound)
}

// ViewMedia отображает отдельное медиа
func (h *Handlers) ViewMedia(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	// Пробуем кэш
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

	isHTMX := r.Header.Get("HX-Request") == "true"

	data := map[string]interface{}{
		"Media":  m,
		"IsHTMX": isHTMX,
	}

	if isHTMX {
		h.render(w, "viewer_content.html", data)
	} else {
		h.render(w, "viewer.html", data)
	}
}

// === Медиа ===

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
	h.serveThumbnailWithSize(w, r, "small")
}

// ServeThumbnailSize отдает превью указанного размера
func (h *Handlers) ServeThumbnailSize(w http.ResponseWriter, r *http.Request) {
	size := chi.URLParam(r, "size")
	if size != "small" && size != "medium" && size != "large" {
		size = "small"
	}
	h.serveThumbnailWithSize(w, r, size)
}

func (h *Handlers) serveThumbnailWithSize(w http.ResponseWriter, r *http.Request, size string) {
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

	// Проверяем, есть ли превью
	thumbPath := h.thumbGen.GetThumbnailPath(id, size)
	if _, err := os.Stat(thumbPath); os.IsNotExist(err) {
		// Превью нет - ставим в очередь и возвращаем placeholder
		if !h.thumbService.IsProcessing(id, size) {
			h.thumbService.QueueThumbnail(id, size)
		}

		w.Header().Set("Content-Type", "image/svg+xml")
		w.Header().Set("Cache-Control", "no-cache")
		w.Write([]byte(placeholderSVG))
		return
	}

	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Cache-Control", "public, max-age=86400")
	http.ServeFile(w, r, thumbPath)
}

// === API ===

// StartScan запускает сканирование
func (h *Handlers) StartScan(w http.ResponseWriter, r *http.Request) {
	if err := h.scanner.Start(); err != nil {
		h.jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}

	// Инвалидируем кэш
	h.cache.Clear()

	h.jsonResponse(w, map[string]interface{}{
		"status":  "started",
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
	// Пробуем кэш
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
		"status":  "started",
		"message": "Генерация превью запущена",
	})
}

// === Поиск ===

// Search выполняет поиск медиа
func (h *Handlers) Search(w http.ResponseWriter, r *http.Request) {
	query := &storage.SearchQuery{
		Text:   r.URL.Query().Get("q"),
		Camera: r.URL.Query().Get("camera"),
	}

	// Тип медиа
	if t := r.URL.Query().Get("type"); t != "" {
		query.Type = storage.MediaType(t)
	}

	// Даты
	if from := r.URL.Query().Get("from"); from != "" {
		if t, err := time.Parse("2006-01-02", from); err == nil {
			query.DateFrom = &t
		}
	}
	if to := r.URL.Query().Get("to"); to != "" {
		if t, err := time.Parse("2006-01-02", to); err == nil {
			query.DateTo = &t
		}
	}

	// Теги
	if tags := r.URL.Query().Get("tags"); tags != "" {
		query.Tags = strings.Split(tags, ",")
	}

	// Избранное
	if fav := r.URL.Query().Get("favorite"); fav == "true" {
		t := true
		query.IsFavorite = &t
	}

	// GPS
	if gps := r.URL.Query().Get("gps"); gps == "true" {
		t := true
		query.HasGPS = &t
	}

	// Пагинация
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

	// Проверяем, запрашивается ли HTML или JSON
	isHTMX := r.Header.Get("HX-Request") == "true"
	if isHTMX {
		h.render(w, "search_results.html", map[string]interface{}{
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
	// Получаем все теги для фильтров
	tags, _ := h.store.ListAllTags()

	// Получаем уникальные камеры
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

	h.render(w, "search.html", map[string]interface{}{
		"Tags":    tags,
		"Cameras": cameraList,
	})
}

// === Альбомы ===

// ListAlbums возвращает список альбомов
func (h *Handlers) ListAlbums(w http.ResponseWriter, r *http.Request) {
	albums, err := h.store.ListAlbums()
	if err != nil {
		h.jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Для браузерных запросов рендерим HTML
	if h.wantsHTML(r) {
		h.render(w, "albums.html", map[string]interface{}{
			"Albums": albums,
		})
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
		h.render(w, "album.html", map[string]interface{}{
			"Album": album,
			"Media": media,
		})
		return
	}

	h.jsonResponse(w, map[string]interface{}{
		"album": album,
		"media": media,
	})
}

// CreateAlbum создает новый альбом
func (h *Handlers) CreateAlbum(w http.ResponseWriter, r *http.Request) {
	// Проверка прав: только admin и editor
	role := auth.GetUserRole(r)
	if !auth.CanCreateAlbum(role) {
		h.jsonError(w, "Forbidden: недостаточно прав", http.StatusForbidden)
		return
	}

	var req struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}

	if r.Header.Get("Content-Type") == "application/json" {
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
		ID:          generateID(),
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
	// Проверка прав: только admin и editor
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
	// Проверка прав: только admin
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

	h.jsonResponse(w, map[string]string{"status": "deleted"})
}

// AddToAlbum добавляет медиа в альбом
func (h *Handlers) AddToAlbum(w http.ResponseWriter, r *http.Request) {
	// Проверка прав: только admin и editor
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

	h.jsonResponse(w, map[string]string{"status": "added"})
}

// RemoveFromAlbum удаляет медиа из альбома
func (h *Handlers) RemoveFromAlbum(w http.ResponseWriter, r *http.Request) {
	// Проверка прав: только admin и editor
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

	h.jsonResponse(w, map[string]string{"status": "removed"})
}

// === Избранное (per-user) ===

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

	// Сортируем по дате
	sort.Slice(media, func(i, j int) bool {
		return media[i].TakenAt.After(media[j].TakenAt)
	})

	if h.wantsHTML(r) {
		h.render(w, "favorites.html", map[string]interface{}{
			"Media": media,
		})
		return
	}

	h.jsonResponse(w, media)
}

// === Теги ===

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
	// Проверка прав: только admin и editor
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

	// Инвалидируем кэш
	h.cache.DeleteMedia(id)

	h.jsonResponse(w, map[string]string{"status": "added"})
}

// RemoveTags удаляет теги с медиа
func (h *Handlers) RemoveTags(w http.ResponseWriter, r *http.Request) {
	// Проверка прав: только admin
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

	// Инвалидируем кэш
	h.cache.DeleteMedia(id)

	h.jsonResponse(w, map[string]string{"status": "removed"})
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
		h.render(w, "gallery_content.html", map[string]interface{}{
			"Media": media,
			"Tag":   tag,
		})
		return
	}

	h.jsonResponse(w, media)
}

// === Timeline ===

// Timeline возвращает группировку медиа по датам
func (h *Handlers) Timeline(w http.ResponseWriter, r *http.Request) {
	timeline, err := h.store.GetTimeline()
	if err != nil {
		h.jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if h.wantsHTML(r) {
		session := auth.GetSession(r)
		data := map[string]interface{}{
			"Timeline": timeline,
		}
		if session != nil {
			data["Username"] = session.Username
			data["Role"] = session.Role
			data["IsAdmin"] = session.Role == storage.RoleAdmin
			data["CanEdit"] = session.Role == storage.RoleAdmin || session.Role == storage.RoleEditor
		}
		h.render(w, "timeline.html", data)
		return
	}

	h.jsonResponse(w, timeline)
}

// TimelineMedia возвращает медиа за определенный период
func (h *Handlers) TimelineMedia(w http.ResponseWriter, r *http.Request) {
	period := chi.URLParam(r, "period")

	media, err := h.store.GetTimelineMedia(period)
	if err != nil {
		h.jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Сортируем по дате
	sort.Slice(media, func(i, j int) bool {
		return media[i].TakenAt.After(media[j].TakenAt)
	})

	if h.wantsHTML(r) {
		h.render(w, "gallery_content.html", map[string]interface{}{
			"Media":  media,
			"Period": period,
		})
		return
	}

	h.jsonResponse(w, media)
}

// TimelineAllMedia возвращает все медиа сгруппированные по периодам
func (h *Handlers) TimelineAllMedia(w http.ResponseWriter, r *http.Request) {
	allMedia, err := h.store.ListAllMedia()
	if err != nil {
		h.jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Сортируем по дате (новые первые)
	sort.Slice(allMedia, func(i, j int) bool {
		dateI := allMedia[i].TakenAt
		if dateI.IsZero() || dateI.Year() < 1900 {
			dateI = allMedia[i].ModifiedAt
		}
		dateJ := allMedia[j].TakenAt
		if dateJ.IsZero() || dateJ.Year() < 1900 {
			dateJ = allMedia[j].ModifiedAt
		}
		return dateI.After(dateJ)
	})

	// Группируем по периодам
	type MediaGroup struct {
		Period string
		Label  string
		Media  []*storage.Media
	}

	months := map[string]string{
		"01": "Январь", "02": "Февраль", "03": "Март",
		"04": "Апрель", "05": "Май", "06": "Июнь",
		"07": "Июль", "08": "Август", "09": "Сентябрь",
		"10": "Октябрь", "11": "Ноябрь", "12": "Декабрь",
	}

	var groups []MediaGroup
	var currentPeriod string
	var currentGroup *MediaGroup

	for _, m := range allMedia {
		var date string
		if !m.TakenAt.IsZero() && m.TakenAt.Year() > 1900 {
			date = m.TakenAt.Format("2006-01")
		} else {
			date = m.ModifiedAt.Format("2006-01")
		}

		if date != currentPeriod {
			if currentGroup != nil {
				groups = append(groups, *currentGroup)
			}
			// Форматируем label
			label := date
			if len(date) >= 7 {
				year := date[:4]
				month := date[5:7]
				if monthName, ok := months[month]; ok {
					label = monthName + " " + year
				}
			}
			currentGroup = &MediaGroup{Period: date, Label: label, Media: []*storage.Media{}}
			currentPeriod = date
		}
		currentGroup.Media = append(currentGroup.Media, m)
	}
	if currentGroup != nil && len(currentGroup.Media) > 0 {
		groups = append(groups, *currentGroup)
	}

	if h.wantsHTML(r) {
		h.render(w, "timeline_all.html", map[string]interface{}{
			"Groups": groups,
			"Total":  len(allMedia),
		})
		return
	}

	h.jsonResponse(w, groups)
}

// === Карта ===

// MapPage отображает страницу карты
func (h *Handlers) MapPage(w http.ResponseWriter, r *http.Request) {
	h.render(w, "map.html", nil)
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

// === Bulk операции ===

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

	// Инвалидируем кэш
	for _, id := range req.MediaIDs {
		h.cache.DeleteMedia(id)
	}

	h.jsonResponse(w, map[string]interface{}{
		"status": "updated",
		"count":  len(req.MediaIDs),
	})
}

// BulkAddTags добавляет теги к нескольким медиа
func (h *Handlers) BulkAddTags(w http.ResponseWriter, r *http.Request) {
	// Проверка прав: только admin и editor
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

	// Инвалидируем кэш
	for _, id := range req.MediaIDs {
		h.cache.DeleteMedia(id)
	}

	h.jsonResponse(w, map[string]interface{}{
		"status": "updated",
		"count":  len(req.MediaIDs),
	})
}

// BulkAddToAlbum добавляет медиа в альбом
func (h *Handlers) BulkAddToAlbum(w http.ResponseWriter, r *http.Request) {
	// Проверка прав: только admin и editor
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
		"status": "added",
		"count":  len(req.MediaIDs),
	})
}

// BulkDelete удаляет несколько медиа
func (h *Handlers) BulkDelete(w http.ResponseWriter, r *http.Request) {
	// Проверка прав: только admin
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

	// Получаем информацию о медиа для удаления файлов превью
	for _, id := range req.MediaIDs {
		h.cache.DeleteMedia(id)
	}

	if err := h.store.BulkDelete(req.MediaIDs); err != nil {
		h.jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Инвалидируем весь кэш
	h.cache.Clear()

	h.jsonResponse(w, map[string]interface{}{
		"status": "deleted",
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
	w.Header().Set("Content-Disposition", "attachment; filename=\"photos.zip\"")

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

// === Admin ===

// AdminPage отображает страницу администрирования
func (h *Handlers) AdminPage(w http.ResponseWriter, r *http.Request) {
	role := auth.GetUserRole(r)
	if !auth.CanManageUsers(role) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	users, err := h.store.ListUsers()
	if err != nil {
		http.Error(w, "Internal error", http.StatusInternalServerError)
		return
	}

	h.render(w, "admin.html", map[string]interface{}{
		"Users": users,
	})
}

// ListUsers возвращает список пользователей (API)
func (h *Handlers) ListUsers(w http.ResponseWriter, r *http.Request) {
	role := auth.GetUserRole(r)
	if !auth.CanManageUsers(role) {
		h.jsonError(w, "Forbidden", http.StatusForbidden)
		return
	}

	users, err := h.store.ListUsers()
	if err != nil {
		h.jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Убираем хеши паролей из ответа
	type safeUser struct {
		ID          string `json:"id"`
		Username    string `json:"username"`
		DisplayName string `json:"display_name"`
		Role        string `json:"role"`
		CreatedAt   string `json:"created_at"`
		LastLogin   string `json:"last_login"`
	}
	var safeUsers []safeUser
	for _, u := range users {
		safeUsers = append(safeUsers, safeUser{
			ID:          u.ID,
			Username:    u.Username,
			DisplayName: u.DisplayName,
			Role:        u.Role,
			CreatedAt:   u.CreatedAt.Format("2006-01-02 15:04"),
			LastLogin:   u.LastLogin.Format("2006-01-02 15:04"),
		})
	}

	h.jsonResponse(w, safeUsers)
}

// CreateUser создает нового пользователя
func (h *Handlers) CreateUser(w http.ResponseWriter, r *http.Request) {
	role := auth.GetUserRole(r)
	if !auth.CanManageUsers(role) {
		h.jsonError(w, "Forbidden", http.StatusForbidden)
		return
	}

	var req struct {
		Username    string `json:"username"`
		DisplayName string `json:"display_name"`
		Password    string `json:"password"`
		Role        string `json:"role"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.jsonError(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	if req.Username == "" || req.Password == "" {
		h.jsonError(w, "Username and password are required", http.StatusBadRequest)
		return
	}

	// Проверяем валидность роли
	if req.Role != storage.RoleAdmin && req.Role != storage.RoleEditor && req.Role != storage.RoleViewer {
		req.Role = storage.RoleViewer
	}

	// Проверяем, что пользователь не существует
	existing, _ := h.store.GetUser(req.Username)
	if existing != nil {
		h.jsonError(w, "User already exists", http.StatusConflict)
		return
	}

	// Хешируем пароль
	hash, err := h.auth.HashPassword(req.Password)
	if err != nil {
		h.jsonError(w, "Failed to hash password", http.StatusInternalServerError)
		return
	}

	user := &storage.User{
		ID:           generateID(),
		Username:     req.Username,
		DisplayName:  req.DisplayName,
		PasswordHash: hash,
		Role:         req.Role,
		CreatedAt:    time.Now(),
	}

	if user.DisplayName == "" {
		user.DisplayName = user.Username
	}

	if err := h.store.SaveUser(user); err != nil {
		h.jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	h.jsonResponse(w, map[string]interface{}{
		"id":       user.ID,
		"username": user.Username,
		"role":     user.Role,
	})
}

// UpdateUser обновляет пользователя
func (h *Handlers) UpdateUser(w http.ResponseWriter, r *http.Request) {
	role := auth.GetUserRole(r)
	if !auth.CanManageUsers(role) {
		h.jsonError(w, "Forbidden", http.StatusForbidden)
		return
	}

	username := chi.URLParam(r, "username")

	user, err := h.store.GetUser(username)
	if err != nil {
		h.jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if user == nil {
		h.jsonError(w, "User not found", http.StatusNotFound)
		return
	}

	var req struct {
		DisplayName string `json:"display_name"`
		Password    string `json:"password"`
		Role        string `json:"role"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.jsonError(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	if req.DisplayName != "" {
		user.DisplayName = req.DisplayName
	}

	if req.Role != "" {
		if req.Role == storage.RoleAdmin || req.Role == storage.RoleEditor || req.Role == storage.RoleViewer {
			user.Role = req.Role
		}
	}

	if req.Password != "" {
		hash, err := h.auth.HashPassword(req.Password)
		if err != nil {
			h.jsonError(w, "Failed to hash password", http.StatusInternalServerError)
			return
		}
		user.PasswordHash = hash
	}

	if err := h.store.SaveUser(user); err != nil {
		h.jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	h.jsonResponse(w, map[string]string{"status": "updated"})
}

// DeleteUser удаляет пользователя
func (h *Handlers) DeleteUser(w http.ResponseWriter, r *http.Request) {
	role := auth.GetUserRole(r)
	if !auth.CanManageUsers(role) {
		h.jsonError(w, "Forbidden", http.StatusForbidden)
		return
	}

	username := chi.URLParam(r, "username")

	// Не позволяем удалить себя
	session := auth.GetSession(r)
	if session != nil && session.Username == username {
		h.jsonError(w, "Cannot delete yourself", http.StatusBadRequest)
		return
	}

	if err := h.store.DeleteUser(username); err != nil {
		h.jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	h.jsonResponse(w, map[string]string{"status": "deleted"})
}

// === Helpers ===

// wantsHTML проверяет, запрашивает ли клиент HTML (браузер или HTMX)
func (h *Handlers) wantsHTML(r *http.Request) bool {
	// HTMX запросы всегда хотят HTML
	if r.Header.Get("HX-Request") == "true" {
		return true
	}
	// Проверяем Accept header (браузеры отправляют text/html в начале)
	accept := r.Header.Get("Accept")
	return strings.Contains(accept, "text/html")
}

func (h *Handlers) render(w http.ResponseWriter, name string, data interface{}) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := h.templates.ExecuteTemplate(w, name, data); err != nil {
		log.Printf("Template error: %v", err)
		http.Error(w, "Internal error", http.StatusInternalServerError)
	}
}

func (h *Handlers) jsonResponse(w http.ResponseWriter, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(data)
}

func (h *Handlers) jsonError(w http.ResponseWriter, message string, code int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": message})
}

const placeholderSVG = `<svg xmlns="http://www.w3.org/2000/svg" width="300" height="300" viewBox="0 0 300 300">
<rect fill="#1a1a1a" width="300" height="300"/>
<g fill="#333" transform="translate(100,100)">
<rect x="20" y="20" width="60" height="60" rx="5">
<animate attributeName="opacity" values="1;0.5;1" dur="1s" repeatCount="indefinite"/>
</rect>
</g>
<text x="150" y="200" fill="#666" text-anchor="middle" font-family="system-ui" font-size="12">Загрузка...</text>
</svg>`

func generateID() string {
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b)
}
