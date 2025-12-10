package cache

import (
	"time"

	"github.com/photocore/photocore/internal/storage"
)

// MediaCache специализированный кэш для медиа-данных
type MediaCache struct {
	// Кэш метаданных медиа
	mediaCache *Cache

	// Кэш списков медиа по директориям
	dirCache *Cache

	// Кэш статистики
	statsCache *Cache
}

// NewMediaCache создает новый медиа-кэш
func NewMediaCache() *MediaCache {
	return &MediaCache{
		mediaCache: New(Config{
			DefaultExpiration: 10 * time.Minute,
			CleanupInterval:   5 * time.Minute,
			MaxItems:          5000,
		}),
		dirCache: New(Config{
			DefaultExpiration: 2 * time.Minute,
			CleanupInterval:   1 * time.Minute,
			MaxItems:          500,
		}),
		statsCache: New(Config{
			DefaultExpiration: 30 * time.Second,
			CleanupInterval:   1 * time.Minute,
			MaxItems:          10,
		}),
	}
}

// GetMedia получает медиа из кэша
func (mc *MediaCache) GetMedia(id string) (*storage.Media, bool) {
	val, found := mc.mediaCache.Get("media:" + id)
	if !found {
		return nil, false
	}
	if media, ok := val.(*storage.Media); ok {
		return media, true
	}
	return nil, false
}

// SetMedia сохраняет медиа в кэш
func (mc *MediaCache) SetMedia(media *storage.Media) {
	mc.mediaCache.Set("media:"+media.ID, media)
}

// DeleteMedia удаляет медиа из кэша
func (mc *MediaCache) DeleteMedia(id string) {
	mc.mediaCache.Delete("media:" + id)
}

// GetMediaByDir получает список медиа для директории
func (mc *MediaCache) GetMediaByDir(dir string) ([]*storage.Media, bool) {
	val, found := mc.dirCache.Get("dir:" + dir)
	if !found {
		return nil, false
	}
	if media, ok := val.([]*storage.Media); ok {
		return media, true
	}
	return nil, false
}

// SetMediaByDir сохраняет список медиа для директории
func (mc *MediaCache) SetMediaByDir(dir string, media []*storage.Media) {
	mc.dirCache.Set("dir:"+dir, media)
}

// InvalidateDir инвалидирует кэш директории
func (mc *MediaCache) InvalidateDir(dir string) {
	mc.dirCache.Delete("dir:" + dir)
}

// GetStats получает статистику из кэша
func (mc *MediaCache) GetStats() (*storage.Stats, bool) {
	val, found := mc.statsCache.Get("stats")
	if !found {
		return nil, false
	}
	if stats, ok := val.(*storage.Stats); ok {
		return stats, true
	}
	return nil, false
}

// SetStats сохраняет статистику в кэш
func (mc *MediaCache) SetStats(stats *storage.Stats) {
	mc.statsCache.Set("stats", stats)
}

// InvalidateStats инвалидирует кэш статистики
func (mc *MediaCache) InvalidateStats() {
	mc.statsCache.Delete("stats")
}

// Clear очищает все кэши
func (mc *MediaCache) Clear() {
	mc.mediaCache.Clear()
	mc.dirCache.Clear()
	mc.statsCache.Clear()
}

// Stop останавливает все кэши
func (mc *MediaCache) Stop() {
	mc.mediaCache.Stop()
	mc.dirCache.Stop()
	mc.statsCache.Stop()
}

// Stats возвращает общую статистику кэшей
func (mc *MediaCache) Stats() map[string]CacheStats {
	return map[string]CacheStats{
		"media": mc.mediaCache.Stats(),
		"dir":   mc.dirCache.Stats(),
		"stats": mc.statsCache.Stats(),
	}
}
