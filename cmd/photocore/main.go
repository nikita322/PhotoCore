package main

import (
	"embed"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/photocore/photocore/internal/auth"
	"github.com/photocore/photocore/internal/cache"
	"github.com/photocore/photocore/internal/config"
	"github.com/photocore/photocore/internal/logger"
	"github.com/photocore/photocore/internal/media"
	"github.com/photocore/photocore/internal/scanner"
	"github.com/photocore/photocore/internal/storage"
	"github.com/photocore/photocore/internal/web"
	"github.com/photocore/photocore/internal/worker"
)

//go:embed static
var staticFS embed.FS

// BuildVersion устанавливается во время сборки через -ldflags
// Например: go build -ldflags "-X main.BuildVersion=$(date +%s)"
var BuildVersion = "dev"

const (
	defaultDirPerm                      = os.FileMode(0755)
	workerPoolSizeAuto                  = 0
	workerQueueSize                     = 1000
	trashCleanupHours                   = 24
	defaultDuplicateSimilarityThreshold = 10
)

func main() {
	// Флаги командной строки
	configPath := flag.String("config", "config.yaml", "Путь к файлу конфигурации")
	flag.Parse()

	// Загружаем конфигурацию (до настройки логирования, чтобы знать путь к логам)
	cfg, err := config.Load(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to load config: %v\n", err)
		os.Exit(1)
	}

	// Настраиваем логирование с путем из конфига
	if err := logger.Init(cfg.Storage.LogsPath); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to setup logging: %v\n", err)
		os.Exit(1)
	}
	defer logger.Cleanup()

	logger.InfoLog.Println("PhotoCore starting...")
	logger.InfoLog.Printf("Config loaded from %s", *configPath)

	// Создаем необходимые директории
	if err := os.MkdirAll(cfg.Storage.CachePath, defaultDirPerm); err != nil {
		logger.ErrorLog.Fatalf("Failed to create cache directory: %v", err)
	}

	// Инициализируем хранилище
	store, err := storage.NewStore(cfg.Storage.DBPath)
	if err != nil {
		logger.ErrorLog.Fatalf("Failed to init storage: %v", err)
	}
	logger.InfoLog.Println("Storage initialized")

	// Инициализируем in-memory кэш
	mediaCache := cache.NewMediaCache()
	logger.InfoLog.Println("Cache initialized")

	// Инициализируем аутентификацию
	authService := auth.NewAuth(cfg, store)
	if err := authService.EnsureAdminUser(); err != nil {
		logger.ErrorLog.Fatalf("Failed to create admin user: %v", err)
	}
	logger.InfoLog.Printf("Admin user ensured (username: %s)", cfg.Auth.AdminUsername)

	// Создаем генератор превью
	thumbGen := media.NewThumbnailGenerator(cfg)
	if err := thumbGen.EnsureCacheDir(); err != nil {
		logger.ErrorLog.Fatalf("Failed to create cache directory: %v", err)
	}
	logger.InfoLog.Println("Thumbnail generator initialized")

	// Создаем worker pool
	workerPool := worker.NewPool(workerPoolSizeAuto, workerQueueSize) // 0 = auto (NumCPU)
	workerPool.Start()
	logger.InfoLog.Println("Worker pool started")

	// Создаем сервис генерации превью
	thumbService := worker.NewThumbnailService(workerPool, store, thumbGen)
	logger.InfoLog.Println("Thumbnail service initialized")

	// Создаем сканер
	mediaScanner := scanner.NewScanner(cfg, store)
	logger.InfoLog.Println("Scanner initialized")

	// Создаем file watcher
	fileWatcher, err := scanner.NewWatcher(cfg, store)
	if err != nil {
		logger.InfoLog.Printf("Warning: failed to create file watcher: %v", err)
	} else {
		// Добавляем обработчик событий
		fileWatcher.AddHandler(func(event scanner.FileEvent) {
			handleFileEvent(event, cfg, store, thumbService, thumbGen, mediaCache)
		})

		if err := fileWatcher.Start(); err != nil {
			logger.InfoLog.Printf("Warning: failed to start file watcher: %v", err)
		} else {
			logger.InfoLog.Println("File watcher started")
		}
	}

	// Извлекаем статические файлы из embed
	staticSubFS, err := fs.Sub(staticFS, "static")
	if err != nil {
		logger.ErrorLog.Fatalf("Failed to get static fs: %v", err)
	}

	// Создаем веб-сервер
	server, err := web.NewServer(cfg, store, mediaScanner, thumbGen, authService, staticSubFS, mediaCache, workerPool, thumbService, BuildVersion)
	if err != nil {
		logger.ErrorLog.Fatalf("Failed to create server: %v", err)
	}

	// Автоматически запускаем сканирование и генерацию превью
	go func() {
		stats, _ := store.GetStats()
		if stats == nil || stats.TotalMedia == 0 {
			logger.InfoLog.Println("No media found, starting initial scan...")
			if err := mediaScanner.Start(); err != nil {
				logger.InfoLog.Printf("Failed to start initial scan: %v", err)
				return
			}

			// Ждем завершения сканирования
			for mediaScanner.IsScanning() {
				time.Sleep(time.Second)
			}

			// Запускаем генерацию превью
			logger.InfoLog.Println("Starting thumbnail pregeneration...")
			thumbService.PregenerateThumbnails()
		}
	}()

	// Автоочистка корзины (удаление файлов старше 30 дней)
	go func() {
		// Очистка при старте
		cleanupTrash(store, thumbGen)

		// Периодическая очистка каждые 24 часа
		ticker := time.NewTicker(trashCleanupHours * time.Hour)
		defer ticker.Stop()

		for range ticker.C {
			cleanupTrash(store, thumbGen)
		}
	}()

	// Graceful shutdown
	done := make(chan os.Signal, 1)
	signal.Notify(done, os.Interrupt, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		if err := server.Start(); err != nil {
			logger.ErrorLog.Fatalf("Server error: %v", err)
		}
	}()

	logger.InfoLog.Printf("Server started on http://%s:%d", cfg.Server.Host, cfg.Server.Port)
	logger.InfoLog.Printf("Login with username: %s", cfg.Auth.AdminUsername)

	sig := <-done
	storage.LogShutdownSignal(sig.String())
	logger.InfoLog.Println("Shutting down...")

	// Явно закрываем все ресурсы в правильном порядке
	// (defer может не сработать при SIGKILL)
	logger.InfoLog.Println("Stopping worker pool...")
	workerPool.Stop()

	logger.InfoLog.Println("Stopping media cache...")
	mediaCache.Stop()

	if fileWatcher != nil {
		logger.InfoLog.Println("Stopping file watcher...")
		fileWatcher.Stop()
	}

	logger.InfoLog.Println("Closing database...")
	if err := store.Close(); err != nil {
		logger.ErrorLog.Printf("Error closing database: %v", err)
	} else {
		logger.InfoLog.Println("Database closed successfully")
	}

	logger.InfoLog.Println("Shutdown complete")
}

