package observability

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	amqp "github.com/rabbitmq/amqp091-go"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

func TestTraceHandlerAddsTraceFields(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	otel.SetTracerProvider(tp)
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })

	var buf bytes.Buffer
	base := slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo})
	logger := slog.New(&traceHandler{next: base, service: "test-svc"})

	ctx, span := tp.Tracer("test").Start(context.Background(), "op")
	logger.InfoContext(ctx, "hello", "k", "v")
	span.End()

	var entry map[string]any
	if err := json.Unmarshal(buf.Bytes(), &entry); err != nil {
		t.Fatal(err)
	}
	if entry["service"] != "test-svc" {
		t.Fatalf("service=%v", entry["service"])
	}
	if entry["trace_id"] == nil || entry["trace_id"] == "" {
		t.Fatalf("missing trace_id: %v", entry)
	}
	if entry["span_id"] == nil || entry["span_id"] == "" {
		t.Fatalf("missing span_id: %v", entry)
	}
	sc := span.SpanContext()
	if entry["trace_id"] != sc.TraceID().String() {
		t.Fatalf("trace_id=%v want %s", entry["trace_id"], sc.TraceID())
	}
}

func TestAMQPHeaderCarrierPropagation(t *testing.T) {
	tp := sdktrace.NewTracerProvider()
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.TraceContext{})
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })

	ctx, span := tp.Tracer("test").Start(context.Background(), "publish")
	defer span.End()

	headers := amqp.Table{}
	otel.GetTextMapPropagator().Inject(ctx, AMQPHeaderCarrier(headers))
	if _, ok := headers["traceparent"]; !ok {
		t.Fatalf("traceparent not injected: %#v", headers)
	}

	extracted := otel.GetTextMapPropagator().Extract(context.Background(), AMQPHeaderCarrier(headers))
	remote := trace.SpanContextFromContext(extracted)
	if !remote.IsValid() {
		t.Fatal("extracted span context is invalid")
	}
	if remote.TraceID() != span.SpanContext().TraceID() {
		t.Fatalf("trace id mismatch: %s vs %s", remote.TraceID(), span.SpanContext().TraceID())
	}
}

func TestParseLevel(t *testing.T) {
	if parseLevel("debug") != slog.LevelDebug {
		t.Fatal("debug")
	}
	if parseLevel("ERROR") != slog.LevelError {
		t.Fatal("error")
	}
	if parseLevel("") != slog.LevelInfo {
		t.Fatal("default")
	}
}

func TestAMQPHeaderCarrierGetSet(t *testing.T) {
	c := AMQPHeaderCarrier{}
	c.Set("traceparent", "00-abc-def-01")
	if got := c.Get("traceparent"); got != "00-abc-def-01" {
		t.Fatal(got)
	}
	keys := c.Keys()
	if len(keys) != 1 || keys[0] != "traceparent" {
		t.Fatalf("%v", keys)
	}
	if AMQPHeaderCarrier(nil).Get("x") != "" {
		t.Fatal("nil carrier")
	}
	c.Set("bin", "x")
	c["bin"] = []byte("from-bytes")
	if !strings.Contains(c.Get("bin"), "from-bytes") {
		t.Fatal(c.Get("bin"))
	}
}
