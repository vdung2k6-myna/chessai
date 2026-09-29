package cloud

import (
	"sort"
	"sync"
	"time"
)

type cacheEntry struct {
	body      string
	expiresAt time.Time
}

// cache is a small bounded, TTL'd map of API responses keyed by action+query.
//
// Opening theory is stable and the UI asks about the same handful of positions
// repeatedly (every board refresh, every sidebar update, every AI turn), so a
// cache removes nearly all of the per-move network latency and keeps us from
// hammering a free third-party service.
type cache struct {
	mu      sync.Mutex
	entries map[string]cacheEntry
	limit   int
	ttl     time.Duration

	// hits and misses are for tests and diagnostics.
	hits, misses int
}

func newCache(limit int, ttl time.Duration) *cache {
	return &cache{
		entries: make(map[string]cacheEntry),
		limit:   limit,
		ttl:     ttl,
	}
}

func (c *cache) get(key string) (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	e, ok := c.entries[key]
	if !ok {
		c.misses++
		return "", false
	}
	if time.Now().After(e.expiresAt) {
		delete(c.entries, key)
		c.misses++
		return "", false
	}
	c.hits++
	return e.body, true
}

func (c *cache) set(key, body string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// A zero limit disables caching; used by tests that count requests.
	if c.limit <= 0 {
		return
	}
	if len(c.entries) >= c.limit {
		c.evictLocked()
	}
	c.entries[key] = cacheEntry{body: body, expiresAt: time.Now().Add(c.ttl)}
}

// evictLocked drops expired entries first. If that is not enough it removes the
// soonest-to-expire quarter, which keeps the map bounded without maintaining an
// LRU list.
func (c *cache) evictLocked() {
	now := time.Now()
	for k, e := range c.entries {
		if now.After(e.expiresAt) {
			delete(c.entries, k)
		}
	}
	if len(c.entries) < c.limit {
		return
	}

	type keyed struct {
		key string
		exp time.Time
	}
	all := make([]keyed, 0, len(c.entries))
	for k, e := range c.entries {
		all = append(all, keyed{k, e.expiresAt})
	}
	sort.Slice(all, func(i, j int) bool { return all[i].exp.Before(all[j].exp) })
	for i := 0; i < len(all)/4+1 && i < len(all); i++ {
		delete(c.entries, all[i].key)
	}
}

func (c *cache) len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.entries)
}

func (c *cache) stats() (hits, misses int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.hits, c.misses
}
