package storage

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/photocore/photocore/internal/logger"
	bolt "go.etcd.io/bbolt"
)

// Имена buckets
var (
	bucketMedia     = []byte("media")
	bucketUsers     = []byte("users")
	bucketSessions  = []byte("sessions")
	bucketAlbums    = []byte("albums")
	bucketTags      = []byte("tags")
	bucketIdxDir    = []byte("idx_dir")
	bucketIdxDate   = []byte("idx_date")
	bucketIdxTag    = []byte("idx_tag")
	bucketFavorites = []byte("favorites")
	bucketUserFav   = []byte("userfav")
	bucketAPITokens = []byte("api_tokens")
)

// LogShutdownSignal логирует получение сигнала завершения
func LogShutdownSignal(sig string) {
	logger.InfoLog.Printf("[DB] === SHUTDOWN SIGNAL RECEIVED: %s ===", sig)
}

// Store обертка над bbolt
type Store struct {
	db     *bolt.DB
	dbPath string
}

// NewStore создает новое хранилище
func NewStore(dbPath string) (*Store, error) {
	logger.InfoLog.Printf("[DB] === NewStore called ===")
	logger.InfoLog.Printf("[DB] DB path: %s", dbPath)
	logger.InfoLog.Printf("[DB] Go version: %s, OS: %s, Arch: %s", runtime.Version(), runtime.GOOS, runtime.GOARCH)

	// Создаем директорию для БД если не существует
	dir := filepath.Dir(dbPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		logger.InfoLog.Printf("[DB] ERROR: failed to create db directory: %v", err)
		return nil, fmt.Errorf("failed to create db directory: %w", err)
	}

	// Проверяем существующий файл
	if info, err := os.Stat(dbPath); err == nil {
		logger.InfoLog.Printf("[DB] Existing DB file: size=%d, mod=%s", info.Size(), info.ModTime().Format(time.RFC3339))
	} else if os.IsNotExist(err) {
		logger.InfoLog.Printf("[DB] DB file does not exist, will create")
	}

	// Открываем bbolt
	logger.InfoLog.Printf("[DB] Opening bbolt database...")
	opts := &bolt.Options{
		Timeout:      5 * time.Second,
		NoSync:       false, // Sync после каждой транзакции
		FreelistType: bolt.FreelistMapType,
	}

	db, err := bolt.Open(dbPath, 0600, opts)
	if err != nil {
		logger.InfoLog.Printf("[DB] ERROR: Failed to open bbolt: %v", err)
		return nil, fmt.Errorf("failed to open bbolt: %w", err)
	}

	logger.InfoLog.Printf("[DB] SUCCESS: bbolt opened successfully")

	// Создаем все buckets
	err = db.Update(func(tx *bolt.Tx) error {
		buckets := [][]byte{
			bucketMedia, bucketUsers, bucketSessions, bucketAlbums,
			bucketTags, bucketIdxDir, bucketIdxDate, bucketIdxTag,
			bucketFavorites, bucketUserFav, bucketAPITokens,
		}
		for _, name := range buckets {
			if _, err := tx.CreateBucketIfNotExists(name); err != nil {
				return fmt.Errorf("create bucket %s: %w", name, err)
			}
		}
		return nil
	})
	if err != nil {
		db.Close()
		logger.InfoLog.Printf("[DB] ERROR: Failed to create buckets: %v", err)
		return nil, err
	}

	logger.InfoLog.Printf("[DB] All buckets initialized")

	store := &Store{
		db:     db,
		dbPath: dbPath,
	}

	return store, nil
}

// Close закрывает хранилище
func (s *Store) Close() error {
	logger.InfoLog.Printf("[DB] === Close called ===")

	// Синхронизируем
	logger.InfoLog.Printf("[DB] Syncing database...")
	if err := s.db.Sync(); err != nil {
		logger.InfoLog.Printf("[DB] WARNING: sync failed: %v", err)
	} else {
		logger.InfoLog.Printf("[DB] Sync successful")
	}

	// Закрываем
	logger.InfoLog.Printf("[DB] Closing bbolt...")
	err := s.db.Close()
	if err != nil {
		logger.InfoLog.Printf("[DB] ERROR: failed to close db: %v", err)
	} else {
		logger.InfoLog.Printf("[DB] SUCCESS: bbolt closed successfully")
	}

	logger.InfoLog.Printf("[DB] === Close completed ===")

	return err
}

// === Media операции ===

// SaveMedia сохраняет медиа-файл
func (s *Store) SaveMedia(m *Media) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		data, err := json.Marshal(m)
		if err != nil {
			return err
		}

		// Сохраняем основную запись
		b := tx.Bucket(bucketMedia)
		if err := b.Put([]byte(m.ID), data); err != nil {
			return err
		}

		// Обновляем индекс по директории
		if err := addToIndex(tx, bucketIdxDir, m.Dir, m.ID); err != nil {
			return err
		}

		// Обновляем индекс по дате (YYYY-MM)
		if !m.TakenAt.IsZero() && m.TakenAt.Year() > 1900 {
			dateKey := m.TakenAt.Format("2006-01")
			if err := addToIndex(tx, bucketIdxDate, dateKey, m.ID); err != nil {
				return err
			}
		}

		return nil
	})
}

// GetMedia получает медиа по ID
func (s *Store) GetMedia(id string) (*Media, error) {
	var media Media
	err := s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketMedia)
		data := b.Get([]byte(id))
		if data == nil {
			return nil
		}
		return json.Unmarshal(data, &media)
	})
	if err != nil {
		return nil, err
	}
	if media.ID == "" {
		return nil, nil
	}
	return &media, nil
}

