package storage

import (
	"encoding/json"
	"fmt"

	bolt "go.etcd.io/bbolt"
)

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
