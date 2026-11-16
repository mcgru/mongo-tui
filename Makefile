SHELL := /bin/bash

BIN     := bin/mongo-tui
PKG     := ./cmd/mongo-tui
VERSION ?= 0.1.1-dev
LDFLAGS := -s -w -X main.version=$(VERSION)
GO      ?= go

.DEFAULT_GOAL := help
.PHONY: help build run test vet fmt tidy check clean mongo-up mongo-down test-mongo auth-up auth-down test-mongo-auth

help: ## Show this help
	@grep -hE '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) \
		| sort \
		| awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-8s\033[0m %s\n", $$1, $$2}'

build: ## Build bin/mongo-tui
	$(GO) build -trimpath -ldflags "$(LDFLAGS)" -o $(BIN) $(PKG)

build-static: ## Build bin/mongo-tui
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags "$(LDFLAGS)" -o $(BIN) $(PKG)

run: ## Run from source, extra args in ARGS (make run ARGS="--help")
	$(GO) run $(PKG) $(ARGS)

test: ## Run unit tests
	$(GO) test ./...

mongo-up: ## Start the MongoDB 8.0 replica set for live tests
	bash deploy/mongo/init.sh

mongo-down: ## Stop the MongoDB test cluster
	docker compose -f deploy/mongo/docker-compose.yml down

test-mongo: mongo-up ## Run live MongoDB tests against the test cluster
	MONGODB_URI='mongodb://127.0.0.1:57017/?replicaSet=rs0' $(GO) test ./internal/mongo/... ./internal/tui

auth-up: ## Start the auth+TLS MongoDB standalone for hardening tests
	bash deploy/mongo-auth/init.sh

auth-down: ## Stop the auth+TLS MongoDB test cluster
	docker compose -f deploy/mongo-auth/docker-compose.yml down

test-mongo-auth: auth-up ## Run live SCRAM/TLS/killop-privilege tests
	MONGODB_AUTH_URI='mongodb://root@127.0.0.1:57018/?authSource=admin&tls=true&tlsCAFile=$(CURDIR)/deploy/mongo-auth/certs/ca.pem' \
	MONGODB_AUTH_PASSWORD=rootpass \
	MONGODB_AUTH_READER_URI='mongodb://reader@127.0.0.1:57018/?authSource=app&tls=true&tlsCAFile=$(CURDIR)/deploy/mongo-auth/certs/ca.pem' \
	MONGODB_AUTH_READER_PASSWORD=readerpass \
	$(GO) test ./internal/mongo -run 'TestLiveAuthTLS|TestLiveReaderCannotWrite' -v

vet: ## Run go vet
	$(GO) vet ./...

fmt: ## Format the sources
	$(GO) fmt ./...

tidy: ## Tidy go.mod and go.sum
	$(GO) mod tidy

check: fmt vet test ## Format, vet and test

clean: ## Remove build artifacts
	rm -rf bin
