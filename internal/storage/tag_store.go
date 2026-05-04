package storage

import (
	"encoding/json"
	"fmt"
	"strings"

	bolt "go.etcd.io/bbolt"
)

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
