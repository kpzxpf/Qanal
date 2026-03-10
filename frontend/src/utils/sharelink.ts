const PREFIX = 'QNL:'

// P2PLink: direct TCP connection credentials embedded in a share string.
export interface P2PLink {
  t: 'p'
  w: string // WAN address (public internet); may be empty if unreachable
  l: string // LAN address (local network)
  c: string // auth code (8 uppercase base32 chars)
  k: string // AES-256-GCM key (base64url, 32 bytes)
}

export type ShareLink = P2PLink

export function encodeLink(link: ShareLink): string {
  const b64 = btoa(JSON.stringify(link))
    .replace(/\+/g, '-')
    .replace(/\//g, '_')
    .replace(/=+$/, '')
  return PREFIX + b64
}

export function decodeLink(s: string): ShareLink | null {
  const trimmed = s.trim()
  if (!trimmed.startsWith(PREFIX)) return null
  try {
    const b64 = trimmed.slice(PREFIX.length).replace(/-/g, '+').replace(/_/g, '/')
    const obj = JSON.parse(atob(b64))
    if (obj.t !== 'p') return null
    return obj as ShareLink
  } catch {
    return null
  }
}
