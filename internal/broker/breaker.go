package broker

import (
	"context"
	"errors"

	"gophprofile/pkg/circuitbreaker"
)

// BreakingRabbit — клиент RabbitMQ с circuit breaker на операциях с брокером.
// Ping и Close идут мимо breaker, чтобы healthcheck и shutdown всегда работали.
type BreakingRabbit struct {
	inner *Rabbit
	cb    *circuitbreaker.Breaker
}

// NewBreakingRabbit создаёт обёртку над *Rabbit.
func NewBreakingRabbit(inner *Rabbit, cb *circuitbreaker.Breaker) *BreakingRabbit {
	return &BreakingRabbit{inner: inner, cb: cb}
}

// Publish публикует сообщение; при открытом breaker возвращает circuitbreaker.ErrOpen.
func (r *BreakingRabbit) Publish(ctx context.Context, routingKey, messageID string, body []byte) error {
	return r.cb.Execute(func() error {
		return r.inner.Publish(ctx, routingKey, messageID, body)
	})
}

// Consume подписывается на очередь. Долгий цикл нельзя завернуть в Execute целиком:
// перед стартом — Allow, по выходу — Success или Failure (context.Canceled не считается сбоем).
func (r *BreakingRabbit) Consume(ctx context.Context, queue string, handler func(context.Context, Message) error) error {
	if err := r.cb.Allow(); err != nil {
		return err
	}
	err := r.inner.Consume(ctx, queue, handler)
	failed := err != nil && !errors.Is(err, context.Canceled)
	if failed {
		r.cb.Failure()
	} else {
		r.cb.Success()
	}
	return err
}

// QueueMessageCount возвращает число сообщений в очереди.
func (r *BreakingRabbit) QueueMessageCount(ctx context.Context, queue string) (int, error) {
	return circuitbreaker.Do(r.cb, func() (int, error) {
		return r.inner.QueueMessageCount(ctx, queue)
	})
}

// Ping проверяет соединение без участия breaker.
func (r *BreakingRabbit) Ping(ctx context.Context) error {
	return r.inner.Ping(ctx)
}

// Close закрывает канал и соединение.
func (r *BreakingRabbit) Close() error {
	return r.inner.Close()
}
