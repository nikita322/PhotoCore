# PhotoCore - Build & Deploy Guide

## 🐳 Сборка и запуск через Podman

Все сборки выполняются **напрямую на сервере** (Orange Pi RV2 внутри домашней сети).

### Подключение к серверу

```bash
plink -ssh root@192.168.1.2 -P 22 -pw "orangepi"
```

### Структура на сервере

```
/opt/containers/photocore/
├── config.yaml          # Конфиг приложения
├── data/                # БД и логи (монтируется в /data)
│   └── logs/
├── thumbs/              # Кэш превью (монтируется в /thumbs)
├── gallery -> /mnt/kingston/gallery  # Симлинк на фото-архив
└── src/                 # Git-репозиторий PhotoCore
    ├── scripts/
    │   └── deploy.sh    # Скрипт быстрого деплоя
    ├── Dockerfile
    ├── go.mod
    └── ...
```

---

## 🚀 Быстрый деплой (рекомендуется)

Деплой через `deploy.sh` — быстрее полной пересборки образа, т.к.:
- Go-модули кэшируются в volume `photocore-go-mod-cache`
- Runtime-образ собирается только из готового бинарника (~5 сек)

```bash
cd /opt/containers/photocore/src
./scripts/deploy.sh
```

Скрипт выполняет:
1. `git pull origin main`
2. `go build` в контейнере `golang:alpine` с кэшем модулей
3. Копирование бинарника в `/opt/containers/photocore/photocore`
4. Сборка runtime-образа на `alpine:latest`
5. Остановка старого контейнера и запуск нового с правильными volume

### Ручной деплой (если скрипт не подходит)

```bash
cd /opt/containers/photocore/src
git pull origin main

# Сборка бинарника с кэшем модулей
podman run --rm \
  -v "$(pwd)":/src \
  -v photocore-go-mod-cache:/go/pkg/mod \
  -w /src \
  docker.io/golang:alpine \
  sh -c 'go build -ldflags "-w -s -X main.BuildVersion=$(date +%s)" -o photocore ./cmd/photocore'

# Сборка runtime образа
cp photocore /opt/containers/photocore/
cd /opt/containers/photocore
podman build -t photocore:latest -f - . <<'EOF'
FROM docker.io/alpine:latest
RUN apk add --no-cache ffmpeg tzdata ca-certificates bash
RUN mkdir -p /app /data /thumbs /media
WORKDIR /app
COPY photocore /app/photocore
EXPOSE 6550
ENTRYPOINT ["/app/photocore"]
CMD ["-config", "/data/config.yaml"]
EOF

# Перезапуск
podman stop photocore 2>/dev/null || true
podman rm photocore 2>/dev/null || true

podman run -d --name photocore \
  -p 6550:6550 \
  -v /opt/containers/photocore/data:/data \
  -v /opt/containers/photocore/thumbs:/thumbs \
  -v /opt/containers/photocore/gallery:/media:ro \
  -v /opt/containers/photocore/config.yaml:/data/config.yaml:ro \
  -v /etc/localtime:/etc/localtime:ro \
  -v /etc/timezone:/etc/timezone:ro \
  photocore:latest
```

---

## 🏗️ Полная сборка образа (если нужно с нуля)

```bash
cd /opt/containers/photocore/src
podman build -t photocore .
```

### Запуск

```bash
podman run -d --name photocore \
  -p 6550:6550 \
  -v /opt/containers/photocore/data:/data \
  -v /opt/containers/photocore/thumbs:/thumbs \
  -v /opt/containers/photocore/gallery:/media:ro \
  -v /opt/containers/photocore/config.yaml:/data/config.yaml:ro \
  -v /etc/localtime:/etc/localtime:ro \
  -v /etc/timezone:/etc/timezone:ro \
  photocore
```

### Остановка и перезапуск

```bash
podman stop photocore
podman start photocore
podman rm -f photocore
```

---

## 🔄 Development (hot-reload)

Для разработки на сервере можно запустить с подмонтированными исходниками:

```bash
podman run -d --name photocore-dev \
  -p 6550:6550 \
  -v $(pwd):/src \
  -w /src \
  docker.io/golang:alpine \
  sh -c "apk add ffmpeg bash && go install github.com/air-verse/air@latest && air -c .air.docker.toml"
```

Или проще — установить Go и Air прямо на сервере и запускать `go run ./cmd/photocore`.

---

## 🖥️ Сервер: Orange Pi RV2 (RISC-V)

- **SoC**: SpacemiT K1 (Ky X1), 8 ядер RISC-V @ 1.6 GHz
- **RAM**: 8 GB
- **Хранилище**: 512 GB NVMe SSD
- **OS**: Ubuntu 24.04 (Noble), kernel `6.6.63-ky`
- **IP**: `192.168.1.2` (SSH: `root` / `orangepi`)
- **Контейнеры**: Podman (rootless / rootful)

### Особенности RISC-V

- Некоторые пакеты Alpine недоступны для `riscv64` (например, `dcraw`). В runtime-образе используется `ffmpeg`, `tzdata`, `ca-certificates`, `bash`.
- Для RAW-обработки в будущем можно использовать `libraw-tools` (доступен в Alpine для riscv64) вместо `dcraw`.
