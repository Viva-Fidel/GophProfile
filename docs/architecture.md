# Архитектура GophProfile

GophProfile — сервис аватарок. Пользователь загружает картинку через API,
сервер сохраняет оригинал и сразу отвечает. Тяжёлую работу (миниатюры) делает
отдельный worker в фоне.

Внутри три хранилища:

- **PostgreSQL** — кто загрузил, статус, пути к файлам
- **MinIO / S3** — сами файлы
- **RabbitMQ** — очередь задач между сервером и worker’ом

---

## Локальный запуск

`docker compose up` поднимает всё сразу: API, worker, базу, очередь, хранилище
и метрики, логи, трейсы.

```mermaid
flowchart LR
  Client[Клиент / веб-UI] -->|HTTP :8080| Server[Сервер]
  Server --> PG[(PostgreSQL)]
  Server --> S3[(MinIO / S3)]
  Server --> MQ[[RabbitMQ]]
  Worker[Worker] --> PG
  Worker --> S3
  Worker --> MQ
  Server -->|трейсы| Jaeger[Jaeger]
  Worker -->|трейсы| Jaeger
  Server -->|метрики| Prom[Prometheus]
  Worker -->|метрики| Prom
  Prom --> Grafana[Grafana]
  Server -->|логи| Promtail --> Loki --> Grafana
```

Приложение общаются только с API. База, файлы и очередь —
общие для API и фонового worker’а. Prometheus с Grafana нужны, чтобы видеть,
не упал ли сервис и как он справляется с нагрузкой.

---

## Что происходит при загрузке аватарки

1. Клиент шлёт файл на `POST /api/v1/avatars`.
2. Сервер кладёт оригинал в S3, пишет запись в БД и кидает задачу в очередь.
3. Клиенту сразу приходит «готово» (201) — ждать миниатюры не нужно.
4. Worker забирает задачу, режет превью и обновляет статус в БД.

```mermaid
sequenceDiagram
  participant C as Клиент
  participant S as Сервер
  participant DB as PostgreSQL
  participant OS as MinIO/S3
  participant Q as RabbitMQ
  participant W as Worker

  C->>S: Загрузка файла
  S->>DB: Сохранить метаданные
  S->>OS: Сохранить оригинал
  S->>Q: Поставить задачу на обработку
  S-->>C: Ответ 201
  Q->>W: Задача
  W->>OS: Скачать оригинал, сохранить превью
  W->>DB: Обновить статус и пути к превью
```

---

## Деплой в Kubernetes

Запрос сначала попадает на Ingress, затем на Service,
который распределяет нагрузку между репликами API.

Worker снаружи недоступен: он читает задачи из очереди и отдаёт метрики
в Prometheus.

Настройки лежат в ConfigMap, секреты (пароли, URI) — в Secret.
При росте нагрузки HPA увеличивает число подов. NetworkPolicy задаёт,
какие сервисы вообще могут общаться друг с другом.

```mermaid
flowchart TB
  subgraph IngressNS[вход в кластер]
    ING[Ingress]
  end

  subgraph AppNS[наш namespace]
    DEP[Сервер]
    WRK[Worker]
    SVC[Service]
    HPA[Автоскейл сервера]
    WHPA[Автоскейл worker]
    SM[Сбор метрик]
    PR[Алерты]
    GD[Дашборды Grafana]
    MIG[Миграции БД]
  end

  subgraph Deps[зависимости]
    DB[(PostgreSQL)]
    OBJ[(MinIO / S3)]
    RMQ[[RabbitMQ]]
    PROM[Prometheus]
    GRAF[Grafana]
  end

  User[Пользователь] --> ING --> SVC --> DEP
  HPA --> DEP
  WHPA --> WRK
  MIG --> DB
  DEP --> DB
  DEP --> OBJ
  DEP --> RMQ
  WRK --> DB
  WRK --> OBJ
  WRK --> RMQ
  SM --> PROM
  PR --> PROM
  GD --> GRAF
```

Где лежат манифесты:

| Что | Куда смотреть |
|---|---|
| Готовые yaml для `kubectl apply` | `deploy/k8s/` |
| Helm-чарт (удобнее для окружений) | `deploy/helm/gophprofile/` |
| SQL миграций для Helm | `deploy/helm/gophprofile/files/migrations/` |

---

## Устойчивость к сбоям

**Недоступны БД, S3 или очередь**  
После нескольких ошибок подряд circuit breaker на время отключает вызовы
к этой зависимости и возвращает `503`. Примерно через 30 секунд делает
повторную попытку.

**Слишком много запросов**  
На API включён rate limit (`RATE_LIMIT_RPS` / `RATE_LIMIT_BURST`).
Превышение лимита — ответ `429`.

**Остановка или обновление пода**  
Под сначала убирают из балансировки, дают завершить текущие запросы,
затем останавливают процесс. Worker аналогично заканчивает текущую задачу
и завершается.

---

## Мониторинг и алерты

Два дашборда в Grafana:

- **Service Overview** — запросы, ошибки, задержки, загрузки, глубина очереди
- **Kubernetes** — число подов, CPU/память, HPA, активные алерты

Алерты (PrometheusRule):

| Алерт | Когда срабатывает |
|---|---|
| TargetDown | Prometheus не получает метрики сервиса |
| PodNotReady | Часть подов не готова принимать трафик |
| HighErrorRate | Высокая доля ответов 5xx |
| HighLatency | Выросла задержка обработки запросов |
| QueueDepthHigh | В очереди накопилось слишком много задач |
| HPAAtMax | Достигнут максимум реплик при сохраняющейся нагрузке |

Подробности запуска и переменных окружения — в [README](../README.md).
Контракт API — в [openapi.yaml](./openapi.yaml).
