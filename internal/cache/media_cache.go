package cache

import (
	"time"

	"github.com/photocore/photocore/internal/storage"
)

// MediaCache специализированный кэш для медиа-данных
type MediaCache struct {
	mediaCache *Cache[*storage.Media]
	dirCache   *Cache[[]*storage.Media]
	statsCache *Cache[*storage.Stats]
}

const (
	mediaCachePrefix = "media:"
	dirCachePrefix   = "dir:"
	statsCacheKey    = "stats"
)

// NewMediaCache создает новый медиа-кэш
func NewMediaCache() *MediaCache {
	return &MediaCache{
		mediaCache: New(Config[*storage.Media]{
			DefaultExpiration: 10 * time.Minute,
			CleanupInterval:   5 * time.Minute,
			MaxItems:          5000,
		}),
		dirCache: New(Config[[]*storage.Media]{
			DefaultExpiration: 2 * time.Minute,
			CleanupInterval:   1 * time.Minute,
			MaxItems:          500,
		}),
		statsCache: New(Config[*storage.Stats]{
			DefaultExpiration: 30 * time.Second,
			CleanupInterval:   1 * time.Minute,
			MaxItems:          10,
		}),
	}
}

// GetMedia получает медиа из кэша
func (mc *MediaCache) GetMedia(id string) (*storage.Media, bool) {
	return mc.mediaCache.Get(mediaCachePrefix + id)
}

// SetMedia сохраняет медиа в кэш
func (mc *MediaCache) SetMedia(media *storage.Media) {
	mc.mediaCache.Set(mediaCachePrefix+media.ID, media)
}

// DeleteMedia удаляет медиа из кэша
func (mc *MediaCache) DeleteMedia(id string) {
	mc.mediaCache.Delete(mediaCachePrefix + id)
}

// GetMediaByDir получает список медиа для директории
func (mc *MediaCache) GetMediaByDir(dir string) ([]*storage.Media, bool) {
	return mc.dirCache.Get(dirCachePrefix + dir)
}

// SetMediaByDir сохраняет список медиа для директории
func (mc *MediaCache) SetMediaByDir(dir string, media []*storage.Media) {
	mc.dirCache.Set(dirCachePrefix+dir, media)
}

// InvalidateDir инвалидирует кэш директории
func (mc *MediaCache) InvalidateDir(dir string) {
	mc.dirCache.Delete(dirCachePrefix + dir)
}

// GetStats получает статистику из кэша
func (mc *MediaCache) GetStats() (*storage.Stats, bool) {
	return mc.statsCache.Get(statsCacheKey)
}

// SetStats сохраняет статистику в кэш
func (mc *MediaCache) SetStats(stats *storage.Stats) {
	mc.statsCache.Set(statsCacheKey, stats)
}

// InvalidateStats инвалидирует кэш статистики
func (mc *MediaCache) InvalidateStats() {
	mc.statsCache.Delete(statsCacheKey)
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
