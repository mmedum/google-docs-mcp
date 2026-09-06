# Common dev tasks. CI runs the same gates from .github/workflows/ci.yml.

GO       ?= go
BIN      ?= ./google-docs-mcp
VERSION  ?= dev
PKG       = github.com/mmedum/google-docs-mcp
LDFLAGS   = -s -w -X $(PKG)/internal/version.Version=$(VERSION)
COVER_MIN ?= 80
GOBIN    := $(shell $(GO) env GOPATH)/bin
# Prefer tools installed with the current Go (go install ...@latest) over distro packages.
export PATH := $(GOBIN):$(PATH)

.PHONY: all
all: check

.PHONY: build
build: ## Build the binary
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags="$(LDFLAGS)" -o $(BIN) ./cmd/google-docs-mcp

.PHONY: install
install: ## go install the binary
	CGO_ENABLED=0 $(GO) install -trimpath -ldflags="$(LDFLAGS)" ./cmd/google-docs-mcp

.PHONY: fmt
fmt: ## Fail if gofmt would change anything
	@out=$$(gofmt -l .); if [ -n "$$out" ]; then echo "gofmt issues:"; echo "$$out"; exit 1; fi

.PHONY: vet
vet: ## go vet, including the integration-tagged tests so they keep compiling
	$(GO) vet ./...
	$(GO) vet -tags=integration ./...
	$(GO) vet -tags=live ./...
	$(GO) vet -tags=evals ./...

.PHONY: lint
lint:
	golangci-lint run

.PHONY: test
test: ## Unit tests with race detector and coverage
	$(GO) test -race -coverpkg=./internal/... -coverprofile=cov.out -covermode=atomic ./...

.PHONY: cover
cover: test ## Enforce the coverage floor on core packages
	@$(GO) run ./scripts/gates coverage cov.out $(COVER_MIN)

.PHONY: bench
bench: ## Benchmarks over the synthetic large document (doctest.Large)
	$(GO) test -run XXX -bench . -benchmem ./internal/doc ./internal/render ./internal/service

.PHONY: vuln
vuln:
	govulncheck ./...

.PHONY: licenses
licenses:
	go-licenses check ./... --allowed_licenses=Apache-2.0,BSD-2-Clause,BSD-3-Clause,MIT,ISC

.PHONY: schemas
schemas: build ## Dump tool schemas
	$(BIN) --dump-schemas > schemas.json

.PHONY: schema-diff
schema-diff: build ## Diff tool schemas against the last tag
	@$(GO) run ./scripts/gates schema-diff $(BIN)

.PHONY: smoke
smoke: build ## Drive the binary over stdio, twice
	@$(GO) run ./scripts/gates smoke $(BIN)

.PHONY: staleness
staleness: build ## Docs must match the code
	@$(GO) run ./scripts/gates staleness $(BIN)

.PHONY: leaks
leaks: ## Nothing identifying may be in the repository
	@$(GO) run ./scripts/gates leaks

.PHONY: pins
pins: ## Every action and every tool it installs is one exact version
	@$(GO) run ./scripts/gates pins

.PHONY: gate-classes
gate-classes: ## The error classes the code emits are the ones it documents
	@$(GO) run ./scripts/gates classes

.PHONY: parity
parity: ## `make check` and CI run the same gates
	@$(GO) run ./scripts/gates parity

.PHONY: check
check: fmt vet lint cover vuln licenses leaks pins gate-classes schema-diff smoke staleness parity ## Everything CI runs

.PHONY: clean
clean:
	$(RM) $(BIN) cov.out schemas.json
