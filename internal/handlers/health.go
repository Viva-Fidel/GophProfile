package handlers

import (
	"net/http"

	"gophprofile/internal/services"
)

// HealthHandler обрабатывает GET /health.
type HealthHandler struct {
	svc *services.HealthService
}

// NewHealthHandler создаёт обработчик healthcheck.
func NewHealthHandler(svc *services.HealthService) *HealthHandler {
	return &HealthHandler{svc: svc}
}

// Get возвращает статусы БД, S3 и брокера.
func (h *HealthHandler) Get(w http.ResponseWriter, r *http.Request) {
	report := h.svc.Check(r.Context())
	status := http.StatusOK
	if report.Status != "ok" {
		status = http.StatusServiceUnavailable
	}
	writeJSON(w, status, report)
}
