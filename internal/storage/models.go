package storage

import (
	"time"
)

// MediaType определяет тип медиа-файла
type MediaType string

const (
	MediaTypeImage MediaType = "image"
	MediaTypeVideo MediaType = "video"
	MediaTypeRaw   MediaType = "raw"
)

// Роли пользователей
const (
	RoleAdmin  = "admin"  // Полный доступ + управление пользователями
	RoleEditor = "editor" // Создание альбомов, теги (без удаления)
	RoleViewer = "viewer" // Только просмотр и своё избранное
)

// Media представляет медиа-файл в галерее
type Media struct {
	ID          string     `json:"id"`                     // SHA256 от пути
	Path        string     `json:"path"`                   // Полный путь к файлу
	RelPath     string     `json:"rel_path"`               // Относительный путь от корня медиа
	Dir         string     `json:"dir"`                    // Директория файла
	Filename    string     `json:"filename"`               // Имя файла
	Ext         string     `json:"ext"`                    // Расширение (.jpg, .mp4, etc)
	Type        MediaType  `json:"type"`                   // image, video, raw
	MimeType    string     `json:"mime_type"`              // MIME тип
	Size        int64      `json:"size"`                   // Размер в байтах
	Width       int        `json:"width"`                  // Ширина (для изображений/видео)
	Height      int        `json:"height"`                 // Высота
	Duration    float64    `json:"duration"`               // Длительность (для видео)
	TakenAt     time.Time  `json:"taken_at"`               // Дата съемки (EXIF)
	CreatedAt   time.Time  `json:"created_at"`             // Дата добавления в БД
	ModifiedAt  time.Time  `json:"modified_at"`            // Дата модификации файла
	DeletedAt   *time.Time `json:"deleted_at"`             // Дата удаления (nil = не удалено)
	Checksum    string     `json:"checksum"`               // SHA256 хеш файла (для точных дубликатов)
	ImageHash   uint64     `json:"image_hash"`             // Perceptual hash (для визуальных дубликатов)
	DuplicateOf string     `json:"duplicate_of,omitempty"` // ID оригинала (если дубликат)
	ThumbSmall  string     `json:"thumb_small"`            // Путь к маленькому превью
	ThumbLarge  string     `json:"thumb_large"`            // Путь к большому превью
	Metadata    Metadata   `json:"metadata"`               // Дополнительные метаданные
	IsFavorite  bool       `json:"is_favorite"`            // Отмечено как избранное
	Tags        []string   `json:"tags"`                   // Теги
}

// Metadata содержит EXIF и другие метаданные
type Metadata struct {
	Camera       string  `json:"camera,omitempty"`
	Lens         string  `json:"lens,omitempty"`
	FocalLength  string  `json:"focal_length,omitempty"`
	Aperture     string  `json:"aperture,omitempty"`
	ShutterSpeed string  `json:"shutter_speed,omitempty"`
	ISO          int     `json:"iso,omitempty"`
	GPSLat       float64 `json:"gps_lat,omitempty"`
	GPSLon       float64 `json:"gps_lon,omitempty"`
	Orientation  int     `json:"orientation,omitempty"`
}

// User представляет пользователя системы
type User struct {
	ID           string    `json:"id"`
	Username     string    `json:"username"`
	DisplayName  string    `json:"display_name"` // Отображаемое имя
	PasswordHash string    `json:"password_hash"`
	Role         string    `json:"role"` // admin, editor, viewer
	CreatedAt    time.Time `json:"created_at"`
	LastLogin    time.Time `json:"last_login"`
}

// Session представляет сессию пользователя
type Session struct {
	ID        string    `json:"id"`
	UserID    string    `json:"user_id"`
	Username  string    `json:"username"`
	Role      string    `json:"role"`
	CreatedAt time.Time `json:"created_at"`
	ExpiresAt time.Time `json:"expires_at"`
}

// APIToken представляет токен для API доступа
type APIToken struct {
	Token      string    `json:"token"`
	UserID     string    `json:"user_id"`
	Username   string    `json:"username"`
	Role       string    `json:"role"`
	CreatedAt  time.Time `json:"created_at"`
	ExpiresAt  time.Time `json:"expires_at"`
	DeviceName string    `json:"device_name"`
}

// Directory представляет директорию в галерее
type Directory struct {
	Path       string `json:"path"`
	Name       string `json:"name"`
	MediaCount int    `json:"media_count"`
	SubDirs    int    `json:"sub_dirs"`
}

// Stats содержит статистику галереи
type Stats struct {
	TotalMedia  int   `json:"total_media"`
	TotalImages int   `json:"total_images"`
	TotalVideos int   `json:"total_videos"`
	TotalRaw    int   `json:"total_raw"`
	TotalSize   int64 `json:"total_size"`
	TotalDirs   int   `json:"total_dirs"`
}

// Album представляет альбом (коллекцию медиа)
type Album struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	CoverID     string    `json:"cover_id"`      // ID медиа для обложки
	MediaIDs    []string  `json:"media_ids"`     // ID медиа в альбоме
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
	MediaCount  int       `json:"media_count"`   // Кэшированное количество
}

// Tag представляет тег для организации медиа
type Tag struct {
	Name       string `json:"name"`
	MediaCount int    `json:"media_count"` // Количество медиа с этим тегом
}

// SearchQuery представляет параметры поиска
type SearchQuery struct {
	Text       string     `json:"text"`        // Поиск по тексту (имя файла, метаданные)
	Type       MediaType  `json:"type"`        // Фильтр по типу
	DateFrom   *time.Time `json:"date_from"`   // От даты
	DateTo     *time.Time `json:"date_to"`     // До даты
	Tags       []string   `json:"tags"`        // Фильтр по тегам
	Camera     string     `json:"camera"`      // Фильтр по камере
	IsFavorite *bool      `json:"is_favorite"` // Только избранное
	HasGPS     *bool      `json:"has_gps"`     // Только с геоданными
	AlbumID    string     `json:"album_id"`    // В конкретном альбоме
	Limit      int        `json:"limit"`
	Offset     int        `json:"offset"`
}

// SearchResult представляет результат поиска
type SearchResult struct {
	Media      []*Media `json:"media"`
	TotalCount int      `json:"total_count"`
	HasMore    bool     `json:"has_more"`
}

// TimelineGroup группа медиа по дате
type TimelineGroup struct {
	Date       string   `json:"date"`        // YYYY-MM или YYYY-MM-DD
	Label      string   `json:"label"`       // Человекочитаемая метка
	MediaCount int      `json:"media_count"`
	Media      []*Media `json:"media,omitempty"`
}

// GeoPoint представляет точку на карте
type GeoPoint struct {
	MediaID  string  `json:"media_id"`
	Lat      float64 `json:"lat"`
	Lon      float64 `json:"lon"`
	ThumbURL string  `json:"thumb_url"`
}

// DuplicateGroup представляет группу дубликатов
type DuplicateGroup struct {
	Type     string   `json:"type"`      // "exact" или "similar"
	Media    []*Media `json:"media"`     // Медиа в группе
	Distance int      `json:"distance"`  // Hamming distance (для similar)
}
