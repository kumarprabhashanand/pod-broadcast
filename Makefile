GOLANGCI_LINT_VERSION := v2.15.0

.PHONY: build test lint e2e

build:
	go build -o bin/kubectl-broadcast ./cmd/kubectl-broadcast

test:
	go test -race ./...

lint:
	go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION) run ./...

e2e:
	test/e2e/run.sh
