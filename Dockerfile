FROM docker.io/golang:alpine

# Установка зависимостей
RUN apk add --no-cache ffmpeg tzdata bash

# Создание директорий
RUN mkdir -p /app /data /thumbs /media

# Копирование зависимостей
COPY go.mod go.sum ./
RUN go mod download

# Копирование исходников
COPY . .

# Сборка
RUN go build -ldflags "-w -s -X main.BuildVersion=$(date +%s)" -o /app/photocore ./cmd/photocore

WORKDIR /app
EXPOSE 6550

ENTRYPOINT ["/app/photocore"]
CMD ["-config", "/data/config.yaml"]
