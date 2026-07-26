package cache

import (
	"container/list"
	"sync"
)

type Cache struct {
	mu             sync.Mutex
	maxBytes       int64
	maxObjectBytes int64
	usedBytes      int64
	hits           int64
	misses         int64
	items          map[string]*list.Element
	lru            *list.List
}

type Stats struct {
	Entries        int   `json:"entries"`
	UsedBytes      int64 `json:"used_bytes"`
	MaxBytes       int64 `json:"max_bytes"`
	MaxObjectBytes int64 `json:"max_object_bytes"`
	Hits           int64 `json:"hits"`
	Misses         int64 `json:"misses"`
}

type entry struct {
	key  string
	data []byte
}

func New(maxBytes, maxObjectBytes int64) *Cache {
	return &Cache{
		maxBytes:       maxBytes,
		maxObjectBytes: maxObjectBytes,
		items:          make(map[string]*list.Element),
		lru:            list.New(),
	}
}

func (c *Cache) Enabled() bool {
	return c != nil && c.maxBytes > 0
}

func (c *Cache) CanStore(size int64) bool {
	if !c.Enabled() || size < 0 || size > c.maxBytes {
		return false
	}
	return c.maxObjectBytes <= 0 || size <= c.maxObjectBytes
}

func (c *Cache) Add(key string, data []byte) bool {
	if !c.CanStore(int64(len(data))) {
		return false
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if existing, ok := c.items[key]; ok {
		c.removeElement(existing)
	}

	copied := append([]byte(nil), data...)
	element := c.lru.PushFront(entry{key: key, data: copied})
	c.items[key] = element
	c.usedBytes += int64(len(copied))

	for c.usedBytes > c.maxBytes {
		c.removeOldest()
	}
	return true
}

func (c *Cache) Get(key string) ([]byte, bool) {
	if !c.Enabled() {
		return nil, false
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	element, ok := c.items[key]
	if !ok {
		c.misses++
		return nil, false
	}
	c.hits++
	c.lru.MoveToFront(element)
	value := element.Value.(entry)
	return append([]byte(nil), value.data...), true
}

func (c *Cache) Stats() Stats {
	if c == nil {
		return Stats{}
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	return Stats{
		Entries:        len(c.items),
		UsedBytes:      c.usedBytes,
		MaxBytes:       c.maxBytes,
		MaxObjectBytes: c.maxObjectBytes,
		Hits:           c.hits,
		Misses:         c.misses,
	}
}

func (c *Cache) removeOldest() {
	element := c.lru.Back()
	if element != nil {
		c.removeElement(element)
	}
}

func (c *Cache) removeElement(element *list.Element) {
	value := element.Value.(entry)
	delete(c.items, value.key)
	c.usedBytes -= int64(len(value.data))
	c.lru.Remove(element)
}