// GetMediaByPath получает медиа по пути
func (s *Store) GetMediaByPath(path string) (*Media, error) {
	id := GenerateID(path)
	return s.GetMedia(id)
}

// DeleteMedia удаляет медиа и все связи
func (s *Store) DeleteMedia(id string) error {
	media, err := s.GetMedia(id)
	if err != nil {
		return err
	}
	if media == nil {
		return nil
	}

	// Удаляем из всех альбомов
	s.removeMediaFromAllAlbums(id)

	// Удаляем из избранного
	s.db.Update(func(tx *bolt.Tx) error {
		return removeFromIndex(tx, bucketFavorites, "global", id)
	})

	// Удаляем из per-user избранного
	s.removeMediaFromAllUserFavorites(id)

	// Удаляем из тегов
	s.removeMediaFromAllTags(media.Tags)

	return s.db.Update(func(tx *bolt.Tx) error {
		// Удаляем из индекса директории
		if err := removeFromIndex(tx, bucketIdxDir, media.Dir, id); err != nil {
			return err
		}

		// Удаляем из индекса даты
		if !media.TakenAt.IsZero() && media.TakenAt.Year() > 1900 {
			dateKey := media.TakenAt.Format("2006-01")
			if err := removeFromIndex(tx, bucketIdxDate, dateKey, id); err != nil {
				return err
			}
		}

		// Удаляем основную запись
		return tx.Bucket(bucketMedia).Delete([]byte(id))
	})
}

// removeMediaFromAllAlbums удаляет медиа из всех альбомов
func (s *Store) removeMediaFromAllAlbums(mediaID string) error {
	albums, err := s.ListAlbums()
	if err != nil {
		return err
	}

	for _, album := range albums {
		for _, id := range album.MediaIDs {
			if id == mediaID {
				s.RemoveMediaFromAlbum(album.ID, []string{mediaID})
				break
			}
		}
	}
	return nil
}

// removeMediaFromAllUserFavorites удаляет медиа из избранного всех пользователей
func (s *Store) removeMediaFromAllUserFavorites(mediaID string) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketUserFav)
		c := b.Cursor()

		for k, v := c.First(); k != nil; k, v = c.Next() {
			var ids []string
			if err := json.Unmarshal(v, &ids); err != nil {
				continue
			}

			newIDs := make([]string, 0, len(ids))
			for _, id := range ids {
				if id != mediaID {
					newIDs = append(newIDs, id)
				}
			}

			if len(newIDs) != len(ids) {
				data, _ := json.Marshal(newIDs)
				b.Put(k, data)
			}
		}
		return nil
	})
}

// removeMediaFromAllTags уменьшает счётчики тегов
func (s *Store) removeMediaFromAllTags(tags []string) error {
	if len(tags) == 0 {
		return nil
	}
	return s.db.Update(func(tx *bolt.Tx) error {
		for _, tagName := range tags {
			decrementTagCount(tx, tagName)
		}
		return nil
	})
}

// ListMediaByDir получает список медиа в директории
func (s *Store) ListMediaByDir(dir string) ([]*Media, error) {
	ids, err := s.getIndex(bucketIdxDir, dir)
	if err != nil {
		return nil, err
	}

	var result []*Media
	for _, id := range ids {
		media, err := s.GetMedia(id)
		if err != nil {
			continue
		}
		if media != nil && media.DeletedAt == nil {
			result = append(result, media)
		}
	}
	return result, nil
}

// ListAllMedia возвращает все медиа-файлы
func (s *Store) ListAllMedia() ([]*Media, error) {
	var result []*Media
	err := s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketMedia)
		return b.ForEach(func(k, v []byte) error {
			var media Media
			if err := json.Unmarshal(v, &media); err != nil {
				return nil // skip invalid
			}
			if media.DeletedAt == nil {
				result = append(result, &media)
			}
			return nil
		})
	})
	return result, err
}

// GetStats возвращает статистику
func (s *Store) GetStats() (*Stats, error) {
	stats := &Stats{}
	dirs := make(map[string]bool)

	err := s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketMedia)
		return b.ForEach(func(k, v []byte) error {
			var media Media
			if err := json.Unmarshal(v, &media); err != nil {
				return nil
			}
			if media.DeletedAt != nil {
				return nil
			}
			stats.TotalMedia++
			stats.TotalSize += media.Size
			dirs[media.Dir] = true

			switch media.Type {
			case MediaTypeImage:
				stats.TotalImages++
			case MediaTypeVideo:
				stats.TotalVideos++
			case MediaTypeRaw:
				stats.TotalRaw++
			}
			return nil
		})
	})

	stats.TotalDirs = len(dirs)
	return stats, err
}

// === User операции ===

// SaveUser сохраняет пользователя
func (s *Store) SaveUser(u *User) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		data, err := json.Marshal(u)
		if err != nil {
			return err
		}
		return tx.Bucket(bucketUsers).Put([]byte(u.Username), data)
	})
}

// GetUser получает пользователя по username
func (s *Store) GetUser(username string) (*User, error) {
	var user User
	err := s.db.View(func(tx *bolt.Tx) error {
		data := tx.Bucket(bucketUsers).Get([]byte(username))
		if data == nil {
			return nil
		}
		return json.Unmarshal(data, &user)
	})
	if err != nil {
		return nil, err
	}
	if user.ID == "" {
		return nil, nil
	}
	return &user, nil
}

