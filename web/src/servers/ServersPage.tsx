import { useCallback, useEffect, useMemo, useState } from 'react'
import { listConnections } from '../connections/api'
import type { ProviderConnection } from '../connections/types'
import { ConsoleDialog } from '../console/ConsoleDialog'
import { createEmbeddedConsole, openConsoleWindow, openProviderPortal } from '../console/api'
import type { ConsoleSession } from '../console/types'
import { useServerEvents } from '../events/useServerEvents'
import { requestPowerAction } from '../operations/api'
import type { PowerAction } from '../operations/types'
import { listServers } from './api'
import type { Server } from './types'

const stateLabels: Record<string, string> = { running: '运行中', stopped: '已关机', pending: '启动中', stopping: '关机中', rebooting: '重启中', error: '错误', unknown: '未知' }
const actionLabels: Record<PowerAction, string> = { start: '开机', stop: '关机', reboot: '重启' }

export function ServersPage() {
  const [servers, setServers] = useState<Server[]>([])
  const [connections, setConnections] = useState<ProviderConnection[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [state, setState] = useState('all')
  const [providerType, setProviderType] = useState('all')
  const [connectionID, setConnectionID] = useState('all')
  const [query, setQuery] = useState('')
  const [selectedID, setSelectedID] = useState<string | null>(null)
  const [confirmation, setConfirmation] = useState<PowerAction | null>(null)
  const [submitting, setSubmitting] = useState(false)
  const [notice, setNotice] = useState('')
  const [actionError, setActionError] = useState('')
  const [consoleSession, setConsoleSession] = useState<ConsoleSession | null>(null)
  const [consoleServerName, setConsoleServerName] = useState('')

  const refreshServers = useCallback(async () => {
    const items = await listServers()
    setServers(items)
    return items
  }, [])

  useEffect(() => {
    let active = true
    void Promise.all([listServers(), listConnections()]).then(([items, providerConnections]) => {
      if (!active) return
      setServers(items)
      setConnections(providerConnections)
      const directID = window.location.pathname.match(/^\/servers\/([^/]+)$/)?.[1]
      if (directID && items.some((item) => item.id === decodeURIComponent(directID))) setSelectedID(decodeURIComponent(directID))
    }).catch(() => { if (active) setError('无法加载服务器清单') }).finally(() => { if (active) setLoading(false) })
    return () => { active = false }
  }, [])
  useServerEvents(() => { void refreshServers().catch(() => setError('无法刷新服务器状态')) })

  const connectionByID = useMemo(() => new Map(connections.map((connection) => [connection.id, connection])), [connections])
  const providerTypes = useMemo(() => [...new Set(connections.map((connection) => connection.provider_type))].sort(), [connections])
  const selected = useMemo(() => servers.find((server) => server.id === selectedID) ?? null, [servers, selectedID])
  const visible = useMemo(() => servers.filter((server) => {
    const connection = connectionByID.get(server.connection_id)
    return (state === 'all' || server.state === state)
      && (providerType === 'all' || connection?.provider_type === providerType)
      && (connectionID === 'all' || server.connection_id === connectionID)
      && server.name.toLowerCase().includes(query.trim().toLowerCase())
  }), [servers, state, query, providerType, connectionID, connectionByID])
  function openDetail(server: Server) {
    window.history.pushState({}, '', `/servers/${encodeURIComponent(server.id)}`)
    setSelectedID(server.id)
    setNotice('')
    setActionError('')
  }
  function closeDetail() {
    window.history.pushState({}, '', '/servers')
    setSelectedID(null)
    setConfirmation(null)
  }
  async function confirmAction() {
    if (!selected || !confirmation) return
    const label = actionLabels[confirmation]
    setSubmitting(true)
    setActionError('')
    try {
      await requestPowerAction(selected.id, confirmation, crypto.randomUUID())
      setConfirmation(null)
      setNotice(`${label}操作已排队`)
      await refreshServers()
    } catch {
      setActionError(`${label}操作提交失败，请稍后重试`)
    } finally {
      setSubmitting(false)
    }
  }
  async function handleConsole(mode: 'embedded' | 'window' | 'portal') {
    if (!selected) return
    setActionError('')
    try {
      if (mode === 'embedded') {
        const session = await createEmbeddedConsole(selected.id)
        setConsoleServerName(selected.name)
        setConsoleSession(session)
        return
      }
      const opened = mode === 'window' ? await openConsoleWindow(selected.id) : await openProviderPortal(selected.id)
      if (!opened) setNotice('链接已生成；如果没有打开，请允许此站点弹出新窗口')
    } catch {
      setActionError('无法打开控制台，请尝试其他可用方式')
    }
  }
  return <section className="workspace-page">
    <div className="page-heading"><div><p className="eyebrow">Unified inventory</p><h2>服务器</h2><p>所有服务商同步后的本地统一清单。</p></div><span className="count-badge">{visible.length} 台</span></div>
    <div className="filter-bar"><label>搜索服务器<input aria-label="搜索服务器" value={query} onChange={(event) => setQuery(event.target.value)} placeholder="名称" /></label><label>服务商类型<select aria-label="服务商类型" value={providerType} onChange={(event) => { setProviderType(event.target.value); setConnectionID('all') }}><option value="all">全部</option>{providerTypes.map((type) => <option value={type} key={type}>{type}</option>)}</select></label><label>服务商连接<select aria-label="服务商连接" value={connectionID} onChange={(event) => setConnectionID(event.target.value)}><option value="all">全部</option>{connections.filter((connection) => providerType === 'all' || connection.provider_type === providerType).map((connection) => <option value={connection.id} key={connection.id}>{connection.name}</option>)}</select></label><label>运行状态<select aria-label="运行状态" value={state} onChange={(event) => setState(event.target.value)}><option value="all">全部</option><option value="running">运行中</option><option value="stopped">已关机</option><option value="pending">过渡中</option><option value="error">错误</option></select></label></div>
    {error && <p className="form-error" role="alert">{error}</p>}
    {notice && <p className="inline-notice" role="status">{notice}</p>}
    {actionError && <p className="form-error" role="alert">{actionError}</p>}
    {loading ? <p className="empty-state">正在加载服务器…</p> : servers.length === 0 ? <p className="empty-state">还没有同步到服务器</p> : visible.length === 0 ? <p className="empty-state">没有符合筛选条件的服务器</p> : <div className="server-table" role="table">
      <div className="server-row server-head" role="row"><span>服务器</span><span>状态</span><span>范围</span><span>地址</span><span /></div>
      {visible.map((server) => <div className="server-row" role="row" key={server.id}><span><strong>{server.name}</strong><small>{connectionByID.get(server.connection_id)?.name ?? server.external_id}</small></span><span><span className={`state-chip ${server.state}`}>{stateLabels[server.state] ?? server.state}</span></span><span>{server.scope || '—'}</span><span>{server.addresses[0]?.address ?? '—'}</span><span><button aria-label={`查看 ${server.name}`} onClick={() => openDetail(server)}>查看详情</button></span></div>)}
    </div>}
    {selected && <ServerDetail server={selected} connection={connectionByID.get(selected.connection_id)} onClose={closeDetail} onAction={setConfirmation} onConsole={(mode) => void handleConsole(mode)} />}
    {selected && confirmation && <ConfirmationDialog
      action={confirmation}
      server={selected}
      submitting={submitting}
      onCancel={() => setConfirmation(null)}
      onConfirm={() => void confirmAction()}
    />}
    {consoleSession && <ConsoleDialog serverName={consoleServerName} session={consoleSession} onClose={() => setConsoleSession(null)} />}
  </section>
}

function ServerDetail({ server, connection, onClose, onAction, onConsole }: { server: Server; connection?: ProviderConnection; onClose(): void; onAction(action: PowerAction): void; onConsole(mode: 'embedded' | 'window' | 'portal'): void }) {
  const powerActions: PowerAction[] = ['start', 'stop', 'reboot']
  return <div className="detail-backdrop" onClick={onClose}><aside className="detail-panel" role="dialog" aria-label="服务器详情" onClick={(event) => event.stopPropagation()}>
    <div className="detail-header"><div><p className="eyebrow">Server detail</p><h3>{server.name}</h3><p>{server.external_id}</p></div><button aria-label="关闭详情" onClick={onClose}>×</button></div>
    <div className="detail-stats"><div><small>状态</small><strong>{stateLabels[server.state] ?? server.state}</strong></div><div><small>配置</small><strong>{server.spec.cpu ?? '—'} vCPU</strong><span>{server.spec.memory_mb ? `${server.spec.memory_mb} MB` : '—'}</span></div><div><small>服务商连接</small><strong>{connection?.name ?? '未知连接'}</strong><span>{connection?.provider_type ?? '—'}</span></div></div>
    <p className="sync-time">上次同步：{new Date(server.updated_at).toLocaleString()}</p>
    <section><h4>能力</h4><div className="capability-list">{server.capabilities.can_start?.available && <span>可开机</span>}{server.capabilities.can_stop?.available && <span>可关机</span>}{server.capabilities.can_reboot?.available && <span>可重启</span>}{server.capabilities.can_embed_console?.available && <span>可内嵌控制台</span>}{server.capabilities.can_open_console_window?.available && <span>可新窗口控制台</span>}{server.capabilities.has_provider_portal?.available && <span>服务商后台</span>}</div></section>
    <section><h4>网络地址</h4>{server.addresses.length ? server.addresses.map((address) => <p className="address-line" key={address.address}>{address.address}<small>{address.type ?? 'address'}</small></p>) : <p className="muted">没有地址信息</p>}</section>
    <section><h4>电源操作</h4><div className="future-actions">{powerActions.map((action) => {
      const capability = server.capabilities[`can_${action}`]
      return <button disabled={!capability?.available} title={capability?.reason} key={action} onClick={() => onAction(action)}>{actionLabels[action]}</button>
    })}</div></section>
    <section><h4>控制台与后台</h4><div className="future-actions">
      <button disabled={!server.capabilities.can_embed_console?.available} title={server.capabilities.can_embed_console?.reason} onClick={() => onConsole('embedded')}>内嵌控制台</button>
      <button disabled={!server.capabilities.can_open_console_window?.available} title={server.capabilities.can_open_console_window?.reason} onClick={() => onConsole('window')}>新窗口控制台</button>
      <button disabled={!server.capabilities.has_provider_portal?.available} title={server.capabilities.has_provider_portal?.reason} onClick={() => onConsole('portal')}>服务商后台</button>
    </div></section>
  </aside></div>
}

function ConfirmationDialog({ action, server, submitting, onCancel, onConfirm }: { action: PowerAction; server: Server; submitting: boolean; onCancel(): void; onConfirm(): void }) {
  const label = actionLabels[action]
  return <div className="modal-backdrop power-confirmation" onClick={onCancel}>
    <section className="modal-card" role="dialog" aria-modal="true" aria-label={`确认${label}`} onClick={(event) => event.stopPropagation()}>
      <p className="eyebrow">Power operation</p>
      <h3>确认{label}</h3>
      <p>将对服务器 <strong>{server.name}</strong> 执行{label}操作。提交后可在操作记录中查看进度。</p>
      <div className="modal-actions"><button disabled={submitting} onClick={onCancel}>取消</button><button className="primary-button compact" disabled={submitting} onClick={onConfirm}>{submitting ? '正在提交…' : `确认${label}`}</button></div>
    </section>
  </div>
}
