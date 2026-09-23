package ethol

import (
	"sync"
	"time"
)

type chatRateLimiter struct {
	mu           sync.Mutex
	tokens       float64
	maxTokens    float64
	refillRate   float64
	lastRefill   time.Time
	lastWarnTime time.Time
}

func newChatRateLimiter(burst, refillPerSec float64) *chatRateLimiter {
	return &chatRateLimiter{tokens: burst, maxTokens: burst, refillRate: refillPerSec, lastRefill: time.Now()}
}

func (rl *chatRateLimiter) Allow(now time.Time, warnCooldown time.Duration) (bool, bool) {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	elapsed := now.Sub(rl.lastRefill).Seconds()
	rl.lastRefill = now
	rl.tokens += elapsed * rl.refillRate
	if rl.tokens > rl.maxTokens {
		rl.tokens = rl.maxTokens
	}
	if rl.tokens >= 1 {
		rl.tokens--
		return true, false
	}
	if rl.lastWarnTime.IsZero() || now.Sub(rl.lastWarnTime) >= warnCooldown {
		rl.lastWarnTime = now
		return false, true
	}
	return false, false
}
