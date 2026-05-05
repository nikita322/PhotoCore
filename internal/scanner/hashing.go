package scanner

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/corona10/goimagehash"
	"github.com/disintegration/imaging"
	"github.com/photocore/photocore/internal/logger"
)

// HashResult содержит результаты хеширования файла
type HashResult struct {
	Checksum  string // SHA256 хеш файла
	ImageHash uint64 // Perceptual hash (0 если не изображение или ошибка)
}

// CalculateHashes вычисляет SHA256 и perceptual hash для файла.
// Если precomputedChecksum не пустой, SHA256 не пересчитывается.
func CalculateHashes(path string, isImage bool, dcrawPath string, precomputedChecksum string) (*HashResult, error) {
	result := &HashResult{}

	if precomputedChecksum != "" {
		result.Checksum = precomputedChecksum
	} else {
		checksum, err := calculateSHA256(path)
		if err != nil {
			return nil, fmt.Errorf("failed to calculate SHA256: %w", err)
		}
		result.Checksum = checksum
	}

	if isImage {
		imgHash, err := CalculateImageHash(path, dcrawPath)
		if err != nil {
			logger.InfoLog.Printf("Warning: failed to calculate image hash for %s: %v", path, err)
		} else {
			result.ImageHash = imgHash
		}
	}

	return result, nil
}

// CalculateChecksum вычисляет только SHA256 хеш из reader
func CalculateChecksum(r io.Reader) (string, error) {
	h := sha256.New()
	if _, err := io.Copy(h, r); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// calculateSHA256 вычисляет SHA256 хеш файла
func calculateSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}

	return hex.EncodeToString(h.Sum(nil)), nil
}

// CalculateImageHash вычисляет perceptual hash (pHash) изображения.
// Для RAW используется dcraw для извлечения preview.
// Для поворотов/отражений возвращает минимальный hash между оригиналом и 180° поворотом.
func CalculateImageHash(path string, dcrawPath string) (uint64, error) {
	img, err := loadImageForHash(path, dcrawPath)
	if err != nil {
		return 0, err
	}

	// Используем PerceptualHash (pHash) — устойчивее к масштабированию и компрессии, чем dHash
	hash, err := goimagehash.PerceptionHash(img)
	if err != nil {
		return 0, fmt.Errorf("failed to calculate pHash: %w", err)
	}
	baseHash := hash.GetHash()

	// Поворот на 180° для детекции перевернутых копий
	rotated := imaging.Rotate180(img)
	rotHash, err := goimagehash.PerceptionHash(rotated)
	if err != nil {
		return baseHash, nil // если не удалось повернуть, возвращаем базовый
	}

	rotatedHash := rotHash.GetHash()
	// Возвращаем hash с наименьшим значением (детерминированный выбор)
	if rotatedHash < baseHash {
		return rotatedHash, nil
	}
	return baseHash, nil
}

// loadImageForHash загружает image.Image из файла (включая RAW через dcraw)
func loadImageForHash(path string, dcrawPath string) (image.Image, error) {
	ext := strings.ToLower(filepath.Ext(path))

	if isRawExtension(ext) {
		return loadRawImageForHash(path, dcrawPath)
	}

	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	img, _, err := image.Decode(f)
	if err != nil {
		return nil, fmt.Errorf("failed to decode image: %w", err)
	}
	return img, nil
}

// loadRawImageForHash загружает RAW через dcraw для вычисления hash
func loadRawImageForHash(path string, dcrawPath string) (image.Image, error) {
	cmd := exec.Command(dcrawPath, "-e", "-c", path)
	output, err := cmd.Output()
	if err == nil && len(output) > 0 {
		img, _, err := image.Decode(bytes.NewReader(output))
		if err == nil {
			return img, nil
		}
	}

	// Fallback: конвертируем в half-size PPM
	cmd = exec.Command(dcrawPath, "-c", "-w", "-W", "-h", path)
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

func isRawExtension(ext string) bool {
	rawExts := map[string]bool{
		".raw": true, ".cr2": true, ".cr3": true,
		".nef": true, ".nrw": true, ".arw": true,
		".srf": true, ".dng": true, ".orf": true,
		".raf": true, ".rw2": true,
	}
	return rawExts[ext]
}

// CompareImageHashes сравнивает два perceptual hash и возвращает Hamming distance
// Deprecated: используйте storage.HammingDistance
func CompareImageHashes(hash1, hash2 uint64) int {
	xor := hash1 ^ hash2
	distance := 0
	for xor != 0 {
		distance++
		xor &= xor - 1
	}
	return distance
}
