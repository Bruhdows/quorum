package server

import (
	"encoding/json"
	"net/http"
	"strconv"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"
)

// responseCache holds rendered JSON for a short while. Every visitor sees the
// same status page, so without this the database work scales with the number
// of open tabs rather than with the number of services. The singleflight
// group means a cold key under load runs the query once, not once per waiting
// request.
type responseCache struct {
	ttl   time.Duration
	group singleflight.Group

	mu    sync.RWMutex
	items map[string]cachedItem
}

type cachedItem struct {
	body    []byte
	expires time.Time
}

func newResponseCache(ttl time.Duration) *responseCache {
	return &responseCache{ttl: ttl, items: map[string]cachedItem{}}
}

func (c *responseCache) lookup(key string) ([]byte, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	item, ok := c.items[key]
	if !ok || time.Now().After(item.expires) {
		return nil, false
	}
	return item.body, true
}

// get returns the cached JSON for key, calling build only when the entry is
// missing or stale.
func (c *responseCache) get(key string, build func() (any, error)) ([]byte, error) {
	if body, ok := c.lookup(key); ok {
		return body, nil
	}

	body, err, _ := c.group.Do(key, func() (any, error) {
		// A request that queued behind the flight may find it already filled.
		if body, ok := c.lookup(key); ok {
			return body, nil
		}
		value, err := build()
		if err != nil {
			return nil, err
		}
		encoded, err := json.Marshal(value)
		if err != nil {
			return nil, err
		}
		c.mu.Lock()
		c.items[key] = cachedItem{body: encoded, expires: time.Now().Add(c.ttl)}
		c.mu.Unlock()
		return encoded, nil
	})
	if err != nil {
		return nil, err
	}
	return body.([]byte), nil
}

// writeCached serves an already-encoded body and tells caches downstream how
// long it stays good for, so a CDN or the browser can absorb repeat polls.
func writeCached(w http.ResponseWriter, body []byte, ttl time.Duration) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "public, max-age="+strconv.Itoa(int(ttl.Seconds())))
	w.Write(body)
}
