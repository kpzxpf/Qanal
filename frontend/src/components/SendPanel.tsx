import { useState, useEffect, useCallback, useRef } from 'react'
import QRCode from 'qrcode'
import { EventsOn, EventsOff } from '../../wailsjs/runtime/runtime'
import { SelectFile, GetFileInfo, SendFile, StartPeerSend, StopPeerSend } from '../../wailsjs/go/main/App'
import type { transfer } from '../../wailsjs/go/models'
import { fmt, fmtDur } from '../utils/format'
import { encodeLink } from '../utils/sharelink'

type Mode = 'relay' | 'p2p'
type Phase = 'idle' | 'starting' | 'waiting' | 'transferring' | 'done' | 'error'
interface Progress { done: number; total: number; bytesDone: number; totalBytes: number; speedBps: number }

function fileIcon(name: string) {
  const ext = name.split('.').pop()?.toLowerCase() ?? ''
  if (['jpg', 'jpeg', 'png', 'gif', 'webp', 'svg', 'bmp'].includes(ext)) return '🖼'
  if (['mp4', 'mov', 'avi', 'mkv', 'webm'].includes(ext)) return '🎬'
  if (['mp3', 'flac', 'aac', 'wav', 'ogg'].includes(ext)) return '🎵'
  if (['zip', 'rar', '7z', 'tar', 'gz'].includes(ext)) return '📦'
  if (['pdf'].includes(ext)) return '📑'
  if (['doc', 'docx', 'odt'].includes(ext)) return '📝'
  if (['xls', 'xlsx', 'csv'].includes(ext)) return '📊'
  if (['exe', 'msi', 'apk'].includes(ext)) return '⚙'
  return '📄'
}

function QRImg({ link }: { link: string }) {
  const [src, setSrc] = useState('')
  useEffect(() => {
    if (!link) return
    QRCode.toDataURL(link, { width: 200, margin: 2, color: { dark: '#e4e7f0', light: '#161920' } })
      .then(setSrc).catch(() => {})
  }, [link])
  if (!src) return null
  return <img src={src} alt="QR" className="w-28 h-28 rounded-xl mx-auto border border-[#2a2f42]" />
}

