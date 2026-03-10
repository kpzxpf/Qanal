import { useState, useEffect, useCallback, useRef } from 'react'
import { EventsOn, EventsOff } from '../lib/events'
import { SelectFile, GetFileInfo, StartPeerSend, StopPeerSend, storeWebFile } from '../lib/app'
import { isWails } from '../lib/mode'
import type { transfer } from '../../wailsjs/go/models'
import { fmt, fmtDur } from '../utils/format'
import { encodeLink } from '../utils/sharelink'

type Phase = 'idle' | 'starting' | 'waiting' | 'transferring' | 'done' | 'error'
interface Progress { done: number; total: number; bytesDone: number; totalBytes: number; speedBps: number }

function fileIcon(name: string) {
  const ext = name.split('.').pop()?.toLowerCase() ?? ''
  if (['jpg','jpeg','png','gif','webp','svg','bmp'].includes(ext)) return '🖼'
  if (['mp4','mov','avi','mkv','webm'].includes(ext)) return '🎬'
  if (['mp3','flac','aac','wav','ogg'].includes(ext)) return '🎵'
  if (['zip','rar','7z','tar','gz'].includes(ext)) return '📦'
  if (['pdf'].includes(ext)) return '📑'
  if (['doc','docx','odt'].includes(ext)) return '📝'
  if (['xls','xlsx','csv'].includes(ext)) return '📊'
  if (['exe','msi','apk'].includes(ext)) return '⚙'
  return '📄'
}

