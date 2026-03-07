# Qanal

High-performance, end-to-end encrypted file transfer desktop app for Windows.
Double-click to run — no installation, no cloud, no accounts.

## Overview

Qanal transfers files directly between two machines on the same network using **AES-256-GCM encryption**. The encryption key never leaves the sender — even the relay server stores only ciphertext it cannot read.

Two transfer modes:

| Mode | How it works | Best for |
|---|---|---|
| **P2P Direct** | Sender opens a TCP port; receiver connects directly | Maximum speed, no server hop |
| **Relay** | Chunks staged on the embedded server; receiver pulls them | Works when direct connection is blocked |

---

## Quick Start

### Send a file

1. Launch `Qanal.exe` on both machines (sender and receiver).
2. On the **sender**: switch to the **Send** tab, pick a file, select **P2P Direct**, click **Start P2P Transfer**.
3. Three credentials appear: **Peer Address**, **Auth Code**, **Encryption Key** — share all three with the receiver (e.g. via chat).
4. On the **receiver**: switch to the **Receive** tab, select **P2P Direct**, paste the three values, click **Receive via P2P**.

### Use Relay mode instead

If P2P is blocked (different subnets, firewall):

1. Both machines must be able to reach the sender's machine on the displayed LAN port (shown in the header as `http://192.168.x.x:8080`).
2. **Sender**: select **Relay**, click **Send via Relay** — wait for upload to finish, then share the **Transfer Code** and **Encryption Key**.
3. **Receiver**: select **Relay**, enter the **Server URL** (sender's LAN address), paste the code and key, click **Download via Relay**.

---

## Build from Source

**Prerequisites:** [Go 1.22+](https://go.dev/dl/), [Wails v2](https://wails.io/docs/gettingstarted/installation), [Node.js 18+](https://nodejs.org/)

```bash
# Install Wails CLI (once)
go install github.com/wailsapp/wails/v2/cmd/wails@latest

# Clone and build
git clone https://github.com/yourname/Qanal.git
cd Qanal
wails build
# Output: build/bin/Qanal.exe
```

Development mode with hot reload:

```bash
wails dev
```

---

## Configuration

The embedded relay server reads environment variables at startup:

| Variable | Default | Description |
|---|---|---|
| `QANAL_ADDR` | `:8080` | Listen address (port is auto-selected if 8080 is taken) |
| `QANAL_STORAGE` | `./data` | Directory for encrypted chunk storage |
| `QANAL_MAX_FILE_SIZE` | `100 GB` | Maximum file size per transfer |
| `QANAL_MAX_CHUNK_SIZE` | `500 MB` | Maximum size of a single chunk |
| `QANAL_TRANSFER_TTL` | `24h` | How long relay transfers are kept before auto-deletion |

---

## How It Works

### P2P Transfer

```
Sender disk → ReadAt (parallel) → AES-256-GCM encrypt → TCP stream → AES-256-GCM decrypt → WriteAt → Receiver disk
```

- Sender opens a TCP listener on a random port.
- An encryption pipeline goroutine pre-encrypts the next chunk while the current one transmits (CPU/network overlap).
- Receiver writes each decrypted chunk directly to the final file offset — no in-memory assembly, no RAM limit.

### Relay Transfer

```
Sender disk → parallel encrypt workers → HTTP PUT chunks → embedded server (ciphertext only)
                                                                         ↓
Receiver disk ← parallel WriteAt ← AES-256-GCM decrypt ← HTTP GET chunks
```

- Up to 16 parallel workers encrypt and upload chunks concurrently.
- Server stores **only ciphertext** — it has no access to the key.
- After the receiver successfully downloads all chunks, the server copy is deleted automatically.
- Remaining transfers expire after 24 h and are purged by a background cleanup goroutine.

### Encryption Details

| Property | Value |
|---|---|
| Cipher | AES-256-GCM (Go stdlib, hardware AES-NI) |
| Key size | 256 bit, generated with `crypto/rand` |
| IV | 8 random bytes + 4-byte chunk index (Big-Endian) |
| Compression | zstd (SpeedFastest) applied before encryption; skipped if not beneficial |

The chunk-index suffix in the IV lets `decryptChunk` detect chunk reordering or substitution before the GCM tag check.

---

## Architecture

```
main.go          Wails entry point, window setup
app.go           Bound methods (SelectFile, SendFile, ReceiveFile, StartPeerSend, …)
                 peerManager — P2P session lifecycle (SRP)

internal/
  domain/        Transfer entity, port interfaces, error sentinels
  usecase/       Business logic (Initiate, UploadChunk, DownloadChunk, CleanupExpired)
  delivery/      HTTP router (chi), WebSocket hub, rate limiter
  infrastructure/FileTransferRepo (JSON meta + write-through cache), FileChunkStore
  transfer/      Send, Receive, PeerServer/PeerReceive, crypto, compress, types
  config/        Env-var config loader

frontend/
  src/
    App.tsx            Tab layout, LAN URL display
    components/
      SendPanel.tsx    Mode selector, file picker, settings, progress, credentials
      ReceivePanel.tsx Mode selector, connection fields, progress, save path
```

Dependency direction: `delivery/transfer` → `usecase` → `domain` ← `infrastructure`

---

## Security

- **Key isolation** — the AES key is generated on the sender, transmitted out-of-band, and never sent to the relay server.
- **Chunk integrity** — each chunk carries a GCM authentication tag; any bit flip or reordering is detected on decryption.
- **Rate limiting** — the relay server limits new transfers to 10 per minute per IP.
- **CORS** — the HTTP server accepts requests only from `localhost` / `127.0.0.1` / `::1` origins (exact hostname match).
- **Filename sanitization** — path traversal characters are stripped from uploaded filenames.
- **Size limits** — chunk size capped at 500 MB by both config and a `LimitReader` guard in the handler.

---

## API Reference

The embedded relay server exposes a REST API on the LAN address shown in the app header.

```
POST   /api/v1/transfers                        Create a new transfer session
GET    /api/v1/transfers/{code}                 Get transfer metadata
PUT    /api/v1/transfers/{code}/chunks/{index}  Upload an encrypted chunk
GET    /api/v1/transfers/{code}/chunks/{index}  Download an encrypted chunk
POST   /api/v1/transfers/{code}/complete        Mark transfer as complete
DELETE /api/v1/transfers/{code}                 Delete transfer and all chunks
GET    /ws/{code}                               WebSocket progress stream
```