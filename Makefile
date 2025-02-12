.PHONY: test run build clean

# Default target
all: build

# Build the application
build:
	go build -o bin/line-photo-bot

# Run tests
test:
	go test -v ./...

# Run the application
run:
	go run main.go

# Clean build artifacts
clean:
	rm -rf bin/
	go clean

# Run linter
lint:
	golangci-lint run

# Generate test coverage
coverage:
	go test -coverprofile=coverage.out ./...
	go tool cover -html=coverage.out
