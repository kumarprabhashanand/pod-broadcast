GOLANGCI_LINT_VERSION := v2.15.0
CONTROLLER_GEN := go run sigs.k8s.io/controller-tools/cmd/controller-gen@v0.23.0

.PHONY: build test lint generate e2e

build:
	go build -o bin/kubectl-broadcast ./cmd/kubectl-broadcast

test:
	go test -race ./...

generate:
	$(CONTROLLER_GEN) object paths=./api/...
	$(CONTROLLER_GEN) crd paths=./api/... output:crd:dir=deploy/crds

lint:
	go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION) run ./...

e2e:
	test/e2e/run.sh
