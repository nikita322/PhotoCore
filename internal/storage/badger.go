package storage

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/dgraph-io/badger/v4"
)

// Префиксы ключей для разных типов данных
const (
	prefixMedia         = "media:"
	prefixUser          = "user:"
	prefixSession       = "session:"
	prefixDir           = "dir:"
	prefixAlbum         = "album:"
	prefixTag           = "tag:"
	prefixByDir         = "idx:dir:"      // Индекс: директория -> media IDs
	prefixByDate        = "idx:date:"     // Индекс: дата -> media IDs
	prefixByTag         = "idx:tag:"      // Индекс: тег -> media IDs
	prefixFavorites     = "idx:favorites" // Индекс избранного (глобальный, deprecated)
	prefixUserFavorites = "userfav:"      // Per-user избранное: userfav:{userID} -> []mediaID
)

// Store обертка над BadgerDB
type Store struct {
	db *badger.DB
}

// NewStore создает новое хранилище
func NewStore(dbPath string) (*Store, error) {
	// Создаем директорию для БД если не существует
	dir := filepath.Dir(dbPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create db directory: %w", err)
	}

	opts := badger.DefaultOptions(dbPath)
	opts.Logger = nil // Отключаем логирование BadgerDB

	db, err := badger.Open(opts)
	if err != nil {
		return nil, fmt.Errorf("failed to open badger db: %w", err)
	}

	return &Store{db: db}, nil
}

// Close закрывает хранилище
func (s *Store) Close() error {
	return s.db.Close()
}

// === Media операции ===

// SaveMedia сохраняет медиа-файл
func (s *Store) SaveMedia(m *Media) error {
	return s.db.Update(func(txn *badger.Txn) error {
		data, err := json.Marshal(m)
		if err != nil {
			return err
		}

		// Сохраняем основную запись
		key := prefixMedia + m.ID
		if err := txn.Set([]byte(key), data); err != nil {
			return err
		}

		// Обновляем индекс по директории
		dirKey := prefixByDir + m.Dir
		if err := s.addToIndex(txn, dirKey, m.ID); err != nil {
			return err
		}

		// Обновляем индекс по дате (YYYY-MM)
		if !m.TakenAt.IsZero() {
			dateKey := prefixByDate + m.TakenAt.Format("2006-01")
			if err := s.addToIndex(txn, dateKey, m.ID); err != nil {
				return err
			}
		}

		return nil
	})
}

// GetMedia получает медиа по ID
func (s *Store) GetMedia(id string) (*Media, error) {
	var media Media
	err := s.db.View(func(txn *badger.Txn) error {
		item, err := txn.Get([]byte(prefixMedia + id))
		if err != nil {
			return err
		}
		return item.Value(func(val []byte) error {
			return json.Unmarshal(val, &media)
		})
	})
	if err == badger.ErrKeyNotFound {
		return nil, nil
	}
	return &media, err
}

// GetMediaByPath получает медиа по пути
func (s *Store) GetMediaByPath(path string) (*Media, error) {
	id := GenerateID(path)
	return s.GetMedia(id)
}

// DeleteMedia удаляет медиа и все связи (альбомы, избранное, теги)
func (s *Store) DeleteMedia(id string) error {
	// Сначала получаем медиа для удаления из индексов
	media, err := s.GetMedia(id)
	if err != nil {
		return err
	}
	if media == nil {
		return nil
	}

	// Удаляем из всех альбомов
	if err := s.removeMediaFromAllAlbums(id); err != nil {
		log.Printf("Error removing media %s from albums: %v", id, err)
	}

	// Удаляем из глобального избранного
	s.db.Update(func(txn *badger.Txn) error {
		return s.removeFromIndex(txn, prefixFavorites, id)
	})

	// Удаляем из per-user избранного
	if err := s.removeMediaFromAllUserFavorites(id); err != nil {
		log.Printf("Error removing media %s from user favorites: %v", id, err)
	}

	// Удаляем из всех тегов
	if err := s.removeMediaFromAllTags(media.Tags); err != nil {
		log.Printf("Error removing media %s from tags: %v", id, err)
	}

	return s.db.Update(func(txn *badger.Txn) error {
		// Удаляем из индекса директории
		dirKey := prefixByDir + media.Dir
		if err := s.removeFromIndex(txn, dirKey, id); err != nil {
			return err
		}

		// Удаляем из индекса даты
		if !media.TakenAt.IsZero() {
			dateKey := prefixByDate + media.TakenAt.Format("2006-01")
			if err := s.removeFromIndex(txn, dateKey, id); err != nil {
				return err
			}
		}

		// Удаляем основную запись
		return txn.Delete([]byte(prefixMedia + id))
	})
}

