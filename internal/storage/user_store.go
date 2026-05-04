package storage

import (
	"encoding/json"

	bolt "go.etcd.io/bbolt"
)

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
			tx.Bucket(bucketUserFav).Delete([]byte(user.ID))
		}

		return b.Delete([]byte(username))
	})
}
