package storage

import (
	"context"

	"gophprofile/pkg/circuitbreaker"
)

// BreakingStorage проксирует ObjectStorage через circuit breaker.
// Ping идёт мимо breaker, чтобы healthcheck не зависел от его состояния.
type BreakingStorage struct {
	inner ObjectStorage
	cb    *circuitbreaker.Breaker
}

// NewBreakingStorage создаёт обёртку над ObjectStorage.
func NewBreakingStorage(inner ObjectStorage, cb *circuitbreaker.Breaker) *BreakingStorage {
	return &BreakingStorage{inner: inner, cb: cb}
}

func (s *BreakingStorage) Upload(ctx context.Context, key string, data []byte, contentType string) error {
	return s.cb.Execute(func() error {
		return s.inner.Upload(ctx, key, data, contentType)
	})
}

func (s *BreakingStorage) Download(ctx context.Context, key string) ([]byte, string, error) {
	type result struct {
		data []byte
		ct   string
	}
	res, err := circuitbreaker.Do(s.cb, func() (result, error) {
		data, ct, err := s.inner.Download(ctx, key)
		return result{data: data, ct: ct}, err
	})
	return res.data, res.ct, err
}

func (s *BreakingStorage) Delete(ctx context.Context, keys []string) error {
	return s.cb.Execute(func() error {
		return s.inner.Delete(ctx, keys)
	})
}

func (s *BreakingStorage) Ping(ctx context.Context) error {
	return s.inner.Ping(ctx)
}
