package storage

import (
	"encoding/json"
	"fmt"

	bolt "go.etcd.io/bbolt"
)

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
			return addToIndex(tx, bucketFavorites, favoriteGlobalKey, mediaID)
		}
		return removeFromIndex(tx, bucketFavorites, favoriteGlobalKey, mediaID)
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
			return addToIndex(tx, bucketFavorites, favoriteGlobalKey, mediaID)
		}
		return removeFromIndex(tx, bucketFavorites, favoriteGlobalKey, mediaID)
	})
}

// ListFavorites возвращает все избранные медиа
func (s *Store) ListFavorites() ([]*Media, error) {
	ids, err := s.getIndex(bucketFavorites, favoriteGlobalKey)
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
			for _, id := range ids {
				if id == mediaID {
					return nil
				}
			}
			ids = append(ids, mediaID)
		} else {
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
