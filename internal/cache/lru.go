package cache

import (
	"container/list"
	"sync"
)

// entry is a single cached item stored in the LRU list.
type entry struct {
	key  string
	data []byte
}

// LRU is a concurrency-safe, byte-size-bounded Least Recently Used cache.
// It evicts the oldest entries when the total size exceeds maxBytes.
type LRU struct {
	mu       sync.Mutex
	maxBytes int64
	curBytes int64
	items    map[string]*list.Element
	order    *list.List // front = most recent
}

// New creates a new LRU cache with the given maximum byte size.
// If maxBytes <= 0 the cache is effectively disabled (every Get misses).
func New(maxBytes int64) *LRU {
	return &LRU{
		maxBytes: maxBytes,
		items:    make(map[string]*list.Element),
		order:    list.New(),
	}
}

// Get retrieves a cached value by key.
// Returns the data and true on hit, or nil and false on miss.
// A hit promotes the entry to the front (most recently used).
func (c *LRU) Get(key string) ([]byte, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	el, ok := c.items[key]
	if !ok {
		return nil, false
	}

	c.order.MoveToFront(el)

	return el.Value.(*entry).data, true
}

// Set inserts or updates a cache entry.
// If the entry already exists its data is replaced and it is promoted.
// After insertion the cache evicts least-recently-used entries until
// the total size is within maxBytes.
func (c *LRU) Set(key string, data []byte) {
	if c.maxBytes <= 0 {
		return
	}

	size := int64(len(data))

	// Single item larger than the whole cache -- do not store it.
	if size > c.maxBytes {
		return
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	// Update existing entry.
	if el, ok := c.items[key]; ok {
		old := el.Value.(*entry)
		c.curBytes += size - int64(len(old.data))
		old.data = data
		c.order.MoveToFront(el)
		c.evict()

		return
	}

	// New entry.
	el := c.order.PushFront(&entry{key: key, data: data})
	c.items[key] = el
	c.curBytes += size
	c.evict()
}

// Delete removes a single entry by key. Returns true if the key existed.
func (c *LRU) Delete(key string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	el, ok := c.items[key]
	if !ok {
		return false
	}

	c.removeElement(el)

	return true
}

// Len returns the number of entries currently in the cache.
func (c *LRU) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.order.Len()
}

// Size returns the total byte size of all cached entries.
func (c *LRU) Size() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.curBytes
}

// evict removes the least-recently-used entries until curBytes <= maxBytes.
// Must be called with c.mu held.
func (c *LRU) evict() {
	for c.curBytes > c.maxBytes {
		tail := c.order.Back()
		if tail == nil {
			break
		}

		c.removeElement(tail)
	}
}

// removeElement removes an element from both the list and the map.
// Must be called with c.mu held.
func (c *LRU) removeElement(el *list.Element) {
	c.order.Remove(el)

	e := el.Value.(*entry)
	delete(c.items, e.key)
	c.curBytes -= int64(len(e.data))
}
