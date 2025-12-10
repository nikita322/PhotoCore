package media

import (
	"bytes"
	"fmt"
	"image"
	_ "image/gif"
	"image/jpeg"
	_ "image/png"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/disintegration/imaging"

	"github.com/photocore/photocore/internal/config"
	"github.com/photocore/photocore/internal/storage"
)

// ThumbnailGenerator генерирует превью для медиа-файлов
type ThumbnailGenerator struct {
	cfg       *config.Config
	cachePath string
}

// NewThumbnailGenerator создает новый генератор превью
func NewThumbnailGenerator(cfg *config.Config) *ThumbnailGenerator {
	return &ThumbnailGenerator{
		cfg:       cfg,
		cachePath: cfg.Storage.CachePath,
	}
}

// EnsureCacheDir создает директорию кэша если не существует
func (t *ThumbnailGenerator) EnsureCacheDir() error {
	thumbDir := filepath.Join(t.cachePath, "thumbs")
	return os.MkdirAll(thumbDir, 0755)
}

// GetThumbnailPath возвращает путь к превью
func (t *ThumbnailGenerator) GetThumbnailPath(mediaID string, size string) string {
	return filepath.Join(t.cachePath, "thumbs", fmt.Sprintf("%s_%s.jpg", mediaID, size))
}

// ThumbnailExists проверяет существование превью
func (t *ThumbnailGenerator) ThumbnailExists(mediaID string, size string) bool {
	path := t.GetThumbnailPath(mediaID, size)
	_, err := os.Stat(path)
	return err == nil
}

// DeleteThumbnails удаляет все превью для медиа-файла
func (t *ThumbnailGenerator) DeleteThumbnails(mediaID string) {
	sizes := []string{"small", "medium", "large"}
	for _, size := range sizes {
		path := t.GetThumbnailPath(mediaID, size)
		os.Remove(path) // игнорируем ошибки - файла может не быть
	}
}

// GenerateThumbnail генерирует превью для медиа-файла
func (t *ThumbnailGenerator) GenerateThumbnail(media *storage.Media, size string) (string, error) {
	if err := t.EnsureCacheDir(); err != nil {
		return "", err
	}

	thumbPath := t.GetThumbnailPath(media.ID, size)

	// Если превью уже существует, возвращаем путь
	if _, err := os.Stat(thumbPath); err == nil {
		return thumbPath, nil
	}

	// Определяем размер
	var maxSize int
	switch size {
	case "small":
		maxSize = t.cfg.Thumbnails.Small
	case "medium":
		maxSize = t.cfg.Thumbnails.Medium
	case "large":
		maxSize = t.cfg.Thumbnails.Large
	default:
		maxSize = t.cfg.Thumbnails.Small
	}

	var img image.Image
	var err error

	switch media.Type {
	case storage.MediaTypeImage:
		img, err = t.loadImage(media.Path)
	case storage.MediaTypeRaw:
		img, err = t.loadRawImage(media.Path)
	case storage.MediaTypeVideo:
		img, err = t.extractVideoFrame(media.Path)
	default:
		return "", fmt.Errorf("unsupported media type: %s", media.Type)
	}

	if err != nil {
		return "", fmt.Errorf("failed to load image: %w", err)
	}

	// Применяем ориентацию из EXIF
	if media.Metadata.Orientation > 1 {
		img = applyOrientation(img, media.Metadata.Orientation)
	}

	// Ресайзим с сохранением пропорций
	thumb := imaging.Fit(img, maxSize, maxSize, imaging.Lanczos)

	// Сохраняем как JPEG
	out, err := os.Create(thumbPath)
	if err != nil {
		return "", fmt.Errorf("failed to create thumbnail file: %w", err)
	}
	defer out.Close()

	if err := jpeg.Encode(out, thumb, &jpeg.Options{Quality: t.cfg.Thumbnails.Quality}); err != nil {
		return "", fmt.Errorf("failed to encode thumbnail: %w", err)
	}

	return thumbPath, nil
}

// loadImage загружает обычное изображение
func (t *ThumbnailGenerator) loadImage(path string) (image.Image, error) {
	return imaging.Open(path)
}

