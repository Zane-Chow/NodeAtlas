import { useEffect, useRef, useState } from 'react'
import type { FormEvent } from 'react'
import type { ConsoleSession } from './types'

export function ConsoleDialog({ serverName, session, onClose }: { serverName: string; session: ConsoleSession; onClose(): void }) {
  const socket = useRef<WebSocket | null>(null)
  const [status, setStatus] = useState('正在连接')
  const [output, setOutput] = useState('')
  const [input, setInput] = useState('')

  useEffect(() => {
    const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:'
    const connection = new WebSocket(`${protocol}//${window.location.host}/ws/console/${encodeURIComponent(session.ticket)}`)
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
  }, [session.ticket])

  function send(event: FormEvent) {
    event.preventDefault()
    if (!input || socket.current?.readyState !== WebSocket.OPEN) return
    socket.current.send(input)
    setInput('')
  }

  return <div className="modal-backdrop console-backdrop" onClick={onClose}>
    <section className="console-dialog" role="dialog" aria-modal="true" aria-label={`${serverName} 控制台`} onClick={(event) => event.stopPropagation()}>
      <div className="console-header"><div><p className="eyebrow">Embedded console</p><h3>{serverName} 控制台</h3></div><div><span className="console-status">{status}</span><button aria-label="关闭控制台" onClick={onClose}>×</button></div></div>
      <pre className="console-output" aria-label="控制台输出">{output || '等待控制台输出…'}</pre>
      <form className="console-input" onSubmit={send}><label>控制台输入<input aria-label="控制台输入" value={input} onChange={(event) => setInput(event.target.value)} autoComplete="off" /></label><button type="submit">发送</button></form>
      <p className="field-note">会话内容不会被面板记录。关闭窗口后票据不可再次使用。</p>
    </section>
  </div>
}
