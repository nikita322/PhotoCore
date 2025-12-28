package web

import (
	"embed"
	"fmt"
	"html/template"
	"io/fs"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/photocore/photocore/internal/auth"
	"github.com/photocore/photocore/internal/cache"
	"github.com/photocore/photocore/internal/config"
	"github.com/photocore/photocore/internal/logger"
	"github.com/photocore/photocore/internal/media"
	"github.com/photocore/photocore/internal/scanner"
	"github.com/photocore/photocore/internal/storage"
	"github.com/photocore/photocore/internal/web/handlers"
	"github.com/photocore/photocore/internal/worker"
)

//go:embed templates/layouts/*.html templates/pages/*.html templates/partials/*.html
var templatesFS embed.FS

// Server представляет веб-сервер приложения
type Server struct {
	cfg           *config.Config
	store         *storage.Store
	scanner       *scanner.Scanner
	thumbGen      *media.ThumbnailGenerator
	auth          *auth.Auth
	pageTemplates map[string]*template.Template // Шаблоны страниц с наследованием от base
	router        *chi.Mux
	staticFS      fs.FS
	cache         *cache.MediaCache
	workerPool    *worker.Pool
	thumbService  *worker.ThumbnailService
	buildVersion  string // Версия сборки для cache busting статических файлов
}

// NewServer создает новый веб-сервер
func NewServer(
	cfg *config.Config,
	store *storage.Store,
	scanner *scanner.Scanner,
	thumbGen *media.ThumbnailGenerator,
	authService *auth.Auth,
	staticFS fs.FS,
	mediaCache *cache.MediaCache,
	workerPool *worker.Pool,
	thumbService *worker.ThumbnailService,
	buildVersion string,
) (*Server, error) {
	// Template functions
	funcMap := template.FuncMap{
		"sub":     func(a, b int) int { return a - b },
		"RFC3339": func() string { return time.RFC3339 }, // Функция возвращающая константу форматирования
		"dict": func(values ...interface{}) (map[string]interface{}, error) {
			if len(values)%2 != 0 {
				return nil, fmt.Errorf("dict requires even number of arguments")
			}
			dict := make(map[string]interface{}, len(values)/2)
			for i := 0; i < len(values); i += 2 {
				key, ok := values[i].(string)
				if !ok {
					return nil, fmt.Errorf("dict keys must be strings")
				}
				dict[key] = values[i+1]
			}
			return dict, nil
		},
	}

	// Парсим базовый шаблон (layout) и общие partials
	baseTemplate, err := template.New("").Funcs(funcMap).ParseFS(templatesFS,
		"templates/layouts/*.html",
		"templates/partials/icons.html",
		"templates/partials/media_card.html",
	)
	if err != nil {
		return nil, fmt.Errorf("failed to parse base templates: %w", err)
	}

	// Страницы (полные страницы с наследованием от base)
	pages := []string{
		"gallery.html",
		"albums.html",
		"album.html",
		"favorites.html",
		"search.html",
		"map.html",
		"admin.html",
		"login.html",
		"viewer.html",
		"trash.html",
		"upload.html",
		"pwa_settings.html",
	}

	// Partials (фрагменты для HTMX)
	partials := []string{
		"gallery_content.html",
		"gallery_all.html",
		"search_results.html",
		"viewer_content.html",
	}

	// Для каждой страницы создаём клон base и парсим страницу
	pageTemplates := make(map[string]*template.Template)
	for _, page := range pages {
		tmpl, err := baseTemplate.Clone()
		if err != nil {
			return nil, fmt.Errorf("failed to clone base template for %s: %w", page, err)
		}
		tmpl, err = tmpl.ParseFS(templatesFS, "templates/pages/"+page)
		if err != nil {
			return nil, fmt.Errorf("failed to parse template %s: %w", page, err)
		}
		pageTemplates[page] = tmpl
	}

	// Для partials тоже создаём клон (они могут использовать base_styles и т.д.)
	for _, partial := range partials {
		tmpl, err := baseTemplate.Clone()
		if err != nil {
			return nil, fmt.Errorf("failed to clone base template for %s: %w", partial, err)
		}
		tmpl, err = tmpl.ParseFS(templatesFS, "templates/partials/"+partial)
		if err != nil {
			return nil, fmt.Errorf("failed to parse template %s: %w", partial, err)
		}
		pageTemplates[partial] = tmpl
	}

	s := &Server{
		cfg:           cfg,
		store:         store,
		scanner:       scanner,
		thumbGen:      thumbGen,
		auth:          authService,
		pageTemplates: pageTemplates,
		staticFS:      staticFS,
		cache:         mediaCache,
		workerPool:    workerPool,
		thumbService:  thumbService,
		buildVersion:  buildVersion,
	}

	s.setupRoutes()
	return s, nil
}

