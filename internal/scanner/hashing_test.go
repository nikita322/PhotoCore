package scanner

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCalculateHashesNonImage(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")
	if err := os.WriteFile(path, []byte("hello world"), 0644); err != nil {
		t.Fatal(err)
	}

	result, err := CalculateHashes(path, false, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if result.Checksum == "" {
		t.Fatal("expected non-empty checksum")
	}
	if result.ImageHash != 0 {
		t.Fatalf("expected zero image hash for non-image, got %d", result.ImageHash)
	}
}

func TestCalculateHashesDeterministic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")
	data := []byte("deterministic content")
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatal(err)
	}

	result1, err := CalculateHashes(path, false, "", "")
	if err != nil {
		t.Fatal(err)
	}

	result2, err := CalculateHashes(path, false, "", "")
	if err != nil {
		t.Fatal(err)
	}

	if result1.Checksum != result2.Checksum {
		t.Fatal("expected deterministic checksum for same file")
	}
}