// handleFileEvent обрабатывает события файловой системы
func handleFileEvent(event scanner.FileEvent, cfg *config.Config, store *storage.Store, thumbService *worker.ThumbnailService, thumbGen *media.ThumbnailGenerator, mediaCache *cache.MediaCache) {
	if event.IsDir {
		return
	}

	ext := strings.ToLower(filepath.Ext(event.Path))

	switch event.Operation {
	case scanner.FileOpCreate, scanner.FileOpModify:
		// Определяем тип медиа
		var mediaType storage.MediaType
		if cfg.IsImage(ext) {
			mediaType = storage.MediaTypeImage
		} else if cfg.IsVideo(ext) {
			mediaType = storage.MediaTypeVideo
		} else if cfg.IsRaw(ext) {
			mediaType = storage.MediaTypeRaw
		} else {
			return
		}

		// Проверяем, не удалён ли этот файл (в корзине)
		mediaID := storage.GenerateID(event.Path)
		existingMedia, _ := store.GetMedia(mediaID)
		if existingMedia != nil && existingMedia.DeletedAt != nil {
			// Файл в корзине - не обновляем его
			return
		}

		// Получаем информацию о файле
		info, err := os.Stat(event.Path)
		if err != nil {
			logger.InfoLog.Printf("Watcher: failed to stat %s: %v", event.Path, err)
			return
		}

		// Определяем относительный путь
		var relPath string
		for _, mediaPath := range cfg.Storage.MediaPaths {
			absPath, _ := filepath.Abs(mediaPath)
			if strings.HasPrefix(event.Path, absPath) {
				relPath, _ = filepath.Rel(absPath, event.Path)
				break
			}
		}

		// Создаем/обновляем запись
		m := &storage.Media{
			ID:         mediaID,
			Path:       event.Path,
			RelPath:    relPath,
			Dir:        filepath.Dir(relPath),
			Filename:   info.Name(),
			Ext:        ext,
			Type:       mediaType,
			Size:       info.Size(),
			ModifiedAt: info.ModTime(),
			CreatedAt:  event.Time,
		}

		// Сохраняем важные поля из существующей записи
		if existingMedia != nil {
			m.CreatedAt = existingMedia.CreatedAt
			m.TakenAt = existingMedia.TakenAt
			m.IsFavorite = existingMedia.IsFavorite
			m.Tags = existingMedia.Tags
			m.Metadata = existingMedia.Metadata
			m.Checksum = existingMedia.Checksum
			m.ImageHash = existingMedia.ImageHash
			m.ThumbSmall = existingMedia.ThumbSmall
			m.ThumbLarge = existingMedia.ThumbLarge
		}

		// Извлекаем метаданные для изображений если они отсутствуют
		// (при SFTP загрузке первое событие может прийти до завершения записи файла)
		if (mediaType == storage.MediaTypeImage || mediaType == storage.MediaTypeRaw) &&
			m.TakenAt.IsZero() && m.Metadata.Camera == "" {
			logger.InfoLog.Printf("Watcher: extracting metadata from %s (size=%d)", event.Path, m.Size)
			if err := scanner.ExtractMetadata(event.Path, m); err != nil {
				logger.InfoLog.Printf("Watcher: failed to extract metadata from %s: %v", event.Path, err)
			} else {
				logger.InfoLog.Printf("Watcher: metadata extracted - camera=%s, taken=%v", m.Metadata.Camera, m.TakenAt)
			}
		}

		// Вычисляем хеши для новых файлов или если они отсутствуют
		if m.Checksum == "" {
			isImage := mediaType == storage.MediaTypeImage || mediaType == storage.MediaTypeRaw
			hashes, err := scanner.CalculateHashes(event.Path, isImage)
			if err != nil {
				logger.InfoLog.Printf("Watcher: failed to calculate hashes for %s: %v", event.Path, err)
			} else {
				m.Checksum = hashes.Checksum
				m.ImageHash = hashes.ImageHash
				logger.InfoLog.Printf("Watcher: hashes calculated for %s", event.Path)
			}
		}

		// Проверяем на дубликаты (только для новых файлов)
		// Гибридный подход: 1) размер ±10%, 2) SHA256, 3) pHash
		if existingMedia == nil {
			isImage := mediaType == storage.MediaTypeImage || mediaType == storage.MediaTypeRaw
			dupResult, err := store.CheckDuplicate(m.Size, m.Checksum, m.ImageHash, isImage, defaultDuplicateSimilarityThreshold)
			if err != nil {
				logger.InfoLog.Printf("Watcher: failed to check duplicates for %s: %v", event.Path, err)
			} else if dupResult.IsDuplicate {
				// Это дубликат - сохраняем с пометкой и перемещаем в корзину
				m.DuplicateOf = dupResult.ExistingID

				if err := store.SaveMedia(m); err != nil {
					logger.InfoLog.Printf("Watcher: failed to save duplicate %s: %v", event.Path, err)
					return
				}

				// Перемещаем в корзину (удалится через 30 дней)
				if err := store.SoftDeleteMedia(m.ID); err != nil {
					logger.InfoLog.Printf("Watcher: failed to trash duplicate %s: %v", event.Path, err)
				}

				// Генерируем превью чтобы было видно в корзине
				thumbService.QueueThumbnail(m.ID, media.ThumbnailSizeSmall)

				if dupResult.Type == storage.DuplicateTypeExact {
					logger.InfoLog.Printf("Watcher: duplicate moved to trash %s (exact copy of %s)", event.Path, dupResult.ExistingID)
				} else {
					logger.InfoLog.Printf("Watcher: duplicate moved to trash %s (similar to %s, distance=%d)", event.Path, dupResult.ExistingID, dupResult.Distance)
				}
				return
			}
		}

		if err := store.SaveMedia(m); err != nil {
			logger.InfoLog.Printf("Watcher: failed to save media %s: %v", event.Path, err)
			return
		}

		// Инвалидируем кэш
		mediaCache.InvalidateDir(m.Dir)
		mediaCache.InvalidateStats()

		// Ставим в очередь генерацию превью
		thumbService.QueueThumbnail(m.ID, media.ThumbnailSizeSmall)

		logger.InfoLog.Printf("Watcher: indexed new file %s", event.Path)

	case scanner.FileOpDelete:
		id := storage.GenerateID(event.Path)
		m, _ := store.GetMedia(id)
		if m != nil {
			// Удаляем thumbnails
			thumbGen.DeleteThumbnails(id)
			// Удаляем из БД
			store.DeleteMedia(id)
			// Инвалидируем кэши
			mediaCache.DeleteMedia(id)
			mediaCache.InvalidateDir(m.Dir)
			mediaCache.InvalidateStats()
			logger.InfoLog.Printf("Watcher: removed %s (+ thumbnails)", event.Path)
		}

	case scanner.FileOpRename:
		// Rename может быть:
		// 1. Файл переименован/удален - обрабатываем как удаление
		// 2. SFTP atomic write - временный файл переименован в целевой
		// Проверяем существует ли файл
		if _, err := os.Stat(event.Path); os.IsNotExist(err) {
			// Файл не существует - это удаление
			id := storage.GenerateID(event.Path)
			m, _ := store.GetMedia(id)
			if m != nil {
				thumbGen.DeleteThumbnails(id)
				store.DeleteMedia(id)
				mediaCache.DeleteMedia(id)
				mediaCache.InvalidateDir(m.Dir)
				mediaCache.InvalidateStats()
				logger.InfoLog.Printf("Watcher: removed %s (+ thumbnails)", event.Path)
			}
		}
		// Если файл существует - скорее всего другое событие (create/modify) уже обработает его
	}
}

