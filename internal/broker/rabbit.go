// Package broker реализует публикацию и потребление событий через RabbitMQ.
package broker

import (
	"context"
	"fmt"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"gophprofile/internal/domain"
	"gophprofile/internal/observability"
)

const brokerTracerName = "gophprofile/broker"

// Message — входящее сообщение брокера с ручным ack/nack.
type Message struct {
	ID   string
	Body []byte
	Ack  func() error
	Nack func(requeue bool) error
}

// Publisher публикует события в exchange.
type Publisher interface {
	Publish(ctx context.Context, routingKey, messageID string, body []byte) error
	Ping(ctx context.Context) error
	Close() error
}

// Rabbit — клиент RabbitMQ с заранее объявленной топологией.
type Rabbit struct {
	conn    *amqp.Connection
	channel *amqp.Channel
}

// NewRabbit подключается к брокеру и объявляет exchange, очереди и привязки.
func NewRabbit(uri string) (*Rabbit, error) {
	conn, err := amqp.Dial(uri)
	if err != nil {
		return nil, err
	}
	ch, err := conn.Channel()
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	r := &Rabbit{conn: conn, channel: ch}
	if err := r.declare(); err != nil {
		_ = r.Close()
		return nil, err
	}
	return r, nil
}

// declare создаёт durable topic-exchange и очереди аватарок.
func (r *Rabbit) declare() error {
	if err := r.channel.ExchangeDeclare(
		domain.ExchangeName,
		domain.ExchangeType,
		true,
		false,
		false,
		false,
		nil,
	); err != nil {
		return err
	}

	bindings := []struct {
		queue string
		key   string
	}{
		{domain.QueueUploaded, domain.RoutingUploaded},
		{domain.QueueDeleted, domain.RoutingDeleted},
		{domain.QueueProcess, domain.RoutingProcess},
	}
	for _, b := range bindings {
		if _, err := r.channel.QueueDeclare(b.queue, true, false, false, false, nil); err != nil {
			return err
		}
		if err := r.channel.QueueBind(b.queue, b.key, domain.ExchangeName, false, nil); err != nil {
			return err
		}
	}
	return nil
}

// Publish отправляет persistent-сообщение с уникальным MessageId.
func (r *Rabbit) Publish(ctx context.Context, routingKey, messageID string, body []byte) error {
	ctx, span := otel.Tracer(brokerTracerName).Start(ctx, "rabbitmq.publish",
		trace.WithSpanKind(trace.SpanKindProducer),
		trace.WithAttributes(
			attribute.String("messaging.system", "rabbitmq"),
			attribute.String("messaging.destination", domain.ExchangeName),
			attribute.String("messaging.rabbitmq.routing_key", routingKey),
			attribute.String("messaging.message_id", messageID),
		),
	)
	defer span.End()

	headers := amqp.Table{}
	otel.GetTextMapPropagator().Inject(ctx, observability.AMQPHeaderCarrier(headers))

	err := r.channel.PublishWithContext(ctx, domain.ExchangeName, routingKey, false, false, amqp.Publishing{
		ContentType:  "application/json",
		DeliveryMode: amqp.Persistent,
		MessageId:    messageID,
		Timestamp:    time.Now().UTC(),
		Headers:      headers,
		Body:         body,
	})
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	}
	return err
}

// Consume читает очередь в отдельном канале и передаёт сообщения handler.
func (r *Rabbit) Consume(ctx context.Context, queue string, handler func(context.Context, Message) error) error {
	ch, err := r.conn.Channel()
	if err != nil {
		return err
	}
	defer ch.Close()
	if err := ch.Qos(1, 0, false); err != nil {
		return err
	}
	msgs, err := ch.Consume(queue, "", false, false, false, false, nil)
	if err != nil {
		return err
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case d, ok := <-msgs:
			if !ok {
				return fmt.Errorf("rabbitmq channel closed")
			}
			msgCtx := otel.GetTextMapPropagator().Extract(ctx, observability.AMQPHeaderCarrier(d.Headers))
			msgCtx, span := otel.Tracer(brokerTracerName).Start(msgCtx, "rabbitmq.consume",
				trace.WithSpanKind(trace.SpanKindConsumer),
				trace.WithAttributes(
					attribute.String("messaging.system", "rabbitmq"),
					attribute.String("messaging.source", queue),
					attribute.String("messaging.message_id", d.MessageId),
				),
			)
			msg := Message{
				ID:   d.MessageId,
				Body: d.Body,
				Ack:  func() error { return d.Ack(false) },
				Nack: func(requeue bool) error { return d.Nack(false, requeue) },
			}
			if err := handler(msgCtx, msg); err != nil {
				span.RecordError(err)
				span.SetStatus(codes.Error, err.Error())
				span.End()
				_ = msg.Nack(true)
				continue
			}
			span.End()
			_ = msg.Ack()
		}
	}
}

// QueueMessageCount возвращает число сообщений в очереди.
func (r *Rabbit) QueueMessageCount(_ context.Context, queue string) (int, error) {
	ch, err := r.conn.Channel()
	if err != nil {
		return 0, err
	}
	defer ch.Close()
	q, err := ch.QueueInspect(queue)
	if err != nil {
		return 0, err
	}
	return q.Messages, nil
}

// Ping проверяет, что соединение с брокером живо.
func (r *Rabbit) Ping(_ context.Context) error {
	if r.conn == nil || r.conn.IsClosed() {
		return fmt.Errorf("rabbitmq connection closed")
	}
	if r.channel == nil {
		return fmt.Errorf("rabbitmq channel is nil")
	}
	return nil
}

// Close закрывает канал и соединение с брокером.
func (r *Rabbit) Close() error {
	var chErr, connErr error
	if r.channel != nil {
		chErr = r.channel.Close()
	}
	if r.conn != nil {
		connErr = r.conn.Close()
	}
	if chErr != nil {
		return chErr
	}
	return connErr
}
