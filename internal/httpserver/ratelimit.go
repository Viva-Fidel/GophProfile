package httpserver

import (
	"encoding/json"
	"math"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

const clientLimiterTTL = 10 * time.Minute

type clientLimiters struct {
	mu       sync.Mutex
	limiters map[string]*clientEntry
	rps      rate.Limit
	burst    int
}

type clientEntry struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

func newClientLimiters(rps float64, burst int) *clientLimiters {
	c := &clientLimiters{
		limiters: make(map[string]*clientEntry),
		rps:      rate.Limit(rps),
		burst:    burst,
	}
	go c.cleanupLoop()
	return c
}

func (c *clientLimiters) get(key string) *rate.Limiter {
	now := time.Now()
	c.mu.Lock()
	defer c.mu.Unlock()
	if e, ok := c.limiters[key]; ok {
		e.lastSeen = now
		return e.limiter
	}
	lim := rate.NewLimiter(c.rps, c.burst)
	c.limiters[key] = &clientEntry{limiter: lim, lastSeen: now}
	return lim
}

func (c *clientLimiters) cleanupLoop() {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		cutoff := time.Now().Add(-clientLimiterTTL)
		c.mu.Lock()
		for key, e := range c.limiters {
			if e.lastSeen.Before(cutoff) {
				delete(c.limiters, key)
			}
		}
		c.mu.Unlock()
	}
}

func clientID(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if i := strings.IndexByte(xff, ','); i >= 0 {
			xff = xff[:i]
		}
		if ip := strings.TrimSpace(xff); ip != "" {
			return ip
		}
	}
	if xri := strings.TrimSpace(r.Header.Get("X-Real-IP")); xri != "" {
		return xri
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// RateLimit ограничивает число запросов per-client (token bucket). rps<=0 отключает лимит.
func RateLimit(rps float64, burst int) func(http.Handler) http.Handler {
	if rps <= 0 {
		return func(next http.Handler) http.Handler { return next }
	}
	if burst < 1 {
		burst = int(rps)
		if burst < 1 {
			burst = 1
		}
	}
	clients := newClientLimiters(rps, burst)
	retryAfter := int(math.Ceil(1 / rps))
	if retryAfter < 1 {
		retryAfter = 1
	}
	retryAfterHeader := strconv.Itoa(retryAfter)

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !clients.get(clientID(r)).Allow() {
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("Retry-After", retryAfterHeader)
				w.WriteHeader(http.StatusTooManyRequests)
				_ = json.NewEncoder(w).Encode(map[string]string{
					"error":   "Too many requests",
					"details": "Rate limit exceeded",
				})
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
