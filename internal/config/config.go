package config

import (
	"os"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Server     ServerConfig     `yaml:"server"`
	Storage    StorageConfig    `yaml:"storage"`
	Thumbnails ThumbnailsConfig `yaml:"thumbnails"`
	Auth       AuthConfig       `yaml:"auth"`
	Scan       ScanConfig       `yaml:"scan"`
	Tools      ToolsConfig      `yaml:"tools"`
}

type ServerConfig struct {
	Host string `yaml:"host"`
	Port int    `yaml:"port"`
}

type StorageConfig struct {
	MediaPaths []string `yaml:"media_paths"`
	CachePath  string   `yaml:"cache_path"`
	DBPath     string   `yaml:"db_path"`
	LogsPath   string   `yaml:"logs_path"`
}

type ThumbnailsConfig struct {
	Small   int `yaml:"small"`
	Medium  int `yaml:"medium"`
	Large   int `yaml:"large"`
	Quality int `yaml:"quality"` // JPEG quality (0-100)
}

type AuthConfig struct {
	SessionSecret string `yaml:"session_secret"`
	SessionMaxAge int    `yaml:"session_max_age"`
	AdminUsername string `yaml:"admin_username"`
	AdminPassword string `yaml:"admin_password"`
}

type ScanConfig struct {
	Extensions ExtensionsConfig `yaml:"extensions"`
}

type ExtensionsConfig struct {
	Images []string `yaml:"images"`
	Videos []string `yaml:"videos"`
	Raw    []string `yaml:"raw"`
}

type ToolsConfig struct {
	Dcraw  string `yaml:"dcraw"`
	Ffmpeg string `yaml:"ffmpeg"`
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
	defaultServerHost    = "0.0.0.0"
	defaultServerPort    = 8080
	defaultCachePath     = "./cache"
	defaultDBPath        = "./data/photocore.db"
	defaultLogsPath      = "./logs"
	defaultThumbSmall    = 300
	defaultThumbMedium   = 600
	defaultThumbLarge    = 1200
	defaultThumbQuality  = 85
	defaultSessionMaxAge = 86400
	defaultToolDcraw     = "dcraw"
	defaultToolFfmpeg    = "ffmpeg"
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