// removeMediaFromAllAlbums удаляет медиа из всех альбомов
func (s *Store) removeMediaFromAllAlbums(mediaID string) error {
	albums, err := s.ListAlbums()
	if err != nil {
		return err
	}

	for _, album := range albums {
		// Проверяем есть ли mediaID в альбоме
		found := false
		for _, id := range album.MediaIDs {
			if id == mediaID {
				found = true
				break
			}
		}
		if found {
			s.RemoveMediaFromAlbum(album.ID, []string{mediaID})
		}
	}
	return nil
}

// removeMediaFromAllUserFavorites удаляет медиа из избранного всех пользователей
func (s *Store) removeMediaFromAllUserFavorites(mediaID string) error {
	return s.db.Update(func(txn *badger.Txn) error {
		opts := badger.DefaultIteratorOptions
		opts.Prefix = []byte(prefixUserFavorites)
		it := txn.NewIterator(opts)
		defer it.Close()

		for it.Rewind(); it.Valid(); it.Next() {
			item := it.Item()
			key := string(item.Key())

			err := item.Value(func(val []byte) error {
				var ids []string
				if err := json.Unmarshal(val, &ids); err != nil {
					return nil // skip invalid
				}

				// Фильтруем mediaID
				newIDs := make([]string, 0, len(ids))
				for _, id := range ids {
					if id != mediaID {
						newIDs = append(newIDs, id)
					}
				}

				// Если изменилось - сохраняем
				if len(newIDs) != len(ids) {
					data, _ := json.Marshal(newIDs)
					return txn.Set([]byte(key), data)
				}
				return nil
			})
			if err != nil {
				return err
			}
		}
		return nil
	})
}

// removeMediaFromAllTags декрементирует счётчики тегов для удалённого медиа
func (s *Store) removeMediaFromAllTags(tags []string) error {
	if len(tags) == 0 {
		return nil
	}
	return s.db.Update(func(txn *badger.Txn) error {
		for _, tagName := range tags {
			if err := s.decrementTagCount(txn, tagName); err != nil {
				log.Printf("Error decrementing tag count for %s: %v", tagName, err)
			}
		}
		return nil
	})
}

// ListMediaByDir получает список медиа в директории
func (s *Store) ListMediaByDir(dir string) ([]*Media, error) {
	ids, err := s.getIndex(prefixByDir + dir)
	if err != nil {
		return nil, err
	}

	var result []*Media
	for _, id := range ids {
		media, err := s.GetMedia(id)
		if err != nil {
			continue
		}
		if media != nil {
			result = append(result, media)
		}
	}
	return result, nil
}

// ListAllMedia возвращает все медиа-файлы
func (s *Store) ListAllMedia() ([]*Media, error) {
	var result []*Media
	err := s.db.View(func(txn *badger.Txn) error {
		opts := badger.DefaultIteratorOptions
		opts.PrefetchSize = 100
		it := txn.NewIterator(opts)
		defer it.Close()

		prefix := []byte(prefixMedia)
		for it.Seek(prefix); it.ValidForPrefix(prefix); it.Next() {
			item := it.Item()
			err := item.Value(func(val []byte) error {
				var media Media
				if err := json.Unmarshal(val, &media); err != nil {
					return err
				}
				result = append(result, &media)
				return nil
			})
			if err != nil {
				return err
			}
		}
		return nil
	})
	return result, err
}

