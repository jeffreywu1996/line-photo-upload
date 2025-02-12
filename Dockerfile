FROM golang:1.22.1-alpine3.19 AS builder

WORKDIR /app

# Install dependencies
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Build the application
RUN CGO_ENABLED=0 GOOS=linux go build -o main .

# Create final lightweight image
FROM alpine:3.19

WORKDIR /app

# Install curl for healthcheck
RUN apk --no-cache add curl

# Copy the binary from builder
COPY --from=builder /app/main .

# Ensure the binary is executable
RUN chmod +x /app/main

# Run the application
CMD ["/app/main"]
