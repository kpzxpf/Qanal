import { useState, useEffect, useCallback } from 'react'
import { EventsOn, EventsOff } from '../../wailsjs/runtime/runtime'
import { SelectFile, GetFileInfo, SendFile, StartPeerSend, StopPeerSend } from '../../wailsjs/go/main/App'
import type { transfer } from '../../wailsjs/go/models'
import { fmt, fmtDur } from '../utils/format'

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
  const [copied, setCopied] = useState<string | null>(null)

  useEffect(() => {
    if (serverURL === 'http://localhost:8080' && defaultServerURL !== 'http://localhost:8080') {
      setServerURL(defaultServerURL)
    }
  }, [defaultServerURL, serverURL])

  // Always clean up event listeners before resetting UI state to prevent leaks.
  const reset = useCallback(() => {
    EventsOff('transfer:progress')
    EventsOff('transfer:error')
    EventsOff('transfer:complete')
    setPhase('idle')
    setProgress(null)
    setPeerInfo(null)
    setRelayResult(null)
    setError('')
  }, [])

  const pickFile = useCallback(async () => {
    const path = await SelectFile()
    if (!path) return
    const info = await GetFileInfo(path)
    setFilePath(path)
    setFileInfo(info)
    reset()
  }, [reset])

  const startTransfer = useCallback(async () => {
    if (!fileInfo) return

    setPhase('starting')
    setError('')
    setProgress(null)

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
        // Serve() runs in background goroutine — events will arrive when receiver connects
      } else {
        setPhase('transferring')
        await SendFile(serverURL, filePath, chunkMB, workers)
      }
    } catch (e: any) {
      setError(e?.message || String(e)); setPhase('error')
      EventsOff('transfer:progress'); EventsOff('transfer:error'); EventsOff('transfer:complete')
    }
  }, [mode, filePath, fileInfo, serverURL, chunkMB, workers])

  const cancel = useCallback(async () => {
    EventsOff('transfer:progress'); EventsOff('transfer:error'); EventsOff('transfer:complete')
    if (mode === 'p2p') await StopPeerSend()
    reset()
  }, [mode, reset])

  const copy = (text: string, id: string) => {
    navigator.clipboard.writeText(text)
    setCopied(id); setTimeout(() => setCopied(null), 2000)
  }

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
          File goes <strong className="text-[#5b7cfa]">directly</strong> to receiver — no server hop, zstd compressed, AES-256-GCM encrypted
        </div>
      )}

      {/* File picker */}
      <div className="bg-[#161920] border border-[#2a2f42] rounded-xl p-5">
        <div className="text-xs text-[#8b92a8] font-semibold uppercase tracking-widest mb-3">File</div>
        {fileInfo ? (
          <div className="flex items-center gap-3">
            <div className="text-3xl">📄</div>
            <div className="flex-1 min-w-0">
              <div className="font-semibold truncate">{fileInfo.name}</div>
              <div className="text-sm text-[#8b92a8]">{fmt(fileInfo.size)}</div>
            </div>
            <button onClick={pickFile} className="text-xs text-[#8b92a8] hover:text-[#e4e7f0] border border-[#2a2f42] hover:border-[#5b7cfa] px-3 py-1.5 rounded-lg">
              Change
            </button>
          </div>
        ) : (
          <button onClick={pickFile} className="w-full border-2 border-dashed border-[#2a2f42] hover:border-[#5b7cfa] rounded-lg py-8 text-center group transition-all">
            <div className="text-3xl mb-2 opacity-50 group-hover:opacity-80">📂</div>
            <div className="text-sm font-medium text-[#8b92a8] group-hover:text-[#e4e7f0]">Click to select file</div>
            <div className="text-xs text-[#8b92a8] mt-1">Any type · Up to 100 GB</div>
          </button>
        )}
      </div>

      {/* Settings */}
      <div className="bg-[#161920] border border-[#2a2f42] rounded-xl p-5">
        <div className="text-xs text-[#8b92a8] font-semibold uppercase tracking-widest mb-3">Settings</div>
        <div className={`grid gap-3 ${mode === 'relay' ? 'grid-cols-3' : 'grid-cols-2'}`}>
          {mode === 'relay' && (
            <div>
              <label className="block text-xs text-[#8b92a8] mb-1.5">Server URL</label>
              <input type="text" value={serverURL} onChange={e => setServerURL(e.target.value)}
                className="w-full bg-[#0d0f14] border border-[#2a2f42] focus:border-[#5b7cfa] rounded-lg px-3 py-2 text-sm outline-none font-mono" />
            </div>
          )}
          <div>
            <label className="block text-xs text-[#8b92a8] mb-1.5">Chunk size</label>
            <select value={chunkMB} onChange={e => setChunkMB(Number(e.target.value))}
              className="w-full bg-[#0d0f14] border border-[#2a2f42] rounded-lg px-3 py-2 text-sm outline-none">
              <option value={5}>5 MB</option>
              <option value={10}>10 MB (default)</option>
              <option value={25}>25 MB</option>
              <option value={50}>50 MB (LAN)</option>
            </select>
          </div>
          {mode === 'relay' && (
            <div>
              <label className="block text-xs text-[#8b92a8] mb-1.5">Workers</label>
              <select value={workers} onChange={e => setWorkers(Number(e.target.value))}
                className="w-full bg-[#0d0f14] border border-[#2a2f42] rounded-lg px-3 py-2 text-sm outline-none">
                <option value={4}>4</option><option value={8}>8</option><option value={16}>16</option>
              </select>
            </div>
          )}
        </div>
      </div>

      {/* Action button */}
      {phase === 'idle' || phase === 'error' ? (
        <button onClick={startTransfer} disabled={!fileInfo}
          className={`w-full py-3 rounded-xl font-semibold text-sm transition-all ${
            fileInfo ? 'bg-gradient-to-r from-[#5b7cfa] to-[#a78bfa] text-white hover:opacity-90 hover:-translate-y-0.5 shadow-lg'
                     : 'bg-[#1e2230] text-[#8b92a8] cursor-not-allowed'}`}>
          {mode === 'p2p' ? '⚡ Start P2P Transfer' : '↑ Send via Relay'}
        </button>
      ) : phase === 'waiting' ? (
        <button onClick={cancel} className="w-full py-3 rounded-xl font-semibold text-sm bg-[#1e2230] text-[#8b92a8] border border-[#2a2f42] hover:border-red-500/50 hover:text-red-400">
          Cancel waiting
        </button>
      ) : null}

      {/* Error */}
      {phase === 'error' && error && (
        <div className="bg-red-500/10 border border-red-500/30 text-red-400 rounded-xl px-4 py-3 text-sm">{error}</div>
      )}

      {/* P2P waiting — show credentials */}
      {mode === 'p2p' && (phase === 'waiting' || phase === 'transferring' || phase === 'done') && peerInfo && (
        <div className="bg-[#161920] border border-[#2a2f42] rounded-xl p-5 space-y-3">
          {phase === 'waiting' && (
            <div className="flex items-center gap-2 text-sm text-[#8b92a8]">
              <span className="w-2 h-2 rounded-full bg-[#fbbf24] animate-pulse"></span>
              Waiting for receiver to connect...
            </div>
          )}
          {(['address', 'code', 'key'] as const).map(k => (
            <div key={k} className="bg-[#0d0f14] rounded-lg p-3">
              <div className="text-xs text-[#8b92a8] uppercase tracking-widest mb-1">
                {k === 'address' ? 'Peer Address' : k === 'code' ? 'Auth Code' : 'Encryption Key'}
              </div>
              <div className="flex items-center gap-2">
                <span className={`font-mono break-all flex-1 ${k === 'key' ? 'text-xs text-[#a78bfa]' : 'text-base font-bold text-[#5b7cfa]'}`}>
                  {peerInfo[k]}
                </span>
                <button onClick={() => copy(peerInfo[k], k)}
                  className="shrink-0 text-xs px-2 py-1 border border-[#2a2f42] hover:border-[#5b7cfa] rounded text-[#8b92a8] hover:text-[#e4e7f0]">
                  {copied === k ? '✓' : '📋'}
                </button>
              </div>
            </div>
          ))}
        </div>
      )}

      {/* Relay result */}
      {mode === 'relay' && phase === 'done' && relayResult && (
        <div className="bg-[#161920] border border-[#34d399]/30 rounded-xl p-5 space-y-3">
          <div className="text-sm font-semibold text-[#34d399]">✅ Share with recipient</div>
          {(['code', 'key'] as const).map(k => (
            <div key={k} className="bg-[#0d0f14] rounded-lg p-3">
              <div className="text-xs text-[#8b92a8] uppercase tracking-widest mb-1">{k === 'code' ? 'Transfer Code' : 'Encryption Key'}</div>
              <div className="flex items-center gap-2">
                <span className={`font-mono break-all flex-1 ${k === 'key' ? 'text-xs text-[#a78bfa]' : 'text-xl font-bold text-[#5b7cfa] tracking-widest'}`}>
                  {relayResult[k]}
                </span>
                <button onClick={() => copy(relayResult[k], k)}
                  className="shrink-0 text-xs px-2 py-1 border border-[#2a2f42] hover:border-[#5b7cfa] rounded text-[#8b92a8] hover:text-[#e4e7f0]">
                  {copied === k ? '✓' : '📋'}
                </button>
              </div>
            </div>
          ))}
        </div>
      )}

      {/* Progress */}
      {(phase === 'transferring' || phase === 'done') && progress && (
        <div className="bg-[#161920] border border-[#2a2f42] rounded-xl p-5">
          <div className="flex justify-between text-sm mb-2">
            <span className="text-[#8b92a8]">{phase === 'done' ? '✅ Complete' : `${progress.done}/${progress.total} chunks`}</span>
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
    </div>
  )
}
