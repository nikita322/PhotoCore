package storage

import (
	"encoding/json"

	bolt "go.etcd.io/bbolt"
)

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
