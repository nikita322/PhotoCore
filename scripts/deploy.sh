#!/bin/bash
set -euo pipefail

# Быстрый деплой PhotoCore на сервере
# Требования: podman, git
# Работает из project/scripts/deploy.sh или project/src/scripts/deploy.sh

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_DIR="$(dirname "$SCRIPT_DIR")"

# Если скрипт находится в src/scripts/, поднимаемся на уровень выше
if [ "$(basename "$PROJECT_DIR")" = "src" ]; then
    PROJECT_DIR="$(dirname "$PROJECT_DIR")"
fi

SRC_DIR="$PROJECT_DIR/src"

# Цвета
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
RED='\033[0;31m'
NC='\033[0m' # No Color

log() {
    echo -e "${GREEN}[deploy]${NC} $1"
}

warn() {
    echo -e "${YELLOW}[deploy]${NC} $1"
}

err() {
    echo -e "${RED}[deploy]${NC} $1"
}

# Проверка окружения
if [ ! -d "$SRC_DIR" ]; then
    err "Directory $SRC_DIR not found. Are you in the right place?"
    exit 1
fi

cd "$SRC_DIR"

# 1. Git pull
log "Pulling latest changes..."
git pull origin main

# 2. Сборка runtime образа (включает go build + dcraw build)
# Go-модули кэшируются через volume photocore-go-mod-cache
log "Building runtime image..."
podman build \
    -t photocore:latest \
    .

# 3. Перезапуск контейнера
log "Restarting container..."
podman stop photocore 2>/dev/null || true
podman rm photocore 2>/dev/null || true

podman run -d --name photocore \
    -p 6550:6550 \
    -v "$PROJECT_DIR/data":/data \
    -v "$PROJECT_DIR/thumbs":/thumbs \
    -v "$PROJECT_DIR/gallery":/media:ro \
    -v "$PROJECT_DIR/config.yaml":/data/config.yaml:ro \
    -v /etc/localtime:/etc/localtime:ro \
    -v /etc/timezone:/etc/timezone:ro \
    photocore:latest

log "Done! Container started:"
podman ps | grep photocore