export default function SendPanel() {
  const [filePath, setFilePath] = useState('')
  const [fileInfo, setFileInfo] = useState<transfer.FileInfo | null>(null)
  const [chunkMB, setChunkMB] = useState(16)
  const [phase, setPhase] = useState<Phase>('idle')
  const [progress, setProgress] = useState<Progress | null>(null)
  const [peerInfo, setPeerInfo] = useState<transfer.PeerInfo | null>(null)
  const [error, setError] = useState('')
  const [copied, setCopied] = useState(false)
  const [isDragging, setIsDragging] = useState(false)
  const [showAdvanced, setShowAdvanced] = useState(false)
  const autoCopied = useRef(false)

  // Wails OS file drop
  useEffect(() => {
    return EventsOn('file:dropped', (e: { path: string }) => {
      if (!e?.path) return
      setFilePath(e.path)
      GetFileInfo(e.path).then(setFileInfo).catch(() => {})
      setPhase('idle'); setProgress(null); setPeerInfo(null)
      setError(''); autoCopied.current = false
    })
  }, [])

  // Web drag-and-drop
  useEffect(() => {
    const onOver = (e: DragEvent) => { e.preventDefault(); if (e.dataTransfer?.types.includes('Files')) setIsDragging(true) }
    const onLeave = () => setIsDragging(false)
    const onDrop = (e: DragEvent) => {
      e.preventDefault(); setIsDragging(false)
      if (!isWails && e.dataTransfer?.files?.[0]) {
        const file = e.dataTransfer.files[0]
        const path = storeWebFile(file)
        setFilePath(path)
        setFileInfo({ name: file.name, size: file.size } as transfer.FileInfo)
        setPhase('idle'); setProgress(null); setPeerInfo(null)
        setError(''); autoCopied.current = false
      }
    }
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
    setPhase('idle'); setProgress(null); setPeerInfo(null)
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
    setTimeout(() => setCopied(false), 2500)
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
    EventsOn('transfer:complete', () => {
      setPhase('done')
      EventsOff('transfer:progress'); EventsOff('transfer:error'); EventsOff('transfer:complete')
    })

    try {
      const info = await StartPeerSend(filePath, chunkMB)
      setPeerInfo(info)
      setPhase('waiting')
      const link = encodeLink({ t: 'p', w: info.wan, l: info.lan, c: info.code, k: info.key })
      if (!autoCopied.current) { autoCopied.current = true; copyLink(link) }
    } catch (e: any) {
      setError(e?.message || String(e)); setPhase('error')
      EventsOff('transfer:progress'); EventsOff('transfer:error'); EventsOff('transfer:complete')
    }
  }, [filePath, fileInfo, chunkMB, copyLink])

  const cancel = useCallback(async () => {
    await StopPeerSend()
    reset()
  }, [reset])

  const pct = progress ? Math.round((progress.done / progress.total) * 100) : 0
  const p2pLink = peerInfo ? encodeLink({ t: 'p', w: peerInfo.wan, l: peerInfo.lan, c: peerInfo.code, k: peerInfo.key }) : ''

  // ── Starting spinner ─────────────────────────────────────────────────────────
  if (phase === 'starting') {
    return (
      <div className="flex flex-col items-center justify-center py-24 gap-4">
        <div className="w-10 h-10 border-2 border-[#5b7cfa]/20 border-t-[#5b7cfa] rounded-full animate-spin" />
        <p className="text-[#5a6280] text-sm">Открытие порта…</p>
      </div>
    )
  }

  // ── Waiting / transferring ───────────────────────────────────────────────────
  if ((phase === 'waiting' || phase === 'transferring') && peerInfo) {
    return (
      <div className="space-y-3">
        {/* Status */}
        <div className="flex items-center gap-2.5 px-1">
          <span className={`w-2 h-2 rounded-full shrink-0 ${phase === 'waiting' ? 'bg-[#fbbf24] animate-pulse' : 'bg-[#34d399] animate-pulse'}`} />
          <span className="text-sm font-medium text-[#8b92a8]">
            {phase === 'waiting' ? 'Ожидание получателя…' : 'Передача идёт'}
          </span>
          {phase === 'transferring' && progress && (
            <span className="ml-auto font-bold text-sm text-[#34d399]">{pct}%</span>
          )}
        </div>

        {/* CGNAT warning */}
        {peerInfo.cgnat && (
          <div className="bg-amber-500/8 border border-amber-500/25 rounded-xl px-4 py-3 text-xs text-amber-400 space-y-1">
            <div className="font-semibold">⚠ CGNAT обнаружен — передача через интернет заблокирована</div>
            <div className="text-amber-400/70">
              Ваш провайдер использует двойной NAT. Используйте <span className="font-semibold">LAN-адрес</span> (одна сеть) или попросите провайдера выдать белый IP.
            </div>
          </div>
        )}

        {/* UPnP status */}
        {!peerInfo.cgnat && !peerInfo.upnp && peerInfo.wan && (
          <div className="bg-blue-500/8 border border-blue-500/20 rounded-xl px-4 py-3 text-xs text-blue-400">
            ℹ UPnP недоступен — если получатель в другой сети, порт нужно открыть вручную в роутере.
          </div>
        )}

        {fileInfo && <FileChip info={fileInfo} />}
        <ShareBox link={p2pLink} copied={copied} onCopy={() => copyLink(p2pLink)} />
        {phase === 'transferring' && progress && <ProgressCard progress={progress} pct={pct} />}

        {/* Connection details */}
        <div className="bg-[#111520] border border-[#242a3c] rounded-xl p-4 space-y-2">
          <p className="text-xs text-[#5a6280] uppercase tracking-widest font-semibold mb-3">Детали подключения</p>
          {peerInfo.wan && <AddrRow label="Интернет" value={peerInfo.wan} accent="text-[#34d399]" />}
          <AddrRow label="LAN" value={peerInfo.lan} accent="text-[#5b7cfa]" />
          <AddrRow label="Код" value={peerInfo.code} accent="text-[#a78bfa]" />
          <div className="flex gap-3 pt-1">
            <Badge label="UPnP" ok={peerInfo.upnp} />
            <Badge label="CGNAT" ok={!peerInfo.cgnat} invert />
          </div>
        </div>

        <CancelBtn onClick={cancel} />
      </div>
    )
  }

  // ── Done ─────────────────────────────────────────────────────────────────────
  if (phase === 'done') {
    return (
      <div className="space-y-3">
        <DoneCard title="Файл передан" bytes={progress?.totalBytes} speed={progress?.speedBps} />
        <ResetBtn onClick={reset} label="Отправить ещё" />
      </div>
    )
  }

  // ── Idle / error ─────────────────────────────────────────────────────────────
  return (
    <div className="space-y-3">
      {/* Drop zone */}
      <div
        onClick={!fileInfo ? pickFile : undefined}
        className={`rounded-xl border-2 transition-all ${
          isDragging
            ? 'border-[#5b7cfa] bg-[#5b7cfa]/5 shadow-lg shadow-[#5b7cfa]/10'
            : fileInfo
              ? 'border-[#242a3c] bg-[#111520]'
              : 'border-dashed border-[#242a3c] bg-[#111520] hover:border-[#2a3050] cursor-pointer'
        }`}
      >
        {fileInfo ? (
          <div className="flex items-center gap-3 px-4 py-3.5">
            <span className="text-2xl">{fileIcon(fileInfo.name)}</span>
            <div className="flex-1 min-w-0">
              <div className="font-semibold text-sm truncate">{fileInfo.name}</div>
              <div className="text-xs text-[#5a6280] mt-0.5">{fmt(fileInfo.size)}</div>
            </div>
            <button onClick={pickFile}
              className="shrink-0 text-xs text-[#5a6280] hover:text-[#8b92a8] border border-[#242a3c] hover:border-[#2a3050] px-3 py-1.5 rounded-lg transition-all">
              Изменить
            </button>
          </div>
        ) : (
          <div className="py-14 flex flex-col items-center gap-2">
            <span className={`text-4xl transition-all ${isDragging ? 'scale-110' : 'opacity-30'}`}>📂</span>
            <span className="text-sm text-[#5a6280]">
              {isDragging ? 'Отпустите файл' : 'Нажмите или перетащите файл'}
            </span>
            <span className="text-xs text-[#3d4562]">Любой формат · До 100 ГБ</span>
          </div>
        )}
      </div>

      {phase === 'error' && error && (
        <div className="bg-red-500/8 border border-red-500/20 text-red-400 rounded-xl px-4 py-3 text-sm">{error}</div>
      )}

      {/* Advanced options */}
      <div>
        <button onClick={() => setShowAdvanced(v => !v)}
          className="flex items-center gap-1.5 text-xs text-[#3d4562] hover:text-[#5a6280] transition-colors px-1 py-0.5">
          <span className={`transition-transform ${showAdvanced ? 'rotate-90' : ''}`}>▶</span>
          Дополнительно
        </button>
        {showAdvanced && (
          <div className="mt-2 bg-[#111520] border border-[#242a3c] rounded-xl p-4">
            <label className="block text-xs text-[#5a6280] mb-1.5">Размер чанка</label>
            <select value={chunkMB} onChange={e => setChunkMB(Number(e.target.value))}
              className="w-full bg-[#0c0f16] border border-[#242a3c] rounded-lg px-3 py-2 text-xs text-[#8b92a8] outline-none">
              <option value={8}>8 МБ</option>
              <option value={16}>16 МБ (по умолчанию)</option>
              <option value={32}>32 МБ</option>
              <option value={64}>64 МБ</option>
            </select>
          </div>
        )}
      </div>

      <button onClick={startTransfer} disabled={!fileInfo}
        className={`w-full py-3.5 rounded-xl font-bold text-sm transition-all ${
          fileInfo
            ? 'bg-gradient-to-r from-[#5b7cfa] to-[#a78bfa] text-white hover:opacity-90 hover:-translate-y-0.5 shadow-lg shadow-[#5b7cfa]/20'
            : 'bg-[#111520] text-[#3d4562] border border-[#242a3c] cursor-not-allowed'
        }`}>
        ⚡ Начать — строка скопируется автоматически
      </button>
    </div>
  )
}

