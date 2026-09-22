// Package httpserver собирает HTTP-маршруты GophProfile.
package httpserver

import (
	"log/slog"
	"net/http"
	"path/filepath"
	"time"

	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"

	"gophprofile/internal/handlers"
	"gophprofile/internal/observability"
)

// Server — HTTP API и раздача веб-интерфейса.
type Server struct {
	avatars *handlers.AvatarHandler
	health  *handlers.HealthHandler
	webDir  string
	logger  *slog.Logger
	metrics *observability.Metrics
}

// New создаёт HTTP-сервер.
func New(avatars *handlers.AvatarHandler, health *handlers.HealthHandler, webDir string, logger *slog.Logger, metrics *observability.Metrics) *Server {
	return &Server{avatars: avatars, health: health, webDir: webDir, logger: logger, metrics: metrics}
}

type responseWriter struct {
	http.ResponseWriter
	status int
}

// WriteHeader сохраняет статус-код ответа для логирования.
func (rw *responseWriter) WriteHeader(code int) {
	rw.status = code
	rw.ResponseWriter.WriteHeader(code)
}

// statusCode возвращает записанный HTTP-статус или 200 по умолчанию.
func (rw *responseWriter) statusCode() int {
	if rw.status == 0 {
		return http.StatusOK
	}
	return rw.status
}

// Unwrap позволяет http.ResponseController достучаться до Flush/Hijack.
func (rw *responseWriter) Unwrap() http.ResponseWriter {
	return rw.ResponseWriter
}

// Router возвращает корневой HTTP-handler.
func (s *Server) Router() http.Handler {
	r := chi.NewRouter()
	r.Use(chimw.Recoverer)
	r.Use(observability.HTTPMiddleware("gophprofile-http", s.metrics))
	r.Use(s.logging)

	r.Get("/metrics", func(w http.ResponseWriter, r *http.Request) {
		s.metrics.Handler().ServeHTTP(w, r)
	})
	r.Get("/health", s.health.Get)

	r.Route("/api/v1", func(r chi.Router) {
		r.Post("/avatars", s.avatars.Upload)
		r.Get("/avatars/{avatar_id}", s.avatars.Get)
		r.Get("/avatars/{avatar_id}/metadata", s.avatars.GetMetadata)
		r.Delete("/avatars/{avatar_id}", s.avatars.Delete)
		r.Get("/users/{user_id}/avatar", s.avatars.GetUserAvatar)
		r.Get("/users/{user_id}/avatars", s.avatars.ListUserAvatars)
		r.Delete("/users/{user_id}/avatar", s.avatars.DeleteUserAvatar)
	})

	staticDir := filepath.Join(s.webDir, "static")
	index := filepath.Join(staticDir, "index.html")
	r.Get("/", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, index)
	})
	r.Get("/web/upload", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, index)
	})
	r.Handle("/static/*", http.StripPrefix("/static/", http.FileServer(http.Dir(staticDir))))

	return r
}

// logging оборачивает handler и пишет в лог метод, путь, статус и длительность.
func (s *Server) logging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		wrapped := &responseWriter{ResponseWriter: w}
		next.ServeHTTP(wrapped, r)
		s.logger.InfoContext(r.Context(), "http request",
			slog.String("method", r.Method),
			slog.String("path", r.URL.Path),
			slog.Int("status", wrapped.statusCode()),
			slog.Duration("duration", time.Since(start)),
		)
	})
}
