package handlers

import (
	"archive/zip"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/photocore/photocore/internal/auth"
	"github.com/photocore/photocore/internal/cache"
	"github.com/photocore/photocore/internal/config"
	"github.com/photocore/photocore/internal/logger"
	"github.com/photocore/photocore/internal/media"
	"github.com/photocore/photocore/internal/scanner"
	"github.com/photocore/photocore/internal/storage"
	"github.com/photocore/photocore/internal/worker"
)

// ResponseStatus представляет статус JSON-ответа
type ResponseStatus string

const (
	StatusStarted      ResponseStatus = "started"
	StatusDeleted      ResponseStatus = "deleted"
	StatusAdded        ResponseStatus = "added"
	StatusRemoved      ResponseStatus = "removed"
	StatusUpdated      ResponseStatus = "updated"
	StatusRestored     ResponseStatus = "restored"
	StatusMovedToTrash ResponseStatus = "moved_to_trash"
	StatusEmptied      ResponseStatus = "emptied"
	StatusReplaced     ResponseStatus = "replaced"
	StatusUnmarked     ResponseStatus = "unmarked"
	StatusRevoked      ResponseStatus = "revoked"
)

const (
	hxRequestHeader    = "HX-Request"
	hxRequestTrue      = "true"
	contentTypeJSON    = "application/json"
	contentDisposition = "attachment; filename=\"photos.zip\""
	cookieSessionName  = "session"

	uploadDir          = "upload"
	defaultDeviceName  = "Unnamed Device"
	trashRetentionDays = 30
	maxUploadSize      = 10 << 30 // 10 GB
	cacheControlDay    = 86400    // 1 day in seconds
)

// Handlers содержит все HTTP-обработчики
type Handlers struct {
	cfg           *config.Config
	store         *storage.Store
	scanner       *scanner.Scanner
	thumbGen      *media.ThumbnailGenerator
	auth          *auth.Auth
	pageTemplates map[string]*template.Template // Шаблоны с наследованием от base
	cache         *cache.MediaCache
	workerPool    *worker.Pool
	thumbService  *worker.ThumbnailService
	buildVersion  string // Версия сборки для cache busting
}

// NewHandlers создает новый экземпляр обработчиков
func NewHandlers(
	cfg *config.Config,
	store *storage.Store,
	scanner *scanner.Scanner,
	thumbGen *media.ThumbnailGenerator,
	auth *auth.Auth,
	pageTemplates map[string]*template.Template,
	mediaCache *cache.MediaCache,
	workerPool *worker.Pool,
	thumbService *worker.ThumbnailService,
	buildVersion string,
) *Handlers {
	return &Handlers{
		cfg:           cfg,
		store:         store,
		scanner:       scanner,
		thumbGen:      thumbGen,
		auth:          auth,
		pageTemplates: pageTemplates,
		cache:         mediaCache,
		workerPool:    workerPool,
		thumbService:  thumbService,
		buildVersion:  buildVersion,
	}
}

// baseData возвращает общие данные для шаблонов (сессия, права)
func (h *Handlers) baseData(r *http.Request) map[string]interface{} {
	data := make(map[string]interface{})

	// Версия сборки для cache busting статических файлов
	data["BuildVersion"] = h.buildVersion

	if session := auth.GetSession(r); session != nil {
		data["Username"] = session.Username
		data["Role"] = session.Role
		data["IsAdmin"] = session.Role == storage.RoleAdmin
		data["CanEdit"] = session.Role == storage.RoleAdmin || session.Role == storage.RoleEditor

		// Загружаем избранные один раз для всей страницы
		if favIDs, err := h.store.GetUserFavorites(session.UserID); err == nil {
			favSet := make(map[string]bool, len(favIDs))
			for _, id := range favIDs {
				favSet[id] = true
			}
			data["FavSet"] = favSet
		}
	}
	return data
}

// === Страницы ===

// Index перенаправляет на галерею
func (h *Handlers) Index(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, "/gallery", http.StatusFound)
}

// LoginPage отображает страницу входа
func (h *Handlers) LoginPage(w http.ResponseWriter, r *http.Request) {
	h.render(w, "login.html", map[string]interface{}{
		"HideHeader": true,
	})
}

