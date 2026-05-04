package storage

import (
	"encoding/json"

	bolt "go.etcd.io/bbolt"
)

// SaveMedia сохраняет медиа-файл
func (s *Store) SaveMedia(m *Media) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		data, err := json.Marshal(m)
		if err != nil {
			return err
		}

		b := tx.Bucket(bucketMedia)
		if err := b.Put([]byte(m.ID), data); err != nil {
			return err
		}

		if err := addToIndex(tx, bucketIdxDir, m.Dir, m.ID); err != nil {
			return err
		}

		if !m.TakenAt.IsZero() {
			dateKey := FormatYearMonth(m.TakenAt)
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

	s.removeMediaFromAllAlbums(id)

	s.db.Update(func(tx *bolt.Tx) error {
		return removeFromIndex(tx, bucketFavorites, favoriteGlobalKey, id)
	})

	s.removeMediaFromAllUserFavorites(id)
	s.removeMediaFromAllTags(media.Tags)

	return s.db.Update(func(tx *bolt.Tx) error {
		if err := removeFromIndex(tx, bucketIdxDir, media.Dir, id); err != nil {
			return err
		}

		if !media.TakenAt.IsZero() {
			dateKey := FormatYearMonth(media.TakenAt)
			if err := removeFromIndex(tx, bucketIdxDate, dateKey, id); err != nil {
				return err
			}
		}

		return tx.Bucket(bucketMedia).Delete([]byte(id))
	})
}

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
				return nil
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
