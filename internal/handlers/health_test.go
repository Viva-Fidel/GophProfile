package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"gophprofile/internal/services"
)

type pingFail struct{}

func (pingFail) Ping(context.Context) error { return errors.New("down") }

func TestHealthHandler(t *testing.T) {
	h := NewHealthHandler(services.NewHealthService(nil, pingFail{}, pingFail{}))
	rr := httptest.NewRecorder()
	h.Get(rr, httptest.NewRequest(http.MethodGet, "/health", nil))
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatal(rr.Code)
	}
	var report services.HealthReport
	if err := json.Unmarshal(rr.Body.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if report.Status != "fail" {
		t.Fatal(report)
	}
}
