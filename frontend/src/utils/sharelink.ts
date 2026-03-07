const PREFIX = 'QNL:'

// P2P link: direct TCP connection
export interface P2PLink {
  t: 'p'
  w: string // WAN address (may be empty)
  l: string // LAN address
  c: string // auth code
  k: string // AES-256 key
}

// Relay link: via embedded HTTP server
export interface RelayLink {
  t: 'r'
  s: string // server URL
  c: string // transfer code
  k: string // AES-256 key
}

export type ShareLink = P2PLink | RelayLink

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
    if (obj.t !== 'p' && obj.t !== 'r') return null
    return obj as ShareLink
  } catch {
    return null
  }
}
