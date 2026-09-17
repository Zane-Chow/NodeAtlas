import { useState } from 'react'
import type { AuthUser } from '../auth/useAuth'

const navigation = ['总览', '服务器', '服务商', '操作记录', '备份与设置']

export function AppShell({ user, onLogout }: { user: AuthUser; onLogout(): Promise<void> }) {
  const [open, setOpen] = useState(false)
  return <div className="operator-layout">
    <aside className={open ? 'sidebar sidebar-open' : 'sidebar'}>
      <div className="sidebar-brand"><span className="brand-mark small">SC</span><strong>Server Control</strong></div>
      <nav aria-label="主导航">{navigation.map((label, index) => <button className={index === 0 ? 'nav-item active' : 'nav-item'} disabled={index !== 0} key={label}><span className="nav-dot" />{label}{index !== 0 && <span className="soon">即将推出</span>}</button>)}</nav>
      <div className="sidebar-footer"><span className="health-dot" /> 系统基础服务正常</div>
    </aside>
    <main className="main-panel">
      <header className="topbar">
        <button className="menu-button" aria-label="切换导航" onClick={() => setOpen(!open)}>☰</button>
        <div><p className="breadcrumb">控制台 / 总览</p><h1>基础环境已就绪</h1></div>
        <div className="user-menu"><span>{user.username.slice(0, 1).toUpperCase()}</span><div><strong>{user.username}</strong><small>唯一管理员</small></div><button onClick={() => void onLogout()}>退出</button></div>
      </header>
      <section className="content-grid">
        <article className="welcome-card"><p className="eyebrow">阶段 1 · 基础与认证</p><h2>安全底座已经建立</h2><p>下一步将添加 Mock 服务商、统一服务器清单与连接管理。</p><div className="progress-track"><span /></div></article>
        <article className="status-card"><span className="status-icon">DB</span><div><small>数据库</small><strong>已连接</strong><p>迁移版本为最新</p></div></article>
        <article className="status-card"><span className="status-icon">ID</span><div><small>身份认证</small><strong>已启用</strong><p>Session 与 CSRF 保护</p></div></article>
        <article className="next-card"><span className="step-number">02</span><div><small>下一里程碑</small><h3>添加第一个服务商连接</h3><p>Mock Provider 将先验证完整交互，再接入真实 API。</p></div></article>
      </section>
    </main>
  </div>
}