// ── Shared components ─────────────────────────────────────────────────────────

function FileChip({ info }: { info: { name: string; size: number } }) {
  const ext = info.name.split('.').pop()?.toLowerCase() ?? ''
  const icons: Record<string, string> = { jpg:'🖼', jpeg:'🖼', png:'🖼', mp4:'🎬', mp3:'🎵', zip:'📦', pdf:'📑' }
  return (
    <div className="flex items-center gap-3 bg-[#111520] border border-[#242a3c] rounded-xl px-4 py-3">
      <span className="text-xl">{icons[ext] ?? '📄'}</span>
      <div className="flex-1 min-w-0">
        <div className="font-semibold text-sm truncate">{info.name}</div>
        <div className="text-xs text-[#5a6280]">{fmt(info.size)}</div>
      </div>
    </div>
  )
}

function ShareBox({ link, copied, onCopy }: { link: string; copied: boolean; onCopy: () => void }) {
  return (
    <div className="bg-[#111520] border border-[#242a3c] rounded-xl overflow-hidden">
      <div className="px-4 pt-3 pb-2">
        <p className="text-xs text-[#5a6280] uppercase tracking-widest font-semibold mb-2">Строка для получателя</p>
        <p className="font-mono text-xs text-[#8b92a8] break-all leading-relaxed select-all">{link}</p>
      </div>
      <button onClick={onCopy}
        className={`w-full py-2.5 text-sm font-semibold transition-all border-t ${
          copied
            ? 'bg-[#34d399]/10 border-[#34d399]/20 text-[#34d399]'
            : 'bg-[#161920] border-[#242a3c] text-[#8b92a8] hover:text-white hover:bg-[#1e2235]'
        }`}>
        {copied ? '✓ Скопировано' : '📋 Скопировать'}
      </button>
    </div>
  )
}

