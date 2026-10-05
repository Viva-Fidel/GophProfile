package httpserver

import (
	"encoding/json"
	"net/http"

	"golang.org/x/time/rate"
)

// RateLimit ограничивает число запросов (token bucket). rps<=0 отключает лимит.
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
	limiter := rate.NewLimiter(rate.Limit(rps), burst)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !limiter.Allow() {
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("Retry-After", "1")
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
