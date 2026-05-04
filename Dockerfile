# syntax=docker/dockerfile:1

# Build stage
FROM docker.io/golang:alpine AS builder

RUN apk add --no-cache bash
WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN go build -ldflags "-w -s -X main.BuildVersion=$(date +%s)" -o photocore ./cmd/photocore

# Runtime stage
FROM docker.io/alpine:latest

RUN apk add --no-cache ffmpeg tzdata dcraw ca-certificates bash

RUN mkdir -p /app /data /thumbs /media

WORKDIR /app
COPY --from=builder /app/photocore /app/photocore

EXPOSE 6550

ENTRYPOINT ["/app/photocore"]
CMD ["-config", "/data/config.yaml"]
