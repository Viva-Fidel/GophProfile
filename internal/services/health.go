package services

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Pinger проверяет доступность зависимости.
type Pinger interface {
	Ping(ctx context.Context) error
}

// HealthService проверяет состояние БД, S3 и брокера.
type HealthService struct {
	db     *pgxpool.Pool
	s3     Pinger
	broker Pinger
}

// NewHealthService создаёт сервис healthcheck.
func NewHealthService(db *pgxpool.Pool, s3, broker Pinger) *HealthService {
	return &HealthService{db: db, s3: s3, broker: broker}
}

// ComponentStatus — статусы отдельных зависимостей.
type ComponentStatus struct {
	Postgres string `json:"postgres"`
	S3       string `json:"s3"`
	RabbitMQ string `json:"rabbitmq"`
}

// HealthReport — JSON-ответ GET /health.
type HealthReport struct {
	Status     string          `json:"status"`
	Components ComponentStatus `json:"components"`
}

// Check возвращает сводный статус компонентов.
func (s *HealthService) Check(ctx context.Context) HealthReport {
	report := HealthReport{
		Components: ComponentStatus{
			Postgres: component(s.pingDB(ctx)),
			S3:       component(s.s3.Ping(ctx)),
			RabbitMQ: component(s.broker.Ping(ctx)),
		},
	}
	if report.Components.Postgres == "ok" && report.Components.S3 == "ok" && report.Components.RabbitMQ == "ok" {
		report.Status = "ok"
	} else {
		report.Status = "fail"
	}
	return report
}

// pingDB проверяет соединение с PostgreSQL.
func (s *HealthService) pingDB(ctx context.Context) error {
	if s.db == nil {
		return errors.New("postgres pool is nil")
	}
	return s.db.Ping(ctx)
}

// component превращает ошибку ping в строковый статус.
func component(err error) string {
	if err != nil {
		return "fail"
	}
	return "ok"
}
