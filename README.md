# GophProfile

Сервис аватарок на Go. Можно загрузить картинку, скачать оригинал или превью,
посмотреть метаданные и удалить аватарку.

Картинки хранятся в MinIO/S3, сведения о них - в PostgreSQL.
Миниатюры собирает отдельный worker по задачам из RabbitMQ.
Метрики смотрят в Prometheus, трейсы - в OpenTelemetry.

Архитектура: [docs/architecture.md](docs/architecture.md).  
Описание API: [docs/openapi.yaml](docs/openapi.yaml).

---

## Быстрый старт

```bash
cp .env.example .env
docker compose up --build
```

| Адрес | Что это |
|---|---|
| http://localhost:8080 | API и веб-интерфейс |
| http://localhost:8080/health | Проверка здоровья |
| http://localhost:8080/metrics | Метрики API |
| http://localhost:9091/metrics | Метрики worker |
| http://localhost:3000 | Grafana (`admin` / `admin`) |
| http://localhost:9090 | Prometheus |
| http://localhost:16686 | Jaeger |
| http://localhost:9001 | Консоль MinIO |
| http://localhost:15672 | Управление RabbitMQ |

Остановка: `Ctrl+C` или `docker compose down`. Сервер и worker корректно
завершают текущую работу по сигналу остановки.

---

## Запуск без Compose

1. Поднять зависимости:
   ```bash
   docker compose up -d postgres rabbitmq minio jaeger
   ```
2. Скопировать и при необходимости поправить `.env`:
   ```bash
   cp .env.example .env
   ```
3. Экспортировать переменные из `.env`.
4. Запустить:
   ```bash
   go run ./cmd/server
   go run ./cmd/worker
   ```

При старте server сам применяет миграции БД.

Полный список переменных — в `.env.example`.

Если Postgres, S3 или RabbitMQ начинают отвечать ошибками, срабатывает
circuit breaker: после нескольких сбоев вызовы временно блокируются (~30 с),
API отвечает `503`.

---

## Деплой в Kubernetes

Сначала соберите образ:

```bash
docker build -t gophprofile:latest .
```

Затем загрузите его в кластер (`kind load` / `minikube image load` / registry).

### Вариант A: манифесты

Перед применением заполните секреты в `deploy/k8s/secret.yaml` (base64)
и хост в Ingress / ConfigMap.

```bash
kubectl apply -f deploy/k8s/configmap.yaml
kubectl apply -f deploy/k8s/secret.yaml
kubectl apply -f deploy/k8s/rbac.yaml
kubectl apply -f deploy/k8s/psp.yaml
kubectl apply -f deploy/k8s/deployment.yaml
kubectl apply -f deploy/k8s/service.yaml
kubectl apply -f deploy/k8s/worker-deployment.yaml
kubectl apply -f deploy/k8s/worker-service.yaml
kubectl apply -f deploy/k8s/ingress.yaml
kubectl apply -f deploy/k8s/hpa.yaml
kubectl apply -f deploy/k8s/worker-hpa.yaml
kubectl apply -f deploy/k8s/servicemonitor.yaml
kubectl apply -f deploy/k8s/worker-servicemonitor.yaml
kubectl apply -f deploy/k8s/prometheusrule.yaml
kubectl apply -f deploy/k8s/grafana-dashboard.yaml
kubectl apply -f deploy/k8s/networkpolicy.yaml
kubectl apply -f deploy/k8s/worker-networkpolicy.yaml
```

### Вариант B: Helm

```bash
helm upgrade --install gophprofile ./deploy/helm/gophprofile \
  -f ./deploy/helm/gophprofile/values-dev.yaml \
  --set secrets.databaseUri='postgres://...' \
  --set secrets.s3AccessKey='...' \
  --set secrets.s3SecretKey='...' \
  --set secrets.rabbitmqUri='amqp://...'
```

Готовые оверлеи: `values-dev.yaml`, `values-staging.yaml`, `values-prod.yaml`.

Перед установкой и обновлением Helm применяет миграции БД.
Вместе с API и worker разворачиваются автомасштабирование, метрики,
алерты и дашборды Grafana.

---

## Завершение работы

При остановке (`SIGTERM` / `SIGINT`) сервер даёт текущим HTTP-запросам
завершиться — не дольше 10 секунд. Worker доделывает текущую задачу
и выключает endpoint метрик.

В Kubernetes перед остановкой под убирают из балансировки (пауза `preStop` 5 с),
чтобы новые запросы на него не попадали. На завершение отводится до 30 секунд
(`terminationGracePeriodSeconds`).

---

## Мониторинг

**Метрики:** API — `:8080/metrics`, worker — `:9091/metrics`.
В кластере их забирает Prometheus через ServiceMonitor.

**Дашборды Grafana:**

| Дашборд | Файл |
|---|---|
| Обзор сервиса | `deploy/grafana/dashboards/gophprofile-overview.json` |
| Кластер | `deploy/grafana/dashboards/gophprofile-k8s.json` |

В Kubernetes те же дашборды лежат в ConfigMap с label `grafana_dashboard: "1"`.

**Алерты** (`deploy/k8s/prometheusrule.yaml` / Helm `prometheusRule`):

| Алерт | Когда |
|---|---|
| TargetDown | Нет метрик дольше 2 минут |
| PodNotReady | Есть неготовые поды дольше 5 минут |
| HighErrorRate | Доля 5xx выше 5% дольше 5 минут |
| HighLatency | p95 задержки выше 1 с дольше 10 минут |
| QueueDepthHigh | В очереди больше 1000 сообщений дольше 10 минут |
| HPAAtMax | Автоскейл на максимуме реплик дольше 15 минут |

---

## Тесты

```bash
go test ./...
```
