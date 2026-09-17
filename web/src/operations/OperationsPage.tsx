import { useCallback, useEffect, useMemo, useState } from 'react'
import { useServerEvents } from '../events/useServerEvents'
import { listServers } from '../servers/api'
import type { Server } from '../servers/types'
import { listOperations } from './api'
import type { Operation } from './types'

const actionLabels = { start: '开机', stop: '关机', reboot: '重启' } as const
const statusLabels = {
  queued: '等待执行', running: '执行中', verifying: '验证中', succeeded: '成功',
  failed: '失败', timed_out: '已超时', cancelled: '已取消',
} as const

export function OperationsPage() {
  const [operations, setOperations] = useState<Operation[]>([])
  const [servers, setServers] = useState<Server[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')

  const refresh = useCallback(async () => {
    try {
      const [nextOperations, nextServers] = await Promise.all([listOperations(), listServers()])
      setOperations(nextOperations)
      setServers(nextServers)
      setError('')
    } catch {
      setError('无法加载操作记录')
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    void Promise.resolve().then(refresh)
  }, [refresh])
  useServerEvents(() => { void refresh() })

  const serverNames = useMemo(() => new Map(servers.map((server) => [server.id, server.name])), [servers])

  return <section className="workspace-page">
    <div className="page-heading">
      <div><p className="eyebrow">Operation history</p><h2>操作记录</h2><p>查看电源操作的执行、验证和失败信息。</p></div>
      <span className="count-badge">{operations.length} 条</span>
    </div>
    {error && <p className="form-error" role="alert">{error}</p>}
    {loading ? <p className="empty-state">正在加载操作记录…</p> : operations.length === 0 ? <p className="empty-state">还没有电源操作记录</p> : <div className="operation-list">
      {operations.map((operation) => <article className="operation-card" key={operation.id}>
        <div className="operation-main">
          <div><strong>{serverNames.get(operation.server_id) ?? operation.server_id}</strong><small>{operation.id}</small></div>
          <span className={`operation-status ${operation.status}`}>{statusLabels[operation.status]}</span>
        </div>
        <dl>
          <div><dt>操作</dt><dd>{actionLabels[operation.action]}</dd></div>
          <div><dt>排队时间</dt><dd>{new Date(operation.queued_at).toLocaleString()}</dd></div>
          <div><dt>更新时间</dt><dd>{new Date(operation.updated_at).toLocaleString()}</dd></div>
        </dl>
        {operation.error_message && <p className="operation-error">{operation.error_message}</p>}
      </article>)}
    </div>}
  </section>
}
