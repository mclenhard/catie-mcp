# Start from a Go base image
FROM golang:1.21-alpine AS builder

# Set working directory
WORKDIR /app

# Copy go.mod and go.sum files
COPY go.mod ./
# COPY go.sum ./ # Uncomment if you have a go.sum file

# Download dependencies
RUN go mod download

# Copy the source code
COPY . .

# Build the application
RUN CGO_ENABLED=0 GOOS=linux go build -o mcp-router-proxy ./cmd/main.go

# Use a minimal alpine image for the final container
FROM alpine:latest

# Install ca-certificates for HTTPS requests
RUN apk --no-cache add ca-certificates

WORKDIR /root/

# Copy the binary from the builder stage
COPY --from=builder /app/mcp-router-proxy .
# Copy the configuration file
COPY router_config.yaml .

# Expose the port the app runs on
EXPOSE 80

# Command to run the executable
CMD ["./mcp-router-proxy"]