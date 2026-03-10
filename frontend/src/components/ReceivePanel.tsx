import { useState, useEffect, useCallback, useRef } from 'react'
import { EventsOn, EventsOff } from '../lib/events'
import { SelectDirectory, PeerReceive } from '../lib/app'
import { fmt, fmtDur } from '../utils/format'
import { decodeLink, type ShareLink } from '../utils/sharelink'

type Phase = 'idle' | 'downloading' | 'done' | 'error'
interface Progress { done: number; total: number; bytesDone: number; totalBytes: number; speedBps: number }

export default function ReceivePanel() {
  const [code, setCode] = useState('')
  const [key, setKey] = useState('')
  const [peerAddr, setPeerAddr] = useState('')
  const [keyVisible, setKeyVisible] = useState(false)
  const [outDir, setOutDir] = useState('')
  const [phase, setPhase] = useState<Phase>('idle')
  const [progress, setProgress] = useState<Progress | null>(null)
  const [savedPath, setSavedPath] = useState('')
  const [error, setError] = useState('')
  const [linkStr, setLinkStr] = useState('')
  const [parsedLink, setParsedLink] = useState<ShareLink | null>(null)
  const [wanAddr, setWanAddr] = useState('')
  const [lanAddr, setLanAddr] = useState('')
  const [showManual, setShowManual] = useState(false)
  const textareaRef = useRef<HTMLTextAreaElement>(null)

  useEffect(() => { textareaRef.current?.focus() }, [])

  const reset = useCallback(() => {
    EventsOff('transfer:progress'); EventsOff('transfer:error'); EventsOff('transfer:complete')
    setPhase('idle'); setProgress(null); setSavedPath(''); setError('')
  }, [])

  const applyLink = useCallback((s: string) => {
    const link = decodeLink(s)
    if (!link) return false
    setParsedLink(link)
    setWanAddr(link.w || '')
    setLanAddr(link.l)
    setPeerAddr(link.w || link.l)
    setCode(link.c)
    setKey(link.k)
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

  const canStart = code.trim() !== '' && key.trim() !== '' && peerAddr.trim() !== ''

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
      await PeerReceive(peerAddr.trim(), code.trim().toUpperCase(), key.trim(), saveDir)
    } catch (e: any) {
      setError(e?.message || String(e)); setPhase('error')
      EventsOff('transfer:progress'); EventsOff('transfer:error'); EventsOff('transfer:complete')
    }
  }, [peerAddr, code, key, outDir, canStart])

  const pct = progress ? Math.round((progress.done / progress.total) * 100) : 0

  // ── Downloading ──────────────────────────────────────────────────────────────
  if (phase === 'downloading') {
    const remaining = progress ? progress.totalBytes - progress.bytesDone : 0
    const eta = progress && progress.speedBps > 0 && remaining > 0 ? remaining / progress.speedBps : 0
    return (
      <div className="space-y-3">
        <div className="flex items-center gap-2.5 px-1">
          <span className="w-2 h-2 rounded-full bg-[#5b7cfa] animate-pulse shrink-0" />
          <span className="text-sm font-medium text-[#8b92a8]">Получение файла…</span>
          {progress && <span className="ml-auto font-bold text-sm text-[#5b7cfa]">{pct}%</span>}
        </div>
        <div className="bg-[#111520] border border-[#242a3c] rounded-xl p-4 space-y-3">
          {progress ? (
            <>
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
            </>
          ) : (
            <div className="flex justify-center py-6">
              <div className="w-8 h-8 border-2 border-[#5b7cfa]/20 border-t-[#5b7cfa] rounded-full animate-spin" />
            </div>
          )}
        </div>
        <button onClick={reset}
          className="w-full py-2.5 rounded-xl text-xs text-[#5a6280] border border-[#242a3c] hover:border-red-500/30 hover:text-red-400 transition-all">
          Отменить
        </button>
      </div>
    )
  }

  // ── Done ─────────────────────────────────────────────────────────────────────
  if (phase === 'done') {
    return (
      <div className="space-y-3">
        <div className="bg-[#111520] border border-[#34d399]/20 rounded-xl p-5 text-center space-y-1.5">
          <div className="text-2xl mb-2">✅</div>
          <div className="font-bold text-[#34d399]">Файл получен</div>
          {savedPath && (
            <div className="font-mono text-xs text-[#5a6280] break-all bg-[#0c0f16] rounded-lg px-3 py-2 mt-2 text-left border border-[#242a3c]">
              {savedPath}
            </div>
          )}
          {progress && (
            <div className="text-xs text-[#5a6280] mt-1">
              {fmt(progress.totalBytes)}
              {progress.speedBps > 0 && <span className="ml-2 text-[#34d399] font-semibold">{fmt(progress.speedBps)}/с</span>}
            </div>
          )}
        </div>
        <button onClick={reset}
          className="w-full py-3 rounded-xl text-sm font-semibold text-[#5a6280] border border-[#242a3c] hover:border-[#5b7cfa]/40 hover:text-[#8b92a8] transition-all">
          Принять ещё
        </button>
      </div>
    )
  }

  // ── Idle / error ─────────────────────────────────────────────────────────────
  return (
    <div className="space-y-3">
      {/* Paste link */}
      <div className={`bg-[#111520] rounded-xl border transition-all ${parsedLink ? 'border-[#34d399]/25' : 'border-[#242a3c]'}`}>
        <div className="px-4 pt-3 pb-1">
          <p className="text-xs text-[#5a6280] uppercase tracking-widest font-semibold mb-2">Строка от отправителя</p>
        </div>
        {!parsedLink ? (
          <div className="px-4 pb-3">
            <textarea
              ref={textareaRef}
              rows={2}
              value={linkStr}
              onChange={e => handleLinkChange(e.target.value)}
              onPaste={e => { const text = e.clipboardData.getData('text'); setTimeout(() => handleLinkChange(text), 0) }}
              placeholder="Вставьте строку QNL:… — поля заполнятся сами"
              className="w-full bg-[#0c0f16] border border-[#242a3c] focus:border-[#5b7cfa] rounded-lg px-3 py-2.5 text-xs outline-none font-mono resize-none text-[#8b92a8] placeholder:text-[#3d4562]"
            />
          </div>
        ) : (
          <div className="px-4 pb-3 flex items-center gap-3">
            <div className="flex-1 min-w-0">
              <div className="flex items-center gap-2 mb-1">
                <span className="text-[#34d399] text-xs font-semibold">✓ Распознано</span>
                <span className="text-xs text-[#5a6280] bg-[#0c0f16] border border-[#242a3c] px-2 py-0.5 rounded-full">⚡ P2P</span>
              </div>
              <p className="font-mono text-xs text-[#3d4562] truncate">{linkStr.slice(0, 56)}…</p>
            </div>
            <button onClick={clearLink}
              className="shrink-0 text-xs px-3 py-1.5 border border-[#242a3c] hover:border-red-500/30 hover:text-red-400 rounded-lg text-[#5a6280] transition-all">
              ✕
            </button>
          </div>
        )}
      </div>

      {/* Address picker */}
      {parsedLink && (wanAddr || lanAddr) && (
        <div className="bg-[#111520] border border-[#242a3c] rounded-xl p-4">
          <p className="text-xs text-[#5a6280] uppercase tracking-widest font-semibold mb-3">Адрес подключения</p>
          <div className={`grid gap-2 ${wanAddr && lanAddr ? 'grid-cols-2' : 'grid-cols-1'}`}>
            {wanAddr && (
              <button onClick={() => setPeerAddr(wanAddr)}
                className={`p-3 rounded-xl border text-left transition-all ${
                  peerAddr === wanAddr
                    ? 'border-[#34d399]/40 bg-[#34d399]/5'
                    : 'border-[#242a3c] hover:border-[#2a3050]'
                }`}>
                <div className="text-xs text-[#5a6280] mb-1">🌍 Интернет</div>
                <div className="font-mono text-xs font-bold text-[#34d399] truncate">{wanAddr}</div>
              </button>
            )}
            {lanAddr && (
              <button onClick={() => setPeerAddr(lanAddr)}
                className={`p-3 rounded-xl border text-left transition-all ${
                  peerAddr === lanAddr
                    ? 'border-[#5b7cfa]/40 bg-[#5b7cfa]/5'
                    : 'border-[#242a3c] hover:border-[#2a3050]'
                }`}>
                <div className="text-xs text-[#5a6280] mb-1">📡 Локально</div>
                <div className="font-mono text-xs font-bold text-[#5b7cfa] truncate">{lanAddr}</div>
              </button>
            )}
          </div>
        </div>
      )}

      {/* Manual fields */}
      {!parsedLink && (
        <button onClick={() => setShowManual(v => !v)}
          className="flex items-center gap-1.5 text-xs text-[#3d4562] hover:text-[#5a6280] transition-colors px-1 py-0.5">
          <span className={`transition-transform ${showManual ? 'rotate-90' : ''}`}>▶</span>
          Заполнить вручную
        </button>
      )}
      {!parsedLink && showManual && (
        <div className="bg-[#111520] border border-[#242a3c] rounded-xl p-4 space-y-3">
          <FormField label="Адрес отправителя (IP:PORT)">
            <input type="text" value={peerAddr} onChange={e => setPeerAddr(e.target.value)}
              placeholder="77.88.55.60:54321"
              className="w-full bg-[#0c0f16] border border-[#242a3c] focus:border-[#5b7cfa] rounded-lg px-3 py-2 text-xs outline-none font-mono text-[#5b7cfa]" />
          </FormField>
          <div className="grid grid-cols-2 gap-3">
            <FormField label="Код">
              <input type="text" value={code} onChange={e => setCode(e.target.value.toUpperCase())}
                placeholder="ABCD1234" maxLength={8}
                className="w-full bg-[#0c0f16] border border-[#242a3c] focus:border-[#5b7cfa] rounded-lg px-3 py-2 text-xs outline-none font-mono font-bold tracking-widest text-[#a78bfa]" />
            </FormField>
            <KeyField value={key} onChange={setKey} visible={keyVisible} onToggle={() => setKeyVisible(v => !v)} />
          </div>
        </div>
      )}

      {/* Save location */}
      <div className="bg-[#111520] border border-[#242a3c] rounded-xl px-4 py-3.5">
        <label className="block text-xs text-[#5a6280] mb-1.5">Сохранить в</label>
        <div className="flex gap-2">
          <input type="text" value={outDir} onChange={e => setOutDir(e.target.value)}
            placeholder="Папка загрузок (по умолчанию)"
            className="flex-1 bg-[#0c0f16] border border-[#242a3c] focus:border-[#5b7cfa] rounded-lg px-3 py-2 text-xs outline-none font-mono text-[#8b92a8]" />
          <button onClick={pickDir}
            className="px-3 py-2 border border-[#242a3c] hover:border-[#5b7cfa]/40 rounded-lg text-xs text-[#5a6280] hover:text-[#8b92a8] transition-all">
            Обзор
          </button>
        </div>
      </div>

      {phase === 'error' && error && (
        <div className="bg-red-500/8 border border-red-500/20 text-red-400 rounded-xl px-4 py-3 text-sm">{error}</div>
      )}

      <button onClick={startReceive} disabled={!canStart}
        className={`w-full py-3.5 rounded-xl font-bold text-sm transition-all ${
          canStart
            ? 'bg-gradient-to-r from-[#5b7cfa] to-[#a78bfa] text-white hover:opacity-90 hover:-translate-y-0.5 shadow-lg shadow-[#5b7cfa]/20'
            : 'bg-[#111520] text-[#3d4562] border border-[#242a3c] cursor-not-allowed'
        }`}>
        ⚡ Получить файл
      </button>
    </div>
  )
}

function FormField({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div>
      <label className="block text-xs text-[#5a6280] mb-1.5">{label}</label>
      {children}
    </div>
  )
}

function KeyField({ value, onChange, visible, onToggle }: {
  value: string; onChange: (v: string) => void; visible: boolean; onToggle: () => void
}) {
  return (
    <FormField label="Ключ">
      <div className="flex gap-1">
        <input
          type={visible ? 'text' : 'password'}
          value={value}
          onChange={e => onChange(e.target.value)}
          placeholder="Вставьте ключ"
          className="flex-1 bg-[#0c0f16] border border-[#242a3c] focus:border-[#a78bfa] rounded-lg px-3 py-2 text-xs outline-none font-mono text-[#8b92a8]"
        />
        <button onClick={onToggle}
          className="px-2 border border-[#242a3c] rounded-lg text-[#5a6280] hover:text-[#8b92a8] text-sm transition-colors">
          {visible ? '🙈' : '👁'}
        </button>
      </div>
    </FormField>
  )
}
