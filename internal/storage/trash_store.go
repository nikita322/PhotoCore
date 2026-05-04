package storage

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/photocore/photocore/internal/logger"
	bolt "go.etcd.io/bbolt"
)

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
