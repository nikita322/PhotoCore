FROM alpine:latest

# Install Go and runtime dependencies
RUN apk add --no-cache \
    go \
    ffmpeg \
    tzdata

# Create directories
RUN mkdir -p /app /data /thumbs /media /go-cache

# Set Go cache directory (for read-only source mount)
ENV GOCACHE=/go-cache
ENV GOMODCACHE=/go-cache/mod

# Expose port
EXPOSE 6550

# Build and run at startup
# Source code mounted to /src, downloads deps, builds to /app/photocore
ENTRYPOINT ["sh", "-c", "cd /src && go mod download && go build -ldflags='-w -s' -o /app/photocore ./cmd/photocore && /app/photocore"]

# Build (один раз, из папки src/):
#   podman build -t photocore .
#
# Run:
#   podman run -d --name photocore \
#     -p 6550:6550 \
#     -v /root/containers/photocore/src:/src:ro \
#     -v /root/containers/photocore/gallery:/media:ro \
#     -v /root/containers/photocore/data:/data \
#     -v /root/containers/photocore/thumbs:/thumbs \
#     photocore
#
# Update code:
#   1. Copy new sources to /root/containers/photocore/src/
#   2. podman restart photocore
