import { useEffect, useRef, useState } from 'react'
import type { FormEvent } from 'react'
import type { ConsoleSession } from './types'

function localSocketURL(ticket: string) {
  const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:'
  return `${protocol}//${window.location.host}/ws/console/${encodeURIComponent(ticket)}`
}

export function ConsoleDialog({ serverName, session, onClose }: { serverName: string; session: ConsoleSession; onClose(): void }) {
  const [status, setStatus] = useState('正在连接')
  const isVNC = session.protocol === 'rfb'

  return <div className="modal-backdrop console-backdrop" onClick={onClose}>
    <section className={`console-dialog ${isVNC ? 'vnc-dialog' : ''}`} role="dialog" aria-modal="true" aria-label={`${serverName} 控制台`} onClick={(event) => event.stopPropagation()}>
      <div className="console-header"><div><p className="eyebrow">{isVNC ? 'Embedded VNC' : 'Embedded console'}</p><h3>{serverName} 控制台</h3></div><div><span className="console-status">{status}</span><button aria-label="关闭控制台" onClick={onClose}>×</button></div></div>
      {isVNC
        ? <VNCConsole session={session} setStatus={setStatus} />
        : <TerminalConsole session={session} setStatus={setStatus} />}
      <p className="field-note">会话内容不会被面板记录。关闭窗口后票据不可再次使用。</p>
    </section>
  </div>
}

function VNCConsole({ session, setStatus }: { session: ConsoleSession; setStatus(value: string): void }) {
  const screen = useRef<HTMLDivElement | null>(null)

  useEffect(() => {
    let disposed = false
    let client: import('@novnc/novnc').default | undefined
    let target: HTMLDivElement | null = null
    async function connect() {
      try {
        const { default: RFB } = await import('@novnc/novnc')
        if (disposed || !screen.current) return
        target = screen.current
        const password = session.credentials?.password ?? ''
        client = new RFB(target, localSocketURL(session.ticket), { credentials: { password } })
        client.scaleViewport = true
        client.resizeSession = true
        client.clipViewport = true
        client.focusOnClick = true
        client.addEventListener('connect', () => { if (!disposed) setStatus('已连接') })
        client.addEventListener('disconnect', (event) => {
          if (!disposed) setStatus((event as CustomEvent<{ clean: boolean }>).detail.clean ? '已断开' : '连接错误')
        })
        client.addEventListener('securityfailure', () => { if (!disposed) setStatus('VNC 验证失败') })
        client.addEventListener('credentialsrequired', () => client?.sendCredentials({ password }))
      } catch {
        if (!disposed) setStatus('无法加载 VNC 客户端')
      }
    }
    void connect()
    return () => {
      disposed = true
      client?.disconnect()
      target?.replaceChildren()
    }
  }, [session.credentials?.password, session.ticket, setStatus])

  return <div className="vnc-screen" ref={screen} aria-label="VNC 画面" />
}

function TerminalConsole({ session, setStatus }: { session: ConsoleSession; setStatus(value: string): void }) {
  const socket = useRef<WebSocket | null>(null)
  const [output, setOutput] = useState('')
  const [input, setInput] = useState('')

  useEffect(() => {
    const connection = new WebSocket(localSocketURL(session.ticket))
    socket.current = connection
    connection.onopen = () => setStatus('已连接')
    connection.onmessage = (event) => {
      const next = typeof event.data === 'string' ? event.data : '[binary frame]'
      setOutput((current) => `${current}${next}`)
    }
    connection.onerror = () => setStatus('连接错误')
    connection.onclose = () => setStatus('已断开')
    return () => {
      connection.close()
      socket.current = null
    }
  }, [session.ticket, setStatus])

  function send(event: FormEvent) {
    event.preventDefault()
    if (!input || socket.current?.readyState !== WebSocket.OPEN) return
    socket.current.send(input)
    setInput('')
  }

  return <>
    <pre className="console-output" aria-label="控制台输出">{output || '等待控制台输出…'}</pre>
    <form className="console-input" onSubmit={send}><label>控制台输入<input aria-label="控制台输入" value={input} onChange={(event) => setInput(event.target.value)} autoComplete="off" /></label><button type="submit">发送</button></form>
  </>
}
