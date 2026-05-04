package worker

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sync"
	"time"

	"github.com/photocore/photocore/internal/logger"
	"github.com/photocore/photocore/internal/media"
	"github.com/photocore/photocore/internal/storage"
)

// ThumbnailService управляет генерацией превью через два пула: быстрый и медленный
type ThumbnailService struct {
	manager    *PoolManager
	store      *storage.Store
	thumbGen   *media.ThumbnailGenerator

	// Отслеживание задач в процессе
	mu         sync.RWMutex
	processing map[string]bool   // mediaID+size -> in progress
	failed     map[string]string // mediaID+size -> error message (постоянные ошибки)
}

// NewThumbnailService создает новый сервис генерации превью
func NewThumbnailService(manager *PoolManager, store *storage.Store, thumbGen *media.ThumbnailGenerator) *ThumbnailService {
	svc := &ThumbnailService{
		manager:    manager,
		store:      store,
		thumbGen:   thumbGen,
		processing: make(map[string]bool),
		failed:     make(map[string]string),
	}

	// Регистрируем обработчик в оба пула
	manager.Fast.RegisterHandler(TaskGenerateThumbnail, svc.handleThumbnail)
	manager.Slow.RegisterHandler(TaskGenerateThumbnail, svc.handleThumbnail)

	return svc
}

const taskKeySeparator = ":"

// QueueThumbnail добавляет задачу на генерацию превью с нормальным приоритетом
func (s *ThumbnailService) QueueThumbnail(mediaID string, size media.ThumbnailSize) bool {
	return s.queueThumbnailInternal(mediaID, size, false)
}

// QueueThumbnailOnDemand добавляет задачу на генерацию превью с высоким приоритетом (для UI)
func (s *ThumbnailService) QueueThumbnailOnDemand(mediaID string, size media.ThumbnailSize) bool {
	return s.queueThumbnailInternal(mediaID, size, true)
}

func (s *ThumbnailService) queueThumbnailInternal(mediaID string, size media.ThumbnailSize, onDemand bool) bool {
	key := mediaID + taskKeySeparator + string(size)

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
		MediaID:   mediaID,
		Size:      size,
		CreatedAt: time.Now(),
	}

	// Выбираем пул и приоритет в зависимости от размера и типа запроса
	var submitted bool
	switch size {
	case media.ThumbnailSizeSmall:
		if onDemand {
			task.Priority = PriorityHigh
		} else {
			task.Priority = PriorityNormal
		}
		submitted = s.manager.Fast.Submit(task)
	case media.ThumbnailSizeMedium, media.ThumbnailSizeLarge:
		task.Priority = PriorityLow
		submitted = s.manager.Slow.Submit(task)
	}

	if !submitted {
		s.mu.Lock()
		delete(s.processing, key)
		s.mu.Unlock()
		return false
	}

	return true
}

// QueueAllThumbnails добавляет задачи на генерацию всех превью для медиа
func (s *ThumbnailService) QueueAllThumbnails(mediaID string) {
	s.QueueThumbnail(mediaID, media.ThumbnailSizeSmall)
	s.QueueThumbnail(mediaID, media.ThumbnailSizeMedium)
	s.QueueThumbnail(mediaID, media.ThumbnailSizeLarge)
}

// QueueBatch добавляет пакет задач
func (s *ThumbnailService) QueueBatch(mediaIDs []string, size media.ThumbnailSize) int {
	queued := 0
	for _, id := range mediaIDs {
		if s.QueueThumbnail(id, size) {
			queued++
		}
	}
	return queued
}

// PregenerateThumbnails запускает генерацию small превью для всех медиа без превью
func (s *ThumbnailService) PregenerateThumbnails() error {
	allMedia, err := s.store.ListAllMedia()
	if err != nil {
		return fmt.Errorf("failed to list media: %w", err)
	}

	queued := 0
	for _, m := range allMedia {
		// Проверяем, существует ли small превью
		if !s.thumbGen.ThumbnailExists(m.ID, media.ThumbnailSizeSmall) {
			if s.QueueThumbnail(m.ID, media.ThumbnailSizeSmall) {
				queued++
			}
		}
	}

	logger.InfoLog.Printf("Queued %d small thumbnail generation tasks", queued)
	return nil
}

func (s *ThumbnailService) handleThumbnail(ctx context.Context, task *Task) (*TaskResult, error) {
	key := task.MediaID + taskKeySeparator + string(task.Size)
	defer func() {
		// Очищаем processing только если не было постоянной ошибки
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
		if IsPermanentError(err) {
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
	case media.ThumbnailSizeSmall:
		m.ThumbSmall = thumbPath
	case media.ThumbnailSizeLarge:
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
func (s *ThumbnailService) IsProcessing(mediaID string, size media.ThumbnailSize) bool {
	key := mediaID + taskKeySeparator + string(size)
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
func (s *ThumbnailService) HasFailed(mediaID string, size media.ThumbnailSize) (bool, string) {
	key := mediaID + taskKeySeparator + string(size)
	s.mu.RLock()
	defer s.mu.RUnlock()
	errMsg, exists := s.failed[key]
	return exists, errMsg
}

// markAsFailed помечает превью как постоянно неудачное
func (s *ThumbnailService) markAsFailed(mediaID string, size media.ThumbnailSize, errMsg string) {
	key := mediaID + taskKeySeparator + string(size)
	s.mu.Lock()
	s.failed[key] = errMsg
	delete(s.processing, key)
	s.mu.Unlock()
	logger.InfoLog.Printf("Marked %s/%s as permanently failed: %s", mediaID[:16], size, errMsg)
}

func generateTaskID() string {
	data := fmt.Sprintf("%d-%d", time.Now().UnixNano(), time.Now().Nanosecond())
	hash := sha256.Sum256([]byte(data))
	return hex.EncodeToString(hash[:8])
}
