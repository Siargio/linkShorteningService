# Link Shortening Service

Сервис сокращения ссылок на Go с PostgreSQL, Redis, Kafka, тестами и запуском через Docker Compose.

Сервис принимает длинный URL, создаёт короткий код, выполняет перенаправление, считает переходы и публикует доменные события в Kafka.

## Возможности

* создание короткой ссылки;
* перенаправление на исходный URL;
* получение статистики;
* хранение данных в PostgreSQL;
* кеширование URL в Redis;
* события `link.created` и `link.visited` в Kafka;
* автоматические миграции;
* unit-тесты;
* запуск всей инфраструктуры через Docker Compose;
* graceful shutdown.

## Технологии

* Go 1.23+;
* PostgreSQL 16;
* `pgx/v5` и `sqlc`;
* Redis 7 и `go-redis/v9`;
* Apache Kafka в KRaft-режиме и `kafka-go`;
* `golang-migrate`;
* Docker и Docker Compose;
* стандартные `net/http`, `httptest`, `slog`.

## Архитектура

```text
HTTP request
    |
    v
handler
    |
    v
service
    |-------------------------|
    v                         v
repository                  cache
    |                         |
    v                         v
PostgreSQL                  Redis

service -> event publisher -> Kafka
```

Назначение слоёв:

* `domain` — модели, ошибки, валидация и генерация короткого кода;
* `repository` — PostgreSQL через `pgx` и `sqlc`;
* `service` — бизнес-логика;
* `handler` — HTTP, JSON, статусы и redirect;
* `cache` — Redis;
* `event` — публикация событий в Kafka;
* `config` — загрузка переменных окружения;
* `cmd/shortener` — сборка зависимостей и запуск приложения.

Основное направление зависимостей:

```text
handler -> service -> repository
                   -> cache
                   -> event publisher
```

`domain` не зависит от HTTP, PostgreSQL, Redis или Kafka.

## Структура проекта

```text
linkShorteningService/
├── cmd/shortener/main.go
├── internal/
│   ├── domain/
│   ├── event/
│   ├── handler/
│   ├── repository/
│   │   └── db/              # код, сгенерированный sqlc
│   └── service/
├── pkg/
│   ├── cache/
│   └── config/
├── migrations/
├── sql/queries/
├── .dockerignore
├── .env.example
├── Dockerfile
├── docker-compose.yml
├── Makefile
├── go.mod
├── go.sum
└── sqlc.yaml
```

## Схема базы данных

```sql
CREATE TABLE links (
    id         SERIAL PRIMARY KEY,
    short_code VARCHAR(10) NOT NULL UNIQUE,
    long_url   TEXT NOT NULL,
    clicks     INT NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
```

## Быстрый запуск через Docker Compose

### Требования

* Docker;
* Docker Compose.

### Запуск

```bash
docker compose up --build
```

Или:

```bash
make docker-up
```

После запуска API доступен по адресу:

```text
http://localhost:8080
```

Docker Compose запускает PostgreSQL, Redis, Kafka, создание Kafka-топика, применение миграций и Go-приложение.

### Проверка контейнеров

```bash
docker compose ps -a
```

Ожидаемые состояния:

```text
db                  Up (healthy)
redis               Up (healthy)
kafka               Up (healthy)
kafka-permissions   Exited (0)
kafka-init          Exited (0)
migrate             Exited (0)
app                 Up
```

`Exited (0)` у одноразовых контейнеров является нормальным состоянием.

### Логи

```bash
docker compose logs -f app
```

Kafka:

```bash
docker compose logs -f kafka
```

### Остановка

```bash
docker compose down
```

Данные в volumes сохраняются.

Полное удаление контейнеров и данных:

```bash
docker compose down -v
```

> Команда с `-v` удаляет данные PostgreSQL, Redis и Kafka.

## Переменные окружения

Создайте `.env` на основе примера:

```bash
cp .env.example .env
```

Пример для локального запуска Go-приложения на Mac:

```env
HTTP_PORT=8080
BASE_URL=http://localhost:8080

DATABASE_URL=postgres://postgres:postgres@localhost:5433/shortener?sslmode=disable

REDIS_ADDR=localhost:6379
REDIS_PASSWORD=
REDIS_DB=0
REDIS_TTL=24h

KAFKA_BROKERS=localhost:29092
KAFKA_TOPIC=link-events
```

