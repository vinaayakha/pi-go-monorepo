.PHONY: all setup build build-lib build-test build-autotest test test-lib autotest vet fmt tidy run clean check-secrets help

LIB_DIR        := pi-go
TEST_DIR       := pi-go-test
BIN_DIR        := bin
TEST_BIN       := $(BIN_DIR)/pi-go-test
AUTOTEST_BIN   := $(BIN_DIR)/pi-go-autotest

all: build test

help:
	@echo "Targets:"
	@echo "  setup            Install git hooks (run once per clone)"
	@echo "  check-secrets    Scan working tree for secrets"
	@echo "  build            Build library + test harness binary ($(TEST_BIN))"
	@echo "  build-lib        go build ./... in $(LIB_DIR)"
	@echo "  build-test       Build test harness into $(TEST_BIN)"
	@echo "  build-autotest   Build non-interactive test runner into $(AUTOTEST_BIN)"
	@echo "  test             Run all unit tests in $(LIB_DIR)"
	@echo "  autotest         Build and run the automated integration tests"
	@echo "                   Requires: OPENAI_API_KEY, OPENAI_MODEL (default: gpt-4.1-mini)"
	@echo "  vet              go vet both modules"
	@echo "  fmt              gofmt -w both modules"
	@echo "  tidy             go mod tidy both modules"
	@echo "  run              Build and run the interactive REPL harness"
	@echo "  clean            Remove $(BIN_DIR)"

build: build-lib build-test build-autotest

build-lib:
	cd $(LIB_DIR) && go build ./...

$(BIN_DIR):
	mkdir -p $(BIN_DIR)

build-test: | $(BIN_DIR)
	cd $(TEST_DIR) && go build -o ../$(TEST_BIN) .

build-autotest: | $(BIN_DIR)
	cd $(TEST_DIR) && go build -o ../$(AUTOTEST_BIN) ./autotest/

test: test-lib

test-lib:
	cd $(LIB_DIR) && go test ./...

autotest: build-autotest
	cd $(TEST_DIR) && ../$(AUTOTEST_BIN)

vet:
	cd $(LIB_DIR) && go vet ./...
	cd $(TEST_DIR) && go vet ./...

fmt:
	cd $(LIB_DIR) && gofmt -w .
	cd $(TEST_DIR) && gofmt -w .

tidy:
	cd $(LIB_DIR) && go mod tidy
	cd $(TEST_DIR) && go mod tidy

run: build-test
	cd $(TEST_DIR) && ../$(TEST_BIN)

clean:
	rm -rf $(BIN_DIR)

setup:
	./scripts/install-hooks.sh

check-secrets:
	./scripts/check-secrets.sh --all