// GetUserByID получает пользователя по ID
func (s *Store) GetUserByID(userID string) (*User, error) {
	var result *User
	err := s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketUsers)
		return b.ForEach(func(k, v []byte) error {
			var user User
			if err := json.Unmarshal(v, &user); err != nil {
				return nil
			}
			if user.ID == userID {
				result = &user
			}
			return nil
		})
	})
	return result, err
}

// ListUsers возвращает всех пользователей
func (s *Store) ListUsers() ([]*User, error) {
	var result []*User
	err := s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketUsers)
		return b.ForEach(func(k, v []byte) error {
			var user User
			if err := json.Unmarshal(v, &user); err != nil {
				return nil
			}
			result = append(result, &user)
			return nil
		})
	})
	return result, err
}

// DeleteUser удаляет пользователя
func (s *Store) DeleteUser(username string) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketUsers)
		data := b.Get([]byte(username))
		if data == nil {
			return nil
		}

		var user User
		if err := json.Unmarshal(data, &user); err == nil {
			// Удаляем favorites пользователя
			tx.Bucket(bucketUserFav).Delete([]byte(user.ID))
		}

		return b.Delete([]byte(username))
	})
}

// === Session операции ===

// SaveSession сохраняет сессию
func (s *Store) SaveSession(sess *Session) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		data, err := json.Marshal(sess)
		if err != nil {
			return err
		}
		return tx.Bucket(bucketSessions).Put([]byte(sess.ID), data)
	})
}

// GetSession получает сессию
func (s *Store) GetSession(id string) (*Session, error) {
	var sess Session
	err := s.db.View(func(tx *bolt.Tx) error {
		data := tx.Bucket(bucketSessions).Get([]byte(id))
		if data == nil {
			return nil
		}
		return json.Unmarshal(data, &sess)
	})
	if err != nil {
		return nil, err
	}
	if sess.ID == "" {
		return nil, nil
	}
	return &sess, nil
}

// DeleteSession удаляет сессию
func (s *Store) DeleteSession(id string) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		return tx.Bucket(bucketSessions).Delete([]byte(id))
	})
}

// === Вспомогательные функции для индексов ===

func addToIndex(tx *bolt.Tx, bucket []byte, key, id string) error {
	b := tx.Bucket(bucket)
	var ids []string

	data := b.Get([]byte(key))
	if data != nil {
		json.Unmarshal(data, &ids)
	}

	// Проверяем дубликаты
	for _, existingID := range ids {
		if existingID == id {
			return nil
		}
	}

	ids = append(ids, id)
	newData, err := json.Marshal(ids)
	if err != nil {
		return err
	}
	return b.Put([]byte(key), newData)
}

func removeFromIndex(tx *bolt.Tx, bucket []byte, key, id string) error {
	b := tx.Bucket(bucket)
	data := b.Get([]byte(key))
	if data == nil {
		return nil
	}

	var ids []string
	if err := json.Unmarshal(data, &ids); err != nil {
		return nil
	}

	var newIDs []string
	for _, existingID := range ids {
		if existingID != id {
			newIDs = append(newIDs, existingID)
		}
	}

	if len(newIDs) == 0 {
		return b.Delete([]byte(key))
	}

	newData, err := json.Marshal(newIDs)
	if err != nil {
		return err
	}
	return b.Put([]byte(key), newData)
}

func (s *Store) getIndex(bucket []byte, key string) ([]string, error) {
	var ids []string
	err := s.db.View(func(tx *bolt.Tx) error {
		data := tx.Bucket(bucket).Get([]byte(key))
		if data == nil {
			return nil
		}
		return json.Unmarshal(data, &ids)
	})
	return ids, err
}

// ListDirectories возвращает список уникальных директорий
func (s *Store) ListDirectories() ([]string, error) {
	var result []string
	err := s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketIdxDir)
		return b.ForEach(func(k, v []byte) error {
			result = append(result, string(k))
			return nil
		})
	})
	return result, err
}

// === Album операции ===

// SaveAlbum сохраняет альбом
func (s *Store) SaveAlbum(album *Album) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		album.MediaCount = len(album.MediaIDs)
		data, err := json.Marshal(album)
		if err != nil {
			return err
		}
		return tx.Bucket(bucketAlbums).Put([]byte(album.ID), data)
	})
}

// GetAlbum получает альбом по ID
func (s *Store) GetAlbum(id string) (*Album, error) {
	var album Album
	err := s.db.View(func(tx *bolt.Tx) error {
		data := tx.Bucket(bucketAlbums).Get([]byte(id))
		if data == nil {
			return nil
		}
		return json.Unmarshal(data, &album)
	})
	if err != nil {
		return nil, err
	}
	if album.ID == "" {
		return nil, nil
	}
	return &album, nil
}

// DeleteAlbum удаляет альбом
func (s *Store) DeleteAlbum(id string) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		return tx.Bucket(bucketAlbums).Delete([]byte(id))
	})
}

// ListAlbums возвращает все альбомы
func (s *Store) ListAlbums() ([]*Album, error) {
	var result []*Album
	err := s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketAlbums)
		return b.ForEach(func(k, v []byte) error {
			var album Album
			if err := json.Unmarshal(v, &album); err != nil {
				return nil
			}
			result = append(result, &album)
			return nil
		})
	})
	return result, err
}

// AddMediaToAlbum добавляет медиа в альбом
func (s *Store) AddMediaToAlbum(albumID string, mediaIDs []string) error {
	album, err := s.GetAlbum(albumID)
	if err != nil {
		return err
	}
	if album == nil {
		return fmt.Errorf("album not found")
	}

	existing := make(map[string]bool)
	for _, id := range album.MediaIDs {
		existing[id] = true
	}
	for _, id := range mediaIDs {
		if !existing[id] {
			album.MediaIDs = append(album.MediaIDs, id)
		}
	}

	return s.SaveAlbum(album)
}

