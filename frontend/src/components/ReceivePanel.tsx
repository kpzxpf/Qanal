import { useState, useEffect, useCallback } from 'react'
import { EventsOn, EventsOff } from '../../wailsjs/runtime/runtime'
import { SelectDirectory, ReceiveFile, PeerReceive } from '../../wailsjs/go/main/App'
import { fmt, fmtDur } from '../utils/format'

type Mode = 'relay' | 'p2p'
type Phase = 'idle' | 'downloading' | 'done' | 'error'

interface Progress {
  done: number
  total: number
  bytesDone: number
  totalBytes: number
  speedBps: number
}

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

  // Keep serverURL in sync with the LAN IP resolved after startup.
  useEffect(() => {
    if (serverURL === 'http://localhost:8080' && defaultServerURL !== 'http://localhost:8080') {
      setServerURL(defaultServerURL)
    }
  }, [defaultServerURL, serverURL])

  const reset = useCallback(() => {
    setPhase('idle')
    setProgress(null)
    setSavedPath('')
    setError('')
  }, [])

  const pickDir = useCallback(async () => {
    const dir = await SelectDirectory()
    if (dir) setOutDir(dir)
  }, [])

  const canStart = mode === 'p2p'
    ? peerAddr.trim() !== '' && code.trim() !== '' && key.trim() !== ''
    : code.trim() !== '' && key.trim() !== ''

  const startReceive = useCallback(async () => {
    if (!canStart) return
    setPhase('downloading')
    setProgress(null)
    setError('')
    setSavedPath('')

    const saveDir = outDir || '.'

    EventsOn('transfer:progress', (e: Progress) => setProgress(e))
    EventsOn('transfer:error', (e: { message: string }) => {
      setError(e.message)
      setPhase('error')
      EventsOff('transfer:progress')
      EventsOff('transfer:error')
      EventsOff('transfer:complete')
    })
    EventsOn('transfer:complete', (e: { path: string }) => {
      setSavedPath(e.path)
      setPhase('done')
      EventsOff('transfer:progress')
      EventsOff('transfer:error')
      EventsOff('transfer:complete')
    })

    try {
      if (mode === 'p2p') {
        await PeerReceive(peerAddr.trim(), code.trim().toUpperCase(), key.trim(), saveDir)
      } else {
        await ReceiveFile(serverURL, code.trim().toUpperCase(), key.trim(), saveDir, workers)
      }
    } catch (e: any) {
      setError(e?.message || String(e))
      setPhase('error')
      EventsOff('transfer:progress')
      EventsOff('transfer:error')
      EventsOff('transfer:complete')
    }
  }, [mode, peerAddr, serverURL, code, key, outDir, workers, canStart])

  const pct = progress ? Math.round((progress.done / progress.total) * 100) : 0

  return (
    <div className="space-y-3 max-w-2xl">
      {/* Mode selector */}
      <div className="flex gap-1 bg-[#0d0f14] border border-[#2a2f42] rounded-xl p-1">
        {([['p2p', '⚡ P2P Direct (fastest)'], ['relay', '☁ Relay (via server)']] as [Mode, string][]).map(([m, label]) => (
          <button key={m} onClick={() => { setMode(m); reset() }}
            className={`flex-1 py-2 text-sm font-semibold rounded-lg transition-all ${
              mode === m ? 'bg-gradient-to-r from-[#5b7cfa] to-[#a78bfa] text-white shadow' : 'text-[#8b92a8] hover:text-[#e4e7f0]'
            }`}>
            {label}
          </button>
        ))}
      </div>

      {mode === 'p2p' && (
        <div className="bg-[#5b7cfa]/8 border border-[#5b7cfa]/20 rounded-xl px-4 py-2.5 text-xs text-[#8b92a8]">
          File arrives <strong className="text-[#5b7cfa]">directly</strong> from sender — no server hop, zstd compressed, AES-256-GCM encrypted
        </div>
      )}

      {/* Connection fields */}
      <div className="bg-[#161920] border border-[#2a2f42] rounded-xl p-5 space-y-3">
        <div className="text-xs text-[#8b92a8] font-semibold uppercase tracking-widest">Connection</div>

        {mode === 'p2p' ? (
          <>
            <div>
              <label className="block text-xs text-[#8b92a8] mb-1.5">Peer Address</label>
              <input
                type="text"
                value={peerAddr}
                onChange={e => setPeerAddr(e.target.value)}
                placeholder="192.168.1.5:54321"
                className="w-full bg-[#0d0f14] border border-[#2a2f42] focus:border-[#5b7cfa] rounded-lg px-3 py-2 text-sm outline-none font-mono text-[#5b7cfa]"
              />
            </div>
            <div className="grid grid-cols-2 gap-3">
              <div>
                <label className="block text-xs text-[#8b92a8] mb-1.5">Auth Code</label>
                <input
                  type="text"
                  value={code}
                  onChange={e => setCode(e.target.value.toUpperCase())}
                  placeholder="ABCD1234"
                  maxLength={8}
                  className="w-full bg-[#0d0f14] border border-[#2a2f42] focus:border-[#5b7cfa] rounded-lg px-3 py-2 text-sm outline-none font-mono font-bold tracking-widest text-[#5b7cfa]"
                />
              </div>
              <div>
                <label className="block text-xs text-[#8b92a8] mb-1.5">Encryption Key</label>
                <div className="flex gap-1">
                  <input
                    type={keyVisible ? 'text' : 'password'}
                    value={key}
                    onChange={e => setKey(e.target.value)}
                    placeholder="Paste key from sender"
                    className="flex-1 bg-[#0d0f14] border border-[#2a2f42] focus:border-[#a78bfa] rounded-lg px-3 py-2 text-xs outline-none font-mono"
                  />
                  <button
                    onClick={() => setKeyVisible(v => !v)}
                    className="px-2 border border-[#2a2f42] rounded-lg text-[#8b92a8] hover:text-[#e4e7f0] text-sm"
                  >
                    {keyVisible ? '🙈' : '👁'}
                  </button>
                </div>
              </div>
            </div>
          </>
        ) : (
          <>
            <div className="grid grid-cols-3 gap-3">
              <div className="col-span-2">
                <label className="block text-xs text-[#8b92a8] mb-1.5">Server URL</label>
                <input
                  type="text"
                  value={serverURL}
                  onChange={e => setServerURL(e.target.value)}
                  placeholder="http://192.168.1.5:8080"
                  className="w-full bg-[#0d0f14] border border-[#2a2f42] focus:border-[#5b7cfa] rounded-lg px-3 py-2 text-sm outline-none font-mono"
                />
              </div>
              <div>
                <label className="block text-xs text-[#8b92a8] mb-1.5">Workers</label>
                <select value={workers} onChange={e => setWorkers(Number(e.target.value))}
                  className="w-full bg-[#0d0f14] border border-[#2a2f42] rounded-lg px-3 py-2 text-sm outline-none">
                  <option value={4}>4</option><option value={8}>8</option><option value={16}>16</option>
                </select>
              </div>
            </div>
            <div className="grid grid-cols-2 gap-3">
              <div>
                <label className="block text-xs text-[#8b92a8] mb-1.5">Transfer Code</label>
                <input
                  type="text"
                  value={code}
                  onChange={e => setCode(e.target.value.toUpperCase())}
                  placeholder="ABCD1234"
                  maxLength={8}
                  className="w-full bg-[#0d0f14] border border-[#2a2f42] focus:border-[#5b7cfa] rounded-lg px-3 py-2 text-sm outline-none font-mono font-bold tracking-widest text-[#5b7cfa]"
                />
              </div>
              <div>
                <label className="block text-xs text-[#8b92a8] mb-1.5">Encryption Key</label>
                <div className="flex gap-1">
                  <input
                    type={keyVisible ? 'text' : 'password'}
                    value={key}
                    onChange={e => setKey(e.target.value)}
                    placeholder="Paste key from sender"
                    className="flex-1 bg-[#0d0f14] border border-[#2a2f42] focus:border-[#a78bfa] rounded-lg px-3 py-2 text-xs outline-none font-mono"
                  />
                  <button
                    onClick={() => setKeyVisible(v => !v)}
                    className="px-2 border border-[#2a2f42] rounded-lg text-[#8b92a8] hover:text-[#e4e7f0] text-sm"
                  >
                    {keyVisible ? '🙈' : '👁'}
                  </button>
                </div>
              </div>
            </div>
          </>
        )}

        <div>
          <label className="block text-xs text-[#8b92a8] mb-1.5">Save to</label>
          <div className="flex gap-2">
            <input
              type="text"
              value={outDir}
              onChange={e => setOutDir(e.target.value)}
              placeholder="Downloads folder (default)"
              className="flex-1 bg-[#0d0f14] border border-[#2a2f42] focus:border-[#5b7cfa] rounded-lg px-3 py-2 text-sm outline-none font-mono"
            />
            <button
              onClick={pickDir}
              className="px-3 py-2 border border-[#2a2f42] hover:border-[#5b7cfa] rounded-lg text-sm text-[#8b92a8] hover:text-[#e4e7f0]"
            >
              Browse
            </button>
          </div>
        </div>
      </div>

      {/* Download button */}
      <button
        onClick={startReceive}
        disabled={!canStart || phase === 'downloading'}
        className={`w-full py-3 rounded-xl font-semibold text-sm transition-all ${
          canStart && phase !== 'downloading'
            ? 'bg-gradient-to-r from-[#5b7cfa] to-[#a78bfa] text-white hover:opacity-90 hover:-translate-y-0.5 shadow-lg'
            : 'bg-[#1e2230] text-[#8b92a8] cursor-not-allowed'
        }`}
      >
        {phase === 'downloading' ? (
          <span className="flex items-center justify-center gap-2">
            <span className="w-4 h-4 border-2 border-white/30 border-t-white rounded-full animate-spin"></span>
            {mode === 'p2p' ? 'Receiving...' : 'Downloading...'}
          </span>
        ) : mode === 'p2p' ? '⚡ Receive via P2P' : '↓ Download via Relay'}
      </button>

      {/* Error */}
      {phase === 'error' && (
        <div className="bg-red-500/10 border border-red-500/30 text-red-400 rounded-xl px-4 py-3 text-sm">
          {error}
        </div>
      )}

      {/* Progress */}
      {(phase === 'downloading' || phase === 'done') && progress && (
        <div className="bg-[#161920] border border-[#2a2f42] rounded-xl p-5">
          <div className="flex justify-between text-sm mb-2">
            <span className="text-[#8b92a8]">
              {phase === 'done' ? '✅ Complete' : `${progress.done}/${progress.total} chunks`}
            </span>
            <span className="font-semibold">{pct}%</span>
          </div>
          <div className="h-2 bg-[#0d0f14] rounded-full overflow-hidden mb-2">
            <div
              className="h-full bg-gradient-to-r from-[#5b7cfa] to-[#a78bfa] rounded-full transition-all duration-300"
              style={{ width: `${pct}%` }}
            />
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

      {/* Done */}
      {phase === 'done' && savedPath && (
        <div className="bg-[#161920] border border-[#34d399]/30 rounded-xl px-5 py-4">
          <div className="text-sm font-semibold text-[#34d399] mb-1">✅ File saved</div>
          <div className="font-mono text-xs text-[#8b92a8] break-all">{savedPath}</div>
        </div>
      )}
    </div>
  )
}
