.PHONY: test test-go test-web build run integration-mysql

test: test-go test-web

test-go:
	go test ./...

test-web:
	npm --prefix web test -- --run

build:
	./scripts/build.sh

run:
	go run ./cmd/controlpanel

integration-mysql:
	@test -n "$(TEST_MYSQL_URL)" || (echo "TEST_MYSQL_URL is required" >&2; exit 1)
	go test ./internal/database ./internal/auth -run 'MySQL|RepositoryContract' -v