// RemoveMediaFromAlbum удаляет медиа из альбома
func (s *Store) RemoveMediaFromAlbum(albumID string, mediaIDs []string) error {
	album, err := s.GetAlbum(albumID)
	if err != nil {
		return err
	}
	if album == nil {
		return fmt.Errorf("album not found")
	}

	toRemove := make(map[string]bool)
	for _, id := range mediaIDs {
		toRemove[id] = true
	}

	var newIDs []string
	for _, id := range album.MediaIDs {
		if !toRemove[id] {
			newIDs = append(newIDs, id)
		}
	}
	album.MediaIDs = newIDs

	return s.SaveAlbum(album)
}

// GetAlbumMedia получает медиа из альбома
func (s *Store) GetAlbumMedia(albumID string) ([]*Media, error) {
	album, err := s.GetAlbum(albumID)
	if err != nil {
		return nil, err
	}
	if album == nil {
		return nil, nil
	}

	var result []*Media
	for _, id := range album.MediaIDs {
		media, err := s.GetMedia(id)
		if err != nil {
			continue
		}
		if media != nil && media.DeletedAt == nil {
			result = append(result, media)
		}
	}
	return result, nil
}

// === Favorites операции ===

// ToggleFavorite переключает статус избранного
func (s *Store) ToggleFavorite(mediaID string) (bool, error) {
	media, err := s.GetMedia(mediaID)
	if err != nil {
		return false, err
	}
	if media == nil {
		return false, fmt.Errorf("media not found")
	}

	media.IsFavorite = !media.IsFavorite

	err = s.db.Update(func(tx *bolt.Tx) error {
		data, err := json.Marshal(media)
		if err != nil {
			return err
		}
		if err := tx.Bucket(bucketMedia).Put([]byte(media.ID), data); err != nil {
			return err
		}

		if media.IsFavorite {
			return addToIndex(tx, bucketFavorites, "global", mediaID)
		}
		return removeFromIndex(tx, bucketFavorites, "global", mediaID)
	})

	return media.IsFavorite, err
}

// SetFavorite устанавливает статус избранного
func (s *Store) SetFavorite(mediaID string, isFavorite bool) error {
	media, err := s.GetMedia(mediaID)
	if err != nil {
		return err
	}
	if media == nil {
		return fmt.Errorf("media not found")
	}

	if media.IsFavorite == isFavorite {
		return nil
	}

	media.IsFavorite = isFavorite

	return s.db.Update(func(tx *bolt.Tx) error {
		data, err := json.Marshal(media)
		if err != nil {
			return err
		}
		if err := tx.Bucket(bucketMedia).Put([]byte(media.ID), data); err != nil {
			return err
		}

		if isFavorite {
			return addToIndex(tx, bucketFavorites, "global", mediaID)
		}
		return removeFromIndex(tx, bucketFavorites, "global", mediaID)
	})
}

// ListFavorites возвращает все избранные медиа
func (s *Store) ListFavorites() ([]*Media, error) {
	ids, err := s.getIndex(bucketFavorites, "global")
	if err != nil {
		return nil, err
	}

	var result []*Media
	for _, id := range ids {
		media, err := s.GetMedia(id)
		if err != nil {
			continue
		}
		if media != nil && media.IsFavorite && media.DeletedAt == nil {
			result = append(result, media)
		}
	}
	return result, nil
}

// === Per-User Favorites ===

// GetUserFavorites возвращает список ID избранных медиа для пользователя
func (s *Store) GetUserFavorites(userID string) ([]string, error) {
	var ids []string
	err := s.db.View(func(tx *bolt.Tx) error {
		data := tx.Bucket(bucketUserFav).Get([]byte(userID))
		if data == nil {
			return nil
		}
		return json.Unmarshal(data, &ids)
	})
	return ids, err
}

// IsUserFavorite проверяет, является ли медиа избранным для пользователя
func (s *Store) IsUserFavorite(userID, mediaID string) (bool, error) {
	ids, err := s.GetUserFavorites(userID)
	if err != nil {
		return false, err
	}
	for _, id := range ids {
		if id == mediaID {
			return true, nil
		}
	}
	return false, nil
}

// SetUserFavorite устанавливает статус избранного для пользователя
func (s *Store) SetUserFavorite(userID, mediaID string, isFavorite bool) error {
	media, err := s.GetMedia(mediaID)
	if err != nil {
		return err
	}
	if media == nil {
		return fmt.Errorf("media not found")
	}

	return s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketUserFav)
		var ids []string

		data := b.Get([]byte(userID))
		if data != nil {
			json.Unmarshal(data, &ids)
		}

		if isFavorite {
			// Добавляем
			for _, id := range ids {
				if id == mediaID {
					return nil // Уже есть
				}
			}
			ids = append(ids, mediaID)
		} else {
			// Удаляем
			var newIDs []string
			for _, id := range ids {
				if id != mediaID {
					newIDs = append(newIDs, id)
				}
			}
			ids = newIDs
		}

		newData, _ := json.Marshal(ids)
		return b.Put([]byte(userID), newData)
	})
}

// ToggleUserFavorite переключает статус избранного для пользователя
func (s *Store) ToggleUserFavorite(userID, mediaID string) (bool, error) {
	media, err := s.GetMedia(mediaID)
	if err != nil {
		return false, err
	}
	if media == nil {
		return false, fmt.Errorf("media not found")
	}

	isFavorite, err := s.IsUserFavorite(userID, mediaID)
	if err != nil {
		return false, err
	}

	newStatus := !isFavorite
	err = s.SetUserFavorite(userID, mediaID, newStatus)
	return newStatus, err
}

