import { useEffect, useMemo, useState } from 'react'
import { listConnections } from '../connections/api'
import type { ProviderConnection } from '../connections/types'
import { listServers } from './api'
import type { Server } from './types'

const stateLabels: Record<string, string> = { running: '运行中', stopped: '已关机', pending: '启动中', stopping: '关机中', rebooting: '重启中', error: '错误', unknown: '未知' }

export function ServersPage() {
  const [servers, setServers] = useState<Server[]>([])
  const [connections, setConnections] = useState<ProviderConnection[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [state, setState] = useState('all')
  const [providerType, setProviderType] = useState('all')
  const [connectionID, setConnectionID] = useState('all')
  const [query, setQuery] = useState('')
  const [selected, setSelected] = useState<Server | null>(null)
  useEffect(() => {
    let active = true
    void Promise.all([listServers(), listConnections()]).then(([items, providerConnections]) => {
      if (!active) return
      setServers(items)
      setConnections(providerConnections)
      const directID = window.location.pathname.match(/^\/servers\/([^/]+)$/)?.[1]
      if (directID) setSelected(items.find((item) => item.id === decodeURIComponent(directID)) ?? null)
    }).catch(() => { if (active) setError('无法加载服务器清单') }).finally(() => { if (active) setLoading(false) })
    return () => { active = false }
  }, [])
  const connectionByID = useMemo(() => new Map(connections.map((connection) => [connection.id, connection])), [connections])
  const providerTypes = useMemo(() => [...new Set(connections.map((connection) => connection.provider_type))].sort(), [connections])
  const visible = useMemo(() => servers.filter((server) => {
    const connection = connectionByID.get(server.connection_id)
    return (state === 'all' || server.state === state)
      && (providerType === 'all' || connection?.provider_type === providerType)
      && (connectionID === 'all' || server.connection_id === connectionID)
      && server.name.toLowerCase().includes(query.trim().toLowerCase())
  }), [servers, state, query, providerType, connectionID, connectionByID])
  function openDetail(server: Server) {
    window.history.pushState({}, '', `/servers/${encodeURIComponent(server.id)}`)
    setSelected(server)
  }
  function closeDetail() {
    window.history.pushState({}, '', '/servers')
    setSelected(null)
  }
  return <section className="workspace-page">
    <div className="page-heading"><div><p className="eyebrow">Unified inventory</p><h2>服务器</h2><p>所有服务商同步后的本地统一清单。</p></div><span className="count-badge">{visible.length} 台</span></div>
    <div className="filter-bar"><label>搜索服务器<input aria-label="搜索服务器" value={query} onChange={(event) => setQuery(event.target.value)} placeholder="名称" /></label><label>服务商类型<select aria-label="服务商类型" value={providerType} onChange={(event) => { setProviderType(event.target.value); setConnectionID('all') }}><option value="all">全部</option>{providerTypes.map((type) => <option value={type} key={type}>{type}</option>)}</select></label><label>服务商连接<select aria-label="服务商连接" value={connectionID} onChange={(event) => setConnectionID(event.target.value)}><option value="all">全部</option>{connections.filter((connection) => providerType === 'all' || connection.provider_type === providerType).map((connection) => <option value={connection.id} key={connection.id}>{connection.name}</option>)}</select></label><label>运行状态<select aria-label="运行状态" value={state} onChange={(event) => setState(event.target.value)}><option value="all">全部</option><option value="running">运行中</option><option value="stopped">已关机</option><option value="pending">过渡中</option><option value="error">错误</option></select></label></div>
    {error && <p className="form-error" role="alert">{error}</p>}
    {loading ? <p className="empty-state">正在加载服务器…</p> : servers.length === 0 ? <p className="empty-state">还没有同步到服务器</p> : visible.length === 0 ? <p className="empty-state">没有符合筛选条件的服务器</p> : <div className="server-table" role="table">
      <div className="server-row server-head" role="row"><span>服务器</span><span>状态</span><span>范围</span><span>地址</span><span /></div>
      {visible.map((server) => <div className="server-row" role="row" key={server.id}><span><strong>{server.name}</strong><small>{connectionByID.get(server.connection_id)?.name ?? server.external_id}</small></span><span><span className={`state-chip ${server.state}`}>{stateLabels[server.state] ?? server.state}</span></span><span>{server.scope || '—'}</span><span>{server.addresses[0]?.address ?? '—'}</span><span><button aria-label={`查看 ${server.name}`} onClick={() => openDetail(server)}>查看详情</button></span></div>)}
    </div>}
    {selected && <ServerDetail server={selected} connection={connectionByID.get(selected.connection_id)} onClose={closeDetail} />}
  </section>
}

function ServerDetail({ server, connection, onClose }: { server: Server; connection?: ProviderConnection; onClose(): void }) {
  return <div className="detail-backdrop" onClick={onClose}><aside className="detail-panel" role="dialog" aria-label="服务器详情" onClick={(event) => event.stopPropagation()}>
    <div className="detail-header"><div><p className="eyebrow">Server detail</p><h3>{server.name}</h3><p>{server.external_id}</p></div><button aria-label="关闭详情" onClick={onClose}>×</button></div>
    <div className="detail-stats"><div><small>状态</small><strong>{stateLabels[server.state] ?? server.state}</strong></div><div><small>配置</small><strong>{server.spec.cpu ?? '—'} vCPU</strong><span>{server.spec.memory_mb ? `${server.spec.memory_mb} MB` : '—'}</span></div><div><small>服务商连接</small><strong>{connection?.name ?? '未知连接'}</strong><span>{connection?.provider_type ?? '—'}</span></div></div>
    <p className="sync-time">上次同步：{new Date(server.updated_at).toLocaleString()}</p>
    <section><h4>能力</h4><div className="capability-list">{server.capabilities.can_start?.available && <span>可开机</span>}{server.capabilities.can_stop?.available && <span>可关机</span>}{server.capabilities.can_reboot?.available && <span>可重启</span>}{server.capabilities.can_embed_console?.available && <span>可内嵌控制台</span>}{server.capabilities.can_open_console_window?.available && <span>可新窗口控制台</span>}{server.capabilities.has_provider_portal?.available && <span>服务商后台</span>}</div></section>
    <section><h4>网络地址</h4>{server.addresses.length ? server.addresses.map((address) => <p className="address-line" key={address.address}>{address.address}<small>{address.type ?? 'address'}</small></p>) : <p className="muted">没有地址信息</p>}</section>
    <section><h4>控制操作</h4><div className="future-actions"><button disabled>开机 · 里程碑 3</button><button disabled>关机 · 里程碑 3</button><button disabled>重启 · 里程碑 3</button><button disabled>控制台 · 里程碑 4</button></div></section>
  </aside></div>
}