// staticCacheMiddleware добавляет Cache-Control заголовки для статических файлов
func staticCacheMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		ext := strings.ToLower(filepath.Ext(path))

		// Определяем Cache-Control в зависимости от типа файла
		var cacheControl string
		switch ext {
		case ".js":
			// JavaScript файлы - 1 год (immutable, так как версионируются)
			cacheControl = "public, max-age=31536000, immutable"
		case ".css":
			// CSS файлы - 1 год (immutable)
			cacheControl = "public, max-age=31536000, immutable"
		case ".woff", ".woff2", ".ttf", ".eot", ".otf":
			// Шрифты - 1 год (immutable)
			cacheControl = "public, max-age=31536000, immutable"
		case ".jpg", ".jpeg", ".png", ".gif", ".svg", ".webp", ".ico":
			// Изображения - 1 месяц
			cacheControl = "public, max-age=2592000"
		case ".json", ".xml":
			// Конфигурационные файлы - 1 день
			cacheControl = "public, max-age=86400"
		case ".html":
			// HTML файлы - без кэша (для offline.html и т.д.)
			cacheControl = "no-cache"
		default:
			// Остальные файлы - 1 неделя
			cacheControl = "public, max-age=604800"
		}

		w.Header().Set("Cache-Control", cacheControl)
		next.ServeHTTP(w, r)
	})
}