export default function SendPanel({ defaultServerURL }: { defaultServerURL: string }) {
  const [mode, setMode] = useState<Mode>('p2p')
  const [serverURL, setServerURL] = useState(defaultServerURL)
  const [filePath, setFilePath] = useState('')
  const [fileInfo, setFileInfo] = useState<transfer.FileInfo | null>(null)
  const [chunkMB, setChunkMB] = useState(16)
  const [workers, setWorkers] = useState(8)
  const [phase, setPhase] = useState<Phase>('idle')
  const [progress, setProgress] = useState<Progress | null>(null)
  const [peerInfo, setPeerInfo] = useState<transfer.PeerInfo | null>(null)
  const [relayResult, setRelayResult] = useState<{ code: string; key: string } | null>(null)
  const [error, setError] = useState('')
  const [copied, setCopied] = useState(false)
  const [isDragging, setIsDragging] = useState(false)
  const autoCopied = useRef(false)

  useEffect(() => {
    if (serverURL === 'http://localhost:8080' && defaultServerURL !== 'http://localhost:8080')
      setServerURL(defaultServerURL)
  }, [defaultServerURL])

  // Listen for OS file drops (Wails OnFileDrop)
  useEffect(() => {
    return EventsOn('file:dropped', (e: { path: string }) => {
      if (!e?.path) return
      setFilePath(e.path)
      GetFileInfo(e.path).then(setFileInfo).catch(() => {})
      setPhase('idle'); setProgress(null); setPeerInfo(null); setRelayResult(null)
      setError(''); autoCopied.current = false
    })
  }, [])

  // Visual drag feedback via DOM events
  useEffect(() => {
    const onOver = (e: DragEvent) => { if (e.dataTransfer?.types.includes('Files')) setIsDragging(true) }
    const onLeave = () => setIsDragging(false)
    const onDrop = () => setIsDragging(false)
    document.addEventListener('dragover', onOver)
    document.addEventListener('dragleave', onLeave)
    document.addEventListener('drop', onDrop)
    return () => {
      document.removeEventListener('dragover', onOver)
      document.removeEventListener('dragleave', onLeave)
      document.removeEventListener('drop', onDrop)
    }
  }, [])

  const reset = useCallback(() => {
    EventsOff('transfer:progress'); EventsOff('transfer:error'); EventsOff('transfer:complete')
    setPhase('idle'); setProgress(null); setPeerInfo(null); setRelayResult(null)
    setError(''); setCopied(false); autoCopied.current = false
  }, [])

  const pickFile = useCallback(async () => {
    const path = await SelectFile()
    if (!path) return
    setFileInfo(await GetFileInfo(path))
    setFilePath(path); reset()
  }, [reset])

  const copyLink = useCallback((link: string) => {
    navigator.clipboard.writeText(link).catch(() => {})
    setCopied(true)
    setTimeout(() => setCopied(false), 3000)
  }, [])

  const startTransfer = useCallback(async () => {
    if (!fileInfo) return
    setPhase('starting'); setError(''); setProgress(null); autoCopied.current = false

    EventsOn('transfer:progress', (e: Progress) => {
      setPhase(p => (p === 'waiting' || p === 'starting') ? 'transferring' : p)
      setProgress(e)
    })
    EventsOn('transfer:error', (e: { message: string }) => {
      setError(e.message); setPhase('error')
      EventsOff('transfer:progress'); EventsOff('transfer:error'); EventsOff('transfer:complete')
    })
    EventsOn('transfer:complete', (e: { code?: string; key?: string }) => {
      setPhase('done')
      if (e.code) setRelayResult({ code: e.code, key: e.key || '' })
      EventsOff('transfer:progress'); EventsOff('transfer:error'); EventsOff('transfer:complete')
    })

    try {
      if (mode === 'p2p') {
        const info = await StartPeerSend(filePath, chunkMB)
        setPeerInfo(info)
        setPhase('waiting')
        const link = encodeLink({ t: 'p', w: info.wan, l: info.lan, c: info.code, k: info.key })
        if (!autoCopied.current) { autoCopied.current = true; copyLink(link) }
      } else {
        setPhase('transferring')
        await SendFile(serverURL, filePath, chunkMB, workers)
      }
    } catch (e: any) {
      setError(e?.message || String(e)); setPhase('error')
      EventsOff('transfer:progress'); EventsOff('transfer:error'); EventsOff('transfer:complete')
    }
  }, [mode, filePath, fileInfo, serverURL, chunkMB, workers, copyLink])

  const cancel = useCallback(async () => {
    if (mode === 'p2p') await StopPeerSend()
    reset()
  }, [mode, reset])

  const pct = progress ? Math.round((progress.done / progress.total) * 100) : 0

  const p2pLink = peerInfo
    ? encodeLink({ t: 'p', w: peerInfo.wan, l: peerInfo.lan, c: peerInfo.code, k: peerInfo.key })
    : ''
  const relayLink = relayResult
    ? encodeLink({ t: 'r', s: serverURL, c: relayResult.code, k: relayResult.key })
    : ''

  // ── Spinner ────────────────────────────────────────────────────────────────
  if (phase === 'starting') {
    return (
      <div className="max-w-2xl flex flex-col items-center justify-center py-20 gap-4">
        <div className="w-10 h-10 border-2 border-[#5b7cfa]/30 border-t-[#5b7cfa] rounded-full animate-spin" />
        <p className="text-[#8b92a8] text-sm">Подготовка передачи…</p>
      </div>
    )
  }

  // ── P2P waiting / transferring ─────────────────────────────────────────────
  if ((phase === 'waiting' || phase === 'transferring') && mode === 'p2p' && peerInfo) {
    return (
      <div className="max-w-2xl space-y-3">
        <div className="bg-[#161920] border border-[#2a2f42] rounded-2xl p-6 space-y-5">
          {/* Status */}
          <div className="flex items-center justify-center gap-2">
            {phase === 'waiting'
              ? <><span className="w-2 h-2 rounded-full bg-[#fbbf24] animate-pulse" /><span className="text-[#8b92a8] text-sm">Ожидание получателя</span></>
              : <><span className="w-2 h-2 rounded-full bg-[#34d399] animate-pulse" /><span className="text-[#34d399] text-sm font-semibold">Передача идёт</span></>
            }
          </div>

          {/* File info */}
          {fileInfo && (
            <div className="flex items-center gap-3 bg-[#0d0f14] rounded-xl px-4 py-2.5">
              <span className="text-2xl">{fileIcon(fileInfo.name)}</span>
              <div className="flex-1 min-w-0">
                <div className="font-semibold text-sm truncate">{fileInfo.name}</div>
                <div className="text-xs text-[#8b92a8]">{fmt(fileInfo.size)}</div>
              </div>
            </div>
          )}

          {/* Copy button */}
          <button
            onClick={() => copyLink(p2pLink)}
            className={`w-full py-4 rounded-xl font-bold text-base transition-all ${
              copied
                ? 'bg-[#34d399]/20 border-2 border-[#34d399]/50 text-[#34d399]'
                : 'bg-gradient-to-r from-[#5b7cfa] to-[#a78bfa] text-white hover:opacity-90 shadow-lg shadow-[#5b7cfa]/20'
            }`}>
            {copied ? '✓ Скопировано!' : '📋 Скопировать строку для получателя'}
          </button>

          {/* QR code */}
          <details>
            <summary className="text-xs text-[#4a5068] cursor-pointer hover:text-[#8b92a8] select-none list-none flex items-center justify-center gap-1">
              <span>📱 Показать QR-код</span>
            </summary>
            <div className="mt-3">
              <QRImg link={p2pLink} />
              <p className="text-xs text-[#4a5068] text-center mt-2">Отсканируйте для копирования строки</p>
            </div>
          </details>
        </div>

        {/* Progress */}
        {phase === 'transferring' && progress && <ProgressCard progress={progress} pct={pct} />}

        {/* Connection details */}
        <details className="group">
          <summary className="text-xs text-[#4a5068] cursor-pointer hover:text-[#8b92a8] select-none list-none flex items-center gap-1 px-1">
            <span className="group-open:rotate-90 transition-transform inline-block">▶</span>
            Детали подключения
          </summary>
          <div className="mt-2 bg-[#161920] border border-[#2a2f42] rounded-xl p-4 space-y-2">
            {peerInfo.wan && <Row label="🌍 Интернет" value={peerInfo.wan} color="text-[#34d399]" />}
            <Row label="📡 Локально" value={peerInfo.lan} color="text-[#5b7cfa]" />
            <Row label="Код" value={peerInfo.code} color="text-[#5b7cfa]" />
          </div>
        </details>

        <button onClick={cancel}
          className="w-full py-2.5 rounded-xl text-sm text-[#8b92a8] border border-[#2a2f42] hover:border-red-500/40 hover:text-red-400 transition-all">
          Отменить
        </button>
      </div>
    )
  }

  // ── Relay transferring ─────────────────────────────────────────────────────
  if (phase === 'transferring' && mode === 'relay') {
    return (
      <div className="max-w-2xl space-y-3">
        {fileInfo && (
          <div className="flex items-center gap-3 bg-[#161920] border border-[#2a2f42] rounded-xl px-4 py-3">
            <span className="text-2xl">{fileIcon(fileInfo.name)}</span>
            <div className="flex-1 min-w-0">
              <div className="font-semibold text-sm truncate">{fileInfo.name}</div>
              <div className="text-xs text-[#8b92a8]">{fmt(fileInfo.size)}</div>
            </div>
            <div className="flex items-center gap-2">
              <span className="w-2 h-2 rounded-full bg-[#5b7cfa] animate-pulse" />
              <span className="text-xs text-[#8b92a8]">Загрузка…</span>
            </div>
          </div>
        )}
        {progress && <ProgressCard progress={progress} pct={pct} />}
        <button onClick={cancel}
          className="w-full py-2.5 rounded-xl text-sm text-[#8b92a8] border border-[#2a2f42] hover:border-red-500/40 hover:text-red-400 transition-all">
          Отменить
        </button>
      </div>
    )
  }

  // ── Done P2P ───────────────────────────────────────────────────────────────
  if (phase === 'done' && mode === 'p2p') {
    return (
      <div className="max-w-2xl space-y-3">
        <div className="bg-[#161920] border border-[#34d399]/30 rounded-2xl p-6 text-center space-y-3">
          <div className="text-3xl">✅</div>
          <div className="font-bold text-[#34d399] text-lg">Файл передан</div>
          {progress && (
            <div className="text-sm text-[#8b92a8]">
              {fmt(progress.totalBytes)}
              {progress.speedBps > 0 && <span className="ml-2 text-[#34d399] font-semibold">{fmt(progress.speedBps)}/с</span>}
            </div>
          )}
        </div>
        <button onClick={reset}
          className="w-full py-3 rounded-xl font-semibold text-sm bg-[#1e2230] text-[#8b92a8] hover:text-[#e4e7f0] border border-[#2a2f42] hover:border-[#5b7cfa] transition-all">
          Отправить ещё
        </button>
      </div>
    )
  }

  // ── Done Relay ─────────────────────────────────────────────────────────────
  if (phase === 'done' && mode === 'relay' && relayResult) {
    return (
      <div className="max-w-2xl space-y-3">
        <div className="bg-[#161920] border border-[#34d399]/30 rounded-2xl p-6 space-y-4">
          <div className="text-center space-y-2">
            <div className="text-3xl">✅</div>
            <div className="font-bold text-[#34d399] text-lg">Файл загружен на сервер</div>
            {progress && (
              <div className="text-sm text-[#8b92a8]">
                {fmt(progress.totalBytes)}
                {progress.speedBps > 0 && <span className="ml-2 text-[#34d399] font-semibold">{fmt(progress.speedBps)}/с</span>}
              </div>
            )}
          </div>
          <button
            onClick={() => copyLink(relayLink)}
            className={`w-full py-4 rounded-xl font-bold text-base transition-all ${
              copied
                ? 'bg-[#34d399]/20 border-2 border-[#34d399]/50 text-[#34d399]'
                : 'bg-gradient-to-r from-[#5b7cfa] to-[#a78bfa] text-white hover:opacity-90 shadow-lg'
            }`}>
            {copied ? '✓ Скопировано!' : '📋 Скопировать строку для получателя'}
          </button>
          <details>
            <summary className="text-xs text-[#4a5068] cursor-pointer hover:text-[#8b92a8] select-none list-none flex items-center justify-center gap-1">
              <span>📱 Показать QR-код</span>
            </summary>
            <div className="mt-3">
              <QRImg link={relayLink} />
              <p className="text-xs text-[#4a5068] text-center mt-2">Действителен 24 часа</p>
            </div>
          </details>
        </div>
        <button onClick={reset}
          className="w-full py-3 rounded-xl font-semibold text-sm bg-[#1e2230] text-[#8b92a8] hover:text-[#e4e7f0] border border-[#2a2f42] hover:border-[#5b7cfa] transition-all">
          Отправить ещё
        </button>
      </div>
    )
  }

  // ── Idle / error ───────────────────────────────────────────────────────────
  return (
    <div className="space-y-3 max-w-2xl">
      {/* Mode */}
      <div className="flex gap-1 bg-[#0d0f14] border border-[#2a2f42] rounded-xl p-1">
        {([['p2p', '⚡ P2P — прямая передача'], ['relay', '☁ Relay — через сервер']] as [Mode, string][]).map(([m, label]) => (
          <button key={m} onClick={() => { setMode(m as Mode); reset() }}
            className={`flex-1 py-2 text-sm font-semibold rounded-lg transition-all ${
              mode === m ? 'bg-gradient-to-r from-[#5b7cfa] to-[#a78bfa] text-white shadow' : 'text-[#8b92a8] hover:text-[#e4e7f0]'
            }`}>{label}
          </button>
        ))}
      </div>

      {/* File picker / drop zone */}
      <div
        className={`bg-[#161920] border-2 rounded-xl transition-all ${
          isDragging
            ? 'border-[#5b7cfa] shadow-lg shadow-[#5b7cfa]/20 scale-[1.01]'
            : 'border-[#2a2f42]'
        }`}>
        {fileInfo ? (
          <div className="flex items-center gap-3 p-5">
            <span className="text-3xl">{fileIcon(fileInfo.name)}</span>
            <div className="flex-1 min-w-0">
              <div className="font-semibold truncate">{fileInfo.name}</div>
              <div className="text-sm text-[#8b92a8]">{fmt(fileInfo.size)}</div>
            </div>
            <button onClick={pickFile}
              className="text-xs text-[#8b92a8] hover:text-[#e4e7f0] border border-[#2a2f42] hover:border-[#5b7cfa] px-3 py-1.5 rounded-lg transition-all">
              Изменить
            </button>
          </div>
        ) : (
          <button onClick={pickFile}
            className="w-full py-12 text-center group">
            <div className={`text-5xl mb-3 transition-all ${isDragging ? 'opacity-100 scale-110' : 'opacity-40 group-hover:opacity-70'}`}>
              {isDragging ? '📂' : '📂'}
            </div>
            <div className="text-sm font-semibold text-[#8b92a8] group-hover:text-[#e4e7f0] transition-colors">
              {isDragging ? 'Отпустите файл' : 'Нажмите или перетащите файл'}
            </div>
            <div className="text-xs text-[#4a5068] mt-1">Любой формат · До 100 ГБ</div>
          </button>
        )}
      </div>

      {/* Error */}
      {phase === 'error' && error && (
        <div className="bg-red-500/10 border border-red-500/30 text-red-400 rounded-xl px-4 py-3 text-sm">{error}</div>
      )}

      {/* Advanced settings */}
      <details className="group">
        <summary className="text-xs text-[#4a5068] cursor-pointer hover:text-[#8b92a8] select-none list-none flex items-center gap-1 px-1">
          <span className="group-open:rotate-90 transition-transform inline-block">▶</span>
          Дополнительно
        </summary>
        <div className="mt-2 bg-[#161920] border border-[#2a2f42] rounded-xl p-4 grid gap-3 grid-cols-2">
          <div>
            <label className="block text-xs text-[#8b92a8] mb-1.5">Размер чанка</label>
            <select value={chunkMB} onChange={e => setChunkMB(Number(e.target.value))}
              className="w-full bg-[#0d0f14] border border-[#2a2f42] rounded-lg px-3 py-2 text-sm outline-none">
              <option value={8}>8 МБ</option>
              <option value={16}>16 МБ (default)</option>
              <option value={32}>32 МБ (быстрый WAN)</option>
              <option value={64}>64 МБ (LAN)</option>
            </select>
          </div>
          {mode === 'relay' && (
            <>
              <div>
                <label className="block text-xs text-[#8b92a8] mb-1.5">Потоки</label>
                <select value={workers} onChange={e => setWorkers(Number(e.target.value))}
                  className="w-full bg-[#0d0f14] border border-[#2a2f42] rounded-lg px-3 py-2 text-sm outline-none">
                  <option value={4}>4</option><option value={8}>8 (default)</option><option value={16}>16</option>
                </select>
              </div>
              <div className="col-span-2">
                <label className="block text-xs text-[#8b92a8] mb-1.5">Server URL</label>
                <input type="text" value={serverURL} onChange={e => setServerURL(e.target.value)}
                  className="w-full bg-[#0d0f14] border border-[#2a2f42] focus:border-[#5b7cfa] rounded-lg px-3 py-2 text-sm outline-none font-mono" />
              </div>
            </>
          )}
        </div>
      </details>

      {/* Send button */}
      <button onClick={startTransfer} disabled={!fileInfo}
        className={`w-full py-3.5 rounded-xl font-bold text-sm transition-all ${
          fileInfo
            ? 'bg-gradient-to-r from-[#5b7cfa] to-[#a78bfa] text-white hover:opacity-90 hover:-translate-y-0.5 shadow-lg shadow-[#5b7cfa]/20'
            : 'bg-[#1e2230] text-[#4a5068] cursor-not-allowed'
        }`}>
        {mode === 'p2p' ? '⚡ Начать P2P — строка скопируется автоматически' : '↑ Загрузить на Relay сервер'}
      </button>
    </div>
  )
}