// Login обрабатывает вход пользователя
func (h *Handlers) Login(w http.ResponseWriter, r *http.Request) {
	username := r.FormValue("username")
	password := r.FormValue("password")

	session, err := h.auth.Login(username, password)
	if err != nil {
		h.render(w, "login.html", map[string]interface{}{
			"HideHeader": true,
			"Error":      "Неверное имя пользователя или пароль",
		})
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name:     cookieSessionName,
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
	cookie, err := r.Cookie(cookieSessionName)
	if err == nil {
		h.auth.Logout(cookie.Value)
	}

	http.SetCookie(w, &http.Cookie{
		Name:   cookieSessionName,
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

	// Проверяем, есть ли превью
	thumbPath := h.thumbGen.GetThumbnailPath(id, size)
	if _, err := os.Stat(thumbPath); os.IsNotExist(err) {
		// Проверяем, не было ли постоянной ошибки
		if hasFailed, errMsg := h.thumbService.HasFailed(id, size); hasFailed {
			logger.InfoLog.Printf("Thumbnail %s/%s permanently failed: %s", id[:16], size, errMsg)
			w.Header().Set("Cache-Control", "no-store, no-cache, must-revalidate, max-age=0")
			w.Header().Set("X-Thumbnail-Status", "failed")
			w.Header().Set("X-Thumbnail-Error", errMsg)
			http.Error(w, "Thumbnail generation failed: "+errMsg, http.StatusUnprocessableEntity)
			return
		}

		// Превью нет - ставим в очередь и возвращаем 503 (Service Unavailable)
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

		// Возвращаем 503 - миниатюра ещё не готова, генерируется
		w.Header().Set("Cache-Control", "no-store, no-cache, must-revalidate, max-age=0")
		w.Header().Set("Retry-After", "2") // Попробовать через 2 секунды
		w.Header().Set("X-Thumbnail-Status", fmt.Sprintf("processing=%v", isProcessing))
		http.Error(w, "Thumbnail is being generated", http.StatusServiceUnavailable)
		return
	}

	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Cache-Control", fmt.Sprintf("public, max-age=%d", cacheControlDay))
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
		"status":  StatusStarted,
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
		if t, err := time.Parse(time.DateOnly, from); err == nil {
			query.DateFrom = &t
		}
	}
	if to := r.URL.Query().Get("to"); to != "" {
		if t, err := time.Parse(time.DateOnly, to); err == nil {
			query.DateTo = &t
		}
	}

	// Теги
	if tags := r.URL.Query().Get("tags"); tags != "" {
		query.Tags = strings.Split(tags, ",")
	}

	// Избранное
	if fav := r.URL.Query().Get("favorite"); fav == hxRequestTrue {
		t := true
		query.IsFavorite = &t
	}

	// GPS
	if gps := r.URL.Query().Get("gps"); gps == hxRequestTrue {
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

	data := h.baseData(r)
	data["Tags"] = tags
	data["Cameras"] = cameraList
	h.render(w, "search.html", data)
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

	h.jsonResponse(w, map[string]string{"status": string(StatusDeleted)})
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

	h.jsonResponse(w, map[string]string{"status": string(StatusAdded)})
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

	h.jsonResponse(w, map[string]string{"status": string(StatusRemoved)})
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
		data := h.baseData(r)
		data["Media"] = media
		h.render(w, "favorites.html", data)
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

	h.jsonResponse(w, map[string]string{"status": string(StatusAdded)})
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

// === Timeline ===

// Timeline возвращает группировку медиа по датам
func (h *Handlers) Timeline(w http.ResponseWriter, r *http.Request) {
	timeline, err := h.store.GetTimeline()
	if err != nil {
		h.jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if h.wantsHTML(r) {
		data := h.baseData(r)
		data["Timeline"] = timeline
		h.render(w, "gallery.html", data)
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
		h.renderPartial(w, "gallery_content.html", map[string]interface{}{
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
		if dateI.IsZero() {
			dateI = allMedia[i].ModifiedAt
		}
		dateJ := allMedia[j].TakenAt
		if dateJ.IsZero() {
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
		if !m.TakenAt.IsZero() {
			date = storage.FormatYearMonth(m.TakenAt)
		} else {
			date = storage.FormatYearMonth(m.ModifiedAt)
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
		h.renderPartial(w, "gallery_all.html", map[string]interface{}{
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
		"status": StatusUpdated,
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
		"status": StatusUpdated,
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
		"status": StatusAdded,
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

	// Удаляем из кэша и thumbnail файлы
	for _, id := range req.MediaIDs {
		h.cache.DeleteMedia(id)
		h.thumbGen.DeleteThumbnails(id)
	}

	if err := h.store.BulkDelete(req.MediaIDs); err != nil {
		h.jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Инвалидируем весь кэш
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

	data := h.baseData(r)
	data["Users"] = users
	h.render(w, "admin.html", data)
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
		ID          string       `json:"id"`
		Username    string       `json:"username"`
		DisplayName string       `json:"display_name"`
		Role        storage.Role `json:"role"`
		CreatedAt   string       `json:"created_at"`
		LastLogin   string       `json:"last_login"`
	}
	var safeUsers []safeUser
	for _, u := range users {
		safeUsers = append(safeUsers, safeUser{
			ID:          u.ID,
			Username:    u.Username,
			DisplayName: u.DisplayName,
			Role:        u.Role,
			CreatedAt:   u.CreatedAt.Format(time.DateTime),
			LastLogin:   u.LastLogin.Format(time.DateTime),
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
		Username    string       `json:"username"`
		DisplayName string       `json:"display_name"`
		Password    string       `json:"password"`
		Role        storage.Role `json:"role"`
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
		DisplayName string       `json:"display_name"`
		Password    string       `json:"password"`
		Role        storage.Role `json:"role"`
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

	h.jsonResponse(w, map[string]string{"status": string(StatusUpdated)})
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

	h.jsonResponse(w, map[string]string{"status": string(StatusDeleted)})
}

// === Корзина ===

// TrashPage отображает страницу корзины
func (h *Handlers) TrashPage(w http.ResponseWriter, r *http.Request) {
	// Проверка прав: только admin и editor могут видеть корзину
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

	// Сортируем по дате удаления (новые первые)
	sort.Slice(trashMedia, func(i, j int) bool {
		if trashMedia[i].DeletedAt == nil || trashMedia[j].DeletedAt == nil {
			return false
		}
		return trashMedia[i].DeletedAt.After(*trashMedia[j].DeletedAt)
	})

	// Добавляем информацию о днях до удаления
	type TrashItem struct {
		*storage.Media
		DaysRemaining  int
		DeletedDaysAgo int
	}

	var items []TrashItem
	for _, m := range trashMedia {
		daysAgo := 0
		remaining := 30
		if m.DeletedAt != nil {
			daysAgo = int(time.Since(*m.DeletedAt).Hours() / 24)
			remaining = trashRetentionDays - daysAgo
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
	// Проверка прав: только admin может удалять
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

	// Инвалидируем кэш
	h.cache.DeleteMedia(id)
	h.cache.Clear()

	h.jsonResponse(w, map[string]interface{}{
		"status":  StatusMovedToTrash,
		"message": "Файл перемещён в корзину",
	})
}

// RestoreFromTrash восстанавливает медиа из корзины
func (h *Handlers) RestoreFromTrash(w http.ResponseWriter, r *http.Request) {
	// Проверка прав: только admin и editor
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

	// Инвалидируем кэш
	h.cache.DeleteMedia(id)
	h.cache.Clear()

	h.jsonResponse(w, map[string]interface{}{
		"status":  StatusRestored,
		"message": "Файл восстановлен",
	})
}

// PermanentDelete окончательно удаляет медиа
func (h *Handlers) PermanentDelete(w http.ResponseWriter, r *http.Request) {
	// Проверка прав: только admin
	role := auth.GetUserRole(r)
	if !auth.CanDeleteMedia(role) {
		h.jsonError(w, "Forbidden: недостаточно прав", http.StatusForbidden)
		return
	}

	id := chi.URLParam(r, "id")

	// Получаем информацию о медиа для удаления физического файла
	media, err := h.store.GetMedia(id)
	if err != nil {
		h.jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if media == nil {
		h.jsonError(w, "Media not found", http.StatusNotFound)
		return
	}

	// Удаляем физический файл с диска
	if err := os.Remove(media.Path); err != nil && !os.IsNotExist(err) {
		logger.InfoLog.Printf("Warning: failed to delete file %s: %v", media.Path, err)
	}

	// Удаляем thumbnails
	h.thumbGen.DeleteThumbnails(id)

	// Удаляем из БД и индексов
	if err := h.store.DeleteMedia(id); err != nil {
		h.jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Инвалидируем кэш
	h.cache.DeleteMedia(id)
	h.cache.Clear()

	h.jsonResponse(w, map[string]interface{}{
		"status":  StatusDeleted,
		"message": "Файл удалён окончательно",
	})
}

// EmptyTrash очищает всю корзину
func (h *Handlers) EmptyTrash(w http.ResponseWriter, r *http.Request) {
	// Проверка прав: только admin
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
		// Удаляем физический файл с диска
		if err := os.Remove(m.Path); err != nil && !os.IsNotExist(err) {
			logger.InfoLog.Printf("Warning: failed to delete file %s: %v", m.Path, err)
		}

		// Удаляем thumbnails
		h.thumbGen.DeleteThumbnails(m.ID)

		// Удаляем из БД
		if err := h.store.DeleteMedia(m.ID); err != nil {
			logger.InfoLog.Printf("Error deleting media %s: %v", m.ID, err)
			continue
		}
		deleted++
	}

	// Инвалидируем кэш
	h.cache.Clear()

	h.jsonResponse(w, map[string]interface{}{
		"status":  StatusEmptied,
		"deleted": deleted,
		"message": "Корзина очищена",
	})
}

// GetMediaInfo возвращает информацию о медиа в JSON
func (h *Handlers) GetMediaInfo(w http.ResponseWriter, r *http.Request) {
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

	h.jsonResponse(w, media)
}

// ReplaceDuplicate заменяет оригинал на дубликат
func (h *Handlers) ReplaceDuplicate(w http.ResponseWriter, r *http.Request) {
	// Проверка прав: только admin
	role := auth.GetUserRole(r)
	if role != storage.RoleAdmin {
		h.jsonError(w, "Forbidden", http.StatusForbidden)
		return
	}

	var req struct {
		DuplicateID string `json:"duplicate_id"`
		OriginalID  string `json:"original_id"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.jsonError(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	// Получаем дубликат (тот что в корзине)
	duplicate, err := h.store.GetMedia(req.DuplicateID)
	if err != nil || duplicate == nil {
		h.jsonError(w, "Duplicate not found", http.StatusNotFound)
		return
	}

	// Получаем оригинал
	original, err := h.store.GetMedia(req.OriginalID)
	if err != nil || original == nil {
		h.jsonError(w, "Original not found", http.StatusNotFound)
		return
	}

	// 1. Оригинал становится дубликатом и перемещается в корзину
	now := time.Now()
	original.DuplicateOf = req.DuplicateID
	original.DeletedAt = &now
	if err := h.store.SaveMedia(original); err != nil {
		h.jsonError(w, "Failed to update original: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// 2. Дубликат становится основным файлом и восстанавливается из корзины
	duplicate.DuplicateOf = ""
	duplicate.DeletedAt = nil
	if err := h.store.SaveMedia(duplicate); err != nil {
		h.jsonError(w, "Failed to update duplicate: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Инвалидируем кэш
	h.cache.Clear()

	h.jsonResponse(w, map[string]interface{}{
		"status":  StatusReplaced,
		"message": "Оригинал заменён на дубликат",
	})
}

// UnmarkDuplicate снимает статус дубликата с медиа
func (h *Handlers) UnmarkDuplicate(w http.ResponseWriter, r *http.Request) {
	// Проверка прав: только admin
	role := auth.GetUserRole(r)
	if role != storage.RoleAdmin {
		h.jsonError(w, "Forbidden", http.StatusForbidden)
		return
	}

	var req struct {
		MediaID string `json:"media_id"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.jsonError(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	// Получаем медиа
	media, err := h.store.GetMedia(req.MediaID)
	if err != nil || media == nil {
		h.jsonError(w, "Media not found", http.StatusNotFound)
		return
	}

	// Снимаем статус дубликата и восстанавливаем из корзины
	media.DuplicateOf = ""
	media.DeletedAt = nil

	if err := h.store.SaveMedia(media); err != nil {
		h.jsonError(w, "Failed to update media: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Инвалидируем кэш
	h.cache.Clear()

	h.jsonResponse(w, map[string]interface{}{
		"status":  StatusUnmarked,
		"message": "Статус дубликата снят",
	})
}

// BulkMoveToTrash перемещает несколько медиа в корзину
func (h *Handlers) BulkMoveToTrash(w http.ResponseWriter, r *http.Request) {
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
	// Проверка прав: только admin и editor
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

// === Helpers ===

// wantsHTML проверяет, запрашивает ли клиент HTML (браузер или HTMX)
func (h *Handlers) wantsHTML(r *http.Request) bool {
	// HTMX запросы всегда хотят HTML
	if r.Header.Get(hxRequestHeader) == hxRequestTrue {
		return true
	}
	// Проверяем Accept header (браузеры отправляют text/html в начале)
	accept := r.Header.Get("Accept")
	return strings.Contains(accept, "text/html")
}

func (h *Handlers) render(w http.ResponseWriter, name string, data interface{}) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")

	tmpl, ok := h.pageTemplates[name]
	if !ok {
		logger.InfoLog.Printf("Template not found: %s", name)
		http.Error(w, "Template not found", http.StatusInternalServerError)
		return
	}

	// Выполняем шаблон "base" который использует блоки определённые в странице
	if err := tmpl.ExecuteTemplate(w, "base", data); err != nil {
		logger.ErrorLog.Printf("Template execution error for %s: %v", name, err)
		http.Error(w, "Internal error", http.StatusInternalServerError)
	}
}

// renderPartial рендерит фрагмент шаблона (без base)
func (h *Handlers) renderPartial(w http.ResponseWriter, name string, data interface{}) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")

	tmpl, ok := h.pageTemplates[name]
	if !ok {
		logger.ErrorLog.Printf("Template not found: %s", name)
		http.Error(w, "Template not found", http.StatusInternalServerError)
		return
	}

	// Выполняем шаблон напрямую по имени (для фрагментов)
	if err := tmpl.ExecuteTemplate(w, name, data); err != nil {
		logger.ErrorLog.Printf("Template execution error for %s: %v", name, err)
		http.Error(w, "Internal error", http.StatusInternalServerError)
	}
}

func (h *Handlers) jsonResponse(w http.ResponseWriter, data interface{}) {
	w.Header().Set("Content-Type", contentTypeJSON)
	json.NewEncoder(w).Encode(data)
}

func (h *Handlers) jsonError(w http.ResponseWriter, message string, code int) {
	w.Header().Set("Content-Type", contentTypeJSON)
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": message})
}

func (h *Handlers) getMimeType(ext string) string {
	mimeTypes := map[string]string{
		".jpg":  "image/jpeg",
		".jpeg": "image/jpeg",
		".png":  "image/png",
		".gif":  "image/gif",
		".webp": "image/webp",
		".heic": "image/heic",
		".mp4":  "video/mp4",
		".mov":  "video/quicktime",
		".avi":  "video/x-msvideo",
		".mkv":  "video/x-matroska",
		".webm": "video/webm",
		".raw":  "image/x-raw",
		".cr2":  "image/x-canon-cr2",
		".cr3":  "image/x-canon-cr3",
		".nef":  "image/x-nikon-nef",
		".arw":  "image/x-sony-arw",
		".dng":  "image/x-adobe-dng",
		".orf":  "image/x-olympus-orf",
		".raf":  "image/x-fuji-raf",
		".rw2":  "image/x-panasonic-rw2",
	}
	if mime, ok := mimeTypes[ext]; ok {
		return mime
	}
	return "application/octet-stream"
}

// === Upload Page ===

// UploadPage отображает страницу загрузки
func (h *Handlers) UploadPage(w http.ResponseWriter, r *http.Request) {
	data := h.baseData(r)

	// Определяем, мобильный ли это браузер (для camera capture)
	ua := r.Header.Get("User-Agent")
	isMobile := strings.Contains(strings.ToLower(ua), "mobile") ||
		strings.Contains(strings.ToLower(ua), "android") ||
		strings.Contains(strings.ToLower(ua), "iphone")

	data["CanCapture"] = isMobile

	h.render(w, "upload.html", data)
}

// PWASettingsPage отображает страницу настроек PWA
func (h *Handlers) PWASettingsPage(w http.ResponseWriter, r *http.Request) {
	data := h.baseData(r)
	h.render(w, "pwa_settings.html", data)
}

// === Upload API ===

// UploadMedia обрабатывает загрузку медиа-файлов через API
func (h *Handlers) UploadMedia(w http.ResponseWriter, r *http.Request) {
	// Все авторизованные пользователи могут загружать
	session := auth.GetSession(r)
	if session == nil {
		h.jsonError(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	// Ограничиваем размер до 10GB (для семейного использования)
	err := r.ParseMultipartForm(maxUploadSize)
	if err != nil {
		h.jsonError(w, "Failed to parse form: "+err.Error(), http.StatusBadRequest)
		return
	}

	files := r.MultipartForm.File["files"]
	if len(files) == 0 {
		h.jsonError(w, "No files provided", http.StatusBadRequest)
		return
	}

	// Создаем map расширений для быстрой проверки
	extensions := make(map[string]storage.MediaType)
	for _, ext := range h.cfg.Scan.Extensions.Images {
		extensions[strings.ToLower(ext)] = storage.MediaTypeImage
	}
	for _, ext := range h.cfg.Scan.Extensions.Videos {
		extensions[strings.ToLower(ext)] = storage.MediaTypeVideo
	}
	for _, ext := range h.cfg.Scan.Extensions.Raw {
		extensions[strings.ToLower(ext)] = storage.MediaTypeRaw
	}

	// Определяем целевую директорию
	now := time.Now()
	baseDir := h.cfg.Storage.MediaPaths[0]
	year := strconv.Itoa(now.Year())
	month := fmt.Sprintf("%02d", now.Month())
	targetDir := filepath.Join(baseDir, uploadDir, year, month)

	// Создаем директорию если её нет
	if err := os.MkdirAll(targetDir, 0755); err != nil {
		h.jsonError(w, "Failed to create upload directory: "+err.Error(), http.StatusInternalServerError)
		return
	}

	var uploaded int
	var errors int
	var mediaIDs []string
	var messages []string

	for _, fileHeader := range files {
		// Проверяем расширение
		ext := strings.ToLower(filepath.Ext(fileHeader.Filename))
		mediaType, ok := extensions[ext]
		if !ok {
			messages = append(messages, "Skipped "+fileHeader.Filename+": unsupported file type")
			errors++
			continue
		}

		// Генерируем уникальное имя файла
		timestamp := fmt.Sprintf("%04d%02d%02d_%02d%02d%02d", now.Year(), now.Month(), now.Day(), now.Hour(), now.Minute(), now.Second())
		uniqueFilename := timestamp + "_" + fileHeader.Filename
		targetPath := filepath.Join(targetDir, uniqueFilename)

		// Открываем загруженный файл
		src, err := fileHeader.Open()
		if err != nil {
			messages = append(messages, "Failed to open "+fileHeader.Filename+": "+err.Error())
			errors++
			continue
		}

		// Создаем целевой файл
		dst, err := os.Create(targetPath)
		if err != nil {
			src.Close()
			messages = append(messages, "Failed to create "+uniqueFilename+": "+err.Error())
			errors++
			continue
		}

		// Гарантируем закрытие файлов даже при панике
		func() {
			defer src.Close()
			defer dst.Close()

			// Копируем содержимое
			_, err = io.Copy(dst, src)
		}()

		if err != nil {
			os.Remove(targetPath)
			messages = append(messages, "Failed to save "+fileHeader.Filename+": "+err.Error())
			errors++
			continue
		}

		// Получаем информацию о файле
		fileInfo, err := os.Stat(targetPath)
		if err != nil {
			os.Remove(targetPath)
			messages = append(messages, "Failed to stat "+uniqueFilename+": "+err.Error())
			errors++
			continue
		}

		// Создаем запись media
		relPath := filepath.Join(uploadDir, year, month, uniqueFilename)
		mediaItem := &storage.Media{
			ID:         storage.GenerateID(targetPath),
			Path:       targetPath,
			RelPath:    relPath,
			Dir:        filepath.Dir(relPath),
			Filename:   uniqueFilename,
			Ext:        ext,
			Type:       mediaType,
			MimeType:   h.getMimeType(ext),
			Size:       fileInfo.Size(),
			ModifiedAt: fileInfo.ModTime(),
			CreatedAt:  now,
		}

		// Извлекаем метаданные для изображений
		if mediaType == storage.MediaTypeImage || mediaType == storage.MediaTypeRaw {
			if err := scanner.ExtractMetadata(targetPath, mediaItem); err != nil {
				logger.InfoLog.Printf("Warning: failed to extract metadata from %s: %v", uniqueFilename, err)
			}
		}

		// Вычисляем хеши
		isImage := mediaType == storage.MediaTypeImage || mediaType == storage.MediaTypeRaw
		hashes, err := scanner.CalculateHashes(targetPath, isImage)
		if err != nil {
			logger.InfoLog.Printf("Warning: failed to calculate hashes for %s: %v", uniqueFilename, err)
		} else {
			mediaItem.Checksum = hashes.Checksum
			mediaItem.ImageHash = hashes.ImageHash
		}

		// Проверяем на дубликаты
		dupResult, err := h.store.CheckDuplicate(
			mediaItem.Size,
			mediaItem.Checksum,
			mediaItem.ImageHash,
			isImage,
			10, // Similarity threshold
		)
		if err == nil && dupResult != nil && dupResult.IsDuplicate {
			// Дубликат - помечаем и переносим в корзину
			mediaItem.DuplicateOf = dupResult.ExistingID
			mediaItem.DeletedAt = &now
			messages = append(messages, "Duplicate detected: "+uniqueFilename+" ("+string(dupResult.Type)+")")
		}

		// Сохраняем в БД
		if err := h.store.SaveMedia(mediaItem); err != nil {
			os.Remove(targetPath)
			messages = append(messages, "Failed to save to database "+uniqueFilename+": "+err.Error())
			errors++
			continue
		}

		mediaIDs = append(mediaIDs, mediaItem.ID)
		uploaded++

		// Добавляем в очередь генерации превью
		if mediaItem.DeletedAt == nil {
			h.thumbService.QueueAllThumbnails(mediaItem.ID)
		}

		messages = append(messages, "Uploaded: "+uniqueFilename)
	}

	// Инвалидируем кэши
	if uploaded > 0 {
		h.cache.Clear()
	}

	h.jsonResponse(w, map[string]interface{}{
		"uploaded":  uploaded,
		"errors":    errors,
		"media_ids": mediaIDs,
		"messages":  messages,
	})
}

// === API Token Management ===

// GenerateAPIToken генерирует новый API токен для пользователя
func (h *Handlers) GenerateAPIToken(w http.ResponseWriter, r *http.Request) {
	session := auth.GetSession(r)
	if session == nil {
		h.jsonError(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	var req struct {
		DeviceName string `json:"device_name"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.jsonError(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if req.DeviceName == "" {
		req.DeviceName = defaultDeviceName
	}

	token, err := h.auth.GenerateAPIToken(session.UserID, session.Username, session.Role, req.DeviceName)
	if err != nil {
		h.jsonError(w, "Failed to generate token: "+err.Error(), http.StatusInternalServerError)
		return
	}

	h.jsonResponse(w, token)
}

// ListAPITokens возвращает список токенов пользователя
func (h *Handlers) ListAPITokens(w http.ResponseWriter, r *http.Request) {
	session := auth.GetSession(r)
	if session == nil {
		h.jsonError(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	tokens, err := h.store.ListUserAPITokens(session.UserID)
	if err != nil {
		h.jsonError(w, "Failed to list tokens: "+err.Error(), http.StatusInternalServerError)
		return
	}

	h.jsonResponse(w, tokens)
}

// RevokeAPIToken отзывает API токен
func (h *Handlers) RevokeAPIToken(w http.ResponseWriter, r *http.Request) {
	session := auth.GetSession(r)
	if session == nil {
		h.jsonError(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	token := chi.URLParam(r, "token")
	if token == "" {
		h.jsonError(w, "Token parameter required", http.StatusBadRequest)
		return
	}

	// Проверяем права: токен должен принадлежать пользователю или user должен быть admin
	if !auth.IsAdmin(session.Role) {
		apiToken, err := h.store.GetAPIToken(token)
		if err != nil {
			h.jsonError(w, "Failed to get token: "+err.Error(), http.StatusInternalServerError)
			return
		}
		if apiToken == nil {
			h.jsonError(w, "Token not found", http.StatusNotFound)
			return
		}
		if apiToken.UserID != session.UserID {
			h.jsonError(w, "Forbidden", http.StatusForbidden)
			return
		}
	}

	if err := h.store.DeleteAPIToken(token); err != nil {
		h.jsonError(w, "Failed to revoke token: "+err.Error(), http.StatusInternalServerError)
		return
	}

	h.jsonResponse(w, map[string]string{"status": string(StatusRevoked)})
}

func generateID() string {
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b)
}
