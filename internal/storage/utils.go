package storage

import (
	"crypto/sha256"
	"encoding/hex"
)

// GenerateID генерирует уникальный ID на основе пути файла
func GenerateID(path string) string {
	hash := sha256.Sum256([]byte(path))
	return hex.EncodeToString(hash[:])
}
