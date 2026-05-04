package storage

import (
	"encoding/json"

	bolt "go.etcd.io/bbolt"
)

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