Адреса при локальном запуске:

```text
PostgreSQL: localhost:5433
Redis:      localhost:6379
Kafka:      localhost:29092
```

Адреса внутри Docker Compose:

```text
PostgreSQL: db:5432
Redis:      redis:6379
Kafka:      kafka:19092
```

`localhost` внутри контейнера означает сам контейнер, поэтому между сервисами используются имена Compose-сервисов.

## Локальный запуск

Запустите инфраструктуру:

```bash
docker compose up -d db redis kafka kafka-init
```

Примените миграции:

```bash
make migrate-up
```

Запустите приложение:

```bash
make run
```

Или:

```bash
go run ./cmd/shortener
```

## API

### Создать короткую ссылку

```http
POST /shorten
Content-Type: application/json
```

Запрос:

```json
{
  "url": "https://golang.org"
}
```

Пример:

```bash
curl -i \
  -X POST \
  http://localhost:8080/shorten \
  -H "Content-Type: application/json" \
  -d '{"url":"https://golang.org"}'
```

Успешный ответ:

```http
HTTP/1.1 201 Created
Content-Type: application/json; charset=utf-8
```

```json
{
  "short_url": "http://localhost:8080/P6k7AF"
}
```

Принимаются только абсолютные URL со схемой `http` или `https`.

### Перейти по короткой ссылке

```http
GET /{code}
```

Пример:

```bash
curl -i http://localhost:8080/P6k7AF
```

Ответ:

```http
HTTP/1.1 302 Found
Location: https://golang.org
```

Для автоматического перехода:

```bash
curl -L http://localhost:8080/P6k7AF
```

При успешном переходе URL ищется в Redis, при cache miss читается из PostgreSQL, счётчик увеличивается в PostgreSQL, а в Kafka публикуется `link.visited`.

### Получить статистику

```http
GET /stats/{code}
```

Пример:

```bash
curl http://localhost:8080/stats/P6k7AF
```

Ответ:

```json
{
  "short_code": "P6k7AF",
  "long_url": "https://golang.org",
  "clicks": 1,
  "created_at": "2026-07-14T21:46:46.457647+03:00"
}
```

Получение статистики не увеличивает счётчик.

## HTTP-статусы

| Сценарий               |                         Статус |
| ---------------------- | -----------------------------: |
| Ссылка создана         |                  `201 Created` |
| Статистика получена    |                       `200 OK` |
| Редирект               |                    `302 Found` |
| Невалидный URL или код |              `400 Bad Request` |
| Некорректный JSON      |              `400 Bad Request` |
| Слишком большое тело   | `413 Request Entity Too Large` |
| Неверный Content-Type  |   `415 Unsupported Media Type` |
| Ссылка не найдена      |                `404 Not Found` |
| Внутренняя ошибка      |    `500 Internal Server Error` |

Формат ошибки:

```json
{
  "error": "link not found"
}
```

## Redis

Redis кеширует соответствие:

```text
short_code -> long_url
```

Формат ключа:

```text
link:{short_code}
```

Проверка:

```bash
docker compose exec redis redis-cli GET link:P6k7AF
```

TTL:

```bash
docker compose exec redis redis-cli TTL link:P6k7AF
```

Redis является оптимизацией. При его недоступности сервис продолжает работать через PostgreSQL.

## Kafka

Kafka работает в KRaft-режиме без ZooKeeper.

Топик:

```text
link-events
```

Конфигурация локального топика:

```text
partitions:         3
replication factor: 1
```

### `link.created`

Публикуется после успешного создания ссылки:

```json
{
  "event_type": "link.created",
  "schema_version": 1,
  "short_code": "P6k7AF",
  "long_url": "https://golang.org",
  "occurred_at": "2026-07-14T20:00:00Z"
}
```

### `link.visited`

Публикуется после успешного перехода:

```json
{
  "event_type": "link.visited",
  "schema_version": 1,
  "short_code": "P6k7AF",
  "long_url": "https://golang.org",
  "occurred_at": "2026-07-14T20:01:00Z"
}
```

Kafka message key равен `short_code`, поэтому события одной ссылки попадают в одну партицию и сохраняют порядок внутри неё.