// ListUserFavorites возвращает все избранные медиа для пользователя
func (s *Store) ListUserFavorites(userID string) ([]*Media, error) {
	ids, err := s.GetUserFavorites(userID)
	if err != nil {
		return nil, err
	}

	var result []*Media
	for _, id := range ids {
		media, err := s.GetMedia(id)
		if err != nil {
			continue
		}
		if media != nil && media.DeletedAt == nil {
			result = append(result, media)
		}
	}
	return result, nil
}

// === Tag операции ===

// AddTagsToMedia добавляет теги к медиа
func (s *Store) AddTagsToMedia(mediaID string, tags []string) error {
	media, err := s.GetMedia(mediaID)
	if err != nil {
		return err
	}
	if media == nil {
		return fmt.Errorf("media not found")
	}

	existing := make(map[string]bool)
	for _, t := range media.Tags {
		existing[t] = true
	}

	return s.db.Update(func(tx *bolt.Tx) error {
		for _, tag := range tags {
			tag = strings.TrimSpace(strings.ToLower(tag))
			if tag == "" {
				continue
			}
			if !existing[tag] {
				media.Tags = append(media.Tags, tag)
				existing[tag] = true
			}
			addToIndex(tx, bucketIdxTag, tag, mediaID)
			incrementTagCount(tx, tag)
		}

		data, err := json.Marshal(media)
		if err != nil {
			return err
		}
		return tx.Bucket(bucketMedia).Put([]byte(mediaID), data)
	})
}

// RemoveTagsFromMedia удаляет теги с медиа
func (s *Store) RemoveTagsFromMedia(mediaID string, tags []string) error {
	media, err := s.GetMedia(mediaID)
	if err != nil {
		return err
	}
	if media == nil {
		return fmt.Errorf("media not found")
	}

	toRemove := make(map[string]bool)
	for _, t := range tags {
		toRemove[strings.TrimSpace(strings.ToLower(t))] = true
	}

	return s.db.Update(func(tx *bolt.Tx) error {
		var newTags []string
		for _, t := range media.Tags {
			if !toRemove[t] {
				newTags = append(newTags, t)
			} else {
				removeFromIndex(tx, bucketIdxTag, t, mediaID)
				decrementTagCount(tx, t)
			}
		}
		media.Tags = newTags

		data, err := json.Marshal(media)
		if err != nil {
			return err
		}
		return tx.Bucket(bucketMedia).Put([]byte(mediaID), data)
	})
}

// ListAllTags возвращает все теги
func (s *Store) ListAllTags() ([]*Tag, error) {
	var result []*Tag
	err := s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketTags)
		return b.ForEach(func(k, v []byte) error {
			var tag Tag
			if err := json.Unmarshal(v, &tag); err != nil {
				return nil
			}
			result = append(result, &tag)
			return nil
		})
	})
	return result, err
}

// ListMediaByTag возвращает медиа с тегом
func (s *Store) ListMediaByTag(tag string) ([]*Media, error) {
	tag = strings.TrimSpace(strings.ToLower(tag))
	ids, err := s.getIndex(bucketIdxTag, tag)
	if err != nil {
		return nil, err
	}

	var result []*Media
	for _, id := range ids {
		media, err := s.GetMedia(id)
		if err != nil {
			continue
		}
		if media != nil && media.DeletedAt == nil {
			result = append(result, media)
		}
	}
	return result, nil
}

func incrementTagCount(tx *bolt.Tx, tagName string) error {
	b := tx.Bucket(bucketTags)
	var tag Tag

	data := b.Get([]byte(tagName))
	if data != nil {
		json.Unmarshal(data, &tag)
	} else {
		tag = Tag{Name: tagName}
	}

	tag.MediaCount++
	newData, _ := json.Marshal(tag)
	return b.Put([]byte(tagName), newData)
}

func decrementTagCount(tx *bolt.Tx, tagName string) error {
	b := tx.Bucket(bucketTags)
	data := b.Get([]byte(tagName))
	if data == nil {
		return nil
	}

	var tag Tag
	json.Unmarshal(data, &tag)
	tag.MediaCount--

	if tag.MediaCount <= 0 {
		return b.Delete([]byte(tagName))
	}

	newData, _ := json.Marshal(tag)
	return b.Put([]byte(tagName), newData)
}

// === Search операции ===

// Search выполняет поиск медиа
func (s *Store) Search(query *SearchQuery) (*SearchResult, error) {
	if query.Limit <= 0 {
		query.Limit = 50
	}

	allMedia, err := s.ListAllMedia()
	if err != nil {
		return nil, err
	}

	var filtered []*Media
	for _, m := range allMedia {
		if s.matchesQuery(m, query) {
			filtered = append(filtered, m)
		}
	}

	totalCount := len(filtered)

	start := query.Offset
	if start > len(filtered) {
		start = len(filtered)
	}
	end := start + query.Limit
	if end > len(filtered) {
		end = len(filtered)
	}

	result := &SearchResult{
		Media:      filtered[start:end],
		TotalCount: totalCount,
		HasMore:    end < totalCount,
	}

	return result, nil
}

