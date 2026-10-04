GOLANGCI_LINT := go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.14.0
GOVULNCHECK   := go run golang.org/x/vuln/cmd/govulncheck@latest
ACTIONLINT    := go run github.com/rhysd/actionlint/cmd/actionlint@latest

.DEFAULT_GOAL := help
.PHONY: help test cover lint fmt generate tidy vuln actionlint check-generate check-tidy check

help: ## List the targets
	@awk 'BEGIN {FS = ":.*## "} /^[a-z-]+:.*## / {printf "  %-16s %s\n", $$1, $$2}' $(MAKEFILE_LIST)

test: ## Run the tests with the race detector (needs Docker)
	go test -race -count=2 ./...

cover: ## Run the tests and print coverage (needs Docker)
	go test -race -count=2 -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out | tail -1

lint: ## Run golangci-lint
	$(GOLANGCI_LINT) run ./...

fmt: ## Format the code
	$(GOLANGCI_LINT) fmt ./...

generate: ## Regenerate the committed sqlc code
	go generate ./...

tidy: ## Tidy go.mod and go.sum
	go mod tidy

vuln: ## Check dependencies for known vulnerabilities
	$(GOVULNCHECK) ./...

actionlint: ## Lint the GitHub Actions workflows
	$(ACTIONLINT)

check-generate: ## Fail if the sqlc code is out of date
	go generate ./...
	git diff --exit-code

check-tidy: ## Fail if go.mod or go.sum is not tidy
	go mod tidy
	git diff --exit-code -- go.mod go.sum

check: lint check-tidy check-generate vuln test ## Everything CI runs
