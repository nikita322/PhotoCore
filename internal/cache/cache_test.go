package cache

import (
	"testing"
	"time"
)

func TestCacheSetGet(t *testing.T) {
	c := New(Config[string]{
		DefaultExpiration: 1 * time.Second,
		CleanupInterval:   1 * time.Second,
		MaxItems:          10,
	})
	defer c.Stop()

	c.Set("key1", "value1")
	val, found := c.Get("key1")
	if !found {
		t.Fatal("expected to find key1")
	}
	if val != "value1" {
		t.Fatalf("expected value1, got %s", val)
	}
}

func TestCacheMissing(t *testing.T) {
	c := New(Config[int]{
		DefaultExpiration: 1 * time.Second,
		CleanupInterval:   1 * time.Second,
		MaxItems:          10,
	})
	defer c.Stop()

	_, found := c.Get("missing")
	if found {
		t.Fatal("expected missing key to not be found")
	}
}

func TestCacheExpiration(t *testing.T) {
	c := New(Config[int]{
		DefaultExpiration: 100 * time.Millisecond,
		CleanupInterval:   50 * time.Millisecond,
		MaxItems:          10,
	})
	defer c.Stop()

	c.Set("key1", 42)
	time.Sleep(200 * time.Millisecond)
	_, found := c.Get("key1")
	if found {
		t.Fatal("expected key1 to be expired")
	}
}

func TestCacheMaxItems(t *testing.T) {
	c := New(Config[int]{
		DefaultExpiration: 0,
		CleanupInterval:   1 * time.Hour,
		MaxItems:          2,
	})
	defer c.Stop()

	c.Set("a", 1)
	c.Set("b", 2)
	c.Set("c", 3)

	if c.Count() != 2 {
		t.Fatalf("expected 2 items, got %d", c.Count())
	}
}

func TestCacheGetOrSet(t *testing.T) {
	c := New(Config[int]{
		DefaultExpiration: 1 * time.Second,
		CleanupInterval:   1 * time.Hour,
		MaxItems:          10,
	})
	defer c.Stop()

	calls := 0
	val, err := c.GetOrSet("key", func() (int, error) {
		calls++
		return 42, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if val != 42 {
		t.Fatalf("expected 42, got %d", val)
	}
	if calls != 1 {
		t.Fatalf("expected 1 call, got %d", calls)
	}

	// Второй вызов должен вернуть закэшированное значение
	val, err = c.GetOrSet("key", func() (int, error) {
		calls++
		return 99, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if val != 42 {
		t.Fatalf("expected 42 (cached), got %d", val)
	}
	if calls != 1 {
		t.Fatalf("expected still 1 call, got %d", calls)
	}
}
