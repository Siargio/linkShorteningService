ifneq (,$(wildcard ./.env))
	include .env
	export
endif

MIGRATE=migrate -path migrations -database "$(DATABASE_URL)"

.PHONY: run test sqlc migrate-up migrate-down migrate-version

run:
	go run ./cmd/shortener

test:
	go test ./... -cover

sqlc:
	sqlc generate

migrate-up:
	$(MIGRATE) up

migrate-down:
	$(MIGRATE) down 1

migrate-version:
	$(MIGRATE) version