BIN ?= auri-tui
PREFIX ?= $(HOME)/.local

.PHONY: build install test clean
build:
	go build -ldflags "-s -w" -o bin/$(BIN) ./cmd/auri-tui

install: build
	mkdir -p $(PREFIX)/bin
	install -m 0755 bin/$(BIN) $(PREFIX)/bin/$(BIN)
	@echo "Installed to $(PREFIX)/bin/$(BIN) (make sure it's on your PATH)"

test:
	go vet ./...
	go test ./...

clean:
	rm -rf bin
