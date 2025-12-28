package scanner

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"os"

	"github.com/corona10/goimagehash"
	"github.com/photocore/photocore/internal/logger"
)

// HashResult содержит результаты хеширования файла
type HashResult struct {
	Checksum  string // SHA256 хеш файла
	ImageHash uint64 // Perceptual hash (0 если не изображение или ошибка)
}

// CalculateHashes вычисляет SHA256 и perceptual hash для файла
func CalculateHashes(path string, isImage bool) (*HashResult, error) {
	result := &HashResult{}

	// SHA256 хеш файла
	checksum, err := calculateSHA256(path)
	if err != nil {
		return nil, fmt.Errorf("failed to calculate SHA256: %w", err)
	}
	result.Checksum = checksum

	// Perceptual hash только для изображений
	if isImage {
		imgHash, err := calculateImageHash(path)
		if err != nil {
			logger.InfoLog.Printf("Warning: failed to calculate image hash for %s: %v", path, err)
			// Не возвращаем ошибку - файл может быть повреждён или в неподдерживаемом формате
		} else {
			result.ImageHash = imgHash
		}
	}

	return result, nil
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

// calculateImageHash вычисляет perceptual hash изображения (dHash)
func calculateImageHash(path string) (uint64, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()

	img, _, err := image.Decode(f)
	if err != nil {
		return 0, fmt.Errorf("failed to decode image: %w", err)
	}

	// Используем DifferenceHash - хорошо работает для поиска похожих изображений
	hash, err := goimagehash.DifferenceHash(img)
	if err != nil {
		return 0, fmt.Errorf("failed to calculate hash: %w", err)
	}

	return hash.GetHash(), nil
}

// CompareImageHashes сравнивает два perceptual hash и возвращает Hamming distance
// distance < 10 обычно означает похожие изображения
func CompareImageHashes(hash1, hash2 uint64) int {
	// Hamming distance - количество различающихся битов
	xor := hash1 ^ hash2
	distance := 0
	for xor != 0 {
		distance++
		xor &= xor - 1 // Сбрасываем младший установленный бит
	}
	return distance
}