// cleanupTrash удаляет файлы из корзины старше 30 дней
func cleanupTrash(store *storage.Store, thumbGen *media.ThumbnailGenerator) {
	trashMedia, err := store.ListTrashMedia()
	if err != nil {
		logger.InfoLog.Printf("Trash cleanup: failed to list trash: %v", err)
		return
	}

	if len(trashMedia) == 0 {
		return
	}

	const retentionDays = 30
	cutoff := time.Now().AddDate(0, 0, -retentionDays)
	var deleted int

	for _, m := range trashMedia {
		if m.DeletedAt != nil && m.DeletedAt.Before(cutoff) {
			// Удаляем физический файл с диска
			if err := os.Remove(m.Path); err != nil && !os.IsNotExist(err) {
				logger.InfoLog.Printf("Trash cleanup: failed to delete file %s: %v", m.Path, err)
			}

			// Удаляем thumbnails
			thumbGen.DeleteThumbnails(m.ID)

			// Удаляем из БД
			if err := store.DeleteMedia(m.ID); err != nil {
				logger.InfoLog.Printf("Trash cleanup: failed to delete %s: %v", m.ID, err)
				continue
			}
			deleted++
		}
	}

	if deleted > 0 {
		logger.InfoLog.Printf("Trash cleanup: permanently deleted %d files older than %d days", deleted, retentionDays)
	}
}
