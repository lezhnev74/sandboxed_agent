# Quality gates: see docs/quality-gates.md. This file is guarded by
# tools/gatesum - an edit fails `make check` until a human runs `make gates-bless`.
.DEFAULT_GOAL := help
.PHONY: help tidy fmt check test itest flaky vuln docs gates-bless build install clean \
	gate-tidy gate-gates gate-fmt gate-lint gate-build gate-test gate-cover gate-crap

export GOFLAGS := -mod=readonly

BIN     := bin
BUILD   := .build
APP     := $(BIN)/sandboxed_agent
INSTALL := $(HOME)/.local/bin/sandboxed_agent
COVER   := coverage.out
PKGS    := ./...
# Fixed so a failing order reproduces; `make flaky` hunts with random orders.
SEED    := 20260929
GATES   := tidy gates fmt lint build test cover crap
# Functions measured by nothing: the composition root and the exec files,
# covered by `make itest` instead. Matched against gocyclo's file:line field.
CRAP_IGNORE := ^cmd\/|_exec\.go:

help: ## Show this help
	@grep -hE '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) \
		| awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-12s\033[0m %s\n", $$1, $$2}'

tidy: ## Fix go.mod/go.sum
	GOFLAGS= go mod tidy

fmt: ## Rewrite formatting and imports
	go tool golangci-lint fmt $(PKGS)

# Runs every gate in order and stops at the first failure. Output is the
# failing gate's log only; the last line is `CHECK OK` or `FAIL: <gate>`.
check: ## The only definition of done: gates 1-8, fail fast, ends with CHECK OK
	@mkdir -p $(BUILD)
	@for g in $(GATES); do \
		if ! $(MAKE) --no-print-directory -s gate-$$g > $(BUILD)/$$g.log 2>&1; then \
			cat $(BUILD)/$$g.log; echo "FAIL: $$g"; exit 1; \
		fi; \
	done; \
	echo "nolint directives: $$(grep -rn --include='*.go' '//nolint' . | wc -l)"; \
	echo "CHECK OK"

gate-tidy:
	go mod tidy -diff

gate-gates:
	go run ./tools/gatesum -verify

gate-fmt:
	go tool golangci-lint fmt --diff $(PKGS)

gate-lint:
	go tool golangci-lint run $(PKGS)

gate-build:
	CGO_ENABLED=0 go build $(PKGS)

gate-test:
	CGO_ENABLED=1 go tool gotestsum --format pkgname-and-test-fails -- \
		-race -count=1 -shuffle=$(SEED) -timeout=120s \
		-covermode=atomic -coverprofile=$(COVER) $(PKGS)

gate-cover:
	go tool go-test-coverage --config=.testcoverage.yml

gate-crap:
	@mkdir -p $(BUILD)
	go tool gocyclo -over 0 . | awk '$$4 !~ /$(CRAP_IGNORE)/' > $(BUILD)/gocyclo.txt
	go tool cover -func=$(COVER) > $(BUILD)/coverfunc.txt
	go run ./tools/crap -gocyclo $(BUILD)/gocyclo.txt -coverfunc $(BUILD)/coverfunc.txt -top 5

test: ## Fast inner loop: no race, no coverage (not proof of done)
	go tool gotestsum --format pkgname-and-test-fails -- $(PKGS)

itest: ## Integration tests against real Docker (-tags integration)
	go tool gotestsum --format pkgname-and-test-fails -- -tags integration -count=1 -timeout=30m $(PKGS)

flaky: ## Run the suite 10 times in random order to find order dependence
	@for i in 1 2 3 4 5 6 7 8 9 10; do \
		go test -count=1 -shuffle=on $(PKGS) > $(BUILD)/flaky.log 2>&1 || { cat $(BUILD)/flaky.log; echo "FLAKY on run $$i"; exit 1; }; \
	done; echo "FLAKY OK"

vuln: ## govulncheck (needs the network)
	GOFLAGS= go run golang.org/x/vuln/cmd/govulncheck@v1.8.0 $(PKGS)

# The CLI reference in docs/usage.md is a golden file of the drift test in
# cmd/sandboxed_agent; -update regenerates it (tools/** is guarded, so no generator there).
docs: ## Regenerate the CLI reference in docs/usage.md
	go test ./cmd/sandboxed_agent -run TestUsageDoc -count=1 -update

gates-bless: ## HUMAN ONLY: accept the current gate files into .gates.sha256
	go run ./tools/gatesum -write
	@echo "blessed .gates.sha256 - review its diff"

build: ## Build the static binary to bin/sandboxed_agent
	CGO_ENABLED=0 go build -o $(APP) ./cmd/sandboxed_agent

install: build ## Install the binary to ~/.local/bin/sandboxed_agent
	install -d $(dir $(INSTALL))
	install -m 0755 $(APP) $(INSTALL)
	@echo "installed $(INSTALL)"

clean: ## Remove build and coverage artefacts
	rm -rf $(BIN) $(BUILD) $(COVER) coverage.html
