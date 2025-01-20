FROM golang:1.22-alpine

WORKDIR /app

# Install dependencies
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Build the application
RUN CGO_ENABLED=0 GOOS=linux go build -o /app/main .

# Ensure the binary is executable
RUN chmod +x /app/main

# Run the application
CMD ["/app/main"]