func (s *Server) setupRoutes() {
	r := chi.NewRouter()

	// Middleware
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(middleware.Compress(5))
	r.Use(middleware.Timeout(60 * time.Second))

	// Создаем handlers
	h := handlers.NewHandlers(s.cfg, s.store, s.scanner, s.thumbGen, s.auth, s.pageTemplates, s.cache, s.workerPool, s.thumbService, s.buildVersion)

	// Статические файлы
	staticHandler := http.FileServer(http.FS(s.staticFS))

	// Service Worker с правильным scope (без кэша)
	r.Get("/static/sw.js", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/javascript")
		w.Header().Set("Service-Worker-Allowed", "/")
		w.Header().Set("Cache-Control", "no-cache")
		http.StripPrefix("/static/", staticHandler).ServeHTTP(w, r)
	})

	// Все остальные статические файлы с кэшированием
	r.Handle("/static/*", staticCacheMiddleware(http.StripPrefix("/static/", staticHandler)))

	// Публичные маршруты
	r.Get("/login", h.LoginPage)
	r.Post("/login", h.Login)

	// Защищенные маршруты
	r.Group(func(r chi.Router) {
		r.Use(s.auth.Middleware)

		// Основные страницы
		r.Get("/", h.Index)
		r.Get("/gallery", h.Timeline) // Главная галерея - timeline view
		r.Get("/view/{id}", h.ViewMedia)
		r.Get("/upload", h.UploadPage)
		r.Get("/pwa/settings", h.PWASettingsPage)

		// Новые страницы
		r.Get("/search", h.SearchPage)
		r.Get("/albums", h.ListAlbums)
		r.Get("/albums/{id}", h.GetAlbum)
		r.Get("/favorites", h.ListFavorites)
		r.Get("/timeline", h.Timeline)
		r.Get("/timeline/all", h.TimelineAllMedia)
		r.Get("/timeline/{period}", h.TimelineMedia)
		r.Get("/map", h.MapPage)
		r.Get("/tags/{tag}", h.MediaByTag)

		// Медиа-файлы
		r.Get("/media/{id}", h.ServeMedia)
		r.Get("/media/{id}/thumb", h.ServeThumbnail)
		r.Get("/media/{id}/thumb/{size}", h.ServeThumbnailSize)

		// API
		r.Post("/logout", h.Logout)
		r.Get("/api/scan", h.StartScan)
		r.Get("/api/scan/progress", h.ScanProgress)
		r.Get("/api/stats", h.Stats)

		// API для мониторинга
		r.Get("/api/queue", h.QueueStats)
		r.Get("/api/cache", h.CacheStats)
		r.Post("/api/thumbnails/generate", h.GenerateThumbnails)

		// API поиска
		r.Get("/api/search", h.Search)

		// API альбомов
		r.Get("/api/albums", h.ListAlbums)
		r.Post("/api/albums", h.CreateAlbum)
		r.Get("/api/albums/{id}", h.GetAlbum)
		r.Put("/api/albums/{id}", h.UpdateAlbum)
		r.Delete("/api/albums/{id}", h.DeleteAlbum)
		r.Post("/api/albums/{id}/media", h.AddToAlbum)
		r.Delete("/api/albums/{id}/media", h.RemoveFromAlbum)

		// API избранного и тегов
		r.Post("/api/media/{id}/favorite", h.ToggleFavorite)
		r.Get("/api/favorites", h.ListFavorites)
		r.Get("/api/tags", h.ListTags)
		r.Post("/api/media/{id}/tags", h.AddTags)
		r.Delete("/api/media/{id}/tags", h.RemoveTags)

		// API timeline и карты
		r.Get("/api/timeline", h.Timeline)
		r.Get("/api/geo", h.GeoPoints)

		// API bulk операций
		r.Post("/api/bulk/favorite", h.BulkFavorite)
		r.Post("/api/bulk/tags", h.BulkAddTags)
		r.Post("/api/bulk/album", h.BulkAddToAlbum)
		r.Post("/api/bulk/delete", h.BulkMoveToTrash) // Теперь перемещает в корзину
		r.Post("/api/bulk/download", h.BulkDownload)
		r.Post("/api/bulk/restore", h.BulkRestore)

		// Корзина
		r.Get("/trash", h.TrashPage)
		r.Get("/api/trash/stats", h.TrashStats)
		r.Post("/api/media/{id}/trash", h.MoveToTrash)
		r.Post("/api/trash/{id}/restore", h.RestoreFromTrash)
		r.Delete("/api/trash/{id}", h.PermanentDelete)
		r.Delete("/api/trash", h.EmptyTrash)

		// API медиа (для модального окна сравнения)
		r.Get("/api/media/{id}", h.GetMediaInfo)
		r.Post("/api/duplicates/replace", h.ReplaceDuplicate)
		r.Post("/api/duplicates/unmark", h.UnmarkDuplicate)

		// Admin страница и API (проверка прав в handlers)
		r.Get("/admin", h.AdminPage)
		r.Get("/api/users", h.ListUsers)
		r.Post("/api/users", h.CreateUser)
		r.Put("/api/users/{username}", h.UpdateUser)
		r.Delete("/api/users/{username}", h.DeleteUser)

		// Upload API
		r.Post("/api/upload", h.UploadMedia)

		// API Token Management
		r.Post("/api/tokens", h.GenerateAPIToken)
		r.Get("/api/tokens", h.ListAPITokens)
		r.Delete("/api/tokens/{token}", h.RevokeAPIToken)
	})

	s.router = r
}

// Start запускает веб-сервер
func (s *Server) Start() error {
	addr := fmt.Sprintf("%s:%d", s.cfg.Server.Host, s.cfg.Server.Port)
	logger.InfoLog.Printf("Starting server on http://%s", addr)
	return http.ListenAndServe(addr, s.router)
}
