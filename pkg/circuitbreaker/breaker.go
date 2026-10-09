// Package circuitbreaker реализует circuit breaker для внешних зависимостей.
//
// Состояния: Closed - Open (после FailureThreshold ошибок) - HalfOpen (после Timeout)
// - Closed (после SuccessThreshold успешных проб) или снова Open при ошибке.
package circuitbreaker

import (
	"errors"
	"sync"
	"time"
)

// ErrOpen - вызов отклонён, потому что breaker в состоянии Open.
var ErrOpen = errors.New("circuit breaker is open")

var errRecordedFailure = errors.New("circuit breaker recorded failure")

type State int

const (
	StateClosed State = iota
	StateOpen
	StateHalfOpen
)

func (s State) String() string {
	switch s {
	case StateClosed:
		return "closed"
	case StateOpen:
		return "open"
	case StateHalfOpen:
		return "half-open"
	default:
		return "unknown"
	}
}

// Settings — пороги срабатывания. Нулевые поля заменяются значениями по умолчанию.
type Settings struct {
	Name             string        // метка зависимости, например "s3"
	FailureThreshold int           // ошибок до Open (по умолчанию 5)
	SuccessThreshold int           // успехов в HalfOpen до Closed (по умолчанию 2)
	Timeout          time.Duration // сколько держать Open перед HalfOpen (по умолчанию 30s)
}

// Breaker сериализует учёт ошибок и блокирует вызовы в состоянии Open.
type Breaker struct {
	mu sync.Mutex

	name             string
	failureThreshold int
	successThreshold int
	timeout          time.Duration

	state     State
	failures  int
	successes int
	openedAt  time.Time
}

func New(settings Settings) *Breaker {
	if settings.FailureThreshold < 1 {
		settings.FailureThreshold = 5
	}
	if settings.SuccessThreshold < 1 {
		settings.SuccessThreshold = 2
	}
	if settings.Timeout <= 0 {
		settings.Timeout = 30 * time.Second
	}
	name := settings.Name
	if name == "" {
		name = "dependency"
	}
	return &Breaker{
		name:             name,
		failureThreshold: settings.FailureThreshold,
		successThreshold: settings.SuccessThreshold,
		timeout:          settings.Timeout,
		state:            StateClosed,
	}
}

func (b *Breaker) Name() string {
	return b.name
}

func (b *Breaker) State() State {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.transitionLocked(time.Now())
	return b.state
}

// Allow / Success / Failure — для долгих операций (например Consume),
// которые нельзя целиком обернуть в Execute.
func (b *Breaker) Allow() error {
	if b == nil {
		return nil
	}
	return b.beforeRequest()
}

func (b *Breaker) Success() {
	if b == nil {
		return
	}
	b.afterRequest(nil)
}

func (b *Breaker) Failure() {
	if b == nil {
		return
	}
	b.afterRequest(errRecordedFailure)
}

// Execute вызывает fn, если breaker разрешает; иначе возвращает ErrOpen.
func (b *Breaker) Execute(fn func() error) error {
	if err := b.beforeRequest(); err != nil {
		return err
	}
	err := fn()
	b.afterRequest(err)
	return err
}

// Do — как Execute, но с возвращаемым значением. nil-breaker пропускает вызов без учёта.
func Do[T any](b *Breaker, fn func() (T, error)) (T, error) {
	var zero T
	if b == nil {
		return fn()
	}
	if err := b.beforeRequest(); err != nil {
		return zero, err
	}
	v, err := fn()
	b.afterRequest(err)
	return v, err
}

func (b *Breaker) beforeRequest() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.transitionLocked(time.Now())
	if b.state == StateOpen {
		return ErrOpen
	}
	return nil
}

func (b *Breaker) afterRequest(err error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	now := time.Now()
	b.transitionLocked(now)
	if err != nil {
		b.onFailureLocked(now)
		return
	}
	b.onSuccessLocked()
}

func (b *Breaker) transitionLocked(now time.Time) {
	if b.state == StateOpen && now.Sub(b.openedAt) >= b.timeout {
		b.state = StateHalfOpen
		b.successes = 0
		b.failures = 0
	}
}

func (b *Breaker) onFailureLocked(now time.Time) {
	b.successes = 0
	switch b.state {
	case StateHalfOpen:
		b.state = StateOpen
		b.openedAt = now
		b.failures = 0
	case StateClosed:
		b.failures++
		if b.failures >= b.failureThreshold {
			b.state = StateOpen
			b.openedAt = now
			b.failures = 0
		}
	}
}

func (b *Breaker) onSuccessLocked() {
	b.failures = 0
	switch b.state {
	case StateHalfOpen:
		b.successes++
		if b.successes >= b.successThreshold {
			b.state = StateClosed
			b.successes = 0
		}
	}
}
