# football-balancer — developer entry points.
# Run `make` or `make help` for a list of targets.

BIN        := football-balancer
PKG        := ./cmd/server
BUILD_DIR  := bin
OUT        := $(BUILD_DIR)/$(BIN)

ADDR       ?= :8080
DATA       ?= data/players.json
MATCHES    ?= data/matches.json
HOST_PORT  ?= 8081

IMAGE      ?= $(BIN):latest
LDFLAGS    := -trimpath -ldflags="-s -w"

GO         ?= go
COMPOSE    ?= docker compose

.DEFAULT_GOAL := help
.PHONY: help all build run dev start stop restart test test-race vet fmt fmt-check lint \
        tidy tidy-check docker-build docker-test docker-run docker-stop logs ps \
        compose-up compose-down compose-logs compose-restart clean dist

help: ## Show this help
	@printf 'football-balancer\n\nUsage: make \033[36m<target>\033[0m\n\n'
	@grep -hE '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) \
		| awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-18s\033[0m %s\n", $$1, $$2}'
	@printf '\nOverrides: ADDR=%s DATA=%s MATCHES=%s HOST_PORT=%s\n' "$(ADDR)" "$(DATA)" "$(MATCHES)" "$(HOST_PORT)"

all: fmt-check vet test build ## Format check, vet, test, build

build: ## Build the server binary into bin/
	CGO_ENABLED=0 $(GO) build $(LDFLAGS) -o $(OUT) $(PKG)

dist: ## Cross-compile linux/amd64 + linux/arm64 binaries
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 $(GO) build $(LDFLAGS) -o $(BUILD_DIR)/$(BIN)-linux-amd64 $(PKG)
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 $(GO) build $(LDFLAGS) -o $(BUILD_DIR)/$(BIN)-linux-arm64 $(PKG)

run: build ## Build, then run the server in the foreground
	$(OUT) -addr $(ADDR) -data $(DATA) -matches $(MATCHES)

dev: ## Run with auto-restart on file change (requires: go install github.com/cespare/reflex@latest)
	reflex -r '\.go$$' -s -- sh -c 'make build && $(OUT) -addr $(ADDR) -data $(DATA) -matches $(MATCHES)'

start: ## Build and run detached in the background, logging to bin/server.log
	@mkdir -p $(BUILD_DIR)
	make build
	nohup $(OUT) -addr $(ADDR) -data $(DATA) -matches $(MATCHES) > $(BUILD_DIR)/server.log 2>&1 & \
		echo $$! > $(BUILD_DIR)/server.pid
	@echo "started (pid $$(cat $(BUILD_DIR)/server.pid)) — logs: $(BUILD_DIR)/server.log"

stop: ## Stop a server started with `make start`
	@if [ -f $(BUILD_DIR)/server.pid ]; then \
		kill $$(cat $(BUILD_DIR)/server.pid) 2>/dev/null || true; \
		rm -f $(BUILD_DIR)/server.pid; \
		echo "stopped"; \
	else \
		echo "not running (no $(BUILD_DIR)/server.pid)"; \
	fi

restart: stop start ## Restart the background server

test: ## Run unit tests
	$(GO) test ./...

test-race: ## Run unit tests with the race detector
	$(GO) test -race ./...

vet: ## go vet
	$(GO) vet ./...

fmt: ## gofmt -w the tree
	gofmt -w .

fmt-check: ## Fail if anything needs gofmt
	@test -z "$$(gofmt -l .)" || { echo "gofmt needed on:"; gofmt -l .; exit 1; }

lint: fmt-check vet test ## Format check + vet + tests (same gates as the Docker test stage)

tidy: ## go mod tidy
	$(GO) mod tidy

tidy-check: ## Fail if go.mod/go.sum would change under go mod tidy
	@mkdir -p $(BUILD_DIR); cp go.mod $(BUILD_DIR)/go.mod.bak
	$(GO) mod tidy
	@diff -u $(BUILD_DIR)/go.mod.bak go.mod || { echo "go.mod not tidy: run 'make tidy'"; exit 1; }

# ---- Docker ---------------------------------------------------------------

docker-build: ## Build the runtime image
	docker build -t $(IMAGE) --target runtime .

docker-test: ## Run fmt/vet/tests inside the image build
	docker build --target test .

docker-run: docker-build ## Run the image, publishing HOST_PORT
	docker rm -f $(BIN) 2>/dev/null || true
	docker run -d --name $(BIN) --init -p $(HOST_PORT):8080 \
		-v $(BIN)-data:/data $(IMAGE)

docker-stop: ## Remove the container started by `make docker-run`
	docker rm -f $(BIN) 2>/dev/null || true

logs: ## Tail logs from the container started by `make docker-run`
	docker logs -f $(BIN)

ps: ## Show container status
	docker ps -a --filter name=$(BIN)

compose-up: ## docker compose up -d --build (honours HOST_PORT)
	HOST_PORT=$(HOST_PORT) $(COMPOSE) up -d --build

compose-down: ## docker compose down
	$(COMPOSE) down

compose-logs: ## Tail docker compose logs
	$(COMPOSE) logs -f

compose-restart: ## Restart the compose service
	HOST_PORT=$(HOST_PORT) $(COMPOSE) restart

clean: ## Remove build artifacts
	rm -rf $(BUILD_DIR)
