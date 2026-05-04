package storage

import "os"

// Кросс-пакетные константы для исключения дублирования между internal/* пакетами.

const (
	// DefaultDuplicateSimilarityThreshold — порог расстояния Хэмминга для визуальных дубликатов.
	DefaultDuplicateSimilarityThreshold = 10

	// TrashRetentionDays — срок хранения файлов в корзине (в днях).
	TrashRetentionDays = 30

	// DefaultDirPerm — права доступа для создаваемых директорий.
	DefaultDirPerm = os.FileMode(0755)

	// DefaultSearchLimit — лимит результатов поиска по умолчанию.
	DefaultSearchLimit = 50

	// SizeToleranceLower — нижняя граница допуска размера файла (±10%) для поиска дубликатов.
	SizeToleranceLower = 0.9

	// SizeToleranceUpper — верхняя граница допуска размера файла (±10%) для поиска дубликатов.
	SizeToleranceUpper = 1.1
)
