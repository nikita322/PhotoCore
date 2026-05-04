# syntax=docker/dockerfile:1

# Build stage
FROM docker.io/golang:alpine AS builder

RUN apk add --no-cache bash
WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN go build -ldflags "-w -s -X main.BuildVersion=$(date +%s)" -o photocore ./cmd/photocore

# dcraw builder — собираем dcraw из исходников для RISC-V
FROM docker.io/alpine:latest AS dcraw-builder

RUN apk add --no-cache gcc musl-dev libjpeg-turbo-dev lcms2-dev jasper-dev curl
RUN curl -sL -o /tmp/dcraw.c https://raw.githubusercontent.com/ncruces/dcraw/master/dcraw.c && \
    gcc -o /tmp/dcraw -O3 /tmp/dcraw.c -lm -ljpeg -llcms2 -ljasper && \
    /tmp/dcraw -h >/dev/null 2>&1

# Runtime stage
FROM docker.io/alpine:latest

RUN apk add --no-cache ffmpeg tzdata ca-certificates bash
RUN mkdir -p /app /data /thumbs /media

WORKDIR /app
COPY --from=builder /app/photocore /app/photocore
COPY --from=dcraw-builder /tmp/dcraw /usr/local/bin/dcraw

EXPOSE 6550

ENTRYPOINT ["/app/photocore"]
CMD ["-config", "/data/config.yaml"]
