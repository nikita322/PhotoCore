# Build stage
FROM golang:1.22-alpine AS builder

RUN apk add --no-cache git

WORKDIR /build

# Clone from GitHub (замени на свой репозиторий)
ARG GITHUB_REPO=github.com/nikita322/PhotoCore
ARG GITHUB_BRANCH=main

RUN git clone --depth 1 --branch ${GITHUB_BRANCH} https://${GITHUB_REPO}.git .

# Download dependencies
RUN go mod download

# Build binary
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-w -s" -o photocore ./cmd/photocore

# Runtime stage
FROM alpine:3.19

RUN apk add --no-cache \
    ffmpeg \
    ca-certificates \
    tzdata

WORKDIR /app

# Copy binary from builder
COPY --from=builder /build/photocore .

# Create directories
RUN mkdir -p /data /thumbs /media

# Default config (can be overridden by volume mount)
COPY --from=builder /build/config.yaml /app/config.yaml

# Expose port
EXPOSE 6550

ENTRYPOINT ["./photocore"]
