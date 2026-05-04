package storage

import (
	"encoding/json"

	bolt "go.etcd.io/bbolt"
)

func addToIndex(tx *bolt.Tx, bucket []byte, key, id string) error {
	b := tx.Bucket(bucket)
	var ids []string

	data := b.Get([]byte(key))
	if data != nil {
		json.Unmarshal(data, &ids)
	}

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
