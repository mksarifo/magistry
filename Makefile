.PHONY: up down test build dev

up:
	docker compose up --build -d

down:
	docker compose down

test:
	go test -race ./...

build:
	cd web && npm ci && npm run build
	go build -o bin/magistry ./cmd/magistry

dev:
	go run ./cmd/magistry
