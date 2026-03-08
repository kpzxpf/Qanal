# ── Build stage ────────────────────────────────────────────────────────────────
FROM golang:1.26-alpine AS builder

RUN apk add --no-cache git
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -ldflags="-s -w" -o /relay ./cmd/relay

# ── Runtime stage ──────────────────────────────────────────────────────────────
FROM alpine:3.21

RUN apk add --no-cache ca-certificates wget && \
    adduser -D -H -h /app relay

WORKDIR /app
COPY --from=builder /relay /app/relay

RUN mkdir -p /data && chown relay:relay /data
USER relay

VOLUME /data

ENV QANAL_ADDR=:8080
ENV QANAL_STORAGE=/data
ENV QANAL_TRANSFER_TTL=24h
ENV QANAL_MAX_FILE_SIZE=107374182400
ENV QANAL_MAX_CHUNK_SIZE=536870912

EXPOSE 8080
ENTRYPOINT ["/app/relay"]
