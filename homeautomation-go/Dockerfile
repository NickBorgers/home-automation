# Build stage
FROM golang:1.24-alpine AS builder

# Install build dependencies
RUN apk add --no-cache git ca-certificates

WORKDIR /build

# Copy go mod files and download dependencies
COPY homeautomation-go/go.mod homeautomation-go/go.sum ./
RUN go mod download

# Copy source code
COPY homeautomation-go/ .

# Build the application
# CGO_ENABLED=0 for static binary (better for Alpine)
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
    -ldflags='-w -s -extldflags "-static"' \
    -o homeautomation \
    ./cmd/main.go

# Runtime stage
FROM alpine:latest

# Install runtime dependencies
RUN apk --no-cache add ca-certificates tzdata

# Create non-root user for security
RUN addgroup -g 1000 homeautomation && \
    adduser -D -u 1000 -G homeautomation homeautomation

WORKDIR /app

# Copy binary from builder
COPY --from=builder /build/homeautomation .

# Copy example env file for reference
COPY --from=builder /build/.env.example .

# Copy config files from repository root
COPY configs ./configs

# Create log directory for persistent logs
RUN mkdir -p /var/log/homeautomation

# Change ownership
RUN chown -R homeautomation:homeautomation /app /var/log/homeautomation

# Switch to non-root user
USER homeautomation

# Expose the HTTP port (default 8080, configurable via HTTP_PORT env var)
EXPOSE 8080

# Health check using the HTTP_PORT env var (defaults to 8080)
# Uses /health endpoint which returns {"status":"ok"}
HEALTHCHECK --interval=30s --timeout=10s --start-period=5s --retries=3 \
    CMD wget --no-verbose --tries=1 --spider http://localhost:${HTTP_PORT:-8080}/health || exit 1

# Run the application
CMD ["./homeautomation"]
