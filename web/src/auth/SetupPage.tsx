import { useState, type FormEvent } from 'react'

export function SetupPage({ onInitialize }: { onInitialize(username: string, password: string): Promise<void> }) {
  const [pending, setPending] = useState(false)
  const [error, setError] = useState('')

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    const data = new FormData(event.currentTarget)
    const username = String(data.get('username') ?? '').trim()
    const password = String(data.get('password') ?? '')
    if (password !== String(data.get('confirmation') ?? '')) {
      setError('两次输入的密码不一致')
      return
    }
    setPending(true)
    setError('')
    try { await onInitialize(username, password) }
    catch { setError('无法创建管理员，请检查输入后重试') }
    finally { setPending(false) }
  }

  return <main className="auth-layout">
    <section className="auth-brand">
      <span className="eyebrow">SERVER CONTROL</span><h1>先建立你的控制中心</h1>
      <p>创建唯一管理员账号。完成后，初始化入口将永久关闭。</p>
      <div className="security-note"><span>01</span> 凭据留在你的部署环境中</div>
      <div className="security-note"><span>02</span> 支持 SQLite 与 MySQL</div>
    </section>
    <section className="auth-card" aria-labelledby="setup-title">
      <div className="brand-mark" aria-hidden="true">SC</div><p className="step-label">首次设置 · 1 / 1</p>
      <h2 id="setup-title">创建管理员</h2><p className="muted">此面板仅允许一个本地管理员。</p>
      <form onSubmit={submit}>
        <label htmlFor="setup-username">管理员用户名</label><input id="setup-username" name="username" autoComplete="username" required maxLength={255} />
        <label htmlFor="setup-password">密码</label><input id="setup-password" name="password" type="password" autoComplete="new-password" required minLength={12} />
        <label htmlFor="setup-confirmation">确认密码</label><input id="setup-confirmation" name="confirmation" type="password" autoComplete="new-password" required minLength={12} />
        {error && <p className="form-error" role="alert">{error}</p>}
        <button className="primary-button" disabled={pending} type="submit">{pending ? '正在创建…' : '创建管理员'}</button>
      </form>
    </section>
  </main>
}
