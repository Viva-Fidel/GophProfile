package retry

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestDoSuccess(t *testing.T) {
	n := 0
	err := Do(context.Background(), 3, time.Millisecond, func() error {
		n++
		if n < 2 {
			return errors.New("fail")
		}
		return nil
	})
	if err != nil || n != 2 {
		t.Fatal(err, n)
	}
}

func TestDoExhausted(t *testing.T) {
	err := Do(context.Background(), 2, time.Millisecond, func() error {
		return errors.New("fail")
	})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestDoCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := Do(ctx, 3, time.Second, func() error {
		return errors.New("fail")
	})
	if !errors.Is(err, context.Canceled) && err == nil {
		t.Fatal(err)
	}
}

func TestDoAttemptsNormalized(t *testing.T) {
	called := 0
	_ = Do(context.Background(), 0, time.Millisecond, func() error {
		called++
		return nil
	})
	if called != 1 {
		t.Fatal(called)
	}
}
