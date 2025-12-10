package cache

import (
	"sync"
	"time"
)

// Item представляет элемент кэша
type Item struct {
	Value      interface{}
	Expiration int64
}

// IsExpired проверяет, истек ли срок жизни элемента
func (i *Item) IsExpired() bool {
	if i.Expiration == 0 {
		return false
	}
	return time.Now().UnixNano() > i.Expiration
}

// Cache представляет in-memory кэш с TTL
type Cache struct {
	items             map[string]*Item
	mu                sync.RWMutex
	defaultExpiration time.Duration
	cleanupInterval   time.Duration
	stopCleanup       chan struct{}
	maxItems          int
	onEvicted         func(key string, value interface{})
}

// Config конфигурация кэша
type Config struct {
	DefaultExpiration time.Duration
	CleanupInterval   time.Duration
	MaxItems          int
	OnEvicted         func(key string, value interface{})
}

// New создает новый кэш
func New(config Config) *Cache {
	if config.DefaultExpiration == 0 {
		config.DefaultExpiration = 5 * time.Minute
	}
	if config.CleanupInterval == 0 {
		config.CleanupInterval = 10 * time.Minute
	}
	if config.MaxItems == 0 {
		config.MaxItems = 10000
	}

	c := &Cache{
		items:             make(map[string]*Item),
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
func (c *Cache) Set(key string, value interface{}) {
	c.SetWithTTL(key, value, c.defaultExpiration)
}

// SetWithTTL добавляет элемент с указанным TTL
func (c *Cache) SetWithTTL(key string, value interface{}, ttl time.Duration) {
	var expiration int64
	if ttl > 0 {
		expiration = time.Now().Add(ttl).UnixNano()
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	// Проверяем лимит и удаляем старые элементы если нужно
	if len(c.items) >= c.maxItems {
		c.evictOldest()
	}

	c.items[key] = &Item{
		Value:      value,
		Expiration: expiration,
	}
}

// Get получает элемент из кэша
func (c *Cache) Get(key string) (interface{}, bool) {
	c.mu.RLock()
	item, found := c.items[key]
	c.mu.RUnlock()

	if !found {
		return nil, false
	}

	if item.IsExpired() {
		c.Delete(key)
		return nil, false
	}

	return item.Value, true
}

// GetOrSet получает элемент или создает новый через функцию
func (c *Cache) GetOrSet(key string, fn func() (interface{}, error)) (interface{}, error) {
	if val, found := c.Get(key); found {
		return val, nil
	}

	val, err := fn()
	if err != nil {
		return nil, err
	}

	c.Set(key, val)
	return val, nil
}

// Delete удаляет элемент из кэша
func (c *Cache) Delete(key string) {
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
func (c *Cache) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.onEvicted != nil {
		for key, item := range c.items {
			c.onEvicted(key, item.Value)
		}
	}

	c.items = make(map[string]*Item)
}

// Count возвращает количество элементов в кэше
func (c *Cache) Count() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.items)
}

// Keys возвращает все ключи
func (c *Cache) Keys() []string {
	c.mu.RLock()
	defer c.mu.RUnlock()

	keys := make([]string, 0, len(c.items))
	for k := range c.items {
		keys = append(keys, k)
	}
	return keys
}

// Stop останавливает фоновую очистку
func (c *Cache) Stop() {
	close(c.stopCleanup)
}

// Stats возвращает статистику кэша
func (c *Cache) Stats() CacheStats {
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

// CacheStats статистика кэша
type CacheStats struct {
	Items        int `json:"items"`
	MaxItems     int `json:"max_items"`
	ExpiredItems int `json:"expired_items"`
}

func (c *Cache) cleanupLoop() {
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

func (c *Cache) deleteExpired() {
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

func (c *Cache) evictOldest() {
	// Простая стратегия: удаляем первый найденный просроченный
	// или любой элемент если просроченных нет
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
