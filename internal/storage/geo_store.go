package storage

// GetGeoPoints возвращает все точки с GPS
func (s *Store) GetGeoPoints() ([]*GeoPoint, error) {
	allMedia, err := s.ListAllMedia()
	if err != nil {
		return nil, err
	}

	var result []*GeoPoint
	for _, m := range allMedia {
		if m.Metadata.GPSLat != 0 || m.Metadata.GPSLon != 0 {
			result = append(result, &GeoPoint{
				MediaID:  m.ID,
				Lat:      m.Metadata.GPSLat,
				Lon:      m.Metadata.GPSLon,
				ThumbURL: "/media/" + m.ID + "/thumb/small",
			})
		}
	}
	return result, nil
}
