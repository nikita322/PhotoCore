FROM docker.io/golang:alpine

# Install runtime dependencies
RUN apk add --no-cache \
    ffmpeg \
    tzdata \
    bash

# Install air for hot reload
RUN go install github.com/air-verse/air@latest

# Create directories
RUN mkdir -p /app /data /thumbs /media /go-cache /src/tmp

# Set Go cache directory (for read-only source mount)
ENV GOCACHE=/go-cache
ENV GOMODCACHE=/go-cache/mod
ENV PATH="/root/go/bin:${PATH}"

# Copy air configuration
COPY .air.docker.toml /src/.air.toml

# Expose port
EXPOSE 6550

# Run with air for automatic rebuild on code changes
# air будет отслеживать /src и автоматически пересобирать при изменениях
WORKDIR /src
ENTRYPOINT ["sh", "-c", "go mod download && air -c /src/.air.toml"]

# === BUILD IMAGE ===
# podman build -t photocore .
#
# === RUN WITH HOT RELOAD ===
# Контейнер автоматически пересобирается при изменении кода!
#
# podman run -d --name photocore \
#   -p 6550:6550 \
#   -v /root/containers/photocore/src:/src \
#   -v /root/containers/photocore/gallery:/media:ro \
#   -v /root/containers/photocore/data:/data \
#   -v /root/containers/photocore/thumbs:/thumbs \
#   photocore