// ── Shared components ────────────────────────────────────────────────────────

function ProgressCard({ progress, pct }: { progress: { done: number; total: number; bytesDone: number; totalBytes: number; speedBps: number }, pct: number }) {
  const remaining = progress.totalBytes - progress.bytesDone
  const eta = progress.speedBps > 0 && remaining > 0 ? remaining / progress.speedBps : 0
  return (
    <div className="bg-[#161920] border border-[#2a2f42] rounded-xl p-4 space-y-3">
      <div className="flex justify-between items-end">
        <span className="text-[#8b92a8] text-xs">{progress.done}/{progress.total} чанков</span>
        <span className="font-bold text-lg">{pct}%</span>
      </div>
      <div className="h-2.5 bg-[#0d0f14] rounded-full overflow-hidden">
        <div
          className="h-full bg-gradient-to-r from-[#5b7cfa] to-[#a78bfa] rounded-full transition-all duration-300"
          style={{ width: `${pct}%` }}
        />
      </div>
      <div className="flex items-center justify-between text-xs">
        <span className="text-[#8b92a8]">{fmt(progress.bytesDone)} / {fmt(progress.totalBytes)}</span>
        <div className="flex items-center gap-3">
          {progress.speedBps > 0 && (
            <span className="text-[#34d399] font-bold text-sm">{fmt(progress.speedBps)}/с</span>
          )}
          {eta > 0 && <span className="text-[#8b92a8]">ETA {fmtDur(eta)}</span>}
        </div>
      </div>
    </div>
  )
}

function Row({ label, value, color }: { label: string; value: string; color: string }) {
  const [c, setC] = useState(false)
  return (
    <div className="flex items-center gap-2">
      <span className="text-xs text-[#8b92a8] w-20 shrink-0">{label}</span>
      <span className={`font-mono text-xs flex-1 truncate ${color}`}>{value}</span>
      <button
        onClick={() => { navigator.clipboard.writeText(value); setC(true); setTimeout(() => setC(false), 1500) }}
        className="shrink-0 text-xs px-2 py-0.5 border border-[#2a2f42] rounded text-[#8b92a8] hover:text-[#e4e7f0]">
        {c ? '✓' : '📋'}
      </button>
    </div>
  )
}