// GetStats возвращает статистику
func (s *Store) GetStats() (*Stats, error) {
	stats := &Stats{}
	dirs := make(map[string]bool)

	err := s.db.View(func(txn *badger.Txn) error {
		opts := badger.DefaultIteratorOptions
		opts.PrefetchSize = 100
		it := txn.NewIterator(opts)
		defer it.Close()

		prefix := []byte(prefixMedia)
		for it.Seek(prefix); it.ValidForPrefix(prefix); it.Next() {
			item := it.Item()
			err := item.Value(func(val []byte) error {
				var media Media
				if err := json.Unmarshal(val, &media); err != nil {
					return err
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
			if err != nil {
				return err
			}
		}
		return nil
	})

	stats.TotalDirs = len(dirs)
	return stats, err
}

// === User операции ===

// SaveUser сохраняет пользователя
func (s *Store) SaveUser(u *User) error {
	return s.db.Update(func(txn *badger.Txn) error {
		data, err := json.Marshal(u)
		if err != nil {
			return err
		}
		return txn.Set([]byte(prefixUser+u.Username), data)
	})
}

// GetUser получает пользователя по username
func (s *Store) GetUser(username string) (*User, error) {
	var user User
	err := s.db.View(func(txn *badger.Txn) error {
		item, err := txn.Get([]byte(prefixUser + username))
		if err != nil {
			return err
		}
		return item.Value(func(val []byte) error {
			return json.Unmarshal(val, &user)
		})
	})
	if err == badger.ErrKeyNotFound {
		return nil, nil
	}
	return &user, err
}

// GetUserByID получает пользователя по ID
func (s *Store) GetUserByID(userID string) (*User, error) {
	var result *User
	err := s.db.View(func(txn *badger.Txn) error {
		opts := badger.DefaultIteratorOptions
		it := txn.NewIterator(opts)
		defer it.Close()

		prefix := []byte(prefixUser)
		for it.Seek(prefix); it.ValidForPrefix(prefix); it.Next() {
			item := it.Item()
			err := item.Value(func(val []byte) error {
				var user User
				if err := json.Unmarshal(val, &user); err != nil {
					return err
				}
				if user.ID == userID {
					result = &user
				}
				return nil
			})
			if err != nil {
				return err
			}
			if result != nil {
				break
			}
		}
		return nil
	})
	return result, err
}

// ListUsers возвращает всех пользователей
func (s *Store) ListUsers() ([]*User, error) {
	var result []*User
	err := s.db.View(func(txn *badger.Txn) error {
		opts := badger.DefaultIteratorOptions
		it := txn.NewIterator(opts)
		defer it.Close()

		prefix := []byte(prefixUser)
		for it.Seek(prefix); it.ValidForPrefix(prefix); it.Next() {
			item := it.Item()
			err := item.Value(func(val []byte) error {
				var user User
				if err := json.Unmarshal(val, &user); err != nil {
					return err
				}
				result = append(result, &user)
				return nil
			})
			if err != nil {
				return err
			}
		}
		return nil
	})
	return result, err
}

// DeleteUser удаляет пользователя
func (s *Store) DeleteUser(username string) error {
	return s.db.Update(func(txn *badger.Txn) error {
		// Получаем пользователя для удаления его favorites
		item, err := txn.Get([]byte(prefixUser + username))
		if err == badger.ErrKeyNotFound {
			return nil
		}
		if err != nil {
			return err
		}

		var user User
		err = item.Value(func(val []byte) error {
			return json.Unmarshal(val, &user)
		})
		if err != nil {
			return err
		}

		// Удаляем favorites пользователя
		_ = txn.Delete([]byte(prefixUserFavorites + user.ID))

		// Удаляем пользователя
		return txn.Delete([]byte(prefixUser + username))
	})
}

// === Session операции ===

// SaveSession сохраняет сессию
func (s *Store) SaveSession(sess *Session) error {
	return s.db.Update(func(txn *badger.Txn) error {
		data, err := json.Marshal(sess)
		if err != nil {
			return err
		}
		return txn.Set([]byte(prefixSession+sess.ID), data)
	})
}

// GetSession получает сессию
func (s *Store) GetSession(id string) (*Session, error) {
	var sess Session
	err := s.db.View(func(txn *badger.Txn) error {
		item, err := txn.Get([]byte(prefixSession + id))
		if err != nil {
			return err
		}
		return item.Value(func(val []byte) error {
			return json.Unmarshal(val, &sess)
		})
	})
	if err == badger.ErrKeyNotFound {
		return nil, nil
	}
	return &sess, err
}

// DeleteSession удаляет сессию
func (s *Store) DeleteSession(id string) error {
	return s.db.Update(func(txn *badger.Txn) error {
		return txn.Delete([]byte(prefixSession + id))
	})
}

// === Вспомогательные функции для индексов ===

func (s *Store) addToIndex(txn *badger.Txn, key, id string) error {
	var ids []string

	item, err := txn.Get([]byte(key))
	if err == nil {
		err = item.Value(func(val []byte) error {
			return json.Unmarshal(val, &ids)
		})
		if err != nil {
			return err
		}
	} else if err != badger.ErrKeyNotFound {
		return err
	}

	// Проверяем, нет ли уже такого ID
	for _, existingID := range ids {
		if existingID == id {
			return nil
		}
	}

	ids = append(ids, id)
	data, err := json.Marshal(ids)
	if err != nil {
		return err
	}
	return txn.Set([]byte(key), data)
}

func (s *Store) removeFromIndex(txn *badger.Txn, key, id string) error {
	var ids []string

	item, err := txn.Get([]byte(key))
	if err == badger.ErrKeyNotFound {
		return nil
	}
	if err != nil {
		return err
	}

	err = item.Value(func(val []byte) error {
		return json.Unmarshal(val, &ids)
	})
	if err != nil {
		return err
	}

	// Удаляем ID из списка
	var newIDs []string
	for _, existingID := range ids {
		if existingID != id {
			newIDs = append(newIDs, existingID)
		}
	}

	if len(newIDs) == 0 {
		return txn.Delete([]byte(key))
	}

	data, err := json.Marshal(newIDs)
	if err != nil {
		return err
	}
	return txn.Set([]byte(key), data)
}

func (s *Store) getIndex(key string) ([]string, error) {
	var ids []string
	err := s.db.View(func(txn *badger.Txn) error {
		item, err := txn.Get([]byte(key))
		if err == badger.ErrKeyNotFound {
			return nil
		}
		if err != nil {
			return err
		}
		return item.Value(func(val []byte) error {
			return json.Unmarshal(val, &ids)
		})
	})
	return ids, err
}

// ListDirectories возвращает список уникальных директорий
func (s *Store) ListDirectories() ([]string, error) {
	dirs := make(map[string]bool)
	err := s.db.View(func(txn *badger.Txn) error {
		opts := badger.DefaultIteratorOptions
		opts.PrefetchValues = false
		it := txn.NewIterator(opts)
		defer it.Close()

		prefix := []byte(prefixByDir)
		for it.Seek(prefix); it.ValidForPrefix(prefix); it.Next() {
			key := string(it.Item().Key())
			dir := strings.TrimPrefix(key, prefixByDir)
			dirs[dir] = true
		}
		return nil
	})

	result := make([]string, 0, len(dirs))
	for dir := range dirs {
		result = append(result, dir)
	}
	return result, err
}

// === Album операции ===

// SaveAlbum сохраняет альбом
func (s *Store) SaveAlbum(album *Album) error {
	return s.db.Update(func(txn *badger.Txn) error {
		album.MediaCount = len(album.MediaIDs)
		data, err := json.Marshal(album)
		if err != nil {
			return err
		}
		return txn.Set([]byte(prefixAlbum+album.ID), data)
	})
}

// GetAlbum получает альбом по ID
func (s *Store) GetAlbum(id string) (*Album, error) {
	var album Album
	err := s.db.View(func(txn *badger.Txn) error {
		item, err := txn.Get([]byte(prefixAlbum + id))
		if err != nil {
			return err
		}
		return item.Value(func(val []byte) error {
			return json.Unmarshal(val, &album)
		})
	})
	if err == badger.ErrKeyNotFound {
		return nil, nil
	}
	return &album, err
}

// DeleteAlbum удаляет альбом
func (s *Store) DeleteAlbum(id string) error {
	return s.db.Update(func(txn *badger.Txn) error {
		return txn.Delete([]byte(prefixAlbum + id))
	})
}

// ListAlbums возвращает все альбомы
func (s *Store) ListAlbums() ([]*Album, error) {
	var result []*Album
	err := s.db.View(func(txn *badger.Txn) error {
		opts := badger.DefaultIteratorOptions
		it := txn.NewIterator(opts)
		defer it.Close()

		prefix := []byte(prefixAlbum)
		for it.Seek(prefix); it.ValidForPrefix(prefix); it.Next() {
			item := it.Item()
			err := item.Value(func(val []byte) error {
				var album Album
				if err := json.Unmarshal(val, &album); err != nil {
					return err
				}
				result = append(result, &album)
				return nil
			})
			if err != nil {
				return err
			}
		}
		return nil
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

	// Добавляем только уникальные ID
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
		if media != nil {
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

	err = s.db.Update(func(txn *badger.Txn) error {
		// Обновляем медиа
		data, err := json.Marshal(media)
		if err != nil {
			return err
		}
		if err := txn.Set([]byte(prefixMedia+media.ID), data); err != nil {
			return err
		}

		// Обновляем индекс избранного
		if media.IsFavorite {
			return s.addToIndex(txn, prefixFavorites, mediaID)
		}
		return s.removeFromIndex(txn, prefixFavorites, mediaID)
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

	return s.db.Update(func(txn *badger.Txn) error {
		data, err := json.Marshal(media)
		if err != nil {
			return err
		}
		if err := txn.Set([]byte(prefixMedia+media.ID), data); err != nil {
			return err
		}

		if isFavorite {
			return s.addToIndex(txn, prefixFavorites, mediaID)
		}
		return s.removeFromIndex(txn, prefixFavorites, mediaID)
	})
}

// ListFavorites возвращает все избранные медиа
func (s *Store) ListFavorites() ([]*Media, error) {
	ids, err := s.getIndex(prefixFavorites)
	if err != nil {
		return nil, err
	}

	var result []*Media
	for _, id := range ids {
		media, err := s.GetMedia(id)
		if err != nil {
			continue
		}
		if media != nil && media.IsFavorite {
			result = append(result, media)
		}
	}
	return result, nil
}

// === Per-User Favorites операции ===

// GetUserFavorites возвращает список ID избранных медиа для пользователя
func (s *Store) GetUserFavorites(userID string) ([]string, error) {
	return s.getIndex(prefixUserFavorites + userID)
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

// SetUserFavorite устанавливает статус избранного для конкретного пользователя
func (s *Store) SetUserFavorite(userID, mediaID string, isFavorite bool) error {
	// Проверяем, что медиа существует
	media, err := s.GetMedia(mediaID)
	if err != nil {
		return err
	}
	if media == nil {
		return fmt.Errorf("media not found")
	}

	return s.db.Update(func(txn *badger.Txn) error {
		key := prefixUserFavorites + userID
		if isFavorite {
			return s.addToIndex(txn, key, mediaID)
		}
		return s.removeFromIndex(txn, key, mediaID)
	})
}

// ToggleUserFavorite переключает статус избранного для пользователя
func (s *Store) ToggleUserFavorite(userID, mediaID string) (bool, error) {
	// Проверяем, что медиа существует
	media, err := s.GetMedia(mediaID)
	if err != nil {
		return false, err
	}
	if media == nil {
		return false, fmt.Errorf("media not found")
	}

	// Проверяем текущий статус
	isFavorite, err := s.IsUserFavorite(userID, mediaID)
	if err != nil {
		return false, err
	}

	// Переключаем
	newStatus := !isFavorite
	err = s.db.Update(func(txn *badger.Txn) error {
		key := prefixUserFavorites + userID
		if newStatus {
			return s.addToIndex(txn, key, mediaID)
		}
		return s.removeFromIndex(txn, key, mediaID)
	})

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
		if media != nil {
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

	return s.db.Update(func(txn *badger.Txn) error {
		for _, tag := range tags {
			tag = strings.TrimSpace(strings.ToLower(tag))
			if tag == "" {
				continue
			}
			if !existing[tag] {
				media.Tags = append(media.Tags, tag)
				existing[tag] = true
			}
			// Обновляем индекс тегов
			if err := s.addToIndex(txn, prefixByTag+tag, mediaID); err != nil {
				return err
			}
			// Обновляем счетчик тега
			if err := s.incrementTagCount(txn, tag); err != nil {
				return err
			}
		}

		data, err := json.Marshal(media)
		if err != nil {
			return err
		}
		return txn.Set([]byte(prefixMedia+mediaID), data)
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

	return s.db.Update(func(txn *badger.Txn) error {
		var newTags []string
		for _, t := range media.Tags {
			if !toRemove[t] {
				newTags = append(newTags, t)
			} else {
				// Удаляем из индекса
				if err := s.removeFromIndex(txn, prefixByTag+t, mediaID); err != nil {
					return err
				}
				// Уменьшаем счетчик
				if err := s.decrementTagCount(txn, t); err != nil {
					return err
				}
			}
		}
		media.Tags = newTags

		data, err := json.Marshal(media)
		if err != nil {
			return err
		}
		return txn.Set([]byte(prefixMedia+mediaID), data)
	})
}

// ListAllTags возвращает все теги с количеством
func (s *Store) ListAllTags() ([]*Tag, error) {
	var result []*Tag
	err := s.db.View(func(txn *badger.Txn) error {
		opts := badger.DefaultIteratorOptions
		it := txn.NewIterator(opts)
		defer it.Close()

		prefix := []byte(prefixTag)
		for it.Seek(prefix); it.ValidForPrefix(prefix); it.Next() {
			item := it.Item()
			err := item.Value(func(val []byte) error {
				var tag Tag
				if err := json.Unmarshal(val, &tag); err != nil {
					return err
				}
				result = append(result, &tag)
				return nil
			})
			if err != nil {
				return err
			}
		}
		return nil
	})
	return result, err
}

// ListMediaByTag возвращает медиа с определенным тегом
func (s *Store) ListMediaByTag(tag string) ([]*Media, error) {
	tag = strings.TrimSpace(strings.ToLower(tag))
	ids, err := s.getIndex(prefixByTag + tag)
	if err != nil {
		return nil, err
	}

	var result []*Media
	for _, id := range ids {
		media, err := s.GetMedia(id)
		if err != nil {
			continue
		}
		if media != nil {
			result = append(result, media)
		}
	}
	return result, nil
}

func (s *Store) incrementTagCount(txn *badger.Txn, tagName string) error {
	var tag Tag
	item, err := txn.Get([]byte(prefixTag + tagName))
	if err == badger.ErrKeyNotFound {
		tag = Tag{Name: tagName, MediaCount: 1}
	} else if err != nil {
		return err
	} else {
		err = item.Value(func(val []byte) error {
			return json.Unmarshal(val, &tag)
		})
		if err != nil {
			return err
		}
		tag.MediaCount++
	}

	data, err := json.Marshal(tag)
	if err != nil {
		return err
	}
	return txn.Set([]byte(prefixTag+tagName), data)
}

func (s *Store) decrementTagCount(txn *badger.Txn, tagName string) error {
	var tag Tag
	item, err := txn.Get([]byte(prefixTag + tagName))
	if err == badger.ErrKeyNotFound {
		return nil
	} else if err != nil {
		return err
	}

	err = item.Value(func(val []byte) error {
		return json.Unmarshal(val, &tag)
	})
	if err != nil {
		return err
	}

	tag.MediaCount--
	if tag.MediaCount <= 0 {
		return txn.Delete([]byte(prefixTag + tagName))
	}

	data, err := json.Marshal(tag)
	if err != nil {
		return err
	}
	return txn.Set([]byte(prefixTag+tagName), data)
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

	// Применяем offset и limit
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
	// Текстовый поиск
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

	// Тип медиа
	if q.Type != "" && m.Type != q.Type {
		return false
	}

	// Диапазон дат
	if q.DateFrom != nil && m.TakenAt.Before(*q.DateFrom) {
		return false
	}
	if q.DateTo != nil && m.TakenAt.After(*q.DateTo) {
		return false
	}

	// Теги
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

	// Камера
	if q.Camera != "" && !strings.Contains(strings.ToLower(m.Metadata.Camera), strings.ToLower(q.Camera)) {
		return false
	}

	// Избранное
	if q.IsFavorite != nil && m.IsFavorite != *q.IsFavorite {
		return false
	}

	// GPS
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

	// Группируем по месяцам
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

	// Конвертируем в слайс и сортируем
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

// GetTimelineMedia возвращает медиа для конкретного периода
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

// GetGeoPoints возвращает все точки с GPS данными
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
		if media != nil {
			result = append(result, media)
		}
	}
	return result, nil
}
