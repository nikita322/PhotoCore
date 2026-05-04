package cache

import (
	"sync"
	"time"
)

// Item представляет элемент кэша с типизированным значением
type Item[T any] struct {
	Value      T
	Expiration int64
}

// IsExpired проверяет, истек ли срок жизни элемента
func (i *Item[T]) IsExpired() bool {
	if i.Expiration == 0 {
		return false
	}
	return time.Now().UnixNano() > i.Expiration
}

// Cache представляет типизированный in-memory кэш с TTL
type Cache[T any] struct {
	items             map[string]*Item[T]
	mu                sync.RWMutex
	defaultExpiration time.Duration
	cleanupInterval   time.Duration
	stopCleanup       chan struct{}
	maxItems          int
	onEvicted         func(key string, value T)
}

// Config конфигурация кэша
type Config[T any] struct {
	DefaultExpiration time.Duration
	CleanupInterval   time.Duration
	MaxItems          int
	OnEvicted         func(key string, value T)
}

const (
	defaultExpiration      = 5 * time.Minute
	defaultCleanupInterval = 10 * time.Minute
	defaultMaxItems        = 10000
)

// New создает новый типизированный кэш
func New[T any](config Config[T]) *Cache[T] {
	if config.DefaultExpiration == 0 {
		config.DefaultExpiration = defaultExpiration
	}
	if config.CleanupInterval == 0 {
		config.CleanupInterval = defaultCleanupInterval
	}
	if config.MaxItems == 0 {
		config.MaxItems = defaultMaxItems
	}

	c := &Cache[T]{
		items:             make(map[string]*Item[T]),
		defaultExpiration: config.DefaultExpiration,
		cleanupInterval:   config.CleanupInterval,
		stopCleanup:       make(chan struct{}),
		maxItems:          config.MaxItems,
		onEvicted:         config.OnEvicted,
	}

	go c.cleanupLoop()

	return c
}

// Set добавляет элемент в кэш с TTL по умолчанию
func (c *Cache[T]) Set(key string, value T) {
	c.SetWithTTL(key, value, c.defaultExpiration)
}

// SetWithTTL добавляет элемент с указанным TTL
func (c *Cache[T]) SetWithTTL(key string, value T, ttl time.Duration) {
	var expiration int64
	if ttl > 0 {
		expiration = time.Now().Add(ttl).UnixNano()
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if len(c.items) >= c.maxItems {
		c.evictOldest()
	}

	c.items[key] = &Item[T]{
		Value:      value,
		Expiration: expiration,
	}
}

// Get получает элемент из кэша
func (c *Cache[T]) Get(key string) (T, bool) {
	c.mu.RLock()
	item, found := c.items[key]
	c.mu.RUnlock()

	if !found {
		var zero T
		return zero, false
	}

	if item.IsExpired() {
		c.Delete(key)
		var zero T
		return zero, false
	}

	return item.Value, true
}

// GetOrSet получает элемент или создает новый через функцию
func (c *Cache[T]) GetOrSet(key string, fn func() (T, error)) (T, error) {
	if val, found := c.Get(key); found {
		return val, nil
	}

	val, err := fn()
	if err != nil {
		var zero T
		return zero, err
	}

	c.Set(key, val)
	return val, nil
}

// Delete удаляет элемент из кэша
func (c *Cache[T]) Delete(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if item, found := c.items[key]; found {
		if c.onEvicted != nil {
			c.onEvicted(key, item.Value)
		}
		delete(c.items, key)
	}
}

// Clear очищает кэш
func (c *Cache[T]) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.onEvicted != nil {
		for key, item := range c.items {
			c.onEvicted(key, item.Value)
		}
	}

	c.items = make(map[string]*Item[T])
}

// Count возвращает количество элементов в кэше
func (c *Cache[T]) Count() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.items)
}

// Keys возвращает все ключи
func (c *Cache[T]) Keys() []string {
	c.mu.RLock()
	defer c.mu.RUnlock()

	keys := make([]string, 0, len(c.items))
	for k := range c.items {
		keys = append(keys, k)
	}
	return keys
}

// Stop останавливает фоновую очистку
func (c *Cache[T]) Stop() {
	close(c.stopCleanup)
}

// CacheStats статистика кэша
type CacheStats struct {
	Items        int `json:"items"`
	MaxItems     int `json:"max_items"`
	ExpiredItems int `json:"expired_items"`
}

// Stats возвращает статистику кэша
func (c *Cache[T]) Stats() CacheStats {
	c.mu.RLock()
	defer c.mu.RUnlock()

	expired := 0
	for _, item := range c.items {
		if item.IsExpired() {
			expired++
		}
	}

	return CacheStats{
		Items:        len(c.items),
		MaxItems:     c.maxItems,
		ExpiredItems: expired,
	}
}

func (c *Cache[T]) cleanupLoop() {
	ticker := time.NewTicker(c.cleanupInterval)
	defer ticker.Stop()

	for {
		select {
		case <-c.stopCleanup:
			return
		case <-ticker.C:
			c.deleteExpired()
		}
	}
}

func (c *Cache[T]) deleteExpired() {
	c.mu.Lock()
	defer c.mu.Unlock()

	for key, item := range c.items {
		if item.IsExpired() {
			if c.onEvicted != nil {
				c.onEvicted(key, item.Value)
			}
			delete(c.items, key)
		}
	}
}

func (c *Cache[T]) evictOldest() {
	var keyToDelete string

	for key, item := range c.items {
		if item.IsExpired() {
			keyToDelete = key
			break
		}
		if keyToDelete == "" {
			keyToDelete = key
		}
	}

	if keyToDelete != "" {
		if c.onEvicted != nil {
			c.onEvicted(keyToDelete, c.items[keyToDelete].Value)
		}
		delete(c.items, keyToDelete)
	}
}
