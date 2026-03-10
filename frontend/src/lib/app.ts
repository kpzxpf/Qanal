// Unified application bindings.
// In Wails (desktop): delegates to auto-generated wailsjs/ bindings.
// In browser/Docker mode: throws for P2P operations (not supported in web).
import { isWails } from './mode'

import {
  SelectFile      as _WSelectFile,
  SelectDirectory as _WSelectDirectory,
  GetFileInfo     as _WGetFileInfo,
  StartPeerSend   as _WStartPeerSend,
  StopPeerSend    as _WStopPeerSend,
  PeerReceive     as _WPeerReceive,
} from '../../wailsjs/go/main/App'
import type { transfer } from '../../wailsjs/go/models'

// ── Web-mode file registry ────────────────────────────────────────────────────
const webFiles = new Map<string, File>()

export function storeWebFile(file: File): string {
  const id = `web:${file.name}:${file.lastModified}:${file.size}`
  webFiles.set(id, file)
  return id
}

// ── Public API ────────────────────────────────────────────────────────────────

export async function SelectFile(): Promise<string> {
  if (isWails) return _WSelectFile()
  return new Promise(resolve => {
    const input = document.createElement('input')
    input.type = 'file'
    input.onchange = () => {
      const file = input.files?.[0]
      resolve(file ? storeWebFile(file) : '')
    }
    input.click()
  })
}

export async function SelectDirectory(): Promise<string> {
  if (isWails) return _WSelectDirectory()
  return ''
}

export async function GetFileInfo(path: string): Promise<transfer.FileInfo> {
  if (isWails) return _WGetFileInfo(path)
  const file = webFiles.get(path)
  if (!file) throw new Error('file not found in web registry')
  return { name: file.name, size: file.size } as transfer.FileInfo
}

export async function StartPeerSend(
  filePath: string,
  chunkMB: number,
): Promise<transfer.PeerInfo> {
  if (isWails) return _WStartPeerSend(filePath, chunkMB)
  throw new Error('P2P не поддерживается в веб-режиме')
}

export async function StopPeerSend(): Promise<void> {
  if (isWails) return _WStopPeerSend()
}

export async function PeerReceive(
  peerAddr: string,
  code: string,
  keyB64: string,
  outputDir: string,
): Promise<string> {
  if (isWails) return _WPeerReceive(peerAddr, code, keyB64, outputDir)
  throw new Error('P2P не поддерживается в веб-режиме')
}
