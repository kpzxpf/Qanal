import { useState, useEffect, useCallback, useRef } from 'react'
import { EventsOn, EventsOff } from '../../wailsjs/runtime/runtime'
import { SelectFile, GetFileInfo, SendFile, StartPeerSend, StopPeerSend } from '../../wailsjs/go/main/App'
import type { transfer } from '../../wailsjs/go/models'
import { fmt, fmtDur } from '../utils/format'
import { encodeLink } from '../utils/sharelink'

type Mode = 'relay' | 'p2p'
type Phase = 'idle' | 'starting' | 'waiting' | 'transferring' | 'done' | 'error'
interface Progress { done: number; total: number; bytesDone: number; totalBytes: number; speedBps: number }

export default function SendPanel({ defaultServerURL }: { defaultServerURL: string }) {
  const [mode, setMode] = useState<Mode>('p2p')
  const [serverURL, setServerURL] = useState(defaultServerURL)
  const [filePath, setFilePath] = useState('')
  const [fileInfo, setFileInfo] = useState<transfer.FileInfo | null>(null)
  const [chunkMB, setChunkMB] = useState(10)
  const [workers, setWorkers] = useState(8)
  const [phase, setPhase] = useState<Phase>('idle')
  const [progress, setProgress] = useState<Progress | null>(null)
  const [peerInfo, setPeerInfo] = useState<transfer.PeerInfo | null>(null)
  const [relayResult, setRelayResult] = useState<{ code: string; key: string } | null>(null)
  const [error, setError] = useState('')
  const [copied, setCopied] = useState(false)
  const autoCopied = useRef(false)

  useEffect(() => {
    if (serverURL === 'http://localhost:8080' && defaultServerURL !== 'http://localhost:8080')
      setServerURL(defaultServerURL)
  }, [defaultServerURL])

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

  // Auto-copy share link as soon as it's ready
  const copyLink = useCallback((link: string) => {
    navigator.clipboard.writeText(link).catch(() => {})
    setCopied(true)
    setTimeout(() => setCopied(false), 3000)
  }, [])

  const startTransfer = useCallback(async () => {
    if (!fileInfo) return
    setPhase('starting'); setError(''); setProgress(null); autoCopied.current = false

    EventsOn('transfer:progress', (e: Progress) => {
      setPhase(p => p === 'waiting' || p === 'starting' ? 'transferring' : p)
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
        // Auto-copy immediately when credentials are ready
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

  // ── Active transfer view ──────────────────────────────────────────────────
  if (phase === 'starting') {
    return (
      <div className="max-w-2xl flex flex-col items-center justify-center py-16 gap-4">
        <div className="w-10 h-10 border-2 border-[#5b7cfa]/30 border-t-[#5b7cfa] rounded-full animate-spin" />
        <p className="text-[#8b92a8] text-sm">Подготовка передачи…</p>
      </div>
    )
  }

  if ((phase === 'waiting' || phase === 'transferring') && mode === 'p2p' && peerInfo) {
    return (
      <div className="max-w-2xl space-y-3">
        {/* Share string — primary action */}
        <div className="bg-[#161920] border border-[#2a2f42] rounded-2xl p-6 text-center space-y-4">
          <div className="flex items-center justify-center gap-2">
            {phase === 'waiting'
              ? <><span className="w-2 h-2 rounded-full bg-[#fbbf24] animate-pulse" /><span className="text-[#8b92a8] text-sm">Ожидание получателя</span></>
              : <><span className="w-2 h-2 rounded-full bg-[#34d399] animate-pulse" /><span className="text-[#34d399] text-sm font-semibold">Передача идёт</span></>
            }
          </div>

          <button
            onClick={() => copyLink(p2pLink)}
            className={`w-full py-4 rounded-xl font-bold text-base transition-all ${
              copied
                ? 'bg-[#34d399]/20 border-2 border-[#34d399]/50 text-[#34d399]'
                : 'bg-gradient-to-r from-[#5b7cfa] to-[#a78bfa] text-white hover:opacity-90 shadow-lg shadow-[#5b7cfa]/20'
            }`}>
            {copied ? '✓ Строка скопирована!' : '📋 Скопировать строку для получателя'}
          </button>

          {copied && (
            <p className="text-xs text-[#8b92a8]">
              Отправьте эту строку получателю — он вставит её в приложение и нажмёт «Получить»
            </p>
          )}
        </div>

        {/* Progress (if transferring) */}
        {phase === 'transferring' && progress && (
          <div className="bg-[#161920] border border-[#2a2f42] rounded-xl p-4">
            <div className="flex justify-between text-sm mb-2">
              <span className="text-[#8b92a8]">{progress.done}/{progress.total} чанков</span>
              <span className="font-semibold">{pct}%</span>
            </div>
            <div className="h-2 bg-[#0d0f14] rounded-full overflow-hidden mb-2">
              <div className="h-full bg-gradient-to-r from-[#5b7cfa] to-[#a78bfa] rounded-full transition-all duration-300" style={{ width: `${pct}%` }} />
            </div>
            <div className="flex gap-4 text-xs text-[#8b92a8]">
              <span>{fmt(progress.bytesDone)} / {fmt(progress.totalBytes)}</span>
              <span className="text-[#34d399] font-semibold">{fmt(progress.speedBps)}/s</span>
              {progress.speedBps > 0 && progress.bytesDone < progress.totalBytes && (
                <span>ETA {fmtDur((progress.totalBytes - progress.bytesDone) / progress.speedBps)}</span>
              )}
            </div>
          </div>
        )}

        {/* Details */}
        <details className="group">
          <summary className="text-xs text-[#4a5068] cursor-pointer hover:text-[#8b92a8] select-none list-none flex items-center gap-1 px-1">
            <span className="group-open:rotate-90 transition-transform inline-block">▶</span>
            Показать детали подключения
          </summary>
          <div className="mt-2 bg-[#161920] border border-[#2a2f42] rounded-xl p-4 space-y-2">
            {peerInfo.wan && (
              <Row label="🌍 Интернет" value={peerInfo.wan} color="text-[#34d399]" />
            )}
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

  if (phase === 'done' && mode === 'p2p') {
    return (
      <div className="max-w-2xl space-y-3">
        <div className="bg-[#161920] border border-[#34d399]/30 rounded-2xl p-6 text-center space-y-3">
          <div className="text-2xl">✅</div>
          <div className="font-semibold text-[#34d399]">Файл передан</div>
          {progress && (
            <div className="text-xs text-[#8b92a8]">
              {fmt(progress.totalBytes)} · {fmt(progress.speedBps)}/s средняя скорость
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

  if (phase === 'done' && mode === 'relay' && relayResult) {
    return (
      <div className="max-w-2xl space-y-3">
        <div className="bg-[#161920] border border-[#34d399]/30 rounded-2xl p-6 text-center space-y-4">
          <div className="text-2xl">✅</div>
          <div className="font-semibold text-[#34d399]">Файл загружен на сервер</div>
          <button
            onClick={() => copyLink(relayLink)}
            className={`w-full py-4 rounded-xl font-bold text-base transition-all ${
              copied
                ? 'bg-[#34d399]/20 border-2 border-[#34d399]/50 text-[#34d399]'
                : 'bg-gradient-to-r from-[#5b7cfa] to-[#a78bfa] text-white hover:opacity-90 shadow-lg'
            }`}>
            {copied ? '✓ Строка скопирована!' : '📋 Скопировать строку для получателя'}
          </button>
          {copied && (
            <p className="text-xs text-[#8b92a8]">
              Получатель вставит строку и скачает файл — он будет храниться 24 часа
            </p>
          )}
        </div>
        {progress && (
          <div className="text-xs text-[#8b92a8] text-center">
            {fmt(progress.totalBytes)} · {fmt(progress.speedBps)}/s средняя скорость
          </div>
        )}
        <button onClick={reset}
          className="w-full py-3 rounded-xl font-semibold text-sm bg-[#1e2230] text-[#8b92a8] hover:text-[#e4e7f0] border border-[#2a2f42] hover:border-[#5b7cfa] transition-all">
          Отправить ещё
        </button>
      </div>
    )
  }

  // ── Idle / error view ─────────────────────────────────────────────────────
  return (
    <div className="space-y-3 max-w-2xl">
      {/* Mode */}
      <div className="flex gap-1 bg-[#0d0f14] border border-[#2a2f42] rounded-xl p-1">
        {([['p2p', '⚡ P2P — прямая передача'], ['relay', '☁ Relay — через сервер']] as [Mode, string][]).map(([m, label]) => (
          <button key={m} onClick={() => { setMode(m); reset() }}
            className={`flex-1 py-2 text-sm font-semibold rounded-lg transition-all ${
              mode === m ? 'bg-gradient-to-r from-[#5b7cfa] to-[#a78bfa] text-white shadow' : 'text-[#8b92a8] hover:text-[#e4e7f0]'
            }`}>{label}
          </button>
        ))}
      </div>

      {/* File picker */}
      <div className="bg-[#161920] border border-[#2a2f42] rounded-xl p-5">
        {fileInfo ? (
          <div className="flex items-center gap-3">
            <span className="text-3xl">📄</span>
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
            className="w-full border-2 border-dashed border-[#2a2f42] hover:border-[#5b7cfa] rounded-xl py-10 text-center group transition-all">
            <div className="text-4xl mb-2 opacity-40 group-hover:opacity-70 transition-opacity">📂</div>
            <div className="text-sm font-semibold text-[#8b92a8] group-hover:text-[#e4e7f0] transition-colors">Нажмите чтобы выбрать файл</div>
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
              <option value={5}>5 МБ</option>
              <option value={10}>10 МБ (default)</option>
              <option value={25}>25 МБ (WAN)</option>
              <option value={50}>50 МБ (LAN)</option>
            </select>
          </div>
          {mode === 'relay' && (
            <>
              <div>
                <label className="block text-xs text-[#8b92a8] mb-1.5">Потоки</label>
                <select value={workers} onChange={e => setWorkers(Number(e.target.value))}
                  className="w-full bg-[#0d0f14] border border-[#2a2f42] rounded-lg px-3 py-2 text-sm outline-none">
                  <option value={4}>4</option><option value={8}>8</option><option value={16}>16</option>
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
        {mode === 'p2p' ? '⚡ Начать — строка скопируется автоматически' : '↑ Отправить через Relay'}
      </button>
    </div>
  )
}

function Row({ label, value, color }: { label: string; value: string; color: string }) {
  const [c, setC] = useState(false)
  return (
    <div className="flex items-center gap-2">
      <span className="text-xs text-[#8b92a8] w-20 shrink-0">{label}</span>
      <span className={`font-mono text-xs flex-1 truncate ${color}`}>{value}</span>
      <button onClick={() => { navigator.clipboard.writeText(value); setC(true); setTimeout(() => setC(false), 1500) }}
        className="shrink-0 text-xs px-2 py-0.5 border border-[#2a2f42] rounded text-[#8b92a8] hover:text-[#e4e7f0]">
        {c ? '✓' : '📋'}
      </button>
    </div>
  )
}