func (s *Store) matchesQuery(m *Media, q *SearchQuery) bool {
	if q.Text != "" {
		text := strings.ToLower(q.Text)
		filename := strings.ToLower(m.Filename)
		camera := strings.ToLower(m.Metadata.Camera)
		lens := strings.ToLower(m.Metadata.Lens)

		if !strings.Contains(filename, text) &&
			!strings.Contains(camera, text) &&
			!strings.Contains(lens, text) {
			return false
		}
	}

	if q.Type != "" && m.Type != q.Type {
		return false
	}

	if q.DateFrom != nil && m.TakenAt.Before(*q.DateFrom) {
		return false
	}
	if q.DateTo != nil && m.TakenAt.After(*q.DateTo) {
		return false
	}

	if len(q.Tags) > 0 {
		mediaTagSet := make(map[string]bool)
		for _, t := range m.Tags {
			mediaTagSet[t] = true
		}
		for _, t := range q.Tags {
			if !mediaTagSet[strings.ToLower(t)] {
				return false
			}
		}
	}

	if q.Camera != "" && !strings.Contains(strings.ToLower(m.Metadata.Camera), strings.ToLower(q.Camera)) {
		return false
	}

	if q.IsFavorite != nil && m.IsFavorite != *q.IsFavorite {
		return false
	}

	if q.HasGPS != nil {
		hasGPS := m.Metadata.GPSLat != 0 || m.Metadata.GPSLon != 0
		if hasGPS != *q.HasGPS {
			return false
		}
	}

	return true
}

// === Timeline операции ===

// GetTimeline возвращает группировку медиа по месяцам
func (s *Store) GetTimeline() ([]*TimelineGroup, error) {
	allMedia, err := s.ListAllMedia()
	if err != nil {
		return nil, err
	}

	groups := make(map[string]*TimelineGroup)
	for _, m := range allMedia {
		var date string
		if !m.TakenAt.IsZero() && m.TakenAt.Year() > 1900 {
			date = m.TakenAt.Format("2006-01")
		} else {
			date = m.ModifiedAt.Format("2006-01")
		}

		if groups[date] == nil {
			groups[date] = &TimelineGroup{
				Date:  date,
				Label: formatMonthLabel(date),
			}
		}
		groups[date].MediaCount++
	}

	var result []*TimelineGroup
	for _, g := range groups {
		result = append(result, g)
	}

	// Сортировка по убыванию даты
	for i := 0; i < len(result)-1; i++ {
		for j := i + 1; j < len(result); j++ {
			if result[i].Date < result[j].Date {
				result[i], result[j] = result[j], result[i]
			}
		}
	}

	return result, nil
}

// GetTimelineMedia возвращает медиа для периода
func (s *Store) GetTimelineMedia(period string) ([]*Media, error) {
	allMedia, err := s.ListAllMedia()
	if err != nil {
		return nil, err
	}

	var result []*Media
	for _, m := range allMedia {
		var date string
		if !m.TakenAt.IsZero() && m.TakenAt.Year() > 1900 {
			date = m.TakenAt.Format("2006-01")
		} else {
			date = m.ModifiedAt.Format("2006-01")
		}

		if date == period {
			result = append(result, m)
		}
	}

	return result, nil
}

func formatMonthLabel(date string) string {
	months := map[string]string{
		"01": "Январь", "02": "Февраль", "03": "Март",
		"04": "Апрель", "05": "Май", "06": "Июнь",
		"07": "Июль", "08": "Август", "09": "Сентябрь",
		"10": "Октябрь", "11": "Ноябрь", "12": "Декабрь",
	}
	if len(date) >= 7 {
		year := date[:4]
		month := date[5:7]
		if m, ok := months[month]; ok {
			return m + " " + year
		}
	}
	return date
}

// === Geo операции ===

// GetGeoPoints возвращает все точки с GPS
func (s *Store) GetGeoPoints() ([]*GeoPoint, error) {
	allMedia, err := s.ListAllMedia()
	if err != nil {
		return nil, err
	}

	var result []*GeoPoint
	for _, m := range allMedia {
		if m.Metadata.GPSLat != 0 || m.Metadata.GPSLon != 0 {
			result = append(result, &GeoPoint{
				MediaID:  m.ID,
				Lat:      m.Metadata.GPSLat,
				Lon:      m.Metadata.GPSLon,
				ThumbURL: "/media/" + m.ID + "/thumb/small",
			})
		}
	}
	return result, nil
}

// === Bulk операции ===

// BulkSetFavorite устанавливает избранное для нескольких медиа
func (s *Store) BulkSetFavorite(mediaIDs []string, isFavorite bool) error {
	for _, id := range mediaIDs {
		if err := s.SetFavorite(id, isFavorite); err != nil {
			return err
		}
	}
	return nil
}

// BulkAddTags добавляет теги к нескольким медиа
func (s *Store) BulkAddTags(mediaIDs []string, tags []string) error {
	for _, id := range mediaIDs {
		if err := s.AddTagsToMedia(id, tags); err != nil {
			return err
		}
	}
	return nil
}

// BulkDelete удаляет несколько медиа
func (s *Store) BulkDelete(mediaIDs []string) error {
	for _, id := range mediaIDs {
		if err := s.DeleteMedia(id); err != nil {
			return err
		}
	}
	return nil
}

// GetMediaByIDs возвращает медиа по списку ID
func (s *Store) GetMediaByIDs(ids []string) ([]*Media, error) {
	var result []*Media
	for _, id := range ids {
		media, err := s.GetMedia(id)
		if err != nil {
			continue
		}
		if media != nil && media.DeletedAt == nil {
			result = append(result, media)
		}
	}
	return result, nil
}

// === Trash операции ===

