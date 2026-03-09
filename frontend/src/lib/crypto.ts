// Browser-side AES-256-GCM matching Go's encryptChunk / decryptChunk.
//
// Wire format: IV(12 bytes) | AES-GCM ciphertext of [flag(1) | data]
//   IV  = crypto.getRandomValues(8 bytes) || BigEndian(chunkIndex, 4 bytes)
//   flag 0 = raw data  (browser always sends raw)
//   flag 1 = zstd-compressed data (only possible when sender is the desktop app)

const FLAG_RAW  = 0
const FLAG_ZSTD = 1

// Web Crypto API requires a plain ArrayBuffer (not ArrayBufferLike / SharedArrayBuffer).
// .buffer.slice() always returns a new ArrayBuffer regardless of the view's origin.
function toPlainBuffer(view: Uint8Array): ArrayBuffer {
  return view.buffer.slice(view.byteOffset, view.byteOffset + view.byteLength) as ArrayBuffer
}

async function importKey(raw: Uint8Array, usage: KeyUsage[]): Promise<CryptoKey> {
  return crypto.subtle.importKey('raw', toPlainBuffer(raw), 'AES-GCM', false, usage)
}

export async function encryptChunk(
  keyBytes: Uint8Array,
  chunkIndex: number,
  plain: Uint8Array,
): Promise<Uint8Array> {
  const iv = new Uint8Array(12)
  crypto.getRandomValues(iv.subarray(0, 8))
  new DataView(iv.buffer).setUint32(8, chunkIndex, false) // big-endian

  // payload = [FLAG_RAW] + plain
  const payload = new Uint8Array(1 + plain.length)
  payload[0] = FLAG_RAW
  payload.set(plain, 1)

  const key = await importKey(keyBytes, ['encrypt'])
  const ct  = await crypto.subtle.encrypt({ name: 'AES-GCM', iv }, key, toPlainBuffer(payload))

  const out = new Uint8Array(12 + ct.byteLength)
  out.set(iv)
  out.set(new Uint8Array(ct), 12)
  return out
}

export async function decryptChunk(
  keyBytes: Uint8Array,
  chunkIndex: number,
  enc: Uint8Array,
): Promise<Uint8Array> {
  if (enc.length < 12 + 1 + 16) {
    throw new Error(`encrypted chunk too short: ${enc.length} bytes`)
  }

  // Use slice() — creates copies backed by plain ArrayBuffer (Web Crypto requirement).
  const iv       = enc.slice(0, 12)
  const ciphered = enc.slice(12)

  const storedIdx = new DataView(iv.buffer).getUint32(8, false)
  if (storedIdx !== chunkIndex) {
    throw new Error(`chunk ${chunkIndex}: IV mismatch (stored ${storedIdx})`)
  }

  const key     = await importKey(keyBytes, ['decrypt'])
  const payload = new Uint8Array(
    await crypto.subtle.decrypt({ name: 'AES-GCM', iv }, key, toPlainBuffer(ciphered)),
  )

  const flag = payload[0]
  const data = payload.slice(1)

  if (flag === FLAG_ZSTD) {
    // Lazy-load fzstd — only needed when the sender was the desktop app.
    const fzstd = await import('fzstd')
    return fzstd.decompress(data)
  }
  if (flag !== FLAG_RAW) {
    throw new Error(`unknown compression flag: ${flag}`)
  }
  return data
}

/** Encode Uint8Array as base64 RawURL (no padding, - and _ instead of + and /). */
export function toBase64RawUrl(bytes: Uint8Array): string {
  return btoa(String.fromCharCode(...bytes))
    .replace(/\+/g, '-')
    .replace(/\//g, '_')
    .replace(/=/g, '')
}

/** Decode base64 RawURL string to Uint8Array. */
export function fromBase64RawUrl(s: string): Uint8Array {
  const padded = s.replace(/-/g, '+').replace(/_/g, '/')
  const b = atob(padded + '=='.slice((s.length + 3) % 4))
  return Uint8Array.from(b, c => c.charCodeAt(0))
}
