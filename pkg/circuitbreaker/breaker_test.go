package circuitbreaker

import (
	"errors"
	"testing"
	"time"
)

func TestExecuteOpensAfterFailures(t *testing.T) {
	b := New(Settings{Name: "test", FailureThreshold: 3, SuccessThreshold: 1, Timeout: time.Hour})
	boom := errors.New("boom")
	for i := 0; i < 3; i++ {
		if err := b.Execute(func() error { return boom }); !errors.Is(err, boom) {
			t.Fatalf("iter %d: %v", i, err)
		}
	}
	if b.State() != StateOpen {
		t.Fatalf("state=%s", b.State())
	}
	if err := b.Execute(func() error { return nil }); !errors.Is(err, ErrOpen) {
		t.Fatalf("expected ErrOpen, got %v", err)
	}
}

func TestHalfOpenRecovery(t *testing.T) {
	b := New(Settings{Name: "test", FailureThreshold: 1, SuccessThreshold: 2, Timeout: time.Millisecond})
	_ = b.Execute(func() error { return errors.New("fail") })
	if b.State() != StateOpen {
		t.Fatal(b.State())
	}
	time.Sleep(2 * time.Millisecond)
	if err := b.Execute(func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	if b.State() != StateHalfOpen {
		t.Fatalf("state=%s", b.State())
	}
	if err := b.Execute(func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	if b.State() != StateClosed {
		t.Fatalf("state=%s", b.State())
	}
}

func TestDoGeneric(t *testing.T) {
	b := New(Settings{FailureThreshold: 1, Timeout: time.Hour})
	v, err := Do(b, func() (int, error) { return 7, nil })
	if err != nil || v != 7 {
		t.Fatal(v, err)
	}
	_, err = Do(b, func() (int, error) { return 0, errors.New("x") })
	if err == nil {
		t.Fatal("expected error")
	}
	_, err = Do(b, func() (int, error) { return 1, nil })
	if !errors.Is(err, ErrOpen) {
		t.Fatal(err)
	}
}

func TestNilDo(t *testing.T) {
	v, err := Do[int](nil, func() (int, error) { return 3, nil })
	if err != nil || v != 3 {
		t.Fatal(v, err)
	}
}
