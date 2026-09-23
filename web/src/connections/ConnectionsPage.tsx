import { useEffect, useState, type FormEvent } from 'react'
import { createConnection, deleteConnection, listConnections, listProviderTypes, syncConnection, testConnection } from './api'
import type { ProviderConnection, ProviderType } from './types'

const healthLabels: Record<ProviderConnection['health_status'], string> = {
  unknown: '等待检测', healthy: '健康', degraded: '受限', offline: '离线',
}

export function ConnectionsPage() {
  const [connections, setConnections] = useState<ProviderConnection[]>([])
  const [providerTypes, setProviderTypes] = useState<ProviderType[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [showForm, setShowForm] = useState(false)
  const [notice, setNotice] = useState('')

  useEffect(() => {
    let active = true
    async function load() {
      try {
        const [types, items] = await Promise.all([listProviderTypes(), listConnections()])
        if (active) { setProviderTypes(types); setConnections(items) }
      } catch { if (active) setError('无法加载服务商连接') }
      finally { if (active) setLoading(false) }
    }
    void load()
    return () => { active = false }
  }, [])

  async function handleCreate(input: Parameters<typeof createConnection>[0]) {
    const created = await createConnection(input)
    setConnections((current) => [...current, created])
    setShowForm(false)
    setNotice('连接已保存，同步任务已排队')
  }

  async function handleTest(connection: ProviderConnection) {
    setNotice('')
    try {
      const result = await testConnection(connection.id)
      setNotice(result.healthy ? '连接测试成功' : `连接测试失败：${result.message}`)
      setConnections((current) => current.map((item) => item.id === connection.id
        ? { ...item, health_status: result.healthy ? 'healthy' : 'degraded' }
        : item))
    } catch { setNotice('连接测试失败') }
  }

  async function handleSync(connection: ProviderConnection) {
    setNotice('')
    try { await syncConnection(connection.id); setNotice('同步任务已排队') }
    catch { setNotice('无法创建同步任务') }
  }

  async function handleDelete(connection: ProviderConnection) {
    if (!window.confirm(`确定删除服务商连接“${connection.name}”吗？同步到本地的服务器也会从清单中移除。`)) return
    setNotice('')
    try {
      await deleteConnection(connection.id)
      setConnections((current) => current.filter((item) => item.id !== connection.id))
      setNotice('服务商连接已删除')
    } catch { setNotice('无法删除服务商连接') }
  }

  return <section className="workspace-page">
    <div className="page-heading"><div><p className="eyebrow">Provider connections</p><h2>服务商连接</h2><p>同一种服务商可以配置多个独立账户。</p></div><button className="primary-button compact" onClick={() => setShowForm(true)}>添加服务商</button></div>
    {notice && <p className="inline-notice" role="status">{notice}</p>}
    {error && <p className="form-error" role="alert">{error}</p>}
    {showForm && <ConnectionForm providerTypes={providerTypes} onCancel={() => setShowForm(false)} onSubmit={handleCreate} />}
    {loading ? <p className="empty-state">正在加载连接…</p> : connections.length === 0 ? <p className="empty-state">还没有服务商连接，请先添加服务商。</p> : <div className="connection-grid">
      {connections.map((connection) => <article className="connection-card" key={connection.id}>
        <div className="card-title"><div><span className="provider-badge">{connection.provider_type}</span><h3>{connection.name}</h3></div><span className={`health-chip ${connection.health_status}`}>{healthLabels[connection.health_status]}</span></div>
        <dl><div><dt>状态</dt><dd>{connection.enabled ? '已启用' : '已停用'}</dd></div><div><dt>上次同步</dt><dd>{connection.last_synced_at ? new Date(connection.last_synced_at).toLocaleString() : '尚未同步'}</dd></div></dl>
        <div className="card-actions"><button aria-label={`测试 ${connection.name}`} onClick={() => void handleTest(connection)}>测试连接</button><button aria-label={`同步 ${connection.name}`} disabled={!connection.enabled} onClick={() => void handleSync(connection)}>立即同步</button><button className="danger-button" aria-label={`删除 ${connection.name}`} onClick={() => void handleDelete(connection)}>删除</button></div>
      </article>)}
    </div>}
  </section>
}

function ConnectionForm({ providerTypes, onCancel, onSubmit }: {
  providerTypes: ProviderType[]
  onCancel(): void
  onSubmit(input: Parameters<typeof createConnection>[0]): Promise<void>
}) {
  const [providerType, setProviderType] = useState(() => providerTypes.find((item) => item.id === 'mock')?.id ?? providerTypes[0]?.id ?? 'mock')
  const [name, setName] = useState('')
  const [serverCount, setServerCount] = useState('4')
  const [token, setToken] = useState('')
  const [consoleProfile, setConsoleProfile] = useState('embedded')
  const [regions, setRegions] = useState('')
  const [accessKeyID, setAccessKeyID] = useState('')
  const [secretAccessKey, setSecretAccessKey] = useState('')
  const [sessionToken, setSessionToken] = useState('')
  const [virtFusionEndpoint, setVirtFusionEndpoint] = useState('')
  const [virtFusionToken, setVirtFusionToken] = useState('')
  const [virtFusionPageSize, setVirtFusionPageSize] = useState('200')
  const [gcpProjectID, setGCPProjectID] = useState('')
  const [gcpServiceAccountJSON, setGCPServiceAccountJSON] = useState('')
  const [virtualizorEndpoint, setVirtualizorEndpoint] = useState('')
  const [virtualizorAPIKey, setVirtualizorAPIKey] = useState('')
  const [virtualizorAPIPassword, setVirtualizorAPIPassword] = useState('')
  const [solusVM2Endpoint, setSolusVM2Endpoint] = useState('')
  const [solusVM2APIToken, setSolusVM2APIToken] = useState('')
  const [submitting, setSubmitting] = useState(false)
  const [error, setError] = useState('')
  async function submit(event: FormEvent) {
    event.preventDefault(); setSubmitting(true); setError('')
    try {
      if (providerType === 'aws') {
        await onSubmit({ name, provider_type: 'aws', endpoint: '', enabled: true,
          settings: { regions: regions.split(',').map((region) => region.trim()).filter(Boolean) },
          credentials: { access_key_id: accessKeyID, secret_access_key: secretAccessKey, ...(sessionToken.trim() ? { session_token: sessionToken } : {}) } })
      } else if (providerType === 'gcp') {
        let serviceAccount: unknown
        try { serviceAccount = JSON.parse(gcpServiceAccountJSON) }
        catch { setError('Service Account JSON 格式无效'); return }
        if (typeof serviceAccount !== 'object' || serviceAccount === null || Array.isArray(serviceAccount)) {
          setError('Service Account JSON 格式无效')
          return
        }
        await onSubmit({ name, provider_type: 'gcp', endpoint: '', enabled: true,
          settings: { project_id: gcpProjectID }, credentials: { service_account_json: serviceAccount } })
      } else if (providerType === 'virtfusion') {
        await onSubmit({ name, provider_type: 'virtfusion', endpoint: virtFusionEndpoint.trim(), enabled: true,
          settings: { page_size: Number(virtFusionPageSize) }, credentials: { token: virtFusionToken } })
      } else if (providerType === 'virtualizor') {
        await onSubmit({ name, provider_type: 'virtualizor', endpoint: virtualizorEndpoint.trim(), enabled: true,
          settings: {}, credentials: { api_key: virtualizorAPIKey, api_password: virtualizorAPIPassword } })
      } else if (providerType === 'solusvm2') {
        await onSubmit({ name, provider_type: 'solusvm2', endpoint: solusVM2Endpoint.trim(), enabled: true,
          settings: {}, credentials: { api_token: solusVM2APIToken } })
      } else {
        await onSubmit({ name, provider_type: 'mock', endpoint: '', enabled: true,
          settings: { server_count: Number(serverCount), console_profile: consoleProfile }, credentials: { token } })
      }
    } catch { setError('无法保存连接，请检查配置') }
    finally { setSubmitting(false) }
  }
  return <div className="modal-backdrop"><form className="modal-card" aria-label="添加服务商" onSubmit={(event) => void submit(event)}>
    <div><p className="eyebrow">New connection</p><h3>添加服务商连接</h3></div>
    <label htmlFor="provider-type">服务商类型</label><select id="provider-type" value={providerType} onChange={(event) => setProviderType(event.target.value)}>{providerTypes.map((item) => <option key={item.id} value={item.id}>{item.name}</option>)}</select>
    <label htmlFor="connection-name">连接名称</label><input id="connection-name" value={name} onChange={(event) => setName(event.target.value)} required />
    {providerType === 'aws' ? <>
      <label htmlFor="aws-regions">AWS Regions</label><input id="aws-regions" value={regions} onChange={(event) => setRegions(event.target.value)} placeholder="us-east-1, eu-west-1" required />
      <label htmlFor="aws-access-key">Access Key ID</label><input id="aws-access-key" value={accessKeyID} onChange={(event) => setAccessKeyID(event.target.value)} required autoComplete="off" />
      <label htmlFor="aws-secret-key">Secret Access Key</label><input id="aws-secret-key" type="password" value={secretAccessKey} onChange={(event) => setSecretAccessKey(event.target.value)} required autoComplete="new-password" />
      <label htmlFor="aws-session-token">Session Token（可选）</label><input id="aws-session-token" type="password" value={sessionToken} onChange={(event) => setSessionToken(event.target.value)} autoComplete="new-password" />
    </> : providerType === 'gcp' ? <>
      <label htmlFor="gcp-project-id">GCP Project ID</label><input id="gcp-project-id" value={gcpProjectID} onChange={(event) => setGCPProjectID(event.target.value)} required />
      <label htmlFor="gcp-service-account">Service Account JSON</label><textarea id="gcp-service-account" value={gcpServiceAccountJSON} onChange={(event) => setGCPServiceAccountJSON(event.target.value)} required autoComplete="new-password" />
    </> : providerType === 'virtfusion' ? <>
      <label htmlFor="virtfusion-endpoint">VirtFusion 面板地址</label><input id="virtfusion-endpoint" type="url" value={virtFusionEndpoint} onChange={(event) => setVirtFusionEndpoint(event.target.value)} placeholder="https://panel.example.com" required />
      <label htmlFor="virtfusion-page-size">每页服务器数</label><input id="virtfusion-page-size" type="number" min="1" max="200" value={virtFusionPageSize} onChange={(event) => setVirtFusionPageSize(event.target.value)} required />
      <label htmlFor="virtfusion-token">API Bearer Token</label><input id="virtfusion-token" type="password" value={virtFusionToken} onChange={(event) => setVirtFusionToken(event.target.value)} required autoComplete="new-password" />
      <p className="field-note">面板必须使用 HTTPS；私网地址需由管理员在服务端白名单中放行。</p>
    </> : providerType === 'virtualizor' ? <>
      <label htmlFor="virtualizor-endpoint">Virtualizor 面板地址</label><input id="virtualizor-endpoint" type="url" value={virtualizorEndpoint} onChange={(event) => setVirtualizorEndpoint(event.target.value)} placeholder="https://panel.example.com:4083" required />
      <label htmlFor="virtualizor-api-key">Virtualizor API Key</label><input id="virtualizor-api-key" type="password" value={virtualizorAPIKey} onChange={(event) => setVirtualizorAPIKey(event.target.value)} required autoComplete="new-password" />
      <label htmlFor="virtualizor-api-password">Virtualizor API Password</label><input id="virtualizor-api-password" type="password" value={virtualizorAPIPassword} onChange={(event) => setVirtualizorAPIPassword(event.target.value)} required autoComplete="new-password" />
      <p className="field-note">面板必须使用 HTTPS；私网地址需由管理员在服务端白名单中放行。</p>
    </> : providerType === 'solusvm2' ? <>
      <label htmlFor="solusvm2-endpoint">SolusVM 2 面板地址</label><input id="solusvm2-endpoint" type="url" value={solusVM2Endpoint} onChange={(event) => setSolusVM2Endpoint(event.target.value)} placeholder="https://panel.example.com" required />
      <label htmlFor="solusvm2-api-token">SolusVM 2 API Token</label><input id="solusvm2-api-token" type="password" value={solusVM2APIToken} onChange={(event) => setSolusVM2APIToken(event.target.value)} required autoComplete="new-password" />
      <p className="field-note">面板必须使用 HTTPS；私网地址需由管理员在服务端白名单中放行。</p>
    </> : <>
      <label htmlFor="server-count">服务器数量</label><input id="server-count" type="number" min="1" max="500" value={serverCount} onChange={(event) => setServerCount(event.target.value)} required />
      <label htmlFor="console-profile">控制台能力</label><select id="console-profile" value={consoleProfile} onChange={(event) => setConsoleProfile(event.target.value)}><option value="embedded">内嵌 + 新窗口</option><option value="window">仅新窗口</option><option value="portal">仅服务商后台</option><option value="none">不可用</option></select>
      <label htmlFor="mock-token">Mock Token</label><input id="mock-token" type="password" value={token} onChange={(event) => setToken(event.target.value)} required autoComplete="new-password" />
    </>}
    <p className="field-note">凭据加密保存，保存后不会再次显示。</p>{error && <p className="form-error" role="alert">{error}</p>}
    <div className="modal-actions"><button type="button" onClick={onCancel}>取消</button><button className="primary-button compact" disabled={submitting}>{submitting ? '保存中…' : '保存并同步'}</button></div>
  </form></div>
}
