import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import './styles.css'
import './features.css'
import { App } from './app/App'
import { AuthProvider } from './auth/AuthContext'

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <AuthProvider><App /></AuthProvider>
  </StrictMode>,
)
