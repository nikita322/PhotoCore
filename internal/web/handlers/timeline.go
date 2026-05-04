package handlers

import (
	"net/http"
	"sort"

	"github.com/go-chi/chi/v5"
	"github.com/photocore/photocore/internal/storage"
)

// Timeline возвращает группировку медиа по датам
func (h *Handlers) Timeline(w http.ResponseWriter, r *http.Request) {
	timeline, err := h.store.GetTimeline()
	if err != nil {
		h.jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if h.wantsHTML(r) {
		data := h.baseData(r)
		data["Timeline"] = timeline
		h.render(w, "gallery.html", data)
		return
	}

	h.jsonResponse(w, timeline)
}

// TimelineMedia возвращает медиа за определенный период
func (h *Handlers) TimelineMedia(w http.ResponseWriter, r *http.Request) {
	period := chi.URLParam(r, "period")

	media, err := h.store.GetTimelineMedia(period)
	if err != nil {
		h.jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	sort.Slice(media, func(i, j int) bool {
		return media[i].TakenAt.After(media[j].TakenAt)
	})

	if h.wantsHTML(r) {
		h.renderPartial(w, "gallery_content.html", map[string]interface{}{
			"Media":  media,
			"Period": period,
		})
		return
	}

	h.jsonResponse(w, media)
}

// TimelineAllMedia возвращает все медиа сгруппированные по периодам
func (h *Handlers) TimelineAllMedia(w http.ResponseWriter, r *http.Request) {
	allMedia, err := h.store.ListAllMedia()
	if err != nil {
		h.jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	sort.Slice(allMedia, func(i, j int) bool {
		dateI := allMedia[i].TakenAt
		if dateI.IsZero() {
			dateI = allMedia[i].ModifiedAt
		}
		dateJ := allMedia[j].TakenAt
		if dateJ.IsZero() {
			dateJ = allMedia[j].ModifiedAt
		}
		return dateI.After(dateJ)
	})

	type MediaGroup struct {
		Period string
		Label  string
		Media  []*storage.Media
	}

	months := map[string]string{
		"01": "Январь", "02": "Февраль", "03": "Март",
		"04": "Апрель", "05": "Май", "06": "Июнь",
		"07": "Июль", "08": "Август", "09": "Сентябрь",
		"10": "Октябрь", "11": "Ноябрь", "12": "Декабрь",
	}

	var groups []MediaGroup
	var currentPeriod string
	var currentGroup *MediaGroup

	for _, m := range allMedia {
		var date string
		if !m.TakenAt.IsZero() {
			date = storage.FormatYearMonth(m.TakenAt)
		} else {
			date = storage.FormatYearMonth(m.ModifiedAt)
		}

		if date != currentPeriod {
			if currentGroup != nil {
				groups = append(groups, *currentGroup)
			}
			label := date
			if len(date) >= 7 {
				year := date[:4]
				month := date[5:7]
				if monthName, ok := months[month]; ok {
					label = monthName + " " + year
				}
			}
			currentGroup = &MediaGroup{Period: date, Label: label, Media: []*storage.Media{}}
			currentPeriod = date
		}
		currentGroup.Media = append(currentGroup.Media, m)
	}
	if currentGroup != nil && len(currentGroup.Media) > 0 {
		groups = append(groups, *currentGroup)
	}

	if h.wantsHTML(r) {
		h.renderPartial(w, "gallery_all.html", map[string]interface{}{
			"Groups": groups,
			"Total":  len(allMedia),
		})
		return
	}

	h.jsonResponse(w, groups)
}
