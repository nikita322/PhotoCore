package web

import (
	"embed"
	"fmt"
	"html/template"
	"io/fs"
	"log"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/photocore/photocore/internal/auth"
	"github.com/photocore/photocore/internal/cache"
	"github.com/photocore/photocore/internal/config"
	"github.com/photocore/photocore/internal/media"
	"github.com/photocore/photocore/internal/scanner"
	"github.com/photocore/photocore/internal/storage"
	"github.com/photocore/photocore/internal/web/handlers"
	"github.com/photocore/photocore/internal/worker"
)

//go:embed templates/*.html
var templatesFS embed.FS

// Server представляет веб-сервер приложения
type Server struct {
	cfg          *config.Config
	store        *storage.Store
	scanner      *scanner.Scanner
	thumbGen     *media.ThumbnailGenerator
	auth         *auth.Auth
	templates    *template.Template
	router       *chi.Mux
	staticFS     fs.FS
	cache        *cache.MediaCache
	workerPool   *worker.Pool
	thumbService *worker.ThumbnailService
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
) (*Server, error) {
	// Template functions
	funcMap := template.FuncMap{
		"sub": func(a, b int) int { return a - b },
	}

	// Парсим шаблоны
	tmpl, err := template.New("").Funcs(funcMap).ParseFS(templatesFS, "templates/*.html")
	if err != nil {
		return nil, fmt.Errorf("failed to parse templates: %w", err)
	}

	s := &Server{
		cfg:          cfg,
		store:        store,
		scanner:      scanner,
		thumbGen:     thumbGen,
		auth:         authService,
		templates:    tmpl,
		staticFS:     staticFS,
		cache:        mediaCache,
		workerPool:   workerPool,
		thumbService: thumbService,
	}

	s.setupRoutes()
	return s, nil
}

func (s *Server) setupRoutes() {
	r := chi.NewRouter()

	// Middleware
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(middleware.Compress(5))
	r.Use(middleware.Timeout(60 * time.Second))

	// Создаем handlers
	h := handlers.NewHandlers(s.cfg, s.store, s.scanner, s.thumbGen, s.auth, s.templates, s.cache, s.workerPool, s.thumbService)

	// Статические файлы
	staticHandler := http.FileServer(http.FS(s.staticFS))
	r.Handle("/static/*", http.StripPrefix("/static/", staticHandler))

	// Публичные маршруты
	r.Get("/login", h.LoginPage)
	r.Post("/login", h.Login)

	// Защищенные маршруты
	r.Group(func(r chi.Router) {
		r.Use(s.auth.Middleware)

		// Основные страницы
		r.Get("/", h.Index)
		r.Get("/gallery", h.Timeline)    // Главная галерея - timeline view
		r.Get("/view/{id}", h.ViewMedia)

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
		r.Post("/api/bulk/delete", h.BulkDelete)
		r.Post("/api/bulk/download", h.BulkDownload)

		// Admin страница и API (проверка прав в handlers)
		r.Get("/admin", h.AdminPage)
		r.Get("/api/users", h.ListUsers)
		r.Post("/api/users", h.CreateUser)
		r.Put("/api/users/{username}", h.UpdateUser)
		r.Delete("/api/users/{username}", h.DeleteUser)
	})

	s.router = r
}

// Start запускает веб-сервер
func (s *Server) Start() error {
	addr := fmt.Sprintf("%s:%d", s.cfg.Server.Host, s.cfg.Server.Port)
	log.Printf("Starting server on http://%s", addr)
	return http.ListenAndServe(addr, s.router)
}
