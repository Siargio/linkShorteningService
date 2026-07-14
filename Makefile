# Если рядом существует .env, подключаем его для локальных команд.
#
# Например, make migrate-up получит DATABASE_URL из .env.
ifneq (,$(wildcard ./.env))
	include .env
	export
endif

# Команда локального golang-migrate.
#
# DATABASE_URL приходит из .env.
MIGRATE=migrate -path migrations -database "$(DATABASE_URL)"

# Объявляем цели как phony.
#
# Это говорит make, что run, test и docker-up —
# команды, а не файлы с такими именами.
.PHONY: \
	run \
	build \
	test \
	test-race \
	cover \
	cover-html \
	fmt \
	vet \
	check \
	sqlc \
	migrate-up \
	migrate-down \
	migrate-version \
	docker-build \
	docker-up \
	docker-logs \
	docker-ps \
	docker-down \
	docker-clean

# Запускает приложение локально.
#
# PostgreSQL и Redis при этом должны быть запущены отдельно.
run:
	go run ./cmd/shortener

# Собирает локальный бинарный файл.
build:
	mkdir -p bin
	go build -o bin/shortener ./cmd/shortener

# Запускает все тесты и показывает покрытие каждого пакета.
test:
	go test ./... -cover

# Запускает тесты с race detector.
#
# Он помогает обнаружить конкурентный доступ
# к памяти из нескольких goroutine.
test-race:
	go test ./... -race

# Создаёт общий файл покрытия.
cover:
	go test ./... -coverprofile=coverage.out
	go tool cover -func=coverage.out

# Открывает HTML-отчёт покрытия в браузере.
cover-html:
	go test ./... -coverprofile=coverage.out
	go tool cover -html=coverage.out

# Форматирует все Go-файлы проекта.
fmt:
	gofmt -w .

# Запускает стандартный статический анализ.
vet:
	go vet ./...

# Полная локальная проверка перед коммитом.
#
# Команды выполняются последовательно.
# Если одна из них упадёт, make остановится.
check: fmt vet test-race

# Перегенерирует код sqlc.
sqlc:
	sqlc generate

# Применяет все ещё не применённые миграции локально.
migrate-up:
	$(MIGRATE) up

# Откатывает одну последнюю миграцию.
migrate-down:
	$(MIGRATE) down 1

# Показывает текущую версию миграции.
migrate-version:
	$(MIGRATE) version

# Только собирает Docker-образ приложения.
docker-build:
	docker compose build app

# Собирает и запускает все контейнеры в фоне.
docker-up:
	docker compose up --build -d
	docker compose ps

# Показывает логи Go-приложения в реальном времени.
docker-logs:
	docker compose logs -f app

# Показывает состояние всех сервисов.
docker-ps:
	docker compose ps

# Останавливает и удаляет контейнеры и сеть.
#
# Данные PostgreSQL и Redis сохраняются.
docker-down:
	docker compose down

# Полностью удаляет контейнеры, сеть и volumes.
#
# ВНИМАНИЕ:
# эта команда удалит все ссылки из PostgreSQL.
docker-clean:
	docker compose down -v --remove-orphans