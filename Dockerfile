# Build stage
FROM golang:1.26-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o server ./cmd/server
RUN CGO_ENABLED=0 GOOS=linux go build -o worker ./cmd/worker

# Runtime stage
FROM alpine:latest
RUN apk --no-cache add ca-certificates tzdata \
    && adduser -D -u 1000 -g 1000 app
WORKDIR /app
COPY --from=builder --chown=app:app /app/server .
COPY --from=builder --chown=app:app /app/worker .
COPY --from=builder --chown=app:app /app/web ./web
COPY --from=builder --chown=app:app /app/migrations ./migrations
USER app
STOPSIGNAL SIGTERM
CMD ["./server"]
