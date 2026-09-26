package ethol

import (
	"sync"
	"time"
)

type rateRecord struct {
	windowStart time.Time
	count       int
}

type IPRateLimiter struct {
	mu         sync.Mutex
	maxPerWind int
	window     time.Duration
	records    map[string]rateRecord
}

func NewIPRateLimiter(maxPerWindow int, window time.Duration) *IPRateLimiter {
	if maxPerWindow <= 0 {
		maxPerWindow = 10
	}
	if window <= 0 {
		window = time.Minute
	}
	return &IPRateLimiter{
		maxPerWind: maxPerWindow,
		window:     window,
		records:    make(map[string]rateRecord),
	}
}

func (r *IPRateLimiter) Allow(key string, now time.Time) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	rec, ok := r.records[key]
	if !ok || now.Sub(rec.windowStart) >= r.window {
		r.records[key] = rateRecord{windowStart: now, count: 1}
		return true
	}
	if rec.count >= r.maxPerWind {
		return false
	}
	rec.count++
	r.records[key] = rec
	return true
}
