package repository

import (
	"context"
	"errors"

	"gophprofile/internal/domain"
	"gophprofile/pkg/circuitbreaker"
)

// BreakingRepository проксирует *PostgresAvatarRepository через circuit breaker.
// domain.ErrNotFound не считается сбоем зависимости.
type BreakingRepository struct {
	inner *PostgresAvatarRepository
	cb    *circuitbreaker.Breaker
}

// NewBreakingRepository создаёт обёртку над *PostgresAvatarRepository.
func NewBreakingRepository(inner *PostgresAvatarRepository, cb *circuitbreaker.Breaker) *BreakingRepository {
	return &BreakingRepository{inner: inner, cb: cb}
}

// execute — разрешить вызов, выполнить run, зафиксировать Success/Failure.
func (r *BreakingRepository) execute(run func() error) error {
	if err := r.cb.Allow(); err != nil {
		return err
	}
	return r.finish(run())
}

func (r *BreakingRepository) finish(err error) error {
	failed := err != nil && !errors.Is(err, domain.ErrNotFound)
	if failed {
		r.cb.Failure()
	} else {
		r.cb.Success()
	}
	return err
}

func (r *BreakingRepository) Create(ctx context.Context, a domain.Avatar) error {
	return r.execute(func() error {
		return r.inner.Create(ctx, a)
	})
}

func (r *BreakingRepository) GetByID(ctx context.Context, id string) (*domain.Avatar, error) {
	var a *domain.Avatar
	err := r.execute(func() error {
		var err error
		a, err = r.inner.GetByID(ctx, id)
		return err
	})
	return a, err
}

func (r *BreakingRepository) GetLatestByUserID(ctx context.Context, userID string) (*domain.Avatar, error) {
	var a *domain.Avatar
	err := r.execute(func() error {
		var err error
		a, err = r.inner.GetLatestByUserID(ctx, userID)
		return err
	})
	return a, err
}

func (r *BreakingRepository) ListByUserID(ctx context.Context, userID string) ([]domain.Avatar, error) {
	var items []domain.Avatar
	err := r.execute(func() error {
		var err error
		items, err = r.inner.ListByUserID(ctx, userID)
		return err
	})
	return items, err
}

func (r *BreakingRepository) SoftDelete(ctx context.Context, id string) error {
	return r.execute(func() error {
		return r.inner.SoftDelete(ctx, id)
	})
}

func (r *BreakingRepository) ClaimForProcessing(ctx context.Context, id string) (*domain.Avatar, error) {
	var a *domain.Avatar
	err := r.execute(func() error {
		var err error
		a, err = r.inner.ClaimForProcessing(ctx, id)
		return err
	})
	return a, err
}

func (r *BreakingRepository) UpdateProcessingResult(ctx context.Context, id string, thumbs map[string]string, status string) error {
	return r.execute(func() error {
		return r.inner.UpdateProcessingResult(ctx, id, thumbs, status)
	})
}

func (r *BreakingRepository) MarkProcessingFailed(ctx context.Context, id string) error {
	return r.execute(func() error {
		return r.inner.MarkProcessingFailed(ctx, id)
	})
}