// loadRawImage загружает RAW-изображение через dcraw
func (t *ThumbnailGenerator) loadRawImage(path string) (image.Image, error) {
	// Пробуем извлечь встроенный JPEG превью
	// dcraw -e -c выдает встроенное превью на stdout
	cmd := exec.Command(t.cfg.Tools.Dcraw, "-e", "-c", path)
	output, err := cmd.Output()
	if err == nil && len(output) > 0 {
		img, _, err := image.Decode(bytes.NewReader(output))
		if err == nil {
			return img, nil
		}
	}

	// Если встроенного превью нет, конвертируем RAW в PPM
	// dcraw -c -w -W -h выдает half-size PPM на stdout (быстрее)
	cmd = exec.Command(t.cfg.Tools.Dcraw, "-c", "-w", "-W", "-h", path)
	output, err = cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("dcraw failed: %w", err)
	}

	img, _, err := image.Decode(bytes.NewReader(output))
	if err != nil {
		return nil, fmt.Errorf("failed to decode dcraw output: %w", err)
	}

	return img, nil
}

// extractVideoFrame извлекает кадр из видео через ffmpeg
func (t *ThumbnailGenerator) extractVideoFrame(path string) (image.Image, error) {
	// ffmpeg -i video.mp4 -ss 00:00:01 -vframes 1 -f image2pipe -vcodec mjpeg -
	cmd := exec.Command(t.cfg.Tools.Ffmpeg,
		"-i", path,
		"-ss", "00:00:01",
		"-vframes", "1",
		"-f", "image2pipe",
		"-vcodec", "mjpeg",
		"-",
	)

	output, err := cmd.Output()
	if err != nil {
		// Пробуем с начала файла, если 1 секунда недоступна
		cmd = exec.Command(t.cfg.Tools.Ffmpeg,
			"-i", path,
			"-vframes", "1",
			"-f", "image2pipe",
			"-vcodec", "mjpeg",
			"-",
		)
		output, err = cmd.Output()
		if err != nil {
			return nil, fmt.Errorf("ffmpeg failed: %w", err)
		}
	}

	img, _, err := image.Decode(bytes.NewReader(output))
	if err != nil {
		return nil, fmt.Errorf("failed to decode ffmpeg output: %w", err)
	}

	return img, nil
}

// applyOrientation применяет EXIF ориентацию к изображению
func applyOrientation(img image.Image, orientation int) image.Image {
	switch orientation {
	case 2:
		return imaging.FlipH(img)
	case 3:
		return imaging.Rotate180(img)
	case 4:
		return imaging.FlipV(img)
	case 5:
		return imaging.Transpose(img)
	case 6:
		return imaging.Rotate270(img)
	case 7:
		return imaging.Transverse(img)
	case 8:
		return imaging.Rotate90(img)
	default:
		return img
	}
}

// GetImageDimensions получает размеры изображения
func GetImageDimensions(path string, mediaType storage.MediaType, dcrawPath string) (width, height int, err error) {
	if mediaType == storage.MediaTypeRaw {
		// Для RAW используем dcraw -i -v для получения информации
		cmd := exec.Command(dcrawPath, "-i", "-v", path)
		output, err := cmd.Output()
		if err == nil {
			// Парсим вывод dcraw для получения размеров
			// Формат: "Image size:  5472 x 3648"
			lines := strings.Split(string(output), "\n")
			for _, line := range lines {
				if strings.HasPrefix(line, "Image size:") {
					var w, h int
					fmt.Sscanf(line, "Image size: %d x %d", &w, &h)
					return w, h, nil
				}
			}
		}
	}

	// Для обычных изображений
	f, err := os.Open(path)
	if err != nil {
		return 0, 0, err
	}
	defer f.Close()

	config, _, err := image.DecodeConfig(f)
	if err != nil {
		return 0, 0, err
	}

	return config.Width, config.Height, nil
}

// IsRawExtension проверяет, является ли расширение RAW-форматом
func IsRawExtension(ext string) bool {
	rawExts := map[string]bool{
		".raw": true, ".cr2": true, ".cr3": true,
		".nef": true, ".nrw": true, ".arw": true,
		".srf": true, ".dng": true, ".orf": true,
		".raf": true, ".rw2": true,
	}
	return rawExts[strings.ToLower(ext)]
}
