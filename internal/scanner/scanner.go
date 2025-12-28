package scanner

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/photocore/photocore/internal/config"
	"github.com/photocore/photocore/internal/logger"
	"github.com/photocore/photocore/internal/storage"
)

// Scanner сканирует файловую систему для поиска медиа-файлов
type Scanner struct {
	cfg   *config.Config
	store *storage.Store

	mu        sync.RWMutex
	scanning  bool
	progress  ScanProgress
	stopChan  chan struct{}
}

// ScanProgress содержит информацию о прогрессе сканирования
type ScanProgress struct {
	Running           bool      `json:"running"`
	StartedAt         time.Time `json:"started_at"`
	TotalFiles        int       `json:"total_files"`
	Scanned           int       `json:"scanned"`
	NewFiles          int       `json:"new_files"`
	UpdatedFiles      int       `json:"updated_files"`
	SkippedDuplicates int       `json:"skipped_duplicates"`
	Errors            int       `json:"errors"`
	CurrentPath       string    `json:"current_path"`
}

// NewScanner создает новый сканер
func NewScanner(cfg *config.Config, store *storage.Store) *Scanner {
	return &Scanner{
		cfg:      cfg,
		store:    store,
		stopChan: make(chan struct{}),
	}
}

// Start запускает сканирование всех медиа-путей
func (s *Scanner) Start() error {
	s.mu.Lock()
	if s.scanning {
		s.mu.Unlock()
		return fmt.Errorf("scan already in progress")
	}
	s.scanning = true
	s.progress = ScanProgress{
		Running:   true,
		StartedAt: time.Now(),
	}
	s.mu.Unlock()

	go s.scan()
	return nil
}

// Stop останавливает текущее сканирование
func (s *Scanner) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.scanning {
		close(s.stopChan)
		s.stopChan = make(chan struct{})
	}
}

// Progress возвращает текущий прогресс сканирования
func (s *Scanner) Progress() ScanProgress {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.progress
}

// IsScanning возвращает true, если сканирование в процессе
func (s *Scanner) IsScanning() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.scanning
}

