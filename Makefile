DB_URL ?= postgres://chirpy:chirpy@localhost:5432/chirpy?sslmode=disable
TEST_DATABASE_URL ?= postgres://chirpy:chirpy@localhost:5432/chirpy_test?sslmode=disable
TEST_REDIS_ADDR ?= localhost:6379
GOOSE = go run github.com/pressly/goose/v3/cmd/goose@v3.26.0

.PHONY: run build docker test test-integration lint sqlc migrate deps

deps: ## start postgres and redis
	docker compose up -d --wait
	docker compose exec -T postgres psql -U chirpy -tc "SELECT 1 FROM pg_database WHERE datname='chirpy_test'" | grep -q 1 || \
		docker compose exec -T postgres createdb -U chirpy chirpy_test

migrate: ## migrations also run automatically on server start (MIGRATE_ON_START)
	$(GOOSE) -dir sql/schema postgres "$(DB_URL)" up

run:
	go run ./cmd/server

build:
	go build -o bin/chirpy ./cmd/server

docker:
	docker build -t chirpy:local .

test:
	go test -race ./...

test-integration:
	TEST_DATABASE_URL="$(TEST_DATABASE_URL)" TEST_REDIS_ADDR="$(TEST_REDIS_ADDR)" \
		go test -tags integration -race -count=1 ./internal/integration/...

lint:
	test -z "$$(gofmt -l $$(git ls-files '*.go'))"
	go vet ./... && go vet -tags integration ./...

sqlc:
	sqlc generate
