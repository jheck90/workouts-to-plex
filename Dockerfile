FROM golang:1.25-alpine AS builder

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN go build -o workouts-to-plex ./cmd/

FROM alpine:3.19

RUN apk add --no-cache \
    ffmpeg \
    font-dejavu \
    chromium \
    nss \
    freetype \
    harfbuzz \
    ca-certificates \
    ttf-freefont

WORKDIR /app
COPY --from=builder /app/workouts-to-plex .
COPY internal/generator/template.html /app/template.html

RUN mkdir -p /input /output

ENV INPUT_DIR=/input
ENV OUTPUT_DIR=/output
ENV CONFIG_PATH=/config/workouts.yaml

VOLUME ["/input", "/output"]

CMD ["./workouts-to-plex"]
