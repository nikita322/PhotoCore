package storage

// BulkSetFavorite устанавливает избранное для нескольких медиа
func (s *Store) BulkSetFavorite(mediaIDs []string, isFavorite bool) error {
	for _, id := range mediaIDs {
		if err := s.SetFavorite(id, isFavorite); err != nil {
			return err
		}
	}
	return nil
}

// BulkAddTags добавляет теги к нескольким медиа
func (s *Store) BulkAddTags(mediaIDs []string, tags []string) error {
	for _, id := range mediaIDs {
		if err := s.AddTagsToMedia(id, tags); err != nil {
			return err
		}
	}
	return nil
}

// BulkDelete удаляет несколько медиа
func (s *Store) BulkDelete(mediaIDs []string) error {
	for _, id := range mediaIDs {
		if err := s.DeleteMedia(id); err != nil {
			return err
		}
	}
	return nil
}

// GetMediaByIDs возвращает медиа по списку ID
func (s *Store) GetMediaByIDs(ids []string) ([]*Media, error) {
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
