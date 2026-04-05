package cache

import (
	"fmt"
	"testing"
)

func TestGetMiss(t *testing.T) {
	c := New(1024)
	if _, ok := c.Get("nope"); ok {
		t.Fatal("expected miss on empty cache")
	}
}

func TestSetAndGet(t *testing.T) {
	c := New(1024)
	c.Set("a", []byte("hello"))

	data, ok := c.Get("a")
	if !ok {
		t.Fatal("expected hit")
	}

	if string(data) != "hello" {
		t.Fatalf("got %q, want %q", data, "hello")
	}
}

func TestEvictionBySize(t *testing.T) {
	// Cache holds at most 10 bytes.
	c := New(10)

	c.Set("a", make([]byte, 6)) // 6 bytes
	c.Set("b", make([]byte, 6)) // 6+6=12 > 10 -> evict "a"

	if _, ok := c.Get("a"); ok {
		t.Fatal("expected 'a' to be evicted")
	}

	if _, ok := c.Get("b"); !ok {
		t.Fatal("expected 'b' to be present")
	}

	if c.Size() != 6 {
		t.Fatalf("size = %d, want 6", c.Size())
	}
}

func TestLRUOrder(t *testing.T) {
	c := New(18) // fits 3 entries of 6 bytes each

	c.Set("a", make([]byte, 6))
	c.Set("b", make([]byte, 6))
	c.Set("c", make([]byte, 6))

	// Access "a" to promote it; then insert "d" which should evict "b" (oldest).
	c.Get("a")
	c.Set("d", make([]byte, 6)) // total would be 24 > 18 -> evict LRU = "b"

	if _, ok := c.Get("b"); ok {
		t.Fatal("expected 'b' to be evicted")
	}

	for _, key := range []string{"a", "c", "d"} {
		if _, ok := c.Get(key); !ok {
			t.Fatalf("expected %q to be present", key)
		}
	}
}

func TestUpdateExistingKey(t *testing.T) {
	c := New(1024)

	c.Set("a", []byte("short"))
	c.Set("a", []byte("much longer value"))

	data, ok := c.Get("a")
	if !ok {
		t.Fatal("expected hit after update")
	}

	if string(data) != "much longer value" {
		t.Fatalf("got %q", data)
	}

	if c.Len() != 1 {
		t.Fatalf("len = %d, want 1", c.Len())
	}

	if c.Size() != int64(len("much longer value")) {
		t.Fatalf("size = %d, want %d", c.Size(), len("much longer value"))
	}
}

func TestDelete(t *testing.T) {
	c := New(1024)
	c.Set("a", []byte("x"))

	if !c.Delete("a") {
		t.Fatal("expected Delete to return true")
	}

	if c.Delete("a") {
		t.Fatal("expected Delete to return false for missing key")
	}

	if c.Len() != 0 || c.Size() != 0 {
		t.Fatalf("expected empty cache, got len=%d size=%d", c.Len(), c.Size())
	}
}

func TestDisabledCache(t *testing.T) {
	c := New(0)
	c.Set("a", []byte("data"))

	if _, ok := c.Get("a"); ok {
		t.Fatal("disabled cache should always miss")
	}
}

func TestOversizedItem(t *testing.T) {
	c := New(10)
	c.Set("big", make([]byte, 11)) // larger than maxBytes

	if _, ok := c.Get("big"); ok {
		t.Fatal("oversized item should not be cached")
	}

	if c.Len() != 0 {
		t.Fatalf("len = %d, want 0", c.Len())
	}
}

func TestConcurrentAccess(t *testing.T) {
	c := New(1 << 20) // 1MB
	done := make(chan struct{})

	for i := range 100 {
		go func(id int) {
			defer func() { done <- struct{}{} }()
			key := fmt.Sprintf("key-%d", id)
			c.Set(key, make([]byte, 1024))
			c.Get(key)
			c.Delete(key)
		}(i)
	}

	for range 100 {
		<-done
	}
}