function ProgressCard({ progress, pct }: { progress: Progress; pct: number }) {
  const remaining = progress.totalBytes - progress.bytesDone
  const eta = progress.speedBps > 0 && remaining > 0 ? remaining / progress.speedBps : 0
  return (
    <div className="bg-[#111520] border border-[#242a3c] rounded-xl p-4 space-y-2.5">
      <div className="h-1.5 bg-[#1e2235] rounded-full overflow-hidden">
        <div className="h-full bg-gradient-to-r from-[#5b7cfa] to-[#a78bfa] rounded-full transition-all duration-300"
          style={{ width: `${pct}%` }} />
      </div>
      <div className="flex items-center justify-between text-xs text-[#5a6280]">
        <span>{fmt(progress.bytesDone)} / {fmt(progress.totalBytes)}</span>
        <div className="flex items-center gap-3">
          {progress.speedBps > 0 && <span className="text-[#34d399] font-bold">{fmt(progress.speedBps)}/с</span>}
          {eta > 0 && <span>ETA {fmtDur(eta)}</span>}
        </div>
      </div>
    </div>
  )
}

function DoneCard({ title, bytes, speed }: { title: string; bytes?: number; speed?: number }) {
  return (
    <div className="bg-[#111520] border border-[#34d399]/20 rounded-xl p-5 text-center space-y-1">
      <div className="text-2xl mb-2">✅</div>
      <div className="font-bold text-[#34d399]">{title}</div>
      {bytes != null && (
        <div className="text-xs text-[#5a6280] mt-1">
          {fmt(bytes)}
          {speed && speed > 0 && <span className="ml-2 text-[#34d399] font-semibold">{fmt(speed)}/с</span>}
        </div>
      )}
    </div>
  )
}

function AddrRow({ label, value, accent }: { label: string; value: string; accent: string }) {
  const [c, setC] = useState(false)
  return (
    <div className="flex items-center gap-2">
      <span className="text-xs text-[#3d4562] w-14 shrink-0">{label}</span>
      <span className={`font-mono text-xs flex-1 truncate ${accent}`}>{value}</span>
      <button
        onClick={() => { navigator.clipboard.writeText(value); setC(true); setTimeout(() => setC(false), 1500) }}
        className="shrink-0 text-xs px-2 py-0.5 border border-[#242a3c] rounded text-[#5a6280] hover:text-[#8b92a8] transition-colors">
        {c ? '✓' : '📋'}
      </button>
    </div>
  )
}

function Badge({ label, ok, invert }: { label: string; ok: boolean; invert?: boolean }) {
  const active = invert ? !ok : ok
  return (
    <span className={`text-xs px-2 py-0.5 rounded-full border ${
      active
        ? 'border-[#34d399]/30 text-[#34d399] bg-[#34d399]/5'
        : 'border-[#242a3c] text-[#3d4562]'
    }`}>
      {ok !== (invert ?? false) ? '✓' : '✗'} {label}
    </span>
  )
}

function CancelBtn({ onClick }: { onClick: () => void }) {
  return (
    <button onClick={onClick}
      className="w-full py-2.5 rounded-xl text-xs text-[#5a6280] border border-[#242a3c] hover:border-red-500/30 hover:text-red-400 transition-all">
      Отменить
    </button>
  )
}

function ResetBtn({ onClick, label }: { onClick: () => void; label: string }) {
  return (
    <button onClick={onClick}
      className="w-full py-3 rounded-xl text-sm font-semibold text-[#5a6280] border border-[#242a3c] hover:border-[#5b7cfa]/40 hover:text-[#8b92a8] transition-all">
      {label}
    </button>
  )
}
