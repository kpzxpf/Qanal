import { useState, useEffect, useCallback, useRef } from 'react'
import { EventsOn, EventsOff } from '../../wailsjs/runtime/runtime'
import { SelectDirectory, ReceiveFile, PeerReceive } from '../../wailsjs/go/main/App'
import { fmt, fmtDur } from '../utils/format'
import { decodeLink, type ShareLink } from '../utils/sharelink'

type Mode = 'relay' | 'p2p'
type Phase = 'idle' | 'downloading' | 'done' | 'error'
interface Progress { done: number; total: number; bytesDone: number; totalBytes: number; speedBps: number }

export default function ReceivePanel({ defaultServerURL }: { defaultServerURL: string }) {
  const [mode, setMode] = useState<Mode>('p2p')
  const [serverURL, setServerURL] = useState(defaultServerURL)
  const [code, setCode] = useState('')
  const [key, setKey] = useState('')
  const [peerAddr, setPeerAddr] = useState('')
  const [keyVisible, setKeyVisible] = useState(false)
  const [outDir, setOutDir] = useState('')
  const [workers, setWorkers] = useState(8)
  const [phase, setPhase] = useState<Phase>('idle')
  const [progress, setProgress] = useState<Progress | null>(null)
  const [savedPath, setSavedPath] = useState('')
  const [error, setError] = useState('')
  const [linkStr, setLinkStr] = useState('')
  const [parsedLink, setParsedLink] = useState<ShareLink | null>(null)
  const [wanAddr, setWanAddr] = useState('')
  const [lanAddr, setLanAddr] = useState('')
  const textareaRef = useRef<HTMLTextAreaElement>(null)

  useEffect(() => {
    if (serverURL === 'http://localhost:8080' && defaultServerURL !== 'http://localhost:8080')
      setServerURL(defaultServerURL)
  }, [defaultServerURL, serverURL])

  // Auto-focus textarea
  useEffect(() => { textareaRef.current?.focus() }, [])

  const reset = useCallback(() => {
    EventsOff('transfer:progress'); EventsOff('transfer:error'); EventsOff('transfer:complete')
    setPhase('idle'); setProgress(null); setSavedPath(''); setError('')
  }, [])

  const applyLink = useCallback((s: string) => {
    const link = decodeLink(s)
    if (!link) return false
    setParsedLink(link)
    if (link.t === 'p') {
      setMode('p2p')
      setWanAddr(link.w || '')
      setLanAddr(link.l)
      setPeerAddr(link.w || link.l)
      setCode(link.c)
      setKey(link.k)
    } else {
      setMode('relay')
      setServerURL(link.s)
      setCode(link.c)
      setKey(link.k)
    }
    reset()
    return true
  }, [reset])

  const handleLinkChange = useCallback((s: string) => {
    setLinkStr(s)
    if (s.trim().startsWith('QNL:')) applyLink(s.trim())
    else setParsedLink(null)
  }, [applyLink])

  const clearLink = useCallback(() => {
    setLinkStr(''); setParsedLink(null)
    setCode(''); setKey(''); setPeerAddr(''); setWanAddr(''); setLanAddr('')
    reset()
  }, [reset])

  const pickDir = useCallback(async () => {
    const dir = await SelectDirectory()
    if (dir) setOutDir(dir)
  }, [])

  const canStart = mode === 'p2p'
    ? peerAddr.trim() !== '' && code.trim() !== '' && key.trim() !== ''
    : code.trim() !== '' && key.trim() !== ''

  const startReceive = useCallback(async () => {
    if (!canStart) return
    setPhase('downloading'); setProgress(null); setError(''); setSavedPath('')
    const saveDir = outDir || '.'

    EventsOn('transfer:progress', (e: Progress) => setProgress(e))
    EventsOn('transfer:error', (e: { message: string }) => {
      setError(e.message); setPhase('error')
      EventsOff('transfer:progress'); EventsOff('transfer:error'); EventsOff('transfer:complete')
    })
    EventsOn('transfer:complete', (e: { path: string }) => {
      setSavedPath(e.path); setPhase('done')
      EventsOff('transfer:progress'); EventsOff('transfer:error'); EventsOff('transfer:complete')
    })

    try {
      if (mode === 'p2p') {
        await PeerReceive(peerAddr.trim(), code.trim().toUpperCase(), key.trim(), saveDir)
      } else {
        await ReceiveFile(serverURL, code.trim().toUpperCase(), key.trim(), saveDir, workers)
      }
    } catch (e: any) {
      setError(e?.message || String(e)); setPhase('error')
      EventsOff('transfer:progress'); EventsOff('transfer:error'); EventsOff('transfer:complete')
    }
  }, [mode, peerAddr, serverURL, code, key, outDir, workers, canStart])

  const pct = progress ? Math.round((progress.done / progress.total) * 100) : 0

  // ── Downloading ─────────────────────────────────────────────────────────────
  if (phase === 'downloading') {
    const remaining = progress ? progress.totalBytes - progress.bytesDone : 0
    const eta = progress && progress.speedBps > 0 && remaining > 0 ? remaining / progress.speedBps : 0
    return (
      <div className="max-w-2xl space-y-3">
        <div className="bg-[#161920] border border-[#2a2f42] rounded-2xl p-6 space-y-4">
          <div className="flex items-center gap-2">
            <span className="w-2 h-2 rounded-full bg-[#5b7cfa] animate-pulse" />
            <span className="text-[#8b92a8] text-sm">
              {mode === 'p2p' ? 'Получение файла…' : 'Загрузка с сервера…'}
            </span>
          </div>
          {progress ? (
            <>
              <div className="flex justify-between items-end">
                <span className="text-[#8b92a8] text-xs">{progress.done}/{progress.total} чанков</span>
                <span className="font-bold text-lg">{pct}%</span>
              </div>
              <div className="h-2.5 bg-[#0d0f14] rounded-full overflow-hidden">
                <div className="h-full bg-gradient-to-r from-[#5b7cfa] to-[#a78bfa] rounded-full transition-all duration-300"
                  style={{ width: `${pct}%` }} />
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
            </>
          ) : (
            <div className="flex justify-center py-4">
              <div className="w-8 h-8 border-2 border-[#5b7cfa]/30 border-t-[#5b7cfa] rounded-full animate-spin" />
            </div>
          )}
        </div>
        <button onClick={reset}
          className="w-full py-2.5 rounded-xl text-sm text-[#8b92a8] border border-[#2a2f42] hover:border-red-500/40 hover:text-red-400 transition-all">
          Отменить
        </button>
      </div>
    )
  }

  // ── Done ────────────────────────────────────────────────────────────────────
  if (phase === 'done') {
    return (
      <div className="max-w-2xl space-y-3">
        <div className="bg-[#161920] border border-[#34d399]/30 rounded-2xl p-6 text-center space-y-3">
          <div className="text-3xl">✅</div>
          <div className="font-bold text-[#34d399] text-lg">Файл получен</div>
          {savedPath && (
            <div className="font-mono text-xs text-[#8b92a8] break-all bg-[#0d0f14] rounded-lg px-3 py-2 text-left">
              {savedPath}
            </div>
          )}
          {progress && (
            <div className="text-sm text-[#8b92a8]">
              {fmt(progress.totalBytes)}
              {progress.speedBps > 0 && <span className="ml-2 text-[#34d399] font-semibold">{fmt(progress.speedBps)}/с</span>}
            </div>
          )}
        </div>
        <button onClick={reset}
          className="w-full py-3 rounded-xl font-semibold text-sm bg-[#1e2230] text-[#8b92a8] hover:text-[#e4e7f0] border border-[#2a2f42] hover:border-[#5b7cfa] transition-all">
          Принять ещё
        </button>
      </div>
    )
  }

  // ── Idle / error ────────────────────────────────────────────────────────────
  return (
    <div className="space-y-3 max-w-2xl">

      {/* Paste link */}
      <div className={`bg-[#161920] rounded-xl p-5 border transition-all ${
        parsedLink ? 'border-[#34d399]/40' : 'border-[#5b7cfa]/40'
      }`}>
        <div className="text-xs font-semibold uppercase tracking-widest mb-2 text-[#8b92a8]">
          Строка подключения от отправителя
        </div>
        {!parsedLink ? (
          <>
            <textarea
              ref={textareaRef}
              rows={2}
              value={linkStr}
              onChange={e => handleLinkChange(e.target.value)}
              onPaste={e => {
                const text = e.clipboardData.getData('text')
                setTimeout(() => handleLinkChange(text), 0)
              }}
              placeholder="Вставьте строку QNL:... — поля заполнятся автоматически"
              className="w-full bg-[#0d0f14] border border-[#2a2f42] focus:border-[#5b7cfa] rounded-lg px-3 py-2.5 text-xs outline-none font-mono resize-none text-[#8b92a8] placeholder:text-[#4a5068]"
            />
            <p className="text-xs text-[#4a5068] mt-1.5">Или заполните поля вручную ниже</p>
          </>
        ) : (
          <div className="flex items-center gap-3">
            <div className="flex-1 min-w-0">
              <div className="flex items-center gap-2 mb-1">
                <span className="text-[#34d399] text-sm font-semibold">✓ Распознано</span>
                <span className="text-xs text-[#8b92a8] bg-[#0d0f14] px-2 py-0.5 rounded-full">
                  {parsedLink.t === 'p' ? '⚡ P2P' : '☁ Relay'}
                </span>
              </div>
              <div className="font-mono text-xs text-[#4a5068] truncate">{linkStr.slice(0, 60)}…</div>
            </div>
            <button onClick={clearLink}
              className="shrink-0 text-xs px-3 py-1.5 border border-[#2a2f42] hover:border-red-500/40 hover:text-red-400 rounded-lg text-[#8b92a8] transition-all">
              ✕
            </button>
          </div>
        )}
      </div>

      {/* P2P address selector */}
      {parsedLink?.t === 'p' && wanAddr && lanAddr && (
        <div className="bg-[#161920] border border-[#2a2f42] rounded-xl p-4 space-y-2">
          <div className="text-xs text-[#8b92a8] font-semibold uppercase tracking-widest mb-2">Адрес подключения</div>
          <div className="grid grid-cols-2 gap-2">
            <button onClick={() => setPeerAddr(wanAddr)}
              className={`p-3 rounded-lg border text-left transition-all ${
                peerAddr === wanAddr ? 'border-[#34d399]/50 bg-[#34d399]/8' : 'border-[#2a2f42] hover:border-[#34d399]/30'
              }`}>
              <div className="text-xs text-[#8b92a8] mb-1">🌍 Интернет</div>
              <div className="font-mono text-xs font-bold text-[#34d399]">{wanAddr}</div>
            </button>
            <button onClick={() => setPeerAddr(lanAddr)}
              className={`p-3 rounded-lg border text-left transition-all ${
                peerAddr === lanAddr ? 'border-[#5b7cfa]/50 bg-[#5b7cfa]/8' : 'border-[#2a2f42] hover:border-[#5b7cfa]/30'
              }`}>
              <div className="text-xs text-[#8b92a8] mb-1">📡 Локально</div>
              <div className="font-mono text-xs font-bold text-[#5b7cfa]">{lanAddr}</div>
            </button>
          </div>
        </div>
      )}

      {/* Manual fields */}
      {!parsedLink && (
        <>
          <div className="flex gap-1 bg-[#0d0f14] border border-[#2a2f42] rounded-xl p-1">
            {([['p2p', '⚡ P2P Direct'], ['relay', '☁ Relay']] as [Mode, string][]).map(([m, label]) => (
              <button key={m} onClick={() => { setMode(m as Mode); reset() }}
                className={`flex-1 py-2 text-sm font-semibold rounded-lg transition-all ${
                  mode === m ? 'bg-gradient-to-r from-[#5b7cfa] to-[#a78bfa] text-white shadow' : 'text-[#8b92a8] hover:text-[#e4e7f0]'
                }`}>{label}</button>
            ))}
          </div>

          <div className="bg-[#161920] border border-[#2a2f42] rounded-xl p-5 space-y-3">
            {mode === 'p2p' ? (
              <>
                <Field label="Peer Address">
                  <input type="text" value={peerAddr} onChange={e => setPeerAddr(e.target.value)}
                    placeholder="192.168.1.5:54321"
                    className="w-full bg-[#0d0f14] border border-[#2a2f42] focus:border-[#5b7cfa] rounded-lg px-3 py-2 text-sm outline-none font-mono text-[#5b7cfa]" />
                </Field>
                <div className="grid grid-cols-2 gap-3">
                  <Field label="Код">
                    <input type="text" value={code} onChange={e => setCode(e.target.value.toUpperCase())}
                      placeholder="ABCD1234" maxLength={8}
                      className="w-full bg-[#0d0f14] border border-[#2a2f42] focus:border-[#5b7cfa] rounded-lg px-3 py-2 text-sm outline-none font-mono font-bold tracking-widest text-[#5b7cfa]" />
                  </Field>
                  <KeyField value={key} onChange={setKey} visible={keyVisible} onToggle={() => setKeyVisible(v => !v)} />
                </div>
              </>
            ) : (
              <>
                <Field label="Server URL">
                  <input type="text" value={serverURL} onChange={e => setServerURL(e.target.value)}
                    placeholder="http://192.168.1.5:8080"
                    className="w-full bg-[#0d0f14] border border-[#2a2f42] focus:border-[#5b7cfa] rounded-lg px-3 py-2 text-sm outline-none font-mono" />
                </Field>
                <div className="grid grid-cols-2 gap-3">
                  <Field label="Transfer Code">
                    <input type="text" value={code} onChange={e => setCode(e.target.value.toUpperCase())}
                      placeholder="ABCD1234" maxLength={8}
                      className="w-full bg-[#0d0f14] border border-[#2a2f42] focus:border-[#5b7cfa] rounded-lg px-3 py-2 text-sm outline-none font-mono font-bold tracking-widest text-[#5b7cfa]" />
                  </Field>
                  <KeyField value={key} onChange={setKey} visible={keyVisible} onToggle={() => setKeyVisible(v => !v)} />
                </div>
                <Field label="Потоки">
                  <select value={workers} onChange={e => setWorkers(Number(e.target.value))}
                    className="bg-[#0d0f14] border border-[#2a2f42] rounded-lg px-3 py-2 text-sm outline-none">
                    <option value={4}>4</option><option value={8}>8</option><option value={16}>16</option>
                  </select>
                </Field>
              </>
            )}
          </div>
        </>
      )}

      {/* Save location */}
      <div className="bg-[#161920] border border-[#2a2f42] rounded-xl px-5 py-4">
        <label className="block text-xs text-[#8b92a8] mb-1.5">Сохранить в</label>
        <div className="flex gap-2">
          <input type="text" value={outDir} onChange={e => setOutDir(e.target.value)}
            placeholder="Папка загрузок (по умолчанию)"
            className="flex-1 bg-[#0d0f14] border border-[#2a2f42] focus:border-[#5b7cfa] rounded-lg px-3 py-2 text-sm outline-none font-mono" />
          <button onClick={pickDir}
            className="px-3 py-2 border border-[#2a2f42] hover:border-[#5b7cfa] rounded-lg text-sm text-[#8b92a8] hover:text-[#e4e7f0]">
            Обзор
          </button>
        </div>
      </div>

      {/* Error */}
      {phase === 'error' && error && (
        <div className="bg-red-500/10 border border-red-500/30 text-red-400 rounded-xl px-4 py-3 text-sm">{error}</div>
      )}

      {/* Receive button */}
      <button onClick={startReceive} disabled={!canStart}
        className={`w-full py-3.5 rounded-xl font-bold text-sm transition-all ${
          canStart
            ? 'bg-gradient-to-r from-[#5b7cfa] to-[#a78bfa] text-white hover:opacity-90 hover:-translate-y-0.5 shadow-lg shadow-[#5b7cfa]/20'
            : 'bg-[#1e2230] text-[#4a5068] cursor-not-allowed'
        }`}>
        {mode === 'p2p' ? '⚡ Получить (P2P)' : '↓ Скачать с Relay'}
      </button>
    </div>
  )
}

function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div>
      <label className="block text-xs text-[#8b92a8] mb-1.5">{label}</label>
      {children}
    </div>
  )
}

function KeyField({ value, onChange, visible, onToggle }: {
  value: string; onChange: (v: string) => void; visible: boolean; onToggle: () => void
}) {
  return (
    <Field label="Ключ шифрования">
      <div className="flex gap-1">
        <input type={visible ? 'text' : 'password'} value={value} onChange={e => onChange(e.target.value)}
          placeholder="Вставьте ключ"
          className="flex-1 bg-[#0d0f14] border border-[#2a2f42] focus:border-[#a78bfa] rounded-lg px-3 py-2 text-xs outline-none font-mono" />
        <button onClick={onToggle}
          className="px-2 border border-[#2a2f42] rounded-lg text-[#8b92a8] hover:text-[#e4e7f0] text-sm">
          {visible ? '🙈' : '👁'}
        </button>
      </div>
    </Field>
  )
}