### Проверка топика

```bash
docker compose exec kafka \
  /opt/kafka/bin/kafka-topics.sh \
  --bootstrap-server kafka:19092 \
  --describe \
  --topic link-events
```

### Console consumer

```bash
docker compose exec kafka \
  /opt/kafka/bin/kafka-console-consumer.sh \
  --bootstrap-server kafka:19092 \
  --topic link-events \
  --from-beginning \
  --property print.key=true \
  --property key.separator=" | "
```

### Гарантии публикации

Сейчас используется best-effort схема:

```text
PostgreSQL commit
    |
    v
Kafka publish
```

Если PostgreSQL успешно сохранил данные, а Kafka недоступна, основная HTTP-операция остаётся успешной, но событие может быть потеряно.

Продакшен-улучшение — Transactional Outbox:

```text
одна PostgreSQL-транзакция
    ├── links
    └── outbox_events
             |
             v
       отдельный worker
             |
             v
           Kafka
```

## PostgreSQL

Проверка записей:

```bash
docker compose exec db \
  psql -U postgres -d shortener \
  -c "SELECT id, short_code, long_url, clicks, created_at FROM links ORDER BY id DESC;"
```

## Миграции

Применить:

```bash
make migrate-up
```

Откатить одну миграцию:

```bash
make migrate-down
```

Версия миграции:

```bash
make migrate-version
```

В Docker миграции применяются автоматически сервисом `migrate`.

## sqlc

SQL-запросы находятся в:

```text
sql/queries/links.sql
```

Генерация кода:

```bash
make sqlc
```

Или:

```bash
sqlc generate
```

Файлы в `internal/repository/db` сгенерированы автоматически и не редактируются вручную.

## Тесты

Все тесты:

```bash
go test ./...
```

С race detector:

```bash
make test-race
```

Полная проверка:

```bash
make check
```

Покрытие:

```bash
make cover
```

HTML-отчёт:

```bash
make cover-html
```

Или напрямую:

```bash
go test ./... -coverprofile=coverage.out
go tool cover -func=coverage.out
go tool cover -html=coverage.out
```

## Команды Makefile

| Команда             | Назначение                    |
| ------------------- | ----------------------------- |
| `make run`          | запустить приложение локально |
| `make build`        | собрать бинарный файл         |
| `make test`         | запустить тесты               |
| `make test-race`    | тесты с race detector         |
| `make cover`        | показать покрытие             |
| `make cover-html`   | HTML-отчёт покрытия           |
| `make fmt`          | форматирование                |
| `make vet`          | статический анализ            |
| `make check`        | полная проверка               |
| `make sqlc`         | сгенерировать sqlc-код        |
| `make migrate-up`   | применить миграции            |
| `make migrate-down` | откатить миграцию             |
| `make docker-up`    | запустить Compose             |
| `make docker-logs`  | логи приложения               |
| `make docker-down`  | остановить Compose            |
| `make docker-clean` | удалить контейнеры и volumes  |

## Особенности реализации

### Генерация кода

Код состоит из латинских букв и цифр и создаётся через `crypto/rand`. При конфликте уникального кода сервис выполняет повторную попытку с ограниченным числом ретраев.

### Счётчик переходов

Счётчик увеличивается атомарно:

```sql
UPDATE links
SET clicks = clicks + 1
WHERE short_code = $1;
```

### Обработка ошибок

Внутренние ошибки PostgreSQL, Redis и Kafka записываются в лог, а клиент получает безопасный ответ без технических деталей.

### Graceful shutdown

При `SIGINT` или `SIGTERM` приложение прекращает принимать новые запросы, завершает текущие и закрывает HTTP-сервер, Kafka writer, Redis-клиент и пул PostgreSQL.

## Возможные улучшения

* Transactional Outbox;
* Prometheus-метрики;
* OpenTelemetry;
* request ID и middleware логирования;
* rate limiting;
* пользовательские alias;
* срок действия и деактивация ссылок;
* Kafka consumer для аналитики;
* интеграционные тесты;
* CI/CD;
* OpenAPI/Swagger;
* `/health/live` и `/health/ready`.

## Автор

Учебный проект сервиса сокращения ссылок на Go.
