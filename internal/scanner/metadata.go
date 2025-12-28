package scanner

import (
	"fmt"
	"strings"
	"time"

	exif "github.com/dsoprea/go-exif/v3"
	exifcommon "github.com/dsoprea/go-exif/v3/common"

	"github.com/photocore/photocore/internal/logger"
	"github.com/photocore/photocore/internal/storage"
)

// ExtractMetadata извлекает EXIF метаданные из изображения
func ExtractMetadata(path string, media *storage.Media) error {
	// Используем универсальный метод который работает с любыми файлами
	rawExif, err := exif.SearchFileAndExtractExif(path)
	if err != nil {
		logger.InfoLog.Printf("EXIF: no EXIF data in %s: %v", path, err)
		return nil
	}

	if len(rawExif) == 0 {
		logger.InfoLog.Printf("EXIF: empty EXIF data in %s", path)
		return nil
	}

	// Парсим EXIF
	im, err := exifcommon.NewIfdMappingWithStandard()
	if err != nil {
		return fmt.Errorf("failed to create IFD mapping: %w", err)
	}

	ti := exif.NewTagIndex()

	_, index, err := exif.Collect(im, ti, rawExif)
	if err != nil {
		logger.InfoLog.Printf("EXIF: failed to parse EXIF in %s: %v", path, err)
		return nil
	}

	logger.InfoLog.Printf("EXIF: found EXIF in %s", path)

	// Извлекаем данные из IFD0 и EXIF IFD
	extractFromIndex(index, media)

	return nil
}

func extractFromIndex(index exif.IfdIndex, media *storage.Media) {
	// Пробуем получить ExifIfd
	exifIfd, err := index.RootIfd.ChildWithIfdPath(exifcommon.IfdExifStandardIfdIdentity)
	if err == nil {
		extractExifTags(exifIfd, media)
	}

	// Также извлекаем из корневого IFD (IFD0)
	extractIfd0Tags(index.RootIfd, media)

	// GPS данные
	gpsIfd, err := index.RootIfd.ChildWithIfdPath(exifcommon.IfdGpsInfoStandardIfdIdentity)
	if err == nil {
		extractGPSTags(gpsIfd, media)
	}
}

func extractExifTags(ifd *exif.Ifd, media *storage.Media) {
	// DateTimeOriginal - дата съёмки
	if entries, err := ifd.FindTagWithName("DateTimeOriginal"); err == nil && len(entries) > 0 {
		if val, err := entries[0].Value(); err == nil {
			if str, ok := val.(string); ok {
				if t, err := parseExifDateTime(str); err == nil {
					media.TakenAt = t
				}
			}
		}
	}

	// Если DateTimeOriginal нет, пробуем DateTimeDigitized
	if media.TakenAt.IsZero() {
		if entries, err := ifd.FindTagWithName("DateTimeDigitized"); err == nil && len(entries) > 0 {
			if val, err := entries[0].Value(); err == nil {
				if str, ok := val.(string); ok {
					if t, err := parseExifDateTime(str); err == nil {
						media.TakenAt = t
					}
				}
			}
		}
	}

	// PixelXDimension, PixelYDimension
	if entries, err := ifd.FindTagWithName("PixelXDimension"); err == nil && len(entries) > 0 {
		if val, err := entries[0].Value(); err == nil {
			media.Width = toInt(val)
		}
	}
	if entries, err := ifd.FindTagWithName("PixelYDimension"); err == nil && len(entries) > 0 {
		if val, err := entries[0].Value(); err == nil {
			media.Height = toInt(val)
		}
	}

	// FocalLength
	if entries, err := ifd.FindTagWithName("FocalLength"); err == nil && len(entries) > 0 {
		if val, err := entries[0].Value(); err == nil {
			if rat, ok := val.([]exifcommon.Rational); ok && len(rat) > 0 {
				focal := float64(rat[0].Numerator) / float64(rat[0].Denominator)
				media.Metadata.FocalLength = fmt.Sprintf("%.0fmm", focal)
			}
		}
	}

	// FNumber (aperture)
	if entries, err := ifd.FindTagWithName("FNumber"); err == nil && len(entries) > 0 {
		if val, err := entries[0].Value(); err == nil {
			if rat, ok := val.([]exifcommon.Rational); ok && len(rat) > 0 {
				aperture := float64(rat[0].Numerator) / float64(rat[0].Denominator)
				media.Metadata.Aperture = fmt.Sprintf("f/%.1f", aperture)
			}
		}
	}

	// ExposureTime (shutter speed)
	if entries, err := ifd.FindTagWithName("ExposureTime"); err == nil && len(entries) > 0 {
		if val, err := entries[0].Value(); err == nil {
			if rat, ok := val.([]exifcommon.Rational); ok && len(rat) > 0 {
				num := rat[0].Numerator
				denom := rat[0].Denominator
				if num < denom {
					media.Metadata.ShutterSpeed = fmt.Sprintf("1/%d", denom/num)
				} else {
					media.Metadata.ShutterSpeed = fmt.Sprintf("%.1fs", float64(num)/float64(denom))
				}
			}
		}
	}

	// ISOSpeedRatings
	if entries, err := ifd.FindTagWithName("ISOSpeedRatings"); err == nil && len(entries) > 0 {
		if val, err := entries[0].Value(); err == nil {
			media.Metadata.ISO = toInt(val)
		}
	}

	// LensModel
	if entries, err := ifd.FindTagWithName("LensModel"); err == nil && len(entries) > 0 {
		if val, err := entries[0].Value(); err == nil {
			if str, ok := val.(string); ok {
				media.Metadata.Lens = strings.TrimSpace(str)
			}
		}
	}
}

