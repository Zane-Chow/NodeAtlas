import { useState, type FormEvent } from 'react'

export function LoginPage({ onLogin }: { onLogin(username: string, password: string): Promise<void> }) {
  const [pending, setPending] = useState(false)
  const [error, setError] = useState('')
  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    const data = new FormData(event.currentTarget)
    setPending(true); setError('')
    try { await onLogin(String(data.get('username') ?? ''), String(data.get('password') ?? '')) }
    catch { setError('用户名或密码错误') }
    finally { setPending(false) }
  }
  return <main className="auth-layout login-layout">
    <section className="auth-brand"><span className="eyebrow">SERVER CONTROL</span><h1>所有服务器，一处掌控</h1><p>跨云厂商和第三方面板查看状态、执行电源操作并打开远程控制台。</p></section>
    <section className="auth-card" aria-labelledby="login-title">
      <div className="brand-mark" aria-hidden="true">SC</div><p className="step-label">管理员访问</p><h2 id="login-title">欢迎回来</h2><p className="muted">登录以进入服务器控制面板。</p>
      <form onSubmit={submit}>
        <label htmlFor="login-username">用户名</label><input id="login-username" name="username" autoComplete="username" required />
        <label htmlFor="login-password">密码</label><input id="login-password" name="password" type="password" autoComplete="current-password" required />
        {error && <p className="form-error" role="alert">{error}</p>}
        <button className="primary-button" disabled={pending} type="submit">{pending ? '正在登录…' : '登录'}</button>
      </form>
    </section>
  </main>
}
