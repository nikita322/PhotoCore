package config

import (
	"os"

	"gopkg.in/yaml.v3"
)

// Config представляет полную конфигурацию приложения
type Config struct {
	Server     ServerConfig     `yaml:"server"`     // Настройки HTTP-сервера
	Storage    StorageConfig    `yaml:"storage"`    // Пути к медиа, кэшу, БД и логам
	Thumbnails ThumbnailsConfig `yaml:"thumbnails"` // Размеры и качество превью
	Auth       AuthConfig       `yaml:"auth"`       // Аутентификация и сессии
	Scan       ScanConfig       `yaml:"scan"`       // Расширения файлов для сканирования
	Tools      ToolsConfig      `yaml:"tools"`      // Внешние утилиты (dcraw, ffmpeg)
}

// ServerConfig содержит настройки HTTP-сервера
type ServerConfig struct {
	Host string `yaml:"host"` // Адрес привязки (0.0.0.0 для всех интерфейсов)
	Port int    `yaml:"port"` // Порт сервера
}

// StorageConfig содержит пути к хранилищам данных
type StorageConfig struct {
	MediaPaths []string `yaml:"media_paths"` // Пути к медиафайлам
	CachePath  string   `yaml:"cache_path"`  // Путь к кэшу превью
	DBPath     string   `yaml:"db_path"`     // Путь к файлу БД BoltDB
	LogsPath   string   `yaml:"logs_path"`   // Путь к директории логов
}

// ThumbnailsConfig содержит настройки генерации превью
type ThumbnailsConfig struct {
	Small   int `yaml:"small"`   // Ширина маленького превью (px)
	Medium  int `yaml:"medium"`  // Ширина среднего превью (px)
	Large   int `yaml:"large"`   // Ширина большого превью (px)
	Quality int `yaml:"quality"` // Качество JPEG (0-100)
}

// AuthConfig содержит настройки аутентификации
type AuthConfig struct {
	SessionSecret string `yaml:"session_secret"`  // Секрет для подписи сессий
	SessionMaxAge int    `yaml:"session_max_age"` // Время жизни сессии в секундах
	AdminUsername string `yaml:"admin_username"`  // Логин администратора по умолчанию
	AdminPassword string `yaml:"admin_password"`  // Пароль администратора по умолчанию
}

// ScanConfig содержит настройки сканирования файлов
type ScanConfig struct {
	Extensions                   ExtensionsConfig `yaml:"extensions"`                      // Расширения по типам медиа
	DuplicateSimilarityThreshold int              `yaml:"duplicate_similarity_threshold"`  // Порог Hamming distance для визуальных дубликатов
	EnableDuplicateDetection     bool             `yaml:"enable_duplicate_detection"`      // Включить детекцию дубликатов
	DuplicateCheckOriginalExists bool             `yaml:"duplicate_check_original_exists"` // Проверять существование оригинала на диске
}

// ExtensionsConfig содержит списки расширений файлов
type ExtensionsConfig struct {
	Images []string `yaml:"images"` // Расширения изображений
	Videos []string `yaml:"videos"` // Расширения видео
	Raw    []string `yaml:"raw"`    // Расширения RAW-файлов
}

// ToolsConfig содержит пути к внешним утилитам
type ToolsConfig struct {
	Dcraw  string `yaml:"dcraw"`  // Путь к dcraw (для RAW)
	Ffmpeg string `yaml:"ffmpeg"` // Путь к ffmpeg (для видео)
}

// Load читает конфигурацию из YAML-файла
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}

	// Установка значений по умолчанию
	cfg.setDefaults()

	return &cfg, nil
}

const (
	defaultServerHost                   = "0.0.0.0"
	defaultServerPort                   = 8080
	defaultCachePath                    = "./cache"
	defaultDBPath                       = "./data/photocore.db"
	defaultLogsPath                     = "./logs"
	defaultThumbSmall                   = 300
	defaultThumbMedium                  = 600
	defaultThumbLarge                   = 1200
	defaultThumbQuality                 = 85
	defaultSessionMaxAge                = 86400
	defaultToolDcraw                    = "dcraw"
	defaultToolFfmpeg                   = "ffmpeg"
	defaultDuplicateSimilarityThreshold = 10
)

func (c *Config) setDefaults() {
	if c.Server.Host == "" {
		c.Server.Host = defaultServerHost
	}
	if c.Server.Port == 0 {
		c.Server.Port = defaultServerPort
	}
	if c.Storage.CachePath == "" {
		c.Storage.CachePath = defaultCachePath
	}
	if c.Storage.DBPath == "" {
		c.Storage.DBPath = defaultDBPath
	}
	if c.Storage.LogsPath == "" {
		c.Storage.LogsPath = defaultLogsPath
	}
	if c.Thumbnails.Small == 0 {
		c.Thumbnails.Small = defaultThumbSmall
	}
	if c.Thumbnails.Medium == 0 {
		c.Thumbnails.Medium = defaultThumbMedium
	}
	if c.Thumbnails.Large == 0 {
		c.Thumbnails.Large = defaultThumbLarge
	}
	if c.Thumbnails.Quality == 0 {
		c.Thumbnails.Quality = defaultThumbQuality
	}
	if c.Auth.SessionMaxAge == 0 {
		c.Auth.SessionMaxAge = defaultSessionMaxAge
	}
	if c.Tools.Dcraw == "" {
		c.Tools.Dcraw = defaultToolDcraw
	}
	if c.Tools.Ffmpeg == "" {
		c.Tools.Ffmpeg = defaultToolFfmpeg
	}
	if c.Scan.DuplicateSimilarityThreshold == 0 {
		c.Scan.DuplicateSimilarityThreshold = defaultDuplicateSimilarityThreshold
	}
	c.Scan.EnableDuplicateDetection = true
	c.Scan.DuplicateCheckOriginalExists = true
}

// AllExtensions возвращает все поддерживаемые расширения
func (c *Config) AllExtensions() []string {
	var all []string
	all = append(all, c.Scan.Extensions.Images...)
	all = append(all, c.Scan.Extensions.Videos...)
	all = append(all, c.Scan.Extensions.Raw...)
	return all
}

// IsImage проверяет, является ли расширение изображением
func (c *Config) IsImage(ext string) bool {
	for _, e := range c.Scan.Extensions.Images {
		if e == ext {
			return true
		}
	}
	return false
}

// IsVideo проверяет, является ли расширение видео
func (c *Config) IsVideo(ext string) bool {
	for _, e := range c.Scan.Extensions.Videos {
		if e == ext {
			return true
		}
	}
	return false
}

// IsRaw проверяет, является ли расширение RAW-файлом
func (c *Config) IsRaw(ext string) bool {
	for _, e := range c.Scan.Extensions.Raw {
		if e == ext {
			return true
		}
	}
	return false
}
