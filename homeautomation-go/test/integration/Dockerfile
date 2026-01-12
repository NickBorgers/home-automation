# Multi-stage build for integration tests
FROM golang:1.24-alpine AS builder

WORKDIR /app

# Copy go mod files
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Build test binary
RUN go test -c -o /integration-tests ./test/integration

# Runtime stage
FROM alpine:latest

RUN apk --no-cache add ca-certificates

WORKDIR /app

# Copy test binary
COPY --from=builder /integration-tests .

# Run tests by default
CMD ["./integration-tests", "-test.v"]
