# Makefile for cel2filter project

.PHONY: build test bench lint fmt clean help install-tools deps vuln-check

# Build the project
build:
	go build -v ./...

# Run tests
test:
	go test -v -race -coverprofile=coverage.out -covermode=atomic ./...

# Run benchmarks
bench:
	go test -bench=. -benchmem -run=^$$ ./...

# Run benchmarks with comparison output
bench-compare:
	@echo "Running benchmarks and saving to bench-new.txt"
	@go test -bench=. -benchmem -run=^$$ ./... | tee bench-new.txt
	@echo ""
	@echo "Compare with previous run using: benchstat bench-old.txt bench-new.txt"

# Run tests with coverage report
test-coverage: test
	go tool cover -html=coverage.out -o coverage.html
	@echo "Coverage report generated: coverage.html"

# Run linting
lint:
	golangci-lint run

# Format code
fmt:
	go fmt ./...
	goimports -w .

# Clean build artifacts
clean:
	rm -f coverage.out coverage.html
	go clean -cache -testcache -modcache

# Install development tools
install-tools:
	go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest
	go install golang.org/x/tools/cmd/goimports@latest
	go install golang.org/x/vuln/cmd/govulncheck@latest

# Download dependencies
deps:
	go mod download
	go mod verify

# Run vulnerability check
vuln-check:
	govulncheck ./...

# Run all checks (used in CI)
ci: fmt lint test vuln-check

# Update dependencies
update-deps:
	go get -u ./...
	go mod tidy

# Run the example
example:
	go run ./examples/basic

# Generate documentation
docs:
	go doc -all ./...

# Help
help:
	@echo "Available targets:"
	@echo "  build         - Build the project"
	@echo "  test          - Run tests"
	@echo "  bench         - Run benchmarks"
	@echo "  bench-compare - Run benchmarks and save for comparison"
	@echo "  test-coverage - Run tests with coverage report"
	@echo "  lint          - Run linting"
	@echo "  fmt           - Format code"
	@echo "  clean         - Clean build artifacts"
	@echo "  install-tools - Install development tools"
	@echo "  deps          - Download and verify dependencies"
	@echo "  vuln-check    - Run vulnerability check"
	@echo "  ci            - Run all checks (used in CI)"
	@echo "  update-deps   - Update dependencies"
	@echo "  example       - Run the basic example"
	@echo "  docs          - Generate documentation"
	@echo "  help          - Show this help message"
