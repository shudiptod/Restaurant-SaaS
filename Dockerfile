# ==========================================
# Multi-Stage Dockerfile for Back4App Container
# ==========================================

# ------------------------------------------
# Stage 1: Build executable binary
# ------------------------------------------
FROM golang:1.24-alpine AS builder

WORKDIR /app

# Install build essentials and SSL certificates
RUN apk add --no-cache git ca-certificates tzdata

# Copy module dependency definitions first (for efficient Docker caching)
COPY go.mod go.sum ./
RUN go mod download

# Copy application source code
COPY cmd/ ./cmd/
COPY internal/ ./internal/

# Build statically linked Go binary
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o server cmd/server/main.go

# ------------------------------------------
# Stage 2: Minimal runtime image
# ------------------------------------------
FROM alpine:3.21

WORKDIR /app

# Install CA certificates for Neon TLS and timezone data
RUN apk --no-cache add ca-certificates tzdata

# Copy compiled binary from builder
COPY --from=builder /app/server .

# Copy runtime assets, templates, and SQL migration files
COPY templates/ ./templates/
COPY static/ ./static/
COPY docs/ ./docs/

# Default application port (Back4App injects PORT via environment)
ENV PORT=8080
ENV GIN_MODE=release

EXPOSE 8080

# Run the server
CMD ["./server"]
