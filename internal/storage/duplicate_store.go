package storage

import (
	"encoding/json"
	"errors"
	"os"
	"strconv"

	bolt "go.etcd.io/bbolt"
)

// DuplicateCheckResult результат проверки на дубликат
type DuplicateCheckResult struct {
	IsDuplicate bool          // Является ли дубликатом
	Type        DuplicateType // exact или similar
	ExistingID  string        // ID существующего медиа
	Distance    int           // Расстояние Хэмминга (для similar)
}

// HammingDistance возвращает расстояние Хэмминга между двумя 64-битными hash
func HammingDistance(hash1, hash2 uint64) int {
	xor := hash1 ^ hash2
	distance := 0
	for xor != 0 {
		distance++
		xor &= xor - 1 // Сбрасываем младший установленный бит
	}
	return distance
}

// FindDuplicates находит дубликаты медиа
func (s *Store) FindDuplicates(similarityThreshold int) ([]*DuplicateGroup, error) {
	var groups []*DuplicateGroup

	// Exact duplicates via idx_checksum
	err := s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketIdxChecksum)
		return b.ForEach(func(k, v []byte) error {
			var ids []string
			if err := json.Unmarshal(v, &ids); err != nil {
				return nil
			}
			if len(ids) <= 1 {
				return nil
			}
			var mediaList []*Media
			for _, id := range ids {
				m, err := s.GetMedia(id)
				if err != nil || m == nil || m.DeletedAt != nil {
					continue
				}
				mediaList = append(mediaList, m)
			}
			if len(mediaList) > 1 {
				groups = append(groups, &DuplicateGroup{
					Type:     DuplicateTypeExact,
					Media:    mediaList,
					Distance: 0,
				})
			}
			return nil
		})
	})
	if err != nil {
		return nil, err
	}

	// Collect exact IDs to skip
	exactIDs := make(map[string]bool)
	for _, g := range groups {
		if g.Type == DuplicateTypeExact {
			for _, m := range g.Media {
				exactIDs[m.ID] = true
			}
		}
	}

	// Similar duplicates via idx_hash
	type hashEntry struct {
		hash uint64
		ids  []string
	}
	var entries []hashEntry

	err = s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketIdxHash)
		return b.ForEach(func(k, v []byte) error {
			var ids []string
			if err := json.Unmarshal(v, &ids); err != nil {
				return nil
			}
			hashVal, _ := strconv.ParseUint(string(k), 16, 64)
			entries = append(entries, hashEntry{hash: hashVal, ids: ids})
			return nil
		})
	})
	if err != nil {
		return nil, err
	}

	processed := make(map[string]bool)
	for _, e := range entries {
		for _, id := range e.ids {
			if exactIDs[id] || processed[id] {
				continue
			}
			m, err := s.GetMedia(id)
			if err != nil || m == nil || m.DeletedAt != nil {
				continue
			}
			processed[id] = true

			var similarGroup []*Media
			similarGroup = append(similarGroup, m)

			for _, e2 := range entries {
				for _, id2 := range e2.ids {
					if id2 == id || processed[id2] || exactIDs[id2] {
						continue
					}
					m2, err := s.GetMedia(id2)
					if err != nil || m2 == nil || m2.DeletedAt != nil {
						continue
					}
					distance := HammingDistance(e.hash, e2.hash)
					if distance <= similarityThreshold {
						similarGroup = append(similarGroup, m2)
						processed[id2] = true
					}
				}
			}

			if len(similarGroup) > 1 {
				groups = append(groups, &DuplicateGroup{
					Type:     DuplicateTypeSimilar,
					Media:    similarGroup,
					Distance: similarityThreshold,
				})
			}
		}
	}

	return groups, nil
}

// ChecksumExists проверяет, существует ли медиа с таким же checksum
func (s *Store) ChecksumExists(checksum string) (string, error) {
	if checksum == "" {
		return "", nil
	}

	ids, err := s.getIndex(bucketIdxChecksum, checksum)
	if err != nil {
		return "", err
	}
	for _, id := range ids {
		media, err := s.GetMedia(id)
		if err != nil || media == nil || media.DeletedAt != nil {
			continue
		}
		return id, nil
	}

	// Fallback: empty index (old DB) -> full scan
	return s.checksumExistsFallback(checksum)
}

func (s *Store) checksumExistsFallback(checksum string) (string, error) {
	var existingID string
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
			if media.Checksum == checksum {
				existingID = media.ID
				return errFound
			}
			return nil
		})
	})

	if err != nil && !errors.Is(err, errFound) {
		return "", err
	}

	return existingID, nil
}

// FindMediaBySizeRange находит медиа с размером в диапазоне ±10%
func (s *Store) FindMediaBySizeRange(size int64) ([]*Media, error) {
	minSize := int64(float64(size) * SizeToleranceLower)
	maxSize := int64(float64(size) * SizeToleranceUpper)

	var result []*Media
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
			if media.Size >= minSize && media.Size <= maxSize {
				result = append(result, &media)
			}
			return nil
		})
	})
	return result, err
}

