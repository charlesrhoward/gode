BINARY = gode
BUILD_DIR = bin
GO = /opt/homebrew/bin/go

.PHONY: build run test clean install

build:
	$(GO) build -o $(BUILD_DIR)/$(BINARY) ./cmd/gode

run: build
	./$(BUILD_DIR)/$(BINARY)

install:
	mkdir -p /Users/tradecraft/.local/bin
	$(GO) build -o /Users/tradecraft/.local/bin/$(BINARY) ./cmd/gode

test:
	$(GO) test ./internal/... -v

clean:
	rm -rf $(BUILD_DIR)

tidy:
	$(GO) mod tidy
