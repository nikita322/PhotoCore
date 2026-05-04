package storage

// GetTimeline возвращает группировку медиа по месяцам
func (s *Store) GetTimeline() ([]*TimelineGroup, error) {
	allMedia, err := s.ListAllMedia()
	if err != nil {
		return nil, err
	}

	groups := make(map[string]*TimelineGroup)
	for _, m := range allMedia {
		var date string
		if !m.TakenAt.IsZero() {
			date = FormatYearMonth(m.TakenAt)
		} else {
			date = FormatYearMonth(m.ModifiedAt)
		}

		if groups[date] == nil {
			groups[date] = &TimelineGroup{
				Date:  date,
				Label: formatMonthLabel(date),
			}
		}
		groups[date].MediaCount++
	}

	var result []*TimelineGroup
	for _, g := range groups {
		result = append(result, g)
	}

	for i := 0; i < len(result)-1; i++ {
		for j := i + 1; j < len(result); j++ {
			if result[i].Date < result[j].Date {
				result[i], result[j] = result[j], result[i]
			}
		}
	}

	return result, nil
}

// GetTimelineMedia возвращает медиа для периода
func (s *Store) GetTimelineMedia(period string) ([]*Media, error) {
	allMedia, err := s.ListAllMedia()
	if err != nil {
		return nil, err
	}

	var result []*Media
	for _, m := range allMedia {
		var date string
		if !m.TakenAt.IsZero() {
			date = FormatYearMonth(m.TakenAt)
		} else {
			date = FormatYearMonth(m.ModifiedAt)
		}

		if date == period {
			result = append(result, m)
		}
	}

	return result, nil
}

func formatMonthLabel(date string) string {
	months := map[string]string{
		"01": "Январь", "02": "Февраль", "03": "Март",
		"04": "Апрель", "05": "Май", "06": "Июнь",
		"07": "Июль", "08": "Август", "09": "Сентябрь",
		"10": "Октябрь", "11": "Ноябрь", "12": "Декабрь",
	}
	if len(date) >= 7 {
		year := date[:4]
		month := date[5:7]
		if m, ok := months[month]; ok {
			return m + " " + year
		}
	}
	return date
}