func extractIfd0Tags(ifd *exif.Ifd, media *storage.Media) {
	// Make (производитель)
	var make string
	if entries, err := ifd.FindTagWithName("Make"); err == nil && len(entries) > 0 {
		if val, err := entries[0].Value(); err == nil {
			if str, ok := val.(string); ok {
				make = strings.TrimSpace(str)
			}
		}
	}

	// Model (модель камеры)
	var model string
	if entries, err := ifd.FindTagWithName("Model"); err == nil && len(entries) > 0 {
		if val, err := entries[0].Value(); err == nil {
			if str, ok := val.(string); ok {
				model = strings.TrimSpace(str)
			}
		}
	}

	if make != "" && model != "" {
		// Избегаем дублирования если модель уже содержит производителя
		if strings.HasPrefix(strings.ToLower(model), strings.ToLower(make)) {
			media.Metadata.Camera = model
		} else {
			media.Metadata.Camera = make + " " + model
		}
	} else if model != "" {
		media.Metadata.Camera = model
	} else if make != "" {
		media.Metadata.Camera = make
	}

	// Orientation
	if entries, err := ifd.FindTagWithName("Orientation"); err == nil && len(entries) > 0 {
		if val, err := entries[0].Value(); err == nil {
			media.Metadata.Orientation = toInt(val)
		}
	}

	// DateTime (fallback если нет DateTimeOriginal)
	if media.TakenAt.IsZero() {
		if entries, err := ifd.FindTagWithName("DateTime"); err == nil && len(entries) > 0 {
			if val, err := entries[0].Value(); err == nil {
				if str, ok := val.(string); ok {
					if t, err := parseExifDateTime(str); err == nil {
						media.TakenAt = t
					}
				}
			}
		}
	}

	// ImageWidth, ImageLength (fallback)
	if media.Width == 0 {
		if entries, err := ifd.FindTagWithName("ImageWidth"); err == nil && len(entries) > 0 {
			if val, err := entries[0].Value(); err == nil {
				media.Width = toInt(val)
			}
		}
	}
	if media.Height == 0 {
		if entries, err := ifd.FindTagWithName("ImageLength"); err == nil && len(entries) > 0 {
			if val, err := entries[0].Value(); err == nil {
				media.Height = toInt(val)
			}
		}
	}
}

func extractGPSTags(ifd *exif.Ifd, media *storage.Media) {
	var latRef, lonRef string
	var lat, lon []exifcommon.Rational

	// GPSLatitudeRef
	if entries, err := ifd.FindTagWithName("GPSLatitudeRef"); err == nil && len(entries) > 0 {
		if val, err := entries[0].Value(); err == nil {
			if str, ok := val.(string); ok {
				latRef = str
			}
		}
	}

	// GPSLatitude
	if entries, err := ifd.FindTagWithName("GPSLatitude"); err == nil && len(entries) > 0 {
		if val, err := entries[0].Value(); err == nil {
			if rats, ok := val.([]exifcommon.Rational); ok {
				lat = rats
			}
		}
	}

	// GPSLongitudeRef
	if entries, err := ifd.FindTagWithName("GPSLongitudeRef"); err == nil && len(entries) > 0 {
		if val, err := entries[0].Value(); err == nil {
			if str, ok := val.(string); ok {
				lonRef = str
			}
		}
	}

	// GPSLongitude
	if entries, err := ifd.FindTagWithName("GPSLongitude"); err == nil && len(entries) > 0 {
		if val, err := entries[0].Value(); err == nil {
			if rats, ok := val.([]exifcommon.Rational); ok {
				lon = rats
			}
		}
	}

	// Конвертируем в decimal degrees
	if len(lat) >= 3 && len(lon) >= 3 {
		media.Metadata.GPSLat = dmsToDecimal(lat, latRef)
		media.Metadata.GPSLon = dmsToDecimal(lon, lonRef)
	}
}

func dmsToDecimal(dms []exifcommon.Rational, ref string) float64 {
	if len(dms) < 3 {
		return 0
	}

	degrees := float64(dms[0].Numerator) / float64(dms[0].Denominator)
	minutes := float64(dms[1].Numerator) / float64(dms[1].Denominator)
	seconds := float64(dms[2].Numerator) / float64(dms[2].Denominator)

	decimal := degrees + minutes/60 + seconds/3600

	if ref == "S" || ref == "W" {
		decimal = -decimal
	}

	return decimal
}

func parseExifDateTime(s string) (time.Time, error) {
	// EXIF формат: "2006:01:02 15:04:05"
	s = strings.TrimSpace(s)
	if s == "" || s == "0000:00:00 00:00:00" {
		return time.Time{}, fmt.Errorf("empty or zero datetime")
	}
	return time.Parse("2006:01:02 15:04:05", s)
}

func toInt(val interface{}) int {
	switch v := val.(type) {
	case int:
		return v
	case int64:
		return int(v)
	case uint16:
		return int(v)
	case uint32:
		return int(v)
	case []uint16:
		if len(v) > 0 {
			return int(v[0])
		}
	case []uint32:
		if len(v) > 0 {
			return int(v[0])
		}
	}
	return 0
}
