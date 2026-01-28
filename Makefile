# Makefile for xw - AI Inference on Domestic Chips
#
# This Makefile provides targets for building, testing, and installing
# the xw application and server.

# Build information
VERSION ?= 1.0.0
BUILD_TIME := $(shell date -u '+%Y-%m-%dT%H:%M:%SZ')
GIT_COMMIT := $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")

# Build flags
LDFLAGS := -ldflags "\
	-X main.Version=$(VERSION) \
	-X main.BuildTime=$(BUILD_TIME) \
	-X main.GitCommit=$(GIT_COMMIT) \
	-s -w"

# Directories
BIN_DIR := bin
INSTALL_DIR := /usr/local/bin
SYSTEMD_DIR := /etc/systemd/system
CONFIG_DIR := /etc/xw
DATA_DIR := /opt/xw

# Targets
CLI_TARGET := $(BIN_DIR)/xw

.PHONY: all
all: build

.PHONY: help
help: ## Display this help message
	@echo "XW - AI Inference on Domestic Chips"
	@echo ""
	@echo "Usage: make [target]"
	@echo ""
	@echo "Targets:"
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "  %-15s %s\n", $$1, $$2}'

.PHONY: build
build: ## Build xw binary (includes server)
	@echo "Building xw..."
	@mkdir -p $(BIN_DIR)
	go build $(LDFLAGS) -o $(CLI_TARGET) ./cmd/xw
	@echo "✓ xw binary built: $(CLI_TARGET)"

.PHONY: cli
cli: build ## Alias for build

.PHONY: server
server: build ## Alias for build (server is included in xw binary)

.PHONY: test
test: ## Run tests
	@echo "Running tests..."
	go test -v -race -cover ./...

.PHONY: lint
lint: ## Run linter
	@echo "Running linter..."
	@which golangci-lint > /dev/null || \
		(echo "golangci-lint not found. Install: https://golangci-lint.run/usage/install/" && exit 1)
	golangci-lint run ./...

.PHONY: fmt
fmt: ## Format code
	@echo "Formatting code..."
	go fmt ./...
	gofmt -s -w .

.PHONY: vet
vet: ## Run go vet
	@echo "Running go vet..."
	go vet ./...

.PHONY: clean
clean: ## Clean build artifacts
	@echo "Cleaning build artifacts..."
	rm -rf $(BIN_DIR)
	go clean
	@echo "✓ Clean complete"

.PHONY: install
install: build ## Install binaries and systemd service
	@echo "Installing xw..."
	@if [ "$$(id -u)" -ne 0 ]; then \
		echo "Error: Installation requires root privileges. Please run with sudo."; \
		exit 1; \
	fi
	
	# Install binary
	install -m 755 $(CLI_TARGET) $(INSTALL_DIR)/xw
	@echo "✓ Binary installed to $(INSTALL_DIR)"
	
	# Create xw user and group if they don't exist
	@if ! id -u xw > /dev/null 2>&1; then \
		useradd --system --no-create-home --shell /bin/false xw; \
		echo "✓ Created xw system user"; \
	fi
	
	# Create directories
	install -d -m 755 $(CONFIG_DIR)
	install -d -m 755 -o xw -g xw $(DATA_DIR)
	install -d -m 755 -o xw -g xw $(DATA_DIR)/models
	install -d -m 755 /var/log/xw
	chown xw:xw /var/log/xw
	@echo "✓ Directories created"
	
	# Install systemd service
	install -m 644 systemd/xw-server.service $(SYSTEMD_DIR)/xw-server.service
	systemctl daemon-reload
	@echo "✓ Systemd service installed"
	
	@echo ""
	@echo "Installation complete!"
	@echo "To start the service:"
	@echo "  sudo systemctl start xw-server"
	@echo "  sudo systemctl enable xw-server"

.PHONY: uninstall
uninstall: ## Uninstall binaries and systemd service
	@echo "Uninstalling xw..."
	@if [ "$$(id -u)" -ne 0 ]; then \
		echo "Error: Uninstallation requires root privileges. Please run with sudo."; \
		exit 1; \
	fi
	
	# Stop and disable service
	-systemctl stop xw-server 2>/dev/null
	-systemctl disable xw-server 2>/dev/null
	
	# Remove files
	rm -f $(INSTALL_DIR)/xw
	rm -f $(SYSTEMD_DIR)/xw-server.service
	systemctl daemon-reload
	
	@echo "✓ Uninstallation complete"
	@echo ""
	@echo "Note: User data in $(DATA_DIR) and /var/log/xw was preserved."
	@echo "To remove data: sudo rm -rf $(DATA_DIR) /var/log/xw"
	@echo "To remove user: sudo userdel xw"

.PHONY: start
start: ## Start the xw service
	@echo "Starting xw service..."
	sudo systemctl start xw-server
	@echo "✓ Service started"
	@echo "Status:"
	@systemctl status xw-server --no-pager

.PHONY: stop
stop: ## Stop the xw service
	@echo "Stopping xw service..."
	sudo systemctl stop xw-server
	@echo "✓ Service stopped"

.PHONY: restart
restart: ## Restart the xw service
	@echo "Restarting xw service..."
	sudo systemctl restart xw-server
	@echo "✓ Service restarted"

.PHONY: status
status: ## Check xw service status
	@systemctl status xw-server --no-pager

.PHONY: logs
logs: ## Show xw service logs
	@journalctl -u xw-server -f

.PHONY: dev
dev: build ## Run server in development mode
	@echo "Starting xw server in development mode..."
	@echo "Press Ctrl+C to stop"
	$(CLI_TARGET) serve -v

.PHONY: deps
deps: ## Download and verify dependencies
	@echo "Downloading dependencies..."
	go mod download
	go mod verify
	@echo "✓ Dependencies ready"

.PHONY: update-deps
update-deps: ## Update dependencies
	@echo "Updating dependencies..."
	go get -u ./...
	go mod tidy
	@echo "✓ Dependencies updated"

.PHONY: release
release: clean ## Build release binaries for multiple platforms
	@echo "Building release binaries..."
	@mkdir -p $(BIN_DIR)/release
	
	# Linux amd64
	GOOS=linux GOARCH=amd64 go build $(LDFLAGS) -o $(BIN_DIR)/release/xw-linux-amd64 ./cmd/xw
	
	# Linux arm64
	GOOS=linux GOARCH=arm64 go build $(LDFLAGS) -o $(BIN_DIR)/release/xw-linux-arm64 ./cmd/xw
	
	# Linux loong64 (for Loongson)
	GOOS=linux GOARCH=loong64 go build $(LDFLAGS) -o $(BIN_DIR)/release/xw-linux-loong64 ./cmd/xw
	
	@echo "✓ Release binaries built in $(BIN_DIR)/release/"
	@ls -lh $(BIN_DIR)/release/

.PHONY: check
check: fmt vet lint test ## Run all checks (fmt, vet, lint, test)

.DEFAULT_GOAL := help

