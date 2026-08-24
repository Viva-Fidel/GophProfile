// Package retry выполняет функцию с экспоненциальным backoff.
package retry

import (
	"context"
	"time"
)

// Do вызывает fn до успеха или исчерпания попыток.
func Do(ctx context.Context, attempts int, initial time.Duration, fn func() error) error {
	if attempts < 1 {
		attempts = 1
	}
	delay := initial
	var err error
	for i := 0; i < attempts; i++ {
		if err = fn(); err == nil {
			return nil
		}
		if i == attempts-1 {
			break
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(delay):
			delay *= 2
		}
	}
	return err
}
