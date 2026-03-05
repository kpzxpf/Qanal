# Qanal — Lightning File Transfer

High-performance, end-to-end encrypted file transfer system. Single binary, zero database, Docker-ready.

## Features

- **End-to-end encryption** — AES-256-GCM in the browser (server never sees plaintext)
- **Parallel chunked transfer** — up to 16 concurrent workers per transfer
- **Streaming I/O** — files never fully loaded into RAM on server
- **File System Access API** — browser writes directly to disk (no RAM limit for downloads)
- **Resume-ready** — track uploaded chunks, re-upload failed ones
- **Real-time progress** — WebSocket-based live updates
- **Zero dependencies** — single binary, no DB, no Redis
- **Docker ready** — one command to deploy

## Quick Start

### Option 1: Go binary

```bash
go run .
# open http://localhost:8080
```

### Option 2: Docker

```bash
docker-compose up -d
# open http://localhost:8080
```

### Option 3: Build binary

```bash
go build -ldflags="-s -w" -o qanal .
./qanal
```

## Configuration

All config via environment variables:

| Variable | Default | Description |
|---|---|---|
| `QANAL_ADDR` | `:8080` | Listen address |
| `QANAL_STORAGE` | `./data` | Storage directory |
| `QANAL_MAX_FILE_SIZE` | `100GB` | Max file size |
| `QANAL_MAX_CHUNK_SIZE` | `500MB` | Max chunk size |
| `QANAL_TRANSFER_TTL` | `24h` | Transfer expiry |

## How It Works

1. **Sender** drops a file → browser generates AES-256-GCM key
2. Browser splits file into chunks and encrypts each one in parallel
3. Encrypted chunks are uploaded to server in parallel (4-16 workers)
4. Server receives a **transfer code** + encryption key to share with recipient
5. **Recipient** enters code + key → browser downloads chunks in parallel
6. Browser decrypts each chunk and writes directly to disk via File System Access API
7. Transfer expires in 24h and is auto-deleted

## Architecture

```
Clean Architecture:
  Domain    → entity.go, ports.go
  UseCase   → service.go
  Delivery  → handler.go (HTTP), hub.go (WebSocket)
  Infra     → repo.go (metadata), store.go (chunks)
```

## API

```
POST   /api/v1/transfers                      Create transfer
GET    /api/v1/transfers/:code               Get transfer info
PUT    /api/v1/transfers/:code/chunks/:n     Upload chunk n
GET    /api/v1/transfers/:code/chunks/:n     Download chunk n
POST   /api/v1/transfers/:code/complete      Mark complete
DELETE /api/v1/transfers/:code               Delete transfer
GET    /ws/:code                              WebSocket progress
GET    /                                      Web UI
```
