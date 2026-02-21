# syntax=docker/dockerfile:1.7
FROM golang:1.23-alpine AS builder
WORKDIR /src
RUN apk add --no-cache ca-certificates git
COPY go.mod ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go build -mod=mod -o /out/queue-service ./cmd/queue-service

FROM alpine:3.20
WORKDIR /app
RUN apk add --no-cache ca-certificates tzdata curl
COPY --from=builder /out/queue-service /usr/local/bin/queue-service
ENTRYPOINT ["/usr/local/bin/queue-service"]
