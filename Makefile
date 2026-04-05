BINARY = gode
BUILD_DIR = bin
GO ?= go
PREFIX ?= $(HOME)/.local/bin

.PHONY: build run test clean install

build:
	$(GO) build -o $(BUILD_DIR)/$(BINARY) ./cmd/gode

run: build
	./$(BUILD_DIR)/$(BINARY)

install:
	mkdir -p $(PREFIX)
	$(GO) build -o $(PREFIX)/$(BINARY) ./cmd/gode

test:
	$(GO) test ./internal/... -v

clean:
	rm -rf $(BUILD_DIR)

tidy:
	$(GO) mod tidy
