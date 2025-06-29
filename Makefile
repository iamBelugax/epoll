# Binary name for your application
BINARY_NAME := epoll

# Main package directory (where your main.go is located)
MAIN_PACKAGE ?= ./cmd/epoll/main.go

# Go build flags (e.g., -v for verbose output, -ldflags for linker flags)
# -ldflags="-s -w" removes debug information and symbol table, reducing binary size
BUILD_FLAGS := -v -ldflags="-s -w"

# Extra build flags that can be passed via command line (e.g., make build EXTRA_BUILD_FLAGS="-race")
EXTRA_BUILD_FLAGS ?=

# Arguments to pass to the application when running (e.g., make run RUN_ARGS="--port 8080")
RUN_ARGS ?=

# Output directory for cross-compiled binaries
BUILD_DIR := ./dist

# ANSI Color Codes
GREEN := \033[32m
YELLOW := \033[33m
CYAN := \033[36m
RESET := \033[0m

# Emojis
RUN_EMOJI := 🚀
BUILD_EMOJI := ⚙️
CLEAN_EMOJI := 🧹
FORMAT_EMOJI := 📝
SUCCESS_EMOJI := ✅
STAR_EMOJI := ✳️

# --- Targets ---

# Build the application for the host OS/ARCH.
build:
	@echo "$(CYAN)$(BUILD_EMOJI) Building $(BINARY_NAME) for $(shell go env GOOS)/$(shell go env GOARCH)...$(RESET)"
	@GOOS=$(shell go env GOOS) GOARCH=$(shell go env GOARCH) go build $(BUILD_FLAGS) $(EXTRA_BUILD_FLAGS) -o $(BUILD_DIR)/$(BINARY_NAME) $(MAIN_PACKAGE)
	@echo "$(GREEN)$(SUCCESS_EMOJI) Build complete.$(RESET)"

# Run the application (uses the binary built for the host OS/ARCH).
run: build
	@echo "$(CYAN)$(RUN_EMOJI) Running $(BINARY_NAME) $(RUN_ARGS)...$(RESET)"
	@$(BUILD_DIR)/$(BINARY_NAME) $(RUN_ARGS)

build-linux-arm64:
	@echo "$(CYAN)$(BUILD_EMOJI) Building $(BINARY_NAME) for linux/arm64...$(RESET)"
	@mkdir -p $(BUILD_DIR)/linux/arm64
	@GOOS=linux GOARCH=arm64 go build $(BUILD_FLAGS) $(EXTRA_BUILD_FLAGS) -o $(BUILD_DIR)/linux/arm64/$(BINARY_NAME) $(MAIN_PACKAGE)
	@echo "$(GREEN)$(SUCCESS_EMOJI) Build complete for linux/arm64.$(RESET)"

run-linux-arm64: build-linux-arm64
	@echo "$(CYAN)$(RUN_EMOJI) Running $(BINARY_NAME) $(RUN_ARGS)...$(RESET)"
	@$(BUILD_DIR)/linux/arm64/$(BINARY_NAME) $(RUN_ARGS)

# Format Go code
fmt:
	@echo "$(CYAN)$(FORMAT_EMOJI) Formatting Go code...$(RESET)"
	@go fmt ./...
	@echo "$(GREEN)$(SUCCESS_EMOJI) Formatting complete.$(RESET)"

# Clean build artifacts.
clean:
	@echo "$(YELLOW)$(CLEAN_EMOJI) Cleaning build artifacts...$(RESET)"
	@go clean
	@rm -rf $(BUILD_DIR)
	@echo "$(GREEN)$(SUCCESS_EMOJI) Clean complete.$(RESET)"

# Display help message.
help:
	@echo "$(CYAN)$(STAR_EMOJI) Usage:$(RESET)"
	@echo "  $(GREEN)make all$(RESET)                     - Builds and runs the application for the host OS/ARCH"
	@echo "  $(GREEN)make build$(RESET)                   - Builds the application binary for the host OS/ARCH (e.g. make build EXTRA_BUILD_FLAGS='-tags netgo')"
	@echo "  $(GREEN)make run$(RESET)                     - Runs the application (host OS/ARCH binary) (e.g. make run RUN_ARGS='--port 8080 --maxListeners 10')"
	@echo "  $(GREEN)make build-linux-arm64$(RESET)       - Builds for Linux (arm64)"
	@echo "  $(GREEN)make run-linux-arm64$(RESET)         - Builds and runs for Linux (arm64)"
	@echo ""
	@echo "$(CYAN)$(STAR_EMOJI) Development & Maintenance:$(RESET)"
	@echo "  $(GREEN)make fmt$(RESET)                     - Formats Go code"
	@echo "  $(YELLOW)make clean$(RESET)                   - Removes build artifacts"
	@echo "  $(CYAN)make help$(RESET)                    - Displays this help message"

# Phony targets - prevents conflicts with files of the same name.
.PHONY: build run build-linux-arm64 run-linux-arm64 fmt clean help