package scanner

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/h2non/filetype"
)

// FormatInfo содержит информацию о формате файла
type FormatInfo struct {
	DetectedMIME      string // MIME тип определенный по содержимому
	DetectedExtension string // Расширение определенное по содержимому
	ClaimedExtension  string // Расширение из имени файла
	IsValid           bool   // Соответствует ли содержимое расширению
	IsSupported       bool   // Поддерживается ли формат для обработки
	Error             string // Описание проблемы если есть
}

// DetectFileFormat определяет реальный формат файла по magic bytes
func DetectFileFormat(path string) (*FormatInfo, error) {
	info := &FormatInfo{
		ClaimedExtension: strings.ToLower(filepath.Ext(path)),
	}

	// Читаем первые 512 байт для определения типа
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("failed to open file: %w", err)
	}
	defer file.Close()

	// Читаем заголовок файла
	head := make([]byte, 512)
	n, err := file.Read(head)
	if err != nil && n == 0 {
		return nil, fmt.Errorf("failed to read file header: %w", err)
	}
	head = head[:n]

	// Определяем тип по magic bytes
	kind, err := filetype.Match(head)
	if err != nil {
		info.Error = "failed to detect file type"
		info.IsValid = false
		return info, nil
	}

	if kind == filetype.Unknown {
		info.Error = "unknown file format"
		info.IsValid = false
		return info, nil
	}

	info.DetectedMIME = kind.MIME.Value
	info.DetectedExtension = "." + kind.Extension

	// Проверяем соответствие расширения содержимому
	info.IsValid = strings.EqualFold(info.ClaimedExtension, info.DetectedExtension)

	// Проверяем, поддерживается ли формат
	info.IsSupported = isSupportedFormat(info.DetectedMIME, info.DetectedExtension)

	if !info.IsValid {
		info.Error = fmt.Sprintf("extension mismatch: file claims %s but contains %s (%s)",
			info.ClaimedExtension, info.DetectedExtension, info.DetectedMIME)
	}

	if !info.IsSupported {
		if info.Error != "" {
			info.Error += "; "
		}
		info.Error += fmt.Sprintf("unsupported format: %s", info.DetectedMIME)
	}

	return info, nil
}

// isSupportedFormat проверяет, поддерживается ли формат для обработки
func isSupportedFormat(mime, ext string) bool {
	// Поддерживаемые форматы изображений
	supportedImages := map[string]bool{
		"image/jpeg":    true,
		"image/jpg":     true,
		"image/png":     true,
		"image/gif":     true,
		"image/webp":    true,
		"image/heic":    true,
		"image/heif":    true,
		"image/bmp":     true,
		"image/tiff":    true,
		"image/x-icon":  true,
	}

	// Поддерживаемые видео форматы
	supportedVideos := map[string]bool{
		"video/mp4":        true,
		"video/quicktime":  true, // .mov
		"video/x-msvideo":  true, // .avi
		"video/x-matroska": true, // .mkv
		"video/webm":       true,
		"video/mpeg":       true,
	}

	// RAW форматы (требуют dcraw)
	// filetype не всегда правильно определяет RAW, но можно по расширению
	rawExtensions := map[string]bool{
		".cr2":  true,
		".cr3":  true,
		".nef":  true,
		".nrw":  true,
		".arw":  true,
		".dng":  true,
		".orf":  true,
		".raf":  true,
		".rw2":  true,
		".raw":  true,
		".srf":  true,
	}

	// Проверяем по MIME
	if supportedImages[mime] || supportedVideos[mime] {
		return true
	}

	// Проверяем RAW по расширению (так как MIME может быть неточным)
	if rawExtensions[strings.ToLower(ext)] {
		return true
	}

	return false
}

// ValidateMediaFile проверяет файл перед добавлением в БД
func ValidateMediaFile(path string) error {
	formatInfo, err := DetectFileFormat(path)
	if err != nil {
		return err
	}

	if !formatInfo.IsValid {
		return fmt.Errorf("invalid file: %s", formatInfo.Error)
	}

	if !formatInfo.IsSupported {
		return fmt.Errorf("unsupported format: %s", formatInfo.Error)
	}

	return nil
}
