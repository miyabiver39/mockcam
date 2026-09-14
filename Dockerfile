# Stage 1: Build Go Binary
FROM golang:1.23-alpine AS builder
WORKDIR /build

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o mockcam cmd/mockcam/main.go

# Stage 2: Runtime
FROM alpine:3.20

RUN apk add --no-cache ffmpeg tzdata ca-certificates ttf-dejavu fontconfig espeak-ng

WORKDIR /app
COPY --from=builder /build/mockcam /app/mockcam

RUN mkdir -p /config /media
VOLUME ["/config", "/media"]

EXPOSE 8554 8080 3702/udp

ENTRYPOINT ["/app/mockcam"]
