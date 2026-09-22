package observability

import amqp "github.com/rabbitmq/amqp091-go"

// AMQPHeaderCarrier адаптирует amqp.Table к TextMapCarrier для context propagation.
type AMQPHeaderCarrier amqp.Table

// Get возвращает значение заголовка.
func (c AMQPHeaderCarrier) Get(key string) string {
	if c == nil {
		return ""
	}
	v, ok := c[key]
	if !ok || v == nil {
		return ""
	}
	switch t := v.(type) {
	case string:
		return t
	case []byte:
		return string(t)
	default:
		return ""
	}
}

// Set записывает значение заголовка.
func (c AMQPHeaderCarrier) Set(key, value string) {
	c[key] = value
}

// Keys возвращает ключи заголовков.
func (c AMQPHeaderCarrier) Keys() []string {
	keys := make([]string, 0, len(c))
	for k := range c {
		keys = append(keys, k)
	}
	return keys
}
