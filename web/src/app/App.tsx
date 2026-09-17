import { useAuth } from '../auth/useAuth'
import { LoginPage } from '../auth/LoginPage'
import { SetupPage } from '../auth/SetupPage'
import { AppShell } from './AppShell'

export function App() {
  const auth = useAuth()
  if (auth.state.status === 'loading') return <main className="loading-screen"><div className="loading-mark">SC</div><p>正在连接控制面板…</p></main>
  if (auth.state.status === 'setup-required') return <SetupPage onInitialize={auth.initialize} />
  if (auth.state.status === 'unauthenticated') return <LoginPage onLogin={auth.login} />
  return <AppShell user={auth.state.user} onLogout={auth.logout} />
}
