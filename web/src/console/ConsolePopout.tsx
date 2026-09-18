import { useEffect, useState } from 'react'
import { ConsoleDialog } from './ConsoleDialog'
import type { ConsoleSession } from './types'

type PopoutPayload = { type: 'server-control:console-session'; serverName: string; session: ConsoleSession }

function isPayload(value: unknown): value is PopoutPayload {
  if (!value || typeof value !== 'object') return false
  const payload = value as Partial<PopoutPayload>
  return payload.type === 'server-control:console-session'
    && typeof payload.serverName === 'string'
    && typeof payload.session?.ticket === 'string'
    && payload.session.protocol === 'rfb'
}

export function ConsolePopout() {
  const [payload, setPayload] = useState<PopoutPayload | null>(null)

  useEffect(() => {
    const opener = window.opener
    if (!opener) return
    function receiveSession(event: MessageEvent) {
      if (event.origin !== window.location.origin || event.source !== opener || !isPayload(event.data)) return
      setPayload(event.data)
    }
    window.addEventListener('message', receiveSession)
    opener.postMessage({ type: 'server-control:console-ready' }, window.location.origin)
    return () => window.removeEventListener('message', receiveSession)
  }, [])

  if (!window.opener) return <main className="loading-screen"><p>控制台窗口必须从服务器详情页打开。</p></main>
  if (!payload) return <main className="loading-screen"><div className="loading-mark">SC</div><p>正在建立一次性控制台会话…</p></main>
  return <ConsoleDialog serverName={payload.serverName} session={payload.session} onClose={() => window.close()} />
}
