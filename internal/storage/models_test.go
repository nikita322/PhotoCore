package storage

import (
	"testing"
	"time"
)

func TestMediaTypeConstants(t *testing.T) {
	if MediaTypeImage != "image" {
		t.Fatalf("unexpected MediaTypeImage: %s", MediaTypeImage)
	}
	if MediaTypeVideo != "video" {
		t.Fatalf("unexpected MediaTypeVideo: %s", MediaTypeVideo)
	}
	if MediaTypeRaw != "raw" {
		t.Fatalf("unexpected MediaTypeRaw: %s", MediaTypeRaw)
	}
}

func TestRoleConstants(t *testing.T) {
	if RoleAdmin != "admin" {
		t.Fatalf("unexpected RoleAdmin: %s", RoleAdmin)
	}
	if RoleEditor != "editor" {
		t.Fatalf("unexpected RoleEditor: %s", RoleEditor)
	}
	if RoleViewer != "viewer" {
		t.Fatalf("unexpected RoleViewer: %s", RoleViewer)
	}
}

func TestDuplicateTypeConstants(t *testing.T) {
	if DuplicateTypeExact != "exact" {
		t.Fatalf("unexpected DuplicateTypeExact: %s", DuplicateTypeExact)
	}
	if DuplicateTypeSimilar != "similar" {
		t.Fatalf("unexpected DuplicateTypeSimilar: %s", DuplicateTypeSimilar)
	}
}

func TestFormatYearMonth(t *testing.T) {
	dt := time.Date(2024, time.March, 15, 0, 0, 0, 0, time.UTC)
	if FormatYearMonth(dt) != "2024-03" {
		t.Fatalf("unexpected format: %s", FormatYearMonth(dt))
	}
}

func TestGenerateIDDeterministic(t *testing.T) {
	id1 := GenerateID("/path/to/photo.jpg")
	id2 := GenerateID("/path/to/photo.jpg")
	if id1 != id2 {
		t.Fatal("expected GenerateID to be deterministic for same path")
	}
}

func TestCrossPackageConstants(t *testing.T) {
	// Проверяем, что константы определены и имеют ожидаемые значения
	if DefaultDuplicateSimilarityThreshold != 10 {
		t.Fatalf("unexpected threshold: %d", DefaultDuplicateSimilarityThreshold)
	}
	if TrashRetentionDays != 30 {
		t.Fatalf("unexpected retention: %d", TrashRetentionDays)
	}
	if DefaultSearchLimit != 50 {
		t.Fatalf("unexpected search limit: %d", DefaultSearchLimit)
	}
}
