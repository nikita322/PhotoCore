package worker

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/photocore/photocore/internal/logger"
	"github.com/photocore/photocore/internal/media"
	"github.com/photocore/photocore/internal/storage"
)

// ThumbnailService управляет генерацией превью
type ThumbnailService struct {
	pool     *Pool
	store    *storage.Store
	thumbGen *media.ThumbnailGenerator

	// Отслеживание задач в процессе
	mu         sync.RWMutex
	processing map[string]bool   // mediaID+size -> in progress
	failed     map[string]string // mediaID+size -> error message (постоянные ошибки)
}

// NewThumbnailService создает новый сервис генерации превью
func NewThumbnailService(pool *Pool, store *storage.Store, thumbGen *media.ThumbnailGenerator) *ThumbnailService {
	svc := &ThumbnailService{
		pool:       pool,
		store:      store,
		thumbGen:   thumbGen,
		processing: make(map[string]bool),
		failed:     make(map[string]string),
	}

	// Регистрируем обработчик
	pool.RegisterHandler(TaskGenerateThumbnail, svc.handleThumbnail)

	return svc
}

// QueueThumbnail добавляет задачу на генерацию превью
func (s *ThumbnailService) QueueThumbnail(mediaID, size string) bool {
	key := mediaID + ":" + size

	s.mu.Lock()
	// Проверяем, не было ли постоянной ошибки ранее
	if _, hasFailed := s.failed[key]; hasFailed {
		s.mu.Unlock()
		return false // Постоянная ошибка - не пытаемся снова
	}
	if s.processing[key] {
		s.mu.Unlock()
		return false // Уже в очереди
	}
	s.processing[key] = true
	s.mu.Unlock()

	task := &Task{
		ID:        generateTaskID(),
		Type:      TaskGenerateThumbnail,
		Priority:  PriorityNormal,
		MediaID:   mediaID,
		Size:      size,
		CreatedAt: time.Now(),
	}

	if !s.pool.Submit(task) {
		s.mu.Lock()
		delete(s.processing, key)
		s.mu.Unlock()
		return false
	}

	return true
}

// QueueAllThumbnails добавляет задачи на генерацию всех превью для медиа
func (s *ThumbnailService) QueueAllThumbnails(mediaID string) {
	for _, size := range []string{"small", "medium", "large"} {
		s.QueueThumbnail(mediaID, size)
	}
}

// QueueBatch добавляет пакет задач
func (s *ThumbnailService) QueueBatch(mediaIDs []string, size string) int {
	queued := 0
	for _, id := range mediaIDs {
		if s.QueueThumbnail(id, size) {
			queued++
		}
	}
	return queued
}

// PregeneateThumbnails запускает генерацию превью для всех медиа без превью
func (s *ThumbnailService) PregenerateThumbnails() error {
	allMedia, err := s.store.ListAllMedia()
	if err != nil {
		return fmt.Errorf("failed to list media: %w", err)
	}

	queued := 0
	for _, m := range allMedia {
		// Проверяем, существует ли превью
		if !s.thumbGen.ThumbnailExists(m.ID, "small") {
			if s.QueueThumbnail(m.ID, "small") {
				queued++
			}
		}
	}

	logger.InfoLog.Printf("Queued %d thumbnail generation tasks", queued)
	return nil
}

func (s *ThumbnailService) handleThumbnail(ctx context.Context, task *Task) (*TaskResult, error) {
	key := task.MediaID + ":" + task.Size
	defer func() {
		// Очищаем processing только если не было постоянной ошибки
		// (markAsFailed сам очищает processing)
		s.mu.Lock()
		if _, failed := s.failed[key]; !failed {
			delete(s.processing, key)
		}
		s.mu.Unlock()
	}()

	// Получаем медиа из БД
	m, err := s.store.GetMedia(task.MediaID)
	if err != nil {
		return nil, fmt.Errorf("failed to get media: %w", err)
	}
	if m == nil {
		return nil, fmt.Errorf("media not found: %s", task.MediaID)
	}

	// Проверяем контекст
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	// Генерируем превью
	start := time.Now()
	logger.InfoLog.Printf("Generating thumbnail for %s/%s (file: %s)", task.MediaID[:16], task.Size, m.Filename)
	thumbPath, err := s.thumbGen.GenerateThumbnail(m, task.Size)
	duration := time.Since(start)

	if err != nil {
		logger.InfoLog.Printf("ERROR: Failed to generate thumbnail for %s/%s: %v", task.MediaID[:16], task.Size, err)

		// Проверяем, является ли ошибка постоянной
		if isPermanentError(err) {
			s.markAsFailed(task.MediaID, task.Size, err.Error())
		}

		return &TaskResult{
			TaskID:   task.ID,
			Success:  false,
			Error:    err,
			Duration: duration,
		}, err
	}

	logger.InfoLog.Printf("SUCCESS: Generated thumbnail for %s/%s in %v -> %s", task.MediaID[:16], task.Size, duration, thumbPath)

	// Обновляем путь к превью в БД
	switch task.Size {
	case "small":
		m.ThumbSmall = thumbPath
	case "large":
		m.ThumbLarge = thumbPath
	}

	if err := s.store.SaveMedia(m); err != nil {
		logger.InfoLog.Printf("Failed to update media thumbnail path: %v", err)
	}

	return &TaskResult{
		TaskID:     task.ID,
		Success:    true,
		Duration:   duration,
		OutputPath: thumbPath,
	}, nil
}

// IsProcessing проверяет, обрабатывается ли медиа
func (s *ThumbnailService) IsProcessing(mediaID, size string) bool {
	key := mediaID + ":" + size
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.processing[key]
}

// ProcessingCount возвращает количество задач в обработке
func (s *ThumbnailService) ProcessingCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.processing)
}

// HasFailed проверяет, была ли постоянная ошибка при генерации превью
func (s *ThumbnailService) HasFailed(mediaID, size string) (bool, string) {
	key := mediaID + ":" + size
	s.mu.RLock()
	defer s.mu.RUnlock()
	errMsg, exists := s.failed[key]
	return exists, errMsg
}

// markAsFailed помечает превью как постоянно неудачное
func (s *ThumbnailService) markAsFailed(mediaID, size, errMsg string) {
	key := mediaID + ":" + size
	s.mu.Lock()
	s.failed[key] = errMsg
	delete(s.processing, key)
	s.mu.Unlock()
	logger.InfoLog.Printf("Marked %s/%s as permanently failed: %s", mediaID[:16], size, errMsg)
}

// isPermanentError проверяет, является ли ошибка постоянной (не retry)
func isPermanentError(err error) bool {
	if err == nil {
		return false
	}
	errStr := err.Error()
	// Постоянные ошибки - формат не поддерживается, файл поврежден и т.д.
	permanentErrors := []string{
		"unknown format",
		"unsupported media type",
		"image: unknown format",
		"invalid JPEG",
		"invalid PNG",
		"corrupt",
		"format detection failed",
		"unsupported format:",
	}
	for _, pe := range permanentErrors {
		if strings.Contains(errStr, pe) {
			return true
		}
	}
	return false
}

func generateTaskID() string {
	data := fmt.Sprintf("%d-%d", time.Now().UnixNano(), time.Now().Nanosecond())
	hash := sha256.Sum256([]byte(data))
	return hex.EncodeToString(hash[:8])
}
