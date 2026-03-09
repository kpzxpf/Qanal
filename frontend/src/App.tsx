import { useState, useEffect } from 'react'
import SendPanel from './components/SendPanel'
import ReceivePanel from './components/ReceivePanel'
import { GetLocalServerURL } from './lib/app'

type Tab = 'send' | 'receive'

export default function App() {
  const [tab, setTab] = useState<Tab>('send')
  const [serverURL, setServerURL] = useState('http://localhost:8080')
  const [urlReady, setUrlReady] = useState(false)

  useEffect(() => {
    GetLocalServerURL().then(url => { setServerURL(url); setUrlReady(true) }).catch(() => {})
  }, [])

  return (
    <div className="flex flex-col h-screen bg-[#0a0c10] text-[#e4e7f0]">
      {/* Header */}
      <header className="flex items-center gap-3 px-6 py-3.5 bg-[#0f1117] border-b border-[#1e2235] select-none shrink-0">
        <div className="flex items-center gap-2.5">
          <div className="w-7 h-7 rounded-lg bg-gradient-to-br from-[#5b7cfa] to-[#a78bfa] flex items-center justify-center">
            <span className="text-xs font-black text-white">Q</span>
          </div>
          <span className="text-sm font-bold text-white tracking-wide">Qanal</span>
        </div>

        <div className="mx-3 h-4 w-px bg-[#1e2235]" />

        <div className="flex items-center gap-2 min-w-0">
          <span className={`w-1.5 h-1.5 rounded-full shrink-0 ${urlReady ? 'bg-[#34d399]' : 'bg-[#fbbf24] animate-pulse'}`} />
          <span className="text-[#4a5068] text-xs font-mono truncate">{serverURL}</span>
        </div>

        {/* Tab switcher */}
        <div className="ml-auto flex bg-[#161920] border border-[#1e2235] rounded-lg p-0.5">
          {(['send', 'receive'] as Tab[]).map(t => (
            <button
              key={t}
              onClick={() => setTab(t)}
              className={`px-4 py-1.5 rounded-md text-xs font-semibold transition-all ${
                tab === t
                  ? 'bg-gradient-to-r from-[#5b7cfa] to-[#a78bfa] text-white shadow-sm'
                  : 'text-[#8b92a8] hover:text-[#e4e7f0]'
              }`}
            >
              {t === 'send' ? '↑ Отправить' : '↓ Получить'}
            </button>
          ))}
        </div>
      </header>

      {/* Content */}
      <main className="flex-1 overflow-y-auto">
        <div className="max-w-xl mx-auto px-5 py-5">
          {tab === 'send'
            ? <SendPanel defaultServerURL={serverURL} />
            : <ReceivePanel defaultServerURL={serverURL} />
          }
        </div>
      </main>
    </div>
  )
}
