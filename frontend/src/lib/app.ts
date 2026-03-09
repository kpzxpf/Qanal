// Unified application bindings.
// In Wails (desktop): delegates to auto-generated wailsjs/ bindings.
// In browser/Docker mode: implements equivalent behaviour via HTTP + Web Crypto.
import { isWails } from './mode'
import { _webEmit } from './events'
import { webSendFile, webReceiveFile } from './webTransfer'

// ── Static imports from wailsjs (safe: module code never throws at load time) ─
import {
  SelectFile      as _WSelectFile,
  SelectDirectory as _WSelectDirectory,
  GetFileInfo     as _WGetFileInfo,
  GetLocalServerURL as _WGetLocalServerURL,
  SendFile        as _WSendFile,
  ReceiveFile     as _WReceiveFile,
  StartPeerSend   as _WStartPeerSend,
  StopPeerSend    as _WStopPeerSend,
  PeerReceive     as _WPeerReceive,
} from '../../wailsjs/go/main/App'
import type { transfer } from '../../wailsjs/go/models'

// ── Web-mode file registry ────────────────────────────────────────────────────
// File objects are stored by a synthetic "path" string so the rest of the UI
// can pass strings around exactly like in Wails mode.

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
  return '' // browser saves to its default download folder
}

export async function GetFileInfo(path: string): Promise<transfer.FileInfo> {
  if (isWails) return _WGetFileInfo(path)
  const file = webFiles.get(path)
  if (!file) throw new Error('file not found in web registry')
  return { name: file.name, size: file.size } as transfer.FileInfo
}

export async function GetLocalServerURL(): Promise<string> {
  if (isWails) return _WGetLocalServerURL()
  return window.location.origin
}

export async function SendFile(
  serverURL: string,
  filePath: string,
  chunkMB: number,
  workers: number,
): Promise<void> {
  if (isWails) { await _WSendFile(serverURL, filePath, chunkMB, workers); return }
  const file = webFiles.get(filePath)
  if (!file) throw new Error('no file selected')
  const result = await webSendFile(serverURL, file, chunkMB, workers, e =>
    _webEmit('transfer:progress', e),
  )
  _webEmit('transfer:complete', { code: result.code, key: result.key })
}

export async function ReceiveFile(
  serverURL: string,
  code: string,
  keyB64: string,
  _outputDir: string,
  workers: number,
): Promise<string> {
  if (isWails) return _WReceiveFile(serverURL, code, keyB64, _outputDir, workers)
  const fileName = await webReceiveFile(serverURL, code, keyB64, workers, e =>
    _webEmit('transfer:progress', e),
  )
  _webEmit('transfer:complete', { path: fileName })
  return fileName
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
  relayURL: string,
  outputDir: string,
): Promise<string> {
  if (isWails) return _WPeerReceive(peerAddr, code, keyB64, relayURL, outputDir)
  throw new Error('P2P не поддерживается в веб-режиме')
}
