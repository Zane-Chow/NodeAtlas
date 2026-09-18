import { useEffect, useState } from 'react'
import type { AuthUser } from '../auth/useAuth'
import { BackupsPage } from '../backups/BackupsPage'
import { ConnectionsPage } from '../connections/ConnectionsPage'
import { OperationsPage } from '../operations/OperationsPage'
import { ServersPage } from '../servers/ServersPage'

type Page = 'dashboard' | 'servers' | 'connections' | 'operations' | 'settings'
const navigation: Array<{ label: string; page?: Page; path?: string }> = [
  { label: '总览', page: 'dashboard', path: '/' },
  { label: '服务器', page: 'servers', path: '/servers' },
  { label: '服务商', page: 'connections', path: '/connections' },
  { label: '操作记录', page: 'operations', path: '/operations' },
  { label: '备份与设置', page: 'settings', path: '/settings' },
]
const pageTitles: Record<Page, string> = { dashboard: '运行总览', servers: '统一服务器清单', connections: '服务商连接', operations: '操作记录', settings: '备份与设置' }

function pageFromPath(path: string): Page {
  if (path.startsWith('/servers')) return 'servers'
  if (path.startsWith('/connections')) return 'connections'
  if (path.startsWith('/operations')) return 'operations'
  if (path.startsWith('/settings')) return 'settings'
  return 'dashboard'
}

export function AppShell({ user, onLogout }: { user: AuthUser; onLogout(): Promise<void> }) {
  const [open, setOpen] = useState(false)
  const [page, setPage] = useState<Page>(() => pageFromPath(window.location.pathname))
  useEffect(() => {
    const onPopState = () => setPage(pageFromPath(window.location.pathname))
    window.addEventListener('popstate', onPopState)
    return () => window.removeEventListener('popstate', onPopState)
  }, [])
  function navigate(next: Page, path: string) {
    if (window.location.pathname !== path) window.history.pushState({}, '', path)
    setPage(next)
    setOpen(false)
  }
  return <div className="operator-layout">
    <aside className={open ? 'sidebar sidebar-open' : 'sidebar'}>
      <div className="sidebar-brand"><span className="brand-mark small">SC</span><strong>Server Control</strong></div>
      <nav aria-label="主导航">{navigation.map((item) => <button
        className={item.page === page ? 'nav-item active' : 'nav-item'} disabled={!item.page} key={item.label}
        onClick={() => item.page && navigate(item.page, item.path!)}
      ><span className="nav-dot" />{item.label}{!item.page && <span className="soon">即将推出</span>}</button>)}</nav>
      <div className="sidebar-footer"><span className="health-dot" /> 核心服务运行正常</div>
    </aside>
    <main className="main-panel">
      <header className="topbar">
        <button className="menu-button" aria-label="切换导航" onClick={() => setOpen(!open)}>☰</button>
        <div><p className="breadcrumb">控制台 / {navigation.find((item) => item.page === page)?.label}</p><h1>{pageTitles[page]}</h1></div>
        <div className="user-menu"><span>{user.username.slice(0, 1).toUpperCase()}</span><div><strong>{user.username}</strong><small>唯一管理员</small></div><button onClick={() => void onLogout()}>退出</button></div>
      </header>
      {page === 'dashboard' && <Dashboard />}
      {page === 'servers' && <ServersPage />}
      {page === 'connections' && <ConnectionsPage />}
      {page === 'operations' && <OperationsPage />}
      {page === 'settings' && <BackupsPage />}
    </main>
  </div>
}

function Dashboard() {
  return <section className="content-grid">
    <article className="welcome-card"><p className="eyebrow">阶段二 · 真实服务商接入</p><h2>统一管理多个服务商的服务器</h2><p>AWS EC2 与 VirtFusion 已接入真实 API，支持多账户清单、电源操作和安全控制台回退。</p><div className="progress-track"><span style={{ width: '100%' }} /></div></article>
    <article className="status-card"><span className="status-icon">PV</span><div><small>Provider 核心</small><strong>AWS 与 VirtFusion 已接入</strong><p>凭据隔离、跨区域同步与 noVNC 控制台</p></div></article>
    <article className="status-card"><span className="status-icon">BK</span><div><small>数据安全</small><strong>SQLite / MySQL 可迁移</strong><p>加密归档、校验、恢复与安全快照</p></div></article>
    <article className="next-card"><span className="step-number">03</span><div><small>下一阶段</small><h3>GCP 与 Virtualizor</h3><p>继续扩展统一 Provider 接口与真实服务商 API。</p></div></article>
  </section>
}