// SoftDeleteMedia помечает медиа как удалённое
func (s *Store) SoftDeleteMedia(id string) error {
	media, err := s.GetMedia(id)
	if err != nil {
		return err
	}
	if media == nil {
		return fmt.Errorf("media not found")
	}

	now := time.Now()
	media.DeletedAt = &now

	return s.db.Update(func(tx *bolt.Tx) error {
		data, err := json.Marshal(media)
		if err != nil {
			return err
		}
		return tx.Bucket(bucketMedia).Put([]byte(media.ID), data)
	})
}

// RestoreMedia восстанавливает медиа из корзины
func (s *Store) RestoreMedia(id string) error {
	media, err := s.GetMedia(id)
	if err != nil {
		return err
	}
	if media == nil {
		return fmt.Errorf("media not found")
	}
	if media.DeletedAt == nil {
		return nil
	}

	media.DeletedAt = nil

	return s.db.Update(func(tx *bolt.Tx) error {
		data, err := json.Marshal(media)
		if err != nil {
			return err
		}
		return tx.Bucket(bucketMedia).Put([]byte(media.ID), data)
	})
}

// ListTrashMedia возвращает все медиа в корзине
func (s *Store) ListTrashMedia() ([]*Media, error) {
	var result []*Media
	err := s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketMedia)
		return b.ForEach(func(k, v []byte) error {
			var media Media
			if err := json.Unmarshal(v, &media); err != nil {
				return nil
			}
			if media.DeletedAt != nil {
				result = append(result, &media)
			}
			return nil
		})
	})
	return result, err
}

// CleanupTrash удаляет медиа из корзины старше указанного времени
func (s *Store) CleanupTrash(olderThan time.Duration) (int, error) {
	trashMedia, err := s.ListTrashMedia()
	if err != nil {
		return 0, err
	}

	cutoff := time.Now().Add(-olderThan)
	var deleted int

	for _, m := range trashMedia {
		if m.DeletedAt != nil && m.DeletedAt.Before(cutoff) {
			if err := s.DeleteMedia(m.ID); err != nil {
				logger.InfoLog.Printf("Error permanently deleting media %s: %v", m.ID, err)
				continue
			}
			deleted++
		}
	}

	return deleted, nil
}

// GetTrashStats возвращает статистику корзины
func (s *Store) GetTrashStats() (count int, totalSize int64, err error) {
	trashMedia, err := s.ListTrashMedia()
	if err != nil {
		return 0, 0, err
	}

	for _, m := range trashMedia {
		count++
		totalSize += m.Size
	}

	return count, totalSize, nil
}

// === Duplicates операции ===

// FindDuplicates находит дубликаты медиа
// Возвращает группы: exact (по SHA256) и similar (по perceptual hash)
func (s *Store) FindDuplicates(similarityThreshold int) ([]*DuplicateGroup, error) {
	allMedia, err := s.ListAllMedia()
	if err != nil {
		return nil, err
	}

	var groups []*DuplicateGroup

	// 1. Точные дубликаты по SHA256
	checksumGroups := make(map[string][]*Media)
	for _, m := range allMedia {
		if m.Checksum != "" {
			checksumGroups[m.Checksum] = append(checksumGroups[m.Checksum], m)
		}
	}

	for _, mediaList := range checksumGroups {
		if len(mediaList) > 1 {
			groups = append(groups, &DuplicateGroup{
				Type:     "exact",
				Media:    mediaList,
				Distance: 0,
			})
		}
	}

	// 2. Похожие по perceptual hash (только изображения с ImageHash)
	// Группируем по размеру файла сначала для оптимизации
	sizeGroups := make(map[int64][]*Media)
	for _, m := range allMedia {
		if m.ImageHash != 0 && (m.Type == MediaTypeImage || m.Type == MediaTypeRaw) {
			sizeGroups[m.Size] = append(sizeGroups[m.Size], m)
		}
	}

	// Для файлов с разным размером но похожим содержимым
	var imagesWithHash []*Media
	for _, m := range allMedia {
		if m.ImageHash != 0 && (m.Type == MediaTypeImage || m.Type == MediaTypeRaw) {
			// Проверяем, что это не уже найденный точный дубликат
			isExact := false
			for _, g := range groups {
				if g.Type == "exact" {
					for _, em := range g.Media {
						if em.ID == m.ID {
							isExact = true
							break
						}
					}
				}
				if isExact {
					break
				}
			}
			if !isExact {
				imagesWithHash = append(imagesWithHash, m)
			}
		}
	}

	// Находим похожие изображения
	processed := make(map[string]bool)
	for i, m1 := range imagesWithHash {
		if processed[m1.ID] {
			continue
		}

		var similarGroup []*Media
		similarGroup = append(similarGroup, m1)
		processed[m1.ID] = true

		for j := i + 1; j < len(imagesWithHash); j++ {
			m2 := imagesWithHash[j]
			if processed[m2.ID] {
				continue
			}

			distance := hammingDistance(m1.ImageHash, m2.ImageHash)
			if distance <= similarityThreshold {
				similarGroup = append(similarGroup, m2)
				processed[m2.ID] = true
			}
		}

		if len(similarGroup) > 1 {
			groups = append(groups, &DuplicateGroup{
				Type:     "similar",
				Media:    similarGroup,
				Distance: similarityThreshold,
			})
		}
	}

	return groups, nil
}

// hammingDistance вычисляет расстояние Хэмминга между двумя хешами
func hammingDistance(hash1, hash2 uint64) int {
	xor := hash1 ^ hash2
	distance := 0
	for xor != 0 {
		distance++
		xor &= xor - 1
	}
	return distance
}

