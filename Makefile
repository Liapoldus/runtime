GOLANGCI_VERSION := v2.12.2
GOLANGCI := .tools/$(GOLANGCI_VERSION)/golangci-lint
export GOWORK := off
export GOFLAGS := -p=1
.PHONY: check lint generate check-generated check-race
check: check-generated lint
	go test ./... -count=1
	npm test -- --maxWorkers=1
	go vet ./...
	go build ./...
lint: $(GOLANGCI)
	$(GOLANGCI) version | grep 'version 2.12.2'
	$(GOLANGCI) config verify --config .golangci.yml
	$(GOLANGCI) run --allow-serial-runners --config .golangci.yml ./cmd/... ./contracts/... ./internal/... ./tests/...
	npm run lint
	npm run typecheck
$(GOLANGCI):
	GOBIN="$(CURDIR)/.tools/$(GOLANGCI_VERSION)" go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_VERSION)
generate:
	go run ./cmd/contracts
check-generated:
	go run ./cmd/contracts -check
check-race:
	go test -race ./... -count=1
	GOFLAGS="-p=1 -race" npm test -- --maxWorkers=1
