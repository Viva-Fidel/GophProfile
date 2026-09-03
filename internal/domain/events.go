package domain

const (
	// ExchangeName — topic-exchange для событий аватарок.
	ExchangeName = "avatars.exchange"
	// ExchangeType — тип RabbitMQ exchange.
	ExchangeType = "topic"

	// RoutingUploaded — ключ маршрутизации после загрузки оригинала.
	RoutingUploaded = "avatar.uploaded"
	// RoutingDeleted — ключ маршрутизации после мягкого удаления.
	RoutingDeleted = "avatar.deleted"
	// RoutingProcess — ключ маршрутизации задач обработки.
	RoutingProcess = "avatar.process"

	// QueueUploaded — очередь событий загрузки.
	QueueUploaded = "avatars.uploaded"
	// QueueDeleted — очередь событий удаления.
	QueueDeleted = "avatars.deleted"
	// QueueProcess — очередь задач обработки.
	QueueProcess = "avatars.process"
)

// AvatarUploadEvent публикуется после сохранения оригинала в S3.
type AvatarUploadEvent struct {
	AvatarID string `json:"avatar_id"`
	UserID   string `json:"user_id"`
	S3Key    string `json:"s3_key"`
}

// ProcessingOp описывает одну операцию обработки изображения.
type ProcessingOp struct {
	Name string `json:"name"`
	Size string `json:"size"`
}

// AvatarProcessEvent описывает набор операций обработки аватарки.
type AvatarProcessEvent struct {
	AvatarID   string         `json:"avatar_id"`
	Operations []ProcessingOp `json:"operations"`
}

// AvatarDeleteEvent публикуется после мягкого удаления для очистки S3.
type AvatarDeleteEvent struct {
	AvatarID string   `json:"avatar_id"`
	S3Keys   []string `json:"s3_keys"`
}