// ChecksumExists проверяет, существует ли медиа с таким же checksum
// Возвращает ID существующего медиа если найден, или пустую строку
func (s *Store) ChecksumExists(checksum string) (string, error) {
	if checksum == "" {
		return "", nil
	}

	var existingID string
	err := s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketMedia)
		return b.ForEach(func(k, v []byte) error {
			var media Media
			if err := json.Unmarshal(v, &media); err != nil {
				return nil
			}
			// Пропускаем удалённые файлы
			if media.DeletedAt != nil {
				return nil
			}
			if media.Checksum == checksum {
				existingID = media.ID
				return fmt.Errorf("found") // Прерываем поиск
			}
			return nil
		})
	})

	// Игнорируем "found" ошибку - это просто способ прервать итерацию
	if err != nil && err.Error() != "found" {
		return "", err
	}

	return existingID, nil
}

// FindMediaBySizeRange находит медиа с размером в диапазоне ±10%
// Это быстрый первичный фильтр для поиска потенциальных дубликатов
func (s *Store) FindMediaBySizeRange(size int64) ([]*Media, error) {
	// ±10% от размера
	minSize := int64(float64(size) * 0.9)
	maxSize := int64(float64(size) * 1.1)

	var result []*Media
	err := s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketMedia)
		return b.ForEach(func(k, v []byte) error {
			var media Media
			if err := json.Unmarshal(v, &media); err != nil {
				return nil
			}
			// Пропускаем удалённые файлы
			if media.DeletedAt != nil {
				return nil
			}
			// Проверяем диапазон размера
			if media.Size >= minSize && media.Size <= maxSize {
				result = append(result, &media)
			}
			return nil
		})
	})
	return result, err
}

// DuplicateCheckResult результат проверки на дубликат
type DuplicateCheckResult struct {
	IsDuplicate bool   // Является ли дубликатом
	Type        string // "exact" или "similar"
	ExistingID  string // ID существующего медиа
	Distance    int    // Расстояние Хэмминга (для similar)
}

// CheckDuplicate выполняет гибридную проверку на дубликат:
// 1. Фильтр по размеру (±10%) + SHA256 для точных дубликатов
// 2. pHash для визуально похожих (проверяет ВСЕ изображения, без фильтра по размеру)
func (s *Store) CheckDuplicate(size int64, checksum string, imageHash uint64, isImage bool, similarityThreshold int) (*DuplicateCheckResult, error) {
	result := &DuplicateCheckResult{IsDuplicate: false}

	// Шаг 1: Точные дубликаты — фильтр по размеру (±10%) + SHA256
	if checksum != "" {
		candidates, err := s.FindMediaBySizeRange(size)
		if err != nil {
			return nil, err
		}
		for _, m := range candidates {
			if m.Checksum == checksum {
				result.IsDuplicate = true
				result.Type = "exact"
				result.ExistingID = m.ID
				result.Distance = 0
				return result, nil
			}
		}
	}

	// Шаг 2: Визуально похожие — pHash по ВСЕМ изображениям (мессенджеры пережимают фото)
	if isImage && imageHash != 0 {
		allMedia, err := s.ListAllMedia()
		if err != nil {
			return nil, err
		}
		for _, m := range allMedia {
			if m.ImageHash == 0 {
				continue
			}
			distance := hammingDistance(imageHash, m.ImageHash)
			if distance <= similarityThreshold {
				result.IsDuplicate = true
				result.Type = "similar"
				result.ExistingID = m.ID
				result.Distance = distance
				return result, nil
			}
		}
	}

	return result, nil
}

// GetDuplicatesStats возвращает статистику дубликатов
func (s *Store) GetDuplicatesStats() (exactCount int, similarCount int, savedSpace int64, err error) {
	groups, err := s.FindDuplicates(10)
	if err != nil {
		return 0, 0, 0, err
	}

	for _, g := range groups {
		if g.Type == "exact" {
			exactCount += len(g.Media) - 1 // Количество лишних копий
			// Считаем сколько места можно освободить
			for i := 1; i < len(g.Media); i++ {
				savedSpace += g.Media[i].Size
			}
		} else {
			similarCount += len(g.Media) - 1
		}
	}

	return exactCount, similarCount, savedSpace, nil
}

// === API Token операции ===

// SaveAPIToken сохраняет API токен
func (s *Store) SaveAPIToken(token *APIToken) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		data, err := json.Marshal(token)
		if err != nil {
			return err
		}
		return tx.Bucket(bucketAPITokens).Put([]byte(token.Token), data)
	})
}

// GetAPIToken получает API токен
func (s *Store) GetAPIToken(token string) (*APIToken, error) {
	var apiToken APIToken
	err := s.db.View(func(tx *bolt.Tx) error {
		data := tx.Bucket(bucketAPITokens).Get([]byte(token))
		if data == nil {
			return nil
		}
		return json.Unmarshal(data, &apiToken)
	})
	if err != nil {
		return nil, err
	}
	if apiToken.Token == "" {
		return nil, nil
	}
	return &apiToken, nil
}

// DeleteAPIToken удаляет API токен
func (s *Store) DeleteAPIToken(token string) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		return tx.Bucket(bucketAPITokens).Delete([]byte(token))
	})
}

// ListUserAPITokens возвращает все токены пользователя
func (s *Store) ListUserAPITokens(userID string) ([]*APIToken, error) {
	var result []*APIToken
	err := s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketAPITokens)
		return b.ForEach(func(k, v []byte) error {
			var token APIToken
			if err := json.Unmarshal(v, &token); err != nil {
				return nil
			}
			if token.UserID == userID {
				result = append(result, &token)
			}
			return nil
		})
	})
	return result, err
}
