import { useState, useEffect } from 'react'
import SendPanel from './components/SendPanel'
import ReceivePanel from './components/ReceivePanel'
import { GetLocalServerURL } from '../wailsjs/go/main/App'

type Tab = 'send' | 'receive'

export default function App() {
  const [tab, setTab] = useState<Tab>('send')
  const [serverURL, setServerURL] = useState('http://localhost:8080')
  const [urlReady, setUrlReady] = useState(false)

  useEffect(() => {
    GetLocalServerURL().then(url => { setServerURL(url); setUrlReady(true) }).catch(() => {})
  }, [])

  return (
    <div className="flex flex-col h-screen bg-[#0d0f14] text-[#e4e7f0]">
      {/* Header */}
      <header className="flex items-center gap-3 px-5 py-3 bg-[#161920] border-b border-[#2a2f42] select-none shrink-0">
        <span className="text-xl">⚡</span>
        <span className="text-base font-bold bg-gradient-to-r from-[#5b7cfa] to-[#a78bfa] bg-clip-text text-transparent">
          Qanal
        </span>
        <div className="ml-auto flex items-center gap-2">
          <span className={`w-1.5 h-1.5 rounded-full ${urlReady ? 'bg-[#34d399] shadow-[0_0_5px_#34d399]' : 'bg-[#fbbf24]'}`} />
          <span className="text-[#4a5068] text-xs font-mono truncate max-w-[220px]">{serverURL}</span>
        </div>
      </header>

      {/* Tabs */}
      <div className="flex gap-1 px-5 pt-4 shrink-0">
        <button
          onClick={() => setTab('send')}
          className={`px-5 py-2 rounded-lg text-sm font-semibold transition-all ${
            tab === 'send'
              ? 'bg-gradient-to-r from-[#5b7cfa] to-[#a78bfa] text-white shadow-lg'
              : 'text-[#8b92a8] hover:text-[#e4e7f0] hover:bg-[#1e2230]'
          }`}
        >
          ↑ Отправить
        </button>
        <button
          onClick={() => setTab('receive')}
          className={`px-5 py-2 rounded-lg text-sm font-semibold transition-all ${
            tab === 'receive'
              ? 'bg-gradient-to-r from-[#5b7cfa] to-[#a78bfa] text-white shadow-lg'
              : 'text-[#8b92a8] hover:text-[#e4e7f0] hover:bg-[#1e2230]'
          }`}
        >
          ↓ Получить
        </button>
      </div>

      {/* Content */}
      <main className="flex-1 overflow-y-auto px-5 py-4">
        {tab === 'send'
          ? <SendPanel defaultServerURL={serverURL} />
          : <ReceivePanel defaultServerURL={serverURL} />
        }
      </main>
    </div>
  )
}
