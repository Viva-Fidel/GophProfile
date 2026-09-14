package observability

import (
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

func (r *statusRecorder) statusCode() int {
	if r.status == 0 {
		return http.StatusOK
	}
	return r.status
}

// Unwrap позволяет http.ResponseController достучаться до Flush/Hijack.
func (r *statusRecorder) Unwrap() http.ResponseWriter {
	return r.ResponseWriter
}

// HTTPMiddleware добавляет OTel-спаны и Prometheus RED-метрики.
func HTTPMiddleware(operation string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		otelHandler := otelhttp.NewHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			rec := &statusRecorder{ResponseWriter: w}
			next.ServeHTTP(rec, r)

			routePattern := chi.RouteContext(r.Context()).RoutePattern()
			if routePattern == "" {
				routePattern = r.URL.Path
			}
			status := rec.statusCode()
			ObserveHTTP(r.Method, routePattern, status, time.Since(start))

			span := trace.SpanFromContext(r.Context())
			span.SetAttributes(
				attribute.String("http.route", routePattern),
				attribute.Int("http.status_code", status),
			)
			if status >= 500 {
				span.RecordError(fmt.Errorf("http status %d", status))
			}
		}), operation, otelhttp.WithSpanNameFormatter(func(_ string, r *http.Request) string {
			routePattern := chi.RouteContext(r.Context()).RoutePattern()
			if routePattern == "" {
				return r.Method + " " + r.URL.Path
			}
			return r.Method + " " + routePattern
		}))

		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			otelHandler.ServeHTTP(w, r)
		})
	}
}
