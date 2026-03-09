// HTTP-based file transfer for browser mode (no Wails).
// Mirrors the logic of Go's transfer.Send / transfer.Receive.
import { encryptChunk, decryptChunk, toBase64RawUrl, fromBase64RawUrl } from './crypto'

export interface ProgressEvent {
  done: number
  total: number
  bytesDone: number
  totalBytes: number
  speedBps: number
}
type ProgressFn = (e: ProgressEvent) => void

export interface SendResult { code: string; key: string }

// ── Send ──────────────────────────────────────────────────────────────────────

export async function webSendFile(
  serverURL: string,
  file: File,
  chunkMB: number,
  workers: number,
  onProgress: ProgressFn,
): Promise<SendResult> {
  const chunkSize   = chunkMB * 1024 * 1024
  const totalChunks = Math.max(1, Math.ceil(file.size / chunkSize))

  const keyBytes = crypto.getRandomValues(new Uint8Array(32))
  const keyB64   = toBase64RawUrl(keyBytes)

  // Create transfer on the relay.
  const initResp = await fetch(`${serverURL}/api/v1/transfers`, {
    method:  'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({
      fileName:    file.name,
      fileSize:    file.size,
      totalChunks,
      chunkSize,
    }),
  })
  if (!initResp.ok) throw new Error(`server error ${initResp.status}`)
  const { code } = await initResp.json() as { code: string }

  // Upload chunks with a fixed-concurrency worker pool.
  let nextIdx   = 0
  let done      = 0
  let bytesDone = 0
  const startTime = Date.now()
  let abortErr: Error | null = null

  async function runWorker(): Promise<void> {
    while (!abortErr) {
      const i = nextIdx++
      if (i >= totalChunks) return

      const start = i * chunkSize
      const end   = Math.min(start + chunkSize, file.size)
      const plain = new Uint8Array(await file.slice(start, end).arrayBuffer())

      if (abortErr) return
      const enc = await encryptChunk(keyBytes, i, plain)

      // enc.buffer is a plain ArrayBuffer from Uint8Array constructor — valid BodyInit.
      const putResp = await fetch(
        `${serverURL}/api/v1/transfers/${code}/chunks/${i}`,
        { method: 'PUT', headers: { 'Content-Type': 'application/octet-stream' }, body: enc.buffer as ArrayBuffer },
      )
      if (!putResp.ok) {
        abortErr = new Error(`upload chunk ${i}: HTTP ${putResp.status}`)
        return
      }

      done++
      bytesDone += end - start
      const elapsed = (Date.now() - startTime) / 1000
      onProgress({
        done, total: totalChunks, bytesDone, totalBytes: file.size,
        speedBps: elapsed > 0 ? bytesDone / elapsed : 0,
      })
    }
  }

  await Promise.all(Array.from({ length: workers }, runWorker))
  if (abortErr) throw abortErr

  const completeResp = await fetch(`${serverURL}/api/v1/transfers/${code}/complete`, {
    method: 'POST', headers: { 'Content-Type': 'application/json' },
  })
  if (!completeResp.ok) throw new Error(`complete: HTTP ${completeResp.status}`)

  return { code, key: keyB64 }
}

// ── Receive ───────────────────────────────────────────────────────────────────

export async function webReceiveFile(
  serverURL: string,
  code: string,
  keyB64: string,
  workers: number,
  onProgress: ProgressFn,
): Promise<string> {
  const keyBytes = fromBase64RawUrl(keyB64)

  const infoResp = await fetch(`${serverURL}/api/v1/transfers/${code}`)
  if (!infoResp.ok) throw new Error(`transfer not found: HTTP ${infoResp.status}`)
  const info = await infoResp.json() as {
    fileName: string; fileSize: number; totalChunks: number; chunkSize: number
  }

  const chunks: ArrayBuffer[] = new Array(info.totalChunks)
  let nextIdx   = 0
  let done      = 0
  let bytesDone = 0
  const startTime = Date.now()
  let abortErr: Error | null = null

  async function runWorker(): Promise<void> {
    while (!abortErr) {
      const i = nextIdx++
      if (i >= info.totalChunks) return

      const resp = await fetch(`${serverURL}/api/v1/transfers/${code}/chunks/${i}`)
      if (!resp.ok) {
        abortErr = new Error(`download chunk ${i}: HTTP ${resp.status}`)
        return
      }
      const enc   = new Uint8Array(await resp.arrayBuffer())
      const plain = await decryptChunk(keyBytes, i, enc)
      // Store as ArrayBuffer — Blob accepts ArrayBuffer[] without type issues.
      chunks[i] = plain.buffer.slice(plain.byteOffset, plain.byteOffset + plain.byteLength) as ArrayBuffer

      done++
      bytesDone += plain.length
      const elapsed = (Date.now() - startTime) / 1000
      onProgress({
        done, total: info.totalChunks, bytesDone, totalBytes: info.fileSize,
        speedBps: elapsed > 0 ? bytesDone / elapsed : 0,
      })
    }
  }

  await Promise.all(Array.from({ length: workers }, runWorker))
  if (abortErr) throw abortErr

  // Trigger browser download.
  const blob = new Blob(chunks)
  const url  = URL.createObjectURL(blob)
  const a    = document.createElement('a')
  a.href     = url
  a.download = info.fileName
  document.body.appendChild(a)
  a.click()
  setTimeout(() => { URL.revokeObjectURL(url); document.body.removeChild(a) }, 2000)

  return info.fileName
}