// CheckDuplicate выполняет гибридную проверку на дубликат
func (s *Store) CheckDuplicate(size int64, checksum string, imageHash uint64, isImage bool, similarityThreshold int, checkOriginalExists bool) (*DuplicateCheckResult, error) {
	result := &DuplicateCheckResult{IsDuplicate: false}

	// 1. Exact duplicate via checksum index
	if checksum != "" {
		ids, err := s.getIndex(bucketIdxChecksum, checksum)
		if err != nil {
			return nil, err
		}
		for _, id := range ids {
			m, err := s.GetMedia(id)
			if err != nil || m == nil || m.DeletedAt != nil {
				continue
			}
			if m.Checksum == checksum {
				if checkOriginalExists {
					if _, err := os.Stat(m.Path); os.IsNotExist(err) {
						continue
					}
				}
				result.IsDuplicate = true
				result.Type = DuplicateTypeExact
				result.ExistingID = id
				result.Distance = 0
				return result, nil
			}
		}

		// Fallback for old DB without index
		fallbackID, err := s.checksumExistsFallback(checksum)
		if err != nil {
			return nil, err
		}
		if fallbackID != "" {
			m, _ := s.GetMedia(fallbackID)
			if m != nil {
				if checkOriginalExists {
					if _, err := os.Stat(m.Path); os.IsNotExist(err) {
						// original file missing, don't treat as duplicate
					} else {
						result.IsDuplicate = true
						result.Type = DuplicateTypeExact
						result.ExistingID = fallbackID
						result.Distance = 0
						return result, nil
					}
				} else {
					result.IsDuplicate = true
					result.Type = DuplicateTypeExact
					result.ExistingID = fallbackID
					result.Distance = 0
					return result, nil
				}
			}
		}
	}

	// 2. Similar duplicate via hash index
	if isImage && imageHash != 0 {
		bestDistance := similarityThreshold + 1
		var bestID string

		err := s.db.View(func(tx *bolt.Tx) error {
			b := tx.Bucket(bucketIdxHash)
			return b.ForEach(func(k, v []byte) error {
				hashVal, parseErr := strconv.ParseUint(string(k), 16, 64)
				if parseErr != nil {
					return nil
				}
				distance := HammingDistance(imageHash, hashVal)
				if distance > similarityThreshold || distance >= bestDistance {
					return nil
				}
				var ids []string
				if json.Unmarshal(v, &ids) != nil {
					return nil
				}
				for _, id := range ids {
					m, err := s.GetMedia(id)
					if err != nil || m == nil || m.DeletedAt != nil {
						continue
					}
					if checkOriginalExists {
						if _, err := os.Stat(m.Path); os.IsNotExist(err) {
							continue
						}
					}
					bestDistance = distance
					bestID = id
				}
				return nil
			})
		})
		if err != nil {
			return nil, err
		}

		if bestID != "" {
			result.IsDuplicate = true
			result.Type = DuplicateTypeSimilar
			result.ExistingID = bestID
			result.Distance = bestDistance
			return result, nil
		}

		// Fallback for old DB
		return s.checkDuplicateFallback(size, imageHash, similarityThreshold, checkOriginalExists)
	}

	return result, nil
}

func (s *Store) checkDuplicateFallback(size int64, imageHash uint64, similarityThreshold int, checkOriginalExists bool) (*DuplicateCheckResult, error) {
	result := &DuplicateCheckResult{IsDuplicate: false}
	candidates, err := s.FindMediaBySizeRange(size)
	if err != nil {
		return nil, err
	}
	for _, m := range candidates {
		if m.ImageHash == 0 || m.DeletedAt != nil {
			continue
		}
		distance := HammingDistance(imageHash, m.ImageHash)
		if distance <= similarityThreshold {
			if checkOriginalExists {
				if _, err := os.Stat(m.Path); os.IsNotExist(err) {
					continue
				}
			}
			result.IsDuplicate = true
			result.Type = DuplicateTypeSimilar
			result.ExistingID = m.ID
			result.Distance = distance
			return result, nil
		}
	}
	return result, nil
}

// GetDuplicatesStats возвращает статистику дубликатов
func (s *Store) GetDuplicatesStats() (exactCount int, similarCount int, savedSpace int64, err error) {
	groups, err := s.FindDuplicates(DefaultDuplicateSimilarityThreshold)
	if err != nil {
		return 0, 0, 0, err
	}

	for _, g := range groups {
		if g.Type == DuplicateTypeExact {
			exactCount += len(g.Media) - 1
			for i := 1; i < len(g.Media); i++ {
				savedSpace += g.Media[i].Size
			}
		} else {
			similarCount += len(g.Media) - 1
		}
	}

	return exactCount, similarCount, savedSpace, nil
}
