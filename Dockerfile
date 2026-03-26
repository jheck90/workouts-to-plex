FROM golang:1.22-alpine AS builder

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN go build -o workouts-to-plex ./cmd/main.go

FROM alpine:3.19

RUN apk add --no-cache ffmpeg font-dejavu

WORKDIR /app
COPY --from=builder /app/workouts-to-plex .

RUN mkdir -p /input /output

ENV INPUT_DIR=/input
ENV OUTPUT_DIR=/output
ENV TIMER_SECONDS=60

VOLUME ["/input", "/output"]

CMD ["./workouts-to-plex"]
