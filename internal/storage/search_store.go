package storage

import "strings"

// Search выполняет поиск медиа
func (s *Store) Search(query *SearchQuery) (*SearchResult, error) {
	if query.Limit <= 0 {
		query.Limit = DefaultSearchLimit
	}

	allMedia, err := s.ListAllMedia()
	if err != nil {
		return nil, err
	}

	var filtered []*Media
	for _, m := range allMedia {
		if s.matchesQuery(m, query) {
			filtered = append(filtered, m)
		}
	}

	totalCount := len(filtered)

	start := query.Offset
	if start > len(filtered) {
		start = len(filtered)
	}
	end := start + query.Limit
	if end > len(filtered) {
		end = len(filtered)
	}

	result := &SearchResult{
		Media:      filtered[start:end],
		TotalCount: totalCount,
		HasMore:    end < totalCount,
	}

	return result, nil
}

func (s *Store) matchesQuery(m *Media, q *SearchQuery) bool {
	if q.Text != "" {
		text := strings.ToLower(q.Text)
		filename := strings.ToLower(m.Filename)
		camera := strings.ToLower(m.Metadata.Camera)
		lens := strings.ToLower(m.Metadata.Lens)

		if !strings.Contains(filename, text) &&
			!strings.Contains(camera, text) &&
			!strings.Contains(lens, text) {
			return false
		}
	}

	if q.Type != "" && m.Type != q.Type {
		return false
	}

	if q.DateFrom != nil && m.TakenAt.Before(*q.DateFrom) {
		return false
	}
	if q.DateTo != nil && m.TakenAt.After(*q.DateTo) {
		return false
	}

	if len(q.Tags) > 0 {
		mediaTagSet := make(map[string]bool)
		for _, t := range m.Tags {
			mediaTagSet[t] = true
		}
		for _, t := range q.Tags {
			if !mediaTagSet[strings.ToLower(t)] {
				return false
			}
		}
	}

	if q.Camera != "" && !strings.Contains(strings.ToLower(m.Metadata.Camera), strings.ToLower(q.Camera)) {
		return false
	}

	if q.IsFavorite != nil && m.IsFavorite != *q.IsFavorite {
		return false
	}

	if q.HasGPS != nil {
		hasGPS := m.Metadata.GPSLat != 0 || m.Metadata.GPSLon != 0
		if hasGPS != *q.HasGPS {
			return false
		}
	}

	return true
}
