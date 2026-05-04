package storage

import (
	"encoding/json"
	"errors"

	bolt "go.etcd.io/bbolt"
)

// DuplicateCheckResult результат проверки на дубликат
type DuplicateCheckResult struct {
	IsDuplicate bool          // Является ли дубликатом
	Type        DuplicateType // exact или similar
	ExistingID  string        // ID существующего медиа
	Distance    int           // Расстояние Хэмминга (для similar)
}

// FindDuplicates находит дубликаты медиа
func (s *Store) FindDuplicates(similarityThreshold int) ([]*DuplicateGroup, error) {
	allMedia, err := s.ListAllMedia()
	if err != nil {
		return nil, err
	}

	var groups []*DuplicateGroup

	checksumGroups := make(map[string][]*Media)
	for _, m := range allMedia {
		if m.Checksum != "" {
			checksumGroups[m.Checksum] = append(checksumGroups[m.Checksum], m)
		}
	}

	for _, mediaList := range checksumGroups {
		if len(mediaList) > 1 {
			groups = append(groups, &DuplicateGroup{
				Type:     DuplicateTypeExact,
				Media:    mediaList,
				Distance: 0,
			})
		}
	}

	var imagesWithHash []*Media
	for _, m := range allMedia {
		if m.ImageHash != 0 && (m.Type == MediaTypeImage || m.Type == MediaTypeRaw) {
			isExact := false
			for _, g := range groups {
				if g.Type == DuplicateTypeExact {
					for _, em := range g.Media {
						if em.ID == m.ID {
							isExact = true
							break
						}
					}
				}
				if isExact {
					break
				}
			}
			if !isExact {
				imagesWithHash = append(imagesWithHash, m)
			}
		}
	}

	processed := make(map[string]bool)
	for i, m1 := range imagesWithHash {
		if processed[m1.ID] {
			continue
		}

		var similarGroup []*Media
		similarGroup = append(similarGroup, m1)
		processed[m1.ID] = true

		for j := i + 1; j < len(imagesWithHash); j++ {
			m2 := imagesWithHash[j]
			if processed[m2.ID] {
				continue
			}

			distance := hammingDistance(m1.ImageHash, m2.ImageHash)
			if distance <= similarityThreshold {
				similarGroup = append(similarGroup, m2)
				processed[m2.ID] = true
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

	return groups, nil
}

func hammingDistance(hash1, hash2 uint64) int {
	xor := hash1 ^ hash2
	distance := 0
	for xor != 0 {
		distance++
		xor &= xor - 1
	}
	return distance
}

// ChecksumExists проверяет, существует ли медиа с таким же checksum
func (s *Store) ChecksumExists(checksum string) (string, error) {
	if checksum == "" {
		return "", nil
	}

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
func (s *Store) CheckDuplicate(size int64, checksum string, imageHash uint64, isImage bool, similarityThreshold int) (*DuplicateCheckResult, error) {
	result := &DuplicateCheckResult{IsDuplicate: false}

	if checksum != "" {
		candidates, err := s.FindMediaBySizeRange(size)
		if err != nil {
			return nil, err
		}
		for _, m := range candidates {
			if m.Checksum == checksum {
				result.IsDuplicate = true
				result.Type = DuplicateTypeExact
				result.ExistingID = m.ID
				result.Distance = 0
				return result, nil
			}
		}
	}

	if isImage && imageHash != 0 {
		allMedia, err := s.ListAllMedia()
		if err != nil {
			return nil, err
		}
		for _, m := range allMedia {
			if m.ImageHash == 0 {
				continue
			}
			distance := hammingDistance(imageHash, m.ImageHash)
			if distance <= similarityThreshold {
				result.IsDuplicate = true
				result.Type = DuplicateTypeSimilar
				result.ExistingID = m.ID
				result.Distance = distance
				return result, nil
			}
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
