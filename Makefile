SHELL := /bin/sh
.DEFAULT_GOAL := help

GO_IMAGE ?= golang:1.26
GO := docker run --rm -v "$(CURDIR):/workspace" -w /workspace/backend -v pamoja-gomod:/go/pkg/mod $(GO_IMAGE)
LINT_IMAGE ?= golangci/golangci-lint:v2.15.0
DEV_COMPOSE := docker compose -f deploy/docker-compose.dev.yml

.PHONY: help setup dev dev-down logs migrate test coverage lint fmt scan build rbac-doc audit-verify openapi-lint clean

help: ## Show the available targets
	@grep -E '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-16s\033[0m %s\n", $$1, $$2}'

setup: ## Download Go modules and Flutter packages (Flutter SDK required locally)
	$(GO) go mod download
	cd app && flutter pub get

dev: ## Start the local development stack (PostgreSQL, migrations, API, worker)
	$(DEV_COMPOSE) up --build

dev-down: ## Stop the local development stack
	$(DEV_COMPOSE) down

logs: ## Follow API and worker logs
	$(DEV_COMPOSE) logs -f api worker

migrate: ## Apply pending migrations against the local stack
	$(DEV_COMPOSE) run --rm migrate

test: ## Run all Go tests with the race detector
	$(GO) go test -race ./...

coverage: ## Run Go tests and enforce the per-package coverage gate
	$(GO) sh -ec 'go test -race -coverprofile=/tmp/coverage.out -covermode=atomic ./... && ./scripts/check-coverage.sh'

lint: ## Check formatting, vet and run golangci-lint
	$(GO) sh -ec 'unformatted="$$(gofmt -l .)"; if [ -n "$$unformatted" ]; then echo "gofmt needed:"; echo "$$unformatted"; exit 1; fi'
	$(GO) go vet ./...
	docker run --rm -v "$(CURDIR)/backend:/src" -w /src $(LINT_IMAGE) golangci-lint run

fmt: ## Format Go sources
	$(GO) gofmt -w .

scan: ## Run gosec, govulncheck, gitleaks and trivy
	$(GO) sh -ec 'go install github.com/securego/gosec/v2/cmd/gosec@latest && go install golang.org/x/vuln/cmd/govulncheck@latest && gosec -quiet ./... && govulncheck ./...'
	docker run --rm -v "$(CURDIR):/repo" zricethezav/gitleaks:latest detect --source=/repo --no-banner -v
	docker run --rm -v "$(CURDIR):/repo" aquasec/trivy:latest fs --scanners vuln,secret,misconfig /repo

build: ## Build API and worker binaries
	$(GO) sh -ec 'go build -trimpath -o /tmp/pamoja-api ./cmd/api && go build -trimpath -o /tmp/pamoja-worker ./cmd/worker && echo built'

rbac-doc: ## Regenerate docs/RBAC.md from the RBAC matrix
	$(GO) go run ./tools/rbacdoc -out ../docs/RBAC.md

audit-verify: ## Walk the append-only audit hash chain and report the first break
	$(DEV_COMPOSE) run --rm --no-deps api go run ./cmd/auditverify

openapi-lint: ## Validate the OpenAPI 3.1 specification
	docker run --rm -v "$(CURDIR):/repo" -w /repo node:22-alpine sh -ec 'npx --yes @redocly/cli@latest lint backend/api/openapi.yaml'

clean: ## Remove local build artefacts
	rm -rf backend/bin backend/coverage.out /tmp/pamoja-api /tmp/pamoja-worker