func (s *Scanner) scan() {
	defer func() {
		s.mu.Lock()
		s.scanning = false
		s.progress.Running = false
		s.mu.Unlock()
	}()

	extensions := make(map[string]storage.MediaType)
	for _, ext := range s.cfg.Scan.Extensions.Images {
		extensions[strings.ToLower(ext)] = storage.MediaTypeImage
	}
	for _, ext := range s.cfg.Scan.Extensions.Videos {
		extensions[strings.ToLower(ext)] = storage.MediaTypeVideo
	}
	for _, ext := range s.cfg.Scan.Extensions.Raw {
		extensions[strings.ToLower(ext)] = storage.MediaTypeRaw
	}

	for _, mediaPath := range s.cfg.Storage.MediaPaths {
		select {
		case <-s.stopChan:
			return
		default:
		}

		absPath, err := filepath.Abs(mediaPath)
		if err != nil {
			logger.InfoLog.Printf("Error resolving path %s: %v", mediaPath, err)
			continue
		}

		err = filepath.Walk(absPath, func(path string, info os.FileInfo, err error) error {
			select {
			case <-s.stopChan:
				return fmt.Errorf("scan stopped")
			default:
			}

			if err != nil {
				s.mu.Lock()
				s.progress.Errors++
				s.mu.Unlock()
				return nil // Продолжаем сканирование
			}

			if info.IsDir() {
				return nil
			}

			ext := strings.ToLower(filepath.Ext(path))
			mediaType, ok := extensions[ext]
			if !ok {
				return nil // Не медиа-файл
			}

			s.mu.Lock()
			s.progress.TotalFiles++
			s.progress.Scanned++
			s.progress.CurrentPath = path
			s.mu.Unlock()

			// Проверяем, есть ли файл в БД
			existing, err := s.store.GetMediaByPath(path)
			if err != nil {
				logger.InfoLog.Printf("Error checking media %s: %v", path, err)
				s.mu.Lock()
				s.progress.Errors++
				s.mu.Unlock()
				return nil
			}

			// Если файл в корзине (soft-deleted), пропускаем
			if existing != nil && existing.DeletedAt != nil {
				return nil
			}

			// Если файл существует и не изменился, пропускаем
			if existing != nil && existing.ModifiedAt.Equal(info.ModTime()) && existing.Size == info.Size() {
				return nil
			}

			// Создаем или обновляем запись
			relPath, _ := filepath.Rel(absPath, path)
			media := &storage.Media{
				ID:         storage.GenerateID(path),
				Path:       path,
				RelPath:    relPath,
				Dir:        filepath.Dir(relPath),
				Filename:   info.Name(),
				Ext:        ext,
				Type:       mediaType,
				MimeType:   getMimeType(ext),
				Size:       info.Size(),
				ModifiedAt: info.ModTime(),
				CreatedAt:  time.Now(),
			}

			// Сохраняем важные поля из существующей записи
			if existing != nil {
				media.CreatedAt = existing.CreatedAt
				media.IsFavorite = existing.IsFavorite
				media.Tags = existing.Tags
				media.ThumbSmall = existing.ThumbSmall
				media.ThumbLarge = existing.ThumbLarge
				media.Checksum = existing.Checksum
				media.ImageHash = existing.ImageHash
				// Не перезаписываем метаданные, если они уже есть
				if existing.TakenAt.Year() > 1900 {
					media.TakenAt = existing.TakenAt
					media.Metadata = existing.Metadata
				}
			}

			// Извлекаем метаданные для изображений (только для новых файлов)
			if existing == nil && (mediaType == storage.MediaTypeImage || mediaType == storage.MediaTypeRaw) {
				if err := ExtractMetadata(path, media); err != nil {
					logger.InfoLog.Printf("Error extracting metadata from %s: %v", path, err)
				}
			}

			// Вычисляем хеши для новых файлов или если они отсутствуют
			if media.Checksum == "" {
				isImage := mediaType == storage.MediaTypeImage || mediaType == storage.MediaTypeRaw
				hashes, err := CalculateHashes(path, isImage)
				if err != nil {
					logger.InfoLog.Printf("Error calculating hashes for %s: %v", path, err)
				} else {
					media.Checksum = hashes.Checksum
					media.ImageHash = hashes.ImageHash
				}
			}

			// Проверяем на дубликаты (только для новых файлов)
			// Гибридный подход: 1) размер ±10%, 2) SHA256, 3) pHash
			if existing == nil {
				isImage := mediaType == storage.MediaTypeImage || mediaType == storage.MediaTypeRaw
				dupResult, err := s.store.CheckDuplicate(media.Size, media.Checksum, media.ImageHash, isImage, 10)
				if err != nil {
					logger.InfoLog.Printf("Error checking duplicates for %s: %v", path, err)
				} else if dupResult.IsDuplicate {
					// Это дубликат - сохраняем с пометкой и перемещаем в корзину
					media.DuplicateOf = dupResult.ExistingID

					if err := s.store.SaveMedia(media); err != nil {
						logger.InfoLog.Printf("Error saving duplicate %s: %v", path, err)
						return nil
					}

					// Перемещаем в корзину
					s.store.SoftDeleteMedia(media.ID)

					if dupResult.Type == "exact" {
						logger.InfoLog.Printf("Duplicate moved to trash: %s (exact copy of %s)", path, dupResult.ExistingID)
					} else {
						logger.InfoLog.Printf("Duplicate moved to trash: %s (similar to %s, distance=%d)", path, dupResult.ExistingID, dupResult.Distance)
					}

					s.mu.Lock()
					s.progress.SkippedDuplicates++
					s.mu.Unlock()
					return nil
				}
			}

			// Сохраняем в БД
			if err := s.store.SaveMedia(media); err != nil {
				logger.InfoLog.Printf("Error saving media %s: %v", path, err)
				s.mu.Lock()
				s.progress.Errors++
				s.mu.Unlock()
				return nil
			}

			s.mu.Lock()
			if existing == nil {
				s.progress.NewFiles++
			} else {
				s.progress.UpdatedFiles++
			}
			s.mu.Unlock()

			return nil
		})

		if err != nil {
			logger.InfoLog.Printf("Error walking path %s: %v", mediaPath, err)
		}
	}

	logger.InfoLog.Printf("Scan completed: %d files, %d new, %d updated, %d duplicates skipped, %d errors",
		s.progress.TotalFiles, s.progress.NewFiles, s.progress.UpdatedFiles, s.progress.SkippedDuplicates, s.progress.Errors)
}

func getMimeType(ext string) string {
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
