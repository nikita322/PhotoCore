package scanner

import (
	"fmt"
	"os"

	"github.com/rwcarlsen/goexif/exif"
	"github.com/rwcarlsen/goexif/mknote"
	"github.com/rwcarlsen/goexif/tiff"

	"github.com/photocore/photocore/internal/storage"
)

func init() {
	// Регистрируем парсеры maker notes для разных производителей
	exif.RegisterParsers(mknote.All...)
}

// extractMetadata извлекает EXIF метаданные из изображения
func extractMetadata(path string, media *storage.Media) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("failed to open file: %w", err)
	}
	defer f.Close()

	x, err := exif.Decode(f)
	if err != nil {
		// Не все файлы имеют EXIF, это нормально
		return nil
	}

	// Дата съемки
	if tm, err := x.DateTime(); err == nil {
		media.TakenAt = tm
	}

	// Размеры изображения
	if tag, err := x.Get(exif.PixelXDimension); err == nil {
		if val, err := tag.Int(0); err == nil {
			media.Width = val
		}
	}
	if tag, err := x.Get(exif.PixelYDimension); err == nil {
		if val, err := tag.Int(0); err == nil {
			media.Height = val
		}
	}

	// Также пробуем ImageWidth/ImageLength если PixelXDimension не доступен
	if media.Width == 0 {
		if tag, err := x.Get(exif.ImageWidth); err == nil {
			if val, err := tag.Int(0); err == nil {
				media.Width = val
			}
		}
	}
	if media.Height == 0 {
		if tag, err := x.Get(exif.ImageLength); err == nil {
			if val, err := tag.Int(0); err == nil {
				media.Height = val
			}
		}
	}

	// Модель камеры
	if tag, err := x.Get(exif.Model); err == nil {
		media.Metadata.Camera = tagToString(tag)
	}
	if tag, err := x.Get(exif.Make); err == nil {
		make := tagToString(tag)
		if media.Metadata.Camera != "" && make != "" {
			media.Metadata.Camera = make + " " + media.Metadata.Camera
		} else if make != "" {
			media.Metadata.Camera = make
		}
	}

	// Объектив
	if tag, err := x.Get(exif.LensModel); err == nil {
		media.Metadata.Lens = tagToString(tag)
	}

	// Фокусное расстояние
	if tag, err := x.Get(exif.FocalLength); err == nil {
		if num, denom, err := tag.Rat2(0); err == nil && denom != 0 {
			focal := float64(num) / float64(denom)
			media.Metadata.FocalLength = fmt.Sprintf("%.0fmm", focal)
		}
	}

	// Диафрагма
	if tag, err := x.Get(exif.FNumber); err == nil {
		if num, denom, err := tag.Rat2(0); err == nil && denom != 0 {
			aperture := float64(num) / float64(denom)
			media.Metadata.Aperture = fmt.Sprintf("f/%.1f", aperture)
		}
	}

	// Выдержка
	if tag, err := x.Get(exif.ExposureTime); err == nil {
		if num, denom, err := tag.Rat2(0); err == nil && denom != 0 {
			if num < denom {
				media.Metadata.ShutterSpeed = fmt.Sprintf("1/%d", denom/num)
			} else {
				media.Metadata.ShutterSpeed = fmt.Sprintf("%.1fs", float64(num)/float64(denom))
			}
		}
	}

	// ISO
	if tag, err := x.Get(exif.ISOSpeedRatings); err == nil {
		if val, err := tag.Int(0); err == nil {
			media.Metadata.ISO = val
		}
	}

	// Ориентация
	if tag, err := x.Get(exif.Orientation); err == nil {
		if val, err := tag.Int(0); err == nil {
			media.Metadata.Orientation = val
		}
	}

	// GPS координаты
	if lat, lon, err := x.LatLong(); err == nil {
		media.Metadata.GPSLat = lat
		media.Metadata.GPSLon = lon
	}

	return nil
}

func tagToString(tag *tiff.Tag) string {
	if tag == nil {
		return ""
	}
	str, err := tag.StringVal()
	if err != nil {
		return ""
	}
	return str
}
