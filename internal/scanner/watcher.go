package scanner

import (
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"

	"github.com/photocore/photocore/internal/config"
	"github.com/photocore/photocore/internal/storage"
)

// FileEvent представляет событие файловой системы
type FileEvent struct {
	Path      string
	Operation string // create, modify, delete, rename
	IsDir     bool
	Time      time.Time
}

// EventHandler обрабатывает события файловой системы
type EventHandler func(event FileEvent)

// Watcher наблюдает за изменениями в файловой системе
type Watcher struct {
	cfg      *config.Config
	store    *storage.Store
	watcher  *fsnotify.Watcher
	handlers []EventHandler

	mu       sync.RWMutex
	running  bool
	stopChan chan struct{}

	// Debouncing - группировка событий
	pendingEvents map[string]*FileEvent
	debounceTimer *time.Timer
	debounceMu    sync.Mutex
}

// NewWatcher создает новый наблюдатель
func NewWatcher(cfg *config.Config, store *storage.Store) (*Watcher, error) {
	fsWatcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}

	return &Watcher{
		cfg:           cfg,
		store:         store,
		watcher:       fsWatcher,
		handlers:      make([]EventHandler, 0),
		stopChan:      make(chan struct{}),
		pendingEvents: make(map[string]*FileEvent),
	}, nil
}

// AddHandler добавляет обработчик событий
func (w *Watcher) AddHandler(handler EventHandler) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.handlers = append(w.handlers, handler)
}

// Start запускает наблюдение за директориями
func (w *Watcher) Start() error {
	w.mu.Lock()
	if w.running {
		w.mu.Unlock()
		return nil
	}
	w.running = true
	w.mu.Unlock()

	// Добавляем все медиа-директории
	for _, path := range w.cfg.Storage.MediaPaths {
		absPath, err := filepath.Abs(path)
		if err != nil {
			log.Printf("Watcher: error resolving path %s: %v", path, err)
			continue
		}

		if err := w.addRecursive(absPath); err != nil {
			log.Printf("Watcher: error adding path %s: %v", absPath, err)
		}
	}

	// Запускаем обработку событий
	go w.eventLoop()

	log.Println("File watcher started")
	return nil
}

// Stop останавливает наблюдение
func (w *Watcher) Stop() error {
	w.mu.Lock()
	if !w.running {
		w.mu.Unlock()
		return nil
	}
	w.running = false
	w.mu.Unlock()

	close(w.stopChan)
	w.watcher.Close()

	log.Println("File watcher stopped")
	return nil
}

// addRecursive добавляет директорию и все поддиректории
func (w *Watcher) addRecursive(root string) error {
	return filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // Продолжаем при ошибках доступа
		}

		if info.IsDir() {
			// Пропускаем скрытые директории
			if strings.HasPrefix(info.Name(), ".") && path != root {
				return filepath.SkipDir
			}

			if err := w.watcher.Add(path); err != nil {
				log.Printf("Watcher: failed to watch %s: %v", path, err)
			}
		}

		return nil
	})
}

func (w *Watcher) eventLoop() {
	for {
		select {
		case <-w.stopChan:
			return

		case event, ok := <-w.watcher.Events:
			if !ok {
				return
			}
			w.handleFSEvent(event)

		case err, ok := <-w.watcher.Errors:
			if !ok {
				return
			}
			log.Printf("Watcher error: %v", err)
		}
	}
}

func (w *Watcher) handleFSEvent(event fsnotify.Event) {
	// Определяем тип операции
	var op string
	switch {
	case event.Op&fsnotify.Create == fsnotify.Create:
		op = "create"
	case event.Op&fsnotify.Write == fsnotify.Write:
		op = "modify"
	case event.Op&fsnotify.Remove == fsnotify.Remove:
		op = "delete"
	case event.Op&fsnotify.Rename == fsnotify.Rename:
		op = "rename"
	default:
		return
	}

	// Проверяем расширение файла
	ext := strings.ToLower(filepath.Ext(event.Name))
	if !w.isSupportedExtension(ext) {
		// Проверяем, не директория ли это
		info, err := os.Stat(event.Name)
		if err != nil || !info.IsDir() {
			return
		}
	}

	// Проверяем, директория или файл
	isDir := false
	if info, err := os.Stat(event.Name); err == nil {
		isDir = info.IsDir()

		// Если создана новая директория, добавляем её в watcher
		if isDir && op == "create" {
			if err := w.addRecursive(event.Name); err != nil {
				log.Printf("Watcher: failed to add new directory %s: %v", event.Name, err)
			}
		}
	}

	// Создаем событие
	fileEvent := &FileEvent{
		Path:      event.Name,
		Operation: op,
		IsDir:     isDir,
		Time:      time.Now(),
	}

	// Debouncing - откладываем обработку для группировки событий
	w.debounceMu.Lock()
	w.pendingEvents[event.Name] = fileEvent

	if w.debounceTimer != nil {
		w.debounceTimer.Stop()
	}
	w.debounceTimer = time.AfterFunc(500*time.Millisecond, w.processPendingEvents)
	w.debounceMu.Unlock()
}

func (w *Watcher) processPendingEvents() {
	w.debounceMu.Lock()
	events := w.pendingEvents
	w.pendingEvents = make(map[string]*FileEvent)
	w.debounceMu.Unlock()

	w.mu.RLock()
	handlers := w.handlers
	w.mu.RUnlock()

	for _, event := range events {
		log.Printf("Watcher: %s %s (dir=%v)", event.Operation, event.Path, event.IsDir)

		for _, handler := range handlers {
			handler(*event)
		}
	}
}

func (w *Watcher) isSupportedExtension(ext string) bool {
	allExts := w.cfg.AllExtensions()
	for _, e := range allExts {
		if e == ext {
			return true
		}
	}
	return false
}

// IsRunning возвращает состояние наблюдателя
func (w *Watcher) IsRunning() bool {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.running
}

// WatchedPaths возвращает список наблюдаемых путей
func (w *Watcher) WatchedPaths() []string {
	return w.watcher.WatchList()
}
