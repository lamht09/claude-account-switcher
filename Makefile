GO ?= go
BIN ?= ca
DIST_DIR ?= dist
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS ?= -s -w -X 'main.version=$(VERSION)'

.PHONY: help test build build-env build-linux-amd64 build-linux-arm64 build-macos-amd64 build-macos-arm64 build-windows-amd64 build-windows-arm64 clean release-local

help:
	@echo "Targets:"
	@echo "  make test          - run all tests"
	@echo "  make build         - build local binary"
	@echo "  make build-env     - build all environment binaries"
	@echo "  make build-linux-amd64   - build Linux amd64 binary"
	@echo "  make build-linux-arm64   - build Linux arm64 binary"
	@echo "  make build-macos-amd64   - build macOS amd64 binary"
	@echo "  make build-macos-arm64   - build macOS arm64 binary"
	@echo "  make build-windows-amd64 - build Windows amd64 binary"
	@echo "  make build-windows-arm64 - build Windows arm64 binary"
	@echo "  make release-local - build multi-OS artifacts + SHA256SUMS"
	@echo "  make clean         - remove build artifacts"

test:
	$(GO) test ./...

build:
	$(GO) build -ldflags "$(LDFLAGS)" -o $(BIN) ./cmd/ca

build-env: build-linux-amd64 build-linux-arm64 build-macos-amd64 build-macos-arm64 build-windows-amd64 build-windows-arm64

build-linux-amd64:
	mkdir -p $(DIST_DIR)
	GOOS=linux GOARCH=amd64 $(GO) build -ldflags "$(LDFLAGS)" -o $(DIST_DIR)/ca_linux_amd64 ./cmd/ca

build-linux-arm64:
	mkdir -p $(DIST_DIR)
	GOOS=linux GOARCH=arm64 $(GO) build -ldflags "$(LDFLAGS)" -o $(DIST_DIR)/ca_linux_arm64 ./cmd/ca

build-macos-amd64:
	mkdir -p $(DIST_DIR)
	GOOS=darwin GOARCH=amd64 $(GO) build -ldflags "$(LDFLAGS)" -o $(DIST_DIR)/ca_macos_amd64 ./cmd/ca

build-macos-arm64:
	mkdir -p $(DIST_DIR)
	GOOS=darwin GOARCH=arm64 $(GO) build -ldflags "$(LDFLAGS)" -o $(DIST_DIR)/ca_macos_arm64 ./cmd/ca

build-windows-amd64:
	mkdir -p $(DIST_DIR)
	GOOS=windows GOARCH=amd64 $(GO) build -ldflags "$(LDFLAGS)" -o $(DIST_DIR)/ca_windows_amd64.exe ./cmd/ca

build-windows-arm64:
	mkdir -p $(DIST_DIR)
	GOOS=windows GOARCH=arm64 $(GO) build -ldflags "$(LDFLAGS)" -o $(DIST_DIR)/ca_windows_arm64.exe ./cmd/ca

clean:
	rm -rf $(DIST_DIR) $(BIN)

release-local: clean
	mkdir -p $(DIST_DIR)
	for GOOS in linux darwin windows; do \
		for GOARCH in amd64 arm64; do \
			LABEL="$$GOOS"; \
			if [ "$$GOOS" = "darwin" ]; then LABEL="macos"; fi; \
			EXT=""; \
			if [ "$$GOOS" = "windows" ]; then EXT=".exe"; fi; \
			OUT="$(DIST_DIR)/ca_$${LABEL}_$${GOARCH}$${EXT}"; \
			GOOS=$$GOOS GOARCH=$$GOARCH $(GO) build -ldflags "$(LDFLAGS)" -o "$$OUT" ./cmd/ca; \
		done; \
	done
	cd $(DIST_DIR) && \
	tar -czf ca_linux_amd64.tar.gz ca_linux_amd64 && \
	tar -czf ca_linux_arm64.tar.gz ca_linux_arm64 && \
	tar -czf ca_macos_amd64.tar.gz ca_macos_amd64 && \
	tar -czf ca_macos_arm64.tar.gz ca_macos_arm64 && \
	zip ca_windows_amd64.zip ca_windows_amd64.exe && \
	zip ca_windows_arm64.zip ca_windows_arm64.exe && \
	shasum -a 256 *.tar.gz *.zip > SHA256SUMS
